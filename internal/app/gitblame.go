// =============================================================================
// File: internal/app/gitblame.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitblame.go is the blame layer: who last touched each line, in a
// column beside the code, and a click that takes you to the commit
// that did it.
//
// It is built on the annotation primitive added to the decoration layer
// for it (editor/decoration.go's AnnotationSource), because blame is
// the first overlay whose content is TEXT the file does not contain —
// a Span can only restyle the user's own characters and a GutterMark is
// one cell wide. The layer supplies the column; this file supplies what
// goes in it and what a click on it means.
//
// Four decisions carry the design:
//
//  1. **It blames the BUFFER, not the file on disk.** `git blame
//     --contents -` takes the text on stdin, so the annotations line up
//     with what is on screen even with unsaved edits, and lines the
//     user just typed come back as "Uncommitted" instead of silently
//     wearing the blame of whatever used to be at that line number.
//     Blaming the saved file would misattribute every line below an
//     unsaved insertion — annotations that are confidently wrong, which
//     is worse than none.
//
//  2. **The column width is a property of the FILE, not the window.**
//     It is measured once per result, from every line, and stays put
//     while you scroll. A width that tracked the visible rows would
//     slide the code sideways as it scrolled, which is unreadable in a
//     way no amount of correctness makes up for.
//
//  3. **A run of lines from one commit is annotated once.** Blame's
//     value is in its boundaries — where the authorship CHANGES — and
//     repeating "a3f2c1 rohan 3d" down forty lines buries them. The
//     whole run still answers a click: the column belongs to the line,
//     not to the text drawn on it.
//
//  4. **Staleness is a debounce, like everything else that watches
//     typing.** EditRev (autosave's signature) says the buffer moved;
//     a settle timer re-blames when it stops. The previous answer stays
//     on screen meanwhile — blanking the column between keystrokes
//     would be a flicker the user cannot switch off.
//
// The click is the other half of the feature and it goes through the
// git log panel: reveal the commit there and its `git show` — metadata,
// stat and patch — is already the panel's detail pane. A blamed commit
// is routinely older than the 400 the panel loads, which is what the
// log filter's `#` mode exists for (gitlogfilter.go).
package app

import (
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/theme"
)

// blameSettle is the pause after typing that re-blames the buffer.
// Deliberately much longer than the syntax settle (150ms) and the
// completion debounce: this is a fork of git that reads the whole
// history of one file, and the annotations it produces are ambient
// context nobody is waiting on.
const blameSettle = 900 * time.Millisecond

// uncommittedHash is what git blame reports for a line that is not in
// any commit yet — the all-zero object id.
const uncommittedHash = "0000000000000000000000000000000000000000"

// blameAuthorMax caps the author slot. Long enough for the given names
// a team recognizes, short enough that the column stays a margin note
// rather than a second document. The full name is in the flash a click
// produces, so nothing is unreachable — only unrepeated.
const blameAuthorMax = 10

// blameLine is one line's authorship. Hash is the full object id (every
// git verb wants it); Short is what the column shows.
type blameLine struct {
	Hash    string
	Short   string
	Author  string // given name, for the column
	Full    string // the author name as git spells it, for the flash
	Age     string // "3d", "8mo", "now"
	Summary string // the commit subject
}

// committed reports whether this line belongs to a commit yet. An
// uncommitted line has no diff to reveal, which is the one refusal the
// click path has to make.
func (b blameLine) committed() bool { return b.Hash != "" && b.Hash != uncommittedHash }

// fileBlame is one file's finished blame: a line per buffer line, the
// column width they need, and the buffer revision they describe.
type fileBlame struct {
	lines []blameLine
	width int
	rev   int // the tab's EditRev when the blame was asked for.
}

// at returns the blame for a 0-based buffer line, ok=false past the end
// of what git answered about (the buffer has grown since).
func (fb *fileBlame) at(line int) (blameLine, bool) {
	if fb == nil || line < 0 || line >= len(fb.lines) {
		return blameLine{}, false
	}
	return fb.lines[line], true
}

// -----------------------------------------------------------------------------
// Parsing
// -----------------------------------------------------------------------------

