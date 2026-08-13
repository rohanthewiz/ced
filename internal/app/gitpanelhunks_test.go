// =============================================================================
// File: internal/app/gitpanelhunks_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// mixedDiffLines is a hand-built stand-in for what loadGitPanelDiff
// produces for a half-staged file: two labelled sections, each with its
// own file header, each with a hunk. Hand-built rather than fetched so
// the span/patch tests pin the SHAPE without needing git.
func mixedDiffLines() []string {
	return []string{
		gitPanelStagedMarker,
		"diff --git a/f.txt b/f.txt",
		"index 1111111..2222222 100644",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -1,3 +1,4 @@ func head()",
		" one",
		"+staged",
		" two",
		" three",
		gitPanelUnstagedMarker,
		"diff --git a/f.txt b/f.txt",
		"index 2222222..3333333 100644",
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -10,2 +10,3 @@ func tail()",
		" nine",
		"+unstaged",
		" ten",
	}
}

// TestGitPanelHunkSpans_SidesAndBounds pins the span walker: each hunk
// runs to the row before whatever ends it, and the marker lines — not a
// guess — decide which diff a hunk belongs to.
func TestGitPanelHunkSpans_SidesAndBounds(t *testing.T) {
	lines := mixedDiffLines()
	spans := gitPanelHunkSpans(lines, sideMixed)
	if len(spans) != 2 {
		t.Fatalf("spans = %+v, want 2", spans)
	}
	// The staged hunk stops at the row before the unstaged marker, not
	// at the end of the list — otherwise its patch would swallow the
	// second section's header.
	if spans[0].header != 5 || spans[0].end != 9 || spans[0].side != sideStaged {
		t.Fatalf("staged span = %+v, want header 5 end 9 sideStaged", spans[0])
	}
	if spans[1].header != 15 || spans[1].end != 18 || spans[1].side != sideUnstaged {
		t.Fatalf("unstaged span = %+v, want header 15 end 18 sideUnstaged", spans[1])
	}

	// An unsectioned diff takes the pane's own side, which is the only
	// place that fact exists (there is no marker to read it off).
	plain := []string{"diff --git a/f b/f", "--- a/f", "+++ b/f", "@@ -1 +1 @@", "-a", "+b"}
	one := gitPanelHunkSpans(plain, sideUnstaged)
	if len(one) != 1 || one[0].side != sideUnstaged || one[0].end != 5 {
		t.Fatalf("plain spans = %+v", one)
	}
	// sideNone is the untracked synthesis and the `diff HEAD` fallback:
	// readable, but addressable by no git apply, so no spans at all.
	if got := gitPanelHunkSpans(plain, sideNone); len(got) != 0 {
		t.Fatalf("sideNone spans = %+v, want none", got)
	}
}

// TestGitPanelHunkPatch_UsesItsOwnSectionHeader is the test that keeps
// a mixed file from being patched with the wrong old-side content: the
// second section's hunk must carry the SECOND file header, even though
// a naive "everything before the first @@" would find the first.
func TestGitPanelHunkPatch_UsesItsOwnSectionHeader(t *testing.T) {
	lines := mixedDiffLines()
	spans := gitPanelHunkSpans(lines, sideMixed)

	staged := gitPanelHunkPatch(lines, spans[0])
	if !strings.Contains(staged, "index 1111111..2222222") {
		t.Fatalf("staged patch lost its own header:\n%s", staged)
	}
	if strings.Contains(staged, "+unstaged") || strings.Contains(staged, "@@ -10,2") {
		t.Fatalf("staged patch leaked the other section:\n%s", staged)
	}

	unstaged := gitPanelHunkPatch(lines, spans[1])
	if !strings.Contains(unstaged, "index 2222222..3333333") {
		t.Fatalf("unstaged patch took the wrong header:\n%s", unstaged)
	}
	if strings.Contains(unstaged, "index 1111111") || strings.Contains(unstaged, "+staged") {
		t.Fatalf("unstaged patch leaked the other section:\n%s", unstaged)
	}
	// git rejects a patch whose last line has no newline.
	if !strings.HasSuffix(unstaged, "\n") {
		t.Fatalf("patch must end in a newline:\n%q", unstaged)
	}
	if strings.Contains(unstaged, gitPanelUnstagedMarker) {
		t.Fatalf("a marker line must never reach git:\n%s", unstaged)
	}
}

