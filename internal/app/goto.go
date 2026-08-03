// =============================================================================
// File: internal/app/goto.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// goto.go implements "go to line" — the verb that closes the loop
// between a compiler error read somewhere else and the line it names.
//
// Two decisions carry the feature:
//
//   - It accepts `line:col` and `line,col`, not just a bare number,
//     because the thing a user actually has in hand is a fragment of a
//     toolchain's output ("app.go:314:22"). Parsing the column costs a
//     few lines and saves the user editing what they just pasted; a
//     trailing colon or a `:0` column is tolerated for the same reason.
//   - An out-of-range line CLAMPS rather than refusing. The line number
//     usually comes from a build log that may be a few edits stale, and
//     landing on the last line of the file is a better answer to "line
//     900 of an 800-line file" than a modal that says no.
//
// The prompt is the shared promptModal (the house rule: no bespoke
// single-line input), and the jump reuses the same center-if-off-screen
// policy the find-all and project-search lists follow.

package app

import (
	"strconv"
	"strings"

	"github.com/rohanthewiz/ced/internal/editor"
)

// menuGoToLine is the ≡ / Esc-j entry point.
func (a *App) menuGoToLine() {
	a.closeMenu()
	a.openGoToLine()
}

// openGoToLine asks for a destination. The hint names the file's actual
// line range, which is the one piece of context that makes the question
// answerable without scrolling first.
func (a *App) openGoToLine() {
	tab := a.activeTabPtr()
	if tab == nil || tab.Buffer == nil || tab.IsImage() {
		return
	}
	hint := "1–" + itoa(tab.Buffer.LineCount()) + " · line or line:column"
	a.openPrompt("Go to line", hint, "", func(app *App, v string) {
		app.goToLineSpec(v)
	})
}

// goToLineSpec parses a destination and jumps there, flashing when the
// text isn't a line reference at all. A flash rather than silence: the
// user typed something and pressed Enter, so "nothing happened" would
// leave them wondering whether the jump or the editor failed.
func (a *App) goToLineSpec(spec string) {
	line, col, ok := parseLineSpec(spec)
	if !ok {
		a.flash("Not a line number: " + spec)
		return
	}
	a.goToLine(line, col)
}

// parseLineSpec decodes a 1-based `line`, `line:col` or `line,col`
// reference into 0-based buffer coordinates. Anything that isn't a
// number in the leading position fails; a missing, empty, malformed or
// zero column is simply column 0, because the line is the part the user
// is actually asking for and half a valid reference should still work.
func parseLineSpec(spec string) (line, col int, ok bool) {
	s := strings.TrimSpace(spec)
	// Tolerate a leading "file.go:" the user pasted along with the
	// numbers by keeping only the tail after the LAST non-numeric run —
	// cheap enough to be worth it, since that's the shape every compiler
	// prints. Splitting on both separators first keeps that simple.
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ':' || r == ',' })
	nums := make([]int, 0, 2)
	for _, f := range fields {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			// A non-numeric field resets what we've collected: in
			// "app.go:314:22" the path is first, and keeping it would
			// make "314" look like the column.
			nums = nums[:0]
			continue
		}
		nums = append(nums, n)
		if len(nums) == 2 {
			break
		}
	}
	if len(nums) == 0 || nums[0] < 1 {
		return 0, 0, false
	}
	line = nums[0] - 1
	if len(nums) > 1 && nums[1] > 1 {
		col = nums[1] - 1
	}
	return line, col, true
}

// goToLine moves the cursor to a 0-based line/column in the active tab,
// clamped to the buffer, and centers it when it wasn't already on
// screen — the same policy the find-all list applies to a previewed hit,
// and for the same reason (a minimal scroll parks the line on the last
// row, which answers "where is it?" but not "what is it doing?").
func (a *App) goToLine(line, col int) {
	tab := a.activeTabPtr()
	if tab == nil || tab.Buffer == nil || tab.IsImage() {
		return
	}
	if lc := tab.Buffer.LineCount(); line >= lc {
		line = lc - 1
	}
	if line < 0 {
		line = 0
	}
	// Clamp handles the column against the destination line's length —
	// MoveCursorTo would otherwise leave the caret past its end.
	pos := tab.Buffer.Clamp(editor.Position{Line: line, Col: col})
	tab.MoveCursorTo(pos, false)
	if _, _, ew, eh := a.editorRect(); !tab.CursorLineVisible(eh) {
		tab.CenterOnCursor(ew, eh)
	}
}

// hasGoToLine reports whether there's a text buffer to jump around in.
func (a *App) hasGoToLine() bool {
	t := a.activeTabPtr()
	return t != nil && t.Buffer != nil && !t.IsImage()
}
