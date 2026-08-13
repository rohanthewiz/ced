// =============================================================================
// File: internal/app/gitblame_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the blame layer: the porcelain parser, the column's shape,
// the source that suppresses repeats, the click that reveals a commit,
// and the staleness rule that keeps a slow answer from annotating a
// buffer it never saw. The end-to-end case runs against a real
// repository, because the parser's whole job is to agree with git.

package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
)

// blamePorcelain is one `git blame --porcelain` transcript: two commits
// over four lines, with the FIRST commit's details given once and
// referenced again later — the shape the parser exists to handle.
const blamePorcelain = "" +
	"1111111111111111111111111111111111111111 1 1 2\n" +
	"author Rohan Allison\n" +
	"author-mail <rohan@example.com>\n" +
	"author-time 1000000000\n" +
	"author-tz +0000\n" +
	"summary the first commit\n" +
	"filename main.go\n" +
	"\tpackage main\n" +
	"1111111111111111111111111111111111111111 2 2\n" +
	"\t\n" +
	"2222222222222222222222222222222222222222 3 3 1\n" +
	"author Ada Lovelace\n" +
	"author-mail <ada@example.com>\n" +
	"author-time 1000086400\n" +
	"author-tz +0000\n" +
	"summary add the func\n" +
	"filename main.go\n" +
	"\tfunc main() {}\n" +
	// The repeat: no author/summary lines at all, only the hash.
	"1111111111111111111111111111111111111111 4 4 1\n" +
	"\t// trailing\n"

// blameNow is a fixed clock for the transcript above: one day after the
// second commit, so the ages are deterministic.
var blameNow = time.Unix(1000172800, 0)

// TestParseBlamePorcelain_RepeatedCommitsCarryTheirDetails is the
// parser's load-bearing property. `--porcelain` spells a commit's
// author and summary out ONCE and refers to it by hash forever after,
// so a parser that only reads the details it is handed would leave
// every later line of the file's oldest commit blank — which is most of
// a real file.
func TestParseBlamePorcelain_RepeatedCommitsCarryTheirDetails(t *testing.T) {
	lines := parseBlamePorcelain([]byte(blamePorcelain), blameNow)

	if len(lines) != 4 {
		t.Fatalf("parsed %d lines, want 4", len(lines))
	}
	if lines[0].Short != "1111111" || lines[0].Author != "Rohan" {
		t.Fatalf("line 1 = %+v", lines[0])
	}
	if lines[2].Author != "Ada" || lines[2].Summary != "add the func" {
		t.Fatalf("line 3 = %+v", lines[2])
	}
	// The line the test exists for: same commit as line 1, details never
	// repeated on the wire.
	if lines[3].Author != "Rohan" || lines[3].Summary != "the first commit" {
		t.Fatalf("repeated commit lost its details: %+v", lines[3])
	}
	if lines[3].Full != "Rohan Allison" {
		t.Fatalf("the full name is what a click reports, got %q", lines[3].Full)
	}
}

// TestParseBlamePorcelain_UncommittedLinesAreNotACommit pins the answer
// for text that exists only in the buffer. git reports the all-zero id;
// showing seven zeros as if they were a commit would invite a click
// that can only fail.
// The two names git gives an uncommitted line, depending only on HOW it
// was asked: plain blame calls it "Not Committed Yet", and blame with
// --contents (which is how this feature always asks) calls it "External
// file (--contents)". Neither belongs in the margin — the second is the
// mechanism leaking into the UI — so the hash decides, not the name.
func TestParseBlamePorcelain_UncommittedLinesAreNotACommit(t *testing.T) {
	for _, author := range []string{"Not Committed Yet", "External file (--contents)"} {
		wire := "" +
			uncommittedHash + " 1 1 1\n" +
			"author " + author + "\n" +
			"author-time 1000172700\n" +
			"summary Version of main.go from main.go\n" +
			"\tjust typed this\n"

		lines := parseBlamePorcelain([]byte(wire), blameNow)

		if len(lines) != 1 {
			t.Fatalf("%s: parsed %d lines, want 1", author, len(lines))
		}
		if lines[0].committed() {
			t.Fatalf("%s: the all-zero hash is not a commit", author)
		}
		if got := blameHashSlot(lines[0]); got != "—" {
			t.Fatalf("%s: hash slot = %q, want a dash", author, got)
		}
		if lines[0].Author != "you" || lines[0].Full != "you" {
			t.Fatalf("%s: author = %q/%q, want \"you\"", author, lines[0].Author, lines[0].Full)
		}
	}
}