// TestGitPanelHunkVerbs_MixedWithholdsRevert pins the safety rule that
// falls out of what `git apply --index` checks: reverting a staged hunk
// has to match the work tree too, and on a half-staged file it doesn't.
func TestGitPanelHunkVerbs_MixedWithholdsRevert(t *testing.T) {
	staged := gitHunkSpan{side: sideStaged}
	unstaged := gitHunkSpan{side: sideUnstaged}

	if got := gitPanelHunkVerbs(unstaged, sideUnstaged); len(got) != 2 || got[0] != hunkStage || got[1] != hunkRevert {
		t.Fatalf("unstaged verbs = %v", got)
	}
	// An unstaged hunk is always addressable, mixed file or not: its old
	// side IS the index and its new side IS the work tree.
	if got := gitPanelHunkVerbs(unstaged, sideMixed); len(got) != 2 {
		t.Fatalf("unstaged verbs on a mixed file = %v, want both", got)
	}
	if got := gitPanelHunkVerbs(staged, sideStaged); len(got) != 2 || got[1] != hunkRevert {
		t.Fatalf("staged verbs = %v, want unstage + revert", got)
	}
	if got := gitPanelHunkVerbs(staged, sideMixed); len(got) != 1 || got[0] != hunkUnstage {
		t.Fatalf("staged verbs on a mixed file = %v, want unstage only", got)
	}
	if got := gitPanelHunkVerbs(gitHunkSpan{side: sideNone}, sideNone); got != nil {
		t.Fatalf("unaddressable verbs = %v, want none", got)
	}
}

// TestGitHunkApplyArgs pins the exact argv per verb. The distance
// between "--cached -R" and "--index -R" is the distance between
// unstaging a change and destroying it, so it gets a test of its own.
func TestGitHunkApplyArgs(t *testing.T) {
	cases := []struct {
		verb gitHunkVerb
		side diffSide
		want string
	}{
		{hunkStage, sideUnstaged, "--cached"},
		{hunkUnstage, sideStaged, "--cached -R"},
		{hunkRevert, sideUnstaged, "-R"},
		{hunkRevert, sideStaged, "--index -R"},
	}
	for _, tc := range cases {
		if got := strings.Join(gitHunkApplyArgs(tc.verb, tc.side), " "); got != tc.want {
			t.Errorf("args(%v, %v) = %q, want %q", tc.verb, tc.side, got, tc.want)
		}
	}
}

// TestGitPanelHunkChips_DrawAndClickAgree is the btnRect contract: the
// chips the drawer paints are the chips the click router finds, at the
// same cells. Driven through the real mouse router so the whole path is
// covered — including that a chip click is NOT treated as the first
// half of a jump-to-line double-click.
func TestGitPanelHunkChips_DrawAndClickAgree(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitPanel.open = true
	a.gitPanel.files = []gitPanelFile{{Path: "/p/f.txt", Rel: "f.txt", Code: " M"}}
	a.gitPanel.diffLines = []string{
		"diff --git a/f.txt b/f.txt", "--- a/f.txt", "+++ b/f.txt",
		"@@ -1,2 +1,3 @@", " one", "+two", " three",
	}
	a.gitPanel.diffSide = sideUnstaged

	chips := a.gitPanelHunkChipsAt(3)
	if len(chips) != 2 || chips[0].verb != hunkStage || chips[1].verb != hunkRevert {
		t.Fatalf("chips = %+v", chips)
	}
	// Body rows carry no targets — a click target over the code being
	// read is exactly what the header row exists to avoid.
	if got := a.gitPanelHunkChipsAt(4); len(got) != 0 {
		t.Fatalf("body row chips = %+v, want none", got)
	}

	// The drawer paints them where the hit-test says they are.
	a.drawGitPanel()
	if got := screenRow(t, a, chips[0].rect.y, chips[0].rect.x, 3); got != gitHunkChipLabel(hunkStage) {
		t.Fatalf("drawn chip = %q, want %q", got, gitHunkChipLabel(hunkStage))
	}

	// Clicking revert opens the confirm rather than running anything —
	// the destructive verb of the three.
	a.handleMouse(tcell.NewEventMouse(chips[1].rect.x+1, chips[1].rect.y, tcell.Button1, 0))
	cm, ok := a.modal.(*confirmModal)
	if !ok {
		t.Fatalf("revert chip opened %T, want a confirm", a.modal)
	}
	if !strings.Contains(strings.Join(cm.confirmBody(), " "), "f.txt") {
		t.Fatalf("confirm should name the file: %v", cm.confirmBody())
	}
	// And it must not have doubled as the first half of a double-click.
	if a.lastClick.x != 0 || a.lastClick.y != 0 {
		t.Fatalf("chip click recorded a double-click seed: %+v", a.lastClick)
	}
}

