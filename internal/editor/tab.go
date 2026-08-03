// =============================================================================
// File: internal/editor/tab.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// gutterWidth is the cell width reserved for the line-number column inside
// the editor area. Six gives us up to five-digit line numbers plus a one-cell
// pad on the right — comfortable for files of any realistic length.
const gutterWidth = 6

// Tab is a single open file. It owns the on-disk path, the in-memory buffer,
// the per-tab view state (scroll position, cursor, selection anchor), the
// cached syntax-highlight styles, and a dirty flag.
type Tab struct {
	Path       string // Empty for an unsaved/scratch tab.
	Buffer     *Buffer
	Cursor     Position // Where new typed text appears.
	Anchor     Position // Selection anchor; equals Cursor when nothing is selected.
	ScrollY    int      // Index of the first visible line.
	ScrollX    int      // Index of the first visible column (rune-indexed).
	Dirty      bool
	Styles     [][]tcell.Style
	StyleStale bool

	// SyntaxOff kills highlighting for this tab outright. Set at open (and
	// re-evaluated on reload) for files over MaxHighlightBytes, where even
	// one re-lex per typing pause is a visible freeze. See syntax.go.
	SyntaxOff bool

	// styleDefer marks the staleness above as "the grid is still aligned
	// with the buffer, keep painting it" — the intra-line edit case, which
	// waits out syntaxSettle instead of re-lexing per keystroke.
	// lastEditAt is when that window last restarted. Both are owned by
	// syntax.go; nothing else should write them.
	styleDefer bool
	lastEditAt time.Time

	// Mtime is the file's modification time as of the last successful
	// read or write. The app's periodic disk-reconcile loop compares it
	// against the live mtime to detect external edits.
	Mtime time.Time

	// LineEnding is the ending the file had when it was read ("\n" or
	// "\r\n"), and the one every save re-emits. The buffer itself always
	// holds bare LF, so nothing above this layer has to think about it.
	// BOM records whether the file began with a UTF-8 byte-order mark,
	// for the same round-trip reason. Both are set by readTextFile; see
	// fileio.go for why a CRLF file that loses its endings on save is
	// worse than it sounds.
	LineEnding string
	BOM        bool

	// DiskGone is set when the most recent disk check found the file
	// missing. It exists so we only flash the "deleted on disk" warning
	// once, instead of re-flashing every reconcile tick.
	DiskGone bool

	// cursorMoved is set by every method that changes Cursor; Render
	// consumes it to decide whether to scroll the viewport so the cursor
	// is visible. Without this flag, mouse-wheel scrolling is fought by
	// every redraw — EnsureVisible would snap us back to the cursor.
	cursorMoved bool

	// Undo / redo stacks plus the original on-open snapshot used by
	// RevertFile. See undo.go for the push / coalescing rules and the
	// public Undo / Redo / RevertFile entry points.
	undoStack     []snapshot
	redoStack     []snapshot
	undoOriginal  snapshot
	lastUndoGroup undoGroup
	lastUndoAt    time.Time

	// Mode is "" for a normal text tab and imageMode (= "image") for a
	// read-only image preview. Image tabs reuse the Tab type so the
	// app's tab list, switcher, and modal-routing all just work — the
	// content-mutating methods short-circuit on imageMode and Render
	// delegates to renderImage. See image.go for the render path.
	Mode     string
	Image    image.Image // populated when Mode == imageMode
	ImageFmt string      // "png" / "jpeg" / "gif" — for the status bar

	// Find state — populated when the user opens the find bar and
	// types a query. The UI layer (App) owns the bar geometry and
	// keystroke routing; the tab owns the query, the resolved match
	// list, and the index of the "current" match so the query
	// survives switching tabs and re-opening the bar.
	FindQuery   string
	FindMatches []Match
	FindIndex   int // -1 = no current match; otherwise an index into FindMatches.

	// IndentUnit is the string the editor inserts when the user presses
	// Tab. Detected on file open (DetectIndent) so the editor matches
	// whatever the file already does — a tab-indented Go file gets a
	// real tab; a 2-space-indented file gets two spaces. Mixed-style
	// files take the dominant signal.
	IndentUnit string

	// DecoSources are external decoration producers (git diff marks,
	// LSP diagnostics, …) consulted on every render, in order, before
	// the built-in selection/find sources. See decoration.go for the
	// span model and merge precedence.
	DecoSources []DecorationSource

	// EditRev counts content mutations — every path that changes what
	// the buffer says (typing, deletes, undo, comment toggle, reload)
	// bumps it. Consumers that need "did the text change since I last
	// looked?" (the LSP didChange debounce) compare against a remembered
	// value instead of diffing the buffer. Deliberately NOT bumped by
	// cursor/selection movement or scrolling — those don't change the
	// document a language server sees.
	EditRev int

	// Ghost is the inline-suggestion overlay (Copilot ghost text), or
	// nil when none is showing. Owned by the app layer — it sets the
	// display form here and clears it on any invalidating event; the
	// Tab only paints it (see ghost.go for the splice mechanism).
	Ghost *GhostText

	// Carets holds the SECONDARY editing points for multi-line editing;
	// the primary one is Cursor/Anchor above. Empty (the common case)
	// means every edit primitive runs its plain single-caret path. See
	// multicaret.go for the fan-out and for why the primary isn't just
	// another entry in this slice.
	Carets []Caret

	// CaretsHidden is the off-phase of the secondary carets' blink: when
	// set, paintCarets draws nothing and the cells show plain text. The
	// app owns the phase (it's the one with an event loop to drive a
	// timer from) — see app/multicaret.go.
	CaretsHidden bool

	// WordHighlight gates the "tint every other instance of the word
	// under the cursor" decoration (wordhl.go). Set from the user
	// config when the tab is opened, and by the ≡ toggle across every
	// open tab — it lives per-tab because DecorationSources are asked
	// per-tab and have no route back to the App.
	WordHighlight bool

	// undoSuppress is set while a multi-caret fan-out is in flight so
	// the per-caret primitives don't each file their own undo entry —
	// applyAtCarets pushes one snapshot for the whole burst. Nothing
	// else may set it: an unbalanced true silently disables undo.
	undoSuppress bool
}

