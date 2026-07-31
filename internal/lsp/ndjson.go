// =============================================================================
// File: internal/lsp/ndjson.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// ndjson.go is the generic launcher for newline-delimited JSON-RPC
// servers. Two protocols ced speaks frame their messages that way — ACP
// (the chat agent, see acp.go) and MCP (Model Context Protocol servers,
// see internal/mcp) — and they differ only in vocabulary, not transport.
// StartACP is a thin alias kept for readability at the ACP call sites;
// StartNDJSON is what both actually use.
//
// The one capability this adds over StartACP's original body is env:
// MCP servers routinely need credentials handed to them (API tokens,
// endpoint overrides) and a config file is where users put them. Env
// entries are ADDED to the editor's own environment rather than
// replacing it — a server launched through npx/uvx still needs PATH,
// HOME, and the rest to work at all.

package lsp

import (
	"encoding/json"
	"os"
	"os/exec"
)

// StartNDJSON launches bin with args in dir and returns a Client wired
// to its stdio with newline-delimited framing. env holds extra "K=V"
// entries appended to the editor's environment (nil for "inherit
// unchanged"). onRequest answers server→client requests — each
// invocation on its own goroutine, see Client.onRequest — and may be
// nil to fall back on the built-in LSP auto-responder.
//
// The caller should have verified the binary exists (exec.LookPath):
// a missing binary errors here too, but checking first is what keeps
// "not installed" a silent no-op instead of a surfaced failure, the
// shared degradation contract of every integration in this codebase.
func StartNDJSON(dir, bin string, args, env []string,
	onNotify func(string, json.RawMessage),
	onRequest func(string, json.RawMessage) (any, error),
	onExit func(error)) (*Client, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	// stderr goes to our stderr (tcell owns the tty, so effectively
	// /dev/null) — deliberate, same as Start: server logs are noise for
	// an editor user.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := NewClientACP(stdout, stdin, onNotify, onRequest, onExit)
	c.cmd = cmd
	return c, nil
}