// TestGitPanelHunkVerbs_RealRepo is the end-to-end proof, against a
// real repo: stage one hunk of a two-hunk file, and only that hunk
// lands in the index — which is also what turns the file half-staged
// and puts the panel's two-section diff to work.
func TestGitPanelHunkVerbs_RealRepo(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t)
	file := filepath.Join(repo, "f.txt")
	// Two changes far enough apart that git emits two hunks.
	base := "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\nl13\nl14\nl15\n"
	writeFileT(t, file, base)
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "init")
	writeFileT(t, file, strings.Replace(strings.Replace(base,
		"l2\n", "l2\nTOP\n", 1), "l14\n", "l14\nBOTTOM\n", 1))

	a := newTestApp(t, repo)
	a.refreshGitStatus()
	a.menuToggleGitPanel()
	pumpAppEvents(t, a, func() bool { return len(a.gitPanel.diffLines) > 0 })

	spans := a.gitPanelHunkSpans()
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want 2:\n%s", len(spans), strings.Join(a.gitPanel.diffLines, "\n"))
	}
	a.gitPanelRunHunkVerb(hunkStage, spans[0])
	pumpAppEvents(t, a, func() bool {
		return strings.Contains(gitOut(t, repo, "diff", "--cached"), "+TOP")
	})

	cached := gitOut(t, repo, "diff", "--cached")
	if strings.Contains(cached, "BOTTOM") {
		t.Fatalf("staging one hunk staged the other too:\n%s", cached)
	}
	// The file is now half-staged, which is precisely the state the
	// union `git diff HEAD` could not describe: the panel must show two
	// labelled sections, and the second hunk must still be stageable.
	lines, side := loadGitPanelDiff(repo, gitPanelFile{Path: file, Rel: "f.txt", Code: "MM"})
	if side != sideMixed {
		t.Fatalf("half-staged side = %v, want sideMixed", side)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, gitPanelStagedMarker) || !strings.Contains(joined, gitPanelUnstagedMarker) {
		t.Fatalf("half-staged diff missing its section labels:\n%s", joined)
	}

	a.gitPanel.diffLines, a.gitPanel.diffSide = lines, side
	var second gitHunkSpan
	for _, sp := range a.gitPanelHunkSpans() {
		if sp.side == sideUnstaged {
			second = sp
		}
	}
	if second.header == 0 {
		t.Fatal("no unstaged hunk found in the mixed diff")
	}
	a.gitPanelRunHunkVerb(hunkStage, second)
	pumpAppEvents(t, a, func() bool {
		return strings.Contains(gitOut(t, repo, "diff", "--cached"), "+BOTTOM")
	})
	if out := gitOut(t, repo, "diff"); strings.TrimSpace(out) != "" {
		t.Fatalf("work tree should be clean after staging both hunks:\n%s", out)
	}

	// Unstage one of them back out again: the index loses that hunk and
	// keeps the other, and the work tree is untouched either way — the
	// difference between `--cached -R` and `--index -R`.
	lines, side = loadGitPanelDiff(repo, gitPanelFile{Path: file, Rel: "f.txt", Code: "M "})
	if side != sideStaged {
		t.Fatalf("fully-staged side = %v, want sideStaged", side)
	}
	a.gitPanel.diffLines, a.gitPanel.diffSide = lines, side
	a.gitPanelRunHunkVerb(hunkUnstage, a.gitPanelHunkSpans()[0])
	pumpAppEvents(t, a, func() bool {
		return !strings.Contains(gitOut(t, repo, "diff", "--cached"), "+TOP")
	})
	if cached := gitOut(t, repo, "diff", "--cached"); !strings.Contains(cached, "+BOTTOM") {
		t.Fatalf("unstaging one hunk unstaged the other too:\n%s", cached)
	}
	if body := readFileString(t, file); !strings.Contains(body, "TOP") {
		t.Fatal("unstage must leave the work tree alone")
	}
}

