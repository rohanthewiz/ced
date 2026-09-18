// =============================================================================
// File: internal/app/treemarks.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-09
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// treemarks.go is the file tree's multi-selection: tick several rows,
// then run one verb over all of them. The tree could always act on ONE
// thing — the row you right-clicked, or the row the keyboard cursor sat
// on — which made "delete these six generated files" six confirmations
// and "zip this handful" impossible.
//
// The split of labour with internal/filetree: that package holds the set
// (Tree.Marked, keyed by path), keeps it honest across a refresh, and
// paints the tick. This file owns what the marks are FOR — the gestures
// that build the set and the picker that spends it. Exactly the split
// gitpanel.go / gitpanelactions.go make, and for the same reason: the
// tree is a renderer, not a place for verbs.
//
// House patterns in play:
//
//   - **The git panel's checkbox, one cell wide.** Its tick is a
//     multi-selection feeding an `Actions ▾` picker, not a stage
//     toggle; this is the same idea in a panel that has no room for a
//     four-cell gutter, so the tick borrows the blank leading cell every
//     row already has (filetree's paintMark) and the click zone is that
//     one column.
//   - **Targets fall back to the row under the pointer**
//     (`treeMarkTargets`, gitPanelTargets' rule), so the Actions picker
//     is useful on its first open, before anything is ticked.
//   - **Picker, not a bespoke dropdown** (modal.go's rule): every
//     choose-one-from-a-list surface in this editor is openPicker, so the
//     verb list is fuzzy-searchable and needs no new hit-testing. Rows
//     that would no-op are omitted rather than dimmed.
//   - **Every surface has a keyboard twin** (the macOS-Terminal rule):
//     marking is a gutter click, a Space press, or a right-click row;
//     the verbs are the picker, reachable from the ≡ File menu (and so
//     the palette) and the context menu. It had an `A` key in the
//     focused tree too, until typing there became a name search.
//   - **Destructive verbs confirm, naming the blast radius**
//     (fileops.go's rule) — and here that matters more than anywhere
//     else in the editor, because the set can hold rows that are
//     scrolled off screen or folded away inside a collapsed branch. The
//     confirm body LISTS what it is about to delete for exactly that
//     reason.

package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rohanthewiz/ced/internal/clipboard"
	"github.com/rohanthewiz/ced/internal/filetree"
)

// treeMarkConfirmList is how many names a destructive confirmation
// spells out before falling back to "…and N more". Long enough that the
// common set is fully visible, short enough to fit the modal.
const treeMarkConfirmList = 8

// -----------------------------------------------------------------------------
// Gestures
// -----------------------------------------------------------------------------

// treeMarkPress claims a sidebar press that means "mark", returning true
// when it consumed the event so sidebarClick never also opens the file.
//
// Two gestures, and the split is deliberate:
//
//   - A press in the row's FIRST COLUMN toggles the tick. That is the
//     git panel's checkbox gutter narrowed to the one cell the tree can
//     spare, and it is the primary gesture because this editor is
//     mouse-first. It cannot collide with anything: a row's chevron sits
//     at column indent+1 and never at 0, and the name starts later still.
//   - SHIFT + a press anywhere on a row extends the range from the
//     anchor. It is a BONUS LAYER, in the metakeys.go sense: several
//     terminals keep shift-click for their own text selection, so the
//     gesture is allowed to never arrive — the gutter click, Space, and
//     the picker's "Mark all visible" row all reach the same set without
//     it. Which is also why an unreported shift degrades to a plain
//     toggle rather than to nothing.
func (a *App) treeMarkPress(x, y int, shift bool) bool {
	sx, sy, sw, _ := a.sidebarRect()
	if sw <= 0 {
		return false
	}
	n, ok := a.tree.HitTest(x-sx, y-sy)
	if !ok || n == a.tree.Root {
		return false
	}
	gutter := x-sx == 0
	if !gutter && !shift {
		return false
	}
	// A mark gesture is still a focus gesture and still moves the
	// cursor — the same rule sidebarClick follows, so a
	// tick-then-arrow-keys mix behaves like one continuous gesture.
	a.treeFocus = true
	a.tree.Selected = n
	if shift {
		a.tree.MarkRange(n)
	} else {
		a.tree.ToggleMark(n)
	}
	a.flashTreeMarks()
	return true
}

