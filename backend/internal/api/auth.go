package api

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// bearerToken pulls the token out of an Authorization: Bearer header.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if !strings.HasPrefix(h, p) {
		return ""
	}
	return strings.TrimSpace(h[len(p):])
}

func samePassword(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// authorized takes session tokens only; POST /api/login is the one place that
// accepts the password. Never from the query string, which leaks into logs.
func (s *Server) authorized(r *http.Request) bool {
	if s.UIPassword == "" {
		return true
	}
	return s.sessions.valid(bearerToken(r))
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
	if !samePassword(body.Password, s.UIPassword) {
		writeJSON(w, 401, map[string]string{"error": "密码错误"})
		return
	}
	// Hand back a session token, never the password: it lands in localStorage.
	token, err := s.sessions.issue()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "auth": true, "token": token})
}

// handleLogout revokes the caller's session. Unauthenticated on purpose:
// presenting a token is all it takes to give that same token up.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	s.sessions.revoke(bearerToken(r))
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

func (s *Server) handleAuthCheck(w http.ResponseWriter, r *http.Request) {
	required := s.UIPassword != ""
	ok := !required || s.authorized(r)
	writeJSON(w, 200, map[string]any{
		"required": required,
		"ok":       ok,
	})
}
