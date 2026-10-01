// =============================================================================
// File: internal/app/tabgroups_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/session"
)

// groupTestApp opens four files — a.go, api/b.go, c.go, api/d.go, in that
// order — with a.go active, and draws once. The api/ pair is what folder
// groups get exercised on; the interleaving is what arranging fixes.
func groupTestApp(t *testing.T) (*App, string) {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{"a.go", "api/b.go", "c.go", "api/d.go"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := newTestApp(t, root)
	a.width, a.height = 160, 40
	for _, rel := range []string{"a.go", "api/b.go", "c.go", "api/d.go"} {
		a.openFile(filepath.Join(root, rel))
	}
	a.switchToTab(0)
	a.draw()
	return a, root
}

// tabNames lists the open tabs' base names in strip order.
func tabNames(a *App) string {
	var out []string
	for _, t := range a.tabs {
		out = append(out, filepath.Base(t.Path))
	}
	return strings.Join(out, " ")
}

// tabByName finds the open tab whose file is named base.
func tabByName(t *testing.T, a *App, base string) *editor.Tab {
	t.Helper()
	for _, tab := range a.tabs {
		if filepath.Base(tab.Path) == base {
			return tab
		}
	}
	t.Fatalf("no tab named %s (have %s)", base, tabNames(a))
	return nil
}

// pickByLabel runs the open picker's first row whose label starts with
// prefix, the way runSelected does (modal closed first).
func pickByLabel(t *testing.T, a *App, prefix string) {
	t.Helper()
	pm, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want a picker", a.modal)
	}
	for _, it := range pm.items {
		if strings.HasPrefix(it.label, prefix) {
			a.closeModal()
			it.run(a)
			return
		}
	}
	t.Fatalf("picker has no row starting %q: %v", prefix, pickerRows(t, a))
}

// openPromptModal returns the open prompt, failing when there is none.
func openPromptModal(t *testing.T, a *App) *promptModal {
	t.Helper()
	pm, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want a prompt", a.modal)
	}
	return pm
}

// TestTabGroupNameProblem pins the name rule: one to four letters or
// digits, unique case-insensitively, a renamed group may keep its own.
func TestTabGroupNameProblem(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	g := a.newTabGroup("api", "")
	cases := []struct {
		name   string
		except *tabGroup
		ok     bool
	}{
		{"docs", nil, true},
		{"wip2", nil, true},
		{"ü", nil, true},
		{"", nil, false},
		{"hello", nil, false},
		{"a-b", nil, false},
		{"a b", nil, false},
		{"API", nil, false},
		{"API", g, true},
	}
	for _, c := range cases {
		if why := a.tabGroupNameProblem(c.name, c.except); (why == "") != c.ok {
			t.Errorf("name %q: problem %q, want ok=%v", c.name, why, c.ok)
		}
	}
}

// TestTabGroupNameFrom_AndUnique pins the seeds the prompts offer: the
// first four letters/digits of a folder or file name, made unique by a
// digit when taken.
func TestTabGroupNameFrom_AndUnique(t *testing.T) {
	for in, want := range map[string]string{"editor": "edit", "README.md": "READ", "a_b-c": "abc", "--": ""} {
		if got := tabGroupNameFrom(in); got != want {
			t.Errorf("tabGroupNameFrom(%q) = %q, want %q", in, got, want)
		}
	}
	a := newTestApp(t, t.TempDir())
	if got := a.uniqueTabGroupName("edit"); got != "edit" {
		t.Errorf("free name changed to %q", got)
	}
	a.newTabGroup("edit", "")
	if got := a.uniqueTabGroupName("edit"); got != "edi2" {
		t.Errorf("taken name became %q, want edi2", got)
	}
}

