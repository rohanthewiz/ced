// =============================================================================
// File: internal/app/tabgroups.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// tabgroups.go is TAB GROUPS: a short named chip in the tab strip with
// its member tabs gathered behind it and underlined, and foldable to the
// chip alone.
//
//	≡ │ api │ server.go × │ routes.go × │ docs +3 │ main.go × │
//	    └─chip┴───────── underlined ─────────────┘ └ collapsed: 3
//	                                                 members hidden
//
// Design choices:
//
//   - **Two kinds, one struct.** A FOLDER group is a rule: every open
//     file under its folder belongs, including files opened later, so a
//     project's "api" or "docs" corner groups itself. An AD-HOC group is
//     a hand-picked set. The folder field tells them apart (empty =
//     ad-hoc), matching session.TabGroup on disk.
//   - **One group per tab, resolved — never stored on the tab.**
//     tabGroupOf answers from the groups: ad-hoc membership first (it is
//     the explicit gesture), then the DEEPEST folder group that has not
//     excluded the tab. A folder group's rule therefore can't fight a
//     hand placement, and nested folder groups split the tree the way
//     the user would expect.
//   - **Membership is keyed by *editor.Tab, not path.** A rename or
//     Save As keeps the tab in its ad-hoc group, and untitled tabs (all
//     sharing "") can join one. Paths are only the disk form
//     (encodeTabGroups / restoreTabGroups). closeTab drops the pointer
//     (tabGroupsForgetTab) like the other per-tab-pointer records.
//   - **Groups are contiguous because a.tabs is REORDERED, not because
//     the strip draws in a different order.** arrangeTabGroups clusters
//     each group at its first member's position, a stable and idempotent
//     pass run before every layout. Everything that walks tabs by index
//     (Esc , / Esc ., the switcher, tabScroll, close-left-of-active)
//     then agrees with what is drawn, without a second "display order"
//     to keep in sync. The active tab is followed by pointer. With no
//     groups the pass returns at once, so ungrouped use is unchanged.
//   - **The chip belongs to the first DRAWN member.** Its width is part
//     of that tab's slot (tabSlotWidth), which keeps tabScroll an index
//     of tabs and lets ensureActiveTabVisible budget for chips without
//     learning about them. When the strip is scrolled into the middle of
//     a group, the first visible member carries the chip so the group
//     stays named.
//   - **Collapse hides members, never the active tab.** A collapsed
//     group draws its chip plus the active tab if it is a member, so
//     switching into a folded group (Esc ., the switcher) never leaves
//     the front file without a tab. Hidden members still get a
//     zero-width rect, so "+N" keeps counting only tabs scrolled off the
//     strip.
//   - **Every chip gesture has a keyboard / ≡ door.** A left click on
//     the chip folds it, and a right click opens the group menu. ≡ File
//     "Tab groups…" reaches the same verbs through pickers, and "Add tab
//     to group…" is the tab menu's row for the active tab. No leader:
//     the flat table is out of letters.
//   - **Names are at most four letters or digits.** The chip competes
//     with file names for the one row every open file shares, and four
//     is enough for a mnemonic ("api", "docs", "wip2"). Names are unique
//     (case-insensitive) so a picker row and a chip always mean one
//     group.

package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/session"
	"github.com/rohanthewiz/ced/internal/theme"
)

// tabGroupNameMax is the longest group name, in runes (see the header).
const tabGroupNameMax = 4

// tabGroup is one live group. See the header for why membership lives
// here rather than on the tab.
type tabGroup struct {
	name string
	// folder is the absolute directory of a folder group; "" for ad-hoc.
	folder string
	// members is an ad-hoc group's membership. Unused by folder groups.
	members map[*editor.Tab]bool
	// exclude lists tabs taken out of a FOLDER group by hand — the rule
	// would otherwise pull them straight back in on the next frame.
	exclude   map[*editor.Tab]bool
	collapsed bool
	// color indexes tabGroupColorKeys; assigned at creation so it stays
	// put when other groups come and go.
	color int
}

// isFolder reports whether g is a folder group.
func (g *tabGroup) isFolder() bool { return g.folder != "" }