// parseBlamePorcelain decodes `git blame --porcelain` into one entry per
// final-file line.
//
// The format is a header line ("<hash> <origLine> <finalLine> [<count>]")
// followed by key/value lines and then the source line prefixed with a
// TAB. The commit's details appear only the FIRST time that commit is
// seen, so the parser carries a hash→details map and fills later groups
// from it — which is also why this is `--porcelain` and not
// `--line-porcelain`: on a file whose history is a handful of commits,
// the repeating form is several times the output for the same answer.
//
// now is passed in rather than read from the clock so the relative ages
// are testable; every caller passes time.Now().
func parseBlamePorcelain(out []byte, now time.Time) []blameLine {
	type commit struct {
		author  string
		when    time.Time
		summary string
	}
	commits := map[string]*commit{}
	var lines []blameLine
	var cur *commit
	curHash := ""
	curLine := 0

	set := func(line int, b blameLine) {
		if line < 1 {
			return
		}
		for len(lines) < line {
			lines = append(lines, blameLine{})
		}
		lines[line-1] = b
	}

	for _, raw := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(raw, "\t") {
			// The source line itself: the current group's details apply
			// to it, and the next one is the line after.
			if cur != nil {
				b := blameLine{
					Hash:    curHash,
					Short:   shortHash(curHash),
					Author:  blameGivenName(cur.author),
					Full:    cur.author,
					Age:     relativeAge(cur.when, now),
					Summary: cur.summary,
				}
				// Who a not-yet-committed line belongs to is decided by
				// the HASH, never by the name git puts on it: the name
				// depends on how it was asked. Plain `git blame` says
				// "Not Committed Yet"; with `--contents` it says
				// "External file (--contents)", which is the mechanism
				// leaking into the margin — the user did not hand git a
				// file, they typed a line.
				if !b.committed() {
					b.Author, b.Full, b.Summary = "you", "you", "not committed yet"
				}
				set(curLine, b)
			}
			curLine++
			continue
		}
		if isBlameHeader(raw) {
			fields := strings.Fields(raw)
			curHash = fields[0]
			curLine, _ = strconv.Atoi(fields[2])
			if commits[curHash] == nil {
				commits[curHash] = &commit{}
			}
			cur = commits[curHash]
			continue
		}
		if cur == nil {
			continue
		}
		// Detail lines. Only the three the column and the flash use are
		// read; the rest (author-mail, committer-*, previous, filename,
		// boundary) are skipped rather than stored.
		switch {
		case strings.HasPrefix(raw, "author "):
			cur.author = strings.TrimPrefix(raw, "author ")
		case strings.HasPrefix(raw, "author-time "):
			if secs, err := strconv.ParseInt(strings.TrimPrefix(raw, "author-time "), 10, 64); err == nil {
				cur.when = time.Unix(secs, 0)
			}
		case strings.HasPrefix(raw, "summary "):
			cur.summary = strings.TrimPrefix(raw, "summary ")
		}
	}
	return lines
}

// isBlameHeader recognizes the group header — 40 hex digits followed by
// at least two numbers. Checked by shape rather than by "not a known
// key", because a commit summary is arbitrary text and the parser must
// never mistake one for a new group.
func isBlameHeader(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 3 || len(fields[0]) != 40 {
		return false
	}
	for _, r := range fields[0] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	_, err1 := strconv.Atoi(fields[1])
	_, err2 := strconv.Atoi(fields[2])
	return err1 == nil && err2 == nil
}

