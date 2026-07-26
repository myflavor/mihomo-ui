package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/xin/mihomo-ui/internal/configgen"
	"github.com/xin/mihomo-ui/internal/configsvc"
	"github.com/xin/mihomo-ui/internal/store"
)

// defaultSource infers where a config comes from when the client did not say.
func defaultSource(explicit string, content []byte) string {
	if explicit != "" {
		return explicit
	}
	if len(content) > 0 {
		return "file"
	}
	return "url"
}

func (s *Server) handleConfigList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errMethod)
		return
	}
	active, _ := s.Store.Active()
	writeJSON(w, 200, map[string]any{
		"configs": s.Store.List(),
		"active":  active,
	})
}

func (s *Server) handleConfigCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	// support JSON or multipart (file upload)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		s.handleConfigUpload(w, r, "")
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
	source := defaultSource(body.Source, []byte(body.Content))
	cfg, err := s.Store.Add(body.Name, body.URL, source, body.Interval)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	bres, berr := s.materializeConfigRaw(cfg, []byte(body.Content), source, body.URL)
	if berr != nil {
		_ = s.Store.Delete(cfg.ID)
		s.Config.DeleteRaw(cfg.ID)
		writeJSON(w, 400, map[string]any{"error": berr.Error(), "detail": bres})
		return
	}
	// add only caches raw; do not switch active unless caller asks
	if body.Activate != nil && *body.Activate {
		active, res, err := s.Config.Activate(cfg.ID)
		// We just created this id, so a miss is our bug, not the client's: 500 not 404.
		if errors.Is(err, configsvc.ErrNoSuchConfig) {
			writeErr(w, 500, err)
			return
		}
		writeConfigApply(w, 201, active, res, err)
		return
	}
	writeJSON(w, 201, map[string]any{"config": cfg, "ok": true})
}

func (s *Server) handleConfigUpload(w http.ResponseWriter, r *http.Request, existingID string) {
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeErr(w, 400, err)
		return
	}
	name := r.FormValue("name")
	if name == "" {
		writeJSON(w, 400, map[string]string{"error": "name required"})
		return
	}
	urlStr := r.FormValue("url")
	interval := parseIntervalForm(r.FormValue("interval")) // 0 = no auto update

	var content []byte
	if f, _, err := r.FormFile("file"); err == nil {
		defer f.Close()
		content, _ = io.ReadAll(f)
	}
	if content == nil && r.FormValue("content") != "" {
		content = []byte(r.FormValue("content"))
	}
	source := defaultSource(r.FormValue("source"), content)

	var cfg store.Config
	var err error
	if existingID == "" {
		cfg, err = s.Store.Add(name, urlStr, source, interval)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
	} else {
		cfg, err = s.Store.Update(existingID, store.ConfigPatch{
			Name: &name, URL: &urlStr, Source: &source, Interval: &interval,
		})
		if err != nil {
			writeErr(w, 404, err)
			return
		}
	}
	// An uploaded body makes this a local config regardless of what was asked for.
	if len(content) > 0 {
		source = "file"
		cfg, _ = s.Store.Update(cfg.ID, store.ConfigPatch{Source: &source})
	}
	bres, berr := s.materializeConfigRaw(cfg, content, source, urlStr)
	if berr != nil {
		// create path: roll back store entry + any partial local file (match JSON create)
		if existingID == "" {
			_ = s.Store.Delete(cfg.ID)
			s.Config.DeleteRaw(cfg.ID)
		}
		writeJSON(w, 400, map[string]any{"error": berr.Error(), "detail": bres})
		return
	}

	// activate only when explicitly requested (create never auto-switches)
	if r.FormValue("activate") == "1" || r.FormValue("activate") == "true" {
		active, res, err := s.Config.Activate(cfg.ID)
		if active.ID != "" {
			cfg = active
		}
		writeConfigApply(w, 200, cfg, res, err)
		return
	}
	writeJSON(w, 200, map[string]any{"config": cfg, "ok": true})
}

