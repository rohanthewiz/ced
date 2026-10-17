// =============================================================================
// File: internal/app/catsworkspace_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rohanthewiz/ced/internal/cats"
	"github.com/rohanthewiz/ced/internal/session"
)

// ladderDirs makes the folders a ladder test points panes at, normalized
// the way planFolderRoute compares them (macOS temp dirs resolve through
// /private).
func ladderDirs(t *testing.T, names ...string) []string {
	t.Helper()
	out := mkdirs(t, t.TempDir(), names...)
	for i, d := range out {
		out[i] = session.Normalize(d)
	}
	return out
}

// TestPlanFolderRoute_FocusesARunningEditor pins rung 1: a ced already rooted
// at the folder is revealed wherever it is, rather than a second one spawned.
func TestPlanFolderRoute_FocusesARunningEditor(t *testing.T) {
	d := ladderDirs(t, "proj")
	panes := []cats.PaneInfo{
		{Pane: 9, Handle: "w1:p1", Agent: "ced", Cwd: "/elsewhere"},
		{Pane: 21, Handle: "w3:p2", Agent: "ced", Cwd: d[0]},
	}
	wss := []cats.WorkspaceEntry{{ID: "w1", Name: "here"}, {ID: "w3", Name: "other"}}

	r := planFolderRoute(d[0], panes, wss, 9, true)
	if r.kind != routeFocusEditor || r.pane != 21 || r.workspace != "w3" || r.wsName != "other" {
		t.Fatalf("route = %+v, want focus pane 21 in w3", r)
	}
}

// TestPlanFolderRoute_PrefersTheEditorInTheFolderWorkspace pins the tie-break
// between several editors on one folder: the one inside the folder's own
// workspace, before the lowest pane id elsewhere.
func TestPlanFolderRoute_PrefersTheEditorInTheFolderWorkspace(t *testing.T) {
	d := ladderDirs(t, "proj")
	panes := []cats.PaneInfo{
		{Pane: 5, Handle: "w1:p1", Agent: "ced", Cwd: d[0]},
		{Pane: 30, Handle: "w2:p1", Cwd: d[0]},
		{Pane: 31, Handle: "w2:p2", Agent: "CED", Cwd: d[0]},
	}
	wss := []cats.WorkspaceEntry{{ID: "w1", Name: "misc"}, {ID: "w2", Name: "proj"}}

	r := planFolderRoute(d[0], panes, wss, 0, false)
	if r.kind != routeFocusEditor || r.pane != 31 {
		t.Fatalf("route = %+v, want pane 31 (the editor in the proj workspace)", r)
	}
}

// TestPlanFolderRoute_NeverPicksItself pins that our own pane is not an
// answer, even if its cwd claims the folder.
func TestPlanFolderRoute_NeverPicksItself(t *testing.T) {
	d := ladderDirs(t, "proj")
	panes := []cats.PaneInfo{{Pane: 9, Handle: "w1:p1", Agent: "ced", Cwd: d[0]}}

	if r := planFolderRoute(d[0], panes, nil, 9, true); r.kind == routeFocusEditor {
		t.Fatalf("route = %+v — ced picked its own pane", r)
	}
}

// TestPlanFolderRoute_SpawnsInTheMatchingWorkspace pins rung 2 and the
// strong rule: a workspace named after the folder with a shell under it
// wins over one whose stray shell merely sits at the folder.
func TestPlanFolderRoute_SpawnsInTheMatchingWorkspace(t *testing.T) {
	d := ladderDirs(t, "proj/internal")
	proj := filepath.Dir(d[0])
	panes := []cats.PaneInfo{
		{Pane: 2, Handle: "w1:p1", Cwd: proj},     // weak: a shell that cd'd in
		{Pane: 3, Handle: "w2:p1", Cwd: d[0]},     // strong: under it, workspace named "proj"
		{Pane: 4, Handle: "w2:p2", Cwd: "/other"}, // irrelevant
	}
	wss := []cats.WorkspaceEntry{{ID: "w1", Name: "scratch"}, {ID: "w2", Name: "proj"}}

	r := planFolderRoute(proj, panes, wss, 0, false)
	if r.kind != routeSpawnInWorkspace || r.workspace != "w2" || r.wsName != "proj" {
		t.Fatalf("route = %+v, want spawn in w2", r)
	}
}

