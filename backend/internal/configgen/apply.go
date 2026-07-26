package configgen

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/xin/mihomo-ui/internal/store"
	"gopkg.in/yaml.v3"
)

// InstallOptions controls the final merge:
//
//	config.yaml = config ⊕ override.yaml ⊕ settings ⊕ forced
//
// forced = secret + external-controller (env) + profile.store-selected/store-fake-ip (code).
type InstallOptions struct {
	OverridePath string  // ui/override.yaml - operator baseline, overrides subscription
	ConfigDir    string  // ui/config
	Secret       string  // kernel API credential - always last
	UI           UIState // panel: mode / log-level / tun.enable

	// KernelAPI is the listen address forced onto external-controller, so the
	// panel always knows where to reach the kernel it started. The kernel's
	// proxy inlet (mixed-port) is NOT pinned: it comes from the subscription or
	// override.yaml, since the operator chose to stop owning that port.
	KernelAPI string
}

// SplitListen turns a host:port listen address into mihomo's two separate
// fields. The kernel takes external-controller as host:port but its proxy inlet
// as a bare mixed-port int plus a separate bind-address; accepting one format
// everywhere and converting here keeps that split out of the user's config.
func SplitListen(addr string) (host string, port int, err error) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("listen address %q: %w", addr, err)
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 0 || n > 65535 {
		return "", 0, fmt.Errorf("listen address %q: bad port", addr)
	}
	if h == "" {
		h = "*" // mihomo spells "every interface" this way
	}
	return h, n, nil
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// mergeYAML deep-merges overlay onto base (overlay wins). Nested maps recurse.
func mergeYAML(base, overlay map[string]any) map[string]any {
	result := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range overlay {
		if m1, ok := result[k].(map[string]any); ok {
			if m2, ok := v.(map[string]any); ok {
				result[k] = mergeYAML(m1, m2)
				continue
			}
		}
		result[k] = v
	}
	return result
}

func loadYAMLFile(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

func writeYAMLFile(path string, doc map[string]any) error {
	out, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return WriteConfigFile(path, out)
}

// WriteConfigFile stages then renames, so no reader sees a half-written file.
func WriteConfigFile(path string, content []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// downloadHTTPClient routes subscription downloads through the standard
// HTTP_PROXY / HTTPS_PROXY / NO_PROXY env vars (via http.ProxyFromEnvironment).
// The panel never proxies on the operator's behalf: no proxy env set means a
// direct download. downloadBytes retries direct on failure, so a wrong proxy
// only costs one failed request.
func downloadHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   60 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
	}
}

func downloadBytes(rawURL string) ([]byte, error) {
	try := func(client *http.Client) ([]byte, error) {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "clash.meta/mihomo-ui")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return nil, fmt.Errorf("%s (%s)", resp.Status, string(b))
		}
		return io.ReadAll(resp.Body)
	}
	raw, err := try(downloadHTTPClient())
	if err != nil {
		direct := &http.Client{Timeout: 45 * time.Second}
		raw, err = try(direct)
		if err != nil {
			return nil, err
		}
	}
	return raw, nil
}

// LocalConfigPath is ui/config/<id>.yaml
func LocalConfigPath(configDir, id string) string {
	return filepath.Join(configDir, id+".yaml")
}

// ReadLocalConfigRaw returns the original bytes of a stored config file.
func ReadLocalConfigRaw(configDir, id string) ([]byte, error) {
	raw, err := os.ReadFile(LocalConfigPath(configDir, id))
	if err != nil {
		return nil, fmt.Errorf("本地原始配置不存在: %w", err)
	}
	return raw, nil
}

// HasLocalConfig reports whether a raw config file exists.
func HasLocalConfig(configDir, id string) bool {
	_, err := os.Stat(LocalConfigPath(configDir, id))
	return err == nil
}

