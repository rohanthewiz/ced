// =============================================================================
// File: internal/app/favmanage.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Managing favorites from inside the editor — ≡ File → "Manage
// favorites…", and the tree's "Add to favorites" row.
//
// favorites.go is for USING a favorite; this file is for maintaining the
// file behind them, so binding, renaming, re-pointing and removing a name
// never require dropping to a shell to edit JSON.
//
// House rules:
//
//   - **THE PROJECT'S LIST IS THE MENU; GLOBAL IS A DRILL-IN.** The
//     asymmetry is the whole layout. The global map is the one you write
//     once and forget — it is shared by every project, so editing it from
//     inside one of them is the rarer act and belongs a gesture deeper.
//     What you maintain day to day is the override list for the repo in
//     front of you, so that is what the row opens on.
//
//   - **IT LISTS WHAT DOESN'T RESOLVE**, unlike the "Go to" picker. That
//     is the report/verb split one floor down: you cannot go to a folder
//     that isn't there, but a broken entry is exactly the one you came
//     here to fix, and a management list that hid it would be a
//     management list you could not use.
//
//   - **EVERY LIST ENDS WITH ITS OWN ADD ROW**, seeded to that list's
//     scope, so where you asked decides what you get instead of a flag
//     you have to remember. The prompt still carries the chip, so the
//     seed is a default and never a trap.
//
//   - **EVERY VERB RETURNS TO THE LIST IT CAME FROM.** Fixing three
//     entries must not be three trips through the ≡ menu, so each prompt
//     carries a `back` and calls it once the write has landed.
//
//   - **THE SCOPE CHIP IS A CLOSURE, NOT AN App FIELD** — the commit
//     prompt's trailer chip exactly (gitcommitmsg.go), and for its
//     reason: the value belongs to one invocation of one prompt, and a
//     field on App would outlive it and be read by the next one.

package app

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/rohanthewiz/ced/internal/favorites"
	"github.com/rohanthewiz/ced/internal/filetree"
)

// favScopeChipWidth reserves the chip's cell budget. A RESERVED width,
// like the commit trailer's: the label changes length as the scope flips
// and the buttons to its right must not slide under the pointer.
var favScopeChipWidth = runeLen("[scope: project]")

// favScopeChipLabel is the one spelling of the chip. "global" / "project"
// rather than "all projects" / "this project" because the same two words
// name the scopes in `ced fav`'s output, in the file's own JSON keys and
// in the removal flash — four surfaces, one vocabulary.
func favScopeChipLabel(sc favorites.Scope) string {
	return "[scope: " + favScopeName(sc) + "]"
}

// favScopeName is that vocabulary's single definition.
func favScopeName(sc favorites.Scope) string {
	if sc == favorites.ScopeProject {
		return "project"
	}
	return "global"
}

// favScopeRoot maps a scope to the root argument the favorites package
// takes: "" means the global map, anything else names a project. One
// converter, because getting it backwards writes to the wrong half of
// the file and the mistake is invisible until somebody opens another
// project.
func (a *App) favScopeRoot(sc favorites.Scope) string {
	if sc == favorites.ScopeProject {
		return a.rootDir
	}
	return ""
}

// loadFavoriteSet reads favorites.json, flashing and reporting failure
// rather than returning an empty set. A malformed file must not read as
// "you have no favorites" on a management surface of all places — the
// user is about to write to it, and writing over a file we could not
// parse would destroy whatever it held.
func (a *App) loadFavoriteSet() (*favorites.Set, bool) {
	set, err := favorites.Load(favoritesPathFn())
	if err != nil {
		a.flash(fmt.Sprintf("Favorites: %v", err))
		return nil, false
	}
	return set, true
}

// saveFavoriteSet writes it back, flashing on failure. Every mutation
// below goes through here so an unwritable config directory is reported
// once, in one wording.
func (a *App) saveFavoriteSet(set *favorites.Set) bool {
	if err := set.Save(favoritesPathFn()); err != nil {
		a.flash(fmt.Sprintf("Could not save favorites: %v", err))
		return false
	}
	return true
}

// menuManageFavorites is the ≡ File row.
func (a *App) menuManageFavorites() {
	a.closeMenu()
	a.openManageFavorites()
}

