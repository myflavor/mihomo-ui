package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/xin/mihomo-ui/internal/configgen"
)

// patchKernel pushes a setting to mihomo, saves it as a pref, then mirrors it
// into config.yaml. Returns false having already written the error response.
// Both writes are best-effort: the kernel already took the value.
//
// persist must run before the mirror. A config apply rebuilds config.yaml from
// the prefs, so with the order reversed it would regenerate from the old value
// and silently undo the setting we just wrote.
func (s *Server) patchKernel(w http.ResponseWriter, r *http.Request, patch map[string]any, persist func()) bool {
	if err := s.Mihomo.PatchConfigs(r.Context(), patch); err != nil {
		writeErr(w, 502, err)
		return false
	}
	persist()
	_ = s.Config.PatchRuntime(patch)
	return true
}

func (s *Server) handleMode(w http.ResponseWriter, r *http.Request) {
	if !isWriteMethod(r) {
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
	if !s.patchKernel(w, r, map[string]any{"mode": mode}, func() { _ = s.Store.SetMode(mode) }) {
		return
	}
	writeJSON(w, 200, map[string]string{"mode": mode})
}

// handleLogLevel sets the kernel log-level (debug|info|warning|error|silent).
func (s *Server) handleLogLevel(w http.ResponseWriter, r *http.Request) {
	if !isWriteMethod(r) {
		writeErr(w, 405, errMethod)
		return
	}
	var body struct {
		Level string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err)
		return
	}
	level := strings.ToLower(strings.TrimSpace(body.Level))
	switch level {
	case "debug", "info", "warning", "error", "silent":
	default:
		writeJSON(w, 400, map[string]string{"error": "level must be debug|info|warning|error|silent"})
		return
	}
	if !s.patchKernel(w, r, map[string]any{"log-level": level}, func() { _ = s.Store.SetLogLevel(level) }) {
		return
	}
	writeJSON(w, 200, map[string]string{"level": level})
}

func (s *Server) handleTun(w http.ResponseWriter, r *http.Request) {
	if !isWriteMethod(r) {
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
	patch := map[string]any{"tun": map[string]any{"enable": *body.Enable}}
	if !s.patchKernel(w, r, patch, func() { _ = s.Store.SetTunEnable(*body.Enable) }) {
		return
	}
	writeJSON(w, 200, map[string]bool{"enable": *body.Enable})
}

// handleRuntime reads/writes the kernel mihomo/config.yaml.
func (s *Server) handleRuntime(w http.ResponseWriter, r *http.Request) {
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
		body, err := readContentBody(r)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		if strings.TrimSpace(body.Content) == "" {
			writeJSON(w, 400, map[string]string{"error": "content required"})
			return
		}
		if err := configgen.ValidateYAML([]byte(body.Content)); err != nil {
			writeErr(w, 400, err)
			return
		}
		reload := body.Reload == nil || *body.Reload
		werr, reloadErr := s.Config.WriteRaw([]byte(body.Content), reload)
		if werr != nil {
			writeErr(w, 500, werr)
			return
		}
		if reloadErr != nil {
			writeJSON(w, 200, map[string]any{
				"ok":    "0",
				"path":  s.ConfigPath,
				"error": reloadErr.Error(),
			})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": "1", "path": s.ConfigPath, "reloaded": reload})
	default:
		writeErr(w, 405, errMethod)
	}
}
