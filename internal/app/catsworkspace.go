// =============================================================================
// File: internal/app/catsworkspace.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Recent folder → its workspace: inside cats, picking another project in
// "Recent folders…" (the tree's ⭑) goes to where that project already
// lives instead of re-rooting this editor.
//
// cats organises work into WORKSPACES, and a project usually has one — the
// workspace whose shells sit in it, auto-named after its folder. Re-rooting
// this ced onto that project would pull it out of its own workspace and
// leave two places claiming the project. So at Tier 1 a pick resolves,
// first match wins:
//
//	1. a ced already rooted at the folder (any workspace)  → reveal that pane
//	2. the folder's workspace, awake and open to automation → new ced tab THERE, then reveal it
//	3. otherwise (no such workspace, or it is asleep)       → new ced tab in the CURRENT workspace
//
// Rung 1 prefers an editor inside the folder's workspace when there are
// several, then the lowest pane id — the same stable tie-break cats'
// pane.open_file uses, so two picks in a row land on the same editor.
//
// HOW A WORKSPACE IS MATCHED TO A FOLDER. workspace.list carries each
// workspace's identity folder (`cwd`, the folder it was created for), and an
// exact match on it outranks everything else. A cats older than that field
// sends none, so below it the match is read from evidence pane.list does
// carry (handles "w2:p3" name the workspace, rows carry the live cwd):
//
//	exact   the workspace's identity cwd IS the folder.
//	strong  the workspace's name is the folder's label (basename, "~" for
//	        home) — what cats auto-names it — AND one of its panes is at or
//	        under the folder. The name alone is not enough: two checkouts
//	        called "app" share it.
//	weak    one of its panes sits exactly at the folder. Covers a
//	        custom-named workspace; ranked below so a stray shell that
//	        cd'd into the project does not out-vote the project's own
//	        workspace.
//
// Ties go to the first workspace in pane.list order, which is cats' own
// workspace order. A ced pane's cwd is its ROOT: ced emits OSC 7 for its
// root at startup (hostident.go), and cats stops probing a pane's process
// cwd once the pane has reported one — so grsh's in-process `cd` cannot
// move it.
//
// Panes on another cathost are ignored: the same path on two machines names
// two folders, and a ced spawned there would run a binary path from this
// one.
//
// Tier 0 has no workspaces; the pick keeps its original meaning, re-rooting
// this editor (requestOpenFolder). The whole sequence runs on a goroutine —
// it is up to four socket round trips — and ends in one notice, the
// events-only rule every cats action here keeps.

package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rohanthewiz/ced/internal/cats"
	"github.com/rohanthewiz/ced/internal/session"
)

// folderRouteKind is which rung of the ladder a pick landed on.
type folderRouteKind int

const (
	// routeFocusEditor: a ced rooted at the folder is running; reveal it.
	routeFocusEditor folderRouteKind = iota
	// routeSpawnInWorkspace: the folder's workspace is awake; open a new
	// ced tab there and follow it.
	routeSpawnInWorkspace
	// routeSpawnHere: nowhere to go; open a new ced tab in the current
	// workspace.
	routeSpawnHere
)

// folderRoute is the resolved plan for one pick. workspace / wsName name
// the folder's workspace when one was matched (also for routeFocusEditor,
// where they are the editor's own workspace) and are empty otherwise.
type folderRoute struct {
	kind      folderRouteKind
	pane      uint32
	workspace string
	wsName    string
}

// openRecentFolder is what a "Recent folders…" row does with its folder:
// route it through cats' workspaces at Tier 1, re-root this editor
// otherwise.
func (a *App) openRecentFolder(dir string) {
	if a.catsTier1() {
		a.catsGoToFolder(dir)
		return
	}
	a.requestOpenFolder(dir)
}

// catsGoToFolder resolves and runs the ladder for dir off the main loop.
// The executable is looked up first, on the loop, because every spawning
// rung needs it and a failure there is worth saying before any round trip.
func (a *App) catsGoToFolder(dir string) {
	exe, err := catsExecutable()
	if err != nil {
		a.flash("Recent folder: cannot find this ced binary: " + err.Error())
		return
	}
	client, scr := a.cats.client, a.screen
	self, selfOK := a.cats.self, a.cats.selfOK
	dir = session.Normalize(dir)
	params := catsProjectTabParams(exe, dir)
	shown := displayPath(dir)
	go func() {
		panes, err := client.PaneList()
		if err != nil {
			catsPostNotice(scr, "Recent folder: cats did not answer: "+err.Error())
			return
		}
		// A failed workspace.list costs the strong rule and the
		// asleep/locked checks, not the feature: pane evidence alone still
		// finds editors and custom-named workspaces.
		wss, _ := client.WorkspaceList()
		route := planFolderRoute(dir, panes, wss, self, selfOK)
		catsPostNotice(scr, runFolderRoute(client, route, params, shown))
	}()
}

// runFolderRoute performs a resolved plan and returns the notice to show.
// Socket IO only — it runs on catsGoToFolder's goroutine.
func runFolderRoute(client *cats.Client, r folderRoute, params cats.TabCreateParams, shown string) string {
	switch r.kind {
	case routeFocusEditor:
		if err := catsRevealPane(client, r.pane); err != nil {
			return "Switch to ced failed: " + err.Error()
		}
		return "Switched to the ced on " + shown + inWorkspaceNote(r.wsName)
	case routeSpawnInWorkspace:
		params.Workspace = r.workspace
		res, err := client.TabCreate(params)
		if err != nil {
			return "New tab failed: " + err.Error()
		}
		// tab.create into another workspace does not move the viewport,
		// so the reveal is what actually takes the user there. A zero pane
		// is an older cats that answered with no data: the tab exists, we
		// just cannot point at it.
		if res.Pane != 0 {
			_ = catsRevealPane(client, res.Pane)
		}
		return "Opened " + shown + " in a new ced tab" + inWorkspaceNote(r.wsName)
	default:
		// tab.create in the active workspace focuses the new tab itself.
		if _, err := client.TabCreate(params); err != nil {
			return "New tab failed: " + err.Error()
		}
		return "Opened " + shown + " in a new cats tab"
	}
}

