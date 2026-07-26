package configgen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xin/mihomo-ui/internal/configgen"
	"github.com/xin/mihomo-ui/internal/store"
	"gopkg.in/yaml.v3"
)

func TestInstallActiveMergeOrder(t *testing.T) {
	dir := t.TempDir()
	mihomoDir := filepath.Join(dir, "mihomo")
	uiDir := filepath.Join(dir, "ui")
	configDir := filepath.Join(uiDir, "config")
	configPath := filepath.Join(mihomoDir, "config.yaml")
	base := filepath.Join(uiDir, "base.yaml")
	if err := os.MkdirAll(mihomoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base, []byte(`
mixed-port: 7890
mode: rule
log-level: info
tun:
  enable: false
  stack: system
secret: from-base
dns:
  enable: true
  nameserver:
    - tls://dns.alidns.com
`), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := store.Config{ID: "s1", Name: "S1", Active: true, Source: "file"}
	cfgRaw := []byte(`
proxies:
  - name: n1
    type: http
    server: 1.1.1.1
    port: 80
proxy-providers:
  p1:
    type: http
    url: https://example.com/p
    path: ./providers/p1.yaml
rule-providers:
  r1:
    type: http
    behavior: classical
    url: https://example.com/r
    path: ./rules/r1.yaml
rules:
  - RULE-SET,r1,n1
  - MATCH,DIRECT
mode: global
secret: from-sub
`)
	if err := configgen.SaveLocalConfig(configDir, entry.ID, cfgRaw); err != nil {
		t.Fatal(err)
	}
	opts := configgen.InstallOptions{
		BasePath:  base,
		ConfigDir: configDir,
		Secret:    "env-secret",
		KernelAPI: "127.0.0.1:9090",
		ProxyHost: "127.0.0.1",
		ProxyPort: 7890,
		UI: configgen.UIState{
			Mode:      "direct",
			LogLevel:  "warning",
			TunEnable: true,
		},
	}
	if _, err := configgen.InstallActive(configPath, entry, opts); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["mode"] != "direct" {
		t.Fatalf("mode want direct got %v", doc["mode"])
	}
	if doc["log-level"] != "warning" {
		t.Fatalf("log-level want warning got %v", doc["log-level"])
	}
	if doc["secret"] != "env-secret" {
		t.Fatalf("secret want env-secret got %v", doc["secret"])
	}
	if doc["mixed-port"] != 7890 {
		t.Fatalf("mixed-port from base missing: %v", doc["mixed-port"])
	}
	tun, _ := doc["tun"].(map[string]any)
	if tun == nil || tun["enable"] != true {
		t.Fatalf("tun.enable want true got %v", tun)
	}
	if tun["stack"] != "system" {
		t.Fatalf("tun.stack should remain from base, got %v", tun["stack"])
	}
	if _, ok := doc["proxy-providers"]; !ok {
		t.Fatal("proxy-providers should pass through from config")
	}
	if _, ok := doc["rule-providers"]; !ok {
		t.Fatal("rule-providers should pass through from config")
	}
	proxies, _ := doc["proxies"].([]any)
	if len(proxies) != 1 {
		t.Fatalf("proxies len want 1 got %d", len(proxies))
	}
}

// The proxy inlet belongs to PROXY_LISTEN alone: with no port configured it must
// be stripped from the merged result, even when base.yaml or the subscription
// asks for one. This is the behaviour a v1.x upgrade depends on.
func TestInstallActiveDropsProxyPortWhenUnset(t *testing.T) {
	dir := t.TempDir()
	mihomoDir := filepath.Join(dir, "mihomo")
	configDir := filepath.Join(dir, "ui", "config")
	for _, d := range []string{mihomoDir, configDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	base := filepath.Join(dir, "ui", "base.yaml")
	// A v1.x-shaped base.yaml that still carries the old hardcoded port.
	if err := os.WriteFile(base, []byte("mixed-port: 7890\nbind-address: 127.0.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := store.Config{ID: "s1", Name: "S1", Active: true, Source: "file"}
	// The subscription asks for one too, from the other direction.
	if err := configgen.SaveLocalConfig(configDir, entry.ID, []byte("mixed-port: 1080\nrules:\n  - MATCH,DIRECT\n")); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(mihomoDir, "config.yaml")

	opts := configgen.InstallOptions{
		BasePath:  base,
		ConfigDir: configDir,
		Secret:    "env-secret",
		KernelAPI: "127.0.0.1:9090",
		// ProxyPort deliberately zero: PROXY_LISTEN was empty.
	}
	if _, err := configgen.InstallActive(configPath, entry, opts); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if v, ok := doc["mixed-port"]; ok {
		t.Fatalf("mixed-port survived with no PROXY_LISTEN: %v", v)
	}

	// And with a port configured it wins over both sources.
	opts.ProxyHost, opts.ProxyPort = "127.0.0.1", 18080
	if _, err := configgen.InstallActive(configPath, entry, opts); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(configPath)
	doc = nil
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["mixed-port"] != 18080 {
		t.Fatalf("mixed-port = %v, want the configured 18080", doc["mixed-port"])
	}
	if doc["bind-address"] != "127.0.0.1" {
		t.Fatalf("bind-address = %v", doc["bind-address"])
	}
}

func TestSplitListen(t *testing.T) {
	ok := []struct {
		in   string
		host string
		port int
	}{
		{"127.0.0.1:7890", "127.0.0.1", 7890},
		{"0.0.0.0:7080", "0.0.0.0", 7080},
		{":7080", "*", 7080},        // empty host is how mihomo spells "any"
		{"[::1]:9090", "::1", 9090}, // IPv6 needs the brackets stripped
		{"localhost:1", "localhost", 1},
	}
	for _, tc := range ok {
		host, port, err := configgen.SplitListen(tc.in)
		if err != nil {
			t.Errorf("%q: unexpected error %v", tc.in, err)
			continue
		}
		if host != tc.host || port != tc.port {
			t.Errorf("%q → (%q, %d), want (%q, %d)", tc.in, host, port, tc.host, tc.port)
		}
	}
	for _, bad := range []string{"7890", "127.0.0.1", "", "host:port", "127.0.0.1:99999", "::1:9090"} {
		if _, _, err := configgen.SplitListen(bad); err == nil {
			t.Errorf("%q was accepted but should not be", bad)
		}
	}
}
