// =============================================================================
// File: internal/app/gitrestore.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitrestore.go is "Restore": throw away ONE file's uncommitted changes
// and put it back the way the last commit has it. Three doors, one verb
// (restoreFile):
//
//	tree right-click on a file   "Git restore…"           (the clicked file)
//	tab right-click              "Restore (discard changes)…" (the clicked tab)
//	≡ Git                        "Restore file (discard changes)…" (active tab)
//
// The ≡ row is the twin the macOS Terminal + tmux rule demands — the
// right button is often swallowed — and acts on the active tab like the
// other per-file ≡ Git rows.
//
// Flow:
//
//	restoreFile(path)
//	  │  probeGitRestore  — synchronous: status code, in-HEAD?, numstat
//	  ├─ untracked / new   → flash (nothing to restore TO; Delete is the verb)
//	  ├─ clean, tab clean  → flash "no uncommitted changes"
//	  ├─ clean, tab dirty  → confirm → ReloadUndoable (buffer-only revert)
//	  └─ changed           → confirm → git checkout HEAD -- path
//	                                     └ onOK: adopt into the open tab
//
// Design choices:
//
//   - **`checkout HEAD --`, not `restore --worktree`.** It resets the
//     work tree AND the index entry, i.e. `git restore --source=HEAD
//     --staged --worktree`: "make this file look like the last commit",
//     which is what aborting a file's changes means. It is also exactly
//     the git panel's Discard (gitPanelDiscard), so the two surfaces can
//     never disagree about what "throw it away" did — and it works on
//     every git ced supports, not only 2.23+.
//   - **The confirm says what will be lost.** The tree-marks picker
//     deliberately carries no Discard because it cannot show the loss
//     (treemarks.go); a single file can — the probe's `diff --numstat
//     HEAD` gives "+12 −3 lines", and the body names staged work and
//     the open tab's unsaved edits separately, because those are the two
//     losses a user does not see from the tree.
//   - **The open tab keeps an Undo.** After git rewrites the file the tab
//     is reloaded with ReloadUndoable rather than left to the reconcile
//     tick: the tick would do the same for a CLEAN tab, but on a DIRTY
//     one it would record a conflict and raise "changed on disk" about
//     ced's own write. One Undo brings back the buffer as it stood —
//     unsaved edits included — so the restore is reversible for any file
//     that was open. A file that was not open is not, and the confirm
//     says so by omission (it only promises the Undo when a tab exists).
//   - **The reconcile tick is held off the path while git runs** with the
//     formatter's in-flight counter (formatRunBegin/End). It means "ced's
//     own write is mid-flight on this path", which is exactly this case;
//     a second counter with the same meaning would only be a second
//     thing reconcile has to remember to consult.
//   - **The probe runs on the main loop**, like gitPanelRevealFile's
//     refresh: three tiny forks, once, on a gesture — and a confirm
//     built from a 10s-old snapshot could promise the wrong loss.
//   - **Never dimmed** in the tab / ≡ menus for the hasGitFileTab reason:
//     a row gated on the stale snapshot lies just after a save. The
//     verb's own flash answers "nothing to restore".

package app

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/filetree"
)

// gitRestoreProbe is what restoreFile learns about one file before it
// asks: whether git sees a change at all, whether HEAD has a version to
// go back to, and how big the change is.
type gitRestoreProbe struct {
	// Code is the porcelain XY code, "" when git reports the file clean
	// (or ignored, or outside the work tree — all "nothing to restore").
	Code string
	// InHead reports whether HEAD has a blob at this path. False for an
	// untracked file, a staged add, the new side of a rename, and every
	// file on an unborn branch: none of them has a committed version.
	InHead bool
	// Added / Deleted are `diff --numstat HEAD` line counts; Binary is
	// set when git reports "-" for both (no line counts to give).
	Added, Deleted int
	Binary         bool
}

// probeGitRestore inspects path against HEAD. Each git call is run from
// the file's own directory so `HEAD:./name` resolves relative to it —
// the one spelling of a tree path that needs no toplevel arithmetic and
// so cannot trip over a symlinked root (macOS's /var → /private/var).
// Failures degrade to "clean": the caller then says there is nothing to
// restore, which is the honest answer when git cannot tell.
func probeGitRestore(path string) gitRestoreProbe {
	dir, base := filepath.Dir(path), filepath.Base(path)
	var p gitRestoreProbe

	out, err := exec.Command("git", "-C", dir, "status", "--porcelain", "--", base).Output()
	if err != nil {
		return p
	}
	// One path was asked for, so the first record is the answer. A
	// rename reports "R  old -> new"; only the XY code matters here.
	if line := strings.SplitN(string(out), "\n", 2)[0]; len(line) >= 3 {
		p.Code = line[:2]
	}

	p.InHead = exec.Command("git", "-C", dir, "cat-file", "-e", "HEAD:./"+base).Run() == nil
	if p.Code == "" || !p.InHead {
		return p
	}

	// `diff HEAD` covers staged and unstaged work together — the whole
	// of what checkout HEAD will throw away.
	ns, err := exec.Command("git", "-C", dir, "diff", "--numstat", "HEAD", "--", base).Output()
	if err == nil {
		p.Added, p.Deleted, p.Binary = parseNumstat(string(ns))
	}
	return p
}

// parseNumstat reads the first `git diff --numstat` record
// ("12\t3\tpath"). "-\t-\tpath" is git's binary marker. Anything
// unparseable yields zeros, which the confirm words as an unsized change.
func parseNumstat(out string) (added, deleted int, binary bool) {
	fields := strings.Fields(strings.SplitN(out, "\n", 2)[0])
	if len(fields) < 2 {
		return 0, 0, false
	}
	if fields[0] == "-" && fields[1] == "-" {
		return 0, 0, true
	}
	added, _ = strconv.Atoi(fields[0])
	deleted, _ = strconv.Atoi(fields[1])
	return added, deleted, false
}