// TestParseBlamePorcelain_SummaryIsNotMistakenForAHeader guards the one
// ambiguity in the format: a summary is arbitrary text, and a commit
// whose message happens to look like a group header must not start a
// new group. Recognizing headers by SHAPE (40 hex digits, then numbers)
// is what makes that safe.
func TestParseBlamePorcelain_SummaryIsNotMistakenForAHeader(t *testing.T) {
	wire := "" +
		"1111111111111111111111111111111111111111 1 1 1\n" +
		"author Rohan Allison\n" +
		"author-time 1000000000\n" +
		"summary 2222222222222222222222222222222222222222 9 9 9\n" +
		"\tpackage main\n"

	lines := parseBlamePorcelain([]byte(wire), blameNow)

	if len(lines) != 1 {
		t.Fatalf("a summary started a new group: %d lines", len(lines))
	}
	if lines[0].Short != "1111111" {
		t.Fatalf("line 1 hash = %q", lines[0].Short)
	}
}

// TestRelativeAge_Buckets pins the compact ages. The column has room
// for a number and a unit, and the question it answers is "ancient or
// this week" — so the buckets are coarse on purpose.
func TestRelativeAge_Buckets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{10 * time.Second, "now"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{50 * time.Hour, "2d"},
		{60 * 24 * time.Hour, "2mo"},
		{800 * 24 * time.Hour, "2y"},
	}
	for _, c := range cases {
		if got := relativeAge(now.Add(-c.ago), now); got != c.want {
			t.Errorf("age %v = %q, want %q", c.ago, got, c.want)
		}
	}
	if got := relativeAge(time.Time{}, now); got != "" {
		t.Errorf("an unknown time has no age, got %q", got)
	}
}

// TestBlameText_AgeIsRightAlignedInTheColumn pins the layout: hash and
// author read from the left, the age hangs on the right so the units
// line up down the page and the column can be scanned as one sorted
// list.
func TestBlameText_AgeIsRightAligned(t *testing.T) {
	lines := parseBlamePorcelain([]byte(blamePorcelain), blameNow)
	w := blameColumnWidth(lines)

	first := blameText(lines[0], w)
	third := blameText(lines[2], w)
	if runeLen(first) != w-1 || runeLen(third) != w-1 {
		t.Fatalf("rows should fill the column minus its pad: %q / %q in %d", first, third, w)
	}
	if !strings.HasSuffix(first, lines[0].Age) || !strings.HasSuffix(third, lines[2].Age) {
		t.Fatalf("ages should end the row: %q / %q", first, third)
	}
	if !strings.HasPrefix(first, "1111111 Rohan") {
		t.Fatalf("row = %q, want hash then author", first)
	}
}

// TestBlameColumnWidth_MeasuresTheWholeFile is the anti-jitter rule.
// A width taken from the visible rows would change as the user
// scrolled, sliding the code sideways under their eyes — which is why
// the measurement is over every line of the answer, once.
func TestBlameColumnWidth_MeasuresTheWholeFile(t *testing.T) {
	lines := []blameLine{
		{Hash: "1111111111111111111111111111111111111111", Short: "1111111", Author: "Ada", Age: "3d"},
		{Hash: "2222222222222222222222222222222222222222", Short: "2222222", Author: "Bartholomew", Age: "10mo"},
	}
	full := blameColumnWidth(lines)
	firstRowOnly := blameColumnWidth(lines[:1])
	if full <= firstRowOnly {
		t.Fatalf("the long row must set the width: %d vs %d", full, firstRowOnly)
	}
	// And every row renders into that one width.
	for _, b := range lines {
		if got := runeLen(blameText(b, full)); got != full-1 {
			t.Errorf("%q rendered %d cells, want %d", b.Author, got, full-1)
		}
	}
}

// blameFixture is an app with one open file and a hand-built blame for
// it — everything the source and the click need, with no git.
func blameFixture(t *testing.T) (*App, *editor.Tab) {
	t.Helper()
	root := t.TempDir()
	a := newTestApp(t, root)
	a.gitIsRepo = true
	path := filepath.Join(root, "main.go")
	writeFileT(t, path, "package main\n\nfunc main() {}\n// trailing\n")
	tab := openTabAtPath(t, a, path)

	lines := parseBlamePorcelain([]byte(blamePorcelain), blameNow)
	a.blameOn = true
	a.fileBlames = map[string]*fileBlame{
		path: {lines: lines, width: blameColumnWidth(lines)},
	}
	// The source is what wireTab installs on a real open; the test
	// helper builds tabs directly, so it registers the one source these
	// cases are about rather than the whole set.
	tab.DecoSources = append(tab.DecoSources, gitBlameSource{app: a})
	return a, tab
}