// NewTab opens path and returns a Tab. If the file does not exist, the tab
// is created with an empty buffer that will be written on first save —
// matching what most editors do when you "open" a brand-new file path.
// When path looks like an image we recognise (PNG / JPEG / GIF), the tab
// is opened in read-only image-preview mode instead of as text.
func NewTab(path string) (*Tab, error) {
	if path != "" && isImageExt(path) {
		return newImageTab(path)
	}
	var text string
	var mtime time.Time
	bom, ending := false, lineEndingLF
	if path != "" {
		var err error
		// Guarded, decoded, and normalised in one place — see fileio.go
		// for what "guarded" refuses and why the endings come back out.
		text, bom, ending, err = readTextFile(path)
		if err != nil {
			return nil, err
		}
		// Record the on-disk mtime so the app can detect external edits
		// later. A missing file leaves mtime as the zero value, which is
		// fine — the reconcile loop handles that case explicitly.
		if info, statErr := os.Stat(path); statErr == nil {
			mtime = info.ModTime()
		}
	}
	t := &Tab{
		Path:       path,
		Buffer:     NewBuffer(text),
		StyleStale: true,
		Mtime:      mtime,
		BOM:        bom,
		LineEnding: ending,
		// Highlighting cost scales with file size, so past a point it is
		// cheaper — and much better to use — with no colors at all.
		SyntaxOff: len(text) > MaxHighlightBytes,
	}
	t.IndentUnit = DetectIndent(t.Buffer.Lines, path)
	// Record the on-open buffer state so RevertFile has somewhere to
	// rewind to even after the user has typed away.
	t.initUndo()
	return t, nil
}

// newImageTab decodes path as an image and returns a Tab in image
// preview mode. The buffer is left empty (image tabs ignore it) but
// allocated so any code that pokes at t.Buffer doesn't have to nil-check.
func newImageTab(path string) (*Tab, error) {
	img, format, err := decodeImageFile(path)
	if err != nil {
		return nil, err
	}
	var mtime time.Time
	if info, statErr := os.Stat(path); statErr == nil {
		mtime = info.ModTime()
	}
	t := &Tab{
		Path:     path,
		Buffer:   NewBuffer(""),
		Mtime:    mtime,
		Mode:     imageMode,
		Image:    img,
		ImageFmt: format,
	}
	// Capture the empty original snapshot so CanRevert / RevertFile
	// behave sensibly even though image tabs are read-only — they'll
	// just always report "nothing to revert".
	t.initUndo()
	return t, nil
}

// IsImage reports whether the tab is an image-preview, not a text editor.
// Callers use this to skip text-only behaviour (cursor placement, key
// dispatch, save, etc.) without having to know about Mode strings.
func (t *Tab) IsImage() bool {
	return t.Mode == imageMode
}

// DisplayName returns the basename of Path, or "untitled" for unsaved tabs.
func (t *Tab) DisplayName() string {
	if t.Path == "" {
		return "untitled"
	}
	return filepath.Base(t.Path)
}