// tabGroupColorKeys are the hues groups cycle through. Taken from the
// SYNTAX palette because every theme already makes those six read as
// distinct from each other and from the background — exactly the
// property a group colour needs — and a theme switch restyles them for
// free since they are read at paint time, never cached.
var tabGroupColorKeys = []func(theme.Theme) tcell.Color{
	func(t theme.Theme) tcell.Color { return t.SynFunction },
	func(t theme.Theme) tcell.Color { return t.SynString },
	func(t theme.Theme) tcell.Color { return t.SynKeyword },
	func(t theme.Theme) tcell.Color { return t.SynType },
	func(t theme.Theme) tcell.Color { return t.SynNumber },
	func(t theme.Theme) tcell.Color { return t.SynConstant },
}

// tabGroupColor is g's colour under the current theme.
func (a *App) tabGroupColor(g *tabGroup) tcell.Color {
	n := len(tabGroupColorKeys)
	return tabGroupColorKeys[((g.color%n)+n)%n](a.theme)
}

// tabGroupChipFG picks the chip's text colour: whichever of the editor
// background and the theme's text reads better on the group colour. A
// syntax hue is light on a dark theme and dark on a light one, so a fixed
// choice would be unreadable on half the themes.
func (a *App) tabGroupChipFG(bg tcell.Color) tcell.Color {
	if contrastRatio(a.theme.BG, bg) >= contrastRatio(a.theme.Text, bg) {
		return a.theme.BG
	}
	return a.theme.Text
}

// nextTabGroupColor is the first colour no live group uses, so the first
// six groups are all distinct; after that the cycle repeats.
func (a *App) nextTabGroupColor() int {
	used := make(map[int]bool, len(a.tabGroups))
	for _, g := range a.tabGroups {
		used[g.color] = true
	}
	for c := range tabGroupColorKeys {
		if !used[c] {
			return c
		}
	}
	return len(a.tabGroups) % len(tabGroupColorKeys)
}

// -----------------------------------------------------------------------------
// Membership
// -----------------------------------------------------------------------------

// tabGroupOf is the group t belongs to, or nil: ad-hoc membership first,
// then the deepest folder group that contains t and has not excluded it.
func (a *App) tabGroupOf(t *editor.Tab) *tabGroup {
	if t == nil || len(a.tabGroups) == 0 {
		return nil
	}
	for _, g := range a.tabGroups {
		if !g.isFolder() && g.members[t] {
			return g
		}
	}
	if t.Path == "" {
		return nil
	}
	var best *tabGroup
	for _, g := range a.tabGroups {
		if !g.isFolder() || g.exclude[t] || !pathUnder(t.Path, g.folder) {
			continue
		}
		// Deepest wins: "internal/app" inside "internal" is the more
		// specific claim, and the one the user made second.
		if best == nil || len(g.folder) > len(best.folder) {
			best = g
		}
	}
	return best
}

