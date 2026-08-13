// =============================================================================
// File: internal/app/catssplit.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// "Open in split" — the editor asks its host to put a second editor beside
// it, on the same file, in the same project.
//
// WHY A SECOND PROCESS AND NOT A SPLIT PANE INSIDE ced
//
// ced has one editor viewport by design, and giving it internal splits would
// mean a second viewport, a second focus owner, and a second everything that
// currently reads "the active tab". Inside cats the multiplexer already owns
// panes — that is its entire job — so the split it can do properly costs ced
// a control-socket call, and the two editors are ordinary independent
// processes that happen to sit next to each other.
//
// Two ceds on one file is only safe because Phase 2 landed first: every save
// stats the file it is about to overwrite, and a buffer whose file moved
// underneath it raises the conflict prompt instead of clobbering. That is
// the load-bearing dependency, not the split call.
//
// THE SPAWN IS THREE ROUND TRIPS, AND THE MIDDLE ONE IS A DIFF
//
//	pane.list   ─► the set of pane ids that exist now
//	pane.split  ─► the host opens a shell beside us … and tells us nothing
//	pane.list   ─► the set again; the id that appeared is the new pane
//	pane.send_input ─► type the ced command into that shell
//
// pane.split answers `ok` with no data, unlike tab.create which returns its
// new pane — so the id has to be recovered by diffing. That is the single
// ugliest thing in this file and it is upstream's to fix (roadmap §5: have
// pane.split return the pane it created). The diff is safe in the way that
// matters — a stray third pane appearing in the same millisecond makes the
// result AMBIGUOUS, and an ambiguous diff sends input to nobody rather than
// to a guess (see catsNewPane).
//
// Why send_input rather than a command: pane.split spawns the user's shell
// and has no argv parameter (tab.create does). So the sibling is started by
// typing at it, which is also why the line begins with `exec` — the shell
// replaces itself with the editor, so quitting the editor closes the pane
// instead of dropping the user at a prompt they did not ask for.
//
// Every step is IO, so the whole sequence runs on a goroutine and reports
// back through catsPostNotice. Nothing here touches App state after the
// launch — the goroutine is handed plain values, never the App.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/cats"
)

// catsExecutable resolves the path of the running ced binary — the program
// the sibling pane should start. A package var so a test can pin it without
// depending on where `go test` put its temporary binary.
var catsExecutable = os.Executable

// catsSplitRight opens the current file in a pane beside this one, and
// catsSplitDown in a pane below it. Two entry points rather than one with a
// parameter because they are two menu rows, two leader keys, and two context
// rows — and a bound method is what all three of those want.
//
// The direction names are ced's (what the user sees), not cats' (h/v).
func (a *App) catsSplitRight() { a.catsOpenInSplit(cats.SplitHorizontal, "→") }
func (a *App) catsSplitDown()  { a.catsOpenInSplit(cats.SplitVertical, "↓") }

// catsOpenInSplit is the shared body: check we can, then hand the whole
// sequence to a goroutine.
//
// The preconditions are checked HERE, on the loop, where a refusal can be
// explained. A row that is enabled but silently does nothing is worse than
// a dimmed one; a row that says why is better than both.
func (a *App) catsOpenInSplit(direction, arrow string) {
	a.closeMenu()
	if !a.catsTier1() {
		a.flash("Splits need cats — ced is running in a plain terminal")
		return
	}
	path := a.catsSplitTarget()
	if path == "" {
		a.flash("Nothing to open in a split — save this tab to a file first")
		return
	}
	exe, err := catsExecutable()
	if err != nil {
		a.flash("Split: cannot find this ced binary: " + err.Error())
		return
	}
	line := catsSpawnLine(exe, a.rootDir, path)

	client, scr := a.cats.client, a.screen
	// A resolved self is what makes the new pane land beside THIS one. If
	// the handle never resolved, catsSelfPane's nil means "split the focused
	// pane", which is us in every case where the user just clicked a row.
	self := a.catsSelfPane()
	shown := filepath.Base(path)
	go func() {
		if _, err := catsSpawnSibling(client, self, direction, line); err != nil {
			catsPostNotice(scr, "Split failed: "+err.Error())
			return
		}
		catsPostNotice(scr, "Opened "+shown+" in a split "+arrow)
	}()
}

