// =============================================================================
// File: internal/app/lsprestart.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// lsprestart.go is the ≡ Code "Restart language server" row — the LSP's
// deliberate retry gesture.
//
// The integration never auto-restarts a server (handleLSPExit: a crashing
// one would flap), which left exactly one way back from a dead slot:
// restarting the editor. Two ordinary situations end there — a server
// that crashed once, and a server INSTALLED AFTER ced launched, whose
// slot was marked dead by the first file that looked for it. Copilot has
// its off/on toggle and the chat panel has re-picking the agent; this is
// the same gesture for the LSP, and per-server slots (lspservers.go) are
// what make it small.
//
//	≡ Restart language server (gopls)
//	        │
//	        ├─ no server handles this file ──► flash, nothing else
//	        ├─ handshake already in flight ──► flash, nothing else
//	        │
//	        ├─ lspDropServer   close the client, clear ITS diags/timers/versions
//	        ├─ sv.gen++        the old process's late exit event is now stale
//	        ├─ dead = false    the verdict being retried
//	        │
//	        ├─ binary still missing ──► dead again, flash NAMES the binaries
//	        ├─ explainStart = true
//	        └─ lspEnsureStarted ──► lspReadyEvent{gen} ──► handleLSPReady
//	                 │               re-announces every open tab it speaks for
//	                 └─ handshake fails ──► lspExitEvent{gen, err} ──► handleLSPExit
//	                                         flash + info modal: "<id> failed to start"
//
// House rules:
//
//   - IT ACTS ON THE ACTIVE FILE'S SERVER, because "which server?" has one
//     answer everywhere else in the integration (a path names its server)
//     and the label can then say which one a click will touch.
//   - A REASON, NOT A GATE (menuCopilotAuth's rule). The row is wanted
//     most where a predicate would dim it — a dead or missing server — and
//     "rust-analyzer is not installed" is only sayable from a flash.
//   - THE GENERATION IS LOAD-BEARING. Closing the live client makes its
//     read loop fire onExit, and that event lands AFTER the restart has
//     re-armed the slot. Unstamped, it would close the successor and mark
//     the server dead — a restart row that kills what it restarted. Same
//     discipline as chatState.connSeq.
//   - IT DOES NOT TOUCH lspState.dead. That switch is the integration-wide
//     one (shutdown, the test harness); clearing it from a menu row would
//     let a row resurrect an integration the editor deliberately stopped.
//   - Inlay records are forgotten (inlayReask) and noInlay cleared: both
//     describe the PROCESS that is gone, and the new one may be a newer
//     build that speaks the method.
//   - A FAILED HANDSHAKE IS EXPLAINED ONLY HERE (explainStart, N-112). Some
//     servers refuse with a sentence worth reading (typescript-language-
//     server over TypeScript 7: "…provides no tsserver.js…"), but a start
//     from opening a file stays silent by the degradation rule. The row is
//     the user asking "what is wrong?", so its spawn's failure is
//     flashed when it lands, and shown whole in an info modal when the
//     slot is free. A plain open never sets the flag.

package app

import (
	"path/filepath"
	"strings"
)

// lspRestartLabel is the ≡ row's dynamic label. It names the server a
// click will restart, so the row never reads as "restart everything".
func (a *App) lspRestartLabel() string {
	if def := a.lspActiveServerDef(); def != nil {
		return "Restart language server (" + def.id + ")"
	}
	return "Restart language server"
}

// lspActiveServerDef returns the registry entry that speaks for the
// active tab, or nil when there is no such tab or no server handles it.
func (a *App) lspActiveServerDef() *lspServerDef {
	t := a.activeTabPtr()
	if t == nil || t.Path == "" || t.IsImage() {
		return nil
	}
	return lspServerFor(t.Path)
}

