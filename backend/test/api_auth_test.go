package test

import (
	"net/http"
	"testing"
)

// The whole point of the session refactor: the password buys a token at
// /api/login and nothing else. Every other way in must be gone.
func TestOnlySessionTokensAreAccepted(t *testing.T) {
	e := newEnv(t)
	token := e.login()

	cases := []struct {
		name string
		set  func(*http.Request)
		want int
	}{
		{"session token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }, 200},
		{"no credentials", func(r *http.Request) {}, 401},
		{"bearer password", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+testPassword) }, 401},
		{"x-ui-password header", func(r *http.Request) { r.Header.Set("X-UI-Password", testPassword) }, 401},
		{"basic auth", func(r *http.Request) { r.SetBasicAuth("x", testPassword) }, 401},
		{"forged token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+forgedToken) }, 401},
		{"empty bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer ") }, 401},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", e.HTTP.URL+"/api/overview", nil)
			if err != nil {
				t.Fatal(err)
			}
			tc.set(req)
			res, err := e.HTTP.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", res.StatusCode, tc.want)
			}
		})
	}
}

const forgedToken = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// The password must never be accepted from the query string: URLs reach access
// logs, proxy logs, browser history and Referer headers.
func TestQueryStringCredentialsRejected(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{
		"/api/overview?token=" + testPassword,
		"/api/logs?token=" + testPassword,
		"/api/traffic?token=" + testPassword,
	} {
		res := e.do("GET", path, "", "")
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatalf("%s: status = %d, want 401", path, res.StatusCode)
		}
	}
}

func TestLoginRejectsWrongPasswordAndIssuesUniqueTokens(t *testing.T) {
	e := newEnv(t)

	code, body := e.json("POST", "/api/login", "", `{"password":"wrong"}`)
	if code != 401 {
		t.Fatalf("wrong password: status = %d, want 401", code)
	}
	if body["token"] != nil {
		t.Fatal("a token was issued for a wrong password")
	}

	a, b := e.login(), e.login()
	if a == b {
		t.Fatal("two logins returned the same token")
	}
	if a == testPassword || b == testPassword {
		t.Fatal("the token is the password itself")
	}
	if len(a) < 32 {
		t.Fatalf("token looks too short to be random: %q", a)
	}
	// Both sessions stay valid; issuing one must not evict the other.
	for _, tok := range []string{a, b} {
		res := e.do("GET", "/api/overview", tok, "")
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("token %q rejected: %d", tok, res.StatusCode)
		}
	}
}

func TestLogoutRevokesOnlyTheCallersSession(t *testing.T) {
	e := newEnv(t)
	mine, other := e.login(), e.login()

	res := e.do("POST", "/api/logout", mine, "")
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("logout status = %d", res.StatusCode)
	}

	res = e.do("GET", "/api/overview", mine, "")
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("revoked token still works: %d", res.StatusCode)
	}
	res = e.do("GET", "/api/overview", other, "")
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("logout revoked an unrelated session: %d", res.StatusCode)
	}

	// Idempotent, and harmless without a token.
	for _, tok := range []string{mine, ""} {
		res = e.do("POST", "/api/logout", tok, "")
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("repeat logout status = %d", res.StatusCode)
		}
	}
	res = e.do("GET", "/api/logout", "", "")
	res.Body.Close()
	if res.StatusCode != 405 {
		t.Fatalf("GET /api/logout = %d, want 405", res.StatusCode)
	}
}

func TestPublicEndpointsNeedNoToken(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/api/health", "/api/auth/check"} {
		res := e.do("GET", path, "", "")
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("%s: status = %d, want 200", path, res.StatusCode)
		}
	}
}

func TestAuthCheckReportsSessionValidity(t *testing.T) {
	e := newEnv(t)

	_, body := e.json("GET", "/api/auth/check", "", "")
	if body["required"] != true || body["ok"] != false {
		t.Fatalf("unauthenticated check = %v", body)
	}
	_, body = e.json("GET", "/api/auth/check", e.login(), "")
	if body["required"] != true || body["ok"] != true {
		t.Fatalf("authenticated check = %v", body)
	}
}

// With no password configured the panel is deliberately open; login says so
// instead of handing out a token.
func TestOpenPanelWhenPasswordEmpty(t *testing.T) {
	e := newEnv(t)
	e.Server.UIPassword = ""

	res := e.do("GET", "/api/overview", "", "")
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("open panel rejected a request: %d", res.StatusCode)
	}

	_, body := e.json("POST", "/api/login", "", `{"password":"anything"}`)
	if body["auth"] != false {
		t.Fatalf("login should report auth:false, got %v", body)
	}
	if body["token"] != nil {
		t.Fatal("a token was issued while no password is set")
	}
	_, body = e.json("GET", "/api/auth/check", "", "")
	if body["required"] != false {
		t.Fatalf("auth/check should report required:false, got %v", body)
	}
}

// No CORS headers at all: the panel serves its own SPA, so every legitimate
// request is same-origin and a permissive header would only help an attacker.
func TestNoCORSHeadersAreEverSent(t *testing.T) {
	e := newEnv(t)
	token := e.login()

	req, err := http.NewRequest("GET", e.HTTP.URL+"/api/overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := e.HTTP.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	for h := range res.Header {
		if len(h) > 14 && h[:14] == "Access-Control" {
			t.Fatalf("response carries %s: %q", h, res.Header.Get(h))
		}
	}
}

func TestUnauthorizedRepliesJSONAndChallenge(t *testing.T) {
	e := newEnv(t)
	res := e.do("GET", "/api/overview", "", "")
	defer res.Body.Close()

	if got := res.Header.Get("WWW-Authenticate"); got == "" {
		t.Fatal("401 carries no WWW-Authenticate challenge")
	}
	if ct := res.Header.Get("Content-Type"); ct == "" || ct[:16] != "application/json" {
		t.Fatalf("401 content-type = %q", ct)
	}
}