// pathUnder reports whether p is dir or inside it. A string test rather
// than pathInside's filepath.Rel: it runs for every tab on every layout
// pass, and both sides are ced's own cleaned absolute paths, so the
// separator check is all the boundary rule needs ("/a/bc" is not under
// "/a/b").
func pathUnder(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

// tabGroupMembers lists g's open tabs in strip order.
func (a *App) tabGroupMembers(g *tabGroup) []*editor.Tab {
	var out []*editor.Tab
	for _, t := range a.tabs {
		if a.tabGroupOf(t) == g {
			out = append(out, t)
		}
	}
	return out
}

// arrangeTabGroups reorders a.tabs so each group's members sit together,
// gathered at the position of the group's FIRST member; ungrouped tabs
// keep their places relative to each other. Stable and idempotent, so it
// is safe to run before every layout (see the header for why the slice
// itself is reordered). The active tab is followed by pointer.
func (a *App) arrangeTabGroups() {
	if len(a.tabGroups) == 0 || len(a.tabs) < 2 {
		return
	}
	active := a.activeTabPtr()
	groupOf := make([]*tabGroup, len(a.tabs))
	for i, t := range a.tabs {
		groupOf[i] = a.tabGroupOf(t)
	}
	out := make([]*editor.Tab, 0, len(a.tabs))
	placed := make(map[*tabGroup]bool, len(a.tabGroups))
	for i, t := range a.tabs {
		g := groupOf[i]
		if g == nil {
			out = append(out, t)
			continue
		}
		if placed[g] {
			continue
		}
		placed[g] = true
		for j := i; j < len(a.tabs); j++ {
			if groupOf[j] == g {
				out = append(out, a.tabs[j])
			}
		}
	}
	a.tabs = out
	if active != nil {
		a.activeTab = a.tabIndexOf(active)
	}
}

// tabGroupsForgetTab drops a closing tab from every group, and an ad-hoc
// group with it when that was its last member — an empty ad-hoc group has
// no chip to draw and nothing to manage, the way a browser's tab group
// goes away with its last tab. Folder groups stay: they are rules, and
// the next file opened under the folder joins.
func (a *App) tabGroupsForgetTab(t *editor.Tab) {
	if len(a.tabGroups) == 0 {
		return
	}
	kept := a.tabGroups[:0]
	for _, g := range a.tabGroups {
		delete(g.members, t)
		delete(g.exclude, t)
		if !g.isFolder() && len(g.members) == 0 {
			continue
		}
		kept = append(kept, g)
	}
	a.tabGroups = kept
}

// -----------------------------------------------------------------------------
// Strip geometry (used by tabbar.go)
// -----------------------------------------------------------------------------

// tabChip returns the chip drawn in front of tab i when the strip's
// first drawn tab is first, or ok=false when i carries none. A tab
// carries the chip when it is in a group and is either the first drawn
// tab or the first of its group (see the header).
func (a *App) tabChip(i, first int) (text string, g *tabGroup, ok bool) {
	if len(a.tabGroups) == 0 || i < 0 || i >= len(a.tabs) {
		return "", nil, false
	}
	g = a.tabGroupOf(a.tabs[i])
	if g == nil {
		return "", nil, false
	}
	if i != first && i > 0 && a.tabGroupOf(a.tabs[i-1]) == g {
		return "", nil, false
	}
	return a.tabChipText(g), g, true
}

// tabChipText is the chip's label: " name " expanded, " name +N "
// collapsed, where N is how many members the fold is hiding — the count
// is what tells a folded chip apart from a group of one.
func (a *App) tabChipText(g *tabGroup) string {
	if g.collapsed {
		if n := a.tabGroupHiddenCount(g); n > 0 {
			return fmt.Sprintf(" %s +%d ", g.name, n)
		}
	}
	return " " + g.name + " "
}

// tabGroupHiddenCount is how many of g's tabs a fold hides: all members
// but the active tab.
func (a *App) tabGroupHiddenCount(g *tabGroup) int {
	n := 0
	for i, t := range a.tabs {
		if i != a.activeTab && a.tabGroupOf(t) == g {
			n++
		}
	}
	return n
}

// tabHiddenByGroup reports whether tab i's body is folded away: it is in
// a collapsed group and is not the active tab.
func (a *App) tabHiddenByGroup(i int) bool {
	if len(a.tabGroups) == 0 || i == a.activeTab || i < 0 || i >= len(a.tabs) {
		return false
	}
	g := a.tabGroupOf(a.tabs[i])
	return g != nil && g.collapsed
}

// -----------------------------------------------------------------------------
// Names
// -----------------------------------------------------------------------------

// tabGroupNameProblem returns why name can't be used for a group (except
// for the group being renamed, which may keep its own name), or "" when
// it can.
func (a *App) tabGroupNameProblem(name string, except *tabGroup) string {
	runes := []rune(name)
	switch {
	case len(runes) == 0:
		return "A group needs a name"
	case len(runes) > tabGroupNameMax:
		return fmt.Sprintf("Group names are at most %d letters (%q has %d)", tabGroupNameMax, name, len(runes))
	}
	for _, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return fmt.Sprintf("Group names are letters and digits only (%q)", name)
		}
	}
	if a.tabGroupByName(name, except) != nil {
		return fmt.Sprintf("There is already a group named %q", name)
	}
	return ""
}

// tabGroupByName finds a group by name, case-insensitively, skipping
// except.
func (a *App) tabGroupByName(name string, except *tabGroup) *tabGroup {
	for _, g := range a.tabGroups {
		if g != except && strings.EqualFold(g.name, name) {
			return g
		}
	}
	return nil
}

// tabGroupNameFrom suggests a name from s: its first four letters or
// digits, so "internal/editor" → "edit" and "README.md" → "READ".
// Empty when s has none.
func tabGroupNameFrom(s string) string {
	var b []rune
	for _, r := range s {
		if len(b) == tabGroupNameMax {
			break
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b = append(b, r)
		}
	}
	return string(b)
}

