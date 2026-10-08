// =============================================================================
// File: internal/app/copyto.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-07
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// copyto.go is "Copy to…": copy a file, a folder, or the tree's
// multi-selection into a folder the user TYPES — anywhere on disk, not
// just somewhere the file tree shows.
//
// WHY A SECOND VERB. Copy + Paste (copypaste.go) can only land in a
// folder the tree can point at, which means inside the open project.
// "Put this in another project", "back this folder up to ~/backup" or
// "drop the config next to the deploy scripts in /srv" had no door at
// all short of the terminal. Paste could have grown a "Paste to…" row,
// but that is two gestures for a one-gesture thought, and the source is
// already on screen when the user reaches for this.
//
// WHAT IT SHARES. Everything past the prompt is copypaste.go's engine:
// planCopyInto (collision-free names, the folder-into-itself refusal,
// missing sources), runCopyPlan (the goroutine, partial-copy cleanup),
// the unsaved-buffer overlay, and pasteDoneEvent — whose `into` field
// routes the receipt back here. So "main.go" copied twice into the same
// folder becomes "main copy.go" exactly as a paste would: Copy to… never
// overwrites anything either.
//
//	tree right-click "Copy to…" ─┐
//	tab right-click "Copy to…" ──┤
//	≡ File "Copy file to…" ──────┤
//	≡ File "Copy folder … to…" ──┼─▶ promptCopyTo ─▶ copyToTyped ─┬─▶ startCopyTo ─▶ runCopyPlan
//	Selected items "Copy N to…" ─┘    (▾ recent)     (resolve)    └─▶ confirm "Create folder" ─┘
//
// THE DESTINATION is a folder, always — the title says "to folder", and
// a path that names an existing FILE is refused rather than read as
// "copy as this name": one meaning per field, and an accidental file
// name can never turn into an overwrite. A folder that does not exist
// yet is created, but only after a confirm that names it, so a typo
// costs one "No" rather than a stray directory.
//
// RECALL. Destinations are remembered per project in the history
// database as their own list (history.CopyDestinations), recorded when a
// copy RUNS — the search history's rule — and recalled with the prompt's
// ▾ / Up dropdown. The field is seeded with the most recent one, because
// the common rhythm is several copies to one place.

package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rohanthewiz/ced/internal/filetree"
	"github.com/rohanthewiz/ced/internal/history"
)

// copyToHint is the prompt's hint row. Kept to 34 columns so the history
// dropdown's "↑ recent" tip still fits beside it in the 54-wide prompt
// (promptModal.draw gives the pair mw-4 cells).
const copyToHint = "absolute, ~/path, or root-relative"

// copyToTitleMax caps the source name in the prompt's title, so a long
// file name cannot push "to folder" — the half that says what the field
// wants — off the frame.
const copyToTitleMax = 28

// -----------------------------------------------------------------------------
// The verb
// -----------------------------------------------------------------------------

// promptCopyTo asks where to copy paths, then copies them there. Shared
// by every door, so they all ask the same question the same way.
func (a *App) promptCopyTo(paths []string) {
	if len(paths) == 0 {
		a.flash("Nothing to copy")
		return
	}
	// Captured by value: the tree's selection or the active tab can
	// change while the prompt is up, and the copy must be of what the
	// user was looking at when they asked.
	srcs := append([]string(nil), paths...)
	a.openSearchPrompt(
		"Copy "+copyToWhat(srcs)+" to folder",
		copyToHint,
		a.copyToSeed(srcs),
		history.CopyDestinations,
		func(app *App, value string) { app.copyToTyped(srcs, value) },
	)
}

// copyToWhat names the sources for a title or a flash: the basename for
// one item (with a trailing / for a folder, so "pkg/" and a file called
// "pkg" read differently), a count for a set. Long names keep their
// TAIL — the extension is the informative end.
func copyToWhat(paths []string) string {
	if len(paths) != 1 {
		return fileClipLabel(paths)
	}
	name := filepath.Base(paths[0])
	if info, err := os.Stat(paths[0]); err == nil && info.IsDir() {
		name += "/"
	}
	if r := []rune(name); len(r) > copyToTitleMax {
		name = "…" + string(r[len(r)-copyToTitleMax+1:])
	}
	return name
}

