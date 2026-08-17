// =============================================================================
// File: internal/app/gitstatusreport_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestGitStatusReportBody_PassesReportThrough pins the ordinary case: a
// short report reaches the modal line for line, with only the CRs a git
// on Windows line endings would leave behind trimmed off.
func TestGitStatusReportBody_PassesReportThrough(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	got := a.gitStatusReportBody([]byte("On branch main\r\n\r\nnothing to commit, working tree clean\n"))
	want := []string{"On branch main", "", "nothing to commit, working tree clean"}
	if len(got) != len(want) {
		t.Fatalf("body = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestGitStatusReportBody_EmptyOutputYieldsNothing checks that an empty
// report produces no lines at all rather than one blank one — openInfo
// substitutes its own "(no output captured)" placeholder, and a body of
// one empty string would suppress it in favour of a blank modal.
func TestGitStatusReportBody_EmptyOutputYieldsNothing(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.gitStatusReportBody([]byte("\n\n")); len(got) != 0 {
		t.Fatalf("body = %q, want none", got)
	}
}

// TestGitStatusReportBody_EllipsizesOverlongLine pins the width clamp: a
// line wider than the info modal is cut with a marker rather than drawn
// past the frame. A deeply nested path is the realistic producer.
func TestGitStatusReportBody_EllipsizesOverlongLine(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	long := "\tmodified:   " + strings.Repeat("nested/", 30) + "file.go"
	got := a.gitStatusReportBody([]byte(long))
	if len(got) != 1 {
		t.Fatalf("body = %d lines, want 1", len(got))
	}
	if runeLen(got[0]) != gitStatusReportBodyWidth {
		t.Errorf("line width = %d, want %d", runeLen(got[0]), gitStatusReportBodyWidth)
	}
	if !strings.HasSuffix(got[0], "…") {
		t.Errorf("clamped line = %q, want an ellipsis marker", got[0])
	}
}

// TestGitStatusReportBody_CapsToWindowAndSaysSo is the important one: the
// info modal doesn't scroll, so the body must fit the window — and the
// cut must be REPORTED, since a silently short status reads exactly like
// a clean one. The note itself counts against the budget.
func TestGitStatusReportBody_CapsToWindowAndSaysSo(t *testing.T) {
	a := newTestApp(t, t.TempDir()) // 120x40 simulation screen
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	got := a.gitStatusReportBody([]byte(b.String()))

	wantRows := a.height - 7
	if len(got) != wantRows {
		t.Fatalf("body = %d lines, want %d (window %d)", len(got), wantRows, a.height)
	}
	last := got[len(got)-1]
	// 100 lines, of which wantRows-1 survive: the rest are named.
	hidden := 100 - (wantRows - 1)
	if !strings.Contains(last, strconv.Itoa(hidden)+" more lines") {
		t.Errorf("truncation note = %q, want a count of %d", last, hidden)
	}
	if got[len(got)-2] != "line "+strconv.Itoa(wantRows-2) {
		t.Errorf("row before the note = %q, want the last kept report line", got[len(got)-2])
	}
}

// TestGitStatusReportBody_FloorsOnTinyWindow pins the floor: a window too
// short for the modal's own chrome still gets a few lines of the answer
// rather than a negative budget.
func TestGitStatusReportBody_FloorsOnTinyWindow(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.height = 4
	got := a.gitStatusReportBody([]byte("a\nb\nc\nd\ne\n"))
	if len(got) != gitStatusReportMinLines {
		t.Fatalf("body = %d lines, want %d", len(got), gitStatusReportMinLines)
	}
}

// TestHandleGitStatusReport_OpensInfoModal pins the success path: git's
// report lands in the info modal under its own title.
func TestHandleGitStatusReport_OpensInfoModal(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleGitStatusReport(&gitStatusReportEvent{
		when: time.Now(),
		out:  []byte("On branch main\nnothing to commit, working tree clean\n"),
	})
	m, ok := a.modal.(*confirmModal)
	if !ok || !m.info {
		t.Fatalf("modal = %T, want info confirmModal", a.modal)
	}
	if m.title != "Git status" {
		t.Fatalf("title = %q, want %q", m.title, "Git status")
	}
	if len(m.lines) != 2 || m.lines[0] != "On branch main" {
		t.Fatalf("lines = %q", m.lines)
	}
}

// TestHandleGitStatusReport_FailureShowsGitsComplaint pins the write-side
// contract this read borrows: the user asked, so a failure is surfaced
// with git's own words rather than swallowed the way the background
// snapshot's failures are.
func TestHandleGitStatusReport_FailureShowsGitsComplaint(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleGitStatusReport(&gitStatusReportEvent{
		when: time.Now(),
		err:  errors.New("exit status 128"),
		out:  []byte("fatal: not a git repository"),
	})
	m, ok := a.modal.(*confirmModal)
	if !ok || !m.info {
		t.Fatalf("modal = %T, want info confirmModal", a.modal)
	}
	if m.title != "Git status failed" {
		t.Fatalf("title = %q", m.title)
	}
	if !strings.Contains(strings.Join(m.lines, "\n"), "not a git repository") {
		t.Fatalf("lines = %q, want git's message", m.lines)
	}
}

// TestHandleGitStatusReport_DeclinesOccupiedSlot pins the refusal: an
// arriving report never replaces a modal that is already up. openModal
// replaces rather than refuses, so stealing the slot would silently drop
// whatever reply that modal was waiting to give.
func TestHandleGitStatusReport_DeclinesOccupiedSlot(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openInfo("Something else", []string{"busy"})
	occupant := a.modal

	a.handleGitStatusReport(&gitStatusReportEvent{when: time.Now(), out: []byte("On branch main\n")})
	if a.modal != occupant {
		t.Fatal("report stole the modal slot from an open dialog")
	}
	if !strings.Contains(a.statusMsg, "Git status") {
		t.Fatalf("statusMsg = %q, want a note that the report was dropped", a.statusMsg)
	}
}

// TestHasGitStatusReport_GatesOnRepo pins the menu predicate: any
// repository enables the row, including a clean one — "nothing to
// commit" is a real answer, and the one a user suspicious of the tree's
// colors is asking for.
func TestHasGitStatusReport_GatesOnRepo(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.hasGitStatusReport() {
		t.Fatal("non-repo should not offer the row")
	}
	a.gitIsRepo = true
	if !a.hasGitStatusReport() {
		t.Fatal("clean repo must still offer the row")
	}
}

// TestMenuGitStatusRow_SitsAtopTheRepoVerbs pins the row's home: it opens
// the repo-level block of the Git group, directly above Stage file — the
// read those verbs act on.
func TestMenuGitStatusRow_SitsAtopTheRepoVerbs(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.customActions = nil
	items, _, _ := a.menuLayout()

	status := menuRowIndex(items, "Git status…")
	stage := menuRowIndex(items, "Stage file")
	if status < 0 {
		t.Fatal("Git status row missing from the menu")
	}
	if stage != status+1 {
		t.Errorf("Git status at %d, Stage file at %d — want adjacent", status, stage)
	}
}

// TestMenuGitStatus_EndToEnd drives the real fork against a real repo and
// checks that what lands in the modal is git's narrative report — the
// branch line and the untracked file — rather than the porcelain list the
// changes panel already draws.
func TestMenuGitStatus_EndToEnd(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, repo)
	a.gitIsRepo = true

	a.menuGitStatus()
	pumpAppEvents(t, a, func() bool { return a.modal != nil })

	m, ok := a.modal.(*confirmModal)
	if !ok || !m.info {
		t.Fatalf("modal = %T, want info confirmModal", a.modal)
	}
	body := strings.Join(m.lines, "\n")
	if !strings.Contains(body, "On branch main") {
		t.Errorf("body missing the branch line:\n%s", body)
	}
	if !strings.Contains(body, "new.txt") {
		t.Errorf("body missing the untracked file:\n%s", body)
	}
	// advice.statusHints=false is part of the command for a reason —
	// those lines name shell verbs that are rows in this very menu.
	if strings.Contains(body, "(use \"git") {
		t.Errorf("advice hints were not suppressed:\n%s", body)
	}
}
