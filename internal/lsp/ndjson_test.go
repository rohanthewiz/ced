// =============================================================================
// File: internal/lsp/ndjson_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStartNDJSON_RoundTripWithEnv pins the whole reason this launcher
// exists: a spawned process gets the caller's extra environment on top
// of the editor's own, and its newline-framed reply resolves the Call.
// The stub server echoes the injected variable back, so a dropped env
// slice (or lost framing) fails the assertion rather than passing
// silently.
func TestStartNDJSON_RoundTripWithEnv(t *testing.T) {
	dir := t.TempDir()
	script := `read line
printf '{"jsonrpc":"2.0","id":1,"result":{"echo":"%s","home":"%s"}}\n' "$CED_TEST_TOKEN" "$HOME"
`
	path := filepath.Join(dir, "server.sh")
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	c, err := StartNDJSON(dir, "/bin/sh", []string{path},
		[]string{"CED_TEST_TOKEN=swordfish"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("StartNDJSON: %v", err)
	}
	defer c.Close()

	var got struct {
		Echo string `json:"echo"`
		Home string `json:"home"`
	}
	if err := c.Call("ping", map[string]any{}, &got); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got.Echo != "swordfish" {
		t.Errorf("injected env = %q, want %q", got.Echo, "swordfish")
	}
	// Extra env ADDS to the process environment; it must not replace it,
	// or servers launched via npx/uvx lose PATH and HOME and never start.
	if got.Home == "" {
		t.Error("inherited environment was dropped (HOME empty)")
	}
}

// TestStartNDJSON_MissingBinary pins that a bad command surfaces as an
// error instead of a live client — callers turn that into the silent
// "not installed" verdict.
func TestStartNDJSON_MissingBinary(t *testing.T) {
	c, err := StartNDJSON(t.TempDir(), "ced-no-such-binary-xyz", nil, nil, nil, nil, nil)
	if err == nil {
		c.Close()
		t.Fatal("expected an error for a missing binary")
	}
}