// flashTreeMarks reports the set's size after a change. The count is
// also drawn permanently on the EXPLORER header, so this exists for the
// other half of the message: naming the surface that spends the marks.
// A one-cell tick is close to invisible as an affordance, and a feature
// nobody can find the verb for is a feature that isn't there — the same
// argument lockTreeAutoFit's flash makes for naming its ≡ row.
func (a *App) flashTreeMarks() {
	n := a.tree.MarkCount()
	if n == 0 {
		a.flash("Selection cleared")
		return
	}
	a.flash(fmt.Sprintf("%d selected — ≡ File ▸ Selected items… (or A in the tree)", n))
}

// treeToggleMarkSelected ticks the keyboard cursor's row — Space while
// the tree has focus, and the context menu's Mark / Unmark row.
func (a *App) treeToggleMarkSelected() {
	sel := a.treeSelection()
	if sel == nil || sel == a.tree.Root {
		a.flash("Nothing to select")
		return
	}
	a.tree.ToggleMark(sel)
	a.flashTreeMarks()
}

// treeMarkAllToggle is the `*` key: mark every visible row, or clear the
// set when anything is already marked. One key for both directions
// because the answer to "I ticked too much" has to be as cheap as the
// mistake, and a separate clear key would be one more thing to remember.
func (a *App) treeMarkAllToggle() {
	if a.tree.MarkCount() > 0 {
		a.tree.ClearMarks()
	} else {
		a.tree.MarkVisible(true)
	}
	a.flashTreeMarks()
}

// ctxToggleMark is the tree context menu's Select / Unselect row. It is
// the feature's discovery surface: the tick lives in one borrowed cell at
// the row's left edge, so without a named row a mouse user has nothing to
// tell them the gutter is clickable at all.
//
// It marks the node that was RIGHT-CLICKED, not the keyboard cursor's
// row — the popup was opened on that node, and openTreeContext has
// already made it the active folder, so the row the user is pointing at
// is the only thing this can honestly mean.
func ctxToggleMark(a *App, n *filetree.Node) {
	a.tree.Selected = n
	a.tree.ToggleMark(n)
	a.flashTreeMarks()
}

// ctxMarkActions opens the multi-selection's verb list from the tree's
// context menu — the same picker the ≡ File row opens,
// at the point where the user is already pointing at the set.
func ctxMarkActions(a *App, _ *filetree.Node) {
	a.openTreeMarkActions()
}

// -----------------------------------------------------------------------------
// Targets
// -----------------------------------------------------------------------------

// treeMarkTargets returns the nodes an action will act on: every marked
// row, or — when nothing is marked — the keyboard cursor's row alone.
//
// The fallback is gitPanelTargets' rule and exists for its reason: it
// keeps the Actions picker useful on the very first open, so the feature
// does not demand a tick before it can do anything. Marked rows come
// back in tree order (filetree.MarkedNodes), so confirm bodies, flashes
// and archive entries all read top-down.
func (a *App) treeMarkTargets() []*filetree.Node {
	if nodes := a.tree.MarkedNodes(); len(nodes) > 0 {
		return nodes
	}
	if sel := a.treeSelection(); sel != nil && sel != a.tree.Root {
		return []*filetree.Node{sel}
	}
	return nil
}

// treeMarkPaths projects a target set onto absolute paths — the form
// every backend verb here takes.
func treeMarkPaths(nodes []*filetree.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Path)
	}
	return out
}

