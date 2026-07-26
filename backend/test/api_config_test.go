package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigCreateActivateAndDeleteRoundTrip(t *testing.T) {
	e := newEnv(t)
	tok := e.login()

	code, body := e.json("POST", "/api/config", tok,
		`{"name":"sub-a","content":`+quote(sampleConfig("GA"))+`}`)
	if code != 201 {
		t.Fatalf("create status = %d, want 201: %v", code, body)
	}
	cfg, _ := body["config"].(map[string]any)
	id, _ := cfg["id"].(string)
	if id == "" {
		t.Fatalf("create returned no id: %v", body)
	}
	if cfg["active"] == true {
		t.Fatal("create must not auto-activate")
	}
	if _, err := os.Stat(filepath.Join(e.ConfigDir, id+".yaml")); err != nil {
		t.Fatalf("raw file not written: %v", err)
	}
	if e.Kernel.reloadCount() != 0 {
		t.Fatal("create reloaded the kernel without being asked to activate")
	}

	code, body = e.json("POST", "/api/config/"+id+"/activate", tok, "")
	if code != 200 {
		t.Fatalf("activate status = %d: %v", code, body)
	}
	apply, _ := body["apply"].(map[string]any)
	if apply["ok"] != "1" {
		t.Fatalf("apply not ok: %v", apply)
	}
	if e.activeID() != id {
		t.Fatalf("settings.yaml active = %q, want %q", e.activeID(), id)
	}

	code, body = e.json("GET", "/api/config/list", tok, "")
	if code != 200 {
		t.Fatalf("list status = %d", code)
	}
	list, _ := body["configs"].([]any)
	if len(list) != 1 {
		t.Fatalf("list has %d configs, want 1", len(list))
	}

	code, _ = e.json("DELETE", "/api/config/"+id, tok, "")
	if code != 200 {
		t.Fatalf("delete status = %d", code)
	}
	if _, err := os.Stat(filepath.Join(e.ConfigDir, id+".yaml")); !os.IsNotExist(err) {
		t.Fatal("raw file survived the delete")
	}
	if e.activeID() != "" {
		t.Fatalf("active id lingers after deleting the only config: %q", e.activeID())
	}
}

func TestActivateUnknownConfigIs404(t *testing.T) {
	e := newEnv(t)
	code, _ := e.json("POST", "/api/config/does-not-exist/activate", e.login(), "")
	if code != 404 {
		t.Fatalf("status = %d, want 404", code)
	}
}

