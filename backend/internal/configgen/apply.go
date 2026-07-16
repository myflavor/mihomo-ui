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
//	config.yaml = base.yml ⊕ sub ⊕ ui-state ⊕ secret(env)
type InstallOptions struct {
	BasePath string   // simple local base (ports / TUN skeleton / DNS / API)
	Secret   string   // MIHOMO_SECRET — always last
	UI       UIState  // panel: mode / log-level / tun.enable
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
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

// LocalSubPath is the on-disk original YAML for a subscription.
// Layout: <configDir>/subs/<id>.yaml
func LocalSubPath(basePath, id string) string {
	return filepath.Join(filepath.Dir(basePath), "subs", id+".yaml")
}

// ReadLocalSubRaw returns the original bytes of a stored subscription file.
func ReadLocalSubRaw(basePath, id string) ([]byte, error) {
	raw, err := os.ReadFile(LocalSubPath(basePath, id))
	if err != nil {
		return nil, fmt.Errorf("本地原始配置不存在: %w", err)
	}
	return raw, nil
}

// HasLocalSub reports whether a raw subscription file exists.
func HasLocalSub(basePath, id string) bool {
	_, err := os.Stat(LocalSubPath(basePath, id))
	return err == nil
}

// SaveLocalSub writes subscription content for a sub id as original bytes.
func SaveLocalSub(basePath, id string, content []byte) error {
	dir := filepath.Join(filepath.Dir(basePath), "subs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return fmt.Errorf("不是合法 YAML: %w", err)
	}
	if doc == nil {
		return fmt.Errorf("YAML 为空")
	}
	return os.WriteFile(LocalSubPath(basePath, id), content, 0o644)
}

// DeleteLocalSub removes stored raw file and prepared file if any.
func DeleteLocalSub(basePath, id string) {
	_ = os.Remove(LocalSubPath(basePath, id))
	DeletePrepared(basePath, id)
}

// PreparedPath is the subscription snapshot used for fast install (near-raw).
// Layout: <configDir>/prepared/<id>.yaml
func PreparedPath(basePath, id string) string {
	return filepath.Join(filepath.Dir(basePath), "prepared", id+".yaml")
}

// HasPrepared reports whether a prepared snapshot exists.
func HasPrepared(basePath, id string) bool {
	_, err := os.Stat(PreparedPath(basePath, id))
	return err == nil
}

// DeletePrepared removes the prepared snapshot.
func DeletePrepared(basePath, id string) {
	_ = os.Remove(PreparedPath(basePath, id))
}

// LoadPrepared loads a previously built prepared snapshot.
func LoadPrepared(basePath, id string) (map[string]any, error) {
	return loadYAMLFile(PreparedPath(basePath, id))
}

func savePrepared(basePath, id string, doc map[string]any) error {
	dir := filepath.Join(filepath.Dir(basePath), "prepared")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeYAMLFile(PreparedPath(basePath, id), doc)
}

// FetchAndSaveSub downloads a URL subscription and stores original bytes under subs/<id>.yaml.
func FetchAndSaveSub(basePath string, sub store.Subscription) ([]byte, error) {
	if sub.URL == "" {
		return nil, fmt.Errorf("无订阅 URL")
	}
	raw, err := downloadBytes(sub.URL)
	if err != nil {
		return nil, err
	}
	if err := SaveLocalSub(basePath, sub.ID, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// loadSubDoc loads the subscription document.
// forceRefresh=true re-downloads URL sources into the raw file first.
func loadSubDoc(basePath string, sub store.Subscription, forceRefresh bool) (map[string]any, error) {
	var raw []byte
	var err error

	if forceRefresh && sub.Source != "file" && sub.URL != "" {
		raw, err = FetchAndSaveSub(basePath, sub)
		if err != nil {
			return nil, err
		}
	} else if HasLocalSub(basePath, sub.ID) {
		raw, err = ReadLocalSubRaw(basePath, sub.ID)
		if err != nil {
			return nil, err
		}
	} else if sub.Source != "file" && sub.URL != "" {
		raw, err = FetchAndSaveSub(basePath, sub)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("本地原始配置不存在")
	}

	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse subscription yaml: %w", err)
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

// BuildPrepared stores a near-raw snapshot of the subscription for fast install.
// forceRefresh re-downloads URL raw. Providers are left to the mihomo kernel.
func BuildPrepared(basePath string, sub store.Subscription, forceRefresh bool) (*ApplyResult, error) {
	result := &ApplyResult{}
	doc, err := loadSubDoc(basePath, sub, forceRefresh)
	if err != nil {
		result.Failed = append(result.Failed, fmt.Sprintf("%s: %v", sub.Name, err))
		return result, err
	}
	if err := savePrepared(basePath, sub.ID, doc); err != nil {
		result.Failed = append(result.Failed, fmt.Sprintf("%s: %v", sub.Name, err))
		return result, err
	}
	result.OK = 1
	return result, nil
}

// InstallActive merges base ⊕ prepared(sub) ⊕ ui-state ⊕ secret → config.yaml.
// Builds prepared lazily if missing. Does not re-download unless prepared is absent.
func InstallActive(configPath string, sub store.Subscription, opts InstallOptions) (*ApplyResult, error) {
	result := &ApplyResult{}
	if !HasPrepared(configPath, sub.ID) {
		br, err := BuildPrepared(configPath, sub, false)
		if br != nil {
			result.Warnings = append(result.Warnings, br.Warnings...)
			result.Failed = append(result.Failed, br.Failed...)
		}
		if err != nil {
			return result, err
		}
	}
	subDoc, err := LoadPrepared(configPath, sub.ID)
	if err != nil {
		result.Failed = append(result.Failed, err.Error())
		return result, err
	}
	if err := writeMergedConfig(configPath, subDoc, opts); err != nil {
		result.Failed = append(result.Failed, err.Error())
		return result, err
	}
	result.OK = 1
	return result, nil
}

// InstallEmpty writes base ⊕ empty proxies ⊕ ui-state ⊕ secret (no active sub).
func InstallEmpty(configPath string, opts InstallOptions) error {
	empty := map[string]any{
		"proxies":         []any{},
		"proxy-providers": map[string]any{},
		"proxy-groups":    []any{},
		"rules":           []any{"MATCH,DIRECT"},
	}
	return writeMergedConfig(configPath, empty, opts)
}

func writeMergedConfig(configPath string, subDoc map[string]any, opts InstallOptions) error {
	base := map[string]any{}
	if opts.BasePath != "" {
		if b, err := loadYAMLFile(opts.BasePath); err == nil {
			base = b
		} else if !os.IsNotExist(err) {
			// fall back: try current config as base if base file missing on first boot
			if cur, cerr := loadYAMLFile(configPath); cerr == nil {
				base = cur
			}
		}
	} else if cur, err := loadYAMLFile(configPath); err == nil {
		base = cur
	}

	root := mergeYAML(base, subDoc)
	opts.UI.Overlay(root)
	if opts.Secret != "" {
		root["secret"] = opts.Secret
	}
	// keep control API on loopback if base didn't set (safety)
	if _, ok := root["external-controller"]; !ok {
		root["external-controller"] = "127.0.0.1:9090"
	}
	return writeYAMLFile(configPath, root)
}

// ApplySubscriptions installs the active subscription (or empty shell).
func ApplySubscriptions(configPath string, subs []store.Subscription, opts InstallOptions) error {
	_, err := ApplySubscriptionsDetailed(configPath, subs, false, opts)
	return err
}

// ApplySubscriptionsDetailed installs the active subscription.
// forceRefresh rebuilds prepared (re-download URL raw). Providers stay with the kernel.
func ApplySubscriptionsDetailed(configPath string, subs []store.Subscription, forceRefresh bool, opts InstallOptions) (*ApplyResult, error) {
	result := &ApplyResult{}
	var active *store.Subscription
	for i := range subs {
		if subs[i].Active {
			active = &subs[i]
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
		br, err := BuildPrepared(configPath, *active, true)
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
