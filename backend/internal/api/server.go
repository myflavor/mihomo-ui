package api

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/xin/mihomo-ui/internal/configgen"
	"github.com/xin/mihomo-ui/internal/mihomo"
	"github.com/xin/mihomo-ui/internal/store"
)

type Server struct {
	Mihomo     *mihomo.Client
	MihomoURL  string
	Secret     string
	Store      *store.Store
	UIPassword string
	ConfigPath string
	StaticDir  string
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/overview", s.handleOverview)
	mux.HandleFunc("/api/mode", s.handleMode)
	mux.HandleFunc("/api/tun", s.handleTun)
	mux.HandleFunc("/api/proxies", s.handleProxies)
	mux.HandleFunc("/api/proxies/select", s.handleSelect)
	mux.HandleFunc("/api/proxies/delay", s.handleDelay)
	mux.HandleFunc("/api/group/delay", s.handleGroupDelay)
	mux.HandleFunc("/api/subscriptions/apply", s.handleApply)
	mux.HandleFunc("/api/subscriptions/refresh", s.handleRefreshSubs)
	mux.HandleFunc("/api/subscriptions/", s.handleSubscriptionItem)
	mux.HandleFunc("/api/subscriptions", s.handleSubscriptions)
	mux.HandleFunc("/api/providers/update", s.handleUpdateProvider)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/logs", s.handleLogs)

	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/auth/check", s.handleAuthCheck)

	if s.StaticDir != "" {
		fs := http.FileServer(http.Dir(s.StaticDir))
		mux.Handle("/", spaHandler(s.StaticDir, fs))
	}
	return withCORS(s.withAuth(mux))
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-UI-Password")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}


// withAuth protects /api/* except health + login. Static SPA is public; APIs need password.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		switch r.URL.Path {
		case "/api/health", "/api/login", "/api/auth/check":
			next.ServeHTTP(w, r)
			return
		}
		if s.UIPassword == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !s.authorized(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mihomo-ui"`)
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	if s.UIPassword == "" {
		return true
	}
	want := s.UIPassword
	// Authorization: Bearer <password>
	if h := r.Header.Get("Authorization"); h != "" {
		const p = "Bearer "
		if strings.HasPrefix(h, p) {
			got := strings.TrimSpace(h[len(p):])
			if subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1 {
				return true
			}
		}
		// Basic user:pass (user ignored)
		if strings.HasPrefix(h, "Basic ") {
			// decode manually via net/http Request BasicAuth
			if _, pass, ok := r.BasicAuth(); ok {
				if subtle.ConstantTimeCompare([]byte(pass), []byte(want)) == 1 {
					return true
				}
			}
		}
	}
	// X-UI-Password header fallback
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-UI-Password")), []byte(want)) == 1 {
		return true
	}
	// query ?token= for EventSource-like clients if needed
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(want)) == 1 {
		return true
	}
	return false
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if s.UIPassword == "" {
		writeJSON(w, 200, map[string]any{"ok": true, "auth": false})
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Password), []byte(s.UIPassword)) != 1 {
		writeJSON(w, 401, map[string]string{"error": "密码错误"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "auth": true, "token": s.UIPassword})
}

func (s *Server) handleAuthCheck(w http.ResponseWriter, r *http.Request) {
	required := s.UIPassword != ""
	ok := !required || s.authorized(r)
	writeJSON(w, 200, map[string]any{
		"required": required,
		"ok":       ok,
	})
}

func spaHandler(dir string, fs http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/" {
			rel := strings.TrimPrefix(r.URL.Path, "/")
			f, err := http.Dir(dir).Open(rel)
			if err == nil {
				_ = f.Close()
				fs.ServeHTTP(w, r)
				return
			}
			http.ServeFile(w, r, dir+"/index.html")
			return
		}
		fs.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// applyAndReload applies the *active* subscription only, then hot-reloads mihomo.