// Save writes the buffer to disk and clears Dirty. It is an error to call
// Save on an untitled tab — callers should prompt for a path first. Mtime
// is refreshed so the disk-reconcile loop doesn't immediately think the
// file we just wrote was changed by someone else. Image tabs return an
// error since the editor only knows how to read those, not re-encode them.
func (t *Tab) Save() error {
	if t.IsImage() {
		return fmt.Errorf("image tabs are read-only")
	}
	if t.Path == "" {
		return fmt.Errorf("no path set for tab")
	}
	// Atomic (temp + rename) rather than a truncating in-place write, and
	// re-encoded into the byte form the file arrived in. See fileio.go.
	if err := writeFileAtomic(t.Path, t.encode()); err != nil {
		return err
	}
	t.Dirty = false
	t.DiskGone = false
	if info, err := os.Stat(t.Path); err == nil {
		t.Mtime = info.ModTime()
	}
	// Save is a natural logical-step boundary: the next typing burst is
	// clearly a separate intent, so don't let it merge into whatever was
	// in flight before the save.
	t.breakUndoGroup()
	return nil
}

// Reload re-reads the file from disk into the buffer. Cursor and anchor
// are clamped to the new content (so the user keeps roughly their place
// instead of getting snapped to line 0); ScrollY is left alone and gets
// clamped on the next render. Dirty is cleared and the syntax cache is
// invalidated. Image tabs decode the file again instead of replacing
// the text buffer.
func (t *Tab) Reload() error {
	if t.Path == "" {
		return fmt.Errorf("no path set for tab")
	}
	if t.IsImage() {
		img, format, err := decodeImageFile(t.Path)
		if err != nil {
			return err
		}
		info, err := os.Stat(t.Path)
		if err != nil {
			return err
		}
		t.Image = img
		t.ImageFmt = format
		t.Mtime = info.ModTime()
		t.DiskGone = false
		return nil
	}
	// Same guards and decoding as the open path: a file can grow past the
	// limit, turn binary, or change its line endings while we hold it
	// open, and a reload is exactly when we'd find out.
	text, bom, ending, err := readTextFile(t.Path)
	if err != nil {
		return err
	}
	info, err := os.Stat(t.Path)
	if err != nil {
		return err
	}
	t.Buffer = NewBuffer(text)
	t.BOM = bom
	t.LineEnding = ending
	t.Cursor = t.Buffer.Clamp(t.Cursor)
	t.Anchor = t.Cursor // drop any selection — line indices may have shifted.
	t.Carets = nil      // …and with them every secondary caret, for the same reason.
	t.Dirty = false
	t.DiskGone = false
	t.Mtime = info.ModTime()
	// Re-evaluated, not sticky: a file that grew past the limit while we
	// held it open should stop highlighting, and one that shrank should
	// start again.
	t.SyntaxOff = len(text) > MaxHighlightBytes
	t.InvalidateStyles()
	t.cursorMoved = true
	t.EditRev++
	// Reload re-establishes "what's on disk" as the new baseline. Any
	// prior undo history is meaningless now (the line indices may have
	// shifted, and the user explicitly asked to take the disk version),
	// so reset both stacks and the revert anchor.
	t.initUndo()
	return nil
}

// HasSelection reports whether the tab currently has a non-empty selection.
func (t *Tab) HasSelection() bool {
	return t.Cursor != t.Anchor
}

// SelectionText returns the currently selected text, or "" if nothing is
// selected. The text is always returned in document order. With
// secondary carets active every caret's selection is included, joined by
// newlines — copying a column of matches should yield the column.
func (t *Tab) SelectionText() string {
	if t.HasCarets() {
		return t.CaretText()
	}
	if !t.HasSelection() {
		return ""
	}
	return t.Buffer.Substring(t.Anchor, t.Cursor)
}

// DeleteSelection removes the selected range at every caret and
// collapses each cursor to the start of its selection. A no-op where
// nothing is selected.
func (t *Tab) DeleteSelection() {
	t.applyAtCarets(true, t.deleteSelectionAt)
}

// deleteSelectionAt is the single-caret core of DeleteSelection. The
// other primitives call THIS rather than the exported wrapper — they're
// already running inside a fan-out, and re-entering it would visit every
// caret once per caret.
func (t *Tab) deleteSelectionAt() {
	if t.IsImage() || !t.HasSelection() {
		return
	}
	// Selection deletes are always their own undo step — they can wipe
	// out a lot in one stroke, and merging them into adjacent typing
	// would make the next undo recover content the user thought was
	// just-deleted.
	t.pushUndo(undoGroupStructural)
	// Captured (and ordered) before the mutation: the style patch below
	// needs the range as it was, and DeleteRange returns only the merge
	// point. Clamped for the same reason DeleteRange clamps — an
	// out-of-range column would simply fail the patch and cost a re-lex.
	from, to := PosOrdered(t.Buffer.Clamp(t.Anchor), t.Buffer.Clamp(t.Cursor))
	pos := t.Buffer.DeleteRange(t.Anchor, t.Cursor)
	t.Cursor = pos
	t.Anchor = pos
	t.Dirty = true
	t.stylesAfterDelete(from, to)
	t.cursorMoved = true
	t.EditRev++
}