// TestArrangeTabGroups_ClustersAtTheFirstMember pins the reorder: a
// group's tabs gather where its first member sits, everything else keeps
// its relative order, and the active tab is followed by pointer.
func TestArrangeTabGroups_ClustersAtTheFirstMember(t *testing.T) {
	a, root := groupTestApp(t)
	c := tabByName(t, a, "c.go")
	a.switchToTab(a.tabIndexOf(c))
	a.newTabGroup("api", filepath.Join(root, "api"))
	a.arrangeTabGroups()
	if got := tabNames(a); got != "a.go b.go d.go c.go" {
		t.Errorf("order = %s, want a.go b.go d.go c.go", got)
	}
	if a.activeTabPtr() != c {
		t.Errorf("active tab = %s, want c.go followed across the reorder", a.activeTabPtr().Path)
	}
	before := tabNames(a)
	a.arrangeTabGroups()
	if tabNames(a) != before {
		t.Errorf("second arrange moved tabs: %s → %s", before, tabNames(a))
	}
}

// TestArrangeTabGroups_NoGroupsIsANoOp pins that ungrouped use is
// untouched: no groups, no reorder.
func TestArrangeTabGroups_NoGroupsIsANoOp(t *testing.T) {
	a, _ := groupTestApp(t)
	a.arrangeTabGroups()
	if got := tabNames(a); got != "a.go b.go c.go d.go" {
		t.Errorf("order = %s, want the open order", got)
	}
}

