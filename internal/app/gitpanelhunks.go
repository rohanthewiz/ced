// =============================================================================
// File: internal/app/gitpanelhunks.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitpanelhunks.go gives the git panel's diff pane per-HUNK verbs:
// stage, unstage, and revert one region of one file without leaving the
// review strip. It is the half of the pre-commit survey that lets a
// reviewer act on what they just read — the other half (walking the
// files) lives in gitpanelwalk.go.
//
// The whole file exists to answer one question honestly: WHICH DIFF is
// the row under the pointer part of, and therefore which git command
// can address it?
//
//	git apply --cached      stages a hunk   (index  ← patch)
//	git apply --cached -R   unstages a hunk (index  ← reverse patch)
//	git apply -R            reverts a hunk  (worktree ← reverse patch)
//	git apply --index -R    reverts both    (index AND worktree)
//
// git checks a patch's CONTEXT against whatever it is applying to, so a
// hunk lifted from the wrong diff is rejected — or worse, accepted
// against a file that happens to still match. That is why the panel's
// diff pane no longer shows the single union `git diff HEAD`: a hunk of
// the union belongs to neither the index nor the work tree once a file
// is half-staged ("MM"), and staging one hunk of a file is precisely
// what MAKES it half-staged. See loadGitPanelDiff for the two-source
// fetch and the marker lines that label the two sections.
//
// House patterns in play:
//
//   - One geometry source (the btnRect rule): gitPanelHunkChipsAt is
//     called by the drawer AND by the click router, so a chip can never
//     be painted somewhere it cannot be clicked.
//   - Async git (gitcmd.go's rule): every apply goes through
//     runGitApplyPatch, so a rejected patch lands in the info modal
//     with git's own words instead of being guessed at here.
//   - Destructive verbs confirm first (fileops.go's rule): revert
//     throws work away, so it names the file and the hunk before it
//     runs. Stage and unstage move a change between two places git is
//     still holding it, so they don't.

package app

import (
	"strings"
)

// diffSide names which of git's two diffs a run of display lines came
// from — the fact that decides which command can address a hunk.
//
//	sideNone      no addressable diff (an untracked file's synthesized
//	              all-added view, or the `diff HEAD` fallback whose
//	              hunks belong to neither side cleanly). No verbs.
//	sideUnstaged  `git diff`         — work tree vs index
//	sideStaged    `git diff --cached`— index vs HEAD
//	sideMixed     both, concatenated and split by the marker lines
type diffSide uint8

const (
	sideNone diffSide = iota
	sideUnstaged
	sideStaged
	sideMixed
)

// The section markers a mixed diff is split by. They are drawn as
// ordinary rows, so they must be impossible to confuse with real diff
// content: every line git emits inside a patch starts with ' ', '+',
// '-', '\', '@', or an ASCII header word, and none of them can start
// with a box-drawing rune. Staged comes first because it reads in
// commit order — what the index already holds, then what it doesn't.
const (
	gitPanelStagedMarker   = "── staged (index) ──"
	gitPanelUnstagedMarker = "── unstaged (work tree) ──"
)

// isGitPanelMarker reports whether a display line is one of the section
// markers rather than diff text. Used by the span walker, the line
// mapper (diffTargetLine), and the drawer's styling.
func isGitPanelMarker(line string) bool {
	return line == gitPanelStagedMarker || line == gitPanelUnstagedMarker
}

// gitHunkVerb is one of the three things that can be done to a hunk.
type gitHunkVerb uint8

const (
	hunkStage gitHunkVerb = iota
	hunkUnstage
	hunkRevert
)

// gitHunkSpan is one hunk as it sits in the display lines: the row of
// its "@@" header, the row of its last body line (inclusive), and the
// diff it belongs to. Rows are indexes into gitPanel.diffLines, which
// is the coordinate space both the drawer and the click router work
// in, so nothing has to be translated at either end.
type gitHunkSpan struct {
	header int
	end    int
	side   diffSide
}

