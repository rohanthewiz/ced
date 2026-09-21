// =============================================================================
// File: internal/app/lspservers.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// lspservers.go is the language-server REGISTRY: which server speaks for
// which file, and the per-server connection state that used to be three
// fields on lspState back when the answer was always "gopls".
//
// The shape is the chat-agent registry's (chatagent.go): a small static
// table of definitions, a lookup, and nothing configurable. Every verb in
// the Code group was already protocol-generic — the client is hand-rolled
// JSON-RPC and knows nothing about Go — so the only thing standing between
// ced and a TypeScript or Rust project was one constant naming one binary.
//
//	path ──ext──► lspServerFor ──► *lspServerDef ──id──► lspState.servers[id]
//	                                     │                     │
//	                         commands (first on PATH wins)     client / starting / dead
//
// The decisions worth spelling out:
//
//   - ONE SERVER PER FILE, CHOSEN BY EXTENSION. Every per-document map on
//     lspState (versions, syncedRev, timers, diags) stays keyed by PATH and
//     needed no change at all, because a path belongs to exactly one server.
//     Two servers on one file (a linter beside a type checker) would need
//     those maps keyed by (server, path) and a merge rule for every verb;
//     that is a different feature, and plugins' decoration providers already
//     cover the "second opinion on this file" case.
//   - INSTALLING THE BINARY IS THE OPT-IN, as it always was for gopls. There
//     is no config key: a server not on PATH marks ITS entry dead in silence
//     and costs nothing, and the other servers are unaffected — degradation
//     is per server (the MCP rule), so a crashed rust-analyzer never takes
//     the Go squiggles with it.
//   - NOTHING SPAWNS UNTIL A FILE IT HANDLES IS OPENED. A Go-only project
//     never looks for clangd, let alone starts it.
//   - A DEFINITION CAN NAME ALTERNATE COMMANDS (pyright, then basedpyright,
//     then pylsp). They are interchangeable answers to "who speaks Python
//     here", and which one a machine has is an accident of how it was set
//     up. First found wins; the order is most-capable first.
//   - THE languageId IS COPILOT'S TABLE (copilotLanguageID), not a second
//     one. Both layers send the same field of the same protocol, and two
//     tables would drift on exactly the ids that matter — "typescriptreact"
//     vs "typescript" decides whether JSX parses.

package app

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// lspServerDef describes one language server: what to run and which
// files it answers for. Pure data — the registry below is the only place
// a definition is written.
type lspServerDef struct {
	// id keys lspState.servers and rides the ready/exit events. It is the
	// server's conventional name, which is also what the status bar and
	// the flashes call it.
	id string
	// commands are the alternate argvs, most-capable first. The first
	// whose binary is on PATH is the one that runs.
	commands [][]string
	// exts are the lower-cased extensions (with the dot) this server
	// handles.
	exts []string
	// initOptions is the server-private initializationOptions blob, for
	// settings a server reads only at startup and ships switched off.
	// Today that is inlay hints (lspinlay.go): enabling them here costs
	// nothing until a hint is requested.
	initOptions map[string]any
}

// lspServers is the registry. Order is irrelevant to lookup (extensions
// don't overlap — TestLSPServers_ExtensionsAreDisjoint pins that) and is
// kept alphabetical by language for the reader.
var lspServers = []lspServerDef{
	{
		id:       "clangd",
		commands: [][]string{{"clangd"}},
		exts:     []string{".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx"},
	},
	{
		id:       "gopls",
		commands: [][]string{{"gopls"}},
		exts:     []string{".go"},
		// gopls ships with EVERY hint off. The two left out are the
		// noisy ones: composite-literal types repeat what the line
		// already says, and constant values annotate every iota.
		initOptions: map[string]any{"hints": map[string]any{
			"assignVariableTypes":    true,
			"rangeVariableTypes":     true,
			"parameterNames":         true,
			"functionTypeParameters": true,
			"compositeLiteralFields": true,
		}},
	},
	{
		id: "python",
		commands: [][]string{
			{"pyright-langserver", "--stdio"},
			{"basedpyright-langserver", "--stdio"},
			{"pylsp"},
		},
		exts: []string{".py", ".pyi"},
	},
	{
		id:       "rust-analyzer",
		commands: [][]string{{"rust-analyzer"}},
		exts:     []string{".rs"},
	},
	{
		id:       "typescript-language-server",
		commands: [][]string{{"typescript-language-server", "--stdio"}},
		exts:     []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts"},
		// Off by default here too. "literals" names only arguments whose
		// text says nothing (`true`, `3`), not every identifier.
		initOptions: map[string]any{"preferences": map[string]any{
			"includeInlayParameterNameHints":          "literals",
			"includeInlayVariableTypeHints":           true,
			"includeInlayFunctionLikeReturnTypeHints": true,
		}},
	},
	{
		id:       "zls",
		commands: [][]string{{"zls"}},
		exts:     []string{".zig"},
	},
}

