// =============================================================================
// File: internal/app/favorites.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// The editor half of named favorite locations (internal/favorites owns
// the map and the resolution; main.go owns the `ced fav` command line).
// What lands here is one verb: put a path the CLI resolved in front of
// the user.
//
// The gesture is REVEAL, not re-root, and that distinction is the whole
// reason the feature exists. `ced ai_docs/plans` already re-roots — and
// pays for it by throwing away the project: git status, gopls's rootUri,
// the finder's index and the plugin working directory are all derived
// from rootDir, so a root of ai_docs/plans is a workspace where none of
// them describe the code. A favorite keeps the project and moves the
// TREE, which is where "go to a location" actually happens in an editor
// shaped like this one.
//
// Three decisions inside RevealPath are worth the ink:
//
//   - A FOLDER IS EXPANDED, ITS ANCESTORS MERELY OPENED. filetree.Reveal
//     deliberately leaves the target as it found it (a scroll must not
//     spring open a folder somebody collapsed); arriving here BY NAME is
//     the opposite case — the user asked for this folder, so showing its
//     contents is the answer rather than a side effect.
//
//   - A FILE OPENS. A favorite may perfectly well name a document
//     (`notes` → `ai_docs/NOTES.md`), and for a file "go there" means a
//     tab, not a highlighted row. It is still revealed in the tree, so
//     the sidebar agrees with the editor about where you are.
//
//   - THE TREE TAKES THE KEYBOARD for a folder, and does not for a file.
//     The selection highlight only renders while the tree is Focused, so
//     a reveal that skipped this would leave the cursor somewhere
//     invisible — the arrow keys would work and nothing on screen would
//     say so. Opening a file is the opposite: the user's next keystroke
//     belongs in the buffer.
//
// The ≡ Navigation row (menuGoToFavorite) is the mid-session half of the
// same verb, and it differs from the CLI in exactly one way that matters:
// it RESOLVES STRICTLY IN THE OPEN ROOT rather than walking up. `ced fav`
// walks because it is still choosing which project to open; a running
// editor already has one, and a walk here could resolve a favorite in the
// PARENT of the workspace — a path outside the file tree, which the tree
// would then refuse to reveal, having been handed somewhere the user
// cannot see. favorites.ResolveIn is that half of the resolver.

package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rohanthewiz/ced/internal/favorites"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// favoritesPathFn resolves favorites.json. A package var for the reason
// every other config seam here is one: newTestApp points it at a
// throwaway directory, so no test run can read the developer's real
// favorites — or reveal one of their folders in a simulated editor.
var favoritesPathFn = userconfig.FavoritesPath

// RevealPath shows path in the file tree: the sidebar comes up if it was
// hidden, every directory on the way down is loaded and expanded, and
// the tree's cursor lands on the target. A directory is expanded and
// takes focus; a file is opened in a tab as well.
//
// Exported because main.go calls it once, after app.New and before Run,
// on the path `ced fav <name>` resolved — the same seam a.OpenFile
// occupies for `ced <file>`. Failures flash rather than refuse: the
// editor is already up and rooted at the right project, so a favorite
// pointing at a folder that has since been deleted costs the jump, not
// the session (the silent-degradation contract every integration here
// follows, minus the silence — the user typed a name and deserves to
// know it missed).
func (a *App) RevealPath(path string) {
	if path == "" {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		a.flash(fmt.Sprintf("Cannot reveal %s: %v", path, err))
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		a.flash(fmt.Sprintf("Cannot reveal %s: %v", abs, err))
		return
	}

	// A file gets a tab first: openFile is what sets the active folder,
	// records nav history and wires every per-tab subsystem, and doing
	// it before the tree walk means the row we select is the row the
	// editor already considers active.
	if !info.IsDir() {
		a.openFile(abs)
	}

	if !a.sidebarShown {
		a.showTool(toolProject)
	}
	node, ok := a.tree.Reveal(abs)
	if !ok {
		// The tree refuses paths outside the root and anything under a
		// hidden segment (.git, node_modules). Say which, rather than
		// leaving a nothing-happened.
		a.flash(fmt.Sprintf("%s is not in this project's tree", a.relativePathFor(abs)))
		return
	}
	if node.IsDir {
		// Arriving by name means "show me what is in here" — see the
		// header comment for why filetree.Reveal doesn't do this itself.
		if !node.Expanded {
			a.tree.Toggle(node)
		}
		a.focusTree()
	}
	a.ensureTreeSelectionVisible()
	a.flash(fmt.Sprintf("Revealed %s", a.relativePathFor(abs)))
}

// menuGoToFavorite opens the project's favorites as a fuzzy picker and
// reveals whichever one is chosen — the ≡ Navigation row, and the
// mid-session twin of `ced fav <name>`.
//
// It sits in Navigation rather than Search because it belongs to that
// group's question exactly: Go back and Go forward walk the trail you
// made, and this jumps to the places you named in advance. The pairing
// is a browser's — history beside bookmarks.
//
// Two rules the surface follows:
//
//   - THE ROW IS ALWAYS CLICKABLE, and says why when it can't help. A
//     dimmed row on a machine with no favorites.json is a dead end that
//     cannot explain itself; the flash names the verb that creates one
//     (the "Recent chats"/MCP rule). Keeping it enabled also keeps
//     menuLayout free of a per-frame file read — predicates run on every
//     frame the menu is open.
//
//   - ONLY WHAT RESOLVES IS OFFERED. The CLI's `fav list` is a REPORT,
//     so it shows a global default this project doesn't follow, marked
//     "missing here". A picker is a list of VERBS, and the palette has no
//     disabled state to borrow: a row answering Enter with "that isn't
//     here" is worse than one never offered (the code-actions rule). What
//     was dropped is still accounted for — in the flash, when everything
//     was.
func (a *App) menuGoToFavorite() {
	a.closeMenu()

	set, err := favorites.Load(favoritesPathFn())
	if err != nil {
		// A malformed file is reported rather than read as "no
		// favorites": the user wrote it, and silence would leave them
		// believing the names were bound.
		a.flash(fmt.Sprintf("Favorites: %v", err))
		return
	}

	entries := set.List(a.rootDir)
	items := make([]paletteItem, 0, len(entries))
	for _, e := range entries {
		got, rerr := set.ResolveIn(a.rootDir, e.Name)
		if rerr != nil {
			continue
		}
		abs := got.Abs
		// Name first: the fuzzy scorer rewards early matches, and the
		// name is what the user is typing. The path trails as context,
		// the way the symbol picker puts its kind word last.
		items = append(items, paletteItem{
			label: e.Name + "  " + e.Path,
			run:   func(a *App) { a.RevealPath(abs) },
		})
	}

	if len(items) == 0 {
		a.flash(favoritesEmptyReason(len(entries)))
		return
	}
	a.openPicker("Go to favorite", items)
}

// favoritesEmptyReason distinguishes the two ways the picker can come up
// empty, because they have different fixes — the same split the CLI
// makes between an unbound name and a bound-but-missing one. "You have
// none" wants the verb that creates one; "none of yours are here" wants
// to say that this project doesn't follow the convention, so the user
// doesn't go hunting for a file they know they wrote.
func favoritesEmptyReason(bound int) string {
	if bound == 0 {
		return "No favorites yet — add one with:  ced fav add plans ai_docs/plans"
	}
	return fmt.Sprintf("None of your %d favorites exist in this project", bound)
}