func TestConfigRawGetAndPut(t *testing.T) {
	e := newEnv(t)
	tok := e.login()
	cfg := e.addConfig("c", sampleConfig("G1"))

	code, body := e.json("GET", "/api/config/"+cfg.ID+"/raw", tok, "")
	if code != 200 {
		t.Fatalf("raw get status = %d", code)
	}
	if content, _ := body["content"].(string); content == "" {
		t.Fatalf("raw get returned no content: %v", body)
	}

	code, _ = e.json("PUT", "/api/config/"+cfg.ID+"/raw", tok,
		`{"content":`+quote(sampleConfig("G-EDITED"))+`}`)
	if code != 200 {
		t.Fatalf("raw put status = %d", code)
	}
	raw, err := os.ReadFile(filepath.Join(e.ConfigDir, cfg.ID+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(raw), "G-EDITED") {
		t.Fatal("edited content was not persisted")
	}

	// Empty content is rejected rather than silently wiping the file.
	code, _ = e.json("PUT", "/api/config/"+cfg.ID+"/raw", tok, `{"content":"   "}`)
	if code != 400 {
		t.Fatalf("empty content status = %d, want 400", code)
	}
}

func TestRuntimeConfigGetAndPut(t *testing.T) {
	e := newEnv(t)
	tok := e.login()
	if _, err := e.Svc.ApplyActive(); err != nil {
		t.Fatal(err)
	}

	code, body := e.json("GET", "/api/runtime", tok, "")
	if code != 200 {
		t.Fatalf("runtime get status = %d", code)
	}
	if content, _ := body["content"].(string); content == "" {
		t.Fatal("runtime get returned empty content")
	}

	code, body = e.json("PUT", "/api/runtime", tok, `{"content":"mode: direct\n","reload":true}`)
	if code != 200 {
		t.Fatalf("runtime put status = %d: %v", code, body)
	}
	if body["ok"] != "1" {
		t.Fatalf("runtime put not ok: %v", body)
	}
	if got := e.readConfigYAML()["mode"]; got != "direct" {
		t.Fatalf("mode = %v, want direct", got)
	}

	// Invalid YAML must be refused before anything reaches disk.
	code, _ = e.json("PUT", "/api/runtime", tok, `{"content":"a:\n- b\n  c: d\n"}`)
	if code != 400 {
		t.Fatalf("invalid yaml status = %d, want 400", code)
	}
	if got := e.readConfigYAML()["mode"]; got != "direct" {
		t.Fatal("a rejected write still changed config.yaml")
	}
}

// A failed reload leaves valid content on disk, and the response says so with
// ok:"0" rather than a transport-level error.
func TestRuntimePutReportsReloadFailureWithoutLosingContent(t *testing.T) {
	e := newEnv(t)
	tok := e.login()
	e.Kernel.mu.Lock()
	e.Kernel.failReloa = true
	e.Kernel.mu.Unlock()

	code, body := e.json("PUT", "/api/runtime", tok, `{"content":"mode: global\n","reload":true}`)
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["ok"] != "0" {
		t.Fatalf("expected ok:0 for a failed reload, got %v", body)
	}
	if got := e.readConfigYAML()["mode"]; got != "global" {
		t.Fatalf("content lost after failed reload: %v", got)
	}
}

// Toggling mode must reach the kernel AND be mirrored into config.yaml, or the
// choice is lost on the next config switch.
func TestRuntimeTogglesPushToKernelAndPersist(t *testing.T) {
	e := newEnv(t)
	tok := e.login()
	cfg := e.addConfig("c", sampleConfig("G1"))
	if _, _, err := e.Svc.Activate(cfg.ID, false); err != nil {
		t.Fatal(err)
	}

	code, _ := e.json("POST", "/api/mode", tok, `{"mode":"global"}`)
	if code != 200 {
		t.Fatalf("mode status = %d", code)
	}
	if p := e.Kernel.lastPatch(); p == nil || p["mode"] != "global" {
		t.Fatalf("kernel patch = %v", p)
	}
	if got := e.readConfigYAML()["mode"]; got != "global" {
		t.Fatalf("config.yaml mode = %v", got)
	}
	if e.Store.Prefs().Mode != "global" {
		t.Fatalf("store prefs mode = %q", e.Store.Prefs().Mode)
	}

	code, _ = e.json("POST", "/api/log-level", tok, `{"level":"warning"}`)
	if code != 200 {
		t.Fatalf("log-level status = %d", code)
	}
	if got := e.readConfigYAML()["log-level"]; got != "warning" {
		t.Fatalf("config.yaml log-level = %v", got)
	}

	code, _ = e.json("POST", "/api/tun", tok, `{"enable":true}`)
	if code != 200 {
		t.Fatalf("tun status = %d", code)
	}
	tun, _ := e.readConfigYAML()["tun"].(map[string]any)
	if tun == nil || tun["enable"] != true {
		t.Fatalf("config.yaml tun = %v", tun)
	}
	if !e.Store.Prefs().TunEnable {
		t.Fatal("store prefs tun not set")
	}
}

// A setting the store rejected must not be mirrored as if it had been accepted.
func TestRuntimeTogglesValidateInput(t *testing.T) {
	e := newEnv(t)
	tok := e.login()

	for _, tc := range []struct{ path, body string }{
		{"/api/mode", `{"mode":"sideways"}`},
		{"/api/log-level", `{"level":"loud"}`},
		{"/api/tun", `{}`},
	} {
		code, _ := e.json("POST", tc.path, tok, tc.body)
		if code != 400 {
			t.Fatalf("%s %s: status = %d, want 400", tc.path, tc.body, code)
		}
	}
	if e.Kernel.lastPatch() != nil {
		t.Fatalf("an invalid request still reached the kernel: %v", e.Kernel.lastPatch())
	}
}

// Switching between configs must leave the kernel running exactly the winner.
func TestSwitchingConfigsReplacesGroupsWholesale(t *testing.T) {
	e := newEnv(t)
	tok := e.login()
	a := e.addConfig("a", sampleConfig("ALPHA"))
	b := e.addConfig("b", sampleConfig("BETA"))

	if code, _ := e.json("POST", "/api/config/"+a.ID+"/activate", tok, ""); code != 200 {
		t.Fatalf("activate a = %d", code)
	}
	if !hasGroup(e.readConfigYAML(), "ALPHA") {
		t.Fatal("config.yaml missing ALPHA")
	}
	if code, _ := e.json("POST", "/api/config/"+b.ID+"/activate", tok, ""); code != 200 {
		t.Fatalf("activate b = %d", code)
	}
	doc := e.readConfigYAML()
	if !hasGroup(doc, "BETA") {
		t.Fatal("config.yaml missing BETA after switch")
	}
	if hasGroup(doc, "ALPHA") {
		t.Fatal("the previous config's group survived the switch")
	}
}

func TestMethodNotAllowedOnKnownRoutes(t *testing.T) {
	e := newEnv(t)
	tok := e.login()
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/config/list"},
		{"GET", "/api/config"},
		{"GET", "/api/mode"},
		{"GET", "/api/tun"},
		{"POST", "/api/traffic"},
		{"POST", "/api/logs"},
	} {
		code, _ := e.json(tc.method, tc.path, tok, "")
		if code != 405 {
			t.Fatalf("%s %s: status = %d, want 405", tc.method, tc.path, code)
		}
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

func hasGroup(doc map[string]any, name string) bool {
	groups, _ := doc["proxy-groups"].([]any)
	for _, g := range groups {
		m, _ := g.(map[string]any)
		if m != nil && m["name"] == name {
			return true
		}
	}
	return false
}
