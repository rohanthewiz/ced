// =============================================================================
// File: internal/gonotes/gonotes_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the GoNotes client. Every one of them talks to an
// httptest.Server standing in for GoNotes — nothing here may reach a
// real server, and NewAt is the seam that guarantees it. The token cache
// is redirected at a temp file for the same reason: a test run must
// never read or rewrite the developer's ~/.gonotes/.api_token.

package gonotes

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// isolate points the credential and token-cache environment at this
// test's own values, restoring whatever was there afterwards. Every test
// calls it: without it a developer with GONOTES_PASSWORD exported would
// get different results than CI.
func isolate(t *testing.T, user, pass string) string {
	t.Helper()
	tokenFile := filepath.Join(t.TempDir(), ".api_token")
	t.Setenv(EnvUser, user)
	t.Setenv(EnvSyncUser, "")
	t.Setenv(EnvPassword, pass)
	t.Setenv(EnvPasswordB64, "")
	t.Setenv(EnvTokenFile, tokenFile)
	return tokenFile
}

// noteRequest is the decoded body of a create call, for assertions.
type noteRequest struct {
	GUID        string  `json:"guid"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
	Body        *string `json:"body"`
	Tags        *string `json:"tags"`
	IsPrivate   bool    `json:"is_private"`
}

// ok writes GoNotes' success envelope around data.
func ok(w http.ResponseWriter, data any) {
	raw, _ := json.Marshal(data)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": json.RawMessage(raw)})
}

// fail writes GoNotes' failure envelope with a status.
func fail(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": msg})
}

// TestCreate_SendsTheNoteAndReportsWhatCameBack is the happy path: a
// cached token is used as-is (no login round trip), the body carries
// exactly the fields ced fills, and the server's id comes back.
func TestCreate_SendsTheNoteAndReportsWhatCameBack(t *testing.T) {
	tokenFile := isolate(t, "u", "p")
	if err := os.WriteFile(tokenFile, []byte("cached-jwt\n"), 0o600); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	var got noteRequest
	var auth string
	var logins int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login" {
			logins++
			ok(w, map[string]any{"token": "fresh"})
			return
		}
		if r.URL.Path != "/api/v1/notes" || r.Method != http.MethodPost {
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("decode note: %v", err)
		}
		ok(w, map[string]any{"id": 42, "guid": got.GUID, "title": got.Title})
	}))
	defer srv.Close()

	res, err := NewAt(srv.URL).Create(Note{
		Title:       "A title",
		Body:        "line one\nline two",
		Description: "ced: proj/main.go:1-2",
		Tags:        "ced",
		Private:     true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ID != 42 || res.Title != "A title" {
		t.Errorf("result = %+v", res)
	}
	if logins != 0 {
		t.Errorf("logged in %d times with a valid cached token — that round trip is the one the cache exists to avoid", logins)
	}
	if auth != "Bearer cached-jwt" {
		t.Errorf("Authorization = %q, want the cached token", auth)
	}
	if got.Body == nil || *got.Body != "line one\nline two" {
		t.Errorf("body = %v — the selection must go out verbatim", got.Body)
	}
	if got.Description == nil || *got.Description != "ced: proj/main.go:1-2" {
		t.Errorf("description = %v", got.Description)
	}
	if got.Tags == nil || *got.Tags != "ced" {
		t.Errorf("tags = %v", got.Tags)
	}
	if !got.IsPrivate {
		t.Error("is_private was dropped")
	}
}

// TestCreate_ReloginsOnceOnAnExpiredToken pins the auth ladder: a 401
// spends exactly one login and one retry, and the fresh token is cached
// so the next capture skips both.
func TestCreate_ReloginsOnceOnAnExpiredToken(t *testing.T) {
	tokenFile := isolate(t, "u", "p")
	if err := os.WriteFile(tokenFile, []byte("stale"), 0o600); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	var creates, logins int
	var lastAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			logins++
			if r.Header.Get("Authorization") != "" {
				t.Error("the login call must not carry a known-bad token")
			}
			var creds map[string]string
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &creds)
			if creds["username"] != "u" || creds["password"] != "p" {
				t.Errorf("credentials = %v", creds)
			}
			ok(w, map[string]any{"token": "fresh-jwt"})
		case "/api/v1/notes":
			creates++
			lastAuth = r.Header.Get("Authorization")
			if creates == 1 {
				fail(w, http.StatusUnauthorized, "token expired")
				return
			}
			ok(w, map[string]any{"id": 7})
		}
	}))
	defer srv.Close()

	res, err := NewAt(srv.URL).Create(Note{Title: "T", Body: "b"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ID != 7 {
		t.Errorf("id = %d, want 7", res.ID)
	}
	if creates != 2 || logins != 1 {
		t.Errorf("creates=%d logins=%d, want 2 and 1 — one retry, never a loop", creates, logins)
	}
	if lastAuth != "Bearer fresh-jwt" {
		t.Errorf("retry Authorization = %q", lastAuth)
	}
	cached, err := os.ReadFile(tokenFile)
	if err != nil || strings.TrimSpace(string(cached)) != "fresh-jwt" {
		t.Errorf("token cache = %q (%v), want the fresh token written back", cached, err)
	}
}

// TestCreate_NoCredentialsSaysHowToFixIt: a 401 with nothing to log in
// with is a dead end, so the error names the environment variables
// rather than repeating "unauthorized".
func TestCreate_NoCredentialsSaysHowToFixIt(t *testing.T) {
	isolate(t, "", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusUnauthorized, "authentication required")
	}))
	defer srv.Close()

	_, err := NewAt(srv.URL).Create(Note{Title: "T", Body: "b"})
	if !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("err = %v, want ErrNoCredentials", err)
	}
	if !strings.Contains(err.Error(), EnvUser) || !strings.Contains(err.Error(), EnvPassword) {
		t.Errorf("error %q names neither variable a user would have to set", err)
	}
}

// TestCreate_ServerErrorIsReportedVerbatim: GoNotes' own message is what
// the user can act on ("note with this guid already exists" means
// something quite different from "database error"), so it must survive
// the trip out.
func TestCreate_ServerErrorIsReportedVerbatim(t *testing.T) {
	isolate(t, "u", "p")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusConflict, "note with this guid already exists")
	}))
	defer srv.Close()

	_, err := NewAt(srv.URL).Create(Note{Title: "T", Body: "b"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want the server's own words", err)
	}
}

// TestCreate_NonJSONReplyNamesTheAddress: something else answering on
// the port is the likeliest misconfiguration, and a JSON decode error
// would send the user hunting in the wrong place.
func TestCreate_NonJSONReplyNamesTheAddress(t *testing.T) {
	isolate(t, "u", "p")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>nginx</html>"))
	}))
	defer srv.Close()

	_, err := NewAt(srv.URL).Create(Note{Title: "T", Body: "b"})
	if err == nil {
		t.Fatal("a proxy error page should not read as success")
	}
	if !strings.Contains(err.Error(), srv.URL) || !strings.Contains(err.Error(), "GoNotes server") {
		t.Errorf("error %q should name the address that answered and doubt it is GoNotes", err)
	}
}

// TestCreate_RefusesAnEmptyTitle locally rather than spending a round
// trip to be told the same thing — the API requires one.
func TestCreate_RefusesAnEmptyTitle(t *testing.T) {
	isolate(t, "u", "p")
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		ok(w, map[string]any{"id": 1})
	}))
	defer srv.Close()

	if _, err := NewAt(srv.URL).Create(Note{Title: "   ", Body: "b"}); err == nil {
		t.Fatal("an empty title should be refused")
	}
	if called {
		t.Error("a refusal must not cost a round trip")
	}
}

// TestURL_HonorsTheEnvironment pins the one knob: the default is a
// guess at a local server, GONOTES_URL is an instruction.
func TestURL_HonorsTheEnvironment(t *testing.T) {
	t.Setenv(EnvURL, "")
	if got := URL(); got != DefaultURL {
		t.Errorf("URL() = %q, want the default %q", got, DefaultURL)
	}
	t.Setenv(EnvURL, "http://box:9000/")
	if got := URL(); got != "http://box:9000" {
		t.Errorf("URL() = %q, want the trailing slash trimmed", got)
	}
}

// TestEnvCredentials_DecodesTheBase64Form: a configured sync spoke holds
// its password base64-encoded, and a user who set that up should not
// have to set a second variable for ced.
func TestEnvCredentials_DecodesTheBase64Form(t *testing.T) {
	t.Setenv(EnvUser, "")
	t.Setenv(EnvSyncUser, "spoke")
	t.Setenv(EnvPassword, "")
	t.Setenv(EnvPasswordB64, "c2VjcmV0") // "secret"
	user, pass := envCredentials()
	if user != "spoke" || pass != "secret" {
		t.Errorf("credentials = %q/%q, want spoke/secret", user, pass)
	}
}

// TestNewGUID_IsAVersion4UUIDAndUnique. The server treats it as an
// opaque key, so shape plus non-collision is the whole contract.
func TestNewGUID_IsAVersion4UUIDAndUnique(t *testing.T) {
	shape := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		g, err := newGUID()
		if err != nil {
			t.Fatalf("newGUID: %v", err)
		}
		if !shape.MatchString(g) {
			t.Fatalf("guid %q is not a v4 UUID", g)
		}
		if seen[g] {
			t.Fatalf("guid %q repeated", g)
		}
		seen[g] = true
	}
}

// TestCacheToken_IsBestEffort: a token cache that cannot be written must
// cost the cache, never the note that was just saved.
func TestCacheToken_IsBestEffort(t *testing.T) {
	c := NewAt("http://example.invalid")
	c.tokenFile = filepath.Join(t.TempDir(), "no-such-dir", "\x00bad", "token")
	c.cacheToken("jwt") // must not panic
	if got := c.cachedToken(); got != "" {
		t.Errorf("cachedToken = %q, want empty", got)
	}
}
