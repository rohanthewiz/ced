// =============================================================================
// File: internal/editor/bookmark.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// bookmark.go is LINE BOOKMARKS: lines the user marked to come back to,
// drawn in the gutter and walked by the app's Next / Previous / list
// verbs (app/bookmarks.go). This file owns the one hard part — keeping a
// bookmark on ITS line while the buffer changes under it.
//
// # Why the lines are re-derived from content, not shifted by edits
//
// The obvious design is to shift every bookmark at the edit primitives:
// InsertString of two newlines above line 40 moves a bookmark on 40 to
// 42. It does not survive this codebase. Buffer.Lines is exported and
// rewritten wholesale by paths that never call a primitive: undo / redo
// restore a snapshot (undo.go), MoveLines rotates lines in place,
// DuplicateLines splices, Reload / ReloadAsEdit replace the Buffer, and a
// formatter run rewrites the file on disk and is adopted as text. Every
// one of those would need its own hook, and the one that was forgotten
// would silently strand bookmarks on the wrong line.
//
// So instead the tab keeps a SNAPSHOT of the lines as they were the last
// time the bookmarks were known to be right, and whenever EditRev has
// moved, the old and new line lists are compared:
//
//	old:  a b c d e f g        common prefix p = 2 (a b)
//	new:  a b X Y d e f g      common suffix s = 4 (d e f g)
//	          └┬┘
//	           changed region: old [p, len(old)-s) = [2,3) "c"
//	                           new [p, len(new)-s) = [2,4) "X Y"
//
//   - A bookmark ABOVE the region (line < p) is untouched.
//   - A bookmark BELOW it shifts by len(new)-len(old) — this is the
//     common case by far (typing, Enter, paste and line deletes all
//     change one small region), and it is exact.
//   - A bookmark INSIDE it is looked for by its recorded text, nearest
//     its old line, within the new region (a moved or swapped line is
//     found where it went); failing that it keeps its place, clamped
//     into the region (the line was rewritten — it is still "this line").
//
// Every mutation path is covered by the same comparison because it
// reads only the result, never the gesture. The cost is one pass of
// string compares per edit, and only for tabs that HAVE bookmarks; Go
// compares unchanged lines by pointer first, and lines an edit did not
// touch still share their backing storage with the snapshot.
//
// # The ambiguity the region rule cannot see
//
// Greedy prefix / suffix matching cannot tell WHICH of two identical
// adjacent lines an edit removed: deleting the first of `b b` reads as
// deleting the second. A bookmark on the second `b` then sits in a
// region that vanished. The fix is narrow: when the region SHRANK (a net
// deletion) and the bookmark's text is the line just outside the region,
// the bookmark lands there — the surviving twin. It is limited to net
// deletions on purpose: when the region kept its size the line was
// edited in place, and a blank bookmarked line you just typed into must
// not hop to the blank line above it.
//
// Restoring from disk (SetBookmarks) is the other content-keyed path:
// the file may have changed since the bookmark was saved — a formatter,
// a pull, an edit in another editor — so a stored bookmark whose line no
// longer reads the same is looked for by its text across the whole file.

package editor

import (
	"sort"
	"strings"
)

// BookmarkGlyph is drawn in the gutter's leading cell on a bookmarked
// line. That cell is blank for every line number below 10000 (the
// number is right-aligned in gutterWidth-1 cells), so the flag costs no
// geometry; a five-digit number keeps its cell and the bookmark is told
// by the number's colour alone.
const BookmarkGlyph = '⚑'

// Bookmark is one bookmarked line. Text is the line's content as of the
// last time the bookmark was reconciled: it is the key the bookmark is
// re-found by after a change the region rule cannot place, and after a
// restart against a file that changed on disk meanwhile.
//
// Label is the user's own name for the bookmark ("" = unnamed). It is
// pure payload: nothing here reads it, it only has to ride along with
// the bookmark through every path that rebuilds one (remapBookmarks,
// SetBookmarks, normalizeBookmarks' merge) — the line text is what a
// bookmark is FOUND by, the label is what it is CALLED.
type Bookmark struct {
	Line  int
	Text  string
	Label string
}

// Bookmarks returns the tab's bookmarks, reconciled against the current
// buffer, in line order. The slice is a copy: callers (the app's list
// and the session encoder) hold it across edits.
func (t *Tab) Bookmarks() []Bookmark {
	t.syncBookmarks()
	if len(t.bookmarks) == 0 {
		return nil
	}
	out := make([]Bookmark, len(t.bookmarks))
	copy(out, t.bookmarks)
	return out
}

// BookmarkCount is the number of bookmarks on the tab. It reconciles
// first so a bookmark merged into its neighbour by a delete is not
// counted twice.
func (t *Tab) BookmarkCount() int {
	t.syncBookmarks()
	return len(t.bookmarks)
}

// HasBookmark reports whether line is bookmarked.
func (t *Tab) HasBookmark(line int) bool {
	t.syncBookmarks()
	_, ok := t.bookmarkIndex(line)
	return ok
}