// uniqueTabGroupName returns base, or base cut to three runes plus a
// digit when base is taken ("api" → "api2"), so the name prompt's seed is
// usable as offered. Falls back to base when every digit is taken; the
// prompt then names the clash.
func (a *App) uniqueTabGroupName(base string) string {
	if base == "" || a.tabGroupByName(base, nil) == nil {
		return base
	}
	stem := []rune(base)
	if len(stem) > tabGroupNameMax-1 {
		stem = stem[:tabGroupNameMax-1]
	}
	for d := 2; d <= 9; d++ {
		if cand := fmt.Sprintf("%s%d", string(stem), d); a.tabGroupByName(cand, nil) == nil {
			return cand
		}
	}
	return base
}

// -----------------------------------------------------------------------------
// Mutations
// -----------------------------------------------------------------------------

// newTabGroup appends a group with the next free colour. folder is ""
// for an ad-hoc group.
func (a *App) newTabGroup(name, folder string) *tabGroup {
	g := &tabGroup{
		name:    name,
		folder:  folder,
		members: map[*editor.Tab]bool{},
		exclude: map[*editor.Tab]bool{},
		color:   a.nextTabGroupColor(),
	}
	a.tabGroups = append(a.tabGroups, g)
	return g
}

// addTabToGroup makes g the group t resolves to. t leaves any ad-hoc
// group first (one group per tab). For a folder group, t is let back in
// if it was excluded, and every DEEPER folder group that would claim it
// excludes it, so the pick actually wins. Shallower ones are left alone:
// deepest-wins already loses to g, and an exclusion there would outlive
// g if it were ungrouped later.
func (a *App) addTabToGroup(t *editor.Tab, g *tabGroup) {
	if t == nil || g == nil {
		return
	}
	for _, other := range a.tabGroups {
		if !other.isFolder() && other != g {
			delete(other.members, t)
		}
	}
	if g.isFolder() {
		delete(g.exclude, t)
		for _, other := range a.tabGroups {
			if other != g && other.isFolder() && len(other.folder) > len(g.folder) &&
				t.Path != "" && pathUnder(t.Path, other.folder) {
				other.exclude[t] = true
			}
		}
	} else {
		g.members[t] = true
	}
	a.dropEmptyAdhocGroups()
	a.arrangeTabGroups()
}

// removeTabFromGroup takes t out of the group it shows in: an ad-hoc
// membership is dropped, a folder group's rule gets an exclusion. If a
// shallower folder group also contains t, t falls through to that one —
// the visible group is the one the gesture was about.
func (a *App) removeTabFromGroup(t *editor.Tab) {
	g := a.tabGroupOf(t)
	if g == nil {
		return
	}
	if g.isFolder() {
		g.exclude[t] = true
	} else {
		delete(g.members, t)
	}
	a.dropEmptyAdhocGroups()
	a.arrangeTabGroups()
	a.flash(fmt.Sprintf("Removed %s from group %s", t.DisplayName(), g.name))
}

// dropEmptyAdhocGroups removes ad-hoc groups left with no members (see
// tabGroupsForgetTab).
func (a *App) dropEmptyAdhocGroups() {
	a.tabGroups = slices.DeleteFunc(a.tabGroups, func(g *tabGroup) bool {
		return !g.isFolder() && len(g.members) == 0
	})
}

// toggleTabGroupCollapsed folds or unfolds g.
func (a *App) toggleTabGroupCollapsed(g *tabGroup) {
	if g == nil || !a.tabGroupLive(g) {
		return
	}
	g.collapsed = !g.collapsed
}

// tabGroupLive reports whether g is still one of the groups — a menu or
// picker row captured g, and the group may have been ungrouped or
// emptied while it was open.
func (a *App) tabGroupLive(g *tabGroup) bool {
	return slices.Contains(a.tabGroups, g)
}

// ungroupTabGroup deletes g. Its tabs stay open, where they are.
func (a *App) ungroupTabGroup(g *tabGroup) {
	if !a.tabGroupLive(g) {
		return
	}
	a.tabGroups = slices.DeleteFunc(a.tabGroups, func(x *tabGroup) bool { return x == g })
	a.flash("Ungrouped " + g.name)
}