// TestPlanFolderRoute_IdentityCwdOutranksPaneEvidence pins the exact rule:
// a workspace whose stored identity folder IS the folder wins, even with no
// pane there and a better-named rival — and an asleep one still does not.
func TestPlanFolderRoute_IdentityCwdOutranksPaneEvidence(t *testing.T) {
	d := ladderDirs(t, "proj")
	panes := []cats.PaneInfo{
		{Pane: 3, Handle: "w1:p1", Cwd: d[0]},     // strong: named "proj", shell at the folder
		{Pane: 4, Handle: "w2:p1", Cwd: "/other"}, // its shell wandered off
	}
	wss := []cats.WorkspaceEntry{
		{ID: "w1", Name: "proj", Cwd: "/somewhere/else"},
		{ID: "w2", Name: "Renamed", Cwd: d[0]},
	}

	if r := planFolderRoute(d[0], panes, wss, 0, false); r.kind != routeSpawnInWorkspace || r.workspace != "w2" {
		t.Fatalf("route = %+v, want spawn in w2 (identity match)", r)
	}
	wss[1].Asleep = true
	if r := planFolderRoute(d[0], panes, wss, 0, false); r.workspace != "w1" {
		t.Fatalf("asleep identity: route = %+v, want the w1 fallback", r)
	}
}

// TestPlanFolderRoute_IdentityOnAnotherHostIsNotAMatch pins the host rule
// for identity folders: a workspace pinned to another machine names a path
// on that machine.
func TestPlanFolderRoute_IdentityOnAnotherHostIsNotAMatch(t *testing.T) {
	d := ladderDirs(t, "proj")
	panes := []cats.PaneInfo{{Pane: 9, Handle: "w1:p1", Host: "local"}}
	wss := []cats.WorkspaceEntry{{ID: "w2", Name: "proj", Cwd: d[0], Host: "devbox"}}

	if r := planFolderRoute(d[0], panes, wss, 9, true); r.kind != routeSpawnHere {
		t.Fatalf("route = %+v, want spawn here", r)
	}
	wss[0].Host = "" // the default host — ours as far as the policy says
	if r := planFolderRoute(d[0], panes, wss, 9, true); r.kind != routeSpawnInWorkspace {
		t.Fatalf("default-host route = %+v, want spawn in w2", r)
	}
}

// TestPlanFolderRoute_WeakMatchCoversACustomName pins the weak rule on its
// own: a renamed workspace whose shell sits at the folder still counts.
func TestPlanFolderRoute_WeakMatchCoversACustomName(t *testing.T) {
	d := ladderDirs(t, "proj")
	panes := []cats.PaneInfo{{Pane: 3, Handle: "w2:p1", Cwd: d[0]}}
	wss := []cats.WorkspaceEntry{{ID: "w2", Name: "My Project"}}

	if r := planFolderRoute(d[0], panes, wss, 0, false); r.kind != routeSpawnInWorkspace || r.workspace != "w2" {
		t.Fatalf("route = %+v, want spawn in w2", r)
	}
}

// TestPlanFolderRoute_NameAloneIsNotAMatch pins that sharing a basename is
// not evidence: two checkouts called "app" must not swap workspaces.
func TestPlanFolderRoute_NameAloneIsNotAMatch(t *testing.T) {
	d := ladderDirs(t, "a/app", "b/app")
	panes := []cats.PaneInfo{{Pane: 3, Handle: "w2:p1", Cwd: d[1]}}
	wss := []cats.WorkspaceEntry{{ID: "w2", Name: "app"}}

	if r := planFolderRoute(d[0], panes, wss, 0, false); r.kind != routeSpawnHere {
		t.Fatalf("route = %+v, want spawn here", r)
	}
}

