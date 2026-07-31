// =============================================================================
// File: internal/mcp/config_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig drops a config document into a temp dir and returns its
// path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestLoad_AbsentIsNotAnError pins the opt-in contract: no path and no
// file both mean "no servers declared", never a startup complaint.
func TestLoad_AbsentIsNotAnError(t *testing.T) {
	if got, err := Load(""); got != nil || err != nil {
		t.Errorf("Load(\"\") = %v, %v; want nil, nil", got, err)
	}
	missing := filepath.Join(t.TempDir(), "nope.json")
	if got, err := Load(missing); got != nil || err != nil {
		t.Errorf("Load(missing) = %v, %v; want nil, nil", got, err)
	}
	empty := writeConfig(t, "   \n")
	if got, err := Load(empty); got != nil || err != nil {
		t.Errorf("Load(empty) = %v, %v; want nil, nil", got, err)
	}
}

// TestLoad_ParsesEcosystemShape covers the happy path in the format
// users already have: the Claude-Desktop mcpServers map, with stdio
// entries, env, an http entry inferred from its url, and a disabled
// entry that still comes back (greyed, not vanished). Order is by name
// because JSON maps have none.
func TestLoad_ParsesEcosystemShape(t *testing.T) {
	path := writeConfig(t, `{
	  "mcpServers": {
	    "github": {"command": "npx", "args": ["-y", "server-github"], "env": {"GITHUB_TOKEN": "secret"}},
	    "docs":   {"url": "https://example.com/mcp", "headers": {"Authorization": "Bearer x"}},
	    "aged":   {"command": "old-server", "disabled": true}
	  }
	}`)
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (%v)", len(got), got)
	}
	if got[0].Name != "aged" || got[1].Name != "docs" || got[2].Name != "github" {
		t.Fatalf("not sorted by name: %v", []string{got[0].Name, got[1].Name, got[2].Name})
	}
	if !got[0].Disabled || got[0].Enabled() {
		t.Error("aged should be disabled")
	}
	if got[1].Transport != TransportHTTP {
		t.Errorf("docs transport = %q, want %q (inferred from url)", got[1].Transport, TransportHTTP)
	}
	if got[1].Headers["Authorization"] != "Bearer x" {
		t.Errorf("docs headers lost: %v", got[1].Headers)
	}
	gh := got[2]
	if gh.Transport != TransportStdio {
		t.Errorf("github transport = %q, want %q (default)", gh.Transport, TransportStdio)
	}
	if gh.Command != "npx" || len(gh.Args) != 2 || gh.Args[1] != "server-github" {
		t.Errorf("github command line = %q %v", gh.Command, gh.Args)
	}
	if pairs := gh.EnvPairs(); len(pairs) != 1 || pairs[0] != "GITHUB_TOKEN=secret" {
		t.Errorf("EnvPairs = %v, want [GITHUB_TOKEN=secret]", pairs)
	}
}

// TestLoad_AcceptsServersKey pins the VS Code spelling of the same
// document — accepted so a user can paste a block they already have
// instead of hand-translating it.
func TestLoad_AcceptsServersKey(t *testing.T) {
	path := writeConfig(t, `{"servers": {"local": {"command": "my-server"}}}`)
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].Name != "local" || got[0].Command != "my-server" {
		t.Fatalf("got %v, want one stdio server named local", got)
	}
}

// TestLoad_RejectsHalfDeclaredServers pins the validation that turns a
// late, mysterious spawn failure into a config error naming the entry:
// stdio needs a command, http needs a url, and an unknown type is a typo
// worth reporting rather than silently defaulting.
func TestLoad_RejectsHalfDeclaredServers(t *testing.T) {
	cases := map[string]string{
		"stdio without command": `{"mcpServers": {"broken": {"args": ["x"]}}}`,
		"http without url":      `{"mcpServers": {"broken": {"type": "http"}}}`,
		"unknown type":          `{"mcpServers": {"broken": {"type": "carrier-pigeon", "command": "x"}}}`,
		"malformed json":        `{"mcpServers": {`,
	}
	for name, body := range cases {
		path := writeConfig(t, body)
		got, err := Load(path)
		if err == nil {
			t.Errorf("%s: expected an error, got %v", name, got)
			continue
		}
		if got != nil {
			t.Errorf("%s: servers should be nil on error, got %v", name, got)
		}
		if name != "malformed json" && !strings.Contains(err.Error(), "broken") {
			t.Errorf("%s: error should name the entry, got %v", name, err)
		}
	}
}

// TestDescribe_HidesEnvValues pins the one privacy rule in this file: a
// picker row shows which credentials a server takes, never what they
// are — config values land in screenshots and bug reports.
func TestDescribe_HidesEnvValues(t *testing.T) {
	s := Server{
		Name: "github", Transport: TransportStdio,
		Command: "npx", Args: []string{"-y", "server-github"},
		Env: map[string]string{"GITHUB_TOKEN": "ghp_supersecret"},
	}
	got := s.Describe()
	if strings.Contains(got, "ghp_supersecret") {
		t.Fatalf("Describe leaked an env value: %q", got)
	}
	if !strings.Contains(got, "GITHUB_TOKEN") || !strings.Contains(got, "npx -y server-github") {
		t.Errorf("Describe = %q, want the command line and the env KEY", got)
	}
	remote := Server{Name: "docs", Transport: TransportHTTP, URL: "https://example.com/mcp"}
	if got := remote.Describe(); got != "http https://example.com/mcp" {
		t.Errorf("remote Describe = %q", got)
	}
}
