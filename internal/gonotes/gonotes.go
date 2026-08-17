// =============================================================================
// File: internal/gonotes/gonotes.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Package gonotes is a minimal client for a running GoNotes server's v1
// REST API — enough to turn a selection (or a whole file) into a new
// note, and nothing else.
//
// WHY HTTP AND NOT THE DATABASE. GoNotes keeps its notes in two bytdb
// files under ~/.gonotes/data, and bytdb is SINGLE-WRITER: the process
// that holds those files holds them exclusively. A second writer is not
// a race to be careful about, it is a file that won't open. The server
// (or the MacApp that embeds it) is the process that legitimately owns
// them, so HTTP is the only safe path to the same notes — the same
// conclusion GoNotes' own TUI reached for its cats-hosted mode.
//
// WHY NO CONFIG KEY. Every knob here is an ENVIRONMENT VARIABLE, and
// they are the ones GoNotes' own clients already read (GONOTES_URL,
// GONOTES_USER / GONOTES_SYNC_USERNAME, GONOTES_PASSWORD /
// GONOTES_SYNC_PASSWORD_B64, GONOTES_TOKEN_FILE). A user who has
// configured a spoke, or who runs the TUI, has already set these; a ced
// config key would be a second place to say the same thing and a second
// place for it to be wrong. It also keeps the credential out of a file
// ced writes.
//
// WHY THE TOKEN FILE IS SHARED. ~/.gonotes/.api_token is where every
// GoNotes client caches its JWT, so a user who logged in through the TUI
// is already logged in here. Writing it back is best-effort and 0600
// under a 0700 parent, matching what those clients do.
//
// Stdlib only, like every other integration in this editor — no SDK, no
// CGO, nothing that costs the single static binary.
package gonotes

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Environment variables, all shared with GoNotes' own clients.
const (
	EnvURL         = "GONOTES_URL"
	EnvUser        = "GONOTES_USER"
	EnvSyncUser    = "GONOTES_SYNC_USERNAME"
	EnvPassword    = "GONOTES_PASSWORD"
	EnvPasswordB64 = "GONOTES_SYNC_PASSWORD_B64"
	EnvTokenFile   = "GONOTES_TOKEN_FILE"
)

// DefaultURL is where a locally running GoNotes server listens. It is a
// GUESS, deliberately: a user who runs the server elsewhere sets
// GONOTES_URL, and everyone else gets the common case working with no
// configuration at all.
const DefaultURL = "http://localhost:8444"

// requestTimeout covers real work — a note whose body is a whole source
// file — rather than a liveness check. Generous enough that a busy
// server doesn't lose the note, short enough that a wedged one doesn't
// hold ced's goroutine for the rest of the session.
const requestTimeout = 15 * time.Second

// ErrNoCredentials is the "I can't even try" verdict: no cached token
// and no username/password in the environment. It is reported to the
// user verbatim, so it names the variables rather than the concept.
var ErrNoCredentials = errors.New(
	"not signed in to GoNotes — set " + EnvUser + " and " + EnvPassword +
		", or sign in once with the GoNotes TUI to cache a token")

// Note is what ced sends. It is a strict subset of GoNotes' NoteInput:
// the fields a "capture this text" gesture can honestly fill, and no
// more. Everything else on that struct describes editing history or
// sync state that belongs to the server.
type Note struct {
	// Title is required by the API. The app layer always has one — the
	// user typed it or the agent drafted it — so an empty title is a
	// bug here rather than a server round trip to discover.
	Title string

	// Body is the captured text, verbatim. The selection IS the body:
	// nothing is prepended to it, because a note the user later edits
	// should not carry a header ced invented.
	Body string

	// Description carries the provenance (path and line range) instead,
	// which is where GoNotes shows a one-line subtitle and where it does
	// not get in the way of the text.
	Description string

	// Tags is GoNotes' free-form tag string. ced stamps its own name so
	// captured notes are findable as a set.
	Tags string

	// Private routes the note into GoNotes' private (optionally
	// encrypted-at-rest) database. Off by default: it is the user's
	// per-note call, made on the prompt.
	Private bool
}

