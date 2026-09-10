// =============================================================================
// File: internal/app/openineditor.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// "Open in $EDITOR" — hand a file or a folder to whatever the user's
// environment says their editor is, from the tree's right-click menu or
// the ≡ File row.
//
// The point is not that ced is inadequate; it is that a terminal is a
// place where several tools share one workspace, and `$EDITOR` is the
// name that workspace already agreed on. A reviewer who lives in ced but
// keeps vim bindings for one kind of surgery, a `code .` on the project
// root, an emacs client — all of them are one row away, and none of them
// needed ced to know anything about them.
//
// House rules:
//
//   - **$VISUAL BEATS $EDITOR**, which is the convention's own answer to
//     exactly this question: $VISUAL is what you set when a full-screen
//     program is welcome, $EDITOR is the line-editor fallback for when it
//     is not. Opening a pane IS the full-screen case.
//
//   - **THE ROW NAMES THE EDITOR, not the variable.** "Open in vim" says
//     what will happen; "Open in $EDITOR" asks the user to remember what
//     they exported. Same rule as the theme row naming the theme in force.
//
//   - **NO $EDITOR MEANS NO ROW.** The tree popup is deliberately small
//     and its fixed vocabulary is something users learn positions in, so
//     a permanently dimmed row that can only ever say "it isn't set"
//     would be worse than its absence — the Paste row's argument. The ≡
//     row is the opposite case and dims instead: the menu is where you go
//     to find out what the editor can do, and a missing row there teaches
//     nothing.
//
//   - **TIER 1 RUNS IT, TIER 0 STAGES IT** — catsRun's own split, and
//     here it is structural rather than merely careful. Inside cats a
//     sibling pane is a REAL pty, so vim, emacs and helix all work.
//     ced's own terminal panel is a REPL strip and explicitly not a pty
//     (see terminal.go), so a full-screen editor cannot run in it; what
//     it can do is put the command on the input line where the user can
//     see it, edit it, and decide. That is the honest Tier-0 answer, and
//     the flash says which one they got.
//
//   - **SIDE BY SIDE, not stacked.** menuCatsTerminal splits vertically
//     because a terminal is a strip under your work; this is a peer
//     editor on the same file tree, and a half-height vim is a worse
//     place to edit than a half-width one.
//
//   - **A DIRECTORY IS A LEGITIMATE TARGET.** `vim .`, `code .`,
//     `emacs .` all mean something, and the project root is the most
//     useful of them — which is why the row is offered on the root too,
//     unlike Rename and Delete.

package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rohanthewiz/ced/internal/cats"
	"github.com/rohanthewiz/ced/internal/filetree"
)

// editorEnv is the environment lookup, indirected so tests can state an
// editor without touching the process environment (t.Setenv would leak
// across the parallel-safe helpers newTestApp installs).
var editorEnv = os.Getenv

// editorCommand resolves the user's editor: $VISUAL first, then $EDITOR.
// Returns "" when neither is set, which every caller reads as "this
// feature is unavailable here" rather than as an error.
//
// The value is a COMMAND LINE, not a program name — `ced --wait`,
// `code -w`, `emacsclient -nw` are all ordinary settings — so it is
// passed to a shell rather than exec'd, and never split by this code.
func editorCommand() string {
	if v := strings.TrimSpace(editorEnv("VISUAL")); v != "" {
		return v
	}
	return strings.TrimSpace(editorEnv("EDITOR"))
}

// editorDisplayName is the editor's name for a menu label: the base name
// of the command's first word. `/usr/local/bin/nvim -u NONE` reads as
// "nvim", which is the part a user recognises — the flags are noise in a
// row that has to fit a popup.
func editorDisplayName() string {
	cmd := editorCommand()
	if cmd == "" {
		return ""
	}
	first := strings.Fields(cmd)[0]
	return filepath.Base(first)
}

// hasOpenInEditor gates the ≡ row: an editor is configured and there is
// something to hand it. Cheap enough for a predicate menuLayout runs on
// every frame the menu is open — two environment reads and a nil check.
func (a *App) hasOpenInEditor() bool {
	return editorCommand() != "" && a.openInEditorTarget() != ""
}

// openInEditorLabel names the editor when there is one and the variable
// when there is not, because those are two different pieces of news: the
// first says what the row will do, the second says what to set.
func (a *App) openInEditorLabel() string {
	if name := editorDisplayName(); name != "" {
		return "Open in " + name
	}
	return "Open in $EDITOR"
}

// openInEditorTarget is what the ≡ row acts on: the active file, falling
// back to the project root. The fallback is not a consolation — `code .`
// on the root is one of the two things people actually want from this
// row, and an editor with no file open should still be able to say it.
func (a *App) openInEditorTarget() string {
	if tab := a.activeTabPtr(); tab != nil && tab.Path != "" {
		return tab.Path
	}
	return a.rootDir
}

// menuOpenInEditor is the ≡ File row — the keyboard twin of the tree's
// context row, and the path that survives a terminal which swallows
// right-click (the project's mouse-first-with-a-menu-twin rule).
func (a *App) menuOpenInEditor() {
	a.closeMenu()
	a.openInEditor(a.openInEditorTarget())
}

// ctxOpenInEditor is the tree-context counterpart: hand the clicked node
// over, file or folder alike.
func ctxOpenInEditor(a *App, n *filetree.Node) {
	a.closeModal()
	a.openInEditor(n.Path)
}

// openInEditor composes `$EDITOR <path>` and gets it running — in a cats
// sibling pane when there is one, staged on ced's own terminal line when
// there is not. See the file header for why those are the two answers.
func (a *App) openInEditor(path string) {
	cmd := editorCommand()
	if cmd == "" {
		a.flash("Neither $VISUAL nor $EDITOR is set")
		return
	}
	if path == "" {
		a.flash("Nothing to open")
		return
	}

	if !a.catsTier1() {
		// Tier 0. The staged line carries an ABSOLUTE path because ced's
		// terminal has its own working directory (grsh's `cd` moves it)
		// and a relative path would silently mean somewhere else.
		a.catsRunInPanel(cmd + " " + shellArg(absolutePathFor(path)))
		a.flash("Staged " + editorDisplayName() + " in ced's terminal — press Enter (a full-screen editor needs a real terminal)")
		return
	}

	// Tier 1. The pane starts in the project root, so the path is written
	// relative to it when it lives inside — the line the user reads in
	// their scrollback should look like one they would have typed.
	line := cmd + " " + shellArg(a.catsRelPath(path)) + "\n"
	client, scr := a.cats.client, a.screen
	self := a.catsSelfPane()
	spawn := catsSpawn{Cwd: a.rootDir, Line: line}
	name := editorDisplayName()
	go func() {
		if _, err := catsSpawnSibling(client, self, cats.SplitHorizontal, spawn); err != nil {
			catsPostNotice(scr, "Open in "+name+" failed: "+err.Error())
			return
		}
		catsPostNotice(scr, "Opened "+name+" beside you")
	}()
}
