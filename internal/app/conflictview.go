// =============================================================================
// File: internal/app/conflictview.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// conflictview.go is merge-conflict resolution INSIDE THE EDITOR: the
// part of the conflict UI that lives where the conflict is. A file git
// reports as unmerged gets three things on top of its text:
//
//	  12 │ func parse(s string) error {
//	  13 │ <<<<<<< HEAD  Accept current · Accept incoming · Accept both   ← lens
//	  14 │     if s == "" { return nil }          ░ current wash (green)
//	  15 │ =======                                ▒ separator (neutral)
//	  16 │     if len(s) == 0 { return errEmpty } ▓ incoming wash (mauve)
//	  17 │ >>>>>>> 2a0370b (Fix parser crash)
//
//   - WASHES (editor.LineWashSource): whole rows tinted by side, so the two
//     halves of a conflict read as two regions at a glance — the question a
//     raw marker file makes you answer by counting `=` signs.
//   - LENSES (editor.LensSource): clickable verbs on each `<<<<<<<` line.
//     One click settles the block as one undo step (editor/conflict.go).
//   - VERB TWINS: every lens button has a keyboard door — the editor's
//     right-click rows, the ≡ Git "Resolve conflict at caret…" picker, and
//     next/previous conflict rows that walk across files. A lens is a
//     mouse affordance, and macOS Terminal + tmux eat clicks.
//
// Decisions worth knowing:
//
//   - THE VIEW IS GATED ON GIT, not on the text. Washes and lenses appear
//     only on a file the last status snapshot lists as UNMERGED. Marker
//     lines are legitimate content in plenty of files (ced's own conflict
//     tests are full of them), and tinting a fixture as if it were a live
//     conflict would offer to "resolve" a test. "Resolved means the index
//     says so" (gitconflict.go) has a twin here: "conflicted means the
//     index says so".
//   - RESOLVING NEVER STAGES. Choosing a side edits the BUFFER; marking the
//     file resolved is a separate, explicit git verb (the Conflicts panel's
//     row button, or the ≡ picker). Auto-staging the moment the last block
//     goes would stage an unsaved buffer's on-disk twin — the classic way
//     to commit a half-resolved file — and would make Undo unable to bring
//     the conflict back the way the user expects.
//   - THE LENS ID IS TAGGED. Lens IDs are opaque to the editor and shared
//     by every LensSource; the tag bit says "this one is a conflict verb",
//     and the block index inside it is re-checked against the block's line
//     at click time, so a click on a frame that has since been edited can
//     never resolve the wrong block.

package app

import (
	"path/filepath"
	"sort"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/theme"
)

// conflictLensTag marks a lens ID as one of this file's verbs. Layout:
//
//	bit 20     tag
//	bits 4–19  block index (65536 blocks — no real file comes close)
//	bits 0–3   editor.ConflictChoice, or conflictCompareLens (0xf) for
//	           the "Compare" button, which settles nothing
const conflictLensTag = 1 << 20

// conflictLensID packs a block index and a choice into a tagged lens ID.
func conflictLensID(block int, c editor.ConflictChoice) int {
	return conflictLensTag | block<<4 | int(c)
}

// conflictLensDecode unpacks a lens ID, reporting false for an ID some
// other LensSource minted.
func conflictLensDecode(id int) (block int, c editor.ConflictChoice, ok bool) {
	if id&conflictLensTag == 0 {
		return 0, 0, false
	}
	id &^= conflictLensTag
	return id >> 4, editor.ConflictChoice(id & 0xf), true
}

// conflictViewOn reports whether tab t is a live conflict worth drawing:
// git lists it as unmerged AND its buffer still holds a block. Both
// halves matter — the first keeps fixtures quiet, the second makes the
// tints vanish the moment the last block is settled, before the next
// status tick has had a chance to notice.
func (a *App) conflictViewOn(t *editor.Tab) bool {
	if t == nil || t.Path == "" || t.IsImage() || !a.gitConflicted[t.Path] {
		return false
	}
	return len(t.Conflicts()) > 0
}

// -----------------------------------------------------------------------------
// The decoration source
// -----------------------------------------------------------------------------

// conflictSource paints a conflicted file: marker rows emphasised (a
// span), every row of every block washed by side (LineWashSource), and
// the verb lens on each opener (LensSource). Registered in wireTab with
// the other app-owned sources; it costs one map lookup per frame on any
// tab git does not list as unmerged.
type conflictSource struct {
	app *App
}

