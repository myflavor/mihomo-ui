package configgen

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xin/mihomo-ui/internal/store"
	"gopkg.in/yaml.v3"
)

// InstallOptions controls the final merge:
//
//	config.yaml = ui/base.yaml ⊕ config ⊕ settings ⊕ secret(env)
type InstallOptions struct {
	BasePath  string  // ui/base.yaml
	ConfigDir string  // ui/config
	Secret    string  // MIHOMO_SECRET — always last
	UI        UIState // panel: mode / log-level / tun.enable
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
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// downloadHTTPClient prefers MIHOMO_PROXY / HTTP_PROXY, else local mixed-port.
func downloadHTTPClient() *http.Client {
	proxy := strings.TrimSpace(os.Getenv("MIHOMO_PROXY"))
	if proxy == "" {
		proxy = strings.TrimSpace(os.Getenv("HTTP_PROXY"))
	}
	if proxy == "" {
		proxy = strings.TrimSpace(os.Getenv("http_proxy"))
	}
	if proxy == "" {
		proxy = "http://127.0.0.1:7890"
	}
	transport := &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			if proxy != "" && proxy != "direct" {
				return url.Parse(proxy)
			}
			return nil, nil
		},
	}
	return &http.Client{Timeout: 60 * time.Second, Transport: transport}
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
	return os.WriteFile(LocalConfigPath(configDir, id), content, 0o644)
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

// InstallActive merges base ⊕ config raw ⊕ settings ⊕ secret → config.yaml.
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

// InstallEmpty writes base ⊕ empty proxies ⊕ settings ⊕ secret (no active config).
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
	base := map[string]any{}
	if opts.BasePath != "" {
		if b, err := loadYAMLFile(opts.BasePath); err == nil {
			base = b
		} else if !os.IsNotExist(err) {
			if cur, cerr := loadYAMLFile(configPath); cerr == nil {
				base = cur
			}
		}
	} else if cur, err := loadYAMLFile(configPath); err == nil {
		base = cur
	}

	root := mergeYAML(base, cfgDoc)
	opts.UI.Overlay(root)
	// keep control API on loopback + force secret from env (not stored in base)
	root["external-controller"] = "127.0.0.1:9090"
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