// makeTabGroupAdhoc turns a folder group into an ad-hoc one holding its
// current members, so tabs from elsewhere can join and files opened
// later under the folder no longer do.
func (a *App) makeTabGroupAdhoc(g *tabGroup) {
	if !a.tabGroupLive(g) || !g.isFolder() {
		return
	}
	members := a.tabGroupMembers(g)
	g.folder = ""
	g.exclude = map[*editor.Tab]bool{}
	for _, t := range members {
		g.members[t] = true
	}
	a.dropEmptyAdhocGroups()
	a.flash(fmt.Sprintf("%s is now an ad-hoc group (%s)", g.name, plural(len(members), "tab", "tabs")))
}

// closeTabGroupTabs closes g's tabs, keeping unsaved ones for the reason
// closeOtherTabs does (one click must not queue a stack of save dialogs
// the single modal slot can't hold). Pointers are captured first and
// resolved per close, since each close shifts the indices after it.
func (a *App) closeTabGroupTabs(g *tabGroup) {
	if !a.tabGroupLive(g) {
		return
	}
	dirty := 0
	for _, t := range a.tabGroupMembers(g) {
		if t.Dirty {
			dirty++
			continue
		}
		a.closeTab(a.tabIndexOf(t))
	}
	if dirty > 0 {
		a.flash("Kept " + plural(dirty, "unsaved tab", "unsaved tabs"))
	}
}

// -----------------------------------------------------------------------------
// Surfaces: chip menu, tab menu rows, ≡ rows, prompts, pickers
// -----------------------------------------------------------------------------

// tabGroupContextItems are the group menu's rows, a fixed list so the
// hand learns the positions: rows that don't apply dim rather than
// vanish (the editor menu's rule). The ≡ "Tab groups…" picker shows the
// same rows, so the two doors can't drift.
func (a *App) tabGroupContextItems(g *tabGroup) []editorContextItem {
	foldLabel := "Collapse group"
	if g.collapsed {
		foldLabel = "Expand group"
	}
	hasMembers := func(app *App) bool { return len(app.tabGroupMembers(g)) > 0 }
	return []editorContextItem{
		{label: foldLabel, action: func(app *App) { app.toggleTabGroupCollapsed(g) }, enabled: alwaysTrue},
		{label: "Switch to a tab in group…", action: func(app *App) { app.switchInTabGroup(g) }, enabled: hasMembers},
		{label: "Rename group…", action: func(app *App) { app.promptRenameTabGroup(g) }, enabled: alwaysTrue},
		{label: "Make ad-hoc group", action: func(app *App) { app.makeTabGroupAdhoc(g) }, enabled: func(*App) bool { return g.isFolder() }},
		{label: "Close group's tabs", action: func(app *App) { app.closeTabGroupTabs(g) }, enabled: hasMembers},
		{label: "Ungroup", action: func(app *App) { app.ungroupTabGroup(g) }, enabled: alwaysTrue},
	}
}

// openTabGroupMenu opens the group menu anchored one row below (x, y),
// so it never covers the chip it describes.
func (a *App) openTabGroupMenu(g *tabGroup, x, y int) {
	items := a.tabGroupContextItems(g)
	w := contextMenuWidth
	for _, it := range items {
		if lw := runeLen(it.label) + 6; lw > w { // border+chevron+padding
			w = lw
		}
	}
	w = min(w, a.width)
	cx, cy := a.placeContextSized(x, y+1, len(items), w)
	a.openModal(&editorContextModal{x: cx, y: cy, w: w, items: items})
}

// tabGroupDescribe is a group's one-line summary for pickers and prompt
// hints: its kind (with the folder, project-relative) and its tab count.
func (a *App) tabGroupDescribe(g *tabGroup) string {
	n := plural(len(a.tabGroupMembers(g)), "tab", "tabs")
	if g.isFolder() {
		return fmt.Sprintf("folder %s · %s", a.tabGroupFolderLabel(g.folder), n)
	}
	return "ad-hoc · " + n
}

// tabGroupFolderLabel is dir as the user knows it: project-relative with
// a trailing slash, "./" for the root itself, absolute outside it.
func (a *App) tabGroupFolderLabel(dir string) string {
	rel, err := filepath.Rel(a.rootDir, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return dir + "/"
	}
	if rel == "." {
		return "./"
	}
	return rel + "/"
}