// treeMarkFiles returns just the file nodes in a set. Verbs that only
// mean something for files (Open) filter through it rather than refusing
// a mixed set: a user who ticked a folder and three files and pressed
// Open meant the three files.
func treeMarkFiles(nodes []*filetree.Node) []*filetree.Node {
	var out []*filetree.Node
	for _, n := range nodes {
		if !n.IsDir {
			out = append(out, n)
		}
	}
	return out
}

// treeMarkLabel names a target set for a picker row, a confirm body or a
// flash: the basename for one item, a count otherwise. One helper so
// "Delete main.go" and "Delete 4 items" cannot drift into two phrasings.
func treeMarkLabel(nodes []*filetree.Node) string {
	if len(nodes) == 1 {
		return filepath.Base(nodes[0].Path)
	}
	return itoa(len(nodes)) + " items"
}

// -----------------------------------------------------------------------------
// The picker
// -----------------------------------------------------------------------------

// openTreeMarkActions gathers the current targets and opens the verb
// list. Shared by the ≡ File row and the tree's context menu, so both
// offer exactly the same verbs against exactly the same selection.
func (a *App) openTreeMarkActions() {
	targets := a.treeMarkTargets()
	if len(targets) == 0 {
		// The refusal is checked against the TARGETS rather than the row
		// count, because the selection helpers at the bottom of the list
		// are always offered — a picker holding nothing but "Select all
		// visible rows" is a worse answer to "File tree actions…" than a
		// flash that names the gesture which builds a set. A dimmed menu
		// row could not say that (the menuCopilotAuth rule).
		a.flash("Nothing selected — click the left edge of a tree row, or press Space in the tree")
		return
	}
	a.openPicker(a.treeMarkActionsTitle(), a.treeMarkActionItems(targets))
}

// treeMarkActionsTitle says what the picker is about to act on, because
// the set is the one thing about this feature a user can get wrong: it
// outlives scrolling and folding, so "act on 6 items" is information the
// title has to carry before a row is picked.
func (a *App) treeMarkActionsTitle() string {
	if n := a.tree.MarkCount(); n > 0 {
		return fmt.Sprintf("Selected items (%d)", n)
	}
	return "File tree actions"
}

// menuTreeMarkActions is the ≡ File / command-palette entry point — the
// path that survives a terminal which swallows right-click, per the
// project's rule that every file action lives in the main menu first.
func (a *App) menuTreeMarkActions() {
	a.closeMenu()
	a.openTreeMarkActions()
}

// treeMarkActionsLabel is the ≡ row's dynamic label. It carries the
// count so the menu itself reports a selection the user may have left
// behind minutes ago — the row is the honest place for that, since the
// EXPLORER header's count is invisible while the sidebar is hidden.
func (a *App) treeMarkActionsLabel() string {
	if n := a.tree.MarkCount(); n > 0 {
		return fmt.Sprintf("Selected items (%d)…", n)
	}
	return "File tree actions…"
}