// forceRefresh re-downloads URL sources into raw files before merge.
func (s *Server) applyAndReload(r *http.Request, forceRefresh bool) (*configgen.ApplyResult, error) {
	res, err := configgen.ApplySubscriptionsDetailed(s.ConfigPath, s.Store.ActiveList(), forceRefresh)
	if err != nil {
		return res, err
	}
	if err := s.Mihomo.ReloadConfig(r.Context(), s.ConfigPath); err != nil {
		return res, err
	}
	return res, nil
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
	tun := map[string]any{}
	if t, ok := cfg["tun"].(map[string]any); ok {
		tun = t
	}
	active, _ := s.Store.Active()
	writeJSON(w, 200, map[string]any{
		"mode":          cfg["mode"],
		"tun":           tun,
		"version":       ver,
		"mixed-port":    cfg["mixed-port"],
		"port":          cfg["port"],
		"socks-port":    cfg["socks-port"],
		"allow-lan":     cfg["allow-lan"],
		"log-level":     cfg["log-level"],
		"config-path":   s.ConfigPath,
		"subscriptions": len(s.Store.List()),
		"active":        active,
	})
}

func (s *Server) handleMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch {
		writeErr(w, 405, errMethod)
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(body.Mode))
	if mode != "rule" && mode != "global" && mode != "direct" {
		writeJSON(w, 400, map[string]string{"error": "mode must be rule|global|direct"})
		return
	}
	if err := s.Mihomo.PatchConfigs(r.Context(), map[string]any{"mode": mode}); err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]string{"mode": mode})
}

func (s *Server) handleTun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch {
		writeErr(w, 405, errMethod)
		return
	}
	var body struct {
		Enable *bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if body.Enable == nil {
		writeJSON(w, 400, map[string]string{"error": "enable required"})
		return
	}
	patch := map[string]any{
		"tun": map[string]any{
			"enable": *body.Enable,
		},
	}
	if err := s.Mihomo.PatchConfigs(r.Context(), patch); err != nil {
		writeErr(w, 502, err)
		return
	}
	// also persist into the real config file so reload keeps the choice
	_ = configgen.PatchYAMLFile(s.ConfigPath, map[string]any{
		"tun": map[string]any{"enable": *body.Enable},
	})
	writeJSON(w, 200, map[string]bool{"enable": *body.Enable})
}

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
	var groups []map[string]any
	groupTypes := map[string]bool{
		"Selector": true, "URLTest": true, "Fallback": true, "LoadBalance": true, "Relay": true,
	}
	hiddenNames := map[string]bool{
		"COMPATIBLE": true, "Pass": true, "REJECT": true, "DIRECT": true,
	}
	synthetic := map[string]bool{
		"PROXY": true, "自动选择": true, "GLOBAL": true,
	}
	hiddenNodes := map[string]bool{
		"COMPATIBLE": true, "Pass": true, "REJECT": true,
	}

	for name, raw := range proxies {
		if hiddenNames[name] {
			continue
		}
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		t, _ := m["type"].(string)
		if !groupTypes[t] {
			continue
		}

		switch mode {
		case "global":
			if name != "GLOBAL" {
				continue
			}
		case "direct":
			continue
		default:
			if synthetic[name] {
				continue
			}
		}

		all := filterNodeNames(m["all"], hiddenNodes)
		// empty groups still shown (user asked: no fill — show empty state)
		now, _ := m["now"].(string)
		if hiddenNodes[now] {
			now = ""
		}
		item := map[string]any{
			"name": name,
			"type": t,
			"now":  now,
			"all":  all,
		}
		groups = append(groups, item)
	}

	if mode == "rule" && len(groups) == 0 {
		if raw, ok := proxies["PROXY"]; ok {
			if m, ok := raw.(map[string]any); ok {
				all := filterNodeNames(m["all"], hiddenNodes)
				now, _ := m["now"].(string)
				if hiddenNodes[now] {
					now = ""
				}
				groups = append(groups, map[string]any{
					"name": "PROXY",
					"type": m["type"],
					"now":  now,
					"all":  all,
				})
			}
		}
	}

	sort.Slice(groups, func(i, j int) bool {
		a, b := groups[i]["name"].(string), groups[j]["name"].(string)
		rank := func(n string) int {
			switch n {
			case "Xin":
				return 0
			case "PROXY":
				return 900
			case "GLOBAL":
				return 1000
			default:
				return 100
			}
		}
		ra, rb := rank(a), rank(b)
		if ra != rb {
			return ra < rb
		}
		return a < b
	})
	writeJSON(w, 200, map[string]any{"groups": groups, "mode": mode})
}