// copyToSeed is the prompt's initial value: the last destination this
// project copied to, else the folder the (first) source lives in — so the
// field is an EDIT of a real path rather than a blank to fill from
// memory. Seeding with the source's own folder is harmless if accepted
// as-is: the copy lands beside the original as "name copy.ext".
func (a *App) copyToSeed(paths []string) string {
	if recent := a.searchHistory(history.CopyDestinations); len(recent) > 0 {
		return recent[0]
	}
	return displayPath(filepath.Dir(paths[0]))
}

// copyToRoot is the absolute project root relative destinations resolve
// against. The tree root rather than a.rootDir, which keeps the user's
// verbatim launch argument ("." in the common case) — and the process
// cwd that "." would then mean moves whenever the terminal panel runs
// `cd` (the absolute-paths rule).
func (a *App) copyToRoot() string {
	if a.tree != nil && a.tree.Root != nil {
		return a.tree.Root.Path
	}
	if abs, err := filepath.Abs(a.rootDir); err == nil {
		return abs
	}
	return a.rootDir
}

// resolveCopyDest turns the typed destination into a clean absolute
// path: ~ expanded, relative paths joined to the project root (the
// root is on screen; the process cwd is not — resolveFolder's rule).
// Existence is the caller's question, because "missing" is a prompt here
// rather than an error.
func (a *App) resolveCopyDest(value string) string {
	p := expandHome(strings.TrimSpace(value))
	if !filepath.IsAbs(p) {
		p = filepath.Join(a.copyToRoot(), p)
	}
	return filepath.Clean(p)
}

// copyToTyped is the prompt's callback: resolve what was typed and
// route on what is there. An existing folder copies straight away; a
// missing one asks first; anything else explains itself.
func (a *App) copyToTyped(paths []string, value string) {
	dest := a.resolveCopyDest(value)
	info, err := os.Stat(dest)
	switch {
	case err == nil && info.IsDir():
		a.startCopyTo(paths, dest)
	case err == nil:
		a.flash(displayPath(dest) + " is a file — Copy to… takes a folder")
	case errors.Is(err, fs.ErrNotExist):
		a.confirmCreateCopyDest(paths, dest)
	default:
		// Permission denied on an ancestor, a too-long name, …: the os
		// error says which, and nothing here can say it better.
		a.flash("Copy failed: " + err.Error())
	}
}

// confirmCreateCopyDest asks before creating a destination folder that
// does not exist yet, naming it in full — the one moment a typo would
// otherwise turn into a directory nobody meant to make.
//
// The copy is planned FIRST, so a copy that would be refused anyway
// (a folder into itself, every source gone) is refused before the user
// is asked to create somewhere for it to land.
func (a *App) confirmCreateCopyDest(paths []string, dest string) {
	plan, missing, err := planCopyInto(paths, dest)
	if err != nil {
		a.flash("Can't copy a folder into itself")
		return
	}
	if len(plan) == 0 {
		a.flash(copyToGoneMessage(paths, missing))
		return
	}
	what := copyToWhat(paths)
	// The path gets a line of its own: the drawer elides a line that
	// doesn't fit, and "…/very/long/path does not exist." would lose
	// exactly the words that say what is being asked.
	a.openConfirmLines("Create folder", []string{
		"This folder does not exist yet:",
		displayPath(dest),
		"Create it and copy " + what + " into it?",
	}, func(app *App) {
		if err := os.MkdirAll(dest, 0o755); err != nil {
			app.flash("Copy failed: " + err.Error())
			return
		}
		app.startCopyTo(paths, dest)
	})
}

// startCopyTo plans the copy of paths into destDir (which exists) and
// starts it. Planned again even after confirmCreateCopyDest planned it:
// the confirm can sit open for minutes, and the plan must describe the
// disk as it is when the copy starts, not as it was when the dialog
// opened.
func (a *App) startCopyTo(paths []string, destDir string) {
	plan, missing, err := planCopyInto(paths, destDir)
	if err != nil {
		a.flash("Can't copy a folder into itself")
		return
	}
	if len(plan) == 0 {
		a.flash(copyToGoneMessage(paths, missing))
		return
	}
	// Recorded where the copy RUNS (the search-history rule), in display
	// form: "~/backup" reads better in the dropdown than the home path
	// spelled out, and expandHome turns it back on the way in. Relative
	// input is stored resolved, so "../x" and "~/projs/x" are one entry.
	a.recordSearch(history.CopyDestinations, displayPath(destDir))

	where := displayPath(destDir)
	if len(missing) > 0 {
		// A partial set is reported, never silently narrowed (startPaste's
		// rule) — the receipt's count would otherwise be the only clue.
		a.flash(fmt.Sprintf("Copying %d items to %s (%d no longer exist)…", len(plan), where, len(missing)))
	} else {
		a.flash("Copying " + copyToWhat(paths) + " to " + where + "…")
	}
	a.runCopyPlan(plan, a.dirtyBufferOverlay(paths), destDir)
}

