// =============================================================================
// File: internal/app/openterminal.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-08
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// "Open in terminal" — a shell sitting in a tree entry's folder, from the
// tree's right-click menu or the ≡ File row. WHICH terminal is the
// "terminal" key in config.json (userconfig.Config.Terminal):
//
//	"auto" (default) ─┬─ cats Tier 1? ──► cats pane below, cwd = folder
//	                  ├─ $TMUX set?   ──► tmux split-window -v -c folder
//	                  └─ otherwise    ──► ced's terminal panel, `cd folder`
//	"cats" / "tmux"   ── that one; not inside it → ced's panel + a flash
//	"ced"             ── ced's terminal panel
//	anything else     ── a command line: sh -c '<it>' from inside the
//	                     folder, {{DIR}} → the folder's quoted path
//	                     (e.g. "open -a Ghostty {{DIR}}")
//
// House rules:
//
//   - **THE TARGET IS A FOLDER.** A file means its parent: nobody wants a
//     shell "in" a file, and the folder holding it is what the click was
//     reaching for. The root is included — "a shell at the project" is
//     the commonest form of the request.
//
//   - **AUTO PREFERS A REAL PTY.** ced's panel is a grsh REPL strip, not a
//     pty (terminal.go) — fine for `ls` and `go test`, useless for vim or
//     htop. A cats pane or a tmux split is a real terminal at the cost of
//     one control call, so auto climbs to the best one available and lands
//     on the panel only when there is nothing better. That keeps the
//     feature whole at Tier 0 (no feature may exist only at Tier 1) while
//     letting it be better where it can.
//
//   - **AN UNAVAILABLE CHOICE FALLS BACK AND SAYS SO.** "cats" outside cats
//     or "tmux" outside tmux still gets the user a terminal — the panel is
//     always there — and the flash names why it is not the one they
//     configured. menuCatsTerminal's rule.
//
//   - **IN THE PANEL THE cd IS SUBMITTED, NOT STAGED.** runexec stages
//     because the user still owes arguments; here the whole request was
//     "be in that folder" and the cd is all of it. A busy panel is the
//     exception: the line is staged and the flash says why.
//
//   - **A CUSTOM COMMAND IS THE USER'S OWN SHELL TEXT**, so it runs under
//     sh exactly as written, in the folder, detached (hostRun's Setsid).
//     The folder is also its cwd, so a command with no {{DIR}} — `open -a
//     Terminal .`, `wezterm start` — still lands in the right place.

package app