// openAddToGroup is the tab menu's "Add to group…": a picker of the
// groups t could join, then the two ways to start one — an ad-hoc group,
// or a folder group for t's own directory (offered only when the tab has
// a file and no group already claims that folder).
func (a *App) openAddToGroup(t *editor.Tab) {
	if t == nil || a.tabIndexOf(t) < 0 {
		return
	}
	cur := a.tabGroupOf(t)
	var items []paletteItem
	for _, g := range a.tabGroups {
		// A folder group can only take a tab that lives under it.
		if g == cur || (g.isFolder() && (t.Path == "" || !pathUnder(t.Path, g.folder))) {
			continue
		}
		items = append(items, paletteItem{
			label: fmt.Sprintf("%s  %s", g.name, a.tabGroupDescribe(g)),
			run: func(app *App) {
				app.addTabToGroup(t, g)
				app.flash(fmt.Sprintf("Added %s to %s", t.DisplayName(), g.name))
			},
		})
	}
	items = append(items, paletteItem{
		label: "New ad-hoc group…",
		run:   func(app *App) { app.promptNewTabGroup(t, "", app.uniqueTabGroupName("grp")) },
	})
	if t.Path != "" {
		dir := filepath.Dir(t.Path)
		taken := false
		for _, g := range a.tabGroups {
			taken = taken || (g.isFolder() && g.folder == dir)
		}
		if !taken {
			items = append(items, paletteItem{
				label: "New folder group: " + a.tabGroupFolderLabel(dir),
				run: func(app *App) {
					app.promptNewTabGroup(t, dir, app.uniqueTabGroupName(tabGroupNameFrom(filepath.Base(dir))))
				},
			})
		}
	}
	title := "Add " + t.DisplayName() + " to group"
	if cur != nil {
		title += " (now in " + cur.name + ")"
	}
	a.openPicker(title, items)
}

// promptNewTabGroup asks for a new group's name, seeded with seed, and
// creates it with t in it. folder is "" for an ad-hoc group. A refused
// name flashes why and re-asks with what was typed, so a five-letter
// attempt costs one Backspace rather than starting over.
func (a *App) promptNewTabGroup(t *editor.Tab, folder, seed string) {
	hint := fmt.Sprintf("ad-hoc group · up to %d letters or digits", tabGroupNameMax)
	title := "New tab group"
	if folder != "" {
		hint = fmt.Sprintf("folder %s · up to %d letters or digits", a.tabGroupFolderLabel(folder), tabGroupNameMax)
		title = "New folder group"
	}
	a.openPrompt(title, hint, seed, func(app *App, name string) {
		if why := app.tabGroupNameProblem(name, nil); why != "" {
			app.flash(why)
			app.promptNewTabGroup(t, folder, name)
			return
		}
		if app.tabIndexOf(t) < 0 {
			return
		}
		g := app.newTabGroup(name, folder)
		app.addTabToGroup(t, g)
		if folder != "" {
			app.flash(fmt.Sprintf("Group %s: %s open under %s", name,
				plural(len(app.tabGroupMembers(g)), "tab", "tabs"), app.tabGroupFolderLabel(folder)))
		} else {
			app.flash(fmt.Sprintf("Group %s: added %s", name, t.DisplayName()))
		}
	})
}

// promptRenameTabGroup asks for g's new name, re-asking on a refusal like
// promptNewTabGroup.
func (a *App) promptRenameTabGroup(g *tabGroup) {
	if !a.tabGroupLive(g) {
		return
	}
	a.promptRenameTabGroupWith(g, g.name)
}

// promptRenameTabGroupWith is promptRenameTabGroup with the field seeded
// by seed (the refused value on a re-ask).
func (a *App) promptRenameTabGroupWith(g *tabGroup, seed string) {
	a.openPrompt("Rename group "+g.name, a.tabGroupDescribe(g), seed, func(app *App, name string) {
		if !app.tabGroupLive(g) {
			return
		}
		if why := app.tabGroupNameProblem(name, g); why != "" {
			app.flash(why)
			app.promptRenameTabGroupWith(g, name)
			return
		}
		g.name = name
	})
}