func filterNodeNames(raw any, hidden map[string]bool) []string {
	var out []string
	switch t := raw.(type) {
	case []any:
		for _, x := range t {
			s, _ := x.(string)
			if s == "" || hidden[s] {
				continue
			}
			out = append(out, s)
		}
	case []string:
		for _, s := range t {
			if s == "" || hidden[s] {
				continue
			}
			out = append(out, s)
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
	out, err := s.Mihomo.ProxyDelay(r.Context(), name, r.URL.Query().Get("url"), 5000)
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
	out, err := s.Mihomo.GroupDelay(r.Context(), group, r.URL.Query().Get("url"), 5000)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		active, _ := s.Store.Active()
		writeJSON(w, 200, map[string]any{
			"items":  s.Store.List(),
			"active": active,
		})
	case http.MethodPost:
		// support JSON or multipart (file upload)
		ct := r.Header.Get("Content-Type")
		if strings.HasPrefix(ct, "multipart/form-data") {
			s.handleSubUpload(w, r, "")
			return
		}
		var body struct {
			Name     string `json:"name"`
			URL      string `json:"url"`
			Source   string `json:"source"`
			Interval int    `json:"interval"`
			Content  string `json:"content"` // optional inline yaml
			Activate *bool  `json:"activate"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, 400, err)
			return
		}
		source := body.Source
		if source == "" {
			if body.Content != "" {
				source = "file"
			} else {
				source = "url"
			}
		}
		sub, err := s.Store.Add(body.Name, body.URL, source, body.Interval)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		if body.Content != "" {
			if err := configgen.SaveLocalSub(s.ConfigPath, sub.ID, []byte(body.Content)); err != nil {
				_ = s.Store.Delete(sub.ID)
				writeErr(w, 400, err)
				return
			}
		} else if source != "file" && body.URL != "" {
			// eagerly cache original subscription YAML so 编辑配置 works before activate
			_, _ = configgen.FetchAndSaveSub(s.ConfigPath, sub)
		}
		activate := true
		if body.Activate != nil {
			activate = *body.Activate
		}
		if activate {
			if _, err := s.Store.SetActive(sub.ID); err != nil {
				writeErr(w, 500, err)
				return
			}
			sub, _ = s.Store.Get(sub.ID)
			res, err := s.applyAndReload(r, false)
			if err != nil {
				writeJSON(w, 201, map[string]any{
					"item":  sub,
					"apply": map[string]any{"ok": "0", "error": err.Error(), "detail": res},
				})
				return
			}
			writeJSON(w, 201, map[string]any{"item": sub, "apply": map[string]any{"ok": "1", "detail": res}})
			return
		}
		writeJSON(w, 201, map[string]any{"item": sub})
	default:
		writeErr(w, 405, errMethod)
	}
}

func (s *Server) handleSubUpload(w http.ResponseWriter, r *http.Request, existingID string) {
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeErr(w, 400, err)
		return
	}
	name := r.FormValue("name")
	urlStr := r.FormValue("url")
	source := r.FormValue("source")
	interval := 0 // 0 = no auto update
	if v := r.FormValue("interval"); v != "" {
		var n int
		if _, err := fmtSscanf(v, &n); err == nil && n >= 0 {
			interval = n
		}
	}
	var content []byte
	if f, _, err := r.FormFile("file"); err == nil {
		defer f.Close()
		content, _ = io.ReadAll(f)
	}
	if content == nil && r.FormValue("content") != "" {
		content = []byte(r.FormValue("content"))
	}
	if source == "" {
		if len(content) > 0 {
			source = "file"
		} else {
			source = "url"
		}
	}
	if name == "" {
		writeJSON(w, 400, map[string]string{"error": "name required"})
		return
	}

	var sub store.Subscription
	var err error
	if existingID == "" {
		sub, err = s.Store.Add(name, urlStr, source, interval)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
	} else {
		p := store.SubPatch{Name: &name, URL: &urlStr, Source: &source, Interval: &interval}
		sub, err = s.Store.Update(existingID, p)
		if err != nil {
			writeErr(w, 404, err)
			return
		}
	}
	if len(content) > 0 {
		if err := configgen.SaveLocalSub(s.ConfigPath, sub.ID, content); err != nil {
			writeErr(w, 400, err)
			return
		}
		// ensure source is file when content provided
		src := "file"
		sub, _ = s.Store.Update(sub.ID, store.SubPatch{Source: &src})
	}

	// default activate on create
	if existingID == "" || r.FormValue("activate") == "1" || r.FormValue("activate") == "true" {
		sub, _ = s.Store.SetActive(sub.ID)
		res, err := s.applyAndReload(r, false)
		if err != nil {
			writeJSON(w, 200, map[string]any{
				"item":  sub,
				"apply": map[string]any{"ok": "0", "error": err.Error(), "detail": res},
			})
			return
		}
		writeJSON(w, 200, map[string]any{"item": sub, "apply": map[string]any{"ok": "1", "detail": res}})
		return
	}
	writeJSON(w, 200, map[string]any{"item": sub})
}

func fmtSscanf(v string, n *int) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, errMethod
	}
	var x int
	for i, ch := range v {
		if ch < '0' || ch > '9' {
			if i == 0 {
				return 0, errMethod
			}
			break
		}
		x = x*10 + int(ch-'0')
	}
	*n = x
	return 1, nil
}

func (s *Server) handleSubscriptionItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/subscriptions/")
	rest = strings.Trim(rest, "/")
	if rest == "" || rest == "apply" || rest == "refresh" {
		http.NotFound(w, r)
		return
	}
	// /api/subscriptions/{id}/activate
	// /api/subscriptions/{id}/upload
	parts := strings.Split(rest, "/")
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	if action == "activate" && r.Method == http.MethodPost {
		sub, err := s.Store.SetActive(id)
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		res, err := s.applyAndReload(r, false)
		if err != nil {
			writeJSON(w, 200, map[string]any{
				"item":  sub,
				"apply": map[string]any{"ok": "0", "error": err.Error(), "detail": res},
			})
			return
		}
		writeJSON(w, 200, map[string]any{"item": sub, "apply": map[string]any{"ok": "1", "detail": res}})
		return
	}

	if action == "upload" && r.Method == http.MethodPost {
		s.handleSubUpload(w, r, id)
		return
	}

	if action == "raw" {
		s.handleSubRaw(w, r, id)
		return
	}

	if action != "" {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		if err := s.Store.Delete(id); err != nil {
			writeErr(w, 404, err)
			return
		}
		configgen.DeleteLocalSub(s.ConfigPath, id)
		// re-apply new active (if any)
		res, err := s.applyAndReload(r, false)
		if err != nil {
			writeJSON(w, 200, map[string]any{"ok": "1", "apply": map[string]any{"ok": "0", "error": err.Error(), "detail": res}})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": "1", "apply": map[string]any{"ok": "1", "detail": res}})
	case http.MethodPatch, http.MethodPut:
		ct := r.Header.Get("Content-Type")
		if strings.HasPrefix(ct, "multipart/form-data") {
			s.handleSubUpload(w, r, id)
			return
		}
		var body struct {
			Name     *string `json:"name"`
			URL      *string `json:"url"`
			Source   *string `json:"source"`
			Interval *int    `json:"interval"`
			Content  *string `json:"content"`
			Activate *bool   `json:"activate"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, 400, err)
			return
		}
		sub, err := s.Store.Update(id, store.SubPatch{
			Name: body.Name, URL: body.URL, Source: body.Source, Interval: body.Interval,
		})
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		if body.Content != nil {
			if err := configgen.SaveLocalSub(s.ConfigPath, id, []byte(*body.Content)); err != nil {
				writeErr(w, 400, err)
				return
			}
			// do NOT force source=file — editing raw of a URL sub keeps source=url
		}
		// re-apply only if this is the active one, or activate requested
		if body.Activate != nil && *body.Activate {
			sub, _ = s.Store.SetActive(id)
		}
		if sub.Active {
			res, err := s.applyAndReload(r, false)
			if err != nil {
				writeJSON(w, 200, map[string]any{"item": sub, "apply": map[string]any{"ok": "0", "error": err.Error(), "detail": res}})
				return
			}
			writeJSON(w, 200, map[string]any{"item": sub, "apply": map[string]any{"ok": "1", "detail": res}})
			return
		}
		writeJSON(w, 200, map[string]any{"item": sub})
	default:
		writeErr(w, 405, errMethod)
	}
}