// SaveLocalConfig writes config content for a config id as original bytes.
func SaveLocalConfig(configDir, id string, content []byte) error {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return fmt.Errorf("不是合法 YAML: %w", err)
	}
	if doc == nil {
		return fmt.Errorf("YAML 为空")
	}
	// Stage then rename, so a concurrent install reads either the old or the new
	// file and never a half-written one. This is what lets downloads run without
	// applyMu. The staging name must be unique per writer: two refreshes of the
	// same id sharing one tmp file would truncate each other and publish a mix.
	path := LocalConfigPath(configDir, id)
	f, err := os.CreateTemp(configDir, id+".yaml.tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename below succeeds
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// CreateTemp makes it 0600; match the 0644 the rest of the tree uses.
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DeleteLocalConfig removes stored raw config file.
func DeleteLocalConfig(configDir, id string) {
	_ = os.Remove(LocalConfigPath(configDir, id))
}

// FetchAndSaveConfig downloads a URL config into ui/config/<id>.yaml.
func FetchAndSaveConfig(configDir string, cfg store.Config) ([]byte, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("无订阅 URL")
	}
	raw, err := downloadBytes(cfg.URL)
	if err != nil {
		return nil, err
	}
	if err := SaveLocalConfig(configDir, cfg.ID, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// loadConfigDoc loads the config document from ui/config.
// forceRefresh=true re-downloads URL sources into the raw file first.
func loadConfigDoc(configDir string, cfg store.Config, forceRefresh bool) (map[string]any, error) {
	var raw []byte
	var err error

	if forceRefresh && cfg.Source != "file" && cfg.URL != "" {
		raw, err = FetchAndSaveConfig(configDir, cfg)
		if err != nil {
			return nil, err
		}
	} else if HasLocalConfig(configDir, cfg.ID) {
		raw, err = ReadLocalConfigRaw(configDir, cfg.ID)
		if err != nil {
			return nil, err
		}
	} else if cfg.Source != "file" && cfg.URL != "" {
		raw, err = FetchAndSaveConfig(configDir, cfg)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("本地原始配置不存在")
	}

	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse config yaml: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

type ApplyResult struct {
	OK       int
	Failed   []string
	Warnings []string
}

// EnsureConfig ensures raw config bytes exist (download if URL + force/missing).
func EnsureConfig(configDir string, cfg store.Config, forceRefresh bool) (*ApplyResult, error) {
	result := &ApplyResult{}
	if _, err := loadConfigDoc(configDir, cfg, forceRefresh); err != nil {
		result.Failed = append(result.Failed, fmt.Sprintf("%s: %v", cfg.Name, err))
		return result, err
	}
	result.OK = 1
	return result, nil
}

// InstallActive merges config ⊕ override ⊕ settings ⊕ forced -> config.yaml.
// Does not re-download unless raw is missing (then one lazy fetch for URL configs).
func InstallActive(configPath string, cfg store.Config, opts InstallOptions) (*ApplyResult, error) {
	result := &ApplyResult{}
	cfgDoc, err := loadConfigDoc(opts.ConfigDir, cfg, false)
	if err != nil {
		result.Failed = append(result.Failed, fmt.Sprintf("%s: %v", cfg.Name, err))
		return result, err
	}
	if err := writeMergedConfig(configPath, cfgDoc, opts); err != nil {
		result.Failed = append(result.Failed, err.Error())
		return result, err
	}
	result.OK = 1
	return result, nil
}

// InstallEmpty writes override ⊕ empty proxies ⊕ settings ⊕ forced (no active config).
func InstallEmpty(configPath string, opts InstallOptions) error {
	empty := map[string]any{
		"proxies":         []any{},
		"proxy-providers": map[string]any{},
		"proxy-groups":    []any{},
		"rules":           []any{"MATCH,DIRECT"},
	}
	return writeMergedConfig(configPath, empty, opts)
}

func writeMergedConfig(configPath string, cfgDoc map[string]any, opts InstallOptions) error {
	override := map[string]any{}
	if opts.OverridePath != "" {
		if b, err := loadYAMLFile(opts.OverridePath); err == nil {
			override = b
		} else if !os.IsNotExist(err) {
			if cur, cerr := loadYAMLFile(configPath); cerr == nil {
				override = cur
			}
		}
	} else if cur, err := loadYAMLFile(configPath); err == nil {
		override = cur
	}

	// override.yaml wins over the subscription: the operator's baseline is not
	// something a remote subscription should be able to rewrite.
	root := mergeYAML(cfgDoc, override)
	opts.UI.Overlay(root)
	// Forced values - neither override.yaml nor a subscription can touch these.
	// The control API address and per-boot secret keep the kernel pinned where
	// the panel can reach it; store-selected / store-fake-ip are load-bearing for
	// the panel UX (node selection survives restart).
	if opts.KernelAPI != "" {
		root["external-controller"] = opts.KernelAPI
	}
	prof := asMap(root["profile"])
	if prof == nil {
		prof = map[string]any{}
	} else {
		prof = cloneMap(prof)
	}
	prof["store-selected"] = true
	prof["store-fake-ip"] = true
	root["profile"] = prof
	if opts.Secret != "" {
		root["secret"] = opts.Secret
	}
	// ensure cors block for browser panel if missing
	if _, ok := root["external-controller-cors"]; !ok {
		root["external-controller-cors"] = map[string]any{
			"allow-origins":         []any{"*"},
			"allow-private-network": true,
		}
	}
	return writeYAMLFile(configPath, root)
}

// ApplyConfigs installs the active config (or empty shell).
func ApplyConfigs(configPath string, cfgs []store.Config, opts InstallOptions) error {
	_, err := ApplyConfigsDetailed(configPath, cfgs, false, opts)
	return err
}

// ApplyConfigsDetailed installs the active config.
// forceRefresh re-downloads URL raw before install. Providers stay with the kernel.
func ApplyConfigsDetailed(configPath string, cfgs []store.Config, forceRefresh bool, opts InstallOptions) (*ApplyResult, error) {
	result := &ApplyResult{}
	var active *store.Config
	for i := range cfgs {
		if cfgs[i].Active {
			active = &cfgs[i]
			break
		}
	}
	if active == nil {
		if err := InstallEmpty(configPath, opts); err != nil {
			result.Failed = append(result.Failed, err.Error())
			return result, err
		}
		result.OK = 1
		return result, nil
	}
	if forceRefresh {
		br, err := EnsureConfig(opts.ConfigDir, *active, true)
		if br != nil {
			result.Warnings = append(result.Warnings, br.Warnings...)
			result.Failed = append(result.Failed, br.Failed...)
		}
		if err != nil {
			return result, err
		}
	}
	ir, err := InstallActive(configPath, *active, opts)
	if ir != nil {
		result.OK = ir.OK
		result.Failed = append(result.Failed, ir.Failed...)
		result.Warnings = append(result.Warnings, ir.Warnings...)
	}
	return result, err
}

// ValidateYAML ensures content is parseable YAML mapping (mihomo config).
func ValidateYAML(raw []byte) error {
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("YAML 无效: %w", err)
	}
	if doc == nil {
		return fmt.Errorf("YAML 为空")
	}
	return nil
}

// PatchYAMLFile shallow-merges top-level keys into an existing YAML file.
// Nested maps (e.g. tun) are merged one level deep.
func PatchYAMLFile(path string, patch map[string]any) error {
	root, err := loadYAMLFile(path)
	if err != nil {
		return err
	}
	for k, v := range patch {
		if vm, ok := v.(map[string]any); ok {
			if cur := asMap(root[k]); cur != nil {
				for ck, cv := range vm {
					cur[ck] = cv
				}
				root[k] = cur
				continue
			}
		}
		root[k] = v
	}
	return writeYAMLFile(path, root)
}
