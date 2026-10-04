// =============================================================================
// File: internal/app/conflictpanel_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// conflictPanelApp roots an App in a real parked cherry-pick and opens
// the panel on it.
func conflictPanelApp(t *testing.T) (*App, string) {
	t.Helper()
	repo := cherryPickConflictRepo(t)
	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	a.showTool(toolConflicts)
	return a, repo
}

// findBtn returns the button with label among btns.
func findBtn(t *testing.T, btns []conflictBtn, label string) conflictBtn {
	t.Helper()
	for _, b := range btns {
		if b.label == label {
			return b
		}
	}
	labels := make([]string, len(btns))
	for i, b := range btns {
		labels[i] = b.label
	}
	t.Fatalf("no %q button among %v", label, labels)
	return conflictBtn{}
}

// TestConflictPanel_ReadsTheStop pins what the panel shows for a real
// stopped cherry-pick: the operation line, the sides line, one unmerged
// file with its block count, and the header count.
func TestConflictPanel_ReadsTheStop(t *testing.T) {
	a, _ := conflictPanelApp(t)
	if got := a.conflictOpTitle(); !strings.HasPrefix(got, "Cherry-pick · ") || !strings.Contains(got, "“side edits the middle”") {
		t.Errorf("op line = %q", got)
	}
	if got := a.conflictSidesNote(); !strings.Contains(got, "current = main (HEAD)") {
		t.Errorf("sides = %q", got)
	}
	rows := a.conflictRows()
	if len(rows) != 1 || rows[0].file.rel != "f.txt" {
		t.Fatalf("rows = %+v", rows)
	}
	if n := a.conflictPanel.counts[rows[0].file.abs]; n != 1 {
		t.Errorf("block count = %d, want 1", n)
	}
	if got := a.toolHeaderTitle(toolConflicts); got != " Conflicts · 1 left " {
		t.Errorf("header = %q", got)
	}
}

// TestConflictPanel_ContinueExplainsItself pins the dimmed Continue: while
// a file still has markers, a press flashes which file is in the way and
// runs nothing.
func TestConflictPanel_ContinueExplainsItself(t *testing.T) {
	a, _ := conflictPanelApp(t)
	b := findBtn(t, a.conflictOpButtons(), "[ Continue ]")
	if b.enabled {
		t.Fatal("Continue is live with an unresolved file")
	}
	a.pressConflictBtn(b)
	if !strings.Contains(a.statusMsg, "f.txt") {
		t.Errorf("flash = %q, want the blocking file named", a.statusMsg)
	}
	findBtn(t, a.conflictOpButtons(), "[ Skip ]")
	findBtn(t, a.conflictOpButtons(), "[ Abort ]")
}

// TestConflictPanel_RowButtonsFollowTheBuffer pins the buffer-first
// counts: the row offers All current / All incoming while the open tab has
// a block, and Mark resolved the moment the buffer's last block goes —
// before any save.
func TestConflictPanel_RowButtonsFollowTheBuffer(t *testing.T) {
	a, _ := conflictPanelApp(t)
	findBtn(t, a.conflictRowButtons(0), "[ All incoming ]")
	a.pressConflictBtn(findBtn(t, a.conflictRowButtons(0), "[ All incoming ]"))
	tab := a.activeTabPtr()
	if tab == nil || !tab.Dirty || strings.Contains(tab.Buffer.String(), "<<<<<<<") {
		t.Fatalf("All incoming did not resolve the open buffer: %v", tab)
	}
	findBtn(t, a.conflictRowButtons(0), "[ Mark resolved ]")
	if b := findBtn(t, a.conflictOpButtons(), "[ Resolve all & continue ]"); !b.enabled {
		t.Error("Continue did not become Resolve all & continue with every file ready")
	}
}

