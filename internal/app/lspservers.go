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
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/child"
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
	// traits holds what one alternate command does differently from the
	// others, keyed by its argv[0] (N-101). Most commands need nothing:
	// absent means trust PATH, push diagnostics, no settings.
	traits map[string]lspCommandTraits
}

// lspCommandTraits are the per-COMMAND facts that alternates for the same
// files can differ by. `tsc --lsp` and typescript-language-server both
// answer for .ts, but tsc 5/6 is not a server at all, and TS7's tsc
// pulls its diagnostics and reads its settings the VS Code way.
type lspCommandTraits struct {
	// probe asks, OFF the main loop, whether the binary found on PATH
	// really is this server; false falls through to the next command.
	// nil trusts PATH.
	probe func(binPath string) bool
	// pullDiagnostics: the server doesn't publish after edits; ced asks
	// textDocument/diagnostic after every sync, and ignores what it
	// pushes (tsc's one empty publish on open would otherwise race the
	// first pull's answer and erase it).
	pullDiagnostics bool
	// settings is pushed with workspace/didChangeConfiguration right
	// after the handshake, and is the answer to workspace/configuration
	// should the server ask (per item, by section).
	settings map[string]any
}

// lspCandidate is one installed alternate: its resolved binary, args and
// traits. lspEnsureStarted collects them on the main loop (a PATH lookup
// each) and the start goroutine probes them in order.
type lspCandidate struct {
	bin    string
	args   []string
	traits lspCommandTraits
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
		// No initOptions, and no settings to answer workspace/configuration
		// with: none of the three needs hints switched on. Run against the
		// real servers on 2026-10-28 —
		//
		//   pyright 1.1.414        textDocument/inlayHint is "Unhandled
		//                          method"; hints are Pylance-only, so no
		//                          setting could turn them on.
		//   basedpyright 1.40.2    hints ON by default (variable types,
		//                          argument names, return types), answered
		//                          through this handshake as it stands.
		//   pylsp 1.15.0           "Method Not Found".
		//
		// basedpyright DOES read its hint settings through
		// workspace/configuration (section "basedpyright", key
		// analysis.inlayHints.*), but only once a client declares
		// workspace.configuration, which ced's initialize does not, so it
		// never asks and its defaults stand. Note its npm package installs
		// a `pyright-langserver` alias too, so the first command below is
		// often basedpyright in disguise.
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
		id: "typescript-language-server",
		// TypeScript 7's own server first (N-101), then the classic one.
		// A tsc on PATH is very often TypeScript 5 or 6, which has no
		// --lsp: the probe asks `tsc --version` and falls through to
		// typescript-language-server for anything below 7.
		commands: [][]string{
			{"tsc", "--lsp", "--stdio"},
			{"typescript-language-server", "--stdio"},
		},
		exts: []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts"},
		traits: map[string]lspCommandTraits{
			"tsc": {
				// Read through the package var at call time so tests can
				// stub the fork.
				probe:           func(bin string) bool { return lspTSCProbe(bin) },
				pullDiagnostics: true,
				settings:        tsLSPSettings,
			},
		},
		// Off by default here too. "literals" names only arguments whose
		// text says nothing (`true`, `3`), not every identifier.
		// initializationOptions.preferences IS the route that works: with
		// typescript-language-server 5.3.0 over TypeScript 5.9 this blob
		// gets five hints on a ten-line file and none without it
		// (2026-10-28); no didChangeConfiguration push is needed.
		//
		// The server needs TypeScript ≤ 6: it drives tsserver.js, which
		// TypeScript 7 (the native rewrite, npm's `latest` since 7.0)
		// doesn't ship. 6.0.3 refuses a TS7-only setup at initialize, and
		// falls back to a TypeScript 6 installed beside it when only the
		// project's node_modules holds 7. TS7's own server, `tsc --lsp
		// --stdio`, is tried FIRST (see commands): its diagnostics are
		// pull-only and its inlay hints ignore these preferences — it reads
		// tsLSPSettings instead (lspCommandTraits). The preferences below
		// are harmless to it.
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

// tsInlayHints is the VS Code-shaped inlay-hint block TypeScript 7's
// server reads, the same choices as typescript-language-server's
// preferences above: literal arguments named, variable and return types
// shown. Probed on 2026-11-06: 0 hints without it, 6 with it.
var tsInlayHints = map[string]any{
	"inlayHints": map[string]any{
		"parameterNames":          map[string]any{"enabled": "literals"},
		"variableTypes":           map[string]any{"enabled": true},
		"functionLikeReturnTypes": map[string]any{"enabled": true},
	},
}

// tsLSPSettings is the whole settings object for `tsc --lsp`, by section.
// "js/ts" is the unified section newer VS Code builds read; the server
// was seen asking for it beside the two classic ones.
var tsLSPSettings = map[string]any{
	"typescript": tsInlayHints,
	"javascript": tsInlayHints,
	"js/ts":      tsInlayHints,
}

// lspTSCProbe reports whether the tsc at bin is TypeScript 7 or later,
// the first with a language server. A package var so tests can pin it.
// Detached and bounded like every child (internal/child): a broken node
// install must not hang the start goroutine.
var lspTSCProbe = func(bin string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	child.Detach(cmd)
	out, err := cmd.Output()
	return err == nil && tscVersionHasLSP(string(out))
}

// tscVersionHasLSP reads `tsc --version` output ("Version 7.0.2") and
// reports whether its major version is 7 or more. Anything unreadable is
// no: falling through to typescript-language-server is the safe answer.
func tscVersionHasLSP(out string) bool {
	v := strings.TrimSpace(out)
	v = strings.TrimPrefix(v, "Version ")
	major, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(major)
	return err == nil && n >= 7
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
	// pull marks a server that doesn't publish diagnostics, so ced asks
	// after every sync (lspCommandTraits.pullDiagnostics, N-101). Set
	// from the ready event: it belongs to the COMMAND that started.
	pull bool
	// noInlay is set once the server answers an inlay-hint request with
	// an error — it does not speak the method, so stop asking.
	noInlay bool
	// gen counts restarts of this slot (lsprestart.go). Every spawn
	// stamps its ready/exit events with the value it started under, and
	// the handlers drop mismatches — the chat layer's connSeq rule. Zero
	// for a slot that has never been restarted, which is what hand-built
	// test events carry.
	gen int
	// explainStart is set by the ≡ Code "Restart language server" row and
	// cleared by the next handshake's outcome. While it is set, a failed
	// start FLASHES the server's reason (handleLSPExit). The user clicked
	// the row to find out what is wrong, the same rule as the row's
	// missing-binary flash. A start from merely opening a file leaves it
	// clear and fails in silence, like every integration (N-112).
	explainStart bool

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

// installedCommands lists the alternates whose binary is on PATH, in
// order, with their traits — what the start goroutine probes. Empty
// means nothing is installed: the server is dead in silence, as with
// resolveCommand (which answers the same question for one command).
func (d *lspServerDef) installedCommands() []lspCandidate {
	var out []lspCandidate
	for _, argv := range d.commands {
		if len(argv) == 0 {
			continue
		}
		if _, err := lspLookPath(argv[0]); err == nil {
			out = append(out, lspCandidate{bin: argv[0], args: argv[1:], traits: d.traits[argv[0]]})
		}
	}
	return out
}

// lspPickCandidate returns the first candidate whose probe passes (or
// that has none). Runs on the start goroutine: a probe forks. The error
// names what was found and refused, for the Restart row's explanation.
func lspPickCandidate(cands []lspCandidate) (lspCandidate, error) {
	var refused []string
	for _, c := range cands {
		if c.traits.probe != nil {
			path, err := lspLookPath(c.bin)
			if err != nil || !c.traits.probe(path) {
				refused = append(refused, c.bin)
				continue
			}
		}
		return c, nil
	}
	return lspCandidate{}, fmt.Errorf("no usable server: %s is installed but is not one (tsc needs TypeScript 7 for --lsp)", strings.Join(refused, ", "))
}

// lspConfigAnswer answers a workspace/configuration request from
// settings: one entry per requested item, the item's dotted section
// looked up in settings, `{}` where it names nothing (the
// auto-responder's answer, which switches nothing off).
func lspConfigAnswer(settings map[string]any, params json.RawMessage) []any {
	var p struct {
		Items []struct {
			Section string `json:"section"`
		} `json:"items"`
	}
	_ = json.Unmarshal(params, &p)
	out := make([]any, len(p.Items))
	for i, it := range p.Items {
		out[i] = map[string]any{}
		if v := lspSettingsSection(settings, it.Section); v != nil {
			out[i] = v
		}
	}
	return out
}

// lspSettingsSection walks a dotted section ("typescript.inlayHints")
// down settings. A whole top-level key wins first, so a section that
// itself contains a dot or slash ("js/ts") is found as written.
func lspSettingsSection(settings map[string]any, section string) any {
	if section == "" {
		return settings
	}
	if v, ok := settings[section]; ok {
		return v
	}
	var cur any = settings
	for _, part := range strings.Split(section, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		if cur, ok = m[part]; !ok {
			return nil
		}
	}
	return cur
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
