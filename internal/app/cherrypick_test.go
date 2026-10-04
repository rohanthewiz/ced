// =============================================================================
// File: internal/app/cherrypick_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// cherryPickSourceRepo builds main plus a `topic` branch holding three
// commits: t1 (already cherry-picked onto main, so patch-equivalent), t2
// rewriting the line main also rewrote, and t3 a clean new file.
//
// Main moves BEFORE t1 is picked onto it. Picked straight onto t1's own
// parent, in the same second, by the same committer, a cherry-pick
// reproduces t1 byte for byte — the SAME hash — and git then rightly
// leaves it out of HEAD...topic altogether instead of marking it `=`.
func cherryPickSourceRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\ntwo\nthree\n", "base")
	writeCommit(t, repo, "shared.txt", "s\n", "shared base")
	gitRun(t, repo, "checkout", "-q", "-b", "topic")
	writeCommit(t, repo, "t1.txt", "t1\n", "t1 new file")
	writeCommit(t, repo, "shared.txt", "TOPIC\n", "t2 edits shared")
	writeCommit(t, repo, "t3.txt", "t3\n", "t3 another file")
	gitRun(t, repo, "checkout", "-q", "main")
	writeCommit(t, repo, "shared.txt", "MAIN\n", "main edits shared")
	gitRun(t, repo, "cherry-pick", "topic~2") // t1, now on main under a new hash
	return repo
}

// openLoadedCherryPick opens the dialog on source and pumps until its
// commits have arrived.
func openLoadedCherryPick(t *testing.T, a *App, source string) *cherryPickModal {
	t.Helper()
	a.openCherryPick(source)
	m := a.modal.(*cherryPickModal)
	pumpAppEvents(t, a, func() bool { return !m.loading })
	return m
}

