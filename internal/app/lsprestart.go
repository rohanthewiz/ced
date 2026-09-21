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
//	        └─ lspEnsureStarted ──► lspReadyEvent{gen} ──► handleLSPReady
//	                                 re-announces every open tab it speaks for
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

package app

import "strings"

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
	a.lspEnsureStarted(def)
	a.flash("Restarting " + def.id + "…")
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
