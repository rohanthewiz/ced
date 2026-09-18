// =============================================================================
// File: internal/app/treefilter.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-18
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// treefilter.go is type-to-find in the focused file tree: typed letters
// build a pattern, every visible row in the current folder (and the
// folders expanded below it) whose name CONTAINS that pattern — anywhere,
// but kept together, not a fuzzy subsequence — has the match lit,
// and the cursor lands on the first one — scrolling the tree if that row
// is off screen. The tree half (matching, painting) lives in
// filetree/filter.go; this file owns the keys and the lifecycle.
//
// It replaced a single-rune typeahead that jumped to the next row
// starting with one letter and cycled on repeats. That design refused a
// multi-rune buffer because "a timeout needs a visible state"; the
// highlight IS the visible state, so the buffer needs no timeout at all.
// It lives until something clears it:
//
//	Backspace to empty · Esc (a side effect, like the ghost text) ·
//	the tree losing focus (opening a file, clicking elsewhere)
//
// Repeating a letter now EXTENDS the pattern, so the old cycle moved to
// Tab / Shift-Tab, which walk the matches forward and back from wherever
// the cursor is. Arrows still move the cursor freely without clearing,
// so a user can step off a match and back.
//
// No letter is a command in the focused tree (treenav.go says why), so
// any letter starts a pattern. Space and * are the mark keys only while
// no pattern is running; once one is, they extend it like any rune.

package app

import (
	"fmt"
	"path/filepath"

	"github.com/rohanthewiz/ced/internal/filetree"
)

// treeFilterScope picks the folder a new filter searches: the tree's
// active folder (the accent-lit one — set by clicking a folder, pressing
// Enter on one, or opening a file inside it) when it is showing its
// contents, else the whole project. A collapsed or folded-away active
// folder has no visible descendants, and a search that could only ever
// answer "no matches" would read as the feature being broken.
func (a *App) treeFilterScope() string {
	root := a.tree.Root.Path
	af := a.tree.ActiveFolder
	if af == "" || af == root {
		return root
	}
	for _, n := range a.tree.VisibleNodes() {
		if n.Path == af {
			if n.IsDir && n.Expanded {
				return af
			}
			break
		}
	}
	return root
}

// treeFilterType appends r to the pattern — capturing the scope on the
// first rune so it stays fixed while the user types, even as Enter on a
// matched folder moves the active folder — and jumps to the first match.
func (a *App) treeFilterType(r rune) {
	scope := a.tree.FilterScope
	if a.tree.Filter == "" {
		scope = a.treeFilterScope()
	}
	a.tree.SetFilter(a.tree.Filter+string(r), scope)
	a.treeFilterFirst()
}

// treeFilterBackspace trims the pattern by one rune, re-jumping to the
// (possibly wider) first match, and clears the filter when it empties.
// Reports whether there was a pattern to trim.
func (a *App) treeFilterBackspace() bool {
	f := []rune(a.tree.Filter)
	if len(f) == 0 {
		return false
	}
	if len(f) == 1 {
		a.tree.ClearFilter()
		return true
	}
	a.tree.SetFilter(string(f[:len(f)-1]), a.tree.FilterScope)
	a.treeFilterFirst()
	return true
}

// treeFilterFirst moves the cursor to the first match in display order
// and reports the count. The caller's ensureTreeSelectionVisible does the
// scrolling. On a miss the cursor stays put: jumping somewhere unrelated
// would lose the user's place for a typo.
func (a *App) treeFilterFirst() {
	matches := a.tree.FilterMatches()
	if len(matches) == 0 {
		a.flash(fmt.Sprintf("No names in %s contain %q", a.treeFilterScopeLabel(), a.tree.Filter))
		return
	}
	a.tree.Selected = matches[0]
	a.flashTreeFilterCount(1, len(matches))
}

// treeFilterStep walks the cursor delta matches from its current row,
// wrapping at both ends — the Tab / Shift-Tab cycle. A cursor that is
// not on a match steps to the nearest one in the direction of travel.
// Reports whether a filter was armed (so Tab stays inert otherwise).
func (a *App) treeFilterStep(delta int) bool {
	if a.tree.Filter == "" {
		return false
	}
	matches := a.tree.FilterMatches()
	if len(matches) == 0 {
		return true
	}
	rows := a.tree.VisibleNodes()
	pos := map[*filetree.Node]int{}
	for i, n := range rows {
		pos[n] = i
	}
	cur := a.tree.SelectedIndex(rows)
	next := -1
	if delta > 0 {
		for i, m := range matches {
			if pos[m] > cur {
				next = i
				break
			}
		}
		if next < 0 {
			next = 0
		}
	} else {
		for i := len(matches) - 1; i >= 0; i-- {
			if pos[matches[i]] < cur {
				next = i
				break
			}
		}
		if next < 0 {
			next = len(matches) - 1
		}
	}
	a.tree.Selected = matches[next]
	a.flashTreeFilterCount(next+1, len(matches))
	return true
}

// flashTreeFilterCount names the pattern, where the cursor sits among the
// matches, and the keys that act on them — the filter's only discovery
// surface for Tab and Esc.
func (a *App) flashTreeFilterCount(at, total int) {
	a.flash(fmt.Sprintf("Find %q in %s: %d/%d  ·  tab next  ·  esc clears",
		a.tree.Filter, a.treeFilterScopeLabel(), at, total))
}

// treeFilterScopeLabel is the scope as the user thinks of it: the
// project's name for the root, a project-relative path otherwise.
func (a *App) treeFilterScopeLabel() string {
	scope := a.tree.FilterScope
	if scope == "" || scope == a.tree.Root.Path {
		return a.tree.Root.Name
	}
	if rel, err := filepath.Rel(a.tree.Root.Path, scope); err == nil {
		return rel + "/"
	}
	return scope
}

// clearTreeFilter drops any armed filter. Safe with no tree — it is
// called from the Esc side-effect block, which runs for every Esc.
func (a *App) clearTreeFilter() {
	if a.tree != nil {
		a.tree.ClearFilter()
	}
}