func (s *Server) handleSubRaw(w http.ResponseWriter, r *http.Request, id string) {
	sub, err := s.Store.Get(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	path := configgen.LocalSubPath(s.ConfigPath, id)
	switch r.Method {
	case http.MethodGet:
		raw, err := configgen.ReadLocalSubRaw(s.ConfigPath, id)
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "尚未缓存原始配置，请先更新或重新添加", "path": path})
			return
		}
		writeJSON(w, 200, map[string]any{
			"id":      sub.ID,
			"name":    sub.Name,
			"source":  sub.Source,
			"path":    path,
			"content": string(raw),
			"active":  sub.Active,
		})
	case http.MethodPut, http.MethodPost:
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			// also accept raw text body
			b, err2 := io.ReadAll(r.Body)
			if err2 != nil {
				writeErr(w, 400, err)
				return
			}
			body.Content = string(b)
		}
		if strings.TrimSpace(body.Content) == "" {
			writeJSON(w, 400, map[string]string{"error": "content required"})
			return
		}
		if err := configgen.SaveLocalSub(s.ConfigPath, id, []byte(body.Content)); err != nil {
			writeErr(w, 400, err)
			return
		}
		// keep source as-is; only apply when this subscription is active
		if sub.Active {
			res, err := s.applyAndReload(r, false)
			if err != nil {
				writeJSON(w, 200, map[string]any{
					"ok":    "0",
					"path":  path,
					"error": err.Error(),
					"detail": res,
					"applied": true,
				})
				return
			}
			writeJSON(w, 200, map[string]any{"ok": "1", "path": path, "applied": true, "detail": res})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": "1", "path": path, "applied": false})
	default:
		writeErr(w, 405, errMethod)
	}
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	res, err := s.applyAndReload(r, false)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": "1", "config-path": s.ConfigPath, "detail": res})
}

