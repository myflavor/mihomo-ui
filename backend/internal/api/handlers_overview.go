package api

import (
	"net/http"
	"slices"
	"strings"
	"time"
)

// syntheticGroups are mihomo's built-ins, never a user-chosen exit.
var syntheticGroups = map[string]bool{
	"GLOBAL": true, "COMPATIBLE": true, "DIRECT": true, "REJECT": true, "PASS": true,
}

// countHidden are the built-in sinks: plumbing, not something the user added.
var countHidden = map[string]bool{
	"COMPATIBLE": true, "Pass": true, "REJECT": true, "REJECT-DROP": true, "PASS": true, "PASS-RULE": true,
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "time": time.Now()})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg, err := s.Mihomo.Configs(ctx)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	ver, _ := s.Mihomo.Version(ctx)
	tun, _ := cfg["tun"].(map[string]any)
	if tun == nil {
		tun = map[string]any{}
	}
	active, _ := s.Store.Active()

	var connCount int
	var upTotal, downTotal, mem any
	if conn, err := s.Mihomo.Connections(ctx); err == nil {
		list, _ := conn["connections"].([]any)
		connCount = len(list)
		upTotal = conn["uploadTotal"]
		downTotal = conn["downloadTotal"]
		mem = conn["memory"]
	}

	// One /proxies fetch feeds both the exit summary and the home stat counts.
	exit := map[string]any{}
	groupCount, proxyCount := 0, 0
	if px, err := s.Mihomo.Proxies(ctx); err == nil {
		proxies, _ := px["proxies"].(map[string]any)
		mode, _ := cfg["mode"].(string)
		exit = currentExit(proxies, strings.ToLower(mode), active.Name)
		groupCount, proxyCount = countProxies(proxies)
	}

	ruleCount := 0
	if rl, err := s.Mihomo.Rules(ctx); err == nil {
		rules, _ := rl["rules"].([]any)
		ruleCount = len(rules)
	}

	writeJSON(w, 200, map[string]any{
		"mode":          cfg["mode"],
		"tun":           tun,
		"version":       ver,
		"mixed-port":    cfg["mixed-port"],
		"port":          cfg["port"],
		"socks-port":    cfg["socks-port"],
		"allow-lan":     cfg["allow-lan"],
		"log-level":     cfg["log-level"],
		"ipv6":          cfg["ipv6"],
		"config-path":   s.ConfigPath,
		"configs":       len(s.Store.List()),
		"active":        active,
		"connections":   connCount,
		"uploadTotal":   upTotal,
		"downloadTotal": downTotal,
		"memory":        mem,
		"exit":          exit,
		"groups":        groupCount,
		"proxies":       proxyCount,
		"rules":         ruleCount,
	})
}

// currentExit summarises the group deciding the outgoing hop: GLOBAL in global
// mode, none in direct, else the group named after the active config.
func currentExit(proxies map[string]any, mode, activeName string) map[string]any {
	exit := map[string]any{}
	pick := func(name string) {
		m, _ := proxies[name].(map[string]any)
		if m == nil {
			return
		}
		exit["group"] = name
		exit["type"] = m["type"]
		exit["now"] = m["now"]
		if history, _ := m["history"].([]any); len(history) > 0 {
			if last, _ := history[len(history)-1].(map[string]any); last != nil {
				exit["delay"] = last["delay"]
			}
		}
	}

	switch mode {
	case "global":
		pick("GLOBAL")
	case "direct":
		exit["group"] = "DIRECT"
		exit["now"] = "DIRECT"
	default:
		if isSelector(proxies[activeName]) {
			pick(activeName)
		}
		if exit["group"] == nil {
			pick(firstSelectorName(proxies))
		}
	}
	return exit
}

func isSelector(raw any) bool {
	m, _ := raw.(map[string]any)
	if m == nil {
		return false
	}
	t, _ := m["type"].(string)
	return selectorTypes[t]
}

// firstSelectorName returns the alphabetically first switchable group, or "".
func firstSelectorName(proxies map[string]any) string {
	first := ""
	for name, raw := range proxies {
		if syntheticGroups[name] || !isSelector(raw) {
			continue
		}
		if first == "" || name < first {
			first = name
		}
	}
	return first
}

func countProxies(proxies map[string]any) (groups, nodes int) {
	for name, raw := range proxies {
		if countHidden[name] {
			continue
		}
		m, _ := raw.(map[string]any)
		if m == nil {
			continue
		}
		t, _ := m["type"].(string)
		switch {
		case groupTypes[t]:
			groups++
		case t != "" && t != "Compatible":
			nodes++
		}
	}
	return groups, nodes
}

// handleConnections: GET full snapshot, DELETE close all, or DELETE ?id= for one.
func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.writeConnections(w, r)
	case http.MethodDelete:
		s.closeConnections(w, r)
	default:
		writeErr(w, 405, errMethod)
	}
}

func (s *Server) writeConnections(w http.ResponseWriter, r *http.Request) {
	out, err := s.Mihomo.Connections(r.Context())
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	list, _ := out["connections"].([]any)
	slices.Reverse(list) // newest first
	items := make([]map[string]any, 0, len(list))
	for _, raw := range list {
		if m, _ := raw.(map[string]any); m != nil {
			items = append(items, flattenConnection(m))
		}
	}
	writeJSON(w, 200, map[string]any{
		"downloadTotal": out["downloadTotal"],
		"uploadTotal":   out["uploadTotal"],
		"memory":        out["memory"],
		"count":         len(items),
		"items":         items,
	})
}

func (s *Server) closeConnections(w http.ResponseWriter, r *http.Request) {
	if id := r.URL.Query().Get("id"); id != "" {
		if err := s.Mihomo.CloseConnection(r.Context(), id); err != nil {
			writeErr(w, 502, err)
			return
		}
		writeJSON(w, 200, map[string]string{"ok": "1", "id": id})
		return
	}
	if err := s.Mihomo.CloseAllConnections(r.Context()); err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

// flattenConnection flattens a connection into a table row: host:port joined,
// first chain hop hoisted out.
func flattenConnection(m map[string]any) map[string]any {
	// A nil metadata map still indexes fine, so no guard is needed.
	meta, _ := m["metadata"].(map[string]any)
	host, _ := meta["host"].(string)
	if host == "" {
		host, _ = meta["destinationIP"].(string)
	}
	process, _ := meta["process"].(string)
	if process == "" {
		process, _ = meta["processPath"].(string)
	}
	srcIP, _ := meta["sourceIP"].(string)
	srcPort, _ := meta["sourcePort"].(string)
	dstPort, _ := meta["destinationPort"].(string)

	chains, _ := m["chains"].([]any)
	var chainList []string
	for _, raw := range chains {
		if hop, _ := raw.(string); hop != "" {
			chainList = append(chainList, hop)
		}
	}
	chain := ""
	if len(chainList) > 0 {
		chain = chainList[0]
	}

	return map[string]any{
		"id":          m["id"],
		"host":        host,
		"network":     meta["network"],
		"type":        meta["type"],
		"upload":      m["upload"],
		"download":    m["download"],
		"rule":        m["rule"],
		"rulePayload": m["rulePayload"],
		"chain":       chain,
		"chains":      chainList,
		"process":     process,
		"source":      joinHostPort(srcIP, srcPort),
		"destination": joinHostPort(host, dstPort),
		"start":       m["start"],
	}
}

// joinHostPort appends :port only when mihomo reported one.
func joinHostPort(host, port string) string {
	if port == "" {
		return host
	}
	return host + ":" + port
}