// TestConflictPanel_EndToEnd walks the whole happy path through the
// panel's own buttons against real git: settle the block, mark the file
// resolved (which saves the dirty buffer first), continue, and watch the
// panel put itself away with the commit landed.
func TestConflictPanel_EndToEnd(t *testing.T) {
	a, repo := conflictPanelApp(t)
	a.pressConflictBtn(findBtn(t, a.conflictRowButtons(0), "[ All incoming ]"))
	a.pressConflictBtn(findBtn(t, a.conflictRowButtons(0), "[ Mark resolved ]"))
	if tab := a.activeTabPtr(); tab.Dirty {
		t.Fatal("Mark resolved staged without saving the buffer first")
	}
	pumpAppEvents(t, a, func() bool { return len(a.gitConflicted) == 0 })
	rows := a.conflictRows()
	if len(rows) != 1 || !rows[0].done {
		t.Fatalf("rows after staging = %+v, want f.txt shown as marked resolved", rows)
	}
	b := findBtn(t, a.conflictOpButtons(), "[ Continue ]")
	if !b.enabled {
		t.Fatalf("Continue still dimmed: %s", b.why)
	}
	a.pressConflictBtn(b)
	pumpAppEvents(t, a, func() bool { return a.gitOp == "" })
	if a.conflictPanel.open {
		t.Error("the panel stayed up after the operation finished")
	}
	if !strings.Contains(a.statusMsg, "Cherry-pick finished") {
		t.Errorf("flash = %q", a.statusMsg)
	}
	if got := gitOut(t, repo, "log", "-1", "--format=%s"); got != "side edits the middle" {
		t.Errorf("HEAD subject = %q, want the picked commit", got)
	}
	if got := gitOut(t, repo, "show", "HEAD:f.txt"); got != "one\nSIDE\nthree" {
		t.Errorf("committed f.txt = %q, want the incoming side", got)
	}
}

// TestConflictPanel_ContinueIntoTheNextConflict is the bug the panel's
// hooks fix: in a multi-commit pick, a continue that stops on the NEXT
// commit's conflict used to land in the error modal. It must raise the
// panel again on the new stop instead.
func TestConflictPanel_ContinueIntoTheNextConflict(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\ntwo\nthree\n", "base")
	gitRun(t, repo, "checkout", "-q", "-b", "side")
	writeCommit(t, repo, "f.txt", "one\nSIDE\nthree\n", "s1")
	writeCommit(t, repo, "f.txt", "one\nSIDE2\nthree\n", "s2")
	gitRun(t, repo, "checkout", "-q", "main")
	writeCommit(t, repo, "f.txt", "one\nMAIN\nthree\n", "main")
	gitRunAllowFail(t, repo, "cherry-pick", "side~1", "side")
	// Resolve s1 to something s2 will conflict with again.
	writeFileT(t, filepath.Join(repo, "f.txt"), "one\nRESOLVED\nthree\n")

	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	a.showTool(toolConflicts)
	if got := a.conflictOpTitle(); !strings.HasPrefix(got, "Cherry-pick 1 of 2") {
		t.Fatalf("op line = %q", got)
	}
	a.pressConflictBtn(findBtn(t, a.conflictOpButtons(), "[ Resolve all & continue ]"))
	pumpAppEvents(t, a, func() bool { return strings.HasPrefix(a.conflictOpTitle(), "Cherry-pick 2 of 2") })
	if a.modal != nil {
		t.Errorf("the second stop opened %T instead of staying in the panel", a.modal)
	}
	if !a.conflictPanel.open || len(a.conflictPanel.files) != 1 {
		t.Errorf("panel open=%v files=%+v after the second stop", a.conflictPanel.open, a.conflictPanel.files)
	}
}