func (s *Server) handleRefreshSubs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	// forceRefresh: re-download active URL subscription into raw file, then apply
	res, err := s.applyAndReload(r, true)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	out, err := s.Mihomo.Providers(r.Context())
	var errs []string
	if err == nil {
		providers, _ := out["providers"].(map[string]any)
		for name, raw := range providers {
			m, _ := raw.(map[string]any)
			if vt, _ := m["vehicleType"].(string); vt == "Compatible" {
				continue
			}
			if err := s.Mihomo.UpdateProvider(r.Context(), name); err != nil {
				errs = append(errs, name+": "+err.Error())
			}
		}
	}
	if res != nil {
		errs = append(errs, res.Failed...)
	}
	writeJSON(w, 200, map[string]any{
		"ok":          len(errs) == 0,
		"config-path": s.ConfigPath,
		"detail":      res,
		"errors":      errs,
	})
}

func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Name == "" {
		out, err := s.Mihomo.Providers(r.Context())
		if err != nil {
			writeErr(w, 502, err)
			return
		}
		providers, _ := out["providers"].(map[string]any)
		var errs []string
		for name, raw := range providers {
			m, _ := raw.(map[string]any)
			if vt, _ := m["vehicleType"].(string); vt == "Compatible" {
				continue
			}
			if err := s.Mihomo.UpdateProvider(r.Context(), name); err != nil {
				errs = append(errs, name+": "+err.Error())
			}
		}
		writeJSON(w, 200, map[string]any{"ok": len(errs) == 0, "errors": errs})
		return
	}
	if err := s.Mihomo.UpdateProvider(r.Context(), body.Name); err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

