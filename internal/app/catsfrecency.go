// =============================================================================
// File: internal/app/catsfrecency.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Frecency navigation: the host knows where you work, so the editor's
// "Recent folders…" picker offers those places too.
//
// cats keeps a cdx-ranked directory history — every `cd` in every pane of
// every session, scored by how often and how recently you go there — and
// hands it over on path.list with recents set. ced's own recent list, by
// contrast, only knows the folders THIS editor has been opened in, which on
// a fresh install is one folder. Merging the two means a ced started five
// minutes ago already knows your projects.
//
// THE TWO LISTS ARE NOT THE SAME KIND OF THING, so they are not interleaved:
//
//   - ced's own recents are places you EDITED. They carry saved tab lists,
//     they were recorded by this program, and they are what the picker has
//     always meant. They stay on top, in their existing order.
//   - the host's recents are places you have BEEN. Wider, noisier, ranked by
//     a different algorithm — and marked "· cats" so a row that turns out to
//     be your downloads directory is explained rather than surprising.
//
// A path in both lists appears once, in ced's half: being the folder you
// edited is the more specific fact.
//
// WHY IT IS CACHED. The picker opens on the main loop; path.list is a socket
// round trip. Blocking a keystroke on the host would be the one thing this
// integration promises never to do, so the list is fetched on a goroutine —
// primed when Tier 1 comes up, refreshed each time the picker opens (for the
// NEXT open). The cost of that policy is one stale list in the rare case
// where a folder became interesting in the last few seconds; the cost of the
// alternative is a UI that pauses when a socket is slow.

package app

import (
	"os"
	"path/filepath"
	"time"

	"github.com/rohanthewiz/ced/internal/cats"
	"github.com/rohanthewiz/ced/internal/session"
)

// catsRecentsMax bounds how many host rows can join the picker. The host
// returned 34 on the machine this was written on and has no upper bound of
// its own; a fuzzy picker with two hundred rows of shell history in it is a
// worse instrument than one with a dozen good ones, and the ranking means
// the ones that matter are at the front.
const catsRecentsMax = 24

// catsPollRecents refreshes the cached frecency list off the main loop.
// Declines below Tier 1, so call sites need no guard of their own.
func (a *App) catsPollRecents() {
	if !a.catsTier1() || a.screen == nil {
		return
	}
	client, scr := a.cats.client, a.screen
	// Ask about OUR pane's directory rather than the focused one: the
	// recents are session-wide either way, but the pane argument also
	// decides whose cwd the reply describes, and being asked about
	// somebody else's pane is a habit worth not forming.
	var self *uint32
	if a.cats.selfOK {
		id := a.cats.self
		self = &id
	}
	go func() {
		res, err := client.PathList("", self, true)
		if err != nil {
			return // the picker falls back to ced's own list
		}
		_ = scr.PostEvent(&catsEvent{
			when: time.Now(), kind: catsKindRecents, recents: res.Recents,
		})
	}()
}

// catsRecentFolders returns the host's recent directories that ced's own
// list does not already carry, normalized, pruned to the ones that still
// exist, and capped.
//
// have is the set of paths already claimed by the caller's own rows — the
// merge's dedup key, passed in rather than recomputed so the two halves
// agree on what "the same folder" means (session.Normalize, which is what
// ced's own store keys on).
func (a *App) catsRecentFolders(have map[string]bool) []string {
	if len(a.cats.recents) == 0 {
		return nil
	}
	out := make([]string, 0, catsRecentsMax)
	seen := make(map[string]bool, len(a.cats.recents))
	for _, raw := range a.cats.recents {
		if len(out) >= catsRecentsMax {
			break
		}
		p := session.Normalize(raw)
		if p == "" || have[p] || seen[p] {
			continue
		}
		seen[p] = true
		// Pruned rather than dimmed, the same rule the editor's own recent
		// list follows: a list of places you cannot go is worse than a
		// shorter list. The host's history is wider than ced's, so this
		// prunes more often — old checkouts, deleted worktrees.
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			continue
		}
		out = append(out, p)
	}
	return out
}

// catsRecentLabel renders one host row. The suffix marks where the row came
// from — it is a fuzzy picker, so "cats" also becomes a search term that
// narrows the list to the host's half for free, exactly as "(custom)" does
// in the theme picker.
func catsRecentLabel(path string) string { return displayPath(path) + "  · cats" }

// catsOpenProjectInTab spawns an INDEPENDENT ced on another project, in a
// new cats tab — the "open that somewhere else" half of the frecency story,
// where the picker's own rows switch this editor's root.
//
// tab.create takes an argv, so unlike the split (catssplit.go) there is no
// shell in the middle and nothing to quote: the pane runs the editor
// directly, and its exit closes the pane.
func (a *App) catsOpenProjectInTab(dir string) {
	if !a.catsTier1() {
		a.flash("New cats tab needs cats — ced is running in a plain terminal")
		return
	}
	exe, err := catsExecutable()
	if err != nil {
		a.flash("New tab: cannot find this ced binary: " + err.Error())
		return
	}
	client, scr := a.cats.client, a.screen
	params := cats.TabCreateParams{
		// The tab wears the project's name, because a strip of tabs all
		// labeled "ced" is a strip of tabs with no information in it.
		Title:   filepath.Base(dir),
		Cwd:     dir,
		Command: []string{exe, "--root", dir},
	}
	shown := displayPath(dir)
	go func() {
		if _, err := client.TabCreate(params); err != nil {
			catsPostNotice(scr, "New tab failed: "+err.Error())
			return
		}
		catsPostNotice(scr, "Opened "+shown+" in a new cats tab")
	}()
}

// menuCatsOpenProject offers every known project — ced's own recents and the
// host's frecency list — and opens the chosen one as a new cats tab.
//
// A separate row from "Recent folders…" rather than a modifier on it,
// because the two answer different questions: that picker MOVES this editor
// (and takes its unsaved work with it), this one leaves it exactly where it
// is and starts a second one elsewhere. Making that the same gesture with a
// hidden modifier would be how somebody loses their place.
func (a *App) menuCatsOpenProject() {
	a.closeMenu()
	if !a.catsTier1() {
		a.flash("New cats tab needs cats — ced is running in a plain terminal")
		return
	}
	items := make([]paletteItem, 0, catsRecentsMax)
	have := map[string]bool{}
	if a.sessionStore != nil {
		for _, root := range a.sessionStore.Recent() {
			if info, err := os.Stat(root); err != nil || !info.IsDir() {
				continue
			}
			have[root] = true
			root := root // capture per iteration
			items = append(items, paletteItem{
				label: displayPath(root),
				run:   func(app *App) { app.catsOpenProjectInTab(root) },
			})
		}
	}
	for _, dir := range a.catsRecentFolders(have) {
		dir := dir
		items = append(items, paletteItem{
			label: catsRecentLabel(dir),
			run:   func(app *App) { app.catsOpenProjectInTab(dir) },
		})
	}
	a.catsPollRecents() // freshen the cache for next time
	if len(items) == 0 {
		a.flash("No projects known yet — cats learns them as you cd around")
		return
	}
	a.openPicker("Open project in a new cats tab", items)
}

// hasCatsProjects gates that row: Tier 1, and somewhere to go.
func (a *App) hasCatsProjects() bool {
	if !a.catsTier1() {
		return false
	}
	if len(a.cats.recents) > 0 {
		return true
	}
	return a.sessionStore != nil && len(a.sessionStore.Recent()) > 0
}