// TestConflictPanel_PresenceConflict pins modify/delete handling: the row
// offers Keep and Delete (never Mark resolved), it holds Continue back,
// and Keep settles it.
func TestConflictPanel_PresenceConflict(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "g.txt", "keep\n", "base")
	gitRun(t, repo, "checkout", "-q", "-b", "side")
	gitRun(t, repo, "rm", "-q", "g.txt")
	gitRun(t, repo, "commit", "-q", "-m", "side deletes g")
	gitRun(t, repo, "checkout", "-q", "main")
	writeCommit(t, repo, "g.txt", "keep\nmore\n", "main edits g")
	gitRunAllowFail(t, repo, "cherry-pick", "side")

	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	a.showTool(toolConflicts)
	if len(a.conflictPanel.files) != 1 || a.conflictPanel.files[0].xy != "UD" {
		t.Fatalf("files = %+v, want g.txt deleted in incoming", a.conflictPanel.files)
	}
	_, _, detail := a.conflictRowState(a.conflictRows()[0])
	if detail != "deleted in incoming" {
		t.Errorf("detail = %q", detail)
	}
	if b := findBtn(t, a.conflictOpButtons(), "[ Continue ]"); b.enabled {
		t.Error("Continue is live while a keep/delete decision is pending")
	}
	findBtn(t, a.conflictRowButtons(0), "[ Delete ]")
	a.pressConflictBtn(findBtn(t, a.conflictRowButtons(0), "[ Keep ]"))
	pumpAppEvents(t, a, func() bool { return len(a.gitConflicted) == 0 })
	// Keep means the work tree's file survives into the index unchanged.
	if got := gitOut(t, repo, "show", ":g.txt"); got != "keep\nmore" {
		t.Errorf("index g.txt = %q, want main's version kept", got)
	}
}

// TestConflictPanel_EmptyPick pins the "already applied" stop: nothing
// unmerged, nothing staged — the empty state names Skip, and Continue is
// dimmed with that reason.
func TestConflictPanel_EmptyPick(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\n", "base")
	gitRun(t, repo, "checkout", "-q", "-b", "side")
	writeCommit(t, repo, "f.txt", "two\n", "side change")
	gitRun(t, repo, "checkout", "-q", "main")
	gitRun(t, repo, "cherry-pick", "side")
	gitRunAllowFail(t, repo, "cherry-pick", "side") // again: now empty

	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	a.showTool(toolConflicts)
	if !a.conflictPanel.empty {
		t.Fatal("an empty pick was not recognised")
	}
	if got := a.conflictEmptyText(); !strings.Contains(got, "Skip") {
		t.Errorf("empty text = %q", got)
	}
	b := findBtn(t, a.conflictOpButtons(), "[ Continue ]")
	if b.enabled || !strings.Contains(b.why, "Skip") {
		t.Errorf("Continue enabled=%v why=%q", b.enabled, b.why)
	}
}

// TestConflictPanel_SkipConfirmsAndMovesOn pins Skip: a confirm naming
// the commit, then the operation is over (a single-commit pick has
// nothing after it).
func TestConflictPanel_SkipConfirmsAndMovesOn(t *testing.T) {
	a, _ := conflictPanelApp(t)
	a.pressConflictBtn(findBtn(t, a.conflictOpButtons(), "[ Skip ]"))
	m, ok := a.modal.(*confirmModal)
	if !ok {
		t.Fatalf("Skip opened %T, want a confirm", a.modal)
	}
	if body := strings.Join(m.lines, " "); !strings.Contains(body, "side edits the middle") {
		t.Errorf("confirm = %q, want the commit named", body)
	}
	m.callback(a)
	pumpAppEvents(t, a, func() bool { return a.gitOp == "" })
	if a.conflictPanel.open {
		t.Error("the panel stayed up after skipping the only commit")
	}
}

// TestConflictPanel_Draw is the smoke test on what is painted: the
// operation line, the file row and its buttons.
func TestConflictPanel_Draw(t *testing.T) {
	a, _ := conflictPanelApp(t)
	a.draw()
	px, py, pw, _ := a.conflictPanelBody()
	op := screenRow(t, a, py, px, pw)
	if !strings.Contains(op, "⚠ Cherry-pick") || !strings.Contains(op, "[ Abort ]") {
		t.Errorf("op row = %q", op)
	}
	row := screenRow(t, a, py+conflictPanelHeadRows, px, pw)
	for _, want := range []string{"f.txt", "1 conflict · both modified", "[ All current ]"} {
		if !strings.Contains(row, want) {
			t.Errorf("file row = %q, missing %q", row, want)
		}
	}
}