// shortHash abbreviates to git's customary seven, and answers "" for
// the uncommitted id — the column shows a dash there instead of seven
// zeros pretending to be a commit anyone could look up.
func shortHash(hash string) string {
	if hash == "" || hash == uncommittedHash {
		return ""
	}
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

// blameGivenName trims an author to what the column can hold: the first
// word of the name, elided if even that is too long. "Rohan Allison" is
// "Rohan"; a team where that is ambiguous still has the full name one
// click away.
func blameGivenName(author string) string {
	author = strings.TrimSpace(author)
	if author == "" {
		return ""
	}
	if i := strings.IndexByte(author, ' '); i > 0 {
		author = author[:i]
	}
	return elide(author, blameAuthorMax)
}

// relativeAge is a compact "how long ago": the column has room for a
// number and a unit, not for "3 days ago". Deliberately coarse — the
// question blame answers is "is this line ancient or did it land this
// week", and a reader who needs the date clicks through to the commit.
func relativeAge(when, now time.Time) string {
	if when.IsZero() {
		return ""
	}
	d := now.Sub(when)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	case d < 31*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	case d < 365*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/24/30)) + "mo"
	default:
		return strconv.Itoa(int(d.Hours()/24/365)) + "y"
	}
}

// blameColumnWidth measures the column one file's annotations need:
// the widest of each part, assembled the way blameText assembles them.
// Measured over EVERY line rather than the visible ones so the column
// never changes width under a scroll.
func blameColumnWidth(lines []blameLine) int {
	if len(lines) == 0 {
		return 0
	}
	hashW, authorW, ageW := 0, 0, 0
	for _, b := range lines {
		if b.Hash == "" {
			continue
		}
		hashW = max(hashW, runeLen(blameHashSlot(b)))
		authorW = max(authorW, runeLen(b.Author))
		ageW = max(ageW, runeLen(b.Age))
	}
	if hashW == 0 && authorW == 0 {
		return 0
	}
	// hash + " " + author + " " + age + one trailing pad, so the column
	// never touches the mark cell beside it.
	return hashW + 1 + authorW + 1 + ageW + 1
}

// blameHashSlot is the hash as the column shows it — a dash for a line
// that has never been committed, which reads as "nothing to look up"
// rather than as an object id.
func blameHashSlot(b blameLine) string {
	if !b.committed() {
		return "—"
	}
	return b.Short
}

// blameText renders one annotation into a width-cell column:
//
//	a3f2c1 rohan   3d
//
// The age is right-aligned against the column's end so the units line
// up down the page and the eye can read the ages as a single sorted
// list, which is how a blame column is actually scanned.
func blameText(b blameLine, width int) string {
	if width <= 0 || b.Hash == "" {
		return ""
	}
	head := blameHashSlot(b) + " " + b.Author
	// -1 for the trailing pad column blameColumnWidth reserved.
	body := width - 1
	if runeLen(head)+1+runeLen(b.Age) > body {
		return elide(head, body)
	}
	pad := body - runeLen(head) - runeLen(b.Age)
	return head + strings.Repeat(" ", pad) + b.Age
}

// -----------------------------------------------------------------------------
// Loading — goroutine side
// -----------------------------------------------------------------------------

// loadFileBlame runs git blame over contents and decodes it. Nil means
// "no answer" — not a repo, no git, a file with no history, a path git
// has never heard of — and the column simply doesn't appear, the
// best-effort rule every git read here follows.
//
// `--contents -` is what makes the annotations describe the BUFFER: git
// diffs the supplied text against the file's history, so unsaved lines
// come back under the all-zero hash instead of shifting everything
// below them onto the wrong commits.
func loadFileBlame(rootDir, path, contents string, now time.Time) *fileBlame {
	if rootDir == "" || path == "" {
		return nil
	}
	cmd := exec.Command("git", "-C", rootDir, "blame", "--porcelain",
		"--contents", "-", "--", path)
	cmd.Stdin = strings.NewReader(contents)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	lines := parseBlamePorcelain(out, now)
	if len(lines) == 0 {
		return nil
	}
	return &fileBlame{lines: lines, width: blameColumnWidth(lines)}
}

// gitBlameEvent carries one finished blame back to the main loop,
// stamped with the request sequence it was born under.
type gitBlameEvent struct {
	when  time.Time
	path  string
	seq   int
	blame *fileBlame
}

// When satisfies the tcell.Event interface.
func (e *gitBlameEvent) When() time.Time { return e.when }