// lspGoServerID names the Go entry. Tests install their fake connection
// under it, since their fixtures are Go files.
const lspGoServerID = "gopls"

// lspLookPath resolves a server binary. A package var so tests can pin
// it at "never found" — openFile must not be able to spawn whatever
// language servers the developer's machine happens to carry (the
// chatLookPath rule).
var lspLookPath = exec.LookPath

// lspServer is one server's connection state, owned by the main loop
// like the rest of lspState.
type lspServer struct {
	client   lspConn
	starting bool // async spawn+initialize in flight
	dead     bool // unavailable: no binary, crashed, or failed to start
	// noInlay is set once the server answers an inlay-hint request with
	// an error — it does not speak the method, so stop asking.
	noInlay bool

	// progress is the server's in-flight work keyed by progress token,
	// and progressLast the token most recently heard from — the one the
	// status bar shows. See lspprogress.go.
	progress     map[string]*lspProgressItem
	progressLast string
}

// lspServerFor returns the definition that handles path, or nil when no
// server does. Extension match is case-insensitive.
func lspServerFor(path string) *lspServerDef {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return nil
	}
	for i := range lspServers {
		for _, e := range lspServers[i].exts {
			if e == ext {
				return &lspServers[i]
			}
		}
	}
	return nil
}

// lspServerByID returns the definition with this id, or nil.
func lspServerByID(id string) *lspServerDef {
	for i := range lspServers {
		if lspServers[i].id == id {
			return &lspServers[i]
		}
	}
	return nil
}

// resolveCommand picks the first alternate whose binary is installed.
// ok=false means none is — the caller marks the server dead in silence.
func (d *lspServerDef) resolveCommand() (bin string, args []string, ok bool) {
	for _, argv := range d.commands {
		if len(argv) == 0 {
			continue
		}
		if _, err := lspLookPath(argv[0]); err == nil {
			return argv[0], argv[1:], true
		}
	}
	return "", nil, false
}

// lspHandles reports whether some language server covers this file.
func lspHandles(path string) bool { return lspServerFor(path) != nil }

// languageIDFor returns the LSP languageId for a path. Copilot's table
// is the one spelling (see the header); it degrades to the bare
// extension, which servers treat as plaintext.
func languageIDFor(path string) string { return copilotLanguageID(path) }

// server returns the state slot for id, creating it on first use so a
// hand-built test App needs no setup.
func (s *lspState) server(id string) *lspServer {
	if s.servers == nil {
		s.servers = map[string]*lspServer{}
	}
	sv := s.servers[id]
	if sv == nil {
		sv = &lspServer{}
		s.servers[id] = sv
	}
	return sv
}

// lspInstall puts a live connection in a server's slot (nil empties it)
// and clears that server's dead/starting verdicts. It is what
// handleLSPReady does with a fresh client, and what tests use to inject
// a fake.
func (a *App) lspInstall(id string, c lspConn) {
	sv := a.lsp.server(id)
	sv.client = c
	sv.starting = false
	if c != nil {
		sv.dead = false
	}
}

// lspClientFor returns the ready connection that speaks for path, or nil:
// no server handles the file, the integration is off, or that server is
// not up. Every verb asks through this, so "which server?" has one
// answer.
func (a *App) lspClientFor(path string) lspConn {
	if a.lsp.dead {
		return nil
	}
	def := lspServerFor(path)
	if def == nil {
		return nil
	}
	sv := a.lsp.servers[def.id]
	if sv == nil || sv.dead {
		return nil
	}
	return sv.client
}

// lspReadyFor reports whether path's server is up and usable.
func (a *App) lspReadyFor(path string) bool { return a.lspClientFor(path) != nil }

// lspAnyReady reports whether ANY server is up — the question surfaces
// that are not about one file ask (the Problems panel's empty text).
func (a *App) lspAnyReady() bool {
	if a.lsp.dead {
		return false
	}
	for _, sv := range a.lsp.servers {
		if sv.client != nil && !sv.dead {
			return true
		}
	}
	return false
}

// lspAnyStarting reports whether a handshake is in flight somewhere.
func (a *App) lspAnyStarting() bool {
	if a.lsp.dead {
		return false
	}
	for _, sv := range a.lsp.servers {
		if sv.starting {
			return true
		}
	}
	return false
}