// TestParseCherryPickLog pins the record format: the cherry mark, every
// field, and the --name-only file list per commit.
func TestParseCherryPickLog(t *testing.T) {
	out := "\x1e+\x1fH1\x1fh1\x1fAnn\x1f2 days ago\x1fFix it\n\na.go\nb.go\n" +
		"\x1e=\x1fH2\x1fh2\x1fBob\x1f3 days ago\x1fBump\n\nv.txt\n"
	got := parseCherryPickLog([]byte(out))
	want := []cherryPickCommit{
		{hash: "H1", short: "h1", author: "Ann", age: "2 days ago", subject: "Fix it", files: []string{"a.go", "b.go"}},
		{hash: "H2", short: "h2", author: "Bob", age: "3 days ago", subject: "Bump", applied: true, files: []string{"v.txt"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// TestParseCherryPickSources pins the branch list: symbolic refs and the
// current branch dropped, order kept.
func TestParseCherryPickSources(t *testing.T) {
	out := "topic\t\nmain\t\norigin/HEAD\trefs/remotes/origin/main\norigin/topic\t\n"
	if got := parseCherryPickSources([]byte(out), "main"); !reflect.DeepEqual(got, []string{"topic", "origin/topic"}) {
		t.Errorf("got %v", got)
	}
}

// TestCherryPickArgs pins the argv: -x only when committing, --no-commit
// otherwise, hashes in the order given.
func TestCherryPickArgs(t *testing.T) {
	cases := []struct {
		x, nc bool
		want  []string
	}{
		{false, false, []string{"cherry-pick", "a", "b"}},
		{true, false, []string{"cherry-pick", "-x", "a", "b"}},
		{true, true, []string{"cherry-pick", "--no-commit", "a", "b"}},
	}
	for _, c := range cases {
		if got := cherryPickArgs([]string{"a", "b"}, c.x, c.nc); !reflect.DeepEqual(got, c.want) {
			t.Errorf("x=%v nc=%v: %v", c.x, c.nc, got)
		}
	}
}

// TestCherryPickModal_TickedInOrderIsOldestFirst pins the application
// order: the list is newest first, the command is oldest first, whatever
// order the ticks went on in.
func TestCherryPickModal_TickedInOrderIsOldestFirst(t *testing.T) {
	m := &cherryPickModal{ticked: map[string]bool{}, commits: []cherryPickCommit{
		{hash: "new"}, {hash: "mid"}, {hash: "old"},
	}}
	m.ticked["new"], m.ticked["old"] = true, true
	if got := m.tickedInOrder(); !reflect.DeepEqual(got, []string{"old", "new"}) {
		t.Errorf("order = %v", got)
	}
}

// TestCherryPickModal_ToggleRules pins the tick rules: an applied commit
// refuses with a reason, select-all skips applied commits, and pressed
// again it clears.
func TestCherryPickModal_ToggleRules(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := &cherryPickModal{target: "main", ticked: map[string]bool{}, commits: []cherryPickCommit{
		{hash: "a", short: "a"}, {hash: "b", short: "b", applied: true}, {hash: "c", short: "c"},
	}}
	m.toggle(a, 1)
	if len(m.ticked) != 0 || !strings.Contains(a.statusMsg, "already on main") {
		t.Errorf("applied commit ticked=%v flash=%q", m.ticked, a.statusMsg)
	}
	m.toggleAll()
	if len(m.ticked) != 2 || m.ticked["b"] || m.allLabel() != "[ Select none ]" {
		t.Errorf("select all = %v (%s)", m.ticked, m.allLabel())
	}
	m.toggleAll()
	if len(m.ticked) != 0 {
		t.Errorf("second press left %v", m.ticked)
	}
}

// TestListCherryPickCommits_RealRepo pins the git side: the
// patch-equivalent commit marked applied, the overlap hint on the commit
// touching a file main also changed, and newest-first order.
func TestListCherryPickCommits_RealRepo(t *testing.T) {
	repo := cherryPickSourceRepo(t)
	commits, capped, err := listCherryPickCommits(repo, "topic")
	if err != nil || capped || len(commits) != 3 {
		t.Fatalf("commits=%+v capped=%v err=%v", commits, capped, err)
	}
	subjects := []string{commits[0].subject, commits[1].subject, commits[2].subject}
	if !reflect.DeepEqual(subjects, []string{"t3 another file", "t2 edits shared", "t1 new file"}) {
		t.Errorf("order = %v", subjects)
	}
	if !commits[2].applied || commits[0].applied || commits[1].applied {
		t.Errorf("applied marks = %v %v %v, want only t1", commits[0].applied, commits[1].applied, commits[2].applied)
	}
	if !reflect.DeepEqual(commits[1].overlap, []string{"shared.txt"}) || len(commits[0].overlap) != 0 {
		t.Errorf("overlap t2=%v t3=%v", commits[1].overlap, commits[0].overlap)
	}
}

// TestCherryPick_EndToEnd ticks two commits, picks them with -x, and
// checks git: both landed, oldest first, each noting its origin.
func TestCherryPick_EndToEnd(t *testing.T) {
	repo := cherryPickSourceRepo(t)
	// t2 would conflict with main's edit; pick t3 alone plus re-check the
	// order with a second clean commit added on top.
	gitRun(t, repo, "checkout", "-q", "topic")
	writeCommit(t, repo, "t4.txt", "t4\n", "t4 newest")
	gitRun(t, repo, "checkout", "-q", "main")

	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	m := openLoadedCherryPick(t, a, "topic")
	for _, c := range m.commits {
		if c.subject == "t3 another file" || c.subject == "t4 newest" {
			m.ticked[c.hash] = true
		}
	}
	m.recordOrigin = true
	if m.submitLabel() != "[ Cherry-pick 2 ]" {
		t.Errorf("submit label = %q", m.submitLabel())
	}
	m.submit(a)
	if a.modal != nil {
		t.Fatalf("dialog still open after submit: %T", a.modal)
	}
	pumpAppEvents(t, a, func() bool { return strings.HasPrefix(a.statusMsg, "Cherry-picked") })
	if got := gitOut(t, repo, "log", "-2", "--format=%s"); got != "t4 newest\nt3 another file" {
		t.Errorf("log = %q, want t3 then t4 applied oldest first", got)
	}
	if body := gitOut(t, repo, "log", "-1", "--format=%b"); !strings.Contains(body, "cherry picked from commit") {
		t.Errorf("-x note missing: %q", body)
	}
}

// TestCherryPick_ConflictRaisesThePanel pins the hand-off: a pick that
// stops on conflicts lands in the Conflicts panel, not an error modal.
func TestCherryPick_ConflictRaisesThePanel(t *testing.T) {
	repo := cherryPickSourceRepo(t)
	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	m := openLoadedCherryPick(t, a, "topic")
	for i, c := range m.commits {
		if c.subject == "t2 edits shared" {
			m.cursor = i // Enter with nothing ticked picks the highlighted row
		}
	}
	m.handleKey(a, keyEvent(tcell.KeyEnter, 0))
	pumpAppEvents(t, a, func() bool { return a.conflictPanel.open })
	if a.modal != nil {
		t.Errorf("a conflicted pick opened %T", a.modal)
	}
	if len(a.conflictPanel.files) != 1 || a.conflictPanel.files[0].rel != "shared.txt" {
		t.Errorf("panel files = %+v", a.conflictPanel.files)
	}
}

// TestCherryPickModal_KeysAndDraw pins the Alt twins of the buttons, Space,
// Esc, and that the frame paints the title, summary and rows.
func TestCherryPickModal_KeysAndDraw(t *testing.T) {
	repo := cherryPickSourceRepo(t)
	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	m := openLoadedCherryPick(t, a, "topic")
	if m.commits[m.cursor].applied {
		t.Fatal("the initial highlight sits on an applied commit")
	}
	alt := func(r rune) *tcell.EventKey { return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModAlt) }
	m.handleKey(a, alt('x'))
	m.handleKey(a, alt('s'))
	if !m.recordOrigin || !m.noCommit {
		t.Errorf("alt+x / alt+s: origin=%v stage=%v", m.recordOrigin, m.noCommit)
	}
	m.handleKey(a, keyEvent(tcell.KeyRune, ' '))
	if len(m.ticked) != 1 {
		t.Errorf("Space ticked %d", len(m.ticked))
	}

	a.draw()
	mx, my, mw, _ := m.rect(a)
	if title := screenRow(t, a, my+1, mx, mw); !strings.Contains(title, "Cherry-pick onto main") {
		t.Errorf("title row = %q", title)
	}
	_, sumY, listTop, _, _, _, _ := m.rowY(a)
	if sum := screenRow(t, a, sumY, mx, mw); !strings.Contains(sum, "3 commits not on main · 1 already applied") {
		t.Errorf("summary = %q", sum)
	}
	if row := screenRow(t, a, listTop+2, mx, mw); !strings.Contains(row, "[=]") || !strings.Contains(row, "already on main") {
		t.Errorf("applied row = %q", row)
	}

	m.handleKey(a, keyEvent(tcell.KeyEsc, 0))
	if a.modal != nil {
		t.Error("Esc left the dialog open")
	}
}

// TestCherryPickModal_MouseTicksTheBox pins the press rules: a press on
// the box ticks, a press elsewhere on a row only highlights, a held
// button does not repeat, and a press outside dismisses.
func TestCherryPickModal_MouseTicksTheBox(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := &cherryPickModal{target: "main", ticked: map[string]bool{}, lastClickRow: -1,
		commits: []cherryPickCommit{{hash: "a", short: "a"}, {hash: "b", short: "b"}}}
	a.openModal(m)
	mx, _, _, _ := m.rect(a)
	_, _, listTop, _, _, _, _ := m.rowY(a)

	m.handleMouse(a, mx+3, listTop+1, tcell.Button1)
	if !m.ticked["b"] {
		t.Fatal("a press on the box did not tick")
	}
	m.handleMouse(a, mx+3, listTop+1, tcell.Button1) // still held: no repeat
	if !m.ticked["b"] {
		t.Error("a held button toggled again")
	}
	m.handleMouse(a, mx+3, listTop+1, tcell.ButtonNone)
	m.handleMouse(a, mx+20, listTop, tcell.Button1)
	if m.cursor != 0 || m.ticked["a"] {
		t.Errorf("a press on the subject: cursor=%d ticked=%v, want highlight only", m.cursor, m.ticked)
	}
	m.handleMouse(a, 0, 0, tcell.ButtonNone)
	m.handleMouse(a, 0, 0, tcell.Button1)
	if a.modal != nil {
		t.Error("a press outside did not dismiss")
	}
}

// TestHandleCherryPickLoad_DropsStaleAnswers pins the sequence check: an
// answer for a load the dialog has moved past is ignored.
func TestHandleCherryPickLoad_DropsStaleAnswers(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := &cherryPickModal{seq: 7, loading: true, ticked: map[string]bool{}}
	a.openModal(m)
	a.handleCherryPickLoad(&cherryPickLoadEvent{seq: 6, commits: []cherryPickCommit{{hash: "x"}}})
	if !m.loading || len(m.commits) != 0 {
		t.Error("a stale load landed")
	}
	a.handleCherryPickLoad(&cherryPickLoadEvent{seq: 7, commits: []cherryPickCommit{{hash: "x"}}})
	if m.loading || len(m.commits) != 1 {
		t.Error("the current load did not land")
	}
}

// TestMenuCherryPickFromBranch_RefusesWhileParked pins the guard: with an
// operation in progress the dialog does not open, and the Conflicts panel
// does.
func TestMenuCherryPickFromBranch_RefusesWhileParked(t *testing.T) {
	repo := cherryPickConflictRepo(t)
	a := newTestApp(t, repo)
	a.rootDir = repo
	a.menuCherryPickFromBranch()
	if a.modal != nil {
		t.Errorf("opened %T while a cherry-pick is parked", a.modal)
	}
	if !a.conflictPanel.open || !strings.Contains(a.statusMsg, "in progress") {
		t.Errorf("panel open=%v flash=%q", a.conflictPanel.open, a.statusMsg)
	}
}

// TestOpenCherryPickSourcePicker pins the source list on a real repo:
// other branches, current one excluded.
func TestOpenCherryPickSourcePicker(t *testing.T) {
	repo := cherryPickSourceRepo(t)
	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	a.openCherryPickSourcePicker(nil)
	p, ok := a.modal.(*paletteModal)
	if !ok || len(p.items) != 1 || p.items[0].label != "topic" {
		t.Fatalf("picker = %T %+v", a.modal, p)
	}
	p.items[0].run(a)
	if m, ok := a.modal.(*cherryPickModal); !ok || m.source != "topic" {
		t.Errorf("picking a branch opened %T", a.modal)
	}
}