// visibleBlocks returns the blocks overlapping [first, last], or nil when
// the view is off for this tab.
func (s conflictSource) visibleBlocks(t *editor.Tab, first, last int) []editor.ConflictBlock {
	if !s.app.conflictViewOn(t) {
		return nil
	}
	var out []editor.ConflictBlock
	for _, b := range t.Conflicts() {
		if b.End < first {
			continue
		}
		if b.Start > last {
			break
		}
		out = append(out, b)
	}
	return out
}

// Decorations emboldens the marker rows and recolours them to the body
// text. Syntax highlighting reads `<<<<<<<` as a run of operators and
// paints it like code; the markers are structure, and on a washed row the
// plain text colour is the one that keeps its contrast in every theme.
func (s conflictSource) Decorations(t *editor.Tab, th theme.Theme, first, last int) ([]editor.Span, []editor.GutterMark) {
	var spans []editor.Span
	delta := editor.StyleDelta{FG: th.Text, SetFG: true, Bold: true}
	for _, b := range s.visibleBlocks(t, first, last) {
		markers := []int{b.Start, b.Mid, b.End}
		if b.HasBase() {
			markers = append(markers, b.Base)
		}
		for _, line := range markers {
			n := len([]rune(t.Buffer.Lines[line]))
			if n == 0 {
				continue
			}
			spans = append(spans, editor.Span{
				Start: editor.Position{Line: line},
				End:   editor.Position{Line: line, Col: n},
				Delta: delta,
			})
		}
	}
	return spans, nil
}

// LineWashes tints every row of every visible block by region. The sides
// take the theme's two conflict colours; their marker rows the same hue a
// step toward the text (so the opener and closer read as the edges of
// their side); the separator and a diff3 base section a neutral grey,
// because the ancestor belongs to neither side.
func (s conflictSource) LineWashes(t *editor.Tab, th theme.Theme, first, last int) map[int]tcell.Color {
	blocks := s.visibleBlocks(t, first, last)
	if len(blocks) == 0 {
		return nil
	}
	out := make(map[int]tcell.Color)
	for _, b := range blocks {
		for line := max(b.Start, first); line <= b.End && line <= last; line++ {
			c := conflictRegionColor(th, b.RegionOf(line))
			// The wash replaces the caret line's highlight (decoration.go),
			// so the caret line gets its own step toward the text — the
			// same 6% the line highlight is — or the caret's row would be
			// the one row in the file you could not find.
			if line == t.Cursor.Line {
				c = blendColor(c, th.Text, 0.06)
			}
			out[line] = c
		}
	}
	return out
}

// conflictRegionColor is the wash for one region of a block.
func conflictRegionColor(th theme.Theme, r editor.ConflictRegion) tcell.Color {
	switch r {
	case editor.RegionStartMarker:
		return blendColor(th.ConflictCurrent, th.Text, 0.14)
	case editor.RegionCurrent:
		return th.ConflictCurrent
	case editor.RegionIncoming:
		return th.ConflictIncoming
	case editor.RegionEndMarker:
		return blendColor(th.ConflictIncoming, th.Text, 0.14)
	case editor.RegionBase:
		return blendColor(th.BG, th.Text, 0.07)
	default: // the separator and the base marker
		// 0.16 is theme.conflictSepWash: the incoming hue is chosen to
		// stand clear of exactly this grey (theme/palette.go).
		return blendColor(th.BG, th.Text, 0.16)
	}
}

// Lenses puts the verbs on each visible block's opener, most-wanted
// first so a narrow pane sheds the rarer ones (editor/lens.go). Accept
// base appears only on a diff3 block — on a two-way block it would be a
// button that refuses. "Compare" goes last: it is a look, not a
// decision, so it is the first button a narrow pane gives up — its
// right-click and ≡ twins stay (conflictcompare.go).
func (s conflictSource) Lenses(t *editor.Tab, th theme.Theme, first, last int) map[int][]editor.Lens {
	blocks := s.visibleBlocks(t, first, last)
	if len(blocks) == 0 {
		return nil
	}
	all := t.Conflicts()
	out := make(map[int][]editor.Lens, len(blocks))
	for _, b := range blocks {
		if b.Start < first {
			continue // the opener is scrolled off; its row is not painted
		}
		idx := conflictIndexOf(all, b.Start)
		set := []editor.Lens{
			{Label: editor.ChooseCurrent.Label(), Short: "Current", FG: th.GitAdded,
				ID: conflictLensID(idx, editor.ChooseCurrent)},
			{Label: editor.ChooseIncoming.Label(), Short: "Incoming", FG: th.AccentSoft,
				ID: conflictLensID(idx, editor.ChooseIncoming)},
			{Label: editor.ChooseBoth.Label(), Short: "Both", FG: th.Accent,
				ID: conflictLensID(idx, editor.ChooseBoth)},
		}
		if b.HasBase() {
			set = append(set, editor.Lens{Label: editor.ChooseBase.Label(), Short: "Base",
				FG: th.Text, ID: conflictLensID(idx, editor.ChooseBase)})
		}
		set = append(set, editor.Lens{Label: "Compare sides", Short: "Compare",
			FG: th.Text, ID: conflictLensID(idx, conflictCompareLens)})
		out[b.Start] = set
	}
	return out
}