// InsertString inserts s at every caret (replacing any selection first)
// and advances each cursor past the inserted text. Always recorded as a
// structural undo step — pasted text or "\n" presses shouldn't merge
// with the surrounding typing burst. No-op on image tabs.
func (t *Tab) InsertString(s string) {
	t.applyAtCarets(true, func() { t.insertStringAt(s) })
}

// insertStringAt is the single-caret core of InsertString.
func (t *Tab) insertStringAt(s string) {
	if t.IsImage() {
		return
	}
	if t.HasSelection() {
		// deleteSelectionAt records its own structural step. Don't push a
		// second one here or the user would have to undo twice to get
		// back to the pre-paste-with-selection state.
		t.deleteSelectionAt()
	} else {
		t.pushUndo(undoGroupStructural)
	}
	at := t.Cursor
	t.Cursor = t.Buffer.InsertString(t.Cursor, s)
	t.Anchor = t.Cursor
	t.Dirty = true
	t.stylesAfterInsert(at, s)
	t.cursorMoved = true
	t.EditRev++
}

// InsertRune inserts a single typed character at every caret. Coalesces
// with adjacent runes inside the undo window so a typed word collapses
// into a single undo step rather than one entry per keystroke. No-op
// on image tabs.
func (t *Tab) InsertRune(r rune) {
	t.applyAtCarets(true, func() { t.insertRuneAt(r) })
}

// insertRuneAt is the single-caret core of InsertRune.
func (t *Tab) insertRuneAt(r rune) {
	if t.IsImage() {
		return
	}
	if t.HasSelection() {
		// First-rune-after-selection: let deleteSelectionAt capture the
		// pre-state, then run the insert without a second push.
		t.deleteSelectionAt()
	} else {
		t.pushUndo(undoGroupTyping)
	}
	at := t.Cursor
	t.Cursor = t.Buffer.InsertString(t.Cursor, string(r))
	t.Anchor = t.Cursor
	t.Dirty = true
	t.stylesAfterInsert(at, string(r))
	t.cursorMoved = true
	t.EditRev++
}

// Backspace deletes the character before every caret (or its selection
// if any). Coalesces with adjacent backspaces inside the undo window.
// No-op on image tabs.
func (t *Tab) Backspace() {
	t.applyAtCarets(true, t.backspaceAt)
}

// backspaceAt is the single-caret core of Backspace.
func (t *Tab) backspaceAt() {
	if t.IsImage() {
		return
	}
	if t.HasSelection() {
		t.deleteSelectionAt()
		return
	}
	if t.Cursor.Line == 0 && t.Cursor.Col == 0 {
		return
	}
	t.pushUndo(undoGroupBackspace)
	var prev Position
	if t.Cursor.Col == 0 {
		prev.Line = t.Cursor.Line - 1
		prev.Col = len([]rune(t.Buffer.Lines[prev.Line]))
	} else {
		prev = Position{Line: t.Cursor.Line, Col: t.Cursor.Col - 1}
	}
	end := t.Cursor
	t.Cursor = t.Buffer.DeleteRange(prev, t.Cursor)
	t.Anchor = t.Cursor
	t.Dirty = true
	t.stylesAfterDelete(prev, end)
	t.cursorMoved = true
	t.EditRev++
}

// Delete removes the character after every caret (or its selection if
// any). Coalesces with adjacent forward-deletes inside the undo window.
// No-op on image tabs.
func (t *Tab) Delete() {
	t.applyAtCarets(true, t.deleteAt)
}

// deleteAt is the single-caret core of Delete.
func (t *Tab) deleteAt() {
	if t.IsImage() {
		return
	}
	if t.HasSelection() {
		t.deleteSelectionAt()
		return
	}
	end := t.Buffer.EndPos()
	if t.Cursor == end {
		return
	}
	t.pushUndo(undoGroupDelete)
	var next Position
	line := []rune(t.Buffer.Lines[t.Cursor.Line])
	if t.Cursor.Col >= len(line) {
		next = Position{Line: t.Cursor.Line + 1, Col: 0}
	} else {
		next = Position{Line: t.Cursor.Line, Col: t.Cursor.Col + 1}
	}
	start := t.Cursor
	t.Cursor = t.Buffer.DeleteRange(t.Cursor, next)
	t.Anchor = t.Cursor
	t.Dirty = true
	t.stylesAfterDelete(start, next)
	t.cursorMoved = true
	t.EditRev++
}

// MoveCursor shifts every caret by (dLine, dCol). When extend is true
// the anchors are left in place so the user is extending a selection.
// Arrow keys move the whole caret set on purpose — placing carets and
// then pressing End is how a column of edits gets lined up.
func (t *Tab) MoveCursor(dLine, dCol int, extend bool) {
	t.applyAtCarets(false, func() { t.moveCursorAt(dLine, dCol, extend) })
}