// gitPanelHunkSpans finds every addressable hunk in a rendered diff.
// side is the whole pane's side; sideMixed means the marker lines
// decide, and any hunk appearing before the first marker (which cannot
// happen with loadGitPanelDiff's output, but could with a hand-built
// list) is left unaddressable rather than guessed at.
//
// A hunk runs from its "@@" line to the row before whatever ends it:
// the next hunk, a marker, or a second file header. The trailing
// "\ No newline at end of file" line is body text and rides along,
// which matters — git rejects a patch that drops it.
func gitPanelHunkSpans(lines []string, side diffSide) []gitHunkSpan {
	var spans []gitHunkSpan
	cur := side
	if side == sideMixed {
		cur = sideNone // until a marker says otherwise
	}
	open := -1
	closeAt := func(end int) {
		if open >= 0 {
			spans[open].end = end
			open = -1
		}
	}
	for i, ln := range lines {
		switch {
		case isGitPanelMarker(ln):
			closeAt(i - 1)
			if ln == gitPanelStagedMarker {
				cur = sideStaged
			} else {
				cur = sideUnstaged
			}
		case strings.HasPrefix(ln, "@@"):
			closeAt(i - 1)
			if cur == sideNone || cur == sideMixed {
				continue
			}
			spans = append(spans, gitHunkSpan{header: i, end: i, side: cur})
			open = len(spans) - 1
		case strings.HasPrefix(ln, "diff --git "):
			// A second file inside one pane's diff (a rename pair, or a
			// hand-assembled list). The previous hunk ends here.
			closeAt(i - 1)
		}
	}
	closeAt(len(lines) - 1)
	return spans
}

// gitPanelHunkAt returns the span whose header sits on display row idx.
// Only the header row carries verbs: it is the one row that names the
// whole hunk, and putting chips on body rows would mean a click target
// over the code being read.
func gitPanelHunkAt(spans []gitHunkSpan, idx int) (gitHunkSpan, bool) {
	for _, sp := range spans {
		if sp.header == idx {
			return sp, true
		}
	}
	return gitHunkSpan{}, false
}

// gitPanelHunkVerbs is which verbs a span may be offered, in draw
// order. The rules follow from what each command checks its context
// against:
//
//   - An unstaged hunk's old side IS the index and its new side IS the
//     work tree, so it can always be staged (--cached) and always be
//     reverted (-R against the work tree).
//   - A staged hunk's old side is HEAD and its new side is the index,
//     so it can always be unstaged (--cached -R).
//   - Reverting a staged hunk has to remove it from the index AND the
//     work tree (--index -R), which git only allows when the two agree.
//     On a mixed file they do not, so the row is not offered — the
//     honest sequence there is unstage first, then revert.
func gitPanelHunkVerbs(sp gitHunkSpan, pane diffSide) []gitHunkVerb {
	switch sp.side {
	case sideUnstaged:
		return []gitHunkVerb{hunkStage, hunkRevert}
	case sideStaged:
		if pane == sideMixed {
			return []gitHunkVerb{hunkUnstage}
		}
		return []gitHunkVerb{hunkUnstage, hunkRevert}
	}
	return nil
}

// gitHunkChipLabel is the three-cell glyph for a verb. Deliberately not
// words: the chips ride at the right edge of a hunk header that already
// carries git's own section text, and three cells is what a narrow pane
// can spare. The picker (gitPanelHunkItems) is where the verbs are
// spelled out, and it is also the keyboard route to them.
func gitHunkChipLabel(v gitHunkVerb) string {
	switch v {
	case hunkStage:
		return "[+]"
	case hunkUnstage:
		return "[−]"
	}
	return "[↺]"
}

// gitHunkVerbName is the verb's name in a picker row, a confirm, and a
// flash — one source so the three surfaces can't drift.
func gitHunkVerbName(v gitHunkVerb) string {
	switch v {
	case hunkStage:
		return "Stage"
	case hunkUnstage:
		return "Unstage"
	}
	return "Revert"
}

// gitHunkChip is one drawn-and-clickable verb on a hunk header row.
type gitHunkChip struct {
	rect btnRect
	verb gitHunkVerb
	span gitHunkSpan
}

// gitPanelHunkChipsAt returns the chips for display row idx of the diff
// pane, positioned on screen. Empty when the row is not a hunk header,
// when the hunk has no verbs, or when the pane is too narrow to carry
// the chips without eating the hunk header's own text — a control
// painted over the text it describes is worse than no control, and the
// picker still reaches the same verbs.
//
// Both the drawer and the click router call this, so the chips cannot
// be painted somewhere they can't be clicked (the btnRect house rule).
func (a *App) gitPanelHunkChipsAt(idx int) []gitHunkChip {
	if !a.gitPanel.open {
		return nil
	}
	sp, ok := gitPanelHunkAt(a.gitPanelHunkSpans(), idx)
	if !ok {
		return nil
	}
	verbs := gitPanelHunkVerbs(sp, a.gitPanel.diffSide)
	if len(verbs) == 0 {
		return nil
	}
	px, py, pw, ph := a.gitPanelRect()
	row := idx - a.gitPanel.diffScroll
	if row < 0 || row >= ph-1 {
		return nil // scrolled out of the body
	}
	listW := a.gitPanelListW(pw)
	textX := px + listW + 2
	paneW := pw - listW - 3
	// Chip strip: "[+] [↺]" — three cells each, one space between.
	strip := len(verbs)*4 - 1
	// The header text has to keep at least a readable stub beside the
	// chips; below that the pane is a scroll bar with ambitions.
	if paneW < strip+8 {
		return nil
	}
	x := textX + paneW - strip
	chips := make([]gitHunkChip, 0, len(verbs))
	for _, v := range verbs {
		chips = append(chips, gitHunkChip{
			rect: btnRect{x: x, y: py + 1 + row, w: 3},
			verb: v,
			span: sp,
		})
		x += 4
	}
	return chips
}