// conflictIndexOf finds the block whose opener is on `line`.
func conflictIndexOf(blocks []editor.ConflictBlock, line int) int {
	for i, b := range blocks {
		if b.Start == line {
			return i
		}
	}
	return -1
}

// blendColor mixes a toward b by t (0 = a, 1 = b) in RGB. A colour with
// no RGB form (a palette index, the terminal default) comes back as a
// unchanged — a theme built from such colours just loses the gradation,
// never the tint itself.
func blendColor(a, b tcell.Color, t float64) tcell.Color {
	ar, ag, ab := a.RGB()
	br, bg, bb := b.RGB()
	if ar < 0 || br < 0 {
		return a
	}
	mix := func(x, y int32) int32 { return x + int32(float64(y-x)*t+0.5) }
	return tcell.NewRGBColor(mix(ar, br), mix(ag, bg), mix(ab, bb))
}

// -----------------------------------------------------------------------------
// The click
// -----------------------------------------------------------------------------

// conflictLensPress handles a left press on a conflict lens button,
// reporting whether it consumed the event. It reads the geometry the last
// frame STAMPED (Tab.LensAt) — the draw and the click share one source.
//
// The block is re-identified by its opener's line before anything is
// written: the index came from a painted frame, and an edit since then
// (another block resolved, a line typed above) can have renumbered the
// blocks. A mismatch is swallowed rather than guessed at — the next frame
// repaints the buttons where they now belong.
func (a *App) conflictLensPress(x, y int) bool {
	t := a.activeTabPtr()
	if t == nil || t.IsImage() || t.IsMarkdownView() {
		return false
	}
	ex, ey, _, _ := a.editorRect()
	hit, ok := t.LensAt(x-ex, y-ey)
	if !ok {
		return false
	}
	idx, choice, ok := conflictLensDecode(hit.Lens.ID)
	if !ok {
		return false
	}
	blocks := t.Conflicts()
	if idx < 0 || idx >= len(blocks) || blocks[idx].Start != hit.Line {
		return true
	}
	if choice == conflictCompareLens {
		a.compareConflictBlock(t, idx)
		return true
	}
	a.resolveConflictBlock(t, idx, choice)
	return true
}

// -----------------------------------------------------------------------------
// The verbs
// -----------------------------------------------------------------------------

// resolveConflictBlock settles one block and reports the outcome. The
// single write path behind the lens, the context menu and the picker.
func (a *App) resolveConflictBlock(t *editor.Tab, idx int, choice editor.ConflictChoice) {
	if !t.ResolveConflict(idx, choice) {
		a.flash("Can't " + lowerFirst(choice.Label()) + " here")
		return
	}
	a.afterConflictResolve(t, choice.Label())
}

// resolveAllConflictsIn settles every block in t with one choice, as one
// undo step, and reports how many it rewrote.
func (a *App) resolveAllConflictsIn(t *editor.Tab, choice editor.ConflictChoice) int {
	n := t.ResolveAllConflicts(choice)
	if n == 0 {
		a.flash("No conflicts here can " + lowerFirst(choice.Label()))
		return 0
	}
	a.afterConflictResolve(t, choice.Label()+" ×"+itoa(n))
	return n
}