// handleConfigItem routes /api/config/{id} and /api/config/{id}/{action}.
func (s *Server) handleConfigItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/config/"), "/")
	if rest == "" || rest == "apply" || rest == "refresh" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(rest, "/")
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case action == "activate" && r.Method == http.MethodPost:
		s.activateConfig(w, id)
	case action == "refresh" && r.Method == http.MethodPost:
		s.refreshConfig(w, r, id)
	case action == "upload" && r.Method == http.MethodPost:
		s.handleConfigUpload(w, r, id)
	case action == "raw":
		s.handleConfigRaw(w, r, id)
	case action != "":
		http.NotFound(w, r)
	case r.Method == http.MethodDelete:
		s.deleteConfig(w, id)
	case r.Method == http.MethodPut:
		s.updateConfig(w, r, id)
	default:
		writeErr(w, 405, errMethod)
	}
}

// activateConfig installs from the local raw file — no re-download on switch.
func (s *Server) activateConfig(w http.ResponseWriter, id string) {
	cfg, res, err := s.Config.Activate(id)
	if errors.Is(err, configsvc.ErrNoSuchConfig) {
		writeErr(w, 404, err)
		return
	}
	writeConfigApply(w, 200, cfg, res, err)
}

// refreshConfig re-downloads the raw file for one URL config.
func (s *Server) refreshConfig(w http.ResponseWriter, r *http.Request, id string) {
	cfg, err := s.Store.Get(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	if cfg.Source == "file" || cfg.URL == "" {
		writeJSON(w, 400, map[string]string{"error": "本地文件无需更新"})
		return
	}
	res, err := s.Config.Refresh(cfg, true)
	if err != nil {
		writeJSON(w, 200, map[string]any{
			"config": cfg,
			"ok":     false,
			"error":  err.Error(),
			"detail": res,
		})
		return
	}
	// touch updatedAt
	cfg, _ = s.Store.Update(id, store.ConfigPatch{})
	if !cfg.Active {
		writeJSON(w, 200, map[string]any{"config": cfg, "ok": true, "detail": res})
		return
	}
	errs := s.updateAllProviders(r.Context())
	if res != nil {
		errs = append(errs, res.Failed...)
	}
	writeJSON(w, 200, map[string]any{
		"config": cfg,
		"ok":     len(errs) == 0,
		"detail": res,
		"errors": errs,
	})
}

func (s *Server) deleteConfig(w http.ResponseWriter, id string) {
	if err := s.Store.Delete(id); err != nil {
		writeErr(w, 404, err)
		return
	}
	s.Config.DeleteRaw(id)
	// re-apply new active (if any)
	res, err := s.Config.ApplyActive(false)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": "1", "apply": map[string]any{"ok": "0", "error": err.Error(), "detail": res}})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": "1", "apply": map[string]any{"ok": "1", "detail": res}})
}

func (s *Server) updateConfig(w http.ResponseWriter, r *http.Request, id string) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		s.handleConfigUpload(w, r, id)
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
	cfg, err := s.Store.Update(id, store.ConfigPatch{
		Name: body.Name, URL: body.URL, Source: body.Source, Interval: body.Interval,
	})
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	if body.Content != nil {
		if err := s.Config.SaveRaw(cfg, []byte(*body.Content)); err != nil {
			writeErr(w, 400, err)
			return
		}
		// do NOT force source=file — editing raw of a URL cfg keeps source=url
	}
	// Always re-run the pipeline on edit rather than guess whether remote changed:
	// URL configs re-download, local ones reinstall, active ones also hot reload.
	forceRefresh := cfg.Source != "file" && cfg.URL != "" && body.Content == nil

	// re-apply only if this is the active one, or activate requested
	if body.Activate != nil && *body.Activate {
		active, res, err := s.Config.ActivateWithRefresh(id, forceRefresh)
		// The Update above proved the id exists, so a miss now is inconsistency.
		if errors.Is(err, configsvc.ErrNoSuchConfig) {
			writeErr(w, 500, err)
			return
		}
		if active.ID != "" {
			cfg = active
		}
		writeConfigApply(w, 200, cfg, res, err)
		return
	}
	res, err := s.Config.Refresh(cfg, forceRefresh)
	writeConfigApply(w, 200, cfg, res, err)
}