// TestFolderGroup_ClaimsFilesOpenedLater pins the folder rule: a file
// opened under the folder after the group exists joins it and is drawn
// beside the others.
func TestFolderGroup_ClaimsFilesOpenedLater(t *testing.T) {
	a, root := groupTestApp(t)
	a.newTabGroup("api", filepath.Join(root, "api"))
	late := filepath.Join(root, "api", "e.go")
	if err := os.WriteFile(late, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.openFile(late)
	a.draw()
	if g := a.tabGroupOf(a.activeTabPtr()); g == nil || g.name != "api" {
		t.Fatalf("late file's group = %v, want api", g)
	}
	if got := tabNames(a); got != "a.go b.go d.go e.go c.go" {
		t.Errorf("order = %s, want e.go gathered into api", got)
	}
}

// TestTabGroupOf_Precedence pins the resolution order: ad-hoc
// membership beats any folder rule, and the deepest folder group wins.
func TestTabGroupOf_Precedence(t *testing.T) {
	a, root := groupTestApp(t)
	outer := a.newTabGroup("all", root)
	inner := a.newTabGroup("api", filepath.Join(root, "api"))
	b := tabByName(t, a, "b.go")
	if got := a.tabGroupOf(b); got != inner {
		t.Errorf("b.go in %v, want the deeper api group", got)
	}
	if got := a.tabGroupOf(tabByName(t, a, "a.go")); got != outer {
		t.Errorf("a.go in %v, want the root group", got)
	}
	hand := a.newTabGroup("mine", "")
	a.addTabToGroup(b, hand)
	if got := a.tabGroupOf(b); got != hand {
		t.Errorf("b.go in %v after an explicit add, want mine", got)
	}
}

// TestAddTabToGroup_FolderPickBeatsADeeperRule pins that adding a tab to
// a shallower folder group actually lands it there.
func TestAddTabToGroup_FolderPickBeatsADeeperRule(t *testing.T) {
	a, root := groupTestApp(t)
	outer := a.newTabGroup("all", root)
	a.newTabGroup("api", filepath.Join(root, "api"))
	b := tabByName(t, a, "b.go")
	a.addTabToGroup(b, outer)
	if got := a.tabGroupOf(b); got != outer {
		t.Errorf("b.go in %v, want all", got)
	}
}

// TestRemoveTabFromGroup pins both kinds: an ad-hoc tab is dropped (and
// its now-empty group with it); a folder member is excluded so the rule
// doesn't pull it straight back.
func TestRemoveTabFromGroup(t *testing.T) {
	a, root := groupTestApp(t)
	folder := a.newTabGroup("api", filepath.Join(root, "api"))
	b := tabByName(t, a, "b.go")
	a.removeTabFromGroup(b)
	a.draw()
	if a.tabGroupOf(b) != nil || !folder.exclude[b] {
		t.Errorf("b.go still grouped after removal")
	}

	c := tabByName(t, a, "c.go")
	hand := a.newTabGroup("mine", "")
	a.addTabToGroup(c, hand)
	a.removeTabFromGroup(c)
	if a.tabGroupLive(hand) {
		t.Errorf("empty ad-hoc group survived its last member")
	}
	if !strings.Contains(a.statusMsg, "Removed c.go from group mine") {
		t.Errorf("flash = %q", a.statusMsg)
	}
}

// TestCloseTab_ForgetsGroupMembership pins the closeTab hook: the
// pointer leaves the group, and an ad-hoc group goes with its last tab
// while a folder group (a rule) stays.
func TestCloseTab_ForgetsGroupMembership(t *testing.T) {
	a, root := groupTestApp(t)
	folder := a.newTabGroup("api", filepath.Join(root, "api"))
	hand := a.newTabGroup("mine", "")
	c := tabByName(t, a, "c.go")
	a.addTabToGroup(c, hand)
	a.closeTab(a.tabIndexOf(c))
	if a.tabGroupLive(hand) {
		t.Errorf("ad-hoc group outlived its only tab")
	}
	a.closeTab(a.tabIndexOf(tabByName(t, a, "b.go")))
	a.closeTab(a.tabIndexOf(tabByName(t, a, "d.go")))
	if !a.tabGroupLive(folder) {
		t.Errorf("folder group dropped with its tabs; it is a rule")
	}
}

// TestDrawTabBar_ChipAndMemberUnderline pins the paint: the chip's name
// sits at ChipX on the group colour, and member tabs are underlined in it
// while ungrouped ones aren't.
func TestDrawTabBar_ChipAndMemberUnderline(t *testing.T) {
	a, root := groupTestApp(t)
	g := a.newTabGroup("api", filepath.Join(root, "api"))
	a.draw()
	var chip, member, plain tabRect
	for _, r := range a.lastTabRects {
		switch filepath.Base(a.tabs[r.Index].Path) {
		case "b.go":
			chip, member = r, r
		case "c.go":
			plain = r
		}
	}
	if chip.ChipW != len(" api ") || chip.Group != g {
		t.Fatalf("b.go rect = %+v, want the api chip", chip)
	}
	_, ty, _, _ := a.tabBarRect()
	var got []rune
	for x := chip.ChipX; x < chip.ChipX+chip.ChipW; x++ {
		ru, _, _, _ := a.screen.GetContent(x, ty)
		got = append(got, ru)
	}
	if string(got) != " api " {
		t.Errorf("chip text = %q, want \" api \"", string(got))
	}
	if _, _, st, _ := a.screen.GetContent(chip.ChipX, ty); true {
		if _, bg, _ := st.Decompose(); bg != a.tabGroupColor(g) {
			t.Errorf("chip bg = %v, want the group colour", bg)
		}
	}
	ul := func(r tabRect) bool {
		_, _, st, _ := a.screen.GetContent(r.X+2, ty)
		return st.GetUnderlineStyle() != tcell.UnderlineStyleNone
	}
	if !ul(member) {
		t.Errorf("member tab not underlined")
	}
	if ul(plain) {
		t.Errorf("ungrouped tab underlined")
	}
	if member.X != chip.ChipX+chip.ChipW {
		t.Errorf("body starts at %d, want right after the chip (%d)", member.X, chip.ChipX+chip.ChipW)
	}
}

// TestCollapsedGroup_HidesMembersButNeverTheActiveTab pins the fold: the
// chip counts what it hides, hidden members get a zero-width rect (so +N
// doesn't count them), and the active member stays drawn.
func TestCollapsedGroup_HidesMembersButNeverTheActiveTab(t *testing.T) {
	a, root := groupTestApp(t)
	g := a.newTabGroup("api", filepath.Join(root, "api"))
	g.collapsed = true
	a.draw()
	for _, r := range a.lastTabRects {
		if a.tabGroupOf(a.tabs[r.Index]) == g && r.Width != 0 {
			t.Errorf("%s drawn while folded", a.tabs[r.Index].Path)
		}
	}
	if got := a.tabChipText(g); got != " api +2 " {
		t.Errorf("chip = %q, want \" api +2 \"", got)
	}
	if len(a.lastTabRects) != len(a.tabs) {
		t.Errorf("%d rects for %d tabs; folded tabs must still get one", len(a.lastTabRects), len(a.tabs))
	}

	d := tabByName(t, a, "d.go")
	a.switchToTab(a.tabIndexOf(d))
	a.draw()
	r := tabRectFor(t, a, d.Path)
	if r.Width == 0 {
		t.Errorf("active member folded away")
	}
	if got := a.tabChipText(g); got != " api +1 " {
		t.Errorf("chip = %q, want \" api +1 \"", got)
	}
}

// TestChipClick_TogglesCollapse pins the chip's left click.
func TestChipClick_TogglesCollapse(t *testing.T) {
	a, root := groupTestApp(t)
	g := a.newTabGroup("api", filepath.Join(root, "api"))
	a.draw()
	r := tabRectFor(t, a, filepath.Join(root, "api", "b.go"))
	a.tabBarClick(r.ChipX, 0)
	if !g.collapsed {
		t.Fatalf("chip click did not fold the group")
	}
	a.draw()
	a.tabBarClick(r.ChipX, 0)
	if g.collapsed {
		t.Errorf("second chip click did not unfold the group")
	}
	if a.activeTab != 0 {
		t.Errorf("chip click switched tabs (active = %d)", a.activeTab)
	}
}

// TestChipRightClick_OpensTheGroupMenu pins the chip's menu and its rows;
// "Make ad-hoc group" is live only on a folder group.
func TestChipRightClick_OpensTheGroupMenu(t *testing.T) {
	a, root := groupTestApp(t)
	a.newTabGroup("api", filepath.Join(root, "api"))
	a.draw()
	r := tabRectFor(t, a, filepath.Join(root, "api", "b.go"))
	rightClick(a, r.ChipX+1, 0)
	m, ok := a.modal.(*editorContextModal)
	if !ok {
		t.Fatalf("modal = %T, want the group menu", a.modal)
	}
	var labels []string
	for _, it := range m.items {
		labels = append(labels, it.label)
		if it.label == "Make ad-hoc group" && !it.enabled(a) {
			t.Errorf("Make ad-hoc dimmed on a folder group")
		}
	}
	want := "Collapse group|Switch to a tab in group…|Rename group…|Make ad-hoc group|Close group's tabs|Ungroup"
	if got := strings.Join(labels, "|"); got != want {
		t.Errorf("rows = %s, want %s", got, want)
	}
}

// TestTabMenu_AddToNewAdhocGroup walks the whole creation path from the
// tab menu: Add to group… → New ad-hoc group… → name → grouped.
func TestTabMenu_AddToNewAdhocGroup(t *testing.T) {
	a, root := groupTestApp(t)
	c := filepath.Join(root, "c.go")
	r := tabRectFor(t, a, c)
	rightClick(a, r.X+2, 0)
	runTabMenuRow(t, a, "Add to group…")
	pickByLabel(t, a, "New ad-hoc group…")
	pm := openPromptModal(t, a)
	if got := pm.field.String(); got != "grp" {
		t.Errorf("seed = %q, want grp", got)
	}
	submitPrompt(t, a, pm, "wip")
	g := a.tabGroupOf(tabByName(t, a, "c.go"))
	if g == nil || g.name != "wip" || g.isFolder() {
		t.Fatalf("c.go group = %+v, want ad-hoc wip", g)
	}
	// The grouped tab's menu now offers the way back out.
	if labels := strings.Join(tabMenuLabels(a, tabByName(t, a, "c.go")), "|"); !strings.Contains(labels, "Remove from group wip") {
		t.Errorf("tab menu = %s, want a Remove row", labels)
	}
}

// TestTabMenu_AddToNewFolderGroup pins the folder offer: seeded from the
// directory's name, and the new group takes every open file under it.
func TestTabMenu_AddToNewFolderGroup(t *testing.T) {
	a, _ := groupTestApp(t)
	a.openAddToGroup(tabByName(t, a, "b.go"))
	pickByLabel(t, a, "New folder group: api/")
	pm := openPromptModal(t, a)
	if got := pm.field.String(); got != "api" {
		t.Errorf("seed = %q, want api", got)
	}
	submitPrompt(t, a, pm, "api")
	if got := len(a.tabGroupMembers(a.tabGroups[0])); got != 2 {
		t.Errorf("folder group has %d tabs, want b.go and d.go", got)
	}
	// The folder is taken now: the offer goes, the group is listed only
	// for tabs that live under it.
	a.openAddToGroup(tabByName(t, a, "a.go"))
	for _, row := range pickerRows(t, a) {
		if strings.HasPrefix(row, "api") || strings.HasPrefix(row, "New folder group: api/") {
			t.Errorf("a.go offered %q", row)
		}
	}
}

// TestNewTabGroup_RefusedNameReasks pins the refusal path: a bad name
// flashes why and reopens the prompt holding what was typed.
func TestNewTabGroup_RefusedNameReasks(t *testing.T) {
	a, _ := groupTestApp(t)
	a.promptNewTabGroup(tabByName(t, a, "c.go"), "", "grp")
	submitPrompt(t, a, openPromptModal(t, a), "toolong")
	if !strings.Contains(a.statusMsg, "at most 4") {
		t.Errorf("flash = %q, want the length rule", a.statusMsg)
	}
	if got := openPromptModal(t, a).field.String(); got != "toolong" {
		t.Errorf("re-asked with %q, want the typed value", got)
	}
	if len(a.tabGroups) != 0 {
		t.Errorf("a group was created from a refused name")
	}
}

// TestRenameTabGroup pins rename, including keeping its own name in a
// different case.
func TestRenameTabGroup(t *testing.T) {
	a, root := groupTestApp(t)
	g := a.newTabGroup("api", filepath.Join(root, "api"))
	a.promptRenameTabGroup(g)
	submitPrompt(t, a, openPromptModal(t, a), "API")
	if g.name != "API" {
		t.Errorf("name = %q, want API", g.name)
	}
}

// TestMakeTabGroupAdhoc pins the conversion: current members are frozen
// in, and files opened later under the folder no longer join.
func TestMakeTabGroupAdhoc(t *testing.T) {
	a, root := groupTestApp(t)
	g := a.newTabGroup("api", filepath.Join(root, "api"))
	a.makeTabGroupAdhoc(g)
	if g.isFolder() || len(g.members) != 2 {
		t.Fatalf("group = %+v, want ad-hoc with b.go and d.go", g)
	}
	late := filepath.Join(root, "api", "e.go")
	if err := os.WriteFile(late, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.openFile(late)
	if a.tabGroupOf(a.activeTabPtr()) != nil {
		t.Errorf("a late file joined an ad-hoc group")
	}
}

// TestCloseTabGroupTabs_KeepsUnsaved pins the close verb: clean members
// close, dirty ones stay and are counted, ungrouped tabs are untouched.
func TestCloseTabGroupTabs_KeepsUnsaved(t *testing.T) {
	a, root := groupTestApp(t)
	g := a.newTabGroup("api", filepath.Join(root, "api"))
	tabByName(t, a, "d.go").Dirty = true
	a.closeTabGroupTabs(g)
	if got := tabNames(a); got != "a.go c.go d.go" {
		t.Errorf("tabs = %s, want b.go closed and dirty d.go kept", got)
	}
	if !strings.Contains(a.statusMsg, "Kept 1 unsaved tab") {
		t.Errorf("flash = %q", a.statusMsg)
	}
}

// TestUngroupTabGroup pins that ungrouping keeps every tab open.
func TestUngroupTabGroup(t *testing.T) {
	a, root := groupTestApp(t)
	g := a.newTabGroup("api", filepath.Join(root, "api"))
	a.ungroupTabGroup(g)
	if len(a.tabGroups) != 0 || len(a.tabs) != 4 {
		t.Errorf("groups = %d, tabs = %d; want 0 groups and 4 tabs", len(a.tabGroups), len(a.tabs))
	}
}

// TestStripScrolledIntoAGroup_FirstDrawnMemberCarriesTheChip pins the
// scroll rule: the leftmost drawn tab carries its group's chip even when
// it isn't the group's first member, so the group stays named.
func TestStripScrolledIntoAGroup_FirstDrawnMemberCarriesTheChip(t *testing.T) {
	a, root := groupTestApp(t)
	a.newTabGroup("api", filepath.Join(root, "api"))
	a.arrangeTabGroups() // a.go b.go d.go c.go
	d := a.tabIndexOf(tabByName(t, a, "d.go"))
	if _, _, ok := a.tabChip(d, 0); ok {
		t.Errorf("d.go carries a chip with b.go drawn before it")
	}
	if _, _, ok := a.tabChip(d, d); !ok {
		t.Errorf("d.go as the first drawn tab carries no chip")
	}
}

// TestMenuTabGroups pins the ≡ door: with no groups it says how to make
// one; with groups it lists them and drills into the group's rows.
func TestMenuTabGroups(t *testing.T) {
	a, root := groupTestApp(t)
	a.menuTabGroups()
	if !strings.Contains(a.statusMsg, "Add to group") {
		t.Errorf("flash = %q, want how to make a group", a.statusMsg)
	}
	g := a.newTabGroup("api", filepath.Join(root, "api"))
	a.menuTabGroups()
	pickByLabel(t, a, "api")
	pickByLabel(t, a, "Collapse group")
	if !g.collapsed {
		t.Errorf("the picker's Collapse row did not fold the group")
	}
}

// TestMenuAddActiveTabToGroup pins the ≡ File twin: it acts on the
// active tab.
func TestMenuAddActiveTabToGroup(t *testing.T) {
	a, _ := groupTestApp(t)
	a.menuAddActiveTabToGroup()
	pm, ok := a.modal.(*paletteModal)
	if !ok || !strings.Contains(pm.title, "a.go") {
		t.Fatalf("modal = %T, want the add picker for a.go", a.modal)
	}
}

// TestTabGroups_SessionRoundTrip pins encode → restore: ad-hoc members
// and folder exclusions come back by path, the fold and colour survive,
// and an ad-hoc group whose files didn't come back is dropped.
func TestTabGroups_SessionRoundTrip(t *testing.T) {
	a, root := groupTestApp(t)
	folder := a.newTabGroup("api", filepath.Join(root, "api"))
	folder.collapsed = true
	a.removeTabFromGroup(tabByName(t, a, "d.go"))
	hand := a.newTabGroup("mine", "")
	a.addTabToGroup(tabByName(t, a, "c.go"), hand)
	saved := a.encodeTabGroups()
	saved = append(saved, session.TabGroup{Name: "gone", Members: []string{filepath.Join(root, "nope.go")}})

	b, _ := groupTestApp(t)
	// Re-point the stored paths at b's root: same layout, new temp dir.
	for i := range saved {
		if saved[i].Folder != "" {
			saved[i].Folder = strings.Replace(saved[i].Folder, root, b.rootDir, 1)
		}
		for j := range saved[i].Members {
			saved[i].Members[j] = strings.Replace(saved[i].Members[j], root, b.rootDir, 1)
		}
		for j := range saved[i].Exclude {
			saved[i].Exclude[j] = strings.Replace(saved[i].Exclude[j], root, b.rootDir, 1)
		}
	}
	b.restoreTabGroups(saved)
	if len(b.tabGroups) != 2 {
		t.Fatalf("restored %d groups, want api and mine", len(b.tabGroups))
	}
	if g := b.tabGroupOf(tabByName(t, b, "b.go")); g == nil || g.name != "api" || !g.collapsed || g.color != folder.color {
		t.Errorf("b.go group = %+v, want the folded api group", g)
	}
	if g := b.tabGroupOf(tabByName(t, b, "d.go")); g != nil {
		t.Errorf("d.go's exclusion was lost (now in %s)", g.name)
	}
	if g := b.tabGroupOf(tabByName(t, b, "c.go")); g == nil || g.name != "mine" {
		t.Errorf("c.go group = %+v, want mine", g)
	}
}