// restoreFile is the one verb behind every Restore door: probe, explain
// the cases that have nothing to restore, otherwise confirm (focus on No)
// and run. See the file header for the case table.
func (a *App) restoreFile(path string) {
	if !a.gitIsRepo {
		a.flash("Not a git repository")
		return
	}
	if path == "" {
		a.flash("This buffer has never been saved")
		return
	}
	name := filepath.Base(path)
	tab := a.tabForSamePath(path)
	tabDirty := tab != nil && tab.Dirty && !tab.IsImage()
	p := probeGitRestore(path)

	switch {
	case p.Code != "" && !p.InHead:
		// Untracked or newly added: HEAD has no version, so "restore"
		// could only mean deleting the file — a different verb with its
		// own confirm, named here so the user knows where it lives.
		a.flash(name + " is new — no committed version to restore (Delete removes it)")
		return
	case p.Code == "" && !tabDirty:
		a.flash("No uncommitted changes in " + name)
		return
	case p.Code == "":
		// Disk already matches HEAD; the only changes are the buffer's.
		// Dropping them is still "abort this file's changes", so offer
		// it rather than answer "no changes" over a dirty tab.
		a.openConfirmLines("Restore file", []string{
			"Discard unsaved edits to " + name + "?",
			"The file already matches its last commit.",
			"One Undo in its tab brings the edits back.",
		}, func(app *App) { app.adoptRestoredFile(path, "Unsaved edits discarded") })
		return
	}

	a.openConfirmLines("Restore file", restoreConfirmLines(name, p, tab != nil, tabDirty),
		func(app *App) { app.runGitRestore(path) })
}

// restoreConfirmLines builds the confirm body for a file git sees as
// changed: the question, the size of the loss, then the two losses the
// tree cannot show (staged work, unsaved edits), and finally the escape
// hatch when there is one. One fact per line so nothing is elided.
func restoreConfirmLines(name string, p gitRestoreProbe, tabOpen, tabDirty bool) []string {
	lines := []string{"Restore " + name + " to its last commit?"}
	switch {
	case p.Binary:
		lines = append(lines, "Its uncommitted (binary) changes are lost.")
	case p.Added > 0 || p.Deleted > 0:
		lines = append(lines, fmt.Sprintf("+%d −%d lines of uncommitted change are lost.", p.Added, p.Deleted))
	default:
		// A pure mode change, a staged deletion with nothing in the
		// diff, an unparseable count: still a change, just unsized.
		lines = append(lines, "Its uncommitted changes are lost.")
	}
	if gitPanelStageState(p.Code) != stageNone {
		lines = append(lines, "Staged changes to it are unstaged and lost too.")
	}
	if tabDirty {
		lines = append(lines, "So are the open tab's unsaved edits.")
	}
	if tabOpen {
		lines = append(lines, "One Undo in its tab brings the text back.")
	}
	return lines
}

// runGitRestore runs `git checkout HEAD -- path` and, on success, adopts
// the result into the open tab. The reconcile tick is held off the path
// for the command's lifetime (see the file header), and released on BOTH
// outcomes — a leaked count would stop the tick watching the file forever.
func (a *App) runGitRestore(path string) {
	if a.screen == nil || a.rootDir == "" {
		return // runGitCmdFull would decline too; don't leak the hold.
	}
	name := filepath.Base(path)
	a.formatRunBegin(path)
	a.runGitCmdFull("Restore "+name, nil,
		func(app *App, _ *gitCmdDoneEvent) bool {
			app.formatRunEnd(path)
			return false // the generic error modal says what git said.
		},
		func(app *App) {
			app.formatRunEnd(path)
			app.adoptRestoredFile(path, "Restored "+name+" to its last commit")
		},
		nil,
		"checkout", "HEAD", "--", path)
}

// adoptRestoredFile reloads path's open tab (if any) from disk,
// undoably, and flashes msg — plus the Undo hint when there was a tab to
// put it in. Shared by the git path and the buffer-only path, which
// differ only in whether git rewrote the file first.
func (a *App) adoptRestoredFile(path, msg string) {
	tab := a.tabForSamePath(path)
	if tab == nil {
		a.flash(msg)
		return
	}
	if err := tab.ReloadUndoable(); err != nil {
		a.flash(fmt.Sprintf("%s reload failed: %v", filepath.Base(path), err))
		return
	}
	// Whatever the tick recorded about this file (a conflict from an
	// earlier outside write) is answered: the buffer now IS the disk.
	a.clearConflict(tab)
	a.flash(msg + " — undo in the tab to get yours back")
}

// tabForSamePath is tabForPath looking through symlinked spellings: the
// tree reports paths under the root as opened, git under its resolved
// toplevel, and a tab keeps whatever spelling opened it.
func (a *App) tabForSamePath(path string) *editor.Tab {
	if t := a.tabForPath(path); t != nil {
		return t
	}
	for _, t := range a.tabs {
		if t.Path != "" && samePath(t.Path, path) {
			return t
		}
	}
	return nil
}

// menuGitRestoreFile is the ≡ Git twin of the tree and tab rows, for the
// active tab.
func (a *App) menuGitRestoreFile() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil {
		a.flash("No file open")
		return
	}
	a.restoreFile(t.Path)
}

// ctxGitRestore is the tree right-click row's action: restore the
// clicked file.
func ctxGitRestore(a *App, n *filetree.Node) {
	a.restoreFile(n.Path)
}