// openManageFavorites is the top level — this project's overrides, under
// a drill-in row for the global set. Split out from the menu row so every
// verb below can return to it.
func (a *App) openManageFavorites() {
	set, ok := a.loadFavoriteSet()
	if !ok {
		return
	}

	items := []paletteItem{{
		label: "Global favorites…  (" + itoa(len(set.Favorites)) + ")",
		run:   func(a *App) { a.openManageGlobalFavorites() },
	}}
	for _, e := range set.List(a.rootDir) {
		if e.Scope == favorites.ScopeProject {
			items = append(items, a.favManageRow(set, e))
		}
	}
	items = append(items, paletteItem{
		label: "Add a favorite…",
		run:   func(a *App) { a.promptAddFavorite("", favorites.ScopeProject, a.openManageFavorites) },
	})
	a.openPicker("Manage favorites — "+filepath.Base(a.rootDir), items)
}

// openManageGlobalFavorites is the drill-in: the same list shape over the
// map that applies everywhere.
func (a *App) openManageGlobalFavorites() {
	set, ok := a.loadFavoriteSet()
	if !ok {
		return
	}
	items := make([]paletteItem, 0, len(set.Favorites)+1)
	for _, e := range set.List("") {
		items = append(items, a.favManageRow(set, e))
	}
	items = append(items, paletteItem{
		label: "Add a global favorite…",
		run:   func(a *App) { a.promptAddFavorite("", favorites.ScopeGlobal, a.openManageGlobalFavorites) },
	})
	a.openPicker("Global favorites", items)
}

// favManageRow builds one entry's row. The row carries the PATH, because
// that is what a user is checking when they come here; the scope is
// implied by which list they are in, so repeating it per row would spend
// width saying what the title already said.
func (a *App) favManageRow(set *favorites.Set, e favorites.Entry) paletteItem {
	label := e.Name + "  " + e.Path
	if _, _, err := favorites.ResolveRel(a.rootDir, e.Path); err != nil && e.Scope == favorites.ScopeProject {
		// Annotated in the PROJECT list only. A global entry that doesn't
		// resolve here is the NORMAL case — that is what a default across
		// many projects means — so marking those would put a warning on
		// nearly every row and teach the user to ignore it.
		label += "  (missing here)"
	}
	back := a.openManageFavorites
	if e.Scope == favorites.ScopeGlobal {
		back = a.openManageGlobalFavorites
	}
	return paletteItem{label: label, run: func(a *App) { a.openFavoriteActions(e, back) }}
}

// openFavoriteActions is the per-entry verb list.
//
// "Go to" heads it and appears only when the entry's OWN path resolves
// here — resolved through favorites.ResolveRel rather than by name,
// because a global entry shadowed by a project override still has a path
// of its own and this row is about the one the user is looking at. The
// edit verbs are always present: a broken entry is precisely what they
// exist to repair.
func (a *App) openFavoriteActions(e favorites.Entry, back func()) {
	set, ok := a.loadFavoriteSet()
	if !ok {
		return
	}
	items := []paletteItem{}
	if abs, _, err := favorites.ResolveRel(a.rootDir, e.Path); err == nil {
		items = append(items, paletteItem{
			label: "Go to  " + e.Path,
			run:   func(a *App) { a.RevealPath(abs) },
		})
	}
	items = append(items,
		paletteItem{
			label: "Rename…",
			run:   func(a *App) { a.promptRenameFavorite(e, back) },
		},
		paletteItem{
			label: "Change path…",
			run:   func(a *App) { a.promptRepointFavorite(e, back) },
		},
	)
	// Overriding a global entry in this project is the two-scope model's
	// whole point, and without a row for it the only way to reach one is
	// the command line. Offered only when there ISN'T already an override
	// — that case is an edit of the project row, not a new entry.
	if e.Scope == favorites.ScopeGlobal {
		if _, sc, found := set.Lookup(a.rootDir, e.Name); !found || sc != favorites.ScopeProject {
			items = append(items, paletteItem{
				label: "Override in this project…",
				run:   func(a *App) { a.promptOverrideFavorite(e, back) },
			})
		}
	}
	items = append(items, paletteItem{
		label: "Remove (" + favScopeName(e.Scope) + ")",
		run:   func(a *App) { a.removeFavorite(e, back) },
	})
	a.openPicker(e.Name+"  ·  "+favScopeName(e.Scope), items)
}