// afterConflictResolve is the shared tail: say what happened and what is
// left, and keep the Conflicts panel's counts in step with the buffer.
//
// The flash answers the two questions a resolver asks after every click
// — "did it take?" and "how much is left?" — and when nothing is left it
// names the NEXT step, because "the markers are gone" and "git knows the
// file is resolved" are different states and the second needs a verb.
func (a *App) afterConflictResolve(t *editor.Tab, what string) {
	name := filepath.Base(t.Path)
	if left := len(t.Conflicts()); left > 0 {
		a.flash(what + " · " + plural(left, "conflict", "conflicts") + " left in " + name)
	} else {
		// Names the next step in few words — the status bar's right half
		// leaves a flash about seventy cells on a typical window.
		a.flash(name + " has no conflicts left — Mark resolved next")
	}
	a.refreshConflictPanelCounts()
}

// conflictAtCaret returns the active tab and the index of the block the
// caret is in, when the conflict view is live there.
func (a *App) conflictAtCaret() (*editor.Tab, int, bool) {
	t := a.activeTabPtr()
	if !a.conflictViewOn(t) {
		return nil, -1, false
	}
	idx, ok := t.ConflictAt(t.Cursor.Line)
	return t, idx, ok
}

// hasConflictAtCaret gates the "Resolve conflict at caret…" row.
func (a *App) hasConflictAtCaret() bool {
	_, _, ok := a.conflictAtCaret()
	return ok
}

// hasConflictsToWalk gates next / previous conflict: something to walk
// to, in this file or another unmerged one.
func (a *App) hasConflictsToWalk() bool {
	return a.conflictViewOn(a.activeTabPtr()) || len(a.gitConflicted) > 0
}

// conflictChoiceItems builds the per-block rows every door shares: the
// lens's verbs plus the two rarer choices the lens has no room for.
func conflictChoiceItems(b editor.ConflictBlock) []editor.ConflictChoice {
	out := []editor.ConflictChoice{editor.ChooseCurrent, editor.ChooseIncoming,
		editor.ChooseBoth, editor.ChooseBothIncomingFirst}
	if b.HasBase() {
		out = append(out, editor.ChooseBase)
	}
	return append(out, editor.ChooseNeither)
}

// menuResolveConflictAtCaret is the ≡ Git row (and so the palette's): the
// keyboard door to the lens. One picker carries both scopes — this block,
// and every block in the file — because "take theirs here" and "take
// theirs everywhere" are the same decision asked at two sizes.
func (a *App) menuResolveConflictAtCaret() {
	a.closeMenu()
	t, idx, ok := a.conflictAtCaret()
	if !ok {
		a.flash("The caret is not inside a conflict — Next conflict finds one")
		return
	}
	b := t.Conflicts()[idx]
	var items []paletteItem
	for _, c := range conflictChoiceItems(b) {
		c := c
		items = append(items, paletteItem{label: c.Label(), run: func(app *App) {
			app.resolveConflictBlock(t, idx, c)
		}})
	}
	if n := len(t.Conflicts()); n > 1 {
		for _, c := range []editor.ConflictChoice{editor.ChooseCurrent, editor.ChooseIncoming} {
			c := c
			items = append(items, paletteItem{
				label: "All " + itoa(n) + " conflicts in this file: " + lowerFirst(c.Label()),
				run:   func(app *App) { app.resolveAllConflictsIn(t, c) },
			})
		}
	}
	a.openPicker("Conflict "+itoa(idx+1)+" of "+itoa(len(t.Conflicts()))+
		" · "+conflictSidesNote(b), items)
}

// conflictSidesNote names the two sides of a block by their marker
// labels — "HEAD ↔ 2a0370b (Fix parser crash)" — so a picker opened from
// the keyboard says which side is which without the lens's colours.
func conflictSidesNote(b editor.ConflictBlock) string {
	cur, inc := b.CurrentLabel, b.IncomingLabel
	if cur == "" {
		cur = "current"
	}
	if inc == "" {
		inc = "incoming"
	}
	return elide(cur, 20) + " ↔ " + elide(inc, 32)
}

// conflictContextItems are the editor right-click rows for a click
// inside a conflict (contextmenu.go prepends them). The click already
// placed the caret, so they aim at the block under the pointer.
func (a *App) conflictContextItems() []editorContextItem {
	t, idx, ok := a.conflictAtCaret()
	if !ok {
		return nil
	}
	b := t.Conflicts()[idx]
	var out []editorContextItem
	for _, c := range conflictChoiceItems(b) {
		c := c
		out = append(out, editorContextItem{
			label:   c.Label(),
			action:  func(app *App) { app.resolveConflictBlock(t, idx, c) },
			enabled: alwaysTrue,
		})
	}
	// The look before the choice, after the choices: the rows above are
	// what the menu was opened for. The ellipsis only where a picker
	// follows — a diff3 block asks which pair.
	label := "Compare sides"
	if b.HasBase() {
		label += "…"
	}
	out = append(out, editorContextItem{
		label:   label,
		action:  func(app *App) { app.compareConflictBlock(t, idx) },
		enabled: alwaysTrue,
	})
	return out
}