// TestConflictPanel_RowPressOpensTheFile pins the row click: the file
// opens at its first block.
func TestConflictPanel_RowPressOpensTheFile(t *testing.T) {
	a, repo := conflictPanelApp(t)
	a.draw()
	px, py, _, _ := a.conflictPanelBody()
	a.conflictPanelPress(px+4, py+conflictPanelHeadRows)
	tab := a.activeTabPtr()
	if tab == nil || tab.Path != filepath.Join(repo, "f.txt") {
		t.Fatalf("active tab = %v", tab)
	}
	if !strings.HasPrefix(tab.Buffer.Lines[tab.Cursor.Line], "<<<<<<<") {
		t.Errorf("caret on %q, want the opener", tab.Buffer.Lines[tab.Cursor.Line])
	}
}

// TestConflictPanel_ContextItems pins the right-click vocabulary for a
// content conflict, including the two whole-file takes.
func TestConflictPanel_ContextItems(t *testing.T) {
	a, _ := conflictPanelApp(t)
	var labels []string
	for _, it := range a.conflictRowContextItems(a.conflictRows()[0]) {
		labels = append(labels, it.label)
	}
	got := strings.Join(labels, "|")
	for _, want := range []string{"Open f.txt", "Accept all incoming", "Mark resolved",
		"Use current version of the whole file…", "Copy path"} {
		if !strings.Contains(got, want) {
			t.Errorf("rows %q missing %q", got, want)
		}
	}
}

// TestLayoutButtonsRight pins the placement (right-aligned, one cell
// apart) and the shedding order on a narrow row.
func TestLayoutButtonsRight(t *testing.T) {
	btns := []conflictBtn{{label: "[ A ]"}, {label: "[ BB ]"}, {label: "⟳"}}
	got := layoutButtonsRight(append([]conflictBtn(nil), btns...), []string{"⟳"}, 0, 100, 3)
	if len(got) != 3 || got[2].rect.x+got[2].rect.w != 100 || got[1].rect.x != got[0].rect.x+6 {
		t.Errorf("placed = %+v", got)
	}
	// 5+1+6 = 12 cells of buttons; a 30-cell row keeps 16 for text only
	// once ⟳ is shed.
	got = layoutButtonsRight(append([]conflictBtn(nil), btns...), []string{"⟳"}, 0, 29, 3)
	if len(got) != 2 {
		t.Errorf("narrow row kept %d buttons, want ⟳ shed", len(got))
	}
	if got := layoutButtonsRight(append([]conflictBtn(nil), btns...), nil, 0, 10, 3); got != nil {
		t.Errorf("no room kept %+v", got)
	}
}

// TestConflictStatusSegment pins the three wordings and silence on a
// clean repo.
func TestConflictStatusSegment(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if s := a.conflictStatusSegment(); s != "" {
		t.Errorf("clean repo: %q", s)
	}
	a.gitOp = "cherry-pick"
	if s := a.conflictStatusSegment(); s != "⚠ cherry-pick in progress" {
		t.Errorf("parked, nothing unmerged: %q", s)
	}
	a.gitConflicted = map[string]bool{"/x": true, "/y": true}
	if s := a.conflictStatusSegment(); s != "⚠ cherry-pick: 2 conflicts" {
		t.Errorf("parked with conflicts: %q", s)
	}
	a.gitOp = ""
	if s := a.conflictStatusSegment(); s != "⚠ 2 conflicted files" {
		t.Errorf("conflicts with no operation: %q", s)
	}
}

// TestConflictPanel_UnseenClearedByInput pins the cats mark's lifetime:
// it survives until the user's next key.
func TestConflictPanel_UnseenClearedByInput(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.conflictPanel.unseen = "cherry-pick stopped on conflicts"
	if state, status := a.catsSelfState(); state != "blocked" || status != a.conflictPanel.unseen {
		t.Fatalf("cats = %q %q", state, status)
	}
	a.handleEvent(keyEvent(tcell.KeyRune, 'x'))
	if a.conflictPanel.unseen != "" {
		t.Error("a key press did not clear the unseen mark")
	}
}