// TestBlameAnnotations_OneHeadingPerRun pins the readability rule:
// blame is read for its BOUNDARIES, and repeating one commit's stamp
// down a run of lines buries them. Lines 1-2 share a commit here, so
// only the first is annotated.
func TestBlameAnnotations_OneHeadingPerRun(t *testing.T) {
	a, tab := blameFixture(t)
	src := gitBlameSource{app: a}

	w, anns := src.Annotations(tab, a.theme, 0, 3)

	if w == 0 {
		t.Fatal("the source should have asked for a column")
	}
	if _, ok := anns[0]; !ok {
		t.Fatal("the first line of a run is annotated")
	}
	if _, ok := anns[1]; ok {
		t.Fatal("the second line of the same commit must not repeat it")
	}
	if _, ok := anns[2]; !ok {
		t.Fatal("a new commit starts a new heading")
	}
	if _, ok := anns[3]; !ok {
		t.Fatal("returning to an earlier commit is also a boundary")
	}
}

// TestBlameAnnotations_TheTopRowAlwaysSaysWhoseCodeThisIs is the other
// half of that rule, and it was found by looking at the thing rather
// than by reasoning about it: suppressing a run against the FILE leaves
// a reader scrolled into the middle of a file written in one commit
// staring at an empty eighteen-cell margin. The run's heading is
// re-drawn on the first VISIBLE row, so the column always answers the
// question it exists for.
func TestBlameAnnotations_TheTopRowAlwaysSaysWhoseCodeThisIs(t *testing.T) {
	a, tab := blameFixture(t)
	src := gitBlameSource{app: a}

	// Line 2 (0-based 1) is a continuation of line 1's commit — but it
	// is the top of this window.
	_, anns := src.Annotations(tab, a.theme, 1, 3)

	if _, ok := anns[1]; !ok {
		t.Fatal("the first visible row must carry its run's heading")
	}
	if got := anns[1].Text; !strings.HasPrefix(got, "1111111") {
		t.Fatalf("top row = %q, want the run's own commit", got)
	}
	// And a window that contains the boundary still suppresses it there.
	if _, anns := src.Annotations(tab, a.theme, 0, 3); len(anns) != 3 {
		t.Fatalf("windowed from the top: %d headings, want 3 (lines 1, 3, 4)", len(anns))
	}
}

// TestBlameAnnotations_UncommittedLinesWearTheAddedColor checks the one
// color decision: your own not-yet-committed lines are marked in the
// same green the diff gutter uses for lines that exist nowhere else.
func TestBlameAnnotations_UncommittedLinesWearTheAddedColor(t *testing.T) {
	a, tab := blameFixture(t)
	fb := a.fileBlames[tab.Path]
	fb.lines[0] = blameLine{Hash: uncommittedHash, Author: "you", Age: "now"}
	src := gitBlameSource{app: a}

	_, anns := src.Annotations(tab, a.theme, 0, 3)

	if got := anns[0].FG; got != a.theme.GitAdded {
		t.Fatalf("uncommitted annotation color = %v, want the added color %v", got, a.theme.GitAdded)
	}
	if anns[1].FG != a.theme.Muted {
		t.Fatal("committed annotations stay muted")
	}
}