// switchInTabGroup is a picker of g's tabs — the way into a folded group
// without unfolding it. Unlike the switcher it lists the active tab too:
// the question is "what's in this group", not "where else can I go".
func (a *App) switchInTabGroup(g *tabGroup) {
	if !a.tabGroupLive(g) {
		return
	}
	var items []paletteItem
	for _, t := range a.tabGroupMembers(g) {
		items = append(items, paletteItem{
			label: a.tabPickerLabel(t.DisplayName(), t.Path, t.Dirty),
			run:   func(app *App) { app.switchToTab(app.tabIndexOf(t)) },
		})
	}
	a.openPicker("Group "+g.name, items)
}

// menuAddActiveTabToGroup is the ≡ File twin of the tab menu's "Add to
// group…", for the active tab.
func (a *App) menuAddActiveTabToGroup() {
	a.closeMenu()
	a.openAddToGroup(a.activeTabPtr())
}

// menuTabGroups is ≡ File "Tab groups…": a picker of the groups, then a
// picker of the chosen group's menu rows — the keyboard door to the
// chip's right-click menu (macOS Terminal + tmux often swallow the right
// button). With no groups it says how to make one rather than dimming.
func (a *App) menuTabGroups() {
	a.closeMenu()
	if len(a.tabGroups) == 0 {
		a.flash("No tab groups — right-click a tab → Add to group…, or ≡ File → Add tab to group…")
		return
	}
	items := make([]paletteItem, 0, len(a.tabGroups))
	for _, g := range a.tabGroups {
		items = append(items, paletteItem{
			label: fmt.Sprintf("%s  %s", g.name, a.tabGroupDescribe(g)),
			run:   func(app *App) { app.tabGroupActionsPicker(g) },
		})
	}
	a.openPicker("Tab groups", items)
}

// tabGroupActionsPicker shows g's menu rows as a picker. Dimmed rows are
// left out — a picker has no dim state, and a row that does nothing is
// noise in a filtered list.
func (a *App) tabGroupActionsPicker(g *tabGroup) {
	if !a.tabGroupLive(g) {
		return
	}
	var items []paletteItem
	for _, it := range a.tabGroupContextItems(g) {
		if it.enabled != nil && !it.enabled(a) {
			continue
		}
		items = append(items, paletteItem{label: it.label, run: it.action})
	}
	a.openPicker("Group "+g.name+" ("+a.tabGroupDescribe(g)+")", items)
}

// -----------------------------------------------------------------------------
// Session
// -----------------------------------------------------------------------------

// encodeTabGroups is the groups' disk form. Ad-hoc members are stored by
// path; members with no file (untitled) can't come back and are left out.
func (a *App) encodeTabGroups() []session.TabGroup {
	var out []session.TabGroup
	for _, g := range a.tabGroups {
		sg := session.TabGroup{Name: g.name, Folder: g.folder, Collapsed: g.collapsed, Color: g.color}
		for _, t := range a.tabs {
			switch {
			case t.Path == "":
			case !g.isFolder() && g.members[t]:
				sg.Members = append(sg.Members, t.Path)
			case g.isFolder() && g.exclude[t]:
				sg.Exclude = append(sg.Exclude, t.Path)
			}
		}
		out = append(out, sg)
	}
	return out
}

// restoreTabGroups rebuilds the groups from disk against the tabs the
// restore actually reopened: paths with no tab are dropped, and an
// ad-hoc group none of whose files came back is dropped with them (the
// same quiet degradation as the tab restore itself).
func (a *App) restoreTabGroups(groups []session.TabGroup) {
	byPath := make(map[string]*editor.Tab, len(a.tabs))
	for _, t := range a.tabs {
		if t.Path != "" {
			byPath[t.Path] = t
		}
	}
	for _, sg := range groups {
		if a.tabGroupNameProblem(sg.Name, nil) != "" {
			continue // hand-edited into something the prompt would refuse
		}
		g := &tabGroup{
			name:      sg.Name,
			folder:    sg.Folder,
			members:   map[*editor.Tab]bool{},
			exclude:   map[*editor.Tab]bool{},
			collapsed: sg.Collapsed,
			color:     sg.Color,
		}
		for _, p := range sg.Members {
			if t := byPath[p]; t != nil {
				g.members[t] = true
			}
		}
		for _, p := range sg.Exclude {
			if t := byPath[p]; t != nil {
				g.exclude[t] = true
			}
		}
		if !g.isFolder() && len(g.members) == 0 {
			continue
		}
		a.tabGroups = append(a.tabGroups, g)
	}
	a.arrangeTabGroups()
}