// Result is what a successful create reports back — enough for the
// flash to name what appeared, not a mirror of the server's note.
type Result struct {
	ID    int64
	GUID  string
	Title string
}

// Client talks to one GoNotes server.
//
// It is single-use by design: the app builds one per send, on the
// goroutine that performs it, so there is no shared mutable state to
// guard and no connection to keep warm. A note capture is a
// once-in-a-while gesture, not a hot path.
type Client struct {
	baseURL   string
	hc        *http.Client
	tokenFile string

	token string
	// user/pass are read from the environment at construction so a
	// 401 can be answered without going back to the environment
	// mid-flight (and so tests can point one Client at a stub server
	// without mutating the process environment twice).
	user, pass string
}

// URL reports the server ced will talk to, honoring GONOTES_URL.
// Exported because the app layer names it in error messages: "connection
// refused" is only actionable once the user knows which address was
// tried.
func URL() string {
	if u := strings.TrimRight(strings.TrimSpace(os.Getenv(EnvURL)), "/"); u != "" {
		return u
	}
	return DefaultURL
}

// New builds a client for the configured server.
func New() *Client { return NewAt(URL()) }

// NewAt builds a client for an explicit base URL — the seam tests use to
// point at an httptest.Server without touching the environment.
func NewAt(baseURL string) *Client {
	user, pass := envCredentials()
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		hc:        &http.Client{Timeout: requestTimeout},
		tokenFile: tokenFilePath(),
		user:      user,
		pass:      pass,
	}
}

// envCredentials reads the credential pair GoNotes' clients share,
// including the base64 form a configured sync spoke already holds.
func envCredentials() (user, pass string) {
	user = strings.TrimSpace(os.Getenv(EnvUser))
	if user == "" {
		user = strings.TrimSpace(os.Getenv(EnvSyncUser))
	}
	pass = os.Getenv(EnvPassword)
	if pass == "" {
		if b64 := strings.TrimSpace(os.Getenv(EnvPasswordB64)); b64 != "" {
			if decoded, err := base64.StdEncoding.DecodeString(b64); err == nil {
				pass = string(decoded)
			}
		}
	}
	return user, pass
}

// tokenFilePath resolves the shared JWT cache. A missing HOME is not
// fatal: an empty path simply disables caching, and every send logs in.
func tokenFilePath() string {
	if p := strings.TrimSpace(os.Getenv(EnvTokenFile)); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".gonotes", ".api_token")
}

// -----------------------------------------------------------------------------
// The one verb
// -----------------------------------------------------------------------------

// Create posts a new note and returns what the server made of it.
//
// The authentication dance is the same one every GoNotes client runs,
// and it is ordered so the common case costs no extra round trip: try
// the cached token, and only on a 401 spend a login. There is no retry
// LOOP — a wrong password re-sent forever is worse than one visible
// failure.
func (c *Client) Create(n Note) (Result, error) {
	if strings.TrimSpace(n.Title) == "" {
		return Result{}, errors.New("a note needs a title")
	}

	guid, err := newGUID()
	if err != nil {
		return Result{}, err
	}
	// GoNotes' NoteInput takes pointers for the optional strings so an
	// absent field is distinguishable from an empty one. ced always has
	// a body and always stamps a tag, so only the description can be
	// genuinely absent.
	body := struct {
		GUID        string  `json:"guid"`
		Title       string  `json:"title"`
		Description *string `json:"description,omitempty"`
		Body        *string `json:"body,omitempty"`
		Tags        *string `json:"tags,omitempty"`
		IsPrivate   bool    `json:"is_private"`
	}{
		GUID:      guid,
		Title:     strings.TrimSpace(n.Title),
		Body:      &n.Body,
		IsPrivate: n.Private,
	}
	if d := strings.TrimSpace(n.Description); d != "" {
		body.Description = &d
	}
	if t := strings.TrimSpace(n.Tags); t != "" {
		body.Tags = &t
	}

	c.token = c.cachedToken()

	var out struct {
		ID    int64  `json:"id"`
		GUID  string `json:"guid"`
		Title string `json:"title"`
	}
	err = c.do(http.MethodPost, "/api/v1/notes", body, &out)
	if err != nil {
		var ae *apiError
		if !errors.As(err, &ae) || ae.status != http.StatusUnauthorized {
			return Result{}, err
		}
		// The cached token is stale (or there wasn't one). Spend a
		// login and retry exactly once.
		if loginErr := c.login(); loginErr != nil {
			return Result{}, loginErr
		}
		if err = c.do(http.MethodPost, "/api/v1/notes", body, &out); err != nil {
			return Result{}, err
		}
	}
	return Result{ID: out.ID, GUID: out.GUID, Title: out.Title}, nil
}