// TestBlameColumn_ShiftsTheCodeAndTheHitTestTogether is the integration
// point with the editor's new annotation primitive, and the reason that
// primitive had to live in the renderer rather than being painted over
// the code: the column takes ROOM, so every conversion between buffer
// columns and screen cells has to know about it. A hit-test that
// didn't would put the caret several characters from the click.
func TestBlameColumn_ShiftsTheCodeAndTheHitTestTogether(t *testing.T) {
	a, tab := blameFixture(t)
	_, _, ew, eh := a.editorRect()

	a.draw()
	start, end := tab.AnnotationCols()
	if end-start != a.fileBlames[tab.Path].width {
		t.Fatalf("annotation column = %d cells, want %d", end-start, a.fileBlames[tab.Path].width)
	}

	// The first code cell of line 1 sits just past the column and the
	// mark cell, and clicking it lands on column 0 — not on the column
	// the pre-blame layout would have put there.
	dx, dy, ok := tab.PosScreenCell(editor.Position{Line: 0, Col: 0}, ew, eh)
	if !ok {
		t.Fatal("line 1 column 0 should be on screen")
	}
	if dx != end+1 {
		t.Fatalf("code starts at %d, want %d (column ends at %d, then the mark cell)", dx, end+1, end)
	}
	pos, ok := tab.HitTest(dx, dy, ew, eh)
	if !ok || pos.Line != 0 || pos.Col != 0 {
		t.Fatalf("clicking the first code cell = %+v (ok=%v), want 0:0", pos, ok)
	}
	// And a click one cell further right is the second rune, not the
	// first — the off-by-one this whole arrangement risks.
	pos, _ = tab.HitTest(dx+1, dy, ew, eh)
	if pos.Col != 1 {
		t.Fatalf("the cell after the first rune = col %d, want 1", pos.Col)
	}
}

// TestBlameColumnPress_RevealsTheCommitWithoutMovingTheCaret is the
// feature's other half. The gesture aims at the margin, so it must not
// also do what a click on the code does — a caret that jumped would
// mean the user paid for the answer with their place in the file.
func TestBlameColumnPress_RevealsTheCommitWithoutMovingTheCaret(t *testing.T) {
	a, tab := blameFixture(t)
	tab.MoveCursorTo(editor.Position{Line: 3, Col: 2}, false)
	// The panel is already up with the commit in its list, so the reveal
	// is a selection change: opening it would re-read the history of a
	// directory that is not a repo and drop the seeded rows.
	a.gitLog.open = true
	a.gitLog.commits = []gitLogCommit{
		{Hash: "2222222222222222222222222222222222222222", Short: "2222222", Subject: "add the func"},
		{Hash: "1111111111111111111111111111111111111111", Short: "1111111", Subject: "the first commit"},
	}
	a.draw() // stamp the annotation column
	ex, ey, _, _ := a.editorRect()
	start, _ := tab.AnnotationCols()

	// Line 3 (0-based 2) is Ada's commit.
	if !a.blameColumnPress(ex+start+1, ey+2) {
		t.Fatal("a press inside the column must be consumed")
	}

	if !a.gitLog.open {
		t.Fatal("revealing a commit opens the log panel")
	}
	c, ok := a.gitLogSelectedCommit()
	if !ok || c.Short != "2222222" {
		t.Fatalf("selected commit = %+v (ok=%v), want 2222222", c, ok)
	}
	if tab.Cursor.Line != 3 || tab.Cursor.Col != 2 {
		t.Fatalf("the caret moved to %+v", tab.Cursor)
	}
}

// TestBlameColumnPress_BelongsToTheLineNotTheInk covers the run
// suppression's consequence for the mouse: line 2 shows no annotation
// because line 1 said it, but the column still belongs to line 2 and
// clicking it answers about line 2's commit.
func TestBlameColumnPress_BelongsToTheLineNotTheInk(t *testing.T) {
	a, tab := blameFixture(t)
	a.gitLog.open = true
	a.gitLog.commits = []gitLogCommit{
		{Hash: "1111111111111111111111111111111111111111", Short: "1111111"},
	}
	a.draw()
	ex, ey, _, _ := a.editorRect()
	start, _ := tab.AnnotationCols()

	if !a.blameColumnPress(ex+start, ey+1) { // the blank continuation row
		t.Fatal("the column is a click target on every row it spans")
	}
	c, _ := a.gitLogSelectedCommit()
	if c.Short != "1111111" {
		t.Fatalf("selected %+v, want the run's commit", c)
	}
}

// TestBlameColumnPress_UncommittedLineIsRefused: there is no diff to
// show for a line that has never been committed, and the click says so
// instead of opening a panel about someone else's commit.
func TestBlameColumnPress_UncommittedLineIsRefused(t *testing.T) {
	a, tab := blameFixture(t)
	a.fileBlames[tab.Path].lines[0] = blameLine{Hash: uncommittedHash, Author: "you"}
	a.draw()
	ex, ey, _, _ := a.editorRect()
	start, _ := tab.AnnotationCols()

	if !a.blameColumnPress(ex+start, ey) {
		t.Fatal("the press is still the column's")
	}
	if a.gitLog.open {
		t.Fatal("an uncommitted line has no commit to reveal")
	}
	if !strings.Contains(a.statusMsg, "isn't committed") {
		t.Fatalf("status = %q, want the refusal", a.statusMsg)
	}
}