// requestFileBlame blames tab's buffer on a goroutine and posts the
// result. The buffer text is snapshotted HERE, on the main loop —
// reading it from the goroutine would be a data race against the next
// keystroke, and the iron rule is that background work never touches
// tab state.
// Callers gate on blameOn where the request is AMBIENT (a tab opening,
// a save); the one deliberate caller — "Blame this line…" on a file
// nobody has blamed — does not, which is why the check is not in here.
func (a *App) requestFileBlame(t *editor.Tab) {
	if t == nil || t.Path == "" || t.IsImage() || a.screen == nil {
		return
	}
	if a.blameSeq == nil {
		a.blameSeq = map[string]int{}
	}
	a.blameSeq[t.Path]++
	seq := a.blameSeq[t.Path]
	rev := t.EditRev
	contents := t.Buffer.String()
	scr, root, path := a.screen, a.rootDir, t.Path
	go func() {
		fb := loadFileBlame(root, path, contents, time.Now())
		if fb != nil {
			fb.rev = rev
		}
		_ = scr.PostEvent(&gitBlameEvent{when: time.Now(), path: path, seq: seq, blame: fb})
	}()
}

// handleGitBlame installs a finished blame. Results are dropped when a
// newer request for the same path is already out: with `--contents`
// every answer describes one exact revision of the buffer, so a slow
// early one landing last would annotate today's lines with yesterday's
// authorship — the one failure mode this feature must not have.
func (a *App) handleGitBlame(e *gitBlameEvent) {
	if a.blameSeq[e.path] != e.seq {
		return
	}
	if a.fileBlames == nil {
		a.fileBlames = map[string]*fileBlame{}
	}
	if e.blame == nil {
		delete(a.fileBlames, e.path)
		a.blamePending = nil
		return
	}
	a.fileBlames[e.path] = e.blame
	// A question asked before there was an answer: "Blame this line…" on
	// a file nobody had blamed yet parked the line here rather than
	// making the user press the row twice, once to load and once to ask.
	if p := a.blamePending; p != nil && p.path == e.path {
		a.blamePending = nil
		if b, ok := e.blame.at(p.line); ok {
			a.revealBlameCommit(b)
		}
	}
}

// blamePendingReveal is a "tell me about this line once you know" — the
// deliberate blame request's parked question. One at a time: the user
// asked about a line, and an older ask is answered by the newer one.
type blamePendingReveal struct {
	path string
	line int
}

// -----------------------------------------------------------------------------
// Staleness — the settle timer
// -----------------------------------------------------------------------------

// blameTickEvent is the settle timer firing: "the buffer has been still
// for blameSettle, re-blame it". An event rather than a direct call
// because the timer runs off the main loop.
type blameTickEvent struct {
	when time.Time
}

// When satisfies the tcell.Event interface.
func (e *blameTickEvent) When() time.Time { return e.when }

// blameAfterEvent runs in the after-event slot beside autoSaveAfterEvent
// and re-arms the settle timer whenever the active tab's buffer has
// moved away from the revision its annotations describe. EditRev is the
// same signature auto-save uses, for the same reason: every mutation
// path already bumps it, so this hooks none of them.
func (a *App) blameAfterEvent() {
	if !a.blameOn {
		a.stopBlameTimer()
		a.blameSig = ""
		return
	}
	t := a.activeTabPtr()
	if t == nil || t.Path == "" {
		return
	}
	fb := a.fileBlames[t.Path]
	if fb != nil && fb.rev == t.EditRev {
		return // the column describes what is on screen.
	}
	// The signature is path AND revision, not revision alone: two tabs
	// sitting at the same EditRev are two different questions, and a
	// bare revision would let a switch between them look like "nothing
	// changed". It is also what keeps this from re-forking git on every
	// event while an answer is in flight — the signature only moves when
	// the user does.
	sig := t.Path + ":" + itoa(t.EditRev)
	if sig == a.blameSig {
		return
	}
	a.blameSig = sig
	if fb == nil {
		// Nothing on screen for this file — a tab just opened or the
		// layer just came on. Ask NOW: a debounce here would mean an
		// empty column for a second every time the user changes tabs,
		// which reads as the feature being broken rather than busy.
		a.requestFileBlame(t)
		return
	}
	a.armBlameTimer()
}

