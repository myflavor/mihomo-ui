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