// gitPanelHunkSpans is the spans of the diff currently on screen.
// Recomputed per call rather than cached on the panel: the walk is a
// handful of hunks over a few hundred lines, and a cache would be one
// more thing to invalidate every time an async diff lands.
func (a *App) gitPanelHunkSpans() []gitHunkSpan {
	return gitPanelHunkSpans(a.gitPanel.diffLines, a.gitPanel.diffSide)
}

// gitPanelHunkClick runs the chip under (x, y), if any, and reports
// whether it claimed the press. Called from gitPanelClick before the
// generic diff-pane handling, so a chip click never doubles as the
// first half of a jump-to-line double-click.
func (a *App) gitPanelHunkClick(x, y int) bool {
	_, py, _, ph := a.gitPanelRect()
	row := y - py - 1
	if row < 0 || row >= ph-1 {
		return false
	}
	idx := a.gitPanel.diffScroll + row
	for _, chip := range a.gitPanelHunkChipsAt(idx) {
		if chip.rect.contains(x, y) {
			a.gitPanelRunHunkVerb(chip.verb, chip.span)
			return true
		}
	}
	return false
}

// -----------------------------------------------------------------------------
// Building the patch
// -----------------------------------------------------------------------------

// gitPanelHunkPatch assembles a one-hunk patch: the file header of the
// section the hunk belongs to, then the hunk itself. git apply needs
// the header (it names the file and, for a rename or a mode change,
// what else the patch does) and accepts exactly one hunk after it.
//
// The header is found by walking BACK from the "@@" line to the section
// boundary — a marker line or the file header — rather than by taking
// the top of the list, because a mixed diff carries two file headers
// and using the wrong one would hand git the wrong old-side content.
func gitPanelHunkPatch(lines []string, sp gitHunkSpan) string {
	if sp.header < 0 || sp.header >= len(lines) {
		return ""
	}
	start := 0
	for i := sp.header - 1; i >= 0; i-- {
		if isGitPanelMarker(lines[i]) {
			start = i + 1
			break
		}
		if strings.HasPrefix(lines[i], "diff --git ") {
			start = i
			break
		}
	}
	var b strings.Builder
	for i := start; i < len(lines) && !strings.HasPrefix(lines[i], "@@"); i++ {
		b.WriteString(lines[i])
		b.WriteByte('\n')
	}
	end := sp.end
	if end >= len(lines) {
		end = len(lines) - 1
	}
	for i := sp.header; i <= end; i++ {
		b.WriteString(lines[i])
		b.WriteByte('\n')
	}
	return b.String()
}

// gitHunkApplyArgs is the `git apply` flag set for one verb on one
// side. Split out as a pure function so the tests can pin the exact
// argv — the difference between "--cached -R" and "--index -R" is the
// difference between unstaging a change and destroying it.
func gitHunkApplyArgs(v gitHunkVerb, side diffSide) []string {
	switch v {
	case hunkStage:
		return []string{"--cached"}
	case hunkUnstage:
		return []string{"--cached", "-R"}
	}
	if side == sideStaged {
		// The hunk lives in the index and (because revert is only
		// offered on an unmixed file) identically in the work tree —
		// --index takes it out of both in one atomic check.
		return []string{"--index", "-R"}
	}
	return []string{"-R"}
}