// treeMarkActionItems builds the picker rows for a target set.
//
// Order is deliberate: the read-only verbs first (Open, the two path
// copies), then the ones that create something (Copy for a later paste,
// Zip), then the destructive one, then the selection helpers that manage
// the set itself. Git's staging rows sit with the creators — they change
// the index, not the work tree.
//
// A row is offered only when it would do something, the palette rule:
// "Open 0 files" teaches the user that Enter sometimes does nothing.
func (a *App) treeMarkActionItems(targets []*filetree.Node) []paletteItem {
	var items []paletteItem
	add := func(label string, run func(*App)) {
		items = append(items, paletteItem{label: label, run: run})
	}

	if len(targets) > 0 {
		what := treeMarkLabel(targets)
		paths := treeMarkPaths(targets)

		if files := treeMarkFiles(targets); len(files) > 0 {
			add("Open "+treeMarkLabel(files), func(app *App) {
				app.treeMarkOpen(files)
			})
		}
		add("Copy relative path of "+what, func(app *App) {
			app.treeMarkCopyPaths(targets, false)
		})
		add("Copy absolute path of "+what, func(app *App) {
			app.treeMarkCopyPaths(targets, true)
		})
		// Arming the file clipboard, so the existing Paste surfaces
		// (≡ File, the tree's right-click, Cmd+V) can land the whole set
		// somewhere else. Nothing here needed a paste verb of its own —
		// copypaste.go grew a set-shaped clipboard instead, which is why
		// the destination question stays where it already was.
		add("Copy "+what+" for paste", func(app *App) {
			app.copyPathsToFileClip(paths)
		})
		add("Zip "+what+"…", func(app *App) {
			app.startZipSet(paths)
		})
		// Staging from the tree, only in a repo. Discard is deliberately
		// NOT here: reverting a file's contents is a loss the git panel
		// shows you the diff of first, and offering it from a surface
		// that cannot render what would be lost is the one git verb this
		// picker should not carry.
		if a.gitIsRepo {
			add("Stage "+what, func(app *App) {
				app.runGitCmd("Stage "+what, append([]string{"add", "--"}, paths...)...)
			})
			add("Unstage "+what, func(app *App) {
				app.runGitCmd("Unstage "+what, append([]string{"reset", "-q", "--"}, paths...)...)
			})
		}
		add("Delete "+what+"…", func(app *App) {
			app.treeMarkDelete(targets)
		})
	}

	// Selection management. "Mark all visible" is scoped to the visible
	// rows for filetree.MarkVisible's reason — a set the user cannot see
	// is a set they cannot check before deleting it.
	if a.tree.MarkCount() > 0 {
		add("Clear selection ("+itoa(a.tree.MarkCount())+")", func(app *App) {
			app.tree.ClearMarks()
			app.flash("Selection cleared")
		})
	}
	add("Select all visible rows", func(app *App) {
		app.tree.MarkVisible(true)
		app.flashTreeMarks()
	})
	if dir := a.treeMarkFolderTarget(); dir != nil {
		add("Select contents of "+filepath.Base(dir.Path)+"/", func(app *App) {
			app.tree.MarkChildren(dir, true)
			app.flashTreeMarks()
		})
	}
	return items
}

// treeMarkFolderTarget resolves which folder "select contents of…"
// means: the cursor's row when it is a directory, else the directory
// containing it. Nil when the tree has no cursor at all — the row is
// omitted rather than guessing the root, which would offer to tick the
// whole project from a picker opened by accident.
func (a *App) treeMarkFolderTarget() *filetree.Node {
	sel := a.treeSelection()
	if sel == nil {
		return nil
	}
	if sel.IsDir {
		if !sel.Expanded {
			// An unexpanded folder has no rows to tick, and marking
			// children the user cannot see is the invisible-set problem
			// MarkVisible avoids. Expanding it here would be a surprise
			// side effect of reading a menu, so the row just stays out.
			return nil
		}
		return sel
	}
	return a.tree.ParentOf(sel)
}

// -----------------------------------------------------------------------------
// The verbs
// -----------------------------------------------------------------------------

// treeMarkOpen opens every file in the set as a tab, leaving the last
// one active — the order a set of related files is usually read in.
//
// openFile owns every refusal (too big, binary, unreadable) and flashes
// its own reason, so a set holding one unopenable file still opens the
// rest. The count reported is what actually landed, counted from the tab
// list rather than from the loop, because "opened 6 files" when two were
// refused is precisely the kind of quiet miscount that makes a user
// trust the wrong thing.
func (a *App) treeMarkOpen(files []*filetree.Node) {
	before := len(a.tabs)
	for _, n := range files {
		a.setActiveFolder(filepath.Dir(n.Path))
		a.openFile(n.Path)
	}
	// Focus follows the files into the editor, the same commitment
	// opening one file from the tree makes (treenav.go's Enter).
	a.treeFocus = false
	if opened := len(a.tabs) - before; opened > 1 {
		a.flash(fmt.Sprintf("Opened %d files", opened))
	}
}