// login exchanges the environment's credentials for a JWT and caches it.
func (c *Client) login() error {
	if c.user == "" || c.pass == "" {
		return ErrNoCredentials
	}
	var out struct {
		Token string `json:"token"`
	}
	c.token = "" // never send a known-bad token to the login endpoint
	if err := c.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": c.user, "password": c.pass}, &out); err != nil {
		return err
	}
	if out.Token == "" {
		return errors.New("GoNotes returned no token")
	}
	c.token = out.Token
	c.cacheToken(out.Token)
	return nil
}

// -----------------------------------------------------------------------------
// Transport
// -----------------------------------------------------------------------------

// apiEnvelope is the shape every v1 endpoint answers in. `data` stays
// raw so one decode step can be skipped for calls that ignore it.
type apiEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// apiError carries the HTTP status alongside the server's message,
// because the STATUS is what the retry decision turns on while the
// MESSAGE is what the user needs to read.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string {
	if e.message != "" {
		return e.message
	}
	return fmt.Sprintf("GoNotes returned %d", e.status)
}

// do performs one call and decodes the envelope's data into out (nil
// when the caller only cares that it succeeded).
func (c *Client) do(method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding the request: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, c.baseURL+path, rdr)
	if err != nil {
		return fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		// The address is part of the answer: "connection refused" is
		// only actionable once the user knows what ced dialed.
		return fmt.Errorf("GoNotes at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading GoNotes' reply: %w", err)
	}

	var env apiEnvelope
	if json.Unmarshal(raw, &env) != nil {
		// A non-JSON body means this isn't GoNotes answering (a proxy's
		// error page, a different service on the port). Report the
		// status and the address rather than a decode error nobody can
		// act on.
		return &apiError{status: resp.StatusCode,
			message: fmt.Sprintf("unexpected reply from %s (HTTP %d) — is that a GoNotes server?", c.baseURL, resp.StatusCode)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !env.Success {
		return &apiError{status: resp.StatusCode, message: env.Error}
	}
	if out == nil || len(env.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("decoding GoNotes' reply: %w", err)
	}
	return nil
}

// -----------------------------------------------------------------------------
// Token cache
// -----------------------------------------------------------------------------

// cachedToken reads the shared JWT cache, "" when there isn't one.
func (c *Client) cachedToken() string {
	if c.tokenFile == "" {
		return ""
	}
	b, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// cacheToken writes the JWT back so the next capture needs no login.
// Best-effort by design — a read-only HOME must cost the cache, never
// the note that was just saved.
func (c *Client) cacheToken(token string) {
	if c.tokenFile == "" || token == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.tokenFile), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(c.tokenFile, []byte(token), 0o600)
}

// -----------------------------------------------------------------------------
// GUID
// -----------------------------------------------------------------------------

// newGUID returns an RFC 4122 version 4 UUID.
//
// Hand-rolled rather than pulled in as a dependency: this is sixteen
// random bytes and two masked nibbles, and github.com/google/uuid would
// be a module in ced's go.mod for exactly that. The server treats the
// GUID as an opaque unique key, so the only property that matters is
// that two captures never collide.
func newGUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating a note id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