// moveCursorAt is the single-caret core of MoveCursor.
func (t *Tab) moveCursorAt(dLine, dCol int, extend bool) {
	cur := t.Cursor
	if dLine != 0 {
		cur.Line += dLine
		if cur.Line < 0 {
			cur.Line = 0
		}
		if cur.Line >= t.Buffer.LineCount() {
			cur.Line = t.Buffer.LineCount() - 1
		}
		runes := []rune(t.Buffer.Lines[cur.Line])
		if cur.Col > len(runes) {
			cur.Col = len(runes)
		}
	}
	if dCol != 0 {
		cur.Col += dCol
		if cur.Col < 0 {
			// Wrap to the end of the previous line.
			if cur.Line > 0 {
				cur.Line--
				cur.Col = len([]rune(t.Buffer.Lines[cur.Line]))
			} else {
				cur.Col = 0
			}
		} else {
			runes := []rune(t.Buffer.Lines[cur.Line])
			if cur.Col > len(runes) {
				if cur.Line < t.Buffer.LineCount()-1 {
					cur.Line++
					cur.Col = 0
				} else {
					cur.Col = len(runes)
				}
			}
		}
	}
	t.Cursor = cur
	if !extend {
		t.Anchor = cur
	}
	t.cursorMoved = true
	// Cursor moved on the user's explicit command — close any open
	// coalescing window so the next typing burst is a fresh undo step.
	t.breakUndoGroup()
}

// MoveCursorTo sets the cursor to a specific buffer position. Position is
// clamped within the buffer; extend=true preserves the selection anchor.
//
// Any explicit jump — a click, a find hit, a definition jump — DROPS the
// secondary carets. The user pointed at one place; leaving carets behind
// somewhere off-screen would make the next keystroke edit text they
// aren't looking at. Alt+click, which adds a caret instead of moving to
// one, goes through AddCaretAt and never reaches here.
func (t *Tab) MoveCursorTo(p Position, extend bool) {
	t.Carets = nil
	p = t.Buffer.Clamp(p)
	t.Cursor = p
	if !extend {
		t.Anchor = p
	}
	t.cursorMoved = true
	t.breakUndoGroup()
}

// MoveLineHome moves every caret to column 0 of its own line.
func (t *Tab) MoveLineHome(extend bool) {
	t.applyAtCarets(false, func() {
		t.Cursor.Col = 0
		if !extend {
			t.Anchor = t.Cursor
		}
		t.cursorMoved = true
		t.breakUndoGroup()
	})
}

// MoveLineEnd moves every caret to the last column of its own line —
// the gesture that turns a column of carets into "append to each of
// these lines".
func (t *Tab) MoveLineEnd(extend bool) {
	t.applyAtCarets(false, func() {
		t.Cursor.Col = len([]rune(t.Buffer.Lines[t.Cursor.Line]))
		if !extend {
			t.Anchor = t.Cursor
		}
		t.cursorMoved = true
		t.breakUndoGroup()
	})
}

// SelectAll selects the entire buffer (anchor at start, cursor at end).
// Secondary carets are dropped — one selection spanning everything is
// what the user asked for, and extra carets inside it would each edit
// the same range.
func (t *Tab) SelectAll() {
	t.Carets = nil
	t.Anchor = Position{Line: 0, Col: 0}
	t.Cursor = t.Buffer.EndPos()
	t.cursorMoved = true
	t.breakUndoGroup()
}

// RestoreView puts the cursor, the selection anchor, and both scroll
// offsets back to a state captured earlier — the "abort a peek"
// primitive, used by the Find-all popup when Esc means "put me back".
//
// It is deliberately NOT MoveCursorTo. Every other cursor write sets
// cursorMoved so the next Render scrolls the cursor into view; a restore
// wants the opposite, because the captured scroll is part of what's
// being restored. A user who had wheeled away from their own cursor
// before peeking must get that view back, not a viewport yanked to the
// cursor. Clearing the flag also swallows any pending move the peek
// itself armed. Positions are clamped: a background reload can shrink
// the buffer while a peek is open.
func (t *Tab) RestoreView(cursor, anchor Position, scrollY, scrollX int) {
	if t.Buffer == nil {
		return
	}
	t.Cursor = t.Buffer.Clamp(cursor)
	t.Anchor = t.Buffer.Clamp(anchor)
	t.ScrollY, t.ScrollX = scrollY, scrollX
	t.cursorMoved = false
}

// CursorLineVisible reports whether the cursor's line falls inside a
// viewH-row viewport at the current scroll offset. Callers that want to
// scroll only when they have to (CenterOnCursor's "off-screen only"
// rule) ask this first, rather than each re-deriving the window bounds.
func (t *Tab) CursorLineVisible(viewH int) bool {
	return viewH > 0 && t.Cursor.Line >= t.ScrollY && t.Cursor.Line < t.ScrollY+viewH
}