// catsRevealPane brings a pane into view across workspaces and tabs
// (agent.focus), falling back to pane.focus for a cats that predates it —
// which still focuses the pane, if not its tab.
func catsRevealPane(client *cats.Client, pane uint32) error {
	if err := client.AgentFocus(pane); err == nil {
		return nil
	}
	return client.PaneFocus(pane)
}

// inWorkspaceNote is the notice's " (workspace X)" tail, empty when the
// workspace's name is unknown.
func inWorkspaceNote(name string) string {
	if name == "" {
		return ""
	}
	return " (workspace " + name + ")"
}

// planFolderRoute is the ladder's decision, pure over the two listings so
// every rung is testable without a socket. dir must already be normalized;
// pane cwds are normalized here (each once — Normalize resolves symlinks,
// which is a syscall per path).
func planFolderRoute(dir string, panes []cats.PaneInfo, wss []cats.WorkspaceEntry, self uint32, selfOK bool) folderRoute {
	// Our own host, read off our own row. Unknown (no self, or a cats that
	// does not send hosts) disables the filter rather than every match.
	myHost, hostKnown := "", false
	if selfOK {
		for _, p := range panes {
			if p.Pane == self {
				myHost, hostKnown = p.Host, true
				break
			}
		}
	}
	byID := make(map[string]cats.WorkspaceEntry, len(wss))
	for _, w := range wss {
		byID[w.ID] = w
	}
	label := folderLabel(dir)
	under := dir + string(filepath.Separator)

	// Rank each awake workspace by its best evidence: 3 exact identity,
	// 2 strong, 1 weak. order keeps first-seen order for the tie-break.
	rank := map[string]int{}
	var order []string
	note := func(ws string, r int) {
		if _, seen := rank[ws]; !seen {
			order = append(order, ws)
		}
		rank[ws] = max(rank[ws], r)
	}
	for _, w := range wss {
		if w.Asleep || w.Cwd == "" {
			continue
		}
		// A workspace pinned to another cathost names a folder on that
		// machine. Its Host is the STORED policy ("" = the default host),
		// so only a named, different host is known to be elsewhere.
		if hostKnown && w.Host != "" && w.Host != myHost {
			continue
		}
		if session.Normalize(w.Cwd) == dir {
			note(w.ID, 3)
		}
	}
	var editors []cats.PaneInfo
	for _, p := range panes {
		if hostKnown && p.Host != myHost {
			continue
		}
		ws := paneWorkspaceID(p.Handle)
		entry, listed := byID[ws]
		if ws == "" || entry.Asleep {
			continue
		}
		cwd := session.Normalize(p.Cwd)
		if cwd == "" {
			continue
		}
		exact := cwd == dir
		inside := exact || strings.HasPrefix(cwd, under)
		r := 0
		switch {
		case listed && inside && entry.Name == label:
			r = 2
		case exact:
			r = 1
		}
		if r > 0 {
			note(ws, r)
		}
		if exact && strings.EqualFold(p.Agent, "ced") && !(selfOK && p.Pane == self) {
			editors = append(editors, p)
		}
	}
	best := ""
	for _, ws := range order {
		if best == "" || rank[ws] > rank[best] {
			best = ws
		}
	}

	if len(editors) > 0 {
		pick := editors[0]
		for _, e := range editors[1:] {
			eIn := paneWorkspaceID(e.Handle) == best
			pIn := paneWorkspaceID(pick.Handle) == best
			if (eIn && !pIn) || (eIn == pIn && e.Pane < pick.Pane) {
				pick = e
			}
		}
		ws := paneWorkspaceID(pick.Handle)
		return folderRoute{kind: routeFocusEditor, pane: pick.Pane, workspace: ws, wsName: byID[ws].Name}
	}
	// A locked workspace refuses tab.create; rather than fail, it counts as
	// "nowhere to go" and the editor opens here.
	if best != "" && !byID[best].Locked {
		return folderRoute{kind: routeSpawnInWorkspace, workspace: best, wsName: byID[best].Name}
	}
	return folderRoute{kind: routeSpawnHere}
}

// paneWorkspaceID extracts the workspace handle from a public pane handle
// ("w2:p3" → "w2"). The "p_<n>" form a pane wears before it has a public id
// names no workspace and answers "".
func paneWorkspaceID(handle string) string {
	ws, _, ok := strings.Cut(handle, ":")
	if !ok || !strings.HasPrefix(ws, "w") {
		return ""
	}
	return ws
}

// folderLabel is the name cats auto-derives for a workspace rooted at dir:
// "~" for the home directory, the basename otherwise.
func folderLabel(dir string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && session.Normalize(home) == dir {
		return "~"
	}
	return filepath.Base(dir)
}

// catsProjectTabParams is the tab.create for "an independent ced on dir":
// an argv, so no shell sits in the middle and nothing needs quoting, and a
// title naming the project, because a strip of tabs all labeled "ced" is a
// strip of tabs with no information in it. Shared by the project-tab row
// and the recent-folder ladder so the two spawn the same editor.
func catsProjectTabParams(exe, dir string) cats.TabCreateParams {
	return cats.TabCreateParams{
		Title:   filepath.Base(dir),
		Cwd:     dir,
		Command: []string{exe, "--root", dir},
	}
}