// handleConfig reads/writes the real MIHOMO_CONFIG file on disk.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		raw, err := os.ReadFile(s.ConfigPath)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{
			"path":    s.ConfigPath,
			"content": string(raw),
		})
	case http.MethodPut, http.MethodPost:
		var body struct {
			Content string `json:"content"`
			Reload  *bool  `json:"reload"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			// also accept raw text body
			b, err2 := io.ReadAll(r.Body)
			if err2 != nil {
				writeErr(w, 400, err)
				return
			}
			body.Content = string(b)
		}
		if strings.TrimSpace(body.Content) == "" {
			writeJSON(w, 400, map[string]string{"error": "content required"})
			return
		}
		// validate yaml
		if err := configgen.ValidateYAML([]byte(body.Content)); err != nil {
			writeErr(w, 400, err)
			return
		}
		tmp := s.ConfigPath + ".tmp"
		if err := os.WriteFile(tmp, []byte(body.Content), 0o644); err != nil {
			writeErr(w, 500, err)
			return
		}
		if err := os.Rename(tmp, s.ConfigPath); err != nil {
			writeErr(w, 500, err)
			return
		}
		reload := true
		if body.Reload != nil {
			reload = *body.Reload
		}
		if reload {
			if err := s.Mihomo.ReloadConfig(r.Context(), s.ConfigPath); err != nil {
				writeJSON(w, 200, map[string]any{
					"ok":    "0",
					"path":  s.ConfigPath,
					"error": err.Error(),
				})
				return
			}
		}
		writeJSON(w, 200, map[string]any{"ok": "1", "path": s.ConfigPath, "reloaded": reload})
	default:
		writeErr(w, 405, errMethod)
	}
}

// handleLogs proxies mihomo's chunked log stream to the browser as NDJSON.
// Mihomo may hold response headers until the first log event, so we always
// flush browser headers + a heartbeat first, then attach to upstream.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errMethod)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]string{"error": "streaming unsupported"})
		return
	}

	level := r.URL.Query().Get("level")
	if level == "" {
		level = "info"
	}

	// Unblock the browser immediately (idle mihomo /logs may not send headers).
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"type":"connected","payload":"log stream ready"}` + "\n"))
	flusher.Flush()

	base := strings.TrimRight(s.MihomoURL, "/")
	if base == "" {
		base = "http://127.0.0.1:9090"
	}
	upURL := base + "/logs?level=" + urlQueryEscape(level)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upURL, nil)
	if err != nil {
		_, _ = w.Write([]byte(`{"type":"error","payload":"` + strings.ReplaceAll(err.Error(), `"`, `'`) + `"}` + "\n"))
		flusher.Flush()
		return
	}
	if s.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+s.Secret)
	}
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		_, _ = w.Write([]byte(`{"type":"error","payload":"` + strings.ReplaceAll(err.Error(), `"`, `'`) + `"}` + "\n"))
		flusher.Flush()
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := strings.ReplaceAll(string(b), `"`, `'`)
		msg = strings.ReplaceAll(msg, "\n", " ")
		payload := "upstream " + resp.Status + ": " + msg
		line, _ := json.Marshal(map[string]string{"type": "error", "payload": payload})
		_, _ = w.Write(append(line, '\n'))
		flusher.Flush()
		return
	}

	// optional idle ping so proxies keep the stream open
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-r.Context().Done():
				return
			case <-t.C:
				if _, err := w.Write([]byte(`{"type":"ping","payload":""}` + "\n")); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}()

	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			flusher.Flush()
		}
		if err != nil {
			return
		}
	}
}

func urlQueryEscape(s string) string {
	// minimal escape for level values
	switch s {
	case "debug", "info", "warning", "error", "silent":
		return s
	default:
		return "info"
	}
}

type simpleError string

func (e simpleError) Error() string { return string(e) }

const errMethod = simpleError("method not allowed")
