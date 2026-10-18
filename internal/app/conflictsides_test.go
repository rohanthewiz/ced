// =============================================================================
// File: internal/app/conflictsides_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/theme"
)

// TestConflictSidesFromInfo pins the file's table: each operation's role
// and name for the side above and the side below, with the rebase row
// pointing "yours" at the side BELOW.
func TestConflictSidesFromInfo(t *testing.T) {
	cases := []struct {
		info     gitOpInfo
		branch   string
		cur, inc string
		known    bool
	}{
		{gitOpInfo{op: "merge", mergeMsg: "Merge branch 'topic'", short: "9f1c2d3"}, "main",
			"yours (main)", "theirs (topic)", true},
		{gitOpInfo{op: "merge", mergeMsg: "edited by hand", short: "9f1c2d3"}, "main",
			"yours (main)", "theirs (9f1c2d3)", true},
		{gitOpInfo{op: "cherry-pick", short: "2a0370b"}, "main",
			"yours (main)", "picked (2a0370b)", true},
		{gitOpInfo{op: "revert", short: "2a0370b"}, "main",
			"yours (main)", "revert (2a0370b)", true},
		{gitOpInfo{op: "rebase", onto: "abc1234", short: "2a0370b", step: 1}, "",
			"upstream (abc1234)", "yours (2a0370b)", true},
		{gitOpInfo{op: "cherry-pick"}, "HEAD", "yours", "picked", true},
		{gitOpInfo{}, "main", "current", "incoming", false},
	}
	for _, c := range cases {
		s := conflictSidesFromInfo(c.info, c.branch)
		if s.cur.tag() != c.cur || s.inc.tag() != c.inc || s.known != c.known {
			t.Errorf("%s: %q / %q known=%v, want %q / %q known=%v",
				c.info.op, s.cur.tag(), s.inc.tag(), s.known, c.cur, c.inc, c.known)
		}
	}
}

// TestConflictSides_RebaseNoteNamesTheReplayedCommits pins the one place
// the rebase's upper side is described exactly: past the first commit it
// holds the user's own replayed commits too.
func TestConflictSides_RebaseNoteNamesTheReplayedCommits(t *testing.T) {
	first := conflictSidesFromInfo(gitOpInfo{op: "rebase", onto: "abc1234", short: "2a0370b", step: 1}, "")
	if got := first.note(); got != "▲ upstream (abc1234) · ▼ yours (2a0370b)" {
		t.Errorf("step 1 note = %q", got)
	}
	later := conflictSidesFromInfo(gitOpInfo{op: "rebase", onto: "abc1234", short: "2a0370b", step: 3}, "")
	if got := later.note(); !strings.Contains(got, "upstream (abc1234) + your commits replayed so far") {
		t.Errorf("step 3 note = %q", got)
	}
}

// TestMergeMsgBranch pins the MERGE_MSG shapes git writes, and "" for a
// message that quotes nothing.
func TestMergeMsgBranch(t *testing.T) {
	cases := map[string]string{
		"Merge branch 'topic'":                          "topic",
		"Merge branch 'topic' of github.com:x/y":        "topic",
		"Merge remote-tracking branch 'origin/fix/a-b'": "origin/fix/a-b",
		"my own words":                                  "",
		"Merge branch ''":                               "",
		"Merge branch 'unclosed":                        "",
	}
	for msg, want := range cases {
		if got := mergeMsgBranch(msg); got != want {
			t.Errorf("%q → %q, want %q", msg, got, want)
		}
	}
}

// TestConflictSide_Phrase pins the in-sentence forms: possessives for
// the two ownership roles, "the X side" for the rest.
func TestConflictSide_Phrase(t *testing.T) {
	cases := map[string]string{
		"yours": "your side", "theirs": "their side", "upstream": "the upstream side",
		"picked": "the picked side", "current": "the current side",
	}
	for role, want := range cases {
		if got := (conflictSide{role: role}).phrase(); got != want {
			t.Errorf("%s → %q, want %q", role, got, want)
		}
	}
}

// TestChoiceLabel_PositionalFallbackIsTheEditorsOwn pins the no-operation
// promise: with positional sides every choice reads exactly as the editor
// names it, so a conflicted stash pop shows what it always did.
func TestChoiceLabel_PositionalFallbackIsTheEditorsOwn(t *testing.T) {
	s := positionalSides()
	for c := editor.ChooseCurrent; c <= editor.ChooseNeither; c++ {
		if got := s.choiceLabel(c); got != c.Label() {
			t.Errorf("choice %d: %q, want %q", c, got, c.Label())
		}
	}
	if s.choiceShort(editor.ChooseCurrent) != "Current" || s.choiceShort(editor.ChooseIncoming) != "Incoming" {
		t.Errorf("shorts = %q / %q", s.choiceShort(editor.ChooseCurrent), s.choiceShort(editor.ChooseIncoming))
	}
}