// ToggleBookmark adds a bookmark on line, or removes the one already
// there, and reports whether the line is bookmarked afterwards. Out of
// range lines are clamped — the caller's line came from a caret or a
// click, both of which are inside the buffer by construction. No-op
// (false) on image tabs, which have no lines to mark.
func (t *Tab) ToggleBookmark(line int) bool {
	if t.Buffer == nil || t.IsImage() {
		return false
	}
	t.syncBookmarks()
	line = clampLine(line, len(t.Buffer.Lines))
	if i, ok := t.bookmarkIndex(line); ok {
		t.bookmarks = append(t.bookmarks[:i], t.bookmarks[i+1:]...)
		t.snapshotBookmarks()
		return false
	}
	t.bookmarks = append(t.bookmarks, Bookmark{Line: line})
	t.normalizeBookmarks()
	t.snapshotBookmarks()
	return true
}

// BookmarkAt returns the bookmark on line, if there is one.
func (t *Tab) BookmarkAt(line int) (Bookmark, bool) {
	t.syncBookmarks()
	if i, ok := t.bookmarkIndex(line); ok {
		return t.bookmarks[i], true
	}
	return Bookmark{}, false
}

// SetBookmarkLabel names the bookmark on line, adding the bookmark when
// the line has none (naming a line is a way of marking it — refusing
// with "toggle it first" would be a second gesture for no reason). An
// empty label clears the name and keeps the bookmark. Reports whether
// the line is bookmarked afterwards: false only for image tabs and tabs
// with no buffer, which have no lines.
func (t *Tab) SetBookmarkLabel(line int, label string) bool {
	if t.Buffer == nil || t.IsImage() {
		return false
	}
	t.syncBookmarks()
	line = clampLine(line, len(t.Buffer.Lines))
	if i, ok := t.bookmarkIndex(line); ok {
		t.bookmarks[i].Label = label
		return true
	}
	t.bookmarks = append(t.bookmarks, Bookmark{Line: line, Label: label})
	t.normalizeBookmarks()
	t.snapshotBookmarks()
	return true
}

// SetBookmarks replaces the tab's bookmarks with stored ones — a
// session restore, or the app handing back the bookmarks it parked when
// the tab was last closed. Each is re-anchored by its text first
// (anchorStored), since the file on disk may have moved since.
func (t *Tab) SetBookmarks(bms []Bookmark) {
	t.bookmarks = nil
	if t.Buffer == nil || t.IsImage() {
		t.snapshotBookmarks()
		return
	}
	for _, b := range bms {
		t.bookmarks = append(t.bookmarks, Bookmark{Line: anchorStored(t.Buffer.Lines, b), Label: b.Label})
	}
	t.normalizeBookmarks()
	t.snapshotBookmarks()
}

// ClearBookmarks removes every bookmark on the tab and reports how many
// there were.
func (t *Tab) ClearBookmarks() int {
	t.syncBookmarks()
	n := len(t.bookmarks)
	t.bookmarks = nil
	t.snapshotBookmarks()
	return n
}

// bookmarkLineSet is the render walk's view: the bookmarked lines as a
// set, nil when there are none (so a tab without bookmarks pays one
// length check per frame).
func (t *Tab) bookmarkLineSet() map[int]bool {
	t.syncBookmarks()
	if len(t.bookmarks) == 0 {
		return nil
	}
	set := make(map[int]bool, len(t.bookmarks))
	for _, b := range t.bookmarks {
		set[b.Line] = true
	}
	return set
}

// bookmarkIndex binary-searches the (sorted) bookmark list for line.
func (t *Tab) bookmarkIndex(line int) (int, bool) {
	i := sort.Search(len(t.bookmarks), func(i int) bool { return t.bookmarks[i].Line >= line })
	return i, i < len(t.bookmarks) && t.bookmarks[i].Line == line
}

// syncBookmarks brings the bookmarks up to date with the buffer when it
// has changed since the last snapshot. See the file header for the
// comparison it runs.
func (t *Tab) syncBookmarks() {
	if len(t.bookmarks) == 0 || t.Buffer == nil {
		return
	}
	if t.bmLines != nil && t.bmRev == t.EditRev {
		return
	}
	t.bookmarks = remapBookmarks(t.bmLines, t.Buffer.Lines, t.bookmarks)
	t.normalizeBookmarks()
	t.snapshotBookmarks()
}

// snapshotBookmarks records the buffer the bookmarks were just placed
// against, and refreshes each bookmark's Text from it. With no
// bookmarks the snapshot is dropped — it exists only to diff against.
//
// The lines are COPIED, not aliased: MoveLines and single-line
// InsertString rewrite Buffer.Lines' backing array in place, so a
// shared slice would show the snapshot the edit it is meant to detect.
// Only the string headers are copied; the line text itself is shared.
func (t *Tab) snapshotBookmarks() {
	if len(t.bookmarks) == 0 || t.Buffer == nil {
		t.bmLines = nil
		t.bmRev = t.EditRev
		return
	}
	t.bmLines = append(t.bmLines[:0:0], t.Buffer.Lines...)
	t.bmRev = t.EditRev
	for i := range t.bookmarks {
		t.bookmarks[i].Text = t.Buffer.Lines[t.bookmarks[i].Line]
	}
}