// catsSplitTarget is the file the split would open: the active tab's path.
// An untitled buffer has no file for a second process to open, and an image
// preview would open in a text editor — both decline rather than substitute
// something else.
func (a *App) catsSplitTarget() string {
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" || tab.IsImage() {
		return ""
	}
	abs, err := filepath.Abs(tab.Path)
	if err != nil {
		return tab.Path
	}
	return abs
}

// hasCatsSplit gates the menu rows: in a cats pane, with Tier 1 up, on a tab
// that has a file. The rows exist (dimmed) below that so the vocabulary
// stays in one place — the ≡-menu convention.
func (a *App) hasCatsSplit() bool { return a.catsTier1() && a.catsSplitTarget() != "" }

// catsSpawnSibling runs the four-call sequence against the host and returns
// the pane it created. It is a free function taking a client so the goroutine
// never sees the App.
//
// An EMPTY line means "just make me a pane": the split is the whole request,
// so the two listings are still made (the caller wants the id — a terminal
// pane is reused by the next run) but an unidentifiable pane is no longer a
// failure. Nothing is going to be typed into it either way.
func catsSpawnSibling(client *cats.Client, pane *uint32, direction, line string) (uint32, error) {
	before, err := client.PaneList()
	if err != nil {
		return 0, err
	}
	if err := client.PaneSplit(pane, direction); err != nil {
		return 0, err
	}
	after, err := client.PaneList()
	if err != nil {
		return 0, err
	}
	target, ok := catsNewPane(before, after)
	if !ok {
		if line == "" {
			// The pane is there and empty, which is exactly what was asked
			// for; we simply cannot name it. A caller that wanted to reuse it
			// later gets a fresh split next time instead.
			return 0, nil
		}
		// The split itself SUCCEEDED — there is now an empty shell beside
		// the user. Saying so is the honest report: they can type the
		// command themselves, and a bare "failed" would send them looking
		// for a pane that is already there.
		return 0, fmt.Errorf("split opened, but its pane could not be identified — run the editor there by hand")
	}
	if line == "" {
		return target, nil
	}
	// submit: the newline is already in the line, and the shell needs the
	// press to run it. This is the one place ced puts words in something
	// else's mouth AND presses enter — legitimate because the something
	// else is a shell we just created for exactly this purpose.
	return target, client.PaneSendInput(target, line, true)
}

// catsNewPane finds the pane that appeared between two pane.list snapshots.
//
// It insists the answer be UNIQUE. Exactly one new pane is the normal case
// (we just made it); zero means the split silently did nothing; two or more
// means somebody else's split raced ours and there is no way to tell which
// half is ours. Both of the odd cases return false, because typing a command
// into the wrong pane is a far worse outcome than not typing it at all.
func catsNewPane(before, after []cats.PaneInfo) (uint32, bool) {
	old := make(map[uint32]bool, len(before))
	for _, p := range before {
		old[p.Pane] = true
	}
	var found uint32
	n := 0
	for _, p := range after {
		if !old[p.Pane] {
			found, n = p.Pane, n+1
		}
	}
	return found, n == 1
}

// catsSpawnLine builds the shell line typed into the new pane:
//
//	exec '/usr/local/bin/ced' --root '/home/me/proj' '/home/me/proj/a/b.go'
//
// exec, so quitting the editor closes the pane rather than leaving a shell
// prompt the user has to quit twice out of. --root, so the sibling opens
// THIS project rather than the file's own parent directory (see main.go).
// Everything quoted, because a path with a space in it is a path, not an
// argument list.
func catsSpawnLine(exe, root, file string) string {
	return fmt.Sprintf("exec %s --root %s %s\n",
		catsShellQuote(exe), catsShellQuote(root), catsShellQuote(file))
}

// catsShellQuote wraps s in single quotes for POSIX shells. A single quote
// inside the string is handled the only way a single-quoted shell word can
// handle one: close the quote, emit an escaped quote, reopen — the sequence
// spelled quote-backslash-quote-quote in the code below.
//
// This is the safety boundary of the whole file: everything inside the
// quotes is a path the user (or the file system) chose, and it is about to
// be typed at a shell. There is no cats-side validation to fall back on.
func catsShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// compile-time reminder that the notice path is the only way this file's
// goroutines talk back; catsPostNotice takes the screen, never the App.
var _ func(tcell.Screen, string) = catsPostNotice