// TestBlameColumnPress_IgnoredWhileOff makes sure the layer costs the
// mouse router nothing when it is off: with no column on screen, a
// press near the gutter is an ordinary editor click.
func TestBlameColumnPress_IgnoredWhileOff(t *testing.T) {
	a, tab := blameFixture(t)
	a.blameOn = false
	a.fileBlames = nil
	a.draw()
	ex, ey, _, _ := a.editorRect()
	start, end := tab.AnnotationCols()

	if start != end {
		t.Fatalf("no column should be stamped with blame off, got [%d,%d)", start, end)
	}
	if a.blameColumnPress(ex+start, ey) {
		t.Fatal("blame must not eat clicks while it is off")
	}
}

// TestHandleGitBlame_DropsAnOvertakenAnswer is the staleness rule, and
// with `--contents` it is a correctness rule rather than a tidiness
// one: every answer describes one exact revision of the buffer, so an
// early slow one landing last would annotate today's lines with
// yesterday's authorship.
func TestHandleGitBlame_DropsAnOvertakenAnswer(t *testing.T) {
	a, tab := blameFixture(t)
	a.fileBlames = nil
	a.blameSeq = map[string]int{tab.Path: 2}
	fresh := &fileBlame{lines: []blameLine{{Hash: "abc", Short: "abc"}}, width: 8}

	a.handleGitBlame(&gitBlameEvent{path: tab.Path, seq: 1, blame: fresh})
	if a.fileBlames[tab.Path] != nil {
		t.Fatal("an answer from before the latest request must be dropped")
	}

	a.handleGitBlame(&gitBlameEvent{path: tab.Path, seq: 2, blame: fresh})
	if a.fileBlames[tab.Path] != fresh {
		t.Fatal("the answer to the latest request installs")
	}
}

// TestBlameAfterEvent_ArmsOnlyWhenTheBufferHasMovedOn pins the settle
// pump: a column that describes the buffer on screen costs nothing, and
// an edit arms exactly one timer no matter how many events it took.
func TestBlameAfterEvent_ArmsOnlyWhenTheBufferHasMovedOn(t *testing.T) {
	a, tab := blameFixture(t)
	a.fileBlames[tab.Path].rev = tab.EditRev
	defer a.stopBlameTimer()

	a.blameAfterEvent()
	if a.blameTimer != nil {
		t.Fatal("annotations that match the buffer need no re-blame")
	}

	tab.InsertString("// mine\n")
	a.blameAfterEvent()
	if a.blameTimer == nil {
		t.Fatal("an edit should arm the settle timer")
	}
	first := a.blameTimer
	a.blameAfterEvent()
	if a.blameTimer != first {
		t.Fatal("a second look at the same revision must not re-arm")
	}
}

// TestMenuToggleBlame_DropsWhatItCannotKeepCurrent: the annotations
// belong to a revision of a buffer that will have moved on by the time
// anyone switches the layer back on, and showing yesterday's column for
// a second is worse than paying for a fork.
func TestMenuToggleBlame_DropsWhatItCannotKeepCurrent(t *testing.T) {
	a, tab := blameFixture(t)
	if a.blameToggleLabel() != "Hide blame" {
		t.Fatalf("label while on = %q", a.blameToggleLabel())
	}

	a.menuToggleBlame()

	if a.blameOn {
		t.Fatal("the toggle should have switched it off")
	}
	if a.fileBlames != nil {
		t.Fatal("stale annotations must not survive the toggle")
	}
	if a.blameToggleLabel() != "Show blame" {
		t.Fatalf("label while off = %q", a.blameToggleLabel())
	}
	// And with it off, the source asks for no column at all.
	if w, _ := (gitBlameSource{app: a}).Annotations(tab, a.theme, 0, 3); w != 0 {
		t.Fatalf("width with no data = %d, want 0", w)
	}
}