// TestPlanFolderRoute_InactiveWorkspaceFallsBackHere pins rung 3: a folder
// whose workspace is asleep (or locked to automation, which would refuse the
// tab) opens in the current workspace.
func TestPlanFolderRoute_InactiveWorkspaceFallsBackHere(t *testing.T) {
	d := ladderDirs(t, "proj")
	panes := []cats.PaneInfo{{Pane: 3, Handle: "w2:p1", Agent: "ced", Cwd: d[0]}}

	asleep := []cats.WorkspaceEntry{{ID: "w2", Name: "proj", Asleep: true}}
	if r := planFolderRoute(d[0], panes, asleep, 0, false); r.kind != routeSpawnHere {
		t.Fatalf("asleep: route = %+v, want spawn here", r)
	}
	shells := []cats.PaneInfo{{Pane: 3, Handle: "w2:p1", Cwd: d[0]}}
	locked := []cats.WorkspaceEntry{{ID: "w2", Name: "proj", Locked: true}}
	if r := planFolderRoute(d[0], shells, locked, 0, false); r.kind != routeSpawnHere {
		t.Fatalf("locked: route = %+v, want spawn here", r)
	}
	if r := planFolderRoute(d[0], nil, nil, 0, false); r.kind != routeSpawnHere {
		t.Fatalf("no panes: route = %+v, want spawn here", r)
	}
}

// TestPlanFolderRoute_IgnoresOtherHosts pins the host filter: a pane on
// another cathost names a folder on another disk, whatever its path says.
func TestPlanFolderRoute_IgnoresOtherHosts(t *testing.T) {
	d := ladderDirs(t, "proj")
	panes := []cats.PaneInfo{
		{Pane: 9, Handle: "w1:p1", Host: "local"},
		{Pane: 3, Handle: "w2:p1", Agent: "ced", Cwd: d[0], Host: "devbox"},
	}

	if r := planFolderRoute(d[0], panes, nil, 9, true); r.kind != routeSpawnHere {
		t.Fatalf("route = %+v, want spawn here", r)
	}
}