// copyToGoneMessage is the refusal when no source survived to be copied.
func copyToGoneMessage(paths, missing []string) string {
	if len(paths) == 1 {
		return "Copy failed: " + filepath.Base(paths[0]) + " no longer exists"
	}
	return fmt.Sprintf("Copy failed: none of the %d items still exist (%s)", len(paths), strings.Join(missing, ", "))
}

// handleCopyToDone is handlePasteDone for a Copy to… run: the same
// workspace re-sync (the destination may be inside the project), but a
// receipt that says WHERE, since the copy usually landed somewhere the
// tree is not showing.
func (a *App) handleCopyToDone(e *pasteDoneEvent) {
	if e.count > 0 {
		a.workspaceChanged()
	}
	where := displayPath(e.into)
	if e.err != nil {
		if e.count > 0 {
			// Earlier items are complete copies and stay (runCopyPlan's
			// rule); say so, or the user would assume nothing landed.
			a.flash(fmt.Sprintf("Copy failed after %d item(s) reached %s: %v", e.count, where, e.err))
			return
		}
		a.flash("Copy failed: " + e.err.Error())
		return
	}
	a.flash(copyToReceipt(e) + bufferedNote(e.buffered))
}

// copyToReceipt is the success flash's text. One item says its name and,
// when the destination already held that name, the name the copy took —
// "Copied main.go to ~/x" is a lie when the new file is "main copy.go".
func copyToReceipt(e *pasteDoneEvent) string {
	where := displayPath(e.into)
	if e.count != 1 {
		return fmt.Sprintf("Copied %d items to %s", e.count, where)
	}
	from, to := filepath.Base(e.src), filepath.Base(e.dest)
	// A folder wears its trailing / here as in the prompt's title
	// (copyToWhat), so "pkg/" doesn't turn into "pkg" between the two.
	if info, err := os.Stat(e.dest); err == nil && info.IsDir() {
		from, to = from+"/", to+"/"
	}
	if from != to {
		return fmt.Sprintf("Copied %s to %s as %s", from, where, to)
	}
	return "Copied " + from + " to " + where
}

// -----------------------------------------------------------------------------
// Doors
// -----------------------------------------------------------------------------

// menuCopyFileTo is ≡ File "Copy file to…": the active tab's file.
func (a *App) menuCopyFileTo() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" {
		return
	}
	a.promptCopyTo([]string{tab.Path})
}

// menuCopyFolderTo is ≡ File "Copy folder (sub/) to…": the active folder,
// or the whole project when none is active. Unlike the clipboard's Copy
// folder row, the root is a legal source here — every paste destination
// is inside it, but a Copy to… destination need not be, and backing a
// project up elsewhere is a fair thing to want. A destination inside it
// is still refused (planCopyInto).
func (a *App) menuCopyFolderTo() {
	a.closeMenu()
	a.promptCopyTo([]string{a.pasteTargetDir()})
}

// copyFolderToLabel is the dynamic label for the ≡ folder row, naming
// the folder it will act on — copyFolderLabel's suffix, or "project
// folder" for the root, which that row never offers. Keyed on the same
// predicate pasteTargetDir falls back by, so label and action agree.
func (a *App) copyFolderToLabel() string {
	if !a.hasActiveSubfolder() {
		return "Copy project folder to…"
	}
	return a.copyFolderLabel() + " to…"
}

// ctxCopyTo copies the node the user right-clicked in the tree. Offered
// on the root too (menuCopyFolderTo's reason).
func ctxCopyTo(a *App, n *filetree.Node) {
	a.promptCopyTo([]string{n.Path})
}