// CenterOnCursor scrolls the viewport so the cursor's LINE sits in the
// middle of a viewH-row view instead of merely being on screen — the
// "peek at this" counterpart to EnsureVisible, used by the Find-all
// popup to land an off-screen hit with context on both sides of it.
// Unconditional by design: whether a line already on screen is worth
// re-centering is the caller's policy, not this primitive's (see
// CursorLineVisible).
//
// Two details it must keep. Horizontal scroll goes through EnsureVisible
// rather than a second copy of the column rule — centering is a vertical
// idea and a long line still has to bring its match into view. And the
// cursorMoved flag is CLEARED afterwards (the RestoreView rationale):
// leaving it set would let the next Render run EnsureVisible over the
// top of the centering, which minimally-scrolls the line straight back
// to an edge.
//
// Near the top of a file the line simply can't be centered, so it sits
// wherever ScrollY 0 puts it; near the bottom, Render's clampScroll has
// the last word — its overscroll allowance (half a view) is exactly
// enough to keep centering honest down to the final line.
func (t *Tab) CenterOnCursor(viewW, viewH int) {
	if viewH <= 0 {
		return
	}
	t.EnsureVisible(viewW, viewH)
	t.ScrollY = t.Cursor.Line - viewH/2
	if t.ScrollY < 0 {
		t.ScrollY = 0
	}
	t.cursorMoved = false
}

// EnsureVisible scrolls the viewport so the cursor is on screen. The
// caller passes the editor area's width and height because the Tab itself
// doesn't know its render rect.
func (t *Tab) EnsureVisible(viewW, viewH int) {
	contentW := viewW - gutterWidth - 1
	if contentW < 1 {
		contentW = 1
	}
	if t.Cursor.Line < t.ScrollY {
		t.ScrollY = t.Cursor.Line
	}
	if t.Cursor.Line >= t.ScrollY+viewH {
		t.ScrollY = t.Cursor.Line - viewH + 1
	}
	if t.Cursor.Col < t.ScrollX {
		t.ScrollX = t.Cursor.Col
	}
	if t.Cursor.Col >= t.ScrollX+contentW {
		t.ScrollX = t.Cursor.Col - contentW + 1
	}
	if t.ScrollY < 0 {
		t.ScrollY = 0
	}
	if t.ScrollX < 0 {
		t.ScrollX = 0
	}
}

