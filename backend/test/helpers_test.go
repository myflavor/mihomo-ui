// Package test holds black-box tests: it can only reach the exported surface,
// which is deliberate — it exercises the packages the way the app wires them.
package test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xin/mihomo-ui/internal/api"
	"github.com/xin/mihomo-ui/internal/configgen"
	"github.com/xin/mihomo-ui/internal/configsvc"
	"github.com/xin/mihomo-ui/internal/mihomo"
	"github.com/xin/mihomo-ui/internal/store"
	"gopkg.in/yaml.v3"
)

// fakeKernel stands in for mihomo's control API. It records what the panel asks
// of it so tests can assert on the interaction, not just on the HTTP response.
type fakeKernel struct {
	*httptest.Server

	mu        sync.Mutex
	reloads   []string // path of every /configs?force=true
	patches   []map[string]any
	configs   map[string]any
	failReloa bool
	reloadHup chan struct{} // when non-nil, reload blocks until this closes
}

func newFakeKernel(t *testing.T) *fakeKernel {
	t.Helper()
	k := &fakeKernel{
		configs: map[string]any{"mode": "rule", "log-level": "info", "mixed-port": 7890},
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"version": "v1.0.0-test", "meta": true})
	})
	mux.HandleFunc("/configs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			k.mu.Lock()
			out := map[string]any{}
			for key, v := range k.configs {
				out[key] = v
			}
			k.mu.Unlock()
			writeJSON(w, out)
		case http.MethodPatch:
			var patch map[string]any
			_ = json.NewDecoder(r.Body).Decode(&patch)
			k.mu.Lock()
			k.patches = append(k.patches, patch)
			for key, v := range patch {
				k.configs[key] = v
			}
			k.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPut: // reload
			var body struct {
				Path string `json:"path"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			k.mu.Lock()
			hup, fail := k.reloadHup, k.failReloa
			k.reloads = append(k.reloads, body.Path)
			k.mu.Unlock()
			if hup != nil {
				<-hup
			}
			if fail {
				http.Error(w, "reload refused", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("/proxies", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"proxies": map[string]any{
			"GLOBAL": map[string]any{"type": "Selector", "name": "GLOBAL", "now": "DIRECT", "all": []any{"DIRECT", "node-a"}},
			"DIRECT": map[string]any{"type": "Direct", "name": "DIRECT"},
			"node-a": map[string]any{"type": "Shadowsocks", "name": "node-a", "alive": true,
				"history": []any{map[string]any{"delay": 120}}},
		}})
	})
	mux.HandleFunc("/connections", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, map[string]any{
			"uploadTotal": 100, "downloadTotal": 200, "memory": 4096,
			"connections": []any{map[string]any{
				"id": "c1", "upload": 10, "download": 20,
				"start":  time.Now().Format(time.RFC3339),
				"chains": []any{"node-a", "GLOBAL"},
				"rule":   "Match", "rulePayload": "",
				"metadata": map[string]any{
					"host": "example.com", "destinationPort": "443",
					"sourceIP": "127.0.0.1", "network": "tcp", "type": "HTTP",
				},
			}},
		})
	})
	mux.HandleFunc("/rules", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"rules": []any{map[string]any{"type": "MATCH", "payload": "", "proxy": "DIRECT"}}})
	})
	mux.HandleFunc("/traffic", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fl, _ := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, `{"up":%d,"down":%d}`+"\n", i, i*2)
			if fl != nil {
				fl.Flush()
			}
		}
	})
	mux.HandleFunc("/logs", func(w http.ResponseWriter, r *http.Request) {
		fl, _ := w.(http.Flusher)
		fmt.Fprintf(w, `{"type":"info","payload":"level=%s"}`+"\n", r.URL.Query().Get("level"))
		if fl != nil {
			fl.Flush()
		}
	})
	mux.HandleFunc("/providers/proxies", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"providers": map[string]any{}})
	})

	k.Server = httptest.NewServer(mux)
	t.Cleanup(k.Close)
	return k
}

func (k *fakeKernel) reloadCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.reloads)
}

func (k *fakeKernel) lastPatch() map[string]any {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.patches) == 0 {
		return nil
	}
	return k.patches[len(k.patches)-1]
}

// blockReloads makes every reload hang until the returned func is called, so a
// test can hold the service's lock and observe what still responds.
func (k *fakeKernel) blockReloads() func() {
	hup := make(chan struct{})
	k.mu.Lock()
	k.reloadHup = hup
	k.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			k.mu.Lock()
			k.reloadHup = nil
			k.mu.Unlock()
			close(hup)
		})
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// env is a fully wired panel over a temp DATA_HOME and a fake kernel.
type env struct {
	t          *testing.T
	Dir        string
	ConfigPath string // mihomo/config.yaml
	BasePath   string // ui/base.yaml
	ConfigDir  string // ui/config
	Store      *store.Store
	Kernel     *fakeKernel
	Svc        *configsvc.Service
	Server     *api.Server
	HTTP       *httptest.Server
}

const testPassword = "test-pw"

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	mihomoDir := filepath.Join(dir, "mihomo")
	uiDir := filepath.Join(dir, "ui")
	configDir := filepath.Join(uiDir, "config")
	for _, d := range []string{mihomoDir, configDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	basePath := filepath.Join(uiDir, "base.yaml")
	if err := configgen.EnsureBase(basePath); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(filepath.Join(uiDir, "settings.yaml"), store.UIPrefs{
		Mode: "rule", LogLevel: "info",
	})
	if err != nil {
		t.Fatal(err)
	}
	k := newFakeKernel(t)
	client := mihomo.NewClient(k.URL, "test-secret")
	configPath := filepath.Join(mihomoDir, "config.yaml")

	svc := &configsvc.Service{
		ConfigPath: configPath,
		BasePath:   basePath,
		ConfigDir:  configDir,
		Secret:     "test-secret",
		KernelAPI:  "127.0.0.1:9090",
		Store:      st,
		Kernel:     client,
	}
	srv := &api.Server{
		Mihomo:     client,
		Store:      st,
		UIPassword: testPassword,
		ConfigPath: configPath,
		ConfigDir:  configDir,
		Config:     svc,
	}
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	return &env{
		t: t, Dir: dir, ConfigPath: configPath, BasePath: basePath, ConfigDir: configDir,
		Store: st, Kernel: k, Svc: svc, Server: srv, HTTP: ts,
	}
}

// login returns a session token from the real login endpoint.
func (e *env) login() string {
	e.t.Helper()
	res := e.do("POST", "/api/login", "", `{"password":"`+testPassword+`"}`)
	defer res.Body.Close()
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		e.t.Fatalf("login decode: %v", err)
	}
	if body.Token == "" {
		e.t.Fatal("login returned no token")
	}
	return body.Token
}

// do issues a request; token may be empty for the unauthenticated case.
func (e *env) do(method, path, token, body string) *http.Response {
	e.t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	var req *http.Request
	var err error
	if rdr != nil {
		req, err = http.NewRequest(method, e.HTTP.URL+path, rdr)
	} else {
		req, err = http.NewRequest(method, e.HTTP.URL+path, nil)
	}
	if err != nil {
		e.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := e.HTTP.Client().Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	return res
}

// json issues an authenticated request and decodes the body.
func (e *env) json(method, path, token, body string) (int, map[string]any) {
	e.t.Helper()
	res := e.do(method, path, token, body)
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// addConfig stores a config with inline YAML content, like the create handler.
func (e *env) addConfig(name, content string) store.Config {
	e.t.Helper()
	cfg, err := e.Store.Add(name, "", "file", 0)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.Svc.SaveRaw(cfg, []byte(content)); err != nil {
		e.t.Fatal(err)
	}
	return cfg
}

func (e *env) readConfigYAML() map[string]any {
	e.t.Helper()
	raw, err := os.ReadFile(e.ConfigPath)
	if err != nil {
		e.t.Fatalf("read config.yaml: %v", err)
	}
	var doc map[string]any
	if err := yamlUnmarshal(raw, &doc); err != nil {
		e.t.Fatalf("parse config.yaml: %v", err)
	}
	return doc
}

// activeID reads the persisted active id straight off disk, so assertions do
// not go through the same in-memory Store they are checking.
func (e *env) activeID() string {
	e.t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.Dir, "ui", "settings.yaml"))
	if err != nil {
		e.t.Fatal(err)
	}
	var doc struct {
		ConfigID string `yaml:"configId"`
	}
	if err := yamlUnmarshal(raw, &doc); err != nil {
		e.t.Fatal(err)
	}
	return doc.ConfigID
}

// sampleConfig is a minimal but structurally complete subscription.
func sampleConfig(groupName string) string {
	return "proxies:\n" +
		"  - {name: n1, type: socks5, server: 127.0.0.1, port: 1080}\n" +
		"proxy-groups:\n" +
		"  - {name: " + groupName + ", type: select, proxies: [n1, DIRECT]}\n" +
		"rules:\n" +
		"  - MATCH," + groupName + "\n"
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// counter is a tiny atomic helper for concurrency assertions.
type counter struct{ n atomic.Int64 }

func (c *counter) inc()       { c.n.Add(1) }
func (c *counter) get() int64 { return c.n.Load() }

func yamlUnmarshal(b []byte, out any) error { return yaml.Unmarshal(b, out) }