// TestChoiceLabel_NamesTheSides pins the operation's words on the verbs,
// including "both, X first" naming the lower side by its role.
func TestChoiceLabel_NamesTheSides(t *testing.T) {
	s := conflictSidesFromInfo(gitOpInfo{op: "rebase", onto: "abc1234", short: "2a0370b"}, "")
	if got := s.choiceLabel(editor.ChooseCurrent); got != "Accept upstream (abc1234)" {
		t.Errorf("current = %q", got)
	}
	if got := s.choiceLabel(editor.ChooseIncoming); got != "Accept yours (2a0370b)" {
		t.Errorf("incoming = %q", got)
	}
	if got := s.choiceLabel(editor.ChooseBothIncomingFirst); got != "Accept both, yours first" {
		t.Errorf("both, incoming first = %q", got)
	}
	if got := s.choiceShort(editor.ChooseIncoming); got != "Yours" {
		t.Errorf("short = %q", got)
	}
}

// TestSeparatorNotes_Diff3LabelsBothSeparators pins the diff3 case: the
// `=======` of a diff3 block has the BASE above it, so it must not claim
// the current side is there — the base marker carries that half.
func TestSeparatorNotes_Diff3LabelsBothSeparators(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := conflictSidesFromInfo(gitOpInfo{op: "cherry-pick", short: "2a0370b"}, "main")

	two := editor.ConflictBlock{Start: 0, Base: -1, Mid: 2, End: 4}
	notes := s.separatorNotes(two, a.theme)
	if got := runsText(notes[2]); got != "▲ yours (main)  ▼ picked (2a0370b)" || len(notes) != 1 {
		t.Errorf("two-way notes = %v", notes)
	}

	three := editor.ConflictBlock{Start: 0, Base: 2, Mid: 4, End: 6}
	notes = s.separatorNotes(three, a.theme)
	if got := runsText(notes[2]); got != "▲ yours (main)  ▼ base" {
		t.Errorf("base marker note = %q", got)
	}
	if got := runsText(notes[4]); got != "▲ base  ▼ picked (2a0370b)" {
		t.Errorf("separator note = %q", got)
	}
	bg := conflictRegionColor(a.theme, editor.RegionMidMarker)
	if notes[2][0].FG != liftToContrast(a.theme.GitAdded, bg) || notes[4][1].FG != liftToContrast(a.theme.AccentSoft, bg) {
		t.Error("the side runs lost their wash-matching colours")
	}
}

// TestSeparatorNotes_ReadableInEveryTheme pins the contrast lift: on the
// separator's grey every run of the label clears 4.5:1 in every shipped
// theme — before the lift Darcula's green sat near 2.5:1.
func TestSeparatorNotes_ReadableInEveryTheme(t *testing.T) {
	s := conflictSidesFromInfo(gitOpInfo{op: "cherry-pick", short: "2a0370b"}, "main")
	three := editor.ConflictBlock{Start: 0, Base: 2, Mid: 4, End: 6}
	for _, sp := range theme.Builtins() {
		th, err := sp.Resolve()
		if err != nil {
			t.Fatalf("%s: %v", sp.Name, err)
		}
		bg := conflictRegionColor(th, editor.RegionMidMarker)
		for line, runs := range s.separatorNotes(three, th) {
			for _, r := range runs {
				if c := contrastRatio(r.FG, bg); c < 4.5 {
					t.Errorf("%s line %d %q: contrast %.2f", sp.Name, line, r.Text, c)
				}
			}
		}
	}
}

// runsText joins a note's runs back into its text.
func runsText(runs []editor.NoteRun) string {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.Text)
	}
	return b.String()
}

// TestConflictSides_StaleRecordKeepsRolesDropsNames pins the staleness
// rule: when the panel's record describes another operation than the
// snapshot's, the roles follow the snapshot and no name is borrowed from
// the stale record.
func TestConflictSides_StaleRecordKeepsRolesDropsNames(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitOp, a.gitBranch = "rebase", ""
	a.conflictPanel.info = gitOpInfo{op: "cherry-pick", short: "deadbee"}
	s := a.conflictSides()
	if s.cur.tag() != "upstream" || s.inc.tag() != "yours" {
		t.Errorf("sides = %q / %q, want bare rebase roles", s.cur.tag(), s.inc.tag())
	}
}