// TestMenuBlameCommit_AnswersAboutOneLineWithoutTurningTheLayerOn is
// the keyboard twin's contract. The row asks about the caret's line;
// showing the whole column would be the menu answering a question of
// its own, and on a file nobody has blamed the answer simply arrives
// when git does rather than making the user press the row twice.
func TestMenuBlameCommit_AnswersAboutOneLineWithoutTurningTheLayerOn(t *testing.T) {
	a, tab := blameFixture(t)
	loaded := a.fileBlames[tab.Path]
	a.blameOn = false
	a.fileBlames = nil
	a.gitLog.open = true
	a.gitLog.commits = []gitLogCommit{
		{Hash: "2222222222222222222222222222222222222222", Short: "2222222"},
	}
	tab.MoveCursorTo(editor.Position{Line: 2, Col: 0}, false)

	a.menuBlameCommit()

	if a.blameOn {
		t.Fatal("asking about one line must not switch the column on")
	}
	if a.blamePending == nil || a.blamePending.line != 2 {
		t.Fatalf("the question should be parked for the answer, got %+v", a.blamePending)
	}

	// git answers; the parked question is what turns it into a reveal.
	a.handleGitBlame(&gitBlameEvent{path: tab.Path, seq: a.blameSeq[tab.Path], blame: loaded})

	if a.blamePending != nil {
		t.Fatal("a parked question is spent once answered")
	}
	c, ok := a.gitLogSelectedCommit()
	if !ok || c.Short != "2222222" {
		t.Fatalf("selected %+v, want the caret line's commit", c)
	}
	// And the data it loaded still paints nothing, because the layer is
	// off.
	if w, _ := (gitBlameSource{app: a}).Annotations(tab, a.theme, 0, 3); w != 0 {
		t.Fatalf("column width with the layer off = %d, want 0", w)
	}
}

// TestLoadFileBlame_EndToEnd runs the real thing against a real
// repository: two commits by two authors, plus an unsaved line that
// exists only in the "buffer" handed to git on stdin. The last part is
// the point — blaming the file on DISK would have attributed the lines
// after an unsaved insertion to whatever used to be at those numbers.
func TestLoadFileBlame_EndToEnd(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t)
	path := filepath.Join(repo, "main.go")

	writeFileT(t, path, "package main\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "first")

	writeFileT(t, path, "package main\n\nfunc main() {}\n")
	gitRun(t, repo, "-c", "user.name=Ada Lovelace", "-c", "user.email=ada@example.com",
		"commit", "-q", "-a", "-m", "add the func")

	// The buffer: the committed file plus a line the user has typed and
	// not saved, inserted ABOVE the second commit's work.
	buffer := "package main\n\n// just typed\nfunc main() {}\n"
	fb := loadFileBlame(repo, path, buffer, time.Now())
	if fb == nil {
		t.Fatal("blame returned nothing for a tracked file")
	}
	if len(fb.lines) != 4 {
		t.Fatalf("blamed %d lines, want 4", len(fb.lines))
	}
	if fb.lines[0].Author != "Test" {
		t.Fatalf("line 1 author = %q, want the first committer", fb.lines[0].Author)
	}
	if fb.lines[2].committed() {
		t.Fatalf("the unsaved line should have no commit: %+v", fb.lines[2])
	}
	// The line that moved DOWN because of the insertion still carries
	// its own commit — the whole reason blame reads the buffer.
	if fb.lines[3].Author != "Ada" || fb.lines[3].Summary != "add the func" {
		t.Fatalf("line 4 = %+v, want Ada's commit", fb.lines[3])
	}
	if fb.width == 0 {
		t.Fatal("a blamed file needs a column width")
	}
	if fb.lines[0].Hash == fb.lines[3].Hash {
		t.Fatal("two commits should not share a hash")
	}
}

// TestLoadFileBlame_BestEffort: everything that isn't a tracked file in
// a repo yields no column, silently, the same contract the diff gutter
// and every other git read here follow.
func TestLoadFileBlame_BestEffort(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	plain := t.TempDir()
	writeFileT(t, filepath.Join(plain, "a.txt"), "hi\n")
	if fb := loadFileBlame(plain, filepath.Join(plain, "a.txt"), "hi\n", time.Now()); fb != nil {
		t.Fatal("a directory that is not a repo has no blame")
	}

	repo := initRepo(t)
	writeFileT(t, filepath.Join(repo, "new.txt"), "hi\n")
	if fb := loadFileBlame(repo, filepath.Join(repo, "new.txt"), "hi\n", time.Now()); fb != nil {
		t.Fatal("an untracked file has no blame")
	}
	if fb := loadFileBlame("", "", "", time.Now()); fb != nil {
		t.Fatal("no root, no blame")
	}
}
