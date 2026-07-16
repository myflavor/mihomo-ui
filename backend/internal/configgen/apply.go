package configgen

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/xin/mihomo-ui/internal/store"
	"gopkg.in/yaml.v3"
)

// Keys that always come from the local base config (TUN / API / ports / DNS).
var preserveKeys = []string{
	"mixed-port", "port", "socks-port", "redir-port", "tproxy-port",
	"allow-lan", "bind-address", "mode", "log-level", "ipv6",
	"find-process-mode", "unified-delay", "tcp-concurrent",
	"external-controller", "external-controller-cors", "secret",
	"external-ui", "external-ui-name", "external-ui-url",
	"profile", "tun", "dns", "sniffer", "geox-url", "geo-auto-update",
	"geo-update-interval", "geodata-mode", "geodata-loader",
	"global-client-fingerprint", "keep-alive-interval",
}

// ProviderName turns a human name into a safe yaml key.
func ProviderName(name string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
	n := re.ReplaceAllString(strings.TrimSpace(name), "_")
	if n == "" {
		n = "sub"
	}
	return n
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

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// proxyURL for downloading subscriptions when TUN intercepts direct HTTPS.
// Prefer env MIHOMO_PROXY / HTTP_PROXY; default to local mixed-port.
func downloadHTTPClient() *http.Client {
	proxy := strings.TrimSpace(os.Getenv("MIHOMO_PROXY"))
	if proxy == "" {
		proxy = strings.TrimSpace(os.Getenv("HTTP_PROXY"))
	}
	if proxy == "" {
		proxy = strings.TrimSpace(os.Getenv("http_proxy"))
	}
	if proxy == "" {
		// host-network mihomo mixed-port
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

func downloadYAML(rawURL string) (map[string]any, error) {
	raw, err := downloadBytes(rawURL)
	if err != nil {
		return nil, err
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

// loadLocalSubYAML reads a previously stored subscription file as a map.
func loadLocalSubYAML(basePath string, sub store.Subscription) (map[string]any, error) {
	raw, err := ReadLocalSubRaw(basePath, sub.ID)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse local sub yaml: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

// SaveLocalSub writes subscription content for a sub id as original bytes.
// Content is validated as YAML mapping but never re-marshaled (preserves formatting).
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

// DeleteLocalSub removes stored raw file if any.
func DeleteLocalSub(basePath, id string) {
	_ = os.Remove(LocalSubPath(basePath, id))
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

// loadSubDoc loads the subscription document for apply.
// forceRefresh=true re-downloads URL sources into the raw file first.
// Otherwise prefers on-disk raw; if missing and source is url, downloads once (lazy migrate).
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

// ApplySubscriptions downloads/loads each enabled subscription, merges proxies /
// providers / groups / rules / hosts into the base config path, and preserves
// local TUN / DNS / API settings. The file written is the one mihomo loads
// (MIHOMO_CONFIG → usually data/mihomo/config.yaml).
//
// Returns a non-nil error only when every enabled subscription fails (or base
// config cannot be read/written). Partial failures are recorded via ApplyResult.
func ApplySubscriptions(basePath string, subs []store.Subscription) error {
	_, err := ApplySubscriptionsDetailed(basePath, subs, false)
	return err
}

type ApplyResult struct {
	OK       int
	Failed   []string
	Warnings []string
}

func ApplySubscriptionsDetailed(basePath string, subs []store.Subscription, forceRefresh bool) (*ApplyResult, error) {
	raw, err := os.ReadFile(basePath)
	if err != nil {
		return nil, err
	}
	var base map[string]any
	if err := yaml.Unmarshal(raw, &base); err != nil {
		return nil, err
	}
	if base == nil {
		base = map[string]any{}
	}

	// Snapshot preserved runtime fields from current base.
	preserved := map[string]any{}
	for _, k := range preserveKeys {
		if v, ok := base[k]; ok {
			preserved[k] = v
		}
	}

	root := map[string]any{}
	for k, v := range preserved {
		root[k] = v
	}

	// Start empty merge targets.
	proxies := []any{}
	providers := map[string]any{}
	groups := []any{}
	hosts := map[string]any{}
	var rules []any

	// Track taken names so multi-sub merges stay unique.
	takenProxy := map[string]bool{"DIRECT": true, "REJECT": true, "PASS": true, "COMPATIBLE": true}
	takenGroup := map[string]bool{}
	takenProv := map[string]bool{}

	cfgDir := filepath.Dir(basePath)
	provRoot := filepath.Join(cfgDir, "providers")
	_ = os.MkdirAll(provRoot, 0o755)

	// Built-in top groups always present.
	var subTopGroups []string

	result := &ApplyResult{}
	enabledCount := 0
	for _, sub := range subs {
		// only active subscription is applied (store.ActiveList filters)
		if !sub.Active && !sub.Enabled {
			continue
		}
		enabledCount++

		doc, err := loadSubDoc(basePath, sub, forceRefresh)
		if err != nil {
			result.Failed = append(result.Failed, fmt.Sprintf("%s: %v", sub.Name, err))
			continue
		}
		result.OK++
		subKey := ProviderName(sub.Name)
		prefix := fmt.Sprintf("[%s] ", sub.Name)

		// --- rename maps for this subscription ---
		proxyRename := map[string]string{} // old -> new
		groupRename := map[string]string{}
		provRename := map[string]string{}
		var subProxyNames []string // all leaf proxies produced by this sub

		// proxies
		for _, p := range asSlice(doc["proxies"]) {
			m := asMap(p)
			if m == nil {
				continue
			}
			old := str(m["name"])
			if old == "" {
				continue
			}
			newName := uniqueName(prefix+old, takenProxy)
			takenProxy[newName] = true
			proxyRename[old] = newName
			nm := cloneMap(m)
			nm["name"] = newName
			proxies = append(proxies, nm)
			subProxyNames = append(subProxyNames, newName)
		}

		// proxy-providers — download now so groups aren't empty (COMPATIBLE)
		if pm := asMap(doc["proxy-providers"]); pm != nil {
			for oldKey, rawP := range pm {
				m := asMap(rawP)
				if m == nil {
					continue
				}
				newKey := uniqueName(subKey+"_"+ProviderName(oldKey), takenProv)
				takenProv[newKey] = true
				provRename[oldKey] = newKey
				nm := cloneMap(m)
				cachePath := filepath.Join(provRoot, newKey+".yaml")
				nm["path"] = filepath.ToSlash(filepath.Join("providers", newKey+".yaml"))
				if asMap(nm["health-check"]) == nil {
					nm["health-check"] = map[string]any{
						"enable":   true,
						"url":      "https://www.gstatic.com/generate_204",
						"interval": 300,
					}
				}
				ov := asMap(nm["override"])
				if ov == nil {
					ov = map[string]any{}
				}
				if _, ok := ov["additional-prefix"]; !ok {
					ov["additional-prefix"] = prefix
				}
				nm["override"] = ov

				// Prefer materializing HTTP providers into local files + inline
				// proxies when possible, so empty "COMPATIBLE" groups never appear.
				provURL := str(nm["url"])
				if provURL != "" {
					if names, err := materializeProvider(provURL, cachePath, prefix, &proxies, takenProxy, proxyRename); err != nil {
						result.Warnings = append(result.Warnings, fmt.Sprintf("provider %s/%s: %v", sub.Name, oldKey, err))
						// keep as http provider for later mihomo refresh
						providers[newKey] = nm
					} else if len(names) > 0 {
						providers[newKey] = nm
						provRename[oldKey+".__names__"] = strings.Join(names, "\x1e")
						subProxyNames = append(subProxyNames, names...)
					} else {
						providers[newKey] = nm
					}
				} else {
					providers[newKey] = nm
				}
			}
		}

		// If subscription has NO groups, create a select group of its proxies + providers.
		rawGroups := asSlice(doc["proxy-groups"])
		if len(rawGroups) == 0 {
			gName := uniqueName(sub.Name, takenGroup)
			takenGroup[gName] = true
			groupRename[sub.Name] = gName
			g := map[string]any{
				"name":    gName,
				"type":    "select",
				"proxies": []string{"DIRECT"},
			}
			var list []string
			for _, newName := range proxyRename {
				list = append(list, newName)
			}
			if len(list) > 0 {
				g["proxies"] = append([]string{"DIRECT"}, list...)
			}
			var use []string
			for _, nk := range provRename {
				use = append(use, nk)
			}
			if len(use) > 0 {
				g["use"] = use
			}
			groups = append(groups, g)
			subTopGroups = append(subTopGroups, gName)
		} else {
			// First pass: reserve group names
			for _, g := range rawGroups {
				m := asMap(g)
				if m == nil {
					continue
				}
				old := str(m["name"])
				if old == "" {
					continue
				}
				newName := old
				if takenGroup[newName] || takenProxy[newName] {
					newName = uniqueName(prefix+old, takenGroup)
				} else {
					takenGroup[newName] = true
				}
				groupRename[old] = newName
			}
			// Second pass: rewrite references
			for _, g := range rawGroups {
				m := asMap(g)
				if m == nil {
					continue
				}
				old := str(m["name"])
				nm := cloneMap(m)
				nm["name"] = groupRename[old]

				var nextProxies []any
				seenProxy := map[string]bool{}
				addProxy := func(s string) {
					if s == "" || seenProxy[s] {
						return
					}
					seenProxy[s] = true
					nextProxies = append(nextProxies, s)
				}

				if pl := asSlice(nm["proxies"]); pl != nil {
					for _, x := range pl {
						s := str(x)
						if s == "" {
							continue
						}
						if nn, ok := proxyRename[s]; ok {
							addProxy(nn)
						} else if nn, ok := groupRename[s]; ok {
							addProxy(nn)
						} else if s == "DIRECT" || s == "REJECT" || s == "PASS" {
							addProxy(s)
						} else {
							addProxy(s)
						}
					}
				}

				// Expand use:[provider] into concrete proxy names when materialized.
				// If provider failed → keep empty (no silent fill with unrelated nodes).
				var nextUse []any
				if ul := asSlice(nm["use"]); ul != nil {
					for _, x := range ul {
						s := str(x)
						if namesCSV, ok := provRename[s+".__names__"]; ok && namesCSV != "" {
							for _, n := range strings.Split(namesCSV, "\x1e") {
								addProxy(n)
							}
							// drop live use — names already inlined
						} else if nk, ok := provRename[s]; ok {
							// provider exists but not materialized: keep http use so
							// mihomo can retry later; group may show empty until then
							nextUse = append(nextUse, nk)
							result.Warnings = append(result.Warnings,
								fmt.Sprintf("组 %s 依赖的 provider %s 暂无节点", groupRename[old], s))
						} else {
							nextUse = append(nextUse, s)
						}
					}
				}

				if len(nextProxies) > 0 {
					nm["proxies"] = nextProxies
				} else {
					delete(nm, "proxies")
				}
				if len(nextUse) > 0 {
					nm["use"] = nextUse
				} else {
					delete(nm, "use")
				}
				groups = append(groups, nm)
			}
			if len(rawGroups) > 0 {
				if m := asMap(rawGroups[0]); m != nil {
					if nn := groupRename[str(m["name"])]; nn != "" {
						subTopGroups = append(subTopGroups, nn)
					}
				}
			}
		}

		// rewrite dialer-proxy references now that proxy/group renames are known
		for _, p := range proxies {
			rewriteDialerRefs(asMap(p), proxyRename, groupRename)
		}
		for _, g := range groups {
			rewriteDialerRefs(asMap(g), proxyRename, groupRename)
		}

		// hosts
		if hm := asMap(doc["hosts"]); hm != nil {
			for k, v := range hm {
				hosts[k] = v
			}
		}

		// rules
		for _, r := range asSlice(doc["rules"]) {
			s := str(r)
			if s == "" {
				rules = append(rules, r)
				continue
			}
			rules = append(rules, rewriteRule(s, proxyRename, groupRename))
		}

		_ = subKey
	}

	if enabledCount > 0 && result.OK == 0 {
		return result, fmt.Errorf("全部订阅下载失败: %s", strings.Join(result.Failed, "; "))
	}

	// Always expose PROXY + 自动选择 + GLOBAL
	proxyProxies := []string{"DIRECT", "REJECT"}
	if len(subTopGroups) > 0 {
		proxyProxies = append(append([]string{}, subTopGroups...), "DIRECT", "REJECT")
	}

	var finalGroups []any
	for _, g := range groups {
		m := asMap(g)
		if m == nil {
			continue
		}
		n := str(m["name"])
		if n == "PROXY" || n == "GLOBAL" || n == "自动选择" {
			nn := uniqueName(n+"_sub", takenGroup)
			takenGroup[nn] = true
			m["name"] = nn
			for i, t := range subTopGroups {
				if t == n {
					subTopGroups[i] = nn
				}
			}
			proxyProxies = []string{}
			proxyProxies = append(proxyProxies, subTopGroups...)
			proxyProxies = append(proxyProxies, "DIRECT", "REJECT")
		}
		finalGroups = append(finalGroups, m)
	}

	autoProxies := []string{}
	for name := range takenProxy {
		if name == "DIRECT" || name == "REJECT" || name == "PASS" || name == "COMPATIBLE" {
			continue
		}
		autoProxies = append(autoProxies, name)
	}
	var allProvKeys []string
	for k := range providers {
		allProvKeys = append(allProvKeys, k)
	}

	proxyGroup := map[string]any{
		"name":    "PROXY",
		"type":    "select",
		"proxies": proxyProxies,
	}
	if len(subTopGroups) > 0 {
		proxyGroup["proxies"] = append([]string{"自动选择"}, proxyProxies...)
	}

	autoGroup := map[string]any{
		"name":      "自动选择",
		"type":      "url-test",
		"url":       "https://www.gstatic.com/generate_204",
		"interval":  300,
		"tolerance": 50,
		"proxies":   uniqueStrings(autoProxies),
	}
	// only attach providers that were NOT fully materialized (no inline names)
	// to avoid duplicate node entries in the group
	var liveProv []string
	for k := range providers {
		// if provider cache empty / failed, skip — leaf proxies already listed
		_ = k
	}
	_ = liveProv
	// keep 自动选择 pure proxies list (all inlined nodes)

	globalGroup := map[string]any{
		"name":    "GLOBAL",
		"type":    "select",
		"proxies": []string{"PROXY", "DIRECT"},
	}
	for _, g := range finalGroups {
		if m := asMap(g); m != nil {
			n := str(m["name"])
			if n != "" {
				globalGroup["proxies"] = append(asStringSlice(globalGroup["proxies"]), n)
			}
		}
	}
	if len(autoProxies) > 0 || len(allProvKeys) > 0 {
		globalGroup["proxies"] = append(asStringSlice(globalGroup["proxies"]), "自动选择")
	}

	ordered := []any{proxyGroup, autoGroup}
	ordered = append(ordered, finalGroups...)
	ordered = append(ordered, globalGroup)

	root["proxies"] = proxies
	root["proxy-providers"] = providers
	root["proxy-groups"] = ordered
	if len(hosts) > 0 {
		root["hosts"] = hosts
	}

	var cleanRules []any
	for _, r := range rules {
		s := str(r)
		if s != "" && strings.HasPrefix(strings.ToUpper(s), "MATCH,") {
			continue
		}
		cleanRules = append(cleanRules, r)
	}
	hasPrivate := false
	hasCN := false
	for _, r := range cleanRules {
		u := strings.ToUpper(str(r))
		if strings.Contains(u, "GEOIP,PRIVATE") || strings.Contains(u, "GEOIP,LAN") {
			hasPrivate = true
		}
		if strings.Contains(u, "GEOIP,CN") {
			hasCN = true
		}
	}
	if !hasPrivate {
		cleanRules = append([]any{"GEOIP,private,DIRECT,no-resolve"}, cleanRules...)
	}
	if !hasCN {
		cleanRules = append(cleanRules, "GEOIP,CN,DIRECT")
	}
	cleanRules = append(cleanRules, "MATCH,PROXY")
	root["rules"] = cleanRules

	if enabledCount == 0 || (result.OK == 0 && enabledCount == 0) {
		root["proxies"] = []any{}
		root["proxy-providers"] = map[string]any{}
		root["proxy-groups"] = []any{
			map[string]any{"name": "PROXY", "type": "select", "proxies": []string{"DIRECT"}},
			map[string]any{"name": "GLOBAL", "type": "select", "proxies": []string{"PROXY", "DIRECT"}},
		}
		root["rules"] = []any{
			"GEOIP,private,DIRECT,no-resolve",
			"GEOIP,CN,DIRECT",
			"MATCH,PROXY",
		}
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		return result, err
	}
	tmp := basePath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return result, err
	}
	if err := os.Rename(tmp, basePath); err != nil {
		return result, err
	}
	return result, nil
}

func asStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return append([]string{}, t...)
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			out = append(out, str(x))
		}
		return out
	default:
		return nil
	}
}

func uniqueName(want string, taken map[string]bool) string {
	if !taken[want] {
		taken[want] = true
		return want
	}
	for i := 2; ; i++ {
		n := fmt.Sprintf("%s_%d", want, i)
		if !taken[n] {
			taken[n] = true
			return n
		}
	}
}

// rewriteRule rewrites the policy field (last comma-separated part, ignoring no-resolve).

// rewriteDialerRefs updates dialer-proxy fields after name renames.
func rewriteDialerRefs(obj map[string]any, proxyRename, groupRename map[string]string) {
	if obj == nil {
		return
	}
	for _, key := range []string{"dialer-proxy", "dialer_proxy"} {
		v := str(obj[key])
		if v == "" {
			continue
		}
		if nn, ok := groupRename[v]; ok {
			obj[key] = nn
			continue
		}
		if nn, ok := proxyRename[v]; ok {
			obj[key] = nn
		}
	}
}

func rewriteRule(rule string, proxyRename, groupRename map[string]string) string {
	parts := strings.Split(rule, ",")
	if len(parts) < 2 {
		return rule
	}
	// last meaningful policy: skip trailing flags like no-resolve
	idx := len(parts) - 1
	for idx >= 1 {
		p := strings.TrimSpace(parts[idx])
		lp := strings.ToLower(p)
		if lp == "no-resolve" || lp == "src" {
			idx--
			continue
		}
		break
	}
	if idx < 1 {
		return rule
	}
	pol := strings.TrimSpace(parts[idx])
	if nn, ok := groupRename[pol]; ok {
		parts[idx] = nn
	} else if nn, ok := proxyRename[pol]; ok {
		parts[idx] = nn
	}
	return strings.Join(parts, ",")
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// materializeProvider downloads a nested proxy-provider, writes cache file,
// and inlines its proxies into the main config (with prefix). Returns new names.
func materializeProvider(
	provURL, cachePath, prefix string,
	proxies *[]any,
	takenProxy map[string]bool,
	proxyRename map[string]string,
) ([]string, error) {
	raw, err := downloadBytes(provURL)
	if err != nil {
		return nil, err
	}
	// write cache for mihomo provider path
	if err := os.WriteFile(cachePath, raw, 0o644); err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	var names []string
	for _, p := range asSlice(doc["proxies"]) {
		m := asMap(p)
		if m == nil {
			continue
		}
		old := str(m["name"])
		if old == "" {
			continue
		}
		// don't double-prefix if already present
		newName := old
		if !strings.HasPrefix(old, prefix) {
			newName = prefix + old
		}
		// if this name already taken with same logical node, reuse
		if takenProxy[newName] {
			// already inlined (e.g. edge also listed elsewhere) — still list for group
			if _, exists := proxyRename[old]; !exists {
				proxyRename[old] = newName
			}
			names = append(names, newName)
			continue
		}
		newName = uniqueName(newName, takenProxy)
		takenProxy[newName] = true
		if _, exists := proxyRename[old]; !exists {
			proxyRename[old] = newName
		}
		nm := cloneMap(m)
		nm["name"] = newName
		*proxies = append(*proxies, nm)
		names = append(names, newName)
	}
	return uniqueStrings(names), nil
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
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var root map[string]any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return err
	}
	if root == nil {
		root = map[string]any{}
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
	out, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