// TestGitPanelHunkRevert_RealRepo pins the destructive verb: revert
// takes the hunk out of the WORK TREE and leaves the rest of the file —
// and leaves the index alone, which is the difference between -R and
// --index -R.
func TestGitPanelHunkRevert_RealRepo(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t)
	file := filepath.Join(repo, "f.txt")
	base := "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\nl13\nl14\nl15\n"
	writeFileT(t, file, base)
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "init")
	writeFileT(t, file, strings.Replace(strings.Replace(base,
		"l2\n", "l2\nTOP\n", 1), "l14\n", "l14\nBOTTOM\n", 1))

	a := newTestApp(t, repo)
	a.refreshGitStatus()
	a.menuToggleGitPanel()
	pumpAppEvents(t, a, func() bool { return len(a.gitPanel.diffLines) > 0 })

	spans := a.gitPanelHunkSpans()
	a.gitPanelRunHunkVerb(hunkRevert, spans[0])
	// The confirm is the gate; Yes is what runs it.
	cm, ok := a.modal.(*confirmModal)
	if !ok {
		t.Fatalf("revert opened %T, want a confirm", a.modal)
	}
	cb := cm.callback
	a.closeModal()
	cb(a)
	// git apply rewrites the file by replacing it, so a tight poll can
	// catch the moment it doesn't exist — a missing read is "not yet",
	// not a failure.
	pumpAppEvents(t, a, func() bool {
		body, err := os.ReadFile(file)
		return err == nil && !strings.Contains(string(body), "TOP")
	})
	if body := readFileString(t, file); !strings.Contains(body, "BOTTOM") {
		t.Fatalf("revert took the wrong hunk (or both):\n%s", body)
	}
}

// TestGitPanelHunkSubdirRoot is the counterpart to the conflict
// resolver's absolute-path test, from the other direction: ced's root
// is a SUBDIRECTORY of the repo, and a patch's paths are root-relative,
// so `git apply` has to run from the toplevel or it patches nothing.
func TestGitPanelHunkSubdirRoot(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t)
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := filepath.Join(sub, "f.txt")
	writeFileT(t, file, "one\ntwo\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "init")
	writeFileT(t, file, "one\nCHANGED\n")

	// The editor is opened ON the subdirectory — the case that breaks
	// every path assumption in the file.
	a := newTestApp(t, sub)
	a.refreshGitStatus()
	a.menuToggleGitPanel()
	pumpAppEvents(t, a, func() bool { return len(a.gitPanel.diffLines) > 0 })

	spans := a.gitPanelHunkSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %+v", spans)
	}
	a.gitPanelRunHunkVerb(hunkStage, spans[0])
	pumpAppEvents(t, a, func() bool {
		return strings.Contains(gitOut(t, repo, "diff", "--cached"), "+CHANGED")
	})
}

// readFileString reads a file back for an assertion, failing the test
// rather than returning an error nobody would check.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// TestDiffTargetLine_MarkersResetTheCounter pins the double-click jump
// across a mixed diff: each section restarts the new-side line counter,
// so a row in the unstaged section maps through ITS hunk header, not
// through the staged section's numbering. The marker rows themselves
// map to nothing — they are the panel's words, not the file's.
func TestDiffTargetLine_MarkersResetTheCounter(t *testing.T) {
	lines := mixedDiffLines()

	if _, ok := diffTargetLine(lines, 0); ok {
		t.Fatal("a marker row must not map to a line")
	}
	// Row 7 is "+staged", the first added line of the staged hunk, whose
	// header says the new side starts at line 1 → " one" is 1, "+staged"
	// is 2 → 0-based 1.
	if got, ok := diffTargetLine(lines, 7); !ok || got != 1 {
		t.Fatalf("staged +line = %d (ok=%v), want 1", got, ok)
	}
	// Row 17 is "+unstaged" under a header starting at line 10 →
	// " nine" is 10, "+unstaged" is 11 → 0-based 10. Without the reset
	// it would carry the first section's count and land elsewhere.
	if got, ok := diffTargetLine(lines, 17); !ok || got != 10 {
		t.Fatalf("unstaged +line = %d (ok=%v), want 10", got, ok)
	}
}