func (s *Server) handleConfigRaw(w http.ResponseWriter, r *http.Request, id string) {
	cfg, err := s.Store.Get(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	path := configgen.LocalConfigPath(s.ConfigDir, id)
	switch r.Method {
	case http.MethodGet:
		raw, err := configgen.ReadLocalConfigRaw(s.ConfigDir, id)
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "尚未缓存原始配置，请先更新或重新添加", "path": path})
			return
		}
		writeJSON(w, 200, map[string]any{
			"id":      cfg.ID,
			"name":    cfg.Name,
			"source":  cfg.Source,
			"path":    path,
			"content": string(raw),
			"active":  cfg.Active,
		})
	case http.MethodPut, http.MethodPost:
		body, err := readContentBody(r)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		if strings.TrimSpace(body.Content) == "" {
			writeJSON(w, 400, map[string]string{"error": "content required"})
			return
		}
		if err := s.Config.SaveRaw(cfg, []byte(body.Content)); err != nil {
			writeErr(w, 400, err)
			return
		}
		// always reinstall from edited raw when active
		res, err := s.Config.Refresh(cfg, false)
		if err != nil {
			writeJSON(w, 200, map[string]any{
				"ok":      "0",
				"path":    path,
				"error":   err.Error(),
				"detail":  res,
				"applied": cfg.Active,
			})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": "1", "path": path, "applied": cfg.Active, "detail": res})
	default:
		writeErr(w, 405, errMethod)
	}
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	res, err := s.Config.ApplyActive(false)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": "1", "config-path": s.ConfigPath, "detail": res})
}

func (s *Server) handleRefreshConfigs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}

	// Re-download every URL config, then install the active one and its providers.
	// EnsureRaw touches only per-config files; ApplyActive serializes config.yaml.
	result := &configgen.ApplyResult{}
	var errs []string
	refreshed := 0
	skipped := 0

	for _, cfg := range s.Store.List() {
		if cfg.Source == "file" || cfg.URL == "" {
			skipped++
			continue
		}
		br, err := s.Config.EnsureRaw(cfg, true)
		if br != nil {
			result.Warnings = append(result.Warnings, br.Warnings...)
			result.Failed = append(result.Failed, br.Failed...)
		}
		if err != nil {
			errs = append(errs, cfg.Name+": "+err.Error())
			continue
		}
		// touch updatedAt
		_, _ = s.Store.Update(cfg.ID, store.ConfigPatch{})
		refreshed++
		result.OK++
	}

	// Install current active (from refreshed raw if any; file active still reinstalls).
	ir, err := s.Config.ApplyActive(false)
	if ir != nil {
		result.OK += ir.OK
		result.Failed = append(result.Failed, ir.Failed...)
		result.Warnings = append(result.Warnings, ir.Warnings...)
	}
	if err != nil {
		errs = append(errs, err.Error())
	}

	errs = append(errs, s.updateAllProviders(r.Context())...)
	errs = append(errs, result.Failed...)

	writeJSON(w, 200, map[string]any{
		"ok":          len(errs) == 0,
		"config-path": s.ConfigPath,
		"refreshed":   refreshed,
		"skipped":     skipped,
		"detail":      result,
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
		errs := s.updateAllProviders(r.Context())
		writeJSON(w, 200, map[string]any{"ok": len(errs) == 0, "errors": errs})
		return
	}
	if err := s.Mihomo.UpdateProvider(r.Context(), body.Name); err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}