// gitPanelRunHunkVerb is the single entry point every hunk surface
// funnels through — the chips, and the picker's rows. Revert confirms
// first; the other two move a change between two places git is still
// holding it and run straight away.
func (a *App) gitPanelRunHunkVerb(v gitHunkVerb, sp gitHunkSpan) {
	patch := gitPanelHunkPatch(a.gitPanel.diffLines, sp)
	if strings.TrimSpace(patch) == "" {
		return
	}
	name := gitPanelHunkName(a.gitPanel.diffLines, sp)
	what := gitHunkVerbName(v) + " hunk"
	if f, ok := a.gitPanelSelectedFile(); ok {
		what += " in " + f.Rel
	}
	args := gitHunkApplyArgs(v, sp.side)
	run := func(app *App) { app.runGitApplyPatch(what, patch, args...) }

	if v != hunkRevert {
		run(a)
		return
	}
	// Revert throws the work away — the one hunk verb with nothing
	// holding a copy. Both facts get their own line: WHICH hunk (the
	// header text is the only name a hunk has) and what is lost.
	target := "this change"
	if f, ok := a.gitPanelSelectedFile(); ok {
		target = f.Rel
	}
	a.openConfirmLines("Revert hunk", []string{
		"Undo " + name + " in " + target + "?",
		"The lines go back to their committed state.",
	}, run)
}

// gitPanelHunkName describes a hunk in prose for a confirm or a picker
// row. The "@@ -12,7 +12,9 @@ func foo()" header carries two useful
// facts — the line range in the file as it is now, and (often) the
// enclosing function git guessed. Both beat "hunk 2".
func gitPanelHunkName(lines []string, sp gitHunkSpan) string {
	if sp.header < 0 || sp.header >= len(lines) {
		return "this hunk"
	}
	hdr := lines[sp.header]
	_, _, newStart, newCount, ok := parseHunkHeader(hdr)
	if !ok {
		return "this hunk"
	}
	name := "lines " + itoa(newStart)
	if newCount > 1 {
		name += "–" + itoa(newStart+newCount-1)
	}
	if newCount == 0 {
		// A pure deletion has no lines left to point at; the boundary
		// is the only honest coordinate.
		name = "the deletion at line " + itoa(newStart)
	}
	// The context git appends after the closing "@@" is the enclosing
	// declaration — worth quoting when it's there.
	if i := strings.Index(hdr[2:], "@@"); i >= 0 {
		if ctx := strings.TrimSpace(hdr[2+i+2:]); ctx != "" {
			name += " (" + elide(ctx, 28) + ")"
		}
	}
	return name
}

// -----------------------------------------------------------------------------
// The keyboard route
// -----------------------------------------------------------------------------

// gitPanelHunkItems lists every hunk of the current diff with every
// verb it can take, as picker rows. This is the Tier-0 half of the
// feature: the chips are a mouse target, and macOS Terminal can swallow
// clicks — the same argument that gave the panel's Actions button a ≡
// twin. It is also what makes the verbs discoverable, since three-cell
// chips can only hint at what they do.
func (a *App) gitPanelHunkItems() []paletteItem {
	spans := a.gitPanelHunkSpans()
	var items []paletteItem
	for _, sp := range spans {
		sp := sp
		name := gitPanelHunkName(a.gitPanel.diffLines, sp)
		for _, v := range gitPanelHunkVerbs(sp, a.gitPanel.diffSide) {
			v := v
			items = append(items, paletteItem{
				label: gitHunkVerbName(v) + " " + name,
				run:   func(app *App) { app.gitPanelRunHunkVerb(v, sp) },
			})
		}
	}
	return items
}

// openGitPanelHunks opens the hunk picker for the file the diff pane is
// showing. Reachable from the Actions list and, while the survey has
// the keyboard, from a bare "h".
func (a *App) openGitPanelHunks() {
	items := a.gitPanelHunkItems()
	if len(items) == 0 {
		a.flash("No hunk actions here — " + a.gitPanelHunkWhyNot())
		return
	}
	title := "Hunk actions"
	if f, ok := a.gitPanelSelectedFile(); ok {
		title += " · " + f.Rel
	}
	a.openPicker(title, items)
}

// gitPanelHunkWhyNot explains an empty hunk list. An untracked file has
// no diff to cut up, and the `diff HEAD` fallback (an unborn branch, or
// a shape neither of git's two diffs claimed) has hunks that belong to
// no addressable side — saying which is the difference between "this
// tool is broken" and "of course, it's a new file".
func (a *App) gitPanelHunkWhyNot() string {
	switch {
	case len(a.gitPanel.files) == 0:
		return "nothing changed"
	case a.gitPanel.diffSide == sideNone && len(a.gitPanel.diffLines) > 0:
		return "this file is new to git; stage it whole"
	case len(a.gitPanel.diffLines) == 0:
		return "no diff loaded yet"
	}
	return "no hunks in this diff"
}