// TestConflictSidesFor_FillsFromMarkers pins the marker fallback: a name
// the record lacks comes from the block's label — but never "HEAD".
func TestConflictSidesFor_FillsFromMarkers(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitOp = "cherry-pick"
	a.conflictPanel.info = gitOpInfo{op: "cherry-pick"}
	b := editor.ConflictBlock{CurrentLabel: "HEAD", IncomingLabel: "2a0370b (bump a)"}
	s := a.conflictSidesFor(b)
	if s.cur.tag() != "yours" || s.inc.tag() != "picked (2a0370b)" {
		t.Errorf("sides = %q / %q", s.cur.tag(), s.inc.tag())
	}
}

// TestConflictView_NamesTheSidesOnScreen pins the in-editor result for a
// marker file during a cherry-pick: the lens names the verbs by side (on
// a typical window the set is too long for full labels, so it shows the
// roles), and the separator line carries the full names — the label is
// what keeps "which one is mine" answered when the lens sheds the names.
func TestConflictView_NamesTheSidesOnScreen(t *testing.T) {
	a, _ := conflictTestApp(t, "a.go")
	a.gitOp, a.gitBranch = "cherry-pick", "main"
	a.conflictPanel.info = gitOpInfo{op: "cherry-pick", short: "2a0370b"}
	tab := a.activeTabPtr()
	set := conflictSource{app: a}.Lenses(tab, a.theme, 0, 10)[1]
	if len(set) < 2 || set[0].Label != "Accept yours (main)" || set[1].Label != "Accept picked (2a0370b)" {
		t.Fatalf("lens set = %+v", set)
	}
	a.draw()
	lensCell(t, a, "<<<<<<< HEAD  Yours · Picked · Both")
	lensCell(t, a, "=======  ▲ yours (main)  ▼ picked (2a0370b)")
}

// TestConflictView_NoOperationNoLabel pins the fallback on screen: with no
// operation known the lens keeps its positional verbs and the separator
// carries no label.
func TestConflictView_NoOperationNoLabel(t *testing.T) {
	a, _ := conflictTestApp(t, "a.go")
	a.draw()
	lensCell(t, a, "Accept current")
	for y := 0; y < a.height; y++ {
		if row := screenRow(t, a, y, 0, a.width); strings.Contains(row, "▲") {
			t.Errorf("row %d labels the sides with no operation known: %q", y, row)
		}
	}
}

// TestConflictSides_RealRebase pins the case this file exists for against
// real git: rebasing a feature commit onto a main that rewrote the same
// line. The lens must call the LOWER side (the feature commit) "yours"
// and the upper side the upstream, and the panel's sides line agrees.
func TestConflictSides_RealRebase(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\ntwo\nthree\n", "base")
	gitRun(t, repo, "checkout", "-q", "-b", "feature")
	writeCommit(t, repo, "f.txt", "one\nFEATURE\nthree\n", "feature edits the middle")
	gitRun(t, repo, "checkout", "-q", "main")
	writeCommit(t, repo, "f.txt", "one\nMAIN\nthree\n", "main edits the middle")
	gitRun(t, repo, "checkout", "-q", "feature")
	gitRunAllowFail(t, repo, "rebase", "main")

	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus() // panel shut: the record must still be read
	if a.gitOp != "rebase" {
		t.Fatalf("gitOp = %q, want rebase", a.gitOp)
	}
	a.openFile(filepath.Join(repo, "f.txt"))
	tab := a.activeTabPtr()
	blocks := tab.Conflicts()
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d", len(blocks))
	}
	s := a.conflictSidesFor(blocks[0])
	if s.cur.role != "upstream" || s.cur.name == "" || s.inc.role != "yours" || s.inc.name == "" {
		t.Fatalf("sides = %+v", s)
	}
	// The lower side really is the feature commit's text.
	if got := strings.Join(blocks[0].IncomingLines(tab.Buffer.Lines), "\n"); got != "FEATURE" {
		t.Fatalf("incoming lines = %q, want the feature commit's", got)
	}
	if got := a.conflictSidesNote(); !strings.HasPrefix(got, "▲ upstream (") || !strings.Contains(got, "▼ yours (") {
		t.Errorf("sides line = %q", got)
	}
}
