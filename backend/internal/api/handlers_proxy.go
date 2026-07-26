package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// selectorTypes are the group types whose member the user can switch.
var selectorTypes = map[string]bool{
	"Selector": true, "URLTest": true, "Fallback": true, "LoadBalance": true,
}

// groupTypes is selectorTypes plus Relay, a group you display but cannot select in.
var groupTypes = map[string]bool{
	"Selector": true, "URLTest": true, "Fallback": true, "LoadBalance": true, "Relay": true,
}

// hiddenNodes are mihomo's built-in sinks, listed neither as members nor tabs.
var hiddenNodes = map[string]bool{
	"COMPATIBLE": true, "Pass": true, "REJECT": true,
}

// delayTimeoutMS bounds a single latency probe.
const delayTimeoutMS = 5000

func (s *Server) handleProxies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out, err := s.Mihomo.Proxies(ctx)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	mode := "rule"
	if cfg, err := s.Mihomo.Configs(ctx); err == nil {
		if m, ok := cfg["mode"].(string); ok && m != "" {
			mode = strings.ToLower(m)
		}
	}

	proxies, _ := out["proxies"].(map[string]any)

	// Global mode routes via GLOBAL: expose it first, then the groups it refers
	// to, so the user can switch inside them and not just pick from a flat list.
	var globalMembers map[string]bool
	if mode == "global" {
		globalMembers = map[string]bool{}
		if m, _ := proxies["GLOBAL"].(map[string]any); m != nil {
			for _, name := range filterNodeNames(m["all"], nil) {
				globalMembers[name] = true
			}
		}
	}

	var groups []map[string]any
	for name, raw := range proxies {
		// DIRECT is a legitimate group member but never a tab of its own.
		if hiddenNodes[name] || name == "DIRECT" {
			continue
		}
		m, _ := raw.(map[string]any)
		if m == nil {
			continue
		}
		t, _ := m["type"].(string)
		if !groupTypes[t] {
			continue
		}

		switch mode {
		case "global":
			// show GLOBAL itself + any group that GLOBAL can select
			if name != "GLOBAL" && !globalMembers[name] {
				continue
			}
		case "direct":
			continue
		default: // rule mode hides our synthetic wrapper
			if name == "GLOBAL" {
				continue
			}
		}

		now, _ := m["now"].(string)
		if hiddenNodes[now] {
			now = ""
		}
		// empty groups still shown (user asked: no fill — show empty state)
		groups = append(groups, map[string]any{
			"name": name,
			"type": t,
			"now":  now,
			"all":  filterNodeNames(m["all"], hiddenNodes),
		})
	}

	sort.Slice(groups, func(i, j int) bool {
		a := groups[i]["name"].(string)
		b := groups[j]["name"].(string)
		if ra, rb := groupRank(a), groupRank(b); ra != rb {
			return ra < rb
		}
		return a < b
	})
	writeJSON(w, 200, map[string]any{"groups": groups, "mode": mode})
}

// groupRank pins commonly used groups first; the rest sort alphabetically.
func groupRank(name string) int {
	switch name {
	case "GLOBAL":
		return 0 // first tab in global mode
	case "PROXY":
		return 10
	case "自动选择":
		return 20
	case "Xin":
		return 30
	default:
		return 100
	}
}

func filterNodeNames(raw any, hidden map[string]bool) []string {
	all, _ := raw.([]any)
	var out []string
	for _, x := range all {
		if name, _ := x.(string); name != "" && !hidden[name] {
			out = append(out, name)
		}
	}
	return out
}

func (s *Server) handleSelect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeErr(w, 405, errMethod)
		return
	}
	var body struct {
		Group string `json:"group"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if body.Group == "" || body.Name == "" {
		writeJSON(w, 400, map[string]string{"error": "group and name required"})
		return
	}
	if err := s.Mihomo.SelectProxy(r.Context(), body.Group, body.Name); err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

func (s *Server) handleDelay(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, 400, map[string]string{"error": "name required"})
		return
	}
	out, err := s.Mihomo.ProxyDelay(r.Context(), name, r.URL.Query().Get("url"), delayTimeoutMS)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleGroupDelay(w http.ResponseWriter, r *http.Request) {
	group := r.URL.Query().Get("group")
	if group == "" {
		writeJSON(w, 400, map[string]string{"error": "group required"})
		return
	}
	out, err := s.Mihomo.GroupDelay(r.Context(), group, r.URL.Query().Get("url"), delayTimeoutMS)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, out)
}
