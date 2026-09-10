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

package app

import (
	"fmt"
	"os"
	"path/filepath"
)

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
		a.sidebarShown = true
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