// armBlameTimer (re)starts the settle debounce. Re-armed, never
// stacked, so a typing burst costs one blame.
func (a *App) armBlameTimer() {
	a.stopBlameTimer()
	scr := a.screen
	if scr == nil {
		return
	}
	a.blameTimer = time.AfterFunc(blameSettle, func() {
		// Goroutine territory: post, never mutate.
		_ = scr.PostEvent(&blameTickEvent{when: time.Now()})
	})
}

// stopBlameTimer cancels a pending settle, if any.
func (a *App) stopBlameTimer() {
	if a.blameTimer != nil {
		a.blameTimer.Stop()
		a.blameTimer = nil
	}
}

// handleBlameTick is the settle landing on the main loop: blame the
// active tab as it stands now. Everything that could have changed
// during the wait (blame switched off, tab closed, another file in
// front) is re-derived rather than remembered.
func (a *App) handleBlameTick() {
	a.blameTimer = nil
	if !a.blameOn {
		return
	}
	a.requestFileBlame(a.activeTabPtr())
}

// -----------------------------------------------------------------------------
// The toggle
// -----------------------------------------------------------------------------

// menuToggleBlame is the ≡ Git row / Esc-A entry point. Turning it on
// blames the file in front of you immediately — the annotations are the
// answer to the gesture, so waiting for a tick would make the toggle
// feel broken.
func (a *App) menuToggleBlame() {
	a.closeMenu()
	if !a.blameOn && !a.gitIsRepo {
		return
	}
	a.blameOn = !a.blameOn
	if !a.blameOn {
		a.stopBlameTimer()
		// The results are dropped with the toggle rather than kept warm:
		// they are a revision of a buffer that will have moved on by the
		// time anyone asks again, and a stale column is worse than a
		// fork.
		a.fileBlames = nil
		return
	}
	a.requestFileBlame(a.activeTabPtr())
}

// blameToggleLabel reads as the action the row will perform, the
// convention every other toggle row in the menu follows.
func (a *App) blameToggleLabel() string {
	if a.blameOn {
		return "Hide blame"
	}
	return "Show blame"
}

// -----------------------------------------------------------------------------
// The decoration source
// -----------------------------------------------------------------------------

// gitBlameSource adapts App.fileBlames into the editor's annotation
// column. It holds the App pointer for the same reason gitDiffSource
// does: blame is app state keyed by path, and the decoration layer only
// knows tabs.
type gitBlameSource struct {
	app *App
}

// Decorations satisfies DecorationSource. Blame paints no spans and no
// gutter marks — it recolors nothing and claims no cell of the mark
// column, whose single slot is spoken for by git, plugins and the LSP.
func (gitBlameSource) Decorations(*editor.Tab, theme.Theme, int, int) ([]editor.Span, []editor.GutterMark) {
	return nil, nil
}

// Annotations is the column itself.
//
// Runs of lines from one commit are annotated only where the run
// STARTS ON SCREEN: at the boundary where authorship changes, or at the
// top row when the run began further up. Repeating the same seventeen
// characters down a function would bury the boundaries that are the
// whole point — but suppressing them against the FILE rather than the
// viewport is worse, and was the first thing this code did: scroll into
// the middle of a file written in one commit and the column is a
// eighteen-cell margin of nothing, on a screen where the answer is
// "all of this is that one commit". The top row is a heading, the rows
// below it are boundaries, and neither is ever blank for want of
// context that scrolled away.
func (s gitBlameSource) Annotations(t *editor.Tab, th theme.Theme, firstLine, lastLine int) (int, map[int]editor.LineAnnotation) {
	// blameOn is checked HERE and not only at load time, because a
	// deliberate "blame this line" loads the same data without asking
	// for the column — and a column that appeared in answer to a
	// question about one line would be the feature switching itself on.
	if !s.app.blameOn {
		return 0, nil
	}
	fb := s.app.fileBlames[t.Path]
	if fb == nil || fb.width == 0 {
		return 0, nil
	}
	out := make(map[int]editor.LineAnnotation, lastLine-firstLine+1)
	for line := max(firstLine, 0); line <= lastLine; line++ {
		b, ok := fb.at(line)
		if !ok || b.Hash == "" {
			continue
		}
		if line > firstLine {
			if prev, ok := fb.at(line - 1); ok && prev.Hash == b.Hash {
				continue // same commit as the line above — not a boundary.
			}
		}
		fg := th.Muted
		if !b.committed() {
			// Your own unsaved work, in the color the diff gutter already
			// uses for lines that exist nowhere else.
			fg = th.GitAdded
		}
		out[line] = editor.LineAnnotation{Text: blameText(b, fb.width), FG: fg}
	}
	return fb.width, out
}