// TestPaneWorkspaceID pins the handle parse, including the pre-public form
// that names no workspace.
func TestPaneWorkspaceID(t *testing.T) {
	for in, want := range map[string]string{"w2:p3": "w2", "w10:p1": "w10", "p_7": "", "": "", "x:p1": ""} {
		if got := paneWorkspaceID(in); got != want {
			t.Errorf("paneWorkspaceID(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFolderLabel pins the name cats auto-derives: the basename, "~" for home.
func TestFolderLabel(t *testing.T) {
	if got := folderLabel("/x/y/proj"); got != "proj" {
		t.Fatalf("folderLabel = %q, want proj", got)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory resolved")
	}
	if got := folderLabel(session.Normalize(home)); got != "~" {
		t.Fatalf("folderLabel(home) = %q, want ~", got)
	}
}

// TestCatsProjectTabParams pins the spawn shape both doors share: an argv
// rooted at the folder, in a tab named after it.
func TestCatsProjectTabParams(t *testing.T) {
	p := catsProjectTabParams("/opt/bin/ced", "/w/proj")
	if p.Title != "proj" || p.Cwd != "/w/proj" || len(p.Command) != 3 ||
		p.Command[0] != "/opt/bin/ced" || p.Command[1] != "--root" || p.Command[2] != "/w/proj" {
		t.Fatalf("params = %+v", p)
	}
}

// pinCatsExecutable stubs the binary lookup the spawning rungs need.
func pinCatsExecutable(t *testing.T) {
	t.Helper()
	catsExecutable = func() (string, error) { return "/opt/bin/ced", nil }
	t.Cleanup(func() { catsExecutable = os.Executable })
}

// TestOpenRecentFolder_RevealsTheRunningEditor drives rung 1 through the
// socket: agent.focus on the editor's pane, and no tab spawned.
func TestOpenRecentFolder_RevealsTheRunningEditor(t *testing.T) {
	d := ladderDirs(t, "proj")
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	pinCatsExecutable(t)
	s.paneRows = []cats.PaneInfo{
		{Pane: 9, Handle: "w1:p1"},
		{Pane: 21, Handle: "w2:p1", Agent: "ced", Cwd: d[0]},
	}
	s.wsRows = []cats.WorkspaceEntry{{ID: "w1", Name: "here"}, {ID: "w2", Name: "proj"}}

	a.openRecentFolder(d[0])

	call := waitForCall(t, s, cats.MethodAgentFocus)
	if got := string(call.Params); got != `{"pane":21}` {
		t.Fatalf("agent.focus params = %s", got)
	}
	if _, ok := s.call(cats.MethodTabCreate); ok {
		t.Fatal("a running editor was found — nothing should be spawned")
	}
	if a.quit {
		t.Fatal("at Tier 1 the pick must not re-root this editor")
	}
}

// TestOpenRecentFolder_SpawnsInTheFolderWorkspace drives rung 2: tab.create
// addressed to the folder's workspace, then a reveal of the new pane.
func TestOpenRecentFolder_SpawnsInTheFolderWorkspace(t *testing.T) {
	d := ladderDirs(t, "proj")
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	pinCatsExecutable(t)
	s.tabPane = 44
	s.paneRows = []cats.PaneInfo{{Pane: 9, Handle: "w1:p1"}, {Pane: 12, Handle: "w2:p1", Cwd: d[0]}}
	s.wsRows = []cats.WorkspaceEntry{{ID: "w1", Name: "here"}, {ID: "w2", Name: "proj"}}

	a.openRecentFolder(d[0])

	focus := waitForCall(t, s, cats.MethodAgentFocus)
	if got := string(focus.Params); got != `{"pane":44}` {
		t.Fatalf("agent.focus params = %s, want the new tab's pane", got)
	}
	tab, _ := s.call(cats.MethodTabCreate)
	var p struct {
		Workspace string   `json:"workspace"`
		Command   []string `json:"command"`
	}
	if err := json.Unmarshal(tab.Params, &p); err != nil {
		t.Fatalf("params: %v", err)
	}
	if p.Workspace != "w2" || len(p.Command) != 3 || p.Command[2] != d[0] {
		t.Fatalf("tab.create = %+v, want a ced on %s in w2", p, d[0])
	}
}

// TestOpenRecentFolder_SpawnsHereWithoutAWorkspace drives rung 3: no
// workspace for the folder, so the tab goes to the current one (no
// workspace field on the wire).
func TestOpenRecentFolder_SpawnsHereWithoutAWorkspace(t *testing.T) {
	d := ladderDirs(t, "proj")
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	pinCatsExecutable(t)
	s.paneRows = []cats.PaneInfo{{Pane: 9, Handle: "w1:p1", Cwd: "/elsewhere"}}
	s.wsRows = []cats.WorkspaceEntry{{ID: "w1", Name: "here"}}

	a.openRecentFolder(d[0])

	tab := waitForCall(t, s, cats.MethodTabCreate)
	var p map[string]any
	if err := json.Unmarshal(tab.Params, &p); err != nil {
		t.Fatalf("params: %v", err)
	}
	if _, ok := p["workspace"]; ok {
		t.Fatalf("tab.create = %s, want the current workspace (no workspace field)", tab.Params)
	}
}

// TestOpenRecentFolder_ReRootsAtTier0 pins the plain-terminal meaning of a
// pick: no workspaces to go to, so this editor re-roots, as it always has.
func TestOpenRecentFolder_ReRootsAtTier0(t *testing.T) {
	other := t.TempDir()
	a := newTestApp(t, t.TempDir())

	a.openRecentFolder(other)

	if !a.quit || session.Normalize(a.NextRoot()) != session.Normalize(other) {
		t.Fatalf("Tier 0 pick did not re-root: next=%q quit=%v", a.NextRoot(), a.quit)
	}
}