// normalizeBookmarks sorts by line, clamps into the buffer and drops
// duplicates — two bookmarks a delete pushed onto the same line become
// one, which is what the gutter (one flag per line) can show anyway.
// The survivor is the first in line order; when it is unnamed and the
// one merged into it had a label, the label is kept — a name the user
// typed is worth more than which of two colliding bookmarks "won".
func (t *Tab) normalizeBookmarks() {
	n := len(t.Buffer.Lines)
	for i := range t.bookmarks {
		t.bookmarks[i].Line = clampLine(t.bookmarks[i].Line, n)
	}
	sort.SliceStable(t.bookmarks, func(i, j int) bool { return t.bookmarks[i].Line < t.bookmarks[j].Line })
	out := t.bookmarks[:0]
	for i, b := range t.bookmarks {
		if i > 0 && b.Line == out[len(out)-1].Line {
			if last := &out[len(out)-1]; last.Label == "" {
				last.Label = b.Label
			}
			continue
		}
		out = append(out, b)
	}
	t.bookmarks = out
}

// remapBookmarks moves bookmarks placed against old onto cur. The
// result is unsorted and may hold duplicates; normalizeBookmarks
// settles both. A nil old (no snapshot yet) leaves lines as they are.
func remapBookmarks(old, cur []string, bms []Bookmark) []Bookmark {
	if old == nil {
		return bms
	}
	n0, n1 := len(old), len(cur)
	lim := min(n0, n1)
	// Common prefix, then common suffix limited so the two never
	// overlap — on a pure insertion or deletion one region is empty.
	p := 0
	for p < lim && old[p] == cur[p] {
		p++
	}
	s := 0
	for s < lim-p && old[n0-1-s] == cur[n1-1-s] {
		s++
	}
	oldEnd, newEnd := n0-s, n1-s
	out := make([]Bookmark, 0, len(bms))
	for _, b := range bms {
		line := b.Line
		switch {
		case line < p:
			// Above every change: nothing moved it.
		case line >= oldEnd:
			line += n1 - n0
		default:
			line = anchorInRegion(cur, p, newEnd, oldEnd-p, b)
		}
		out = append(out, Bookmark{Line: line, Text: b.Text, Label: b.Label})
	}
	return out
}

// anchorInRegion places a bookmark whose line fell inside the changed
// region. cur[p:newEnd] is the region's new content and oldSize its old
// line count. In order:
//
//  1. its text inside the new region, nearest its old line (a line the
//     edit moved — MoveLines, an undo that swapped blocks);
//  2. on a net deletion, a twin of its text just outside the region
//     (see "The ambiguity" in the file header);
//  3. its old line clamped into the region — the line was rewritten,
//     and the bookmark stays on what is now there;
//  4. the region is gone entirely: the line that now follows it.
//
// Below the old line is searched before above at each distance: an
// edit inside a region more often pushed lines down (an insertion) than
// up, so on a tie the shifted position is the likelier one.
func anchorInRegion(cur []string, p, newEnd, oldSize int, b Bookmark) int {
	if newEnd > p {
		want := b.Line
		if want >= newEnd {
			want = newEnd - 1
		}
		if at, ok := nearestText(cur, p, newEnd, want, b.Text); ok {
			return at
		}
	}
	if newEnd-p < oldSize {
		if p > 0 && cur[p-1] == b.Text {
			return p - 1
		}
		if newEnd < len(cur) && cur[newEnd] == b.Text {
			return newEnd
		}
	}
	if newEnd > p {
		if b.Line >= newEnd {
			return newEnd - 1
		}
		return b.Line
	}
	return p
}

// anchorStored places a bookmark coming back from storage: its own line
// if that line still reads the same, else the nearest line anywhere in
// the file with its text, else its old line clamped. A blank (or
// whitespace-only) line is never searched for — every file has dozens,
// so "the nearest one" carries no information about where the bookmark
// went, and staying put is the better guess.
func anchorStored(lines []string, b Bookmark) int {
	line := clampLine(b.Line, len(lines))
	if lines[line] == b.Text || strings.TrimSpace(b.Text) == "" {
		return line
	}
	if at, ok := nearestText(lines, 0, len(lines), line, b.Text); ok {
		return at
	}
	return line
}

// nearestText finds text in lines[lo:hi] nearest to want (which must be
// inside the range), checking below before above at each distance.
func nearestText(lines []string, lo, hi, want int, text string) (int, bool) {
	for d := 0; want-d >= lo || want+d < hi; d++ {
		if below := want + d; below < hi && lines[below] == text {
			return below, true
		}
		if above := want - d; d > 0 && above >= lo && lines[above] == text {
			return above, true
		}
	}
	return 0, false
}

// clampLine pins line into [0, n-1]; n is a buffer's line count, which
// is always at least 1.
func clampLine(line, n int) int {
	if line >= n {
		line = n - 1
	}
	if line < 0 {
		line = 0
	}
	return line
}