// -----------------------------------------------------------------------------
// The click
// -----------------------------------------------------------------------------

// blameColumnPress handles a press inside the annotation column,
// reporting whether it consumed the event. Consuming is the point: the
// column sits where a click used to mean "caret to column 0 of this
// line", and a gesture that both revealed a commit and moved the caret
// would be two answers to one question.
//
// A click anywhere in the column belongs to that LINE, including the
// rows of a run whose annotation is drawn further up. The column is
// about the line it sits beside; only the ink is shared.
func (a *App) blameColumnPress(x, y int) bool {
	if !a.blameOn {
		return false
	}
	t := a.activeTabPtr()
	if t == nil || t.IsImage() {
		return false
	}
	ex, ey, _, eh := a.editorRect()
	start, end := t.AnnotationCols()
	if start == end {
		return false
	}
	lx, ly := x-ex, y-ey
	if lx < start || lx >= end || ly < 0 || ly >= eh {
		return false
	}
	line := t.ScrollY + ly
	b, ok := a.fileBlames[t.Path].at(line)
	if !ok || b.Hash == "" {
		return true // inside the column, nothing to reveal — still ours.
	}
	a.revealBlameCommit(b)
	return true
}

// revealBlameCommit opens the git log on the commit that wrote this
// line. The panel's detail pane is already `git show` — metadata, stat
// and patch — so "which commit did this, and what else did it change"
// is one click and no new surface.
func (a *App) revealBlameCommit(b blameLine) {
	if !b.committed() {
		a.flash("That line isn't committed yet")
		return
	}
	a.flash(b.Short + " " + b.Full + " — " + b.Summary)
	a.revealGitLogCommit(b.Hash)
}

// -----------------------------------------------------------------------------
// Menu / keyboard twins for the click
// -----------------------------------------------------------------------------

// hasBlameForCursor is the predicate for the row below: enabled only
// when the caret's line has a commit to show.
func (a *App) hasBlameForCursor() bool {
	b, ok := a.blameForCursor()
	return ok && b.committed()
}

// blameForCursor returns the blame of the caret's line.
func (a *App) blameForCursor() (blameLine, bool) {
	t := a.activeTabPtr()
	if t == nil {
		return blameLine{}, false
	}
	return a.fileBlames[t.Path].at(t.Cursor.Line)
}

// menuBlameCommit is the keyboard twin of clicking an annotation — the
// house rule that every mouse verb has one, and the only route to this
// commit in a terminal that eats clicks. It works with the column
// hidden: the question is about the caret's line either way, so
// switching blame on first would be the menu asking the user to do half
// the work.
func (a *App) menuBlameCommit() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil || t.Path == "" {
		return
	}
	b, ok := a.blameForCursor()
	if ok {
		a.revealBlameCommit(b)
		return
	}
	// Nothing known about this file yet. Ask git, and park the question
	// so the answer arrives as the commit rather than as a column the
	// user never asked to see: this row is about ONE line, and turning
	// the whole layer on as a side effect would be the menu answering a
	// question of its own.
	if !a.gitIsRepo {
		return
	}
	a.blamePending = &blamePendingReveal{path: t.Path, line: t.Cursor.Line}
	a.requestFileBlame(t)
	a.flash("Blaming " + t.DisplayName() + "…")
}

// Compile-time check that the source really satisfies both halves of
// the decoration contract — the plain one and the annotation column.
var _ editor.AnnotationSource = gitBlameSource{}