// -----------------------------------------------------------------------------
// The prompts
// -----------------------------------------------------------------------------

// promptAddFavorite binds a new name. rel is the path when the caller
// already knows it (the tree's right-click knows exactly which directory
// was clicked); when it is empty the path is asked for FIRST, because the
// name's default is derived from it — answering in the other order would
// mean typing a name and then discovering what it should have been.
func (a *App) promptAddFavorite(rel string, scope favorites.Scope, back func()) {
	if rel != "" {
		a.promptFavoriteName(rel, scope, back)
		return
	}
	a.openPrompt(
		"Add favorite — path",
		"Relative to "+filepath.Base(a.rootDir)+", e.g. ai_docs/plans",
		a.favPathSeed(),
		func(app *App, v string) { app.promptFavoriteName(v, scope, back) },
	)
}

// favPathSeed pre-fills the path prompt with the active file's folder,
// project-relative. It is the most likely answer by a wide margin —
// you bind a favorite for the place you are already working in — and a
// wrong guess costs one keystroke to clear.
func (a *App) favPathSeed() string {
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" {
		return ""
	}
	rel, err := filepath.Rel(a.rootDir, filepath.Dir(tab.Path))
	if err != nil || rel == "." || rel == ".." {
		return ""
	}
	if clean, cerr := favorites.Clean(rel); cerr == nil {
		return clean
	}
	return ""
}

// promptFavoriteName asks for the name and carries the scope chip.
//
// The chip exists because a favorite's scope is not something the user
// should have to know a flag for, and not something a second menu row
// should cost: it states what OK will do, in the one place they are
// already looking. `alt+s` is its chord, named in the hint — a modal owns
// the keyboard, so the ≡ menu is unreachable from inside a prompt and the
// hint is the chord's only discovery surface (the commit prompt's rule).
func (a *App) promptFavoriteName(rel string, scope favorites.Scope, back func()) {
	clean, err := favorites.Clean(rel)
	if err != nil {
		a.flash(fmt.Sprintf("Favorite path: %v", err))
		return
	}
	// Checked at the ADD, not only at use: binding a name to a folder
	// that isn't there is almost always a typo in the path, and the one
	// moment the user can still see what they typed is now. It is a
	// warning rather than a refusal — a favorite for a directory you are
	// about to create is legitimate, which is why `ced fav add` allows it
	// too.
	missing := ""
	if _, _, rerr := favorites.ResolveRel(a.rootDir, clean); rerr != nil {
		missing = "  (not in this project yet)"
	}

	sc := scope
	extras := []promptExtra{{
		label: func(*App) string { return favScopeChipLabel(sc) },
		width: favScopeChipWidth,
		key:   's',
		run: func(*App) {
			if sc == favorites.ScopeProject {
				sc = favorites.ScopeGlobal
			} else {
				sc = favorites.ScopeProject
			}
		},
	}}
	a.openPromptExtras(
		"Add to favorites",
		"Name for "+clean+missing+" · alt+s scope",
		filepath.Base(clean),
		extras,
		func(app *App, name string) { app.commitFavorite(sc, name, clean, back) },
	)
}

// commitFavorite is the single write path for a new binding, so the
// validation, the save and the report can never differ between the three
// surfaces that reach it (the tree row, and both Add rows).
func (a *App) commitFavorite(scope favorites.Scope, name, rel string, back func()) {
	set, ok := a.loadFavoriteSet()
	if !ok {
		return
	}
	// Named before it is taken: replacing a binding silently is how a
	// user loses one they still wanted, and the two scopes make it easy
	// to overwrite the wrong half without noticing.
	replaced := ""
	if old, sc, found := set.Lookup(a.favScopeRoot(scope), name); found && sc == scope && old != rel {
		replaced = "  (was " + old + ")"
	}
	entry, err := set.Add(a.favScopeRoot(scope), name, rel)
	if err != nil {
		a.flash(fmt.Sprintf("Favorite: %v", err))
		return
	}
	if !a.saveFavoriteSet(set) {
		return
	}
	a.flash(fmt.Sprintf("Favorite %s → %s (%s)%s", entry.Name, entry.Path, favScopeName(scope), replaced))
	if back != nil {
		back()
	}
}