// -----------------------------------------------------------------------------
// Next / previous conflict, across files
// -----------------------------------------------------------------------------

// menuNextConflict / menuPrevConflict are the ≡ Git rows. They walk the
// whole repository's conflicts as one document: past the last block in
// this file they continue into the next unmerged file, in path order —
// the Problems panel's "the project is one list" rule, because the job
// is "resolve everything", not "resolve this file".
func (a *App) menuNextConflict() { a.closeMenu(); a.stepConflict(1) }

// menuPrevConflict walks backwards; see menuNextConflict.
func (a *App) menuPrevConflict() { a.closeMenu(); a.stepConflict(-1) }

// stepConflict moves to the neighbouring conflict block in direction dir.
func (a *App) stepConflict(dir int) {
	t := a.activeTabPtr()
	if a.conflictViewOn(t) {
		if idx, ok := t.NextConflict(t.Cursor.Line, dir); ok {
			a.jumpToConflict(t, idx)
			return
		}
	}
	// Nothing further in this file: the next unmerged file that still
	// holds a block, in the walk's direction.
	from := ""
	if t != nil {
		from = t.Path
	}
	path, ok := a.neighbourConflictFile(from, dir)
	if !ok {
		if dir > 0 {
			a.flash("No further conflicts")
		} else {
			a.flash("No earlier conflicts")
		}
		return
	}
	a.openFile(path)
	nt := a.activeTabPtr()
	if nt == nil || nt.Path != path {
		return // openFile failed and flashed its own reason
	}
	blocks := nt.Conflicts()
	if len(blocks) == 0 {
		return
	}
	idx := 0
	if dir < 0 {
		idx = len(blocks) - 1
	}
	a.jumpToConflict(nt, idx)
}

// neighbourConflictFile returns the next (dir > 0) or previous unmerged
// file after `from` in path order that still holds a conflict block.
// Files are asked about through their open BUFFER when they have one
// (the house rule: buffer before disk) and by a disk scan otherwise, so
// the walk never opens a tab only to find it already resolved.
func (a *App) neighbourConflictFile(from string, dir int) (string, bool) {
	paths := make([]string, 0, len(a.gitConflicted))
	for p := range a.gitConflicted {
		if pathInside(p, a.rootDir) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	if dir < 0 {
		for i, j := 0, len(paths)-1; i < j; i, j = i+1, j-1 {
			paths[i], paths[j] = paths[j], paths[i]
		}
	}
	for _, p := range paths {
		if from != "" && ((dir > 0 && p <= from) || (dir < 0 && p >= from)) {
			continue
		}
		if a.conflictCountFor(p) > 0 {
			return p, true
		}
	}
	return "", false
}

// conflictCountFor reports how many conflict blocks path holds right now:
// the open tab's buffer when there is one, else the file on disk. A file
// too large or unreadable to scan answers -1, "unknown" — which every
// caller treats as "not known to be clean".
func (a *App) conflictCountFor(path string) int {
	if t := a.tabForPath(path); t != nil {
		return len(t.Conflicts())
	}
	lines, ok := readConflictScanLines(path)
	if !ok {
		return -1
	}
	return len(editor.ParseConflicts(lines))
}

// jumpToConflict parks the caret on block idx's opener with the jump
// margin (so the lens and the first lines of both sides are on screen),
// and says where in the file's set the user now is.
func (a *App) jumpToConflict(t *editor.Tab, idx int) {
	blocks := t.Conflicts()
	if idx < 0 || idx >= len(blocks) {
		return
	}
	t.MoveCursorTo(editor.Position{Line: blocks[idx].Start}, false)
	t.MarkJump()
	if _, _, ew, eh := a.editorRect(); !t.CursorLineVisible(eh) {
		t.CenterOnCursor(ew, eh)
	}
	a.flash("Conflict " + itoa(idx+1) + " of " + itoa(len(blocks)) + " in " + filepath.Base(t.Path))
}

// lowerFirst lower-cases a label's first letter so a verb reads inside a
// sentence ("Can't accept base here").
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'A' && r[0] <= 'Z' {
		r[0] += 'a' - 'A'
	}
	return string(r)
}