// menuRestartLSP tears down the active file's language server and starts
// it again, clearing a dead verdict on the way. Every refusal flashes its
// reason; see the file header for why none of them dims the row.
func (a *App) menuRestartLSP() {
	a.closeMenu()
	def := a.lspActiveServerDef()
	if def == nil {
		a.flash("No language server handles this file")
		return
	}
	if a.lsp.dead {
		// The integration-wide switch is not this row's to clear.
		a.flash("Language servers are shut down")
		return
	}
	sv := a.lsp.server(def.id)
	if sv.starting {
		// A second spawn beside the first would leave two processes and
		// one slot. The in-flight one is already what the user wants.
		a.flash(def.id + " is already starting")
		return
	}

	a.lspDropServer(def.id)
	sv.gen++
	sv.dead = false
	sv.noInlay = false
	a.inlayReask()

	// Checked here rather than left to lspEnsureStarted, which marks a
	// missing binary dead IN SILENCE — right for a file merely opened,
	// wrong for a row the user clicked to find out what is wrong.
	if _, _, ok := def.resolveCommand(); !ok {
		sv.dead = true
		a.flash(def.id + " is not installed (looked for " + lspCommandNames(def) + ")")
		return
	}
	// The binary is there, so a failure now happens in the handshake, off
	// the loop. Arm the slot so that failure is flashed with the server's
	// own reason when it lands (handleLSPExit) — the async twin of the
	// missing-binary flash above.
	sv.explainStart = true
	a.lspEnsureStarted(def)
	a.flash("Restarting " + def.id + "…")
}

// explainLSPStartFailure answers a restart whose handshake failed. The
// flash always fires, and an info modal carries the reason WHOLE: a
// server's refusal is often a sentence wider than the status bar
// (typescript-language-server over TypeScript 7 runs ~170 cells, with
// the words that matter at the end). That is the failed-git-command
// rule: a failure the user can act on is shown in full.
//
// The answer lands a moment AFTER the click, so it is an unprompted
// arrival: it declines an occupied modal slot or an open ≡ menu, and the
// flash is then the whole answer.
func (a *App) explainLSPStartFailure(id, reason string) {
	a.flash(id + " failed to start: " + reason)
	if a.modal != nil || a.menuOpen {
		return
	}
	a.openInfo(id+" failed to start", wrapChatText(reason, lspStartReasonWidth))
}

// lspStartReasonWidth is the wrap width for the reason in the info modal:
// the modal body's text width, the same 78 errorBodyLines clips to.
const lspStartReasonWidth = 78

// lspStartReason turns a failed start's error into the words after
// "<server> failed to start: ". It peels the wrapping layers, so the
// server's own sentence is what fits in the status bar: the lsp client's
// "lsp: initialize: … (code N)", then the "Request initialize failed with
// message: " prefix that servers built on vscode-languageserver-node
// (typescript-language-server, pyright) add to the error they throw.
// Anything else (a spawn error, "connection closed") passes through bare.
//
// Paths under root are spelled relative to it. Servers name the install
// they rejected by absolute path ("TypeScript 7.0.2 at
// \"/Users/…/proj/node_modules/typescript/lib\""), and on a status bar
// that path alone can push the sentence that matters ("…provides no
// tsserver.js") off the right edge. The status bar's own path segment
// uses the same project-relative spelling.
func lspStartReason(err error, root string) string {
	s := strings.TrimSpace(err.Error())
	s = strings.TrimPrefix(s, "lsp: ")
	s = strings.TrimPrefix(s, "initialize: ")
	if i := strings.LastIndex(s, " (code "); i > 0 && strings.HasSuffix(s, ")") {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "Request initialize failed with message: ")
	if root != "" {
		s = strings.ReplaceAll(s, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator), "")
	}
	return s
}

// lspCommandNames lists the binaries a definition would accept, in the
// order they are tried — what a user needs to read to fix their PATH.
func lspCommandNames(def *lspServerDef) string {
	var names []string
	for _, argv := range def.commands {
		if len(argv) > 0 {
			names = append(names, argv[0])
		}
	}
	return strings.Join(names, ", ")
}