// treeMarkCopyPaths puts the set's paths on the system clipboard, one
// per line — the shape every shell, editor and chat prompt accepts, and
// the reason this verb exists at all for a multi-selection.
//
// A single target routes to copyPathToSystemClipboard so the flash reads
// exactly as the existing "Copy rel path" row's does; anything more
// reports a count, because echoing six paths into a one-line flash would
// truncate the answer where it matters.
func (a *App) treeMarkCopyPaths(nodes []*filetree.Node, absolute bool) {
	if len(nodes) == 0 {
		return
	}
	render := a.relativePathFor
	label := "relative path"
	if absolute {
		render = absolutePathFor
		label = "absolute path"
	}
	if len(nodes) == 1 {
		a.copyPathToSystemClipboard(render(nodes[0].Path), label)
		return
	}
	lines := treeMarkPathLines(nodes, render)
	if err := clipboard.CopyToSystem(strings.Join(lines, "\n")); err != nil {
		a.flash(fmt.Sprintf("Copy failed: %v", err))
		return
	}
	a.flash(fmt.Sprintf("Copied %d %ss", len(lines), label))
}

// treeMarkPathLines renders a target set one path per line, through the
// caller's relative/absolute renderer. Pulled out as a pure function so
// the shape of what lands on the clipboard is testable without a
// /dev/tty — which is where clipboard.CopyToSystem writes, and which a
// test host does not necessarily have.
func treeMarkPathLines(nodes []*filetree.Node, render func(string) string) []string {
	lines := make([]string, 0, len(nodes))
	for _, n := range nodes {
		lines = append(lines, render(n.Path))
	}
	return lines
}

// treeMarkDelete confirms, then deletes the whole set as one gesture.
//
// The confirmation LISTS the names rather than only counting them, which
// is the one place this feature differs from the single-file Delete
// dialog it grew out of. A mark survives scrolling and folding, so the
// set can legitimately contain rows that are nowhere on screen — and a
// count alone would ask the user to approve a deletion they cannot see
// the contents of. Folders are shown with a trailing '/' and the body
// says the delete is recursive, because that is the same warning the
// single-folder confirm gives and the set must not lose it.
func (a *App) treeMarkDelete(nodes []*filetree.Node) {
	if len(nodes) == 0 {
		return
	}
	paths := treeMarkPaths(nodes)
	if len(nodes) == 1 {
		// One target is the existing single-file dialog, verbatim — the
		// verb reached from a tick must not look different from the one
		// reached by right-clicking the same row.
		ctxDelete(a, nodes[0])
		return
	}

	hasDir := false
	lines := []string{fmt.Sprintf("Delete these %d items?", len(nodes)), ""}
	for i, n := range nodes {
		if n.IsDir {
			hasDir = true
		}
		if i == treeMarkConfirmList && len(nodes) > treeMarkConfirmList+1 {
			lines = append(lines, fmt.Sprintf("…and %d more", len(nodes)-i))
			break
		}
		// No indent: the confirm drawer CENTERS every line it is handed,
		// so a leading gutter would just shift the name off-center.
		name := a.relativePathFor(n.Path)
		if n.IsDir {
			name += "/"
		}
		lines = append(lines, name)
	}
	lines = append(lines, "")
	if hasDir {
		lines = append(lines, "Folders are deleted with everything inside them.")
	}
	lines = append(lines, "This cannot be undone.")

	a.openConfirmLines("Delete "+itoa(len(nodes))+" items", lines, func(app *App) {
		app.doDeletePaths(paths)
		// The marks are dropped rather than left to Refresh's pruning:
		// a path that failed to delete would otherwise stay ticked and
		// silently ride along into the next action.
		app.tree.ClearMarks()
	})
}