// TestConflictPanel_CountsReadTheLiveBuffer is the regression for a stale
// row: a buffer changed by any path other than the panel's own verbs (a
// hand edit, an Undo, a reload) must change the row in the same frame.
func TestConflictPanel_CountsReadTheLiveBuffer(t *testing.T) {
	a, repo := conflictPanelApp(t)
	a.openFile(filepath.Join(repo, "f.txt"))
	tab := a.activeTabPtr()
	tab.ResolveAllConflicts(editor.ChooseIncoming) // behind the panel's back
	findBtn(t, a.conflictRowButtons(0), "[ Mark resolved ]")
	tab.Undo()
	findBtn(t, a.conflictRowButtons(0), "[ All current ]")
}

// TestConflictPanel_ContinueIntoTheNextConflict_OpenTab is the same stop
// as TestConflictPanel_ContinueIntoTheNextConflict with the file OPEN and
// resolved in its buffer — the shape a real session has. After the stop
// the tab holds the next commit's markers, the row counts them, and the
// caret sits on the new opener.
func TestConflictPanel_ContinueIntoTheNextConflict_OpenTab(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\ntwo\nthree\n", "base")
	gitRun(t, repo, "checkout", "-q", "-b", "side")
	writeCommit(t, repo, "f.txt", "one\nSIDE\nthree\n", "s1")
	writeCommit(t, repo, "f.txt", "one\nSIDE2\nthree\n", "s2")
	gitRun(t, repo, "checkout", "-q", "main")
	writeCommit(t, repo, "f.txt", "one\nMAIN\nthree\n", "main")
	gitRunAllowFail(t, repo, "cherry-pick", "side~1", "side")

	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	a.showTool(toolConflicts)
	path := filepath.Join(repo, "f.txt")
	a.openFile(path)
	a.activeTabPtr().ResolveAllConflicts(editor.ChooseBoth) // s2 will conflict with this
	a.pressConflictBtn(findBtn(t, a.conflictOpButtons(), "[ Resolve all & continue ]"))
	pumpAppEvents(t, a, func() bool { return strings.HasPrefix(a.conflictOpTitle(), "Cherry-pick 2 of 2") })

	tab := a.activeTabPtr()
	if tab.Path != path || tab.Dirty {
		t.Fatalf("tab %s dirty=%v", tab.Path, tab.Dirty)
	}
	if n := a.conflictFileCount(path); n != 1 {
		t.Errorf("row count = %d, want the new stop's 1 block", n)
	}
	if !strings.HasPrefix(tab.Buffer.Lines[tab.Cursor.Line], "<<<<<<<") {
		t.Errorf("caret on %q, want the new block's opener", tab.Buffer.Lines[tab.Cursor.Line])
	}
	// s1 was committed with exactly what the buffer said.
	if got := gitOut(t, repo, "show", "HEAD:f.txt"); got != "one\nMAIN\nSIDE\nthree" {
		t.Errorf("committed s1 = %q", got)
	}
}

// TestSaveForStaging_WritesTheBufferVerbatim pins the staging save: the
// disk holds exactly the buffer and the tab is clean.
func TestSaveForStaging_WritesTheBufferVerbatim(t *testing.T) {
	a, paths := conflictTestApp(t, "a.go")
	tab := a.activeTabPtr()
	tab.ResolveAllConflicts(editor.ChooseIncoming)
	if !a.saveForStaging(tab) {
		t.Fatal("save refused")
	}
	if tab.Dirty {
		t.Error("tab still dirty")
	}
	if got := readFileT(t, paths[0]); got != tab.Buffer.String() {
		t.Errorf("disk = %q, buffer = %q", got, tab.Buffer.String())
	}
}