// Render draws the editor's content (line numbers, code with syntax
// highlighting, selection, cursor) into the rectangle (x, y, w, h).
// Image tabs delegate to renderImage instead of drawing text.
func (t *Tab) Render(scr tcell.Screen, th theme.Theme, x, y, w, h int) {
	if t.IsImage() {
		t.renderImage(scr, th, x, y, w, h)
		return
	}
	// Re-lex only when it's actually due — syntax.go's settle policy keeps
	// a typing burst from paying the O(file) Chroma cost per keystroke,
	// and paints from a patched grid until the buffer goes quiet.
	if t.needsRelex() {
		t.Styles = Highlight(t.Path, t.Buffer.String(), th)
		t.StyleStale = false
		t.styleDefer = false
	}
	// Only re-center on the cursor if the cursor moved this tick. Doing it
	// every render fights the user when they scroll with the wheel.
	if t.cursorMoved {
		t.EnsureVisible(w, h)
		t.cursorMoved = false
	}
	t.clampScroll(h)

	bg := th.BG
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(th.Text)

	// Paint the entire editor rectangle with the base background first so
	// any cells we don't draw (short lines, blank rows) still get themed.
	for cy := y; cy < y+h; cy++ {
		for cx := x; cx < x+w; cx++ {
			scr.SetContent(cx, cy, ' ', nil, bgStyle)
		}
	}

	// Collect decoration spans and gutter marks for the visible window
	// once per frame; the row loop below merges them over the syntax
	// grid. Selection and find highlighting arrive through here too —
	// they're the built-in sources (see decoration.go).
	spans, marks := t.collectDecorations(th, t.ScrollY, t.ScrollY+h-1)
	markByLine := make(map[int]GutterMark, len(marks))
	for _, mk := range marks {
		// Map overwrite = "latest source wins", the same precedence rule
		// spans use.
		markByLine[mk.Line] = mk
	}

	contentX := x + gutterWidth + 1
	contentW := w - gutterWidth - 1
	if contentW < 1 {
		contentW = 1
	}

	for row := 0; row < h; row++ {
		lineIdx := t.ScrollY + row
		if lineIdx >= t.Buffer.LineCount() {
			break
		}
		cy := y + row
		isCursorLine := lineIdx == t.Cursor.Line

		// Pick the row background — a hair lighter on the cursor's line so
		// the eye can catch where the caret is from across the screen.
		lineBg := bg
		if isCursorLine {
			lineBg = th.LineHL
		}
		lineBgStyle := tcell.StyleDefault.Background(lineBg).Foreground(th.Text)

		// Re-paint this row with its (possibly highlighted) bg.
		for cx := x; cx < x+w; cx++ {
			scr.SetContent(cx, cy, ' ', nil, lineBgStyle)
		}

		// Gutter / line number, right-aligned with one trailing space.
		numStr := fmt.Sprintf("%*d", gutterWidth-1, lineIdx+1)
		gutterStyle := tcell.StyleDefault.Background(lineBg).Foreground(th.Muted)
		if isCursorLine {
			gutterStyle = gutterStyle.Foreground(th.AccentSoft)
		}
		for i, r := range numStr {
			scr.SetContent(x+i, cy, r, nil, gutterStyle)
		}

		// Gutter mark column — the single cell between the numbers and
		// the code. Blank on unmarked lines, so with no sources active
		// the layout is pixel-identical to the pre-decoration renderer.
		if mk, ok := markByLine[lineIdx]; ok {
			markStyle := tcell.StyleDefault.Background(lineBg).Foreground(mk.FG)
			scr.SetContent(x+gutterWidth, cy, mk.Glyph, nil, markStyle)
		}

		// Line content: effective styles first, then the paint walk.
		// We walk from the start of the line so tab stops anchor to col 0
		// — a tab one cell into the line still expands to the next stop,
		// not the next-stop-from-the-scroll-offset. ScrollX skips runes;
		// the visual column we paint at is rune-walked from there.
		runes := []rune(t.Buffer.Lines[lineIdx])
		var styles []tcell.Style
		if lineIdx < len(t.Styles) {
			styles = t.Styles[lineIdx]
		}
		// Resolve each rune's final style up front — syntax + row bg,
		// then span deltas in precedence order — so the paint loop
		// below stays a straight walk (it already has tab-stop
		// bookkeeping to carry). O(lineLen + coveredCells) per row,
		// which beats the per-cell match probing this replaced.
		rowStyles := make([]tcell.Style, len(runes))
		for i := range rowStyles {
			st := bgStyle
			if i < len(styles) {
				st = styles[i]
			}
			rowStyles[i] = st.Background(lineBg)
		}
		for _, sp := range spans {
			if s, e, ok := sp.colRange(lineIdx, len(runes)); ok {
				for i := s; i < e; i++ {
					rowStyles[i] = sp.Delta.Apply(rowStyles[i])
				}
			}
		}
		// Inline-suggestion ghost text: splice the dimmed proposal into
		// the cursor row AFTER spans resolve, so decoration precedence
		// is untouched and the paint walk below (tab stops, ScrollX,
		// overflow arrows) handles the widened row with zero ghost
		// awareness. See ghost.go for why this isn't a DecorationSource.
		if t.Ghost != nil {
			ghostStyle := tcell.StyleDefault.Background(lineBg).
				Foreground(th.Muted).Attributes(tcell.AttrItalic)
			runes, rowStyles = t.ghostOverlay(lineIdx, runes, rowStyles, ghostStyle)
		}
		scrollVisual := LineVisualCol(runes, t.ScrollX)
		visualCol := 0 // visual cell offset from the start of the LINE
		for runeIdx, r := range runes {
			width := RuneVisualWidth(r, visualCol)
			if runeIdx >= t.ScrollX {
				// Once we're past ScrollX, paint each cell of this rune.
				// The rune's first cell shows the actual glyph (or ' '
				// for tabs); padding cells for a multi-cell tab show a
				// space so the trailing tab area still gets the right bg.
				st := rowStyles[runeIdx]
				glyph := r
				if r == '\t' {
					glyph = ' '
				}
				for cell := 0; cell < width; cell++ {
					sc := visualCol - scrollVisual + cell
					if sc < 0 {
						continue
					}
					if sc >= contentW {
						break
					}
					ch := glyph
					if cell > 0 {
						ch = ' '
					}
					scr.SetContent(contentX+sc, cy, ch, nil, st)
				}
			}
			visualCol += width
		}

		// Overflow affordance: paint a muted '‹' / '›' over the leftmost /
		// rightmost content cell when the line extends past the viewport
		// in that direction. Without this hint a terminal user has no way
		// to tell that more content exists off-screen — there's no
		// scrollbar to clue them in. visualCol now equals the total
		// visual width of the line; scrollVisual is the visual cell
		// corresponding to ScrollX.
		overflowStyle := tcell.StyleDefault.Background(lineBg).Foreground(th.Muted)
		if t.ScrollX > 0 {
			scr.SetContent(contentX, cy, '‹', nil, overflowStyle)
		}
		if visualCol-scrollVisual > contentW {
			scr.SetContent(contentX+contentW-1, cy, '›', nil, overflowStyle)
		}

		// Secondary carets sitting on this row. Painted here rather than
		// emitted as decoration spans because a caret is a zero-width
		// POSITION: the one at the end of a line has no cell for a Span
		// to cover, and end-of-line is exactly where a column of carets
		// lands after Home/End. See multicaret.go.
		t.paintCarets(scr, th, lineIdx, cy, contentX, contentW, lineBg)
	}

	// Position the hardware cursor at its visual column (so a cursor
	// past a tab lands at the tab's *end* cell, not just rune-Col cells
	// to the right of ScrollX).
	cy := y + (t.Cursor.Line - t.ScrollY)
	cursorRunes := t.Buffer.LineRunes(t.Cursor.Line)
	cursorVisual := LineVisualCol(cursorRunes, t.Cursor.Col)
	scrollVisual := LineVisualCol(cursorRunes, t.ScrollX)
	cx := contentX + (cursorVisual - scrollVisual)
	if cy >= y && cy < y+h && cx >= contentX && cx < contentX+contentW {
		scr.ShowCursor(cx, cy)
	} else {
		scr.HideCursor()
	}
}

