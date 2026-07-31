// =============================================================================
// File: internal/mcp/config.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Package mcp is ced's Model Context Protocol client: the config that
// declares which MCP servers exist, and a minimal stdio client that can
// talk to one.
//
// config.go owns the declaration half. The on-disk shape is deliberately
// the one the ecosystem already uses (Claude Desktop's
// claude_desktop_config.json, and every tool that copied it), so a user
// can paste the block they already have:
//
//	{
//	  "mcpServers": {
//	    "github":     {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-github"],
//	                   "env": {"GITHUB_TOKEN": "ghp_…"}},
//	    "filesystem": {"command": "mcp-server-filesystem", "args": ["/srv/data"]},
//	    "docs":       {"type": "http", "url": "https://example.com/mcp",
//	                   "headers": {"Authorization": "Bearer …"}},
//	    "retired":    {"command": "old-server", "disabled": true}
//	  }
//	}
//
// VS Code's "servers" key is accepted as a fallback spelling for the
// same reason — inventing a third dialect would only mean users hand-
// translating a block they already have working.
//
// This file lives apart from userconfig on the same principle that
// splits actions.json from config.json: config.json is flat editor
// preferences a menu toggle writes back, mcp.json is a nested inventory
// the user hand-edits. A syntax error in one must not disable the other.
//
// Loading is best-effort in the same shape as every other ced config:
// no file → no servers, no complaint (declaring servers is the opt-in);
// a malformed file → an error the caller flashes, with the editor still
// starting normally.
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Transport is how a server is reached. Stdio (a child process speaking
// newline-framed JSON-RPC over its pipes) is the only one ced's own
// client connects to; http and sse are still parsed and kept because
// they are meaningful to the chat agent, which ced hands the whole
// inventory to — see the app layer's declaration path.
type Transport string

const (
	TransportStdio Transport = "stdio"
	TransportHTTP  Transport = "http"
	TransportSSE   Transport = "sse"
)

// Server is one declared MCP server, resolved and validated.
type Server struct {
	// Name is the map key from the config file — the display name, the
	// id the chat agent receives, and the key ced tracks connections by.
	Name string

	// Transport defaults to stdio when the entry omits "type"; an entry
	// with a "url" and no "type" is taken as http, since that is the
	// only thing a URL can mean here.
	Transport Transport

	// Command / Args / Env describe a stdio server's child process. Env
	// is ADDED to the editor's environment (see lsp.StartNDJSON), which
	// is what makes `npx`-style commands work at all.
	Command string
	Args    []string
	Env     map[string]string

	// URL / Headers describe an http or sse server.
	URL     string
	Headers map[string]string

	// Disabled keeps an entry in the file but out of play — the
	// ecosystem's standard way to park a server without deleting its
	// (often fiddly) command line. Disabled servers are returned by
	// Load so the UI can show them greyed rather than pretending they
	// don't exist; every consumer filters with Enabled().
	Disabled bool
}

// fileEntry mirrors one server object as written on disk. Decoding into
// this and promoting into Server keeps the public type free of JSON
// tags and lets us apply defaults (transport inference) in one place.
type fileEntry struct {
	Type     string            `json:"type"`
	Command  string            `json:"command"`
	Args     []string          `json:"args"`
	Env      map[string]string `json:"env"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	Disabled bool              `json:"disabled"`
}

// fileFormat is the whole document. Both spellings of the top-level key
// are read; mcpServers wins when a file somehow carries both.
type fileFormat struct {
	MCPServers map[string]fileEntry `json:"mcpServers"`
	Servers    map[string]fileEntry `json:"servers"`
}

// Load reads and validates the MCP inventory at path.
//
// Contract (deliberately identical to userconfig.Load's):
//   - path == ""            → (nil, nil). No config location resolved.
//   - file doesn't exist    → (nil, nil). Declaring servers is the opt-in.
//   - file empty            → (nil, nil).
//   - unreadable / bad JSON → (nil, err). The caller flashes it.
//   - an entry missing its command (stdio) or url (http/sse) → (nil, err),
//     naming the entry. A half-declared server would otherwise fail much
//     later, as a spawn error with no obvious cause.
//
// Servers come back sorted by name: JSON objects have no order, so
// sorting is what makes the picker and the agent declaration stable
// across runs instead of shuffling with Go's map iteration.
func Load(path string) ([]Server, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}

	var ff fileFormat
	if err := json.Unmarshal(data, &ff); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	entries := ff.MCPServers
	if len(entries) == 0 {
		entries = ff.Servers
	}
	if len(entries) == 0 {
		return nil, nil
	}

	out := make([]Server, 0, len(entries))
	for name, e := range entries {
		s, err := resolveEntry(name, e)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// resolveEntry promotes one raw entry into a validated Server, filling
// in the transport the entry left implicit.
func resolveEntry(name string, e fileEntry) (Server, error) {
	s := Server{
		Name:     strings.TrimSpace(name),
		Command:  strings.TrimSpace(e.Command),
		Args:     e.Args,
		Env:      e.Env,
		URL:      strings.TrimSpace(e.URL),
		Headers:  e.Headers,
		Disabled: e.Disabled,
	}
	if s.Name == "" {
		return Server{}, errors.New("a server entry has an empty name")
	}
	switch Transport(strings.ToLower(strings.TrimSpace(e.Type))) {
	case "":
		// Infer: a url and no command can only mean a remote server.
		if s.URL != "" && s.Command == "" {
			s.Transport = TransportHTTP
		} else {
			s.Transport = TransportStdio
		}
	case TransportStdio:
		s.Transport = TransportStdio
	case TransportHTTP:
		s.Transport = TransportHTTP
	case TransportSSE:
		s.Transport = TransportSSE
	default:
		return Server{}, fmt.Errorf("server %q: type must be %q, %q, or %q (got %q)",
			s.Name, TransportStdio, TransportHTTP, TransportSSE, e.Type)
	}
	if s.Transport == TransportStdio && s.Command == "" {
		return Server{}, fmt.Errorf("server %q: %q needs a \"command\"", s.Name, TransportStdio)
	}
	if s.Transport != TransportStdio && s.URL == "" {
		return Server{}, fmt.Errorf("server %q: %q needs a \"url\"", s.Name, s.Transport)
	}
	return s, nil
}

// Enabled reports whether this server should be acted on — connected to
// by ced, and declared to the chat agent.
func (s Server) Enabled() bool { return !s.Disabled }

// EnvPairs renders Env as sorted "K=V" strings for exec. Sorted so a
// spawned command line is reproducible (and diffable in a bug report);
// map order would otherwise vary run to run.
func (s Server) EnvPairs() []string {
	if len(s.Env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+s.Env[k])
	}
	return out
}

// Describe is the one-line summary the pickers and transcript notes
// use: enough to tell two entries apart without leaking a token from an
// env value (only the KEYS are shown, never the values).
func (s Server) Describe() string {
	if s.Transport != TransportStdio {
		return string(s.Transport) + " " + s.URL
	}
	parts := append([]string{s.Command}, s.Args...)
	line := strings.Join(parts, " ")
	if n := len(s.Env); n > 0 {
		keys := make([]string, 0, n)
		for k := range s.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		line += "  [env: " + strings.Join(keys, ", ") + "]"
	}
	return line
}