import (
	"os"
	"path/filepath"

	"github.com/rohanthewiz/ced/internal/cats"
	"github.com/rohanthewiz/ced/internal/filetree"
	"github.com/rohanthewiz/ced/internal/session"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// termKind is where "Open in terminal" resolved to for one click.
type termKind int

const (
	termKindPanel   termKind = iota // ced's own grsh panel
	termKindCats                    // a cats sibling pane
	termKindTmux                    // a tmux split
	termKindCommand                 // the user's own command line
)

// resolveTerminal turns the configured preference into this click's
// terminal, plus a note to flash when a named choice had to fall back.
// Asked per click rather than at startup because both answers can change
// under a running editor: the cats probe completes on a goroutine, and the
// same binary is started inside and outside tmux.
func (a *App) resolveTerminal() (kind termKind, note string) {
	inTmux := hostEnv("TMUX") != ""
	switch a.terminalPref {
	case "", userconfig.TerminalAuto:
		switch {
		case a.catsTier1():
			return termKindCats, ""
		case inTmux:
			return termKindTmux, ""
		}
		return termKindPanel, ""
	case userconfig.TerminalCed:
		return termKindPanel, ""
	case userconfig.TerminalCats:
		if a.catsTier1() {
			return termKindCats, ""
		}
		return termKindPanel, "No cats control socket — opened ced's own terminal instead"
	case userconfig.TerminalTmux:
		if inTmux {
			return termKindTmux, ""
		}
		return termKindPanel, "Not inside tmux ($TMUX is unset) — opened ced's own terminal instead"
	}
	return termKindCommand, ""
}

// terminalDirFor is the folder a terminal should open in for path: the
// path itself when it is a directory, its parent otherwise. Read from disk
// so a path replaced since the tree's last refresh still takes the right
// branch; ok=false when the path is gone.
func terminalDirFor(path string) (dir string, ok bool) {
	abs := absolutePathFor(path)
	info, err := os.Stat(abs)
	if err != nil {
		return "", false
	}
	if info.IsDir() {
		return abs, true
	}
	return filepath.Dir(abs), true
}

// menuOpenTerminal is the ≡ File row: the active file's folder, else the
// project root (openInEditorTarget's target, then terminalDirFor's rule).
func (a *App) menuOpenTerminal() {
	a.closeMenu()
	a.openTerminalAt(a.openInEditorTarget())
}

// ctxOpenTerminal is the tree's right-click row: the clicked node's folder.
func ctxOpenTerminal(a *App, n *filetree.Node) {
	a.closeModal()
	a.openTerminalAt(n.Path)
}

// openTerminalAt opens the configured terminal in path's folder. See the
// file header for the resolution ladder.
func (a *App) openTerminalAt(path string) {
	if path == "" {
		a.flash("Nothing to open")
		return
	}
	dir, ok := terminalDirFor(path)
	if !ok {
		a.flash(filepath.Base(path) + " no longer exists")
		return
	}
	kind, note := a.resolveTerminal()
	switch kind {
	case termKindCats:
		a.openCatsTerminalAt(dir)
	case termKindTmux:
		// -v: a strip BELOW, menuCatsTerminal's shape — a shell is
		// something you glance down at beside your work. -c gives the new
		// pane its cwd directly, so no cd lands in its scrollback.
		a.hostRunAsync(dir, []string{"tmux", "split-window", "-v", "-c", dir}, "tmux split")
		a.flash("Opened a tmux pane in " + displayPath(dir))
	case termKindCommand:
		line := expandEntryTemplate(a.terminalPref, dir, dir)
		a.hostRunAsync(dir, []string{"sh", "-c", line}, "Open in terminal")
		a.flash("Opening a terminal in " + displayPath(dir))
	default:
		a.openPanelTerminalAt(dir, note)
	}
}

// openCatsTerminalAt splits a cats pty pane below, started in dir. Both
// spellings of "start there" go along, for the two vintages of host (see
// menuCatsTerminal): Cwd for a host that takes it, the cd line for one
// that does not.
func (a *App) openCatsTerminalAt(dir string) {
	client, scr := a.cats.client, a.screen
	self := a.catsSelfPane()
	spawn := catsSpawn{Cwd: dir, Line: "cd " + catsShellQuote(dir) + "\n"}
	where := displayPath(dir)
	go func() {
		if _, err := catsSpawnSibling(client, self, cats.SplitVertical, spawn); err != nil {
			catsPostNotice(scr, "Terminal split failed: "+err.Error())
			return
		}
		catsPostNotice(scr, "Opened a terminal pane in "+where)
	}()
}

// openPanelTerminalAt brings up ced's own panel sitting in dir. note, when
// set, is a fallback explanation from resolveTerminal and wins the flash —
// "why isn't this the terminal I configured" is the bigger news.
func (a *App) openPanelTerminalAt(dir, note string) {
	flash := func(msg string) {
		if note != "" {
			msg = note
		}
		a.flash(msg)
	}
	if session.Normalize(dir) == session.Normalize(a.termCwd()) {
		// Already there: open and focus, and send nothing — a pointless
		// cd in the scrollback looks like the editor lost track of where
		// its own shell is.
		a.focusTermPanel()
		flash("Terminal is in " + displayPath(dir))
		return
	}
	if a.runInTermPanel("cd " + shellArg(dir)) {
		flash("Terminal is in " + displayPath(dir))
		return
	}
	flash("Terminal is busy — the cd is staged; press Enter when it finishes")
}

// focusTermPanel opens ced's terminal panel if needed, makes sure it has a
// session, and gives it the keyboard. menuToggleTerminal is a strict
// toggle, hence the guard (stageRun's reasoning).
func (a *App) focusTermPanel() {
	if !a.term.open {
		a.menuToggleTerminal()
	}
	a.ensureTermSession()
	a.term.focused = true
}

// runInTermPanel puts line on ced's terminal input and submits it, opening
// and focusing the panel first. Returns false when a command is still
// running there: the line is then STAGED instead (submitting would only be
// refused), so nothing the user asked for is lost — they press Enter once
// the panel is free. Shared by "Open in terminal" and "Shell command…".
func (a *App) runInTermPanel(line string) bool {
	a.focusTermPanel()
	a.term.input = newTextField(line)
	if a.term.running {
		return false
	}
	a.submitTermCommand()
	return true
}