// CursorScreenCell returns the cursor's cell offset within a w×h render
// of this tab — the same math Render uses to place the hardware cursor.
// ok=false when the cursor is currently scrolled out of view. Callers
// (the hover popup) use it to anchor overlays to the caret without
// duplicating the gutter/tab-stop arithmetic.
func (t *Tab) CursorScreenCell(w, h int) (dx, dy int, ok bool) {
	contentX := gutterWidth + 1
	contentW := w - gutterWidth - 1
	if contentW < 1 {
		contentW = 1
	}
	dy = t.Cursor.Line - t.ScrollY
	runes := t.Buffer.LineRunes(t.Cursor.Line)
	dx = contentX + (LineVisualCol(runes, t.Cursor.Col) - LineVisualCol(runes, t.ScrollX))
	if dy < 0 || dy >= h || dx < contentX || dx >= contentX+contentW {
		return 0, 0, false
	}
	return dx, dy, true
}

// HitTest converts screen coordinates within this tab's render area to a
// buffer position. ok=false means the click was outside any line.
func (t *Tab) HitTest(localX, localY, w, h int) (Position, bool) {
	if localY < 0 || localY >= h {
		return Position{}, false
	}
	contentX := gutterWidth + 1
	line := t.ScrollY + localY
	if line < 0 || line >= t.Buffer.LineCount() {
		return Position{}, false
	}
	if localX < contentX {
		// Click in the gutter — treat as click at column 0 of that line.
		return Position{Line: line, Col: 0}, true
	}
	runes := []rune(t.Buffer.Lines[line])
	// Convert the click's screen column back to a rune column. With tabs
	// expanding to multi-cell tab stops we can't just subtract ScrollX
	// from localX — we have to walk the runes counting visual cells.
	scrollVisual := LineVisualCol(runes, t.ScrollX)
	targetVisual := scrollVisual + (localX - contentX)
	col := RuneColAtVisual(runes, targetVisual)
	if col > len(runes) {
		col = len(runes)
	}
	if col < 0 {
		col = 0
	}
	return Position{Line: line, Col: col}, true
}

// Scroll moves the viewport by delta lines (negative = up). Render runs
// clampScroll afterwards so the user never scrolls into pure void; here we
// just adjust the raw value.
func (t *Tab) Scroll(deltaLines int) {
	t.ScrollY += deltaLines
	if t.ScrollY < 0 {
		t.ScrollY = 0
	}
}

// ScrollH moves the viewport horizontally by delta rune-columns (negative
// = left). Clamped at zero; the right side is naturally bounded by
// Render's contentW window — scrolling past the longest visible line just
// shows blank space, which is fine. Lives next to Scroll so the app's
// mouse-wheel dispatcher can treat horizontal and vertical wheels
// symmetrically.
func (t *Tab) ScrollH(deltaCols int) {
	t.ScrollX += deltaCols
	if t.ScrollX < 0 {
		t.ScrollX = 0
	}
}

// clampScroll keeps ScrollY inside a sensible range for the current viewport
// height. The max is "last line still on screen, plus a small overscroll
// pad" so the user can scroll the bottom of the file up to the middle of
// the viewport — which feels much better than abruptly stopping when the
// last line hits the bottom row.
func (t *Tab) clampScroll(viewH int) {
	if t.ScrollY < 0 {
		t.ScrollY = 0
	}
	overscroll := viewH / 2
	if overscroll < 3 {
		overscroll = 3
	}
	max := t.Buffer.LineCount() - viewH + overscroll
	if max < 0 {
		max = 0
	}
	if t.ScrollY > max {
		t.ScrollY = max
	}
}