// promptRenameFavorite changes the NAME an entry is filed under, in its
// own scope. Implemented as remove-then-add rather than as a key edit,
// because Add is the only path that validates a name and the two must
// not disagree about what one may look like.
func (a *App) promptRenameFavorite(e favorites.Entry, back func()) {
	a.openPrompt("Rename favorite", "New name for "+e.Path, e.Name, func(app *App, name string) {
		if name == e.Name {
			if back != nil {
				back()
			}
			return
		}
		set, ok := app.loadFavoriteSet()
		if !ok {
			return
		}
		root := app.favScopeRoot(e.Scope)
		if _, err := set.Add(root, name, e.Path); err != nil {
			app.flash(fmt.Sprintf("Favorite: %v", err))
			return
		}
		// Only after the new name is safely in: a failed Add followed by
		// a completed Remove would lose the entry entirely.
		if _, err := set.Remove(root, e.Scope, e.Name); err != nil && !errors.Is(err, favorites.ErrNotFound) {
			app.flash(fmt.Sprintf("Favorite: %v", err))
			return
		}
		if !app.saveFavoriteSet(set) {
			return
		}
		app.flash(fmt.Sprintf("Renamed %s → %s (%s)", e.Name, name, favScopeName(e.Scope)))
		if back != nil {
			back()
		}
	})
}

// promptRepointFavorite changes where an entry points, keeping its name
// and scope — the verb for "we moved the docs folder".
func (a *App) promptRepointFavorite(e favorites.Entry, back func()) {
	a.openPrompt("Change path", e.Name+" — relative to "+filepath.Base(a.rootDir), e.Path,
		func(app *App, rel string) { app.commitFavorite(e.Scope, e.Name, rel, back) })
}

// promptOverrideFavorite creates a project override for a global entry,
// pre-filled with the global path so the common edit is a few keystrokes
// rather than a retype.
func (a *App) promptOverrideFavorite(e favorites.Entry, back func()) {
	a.openPrompt("Override "+e.Name+" here", "Path in "+filepath.Base(a.rootDir)+" (global: "+e.Path+")", e.Path,
		func(app *App, rel string) { app.commitFavorite(favorites.ScopeProject, e.Name, rel, back) })
}

// removeFavorite unbinds an entry from its own scope, and says when the
// removal merely UNCOVERED a global default rather than unbinding the
// name — a user who doesn't expect that reads the silence as the removal
// having failed (the CLI's `fav rm` rule, one floor up).
//
// No confirmation: a favorite is a name, not data, and re-adding one is
// the same two keystrokes that made it. A dialog in front of a reversible
// action trains people to dismiss dialogs.
func (a *App) removeFavorite(e favorites.Entry, back func()) {
	set, ok := a.loadFavoriteSet()
	if !ok {
		return
	}
	if _, err := set.Remove(a.favScopeRoot(e.Scope), e.Scope, e.Name); err != nil {
		a.flash(fmt.Sprintf("No %s favorite named %q", favScopeName(e.Scope), e.Name))
		return
	}
	if !a.saveFavoriteSet(set) {
		return
	}
	msg := fmt.Sprintf("Removed %s (%s)", e.Name, favScopeName(e.Scope))
	if rel, _, found := set.Lookup(a.rootDir, e.Name); found {
		msg += fmt.Sprintf(" — now falls back to %s", rel)
	}
	a.flash(msg)
	if back != nil {
		back()
	}
}

// ctxAddFavorite is the tree's right-click row: bind a name to the
// clicked directory. The path is already known, so this goes straight to
// the name prompt — the click WAS the path answer, and asking again would
// be the editor forgetting what the user just pointed at.
//
// The scope chip starts on GLOBAL, because a favorite earns its name by
// repeating across projects; one chord says otherwise.
func ctxAddFavorite(a *App, n *filetree.Node) {
	a.closeModal()
	rel, err := filepath.Rel(a.rootDir, n.Path)
	if err != nil {
		a.flash("Cannot add a favorite outside the project")
		return
	}
	a.promptFavoriteName(rel, favorites.ScopeGlobal, nil)
}
