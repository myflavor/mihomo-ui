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
	cfg := filepath.Join(dir, "config.yaml")
	base := filepath.Join(dir, "base.yaml")
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
	sub := store.Subscription{ID: "s1", Name: "S1", Active: true, Source: "file"}
	subRaw := []byte(`
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
	if err := configgen.SaveLocalSub(cfg, sub.ID, subRaw); err != nil {
		t.Fatal(err)
	}
	te := true
	opts := configgen.InstallOptions{
		BasePath: base,
		Secret:   "env-secret",
		UI: configgen.UIState{
			Mode:      "direct",
			LogLevel:  "warning",
			TunEnable: &te,
		},
	}
	if _, err := configgen.BuildPrepared(cfg, sub, false); err != nil {
		t.Fatal(err)
	}
	if _, err := configgen.InstallActive(cfg, sub, opts); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(cfg)
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
		t.Fatal("proxy-providers should pass through from sub")
	}
	if _, ok := doc["rule-providers"]; !ok {
		t.Fatal("rule-providers should pass through from sub")
	}
	proxies, _ := doc["proxies"].([]any)
	if len(proxies) != 1 {
		t.Fatalf("proxies len want 1 got %d", len(proxies))
	}
}
