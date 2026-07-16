package configgen

import (
	"os"

	"github.com/xin/mihomo-ui/internal/store"
	"gopkg.in/yaml.v3"
)

// UIState is the panel overlay applied last when installing config.
type UIState struct {
	Mode      string
	LogLevel  string
	TunEnable bool
}

// DefaultUIState is the product default (TUN off even if base has tun.enable).
func DefaultUIState() UIState {
	return UIState{
		Mode:      "rule",
		LogLevel:  "info",
		TunEnable: false,
	}
}

// DefaultUIStateFromBase reads mode / log-level from base.yaml only.
func DefaultUIStateFromBase(basePath string) UIState {
	st := DefaultUIState()
	if basePath == "" {
		return st
	}
	raw, err := os.ReadFile(basePath)
	if err != nil {
		return st
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil || doc == nil {
		return st
	}
	if m, ok := doc["mode"].(string); ok && m != "" {
		st.Mode = m
	}
	if lv, ok := doc["log-level"].(string); ok && lv != "" {
		st.LogLevel = lv
	}
	return st
}

// UIStateFromPrefs maps store prefs into install overlay.
func UIStateFromPrefs(p store.UIPrefs) UIState {
	return UIState{
		Mode:      p.Mode,
		LogLevel:  p.LogLevel,
		TunEnable: p.TunEnable,
	}
}

// Overlay applies panel switches on top of a merged config map.
func (st UIState) Overlay(root map[string]any) {
	if root == nil {
		return
	}
	if st.Mode != "" {
		root["mode"] = st.Mode
	}
	if st.LogLevel != "" {
		root["log-level"] = st.LogLevel
	}
	tun := asMap(root["tun"])
	if tun == nil {
		tun = map[string]any{}
	} else {
		tun = cloneMap(tun)
	}
	tun["enable"] = st.TunEnable
	root["tun"] = tun
}
