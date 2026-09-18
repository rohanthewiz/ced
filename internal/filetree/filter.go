// =============================================================================
// File: internal/filetree/filter.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-18
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// filter.go is the tree's half of type-to-find: a pattern the user is
// typing, a folder it is scoped to, and the answer to "which rows does it
// light up?". Which key grows the pattern, when it is cleared and where the
// cursor goes are the app's business (app/treefilter.go); the tree only
// knows how to match and how to paint a match.
//
// Two decisions shape everything here:
//
//   - IT MATCHES VISIBLE ROWS ONLY. "The current folder and below" means
//     below as far as the user has expanded it. The tree is lazy, so a
//     deep match would be a directory walk per keystroke, and a match
//     inside a collapsed folder would be a highlight nobody can see —
//     the multi-selection's "a set nobody can see" rule. The finder is
//     the tool for names buried in folders nobody opened.
//   - THE SCOPE IS A PATH, NOT A *Node, for the Marked map's reason: a
//     refresh that rewrites a folder hands its rows fresh pointers, and a
//     scope held by pointer would silently stop matching anything.

package filetree

import (
	"path/filepath"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// SetFilter arms (or, with an empty pattern, clears) the type-to-find
// highlight. scope is the absolute path of the folder whose visible
// descendants are searched; an empty scope, or one naming the root,
// searches every visible row.
func (t *Tree) SetFilter(pattern, scope string) {
	if pattern == "" {
		t.ClearFilter()
		return
	}
	t.Filter = pattern
	t.FilterScope = scope
}

// ClearFilter drops the pattern and its scope, so Render paints no match
// highlight at all.
func (t *Tree) ClearFilter() {
	t.Filter = ""
	t.FilterScope = ""
}

// FilterMatches returns every visible row inside the filter's scope whose
// name contains the pattern, case-insensitively, in display order — so
// element 0 is the "first match" the app moves the cursor to. nil when no
// filter is armed.
func (t *Tree) FilterMatches() []*Node {
	if t == nil || t.Root == nil || t.Filter == "" {
		return nil
	}
	var out []*Node
	for _, n := range t.VisibleNodes() {
		if t.filterHit(n) {
			out = append(out, n)
		}
	}
	return out
}

// filterHit reports whether n is a match: inside the scope and with the
// pattern occurring ANYWHERE in its name (kept together — a substring,
// not a fuzzy subsequence). The scope test is a path-prefix test WITH a
// trailing separator, or a scope of /a/foo would claim rows under
// /a/foobar (the commonParentDir trap). The scope folder itself is not a
// match of its own search — the user asked about what is in it.
func (t *Tree) filterHit(n *Node) bool {
	_, k := t.filterSpan(n)
	return k > 0
}

// filterSpan locates the pattern in n's name, returning the rune column
// where the first occurrence starts and its length in runes — (0, 0) when
// n is not a match. Columns are RUNES because drawString advances one
// column per rune, and the fold is per rune (unicode.ToLower on each)
// rather than strings.ToLower on the whole name: a few runes lowercase to
// more than one, and a fold that changed the rune count would shift the
// lit cells off the letters that matched (find.go's foldRunes rule).
func (t *Tree) filterSpan(n *Node) (start, k int) {
	if n == nil || t.Filter == "" {
		return 0, 0
	}
	if scope := t.FilterScope; scope != "" && scope != t.Root.Path {
		if !strings.HasPrefix(n.Path, scope+string(filepath.Separator)) {
			return 0, 0
		}
	}
	name := foldRunes(n.Name)
	pat := foldRunes(t.Filter)
	if len(pat) > len(name) {
		return 0, 0
	}
	for i := 0; i+len(pat) <= len(name); i++ {
		if runesEqual(name[i:i+len(pat)], pat) {
			return i, len(pat)
		}
	}
	return 0, 0
}

// foldRunes lowercases s rune by rune, preserving its rune count.
func foldRunes(s string) []rune {
	rs := []rune(s)
	for i, r := range rs {
		rs[i] = unicode.ToLower(r)
	}
	return rs
}

// runesEqual reports whether two equal-length rune slices match.
func runesEqual(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// paintFilterMatch re-styles k cells of a row's NAME, starting at the
// match's own column (nameCol already includes it), with the
// find-match wash plus bold, keeping each cell's rune and foreground.
//
// The find colors are borrowed on purpose rather than a new theme key:
// the question on screen is the same one the find bar answers ("where
// does the text I typed occur?"), and FindMatch is already tuned in every
// shipped theme to read against the background without being mistaken
// for the Selection fill — which matters here, because the cursor row IS
// painted on Selection. Bold is paired with the wash so the match
// survives a terminal whose contrast flattens a background step.
//
// Painting AFTER the row (like paintMark) keeps nodeRowSegments — and so
// ContentWidth and the sidebar's auto-fit — untouched by the filter.
func paintFilterMatch(scr tcell.Screen, th theme.Theme, x, y, w, nameCol, k int) {
	for i := 0; i < k; i++ {
		cx := x + nameCol + i
		if nameCol+i >= w {
			return
		}
		r, comb, st, _ := scr.GetContent(cx, y)
		fg, _, _ := st.Decompose()
		scr.SetContent(cx, y, r, comb,
			tcell.StyleDefault.Background(th.FindMatch).Foreground(fg).Bold(true))
	}
}

// nameColumn is the column (relative to the row's x) where the node's
// name begins, derived from the same segments the row is drawn from so
// the highlight can never land a cell off the text. With icons on, the
// tail opens with two spaces before the name.
func nameColumn(item flatNode, withIcons, execMarks bool) int {
	prefix, glyph, _ := nodeRowSegments(item, withIcons, execMarks)
	col := runeLen(prefix) + runeLen(glyph)
	if glyph != "" {
		col += 2
	}
	return col
}
