// =============================================================================
// File: internal/filetree/filter_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-18
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the tree's half of type-to-find: which rows a pattern matches
// inside which scope, and that the paint lights exactly the typed letters
// without changing the row's text or the tree's measured width.

package filetree

import (
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// filterTree builds root/{app/{alpha.go, apple.go, beta.go}, apps/amber.go,
// avocado.txt} with both folders expanded, so a scope test has a sibling
// whose path shares the scope's string prefix.
func filterTree(t *testing.T) *Tree {
	t.Helper()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "app"))
	mustMkdir(t, filepath.Join(root, "apps"))
	for _, rel := range []string{"app/alpha.go", "app/apple.go", "app/beta.go", "apps/amber.go", "avocado.txt"} {
		mustWrite(t, filepath.Join(root, rel), "x")
	}
	tr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, c := range tr.Root.Children {
		if c.IsDir {
			tr.Toggle(c)
		}
	}
	return tr
}

// names flattens nodes to their names for readable failures.
func names(ns []*Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Name
	}
	return out
}

// TestFilterMatches_RootScopeIsCaseInsensitiveAndOrdered pins the basic
// contract: every visible row whose name contains the pattern ANYWHERE,
// whatever its case, in display order — element 0 is where the app puts
// the cursor. beta.go matching "A" by its last letter is the point.
func TestFilterMatches_RootScopeIsCaseInsensitiveAndOrdered(t *testing.T) {
	tr := filterTree(t)
	tr.SetFilter("A", tr.Root.Path)
	got := names(tr.FilterMatches())
	want := []string{"app", "alpha.go", "apple.go", "beta.go", "apps", "amber.go", "avocado.txt"}
	if len(got) != len(want) {
		t.Fatalf("matches = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("matches = %v, want %v", got, want)
		}
	}
}

// TestFilterMatches_PatternStaysTogether pins "not a fuzzy search": the
// pattern must occur as a contiguous run, so "ao" does not match
// avocado.txt even though a…o appear in order.
func TestFilterMatches_PatternStaysTogether(t *testing.T) {
	tr := filterTree(t)
	tr.SetFilter("ple", tr.Root.Path)
	if got := names(tr.FilterMatches()); len(got) != 1 || got[0] != "apple.go" {
		t.Fatalf("'ple' matches = %v, want [apple.go]", got)
	}
	tr.SetFilter("ao", tr.Root.Path)
	if got := tr.FilterMatches(); len(got) != 0 {
		t.Fatalf("'ao' should not fuzzy-match: %v", names(got))
	}
}

// TestFilterSpan_FirstOccurrenceInRunes pins where the highlight goes: the
// FIRST occurrence, as a rune column, so a multibyte rune before the match
// cannot push the lit cells off the letters that matched.
func TestFilterSpan_FirstOccurrenceInRunes(t *testing.T) {
	tr := filterTree(t)
	n := &Node{Name: "éta-beta.go", Path: filepath.Join(tr.Root.Path, "éta-beta.go")}
	tr.SetFilter("TA", tr.Root.Path)
	if start, k := tr.filterSpan(n); start != 1 || k != 2 {
		t.Fatalf("span = (%d, %d), want (1, 2)", start, k)
	}
}

// TestFilterMatches_ScopeIsTheFolderAndBelow pins "the current folder and
// below": the scope folder itself is not a match, and a sibling whose path
// merely shares the scope's string prefix (app vs apps) is outside it.
func TestFilterMatches_ScopeIsTheFolderAndBelow(t *testing.T) {
	tr := filterTree(t)
	tr.SetFilter("pp", filepath.Join(tr.Root.Path, "app"))
	got := names(tr.FilterMatches())
	if len(got) != 1 || got[0] != "apple.go" {
		t.Fatalf("scoped matches = %v, want [apple.go]", got)
	}
}

// TestFilterMatches_CollapsedRowsDoNotMatch pins the visible-only rule: a
// match inside a folded folder would be a highlight nobody can see.
func TestFilterMatches_CollapsedRowsDoNotMatch(t *testing.T) {
	tr := filterTree(t)
	tr.Toggle(findChild(tr.Root, "app")) // fold it
	tr.SetFilter("lph", tr.Root.Path)
	if got := tr.FilterMatches(); len(got) != 0 {
		t.Fatalf("folded alpha.go matched: %v", names(got))
	}
}

// TestSetFilter_EmptyClears pins that an empty pattern is "no filter", not
// "match everything" — Backspace to empty must switch the highlight off.
func TestSetFilter_EmptyClears(t *testing.T) {
	tr := filterTree(t)
	tr.SetFilter("a", tr.Root.Path)
	tr.SetFilter("", tr.Root.Path)
	if tr.Filter != "" || tr.FilterScope != "" || tr.FilterMatches() != nil {
		t.Fatal("empty pattern should clear the filter")
	}
}

// TestRender_FilterLightsOnlyTheTypedLetters pins the paint: the matched
// cells MID-NAME take the find-match wash and bold, the letters on either
// side do not, and the row's text is unchanged.
func TestRender_FilterLightsOnlyTheTypedLetters(t *testing.T) {
	tr := filterTree(t)
	tr.SetFilter("ET", tr.Root.Path)
	const w, h = 40, 20
	cells, cw := renderAndCollect(t, tr, w, h)
	y := findRowY(cells, cw, h, "beta.go")
	if y < 0 {
		t.Fatal("beta.go not drawn")
	}
	text := []rune(rowText(cells, cw, y))
	x := -1
	for i := 0; i+1 < len(text); i++ {
		if text[i] == 'e' && text[i+1] == 't' {
			x = i
			break
		}
	}
	if x < 0 {
		t.Fatalf("row text changed: %q", string(text))
	}
	want := theme.Default().FindMatch
	for i := 0; i < 2; i++ {
		_, bg, attr := cells[y*cw+x+i].Style.Decompose()
		if bg != want || attr&tcell.AttrBold == 0 {
			t.Fatalf("cell %d of the match not lit (bg %v, attr %v)", i, bg, attr)
		}
	}
	if _, bg, _ := cells[y*cw+x-1].Style.Decompose(); bg == want {
		t.Fatal("the letter before the match should not be lit")
	}
	if _, bg, _ := cells[y*cw+x+2].Style.Decompose(); bg == want {
		t.Fatal("the letter after the match should not be lit")
	}
	// A non-matching row carries no wash at all.
	ay := findRowY(cells, cw, h, "alpha.go")
	for i := 0; i < cw; i++ {
		if _, bg, _ := cells[ay*cw+i].Style.Decompose(); bg == want {
			t.Fatal("non-matching row was lit")
		}
	}
}

// TestFilter_CostsNoWidth pins that the highlight is paint-only: the
// sidebar's auto-fit reads ContentWidth, and a filter that widened it
// would move the editor's columns on every keystroke.
func TestFilter_CostsNoWidth(t *testing.T) {
	tr := filterTree(t)
	before := tr.ContentWidth()
	tr.SetFilter("a", tr.Root.Path)
	if got := tr.ContentWidth(); got != before {
		t.Fatalf("ContentWidth %d → %d with a filter armed", before, got)
	}
}

// TestNameColumn_TracksIcons pins that the highlight's start column is
// where the NAME begins in both row shapes, so it can't land a cell off.
func TestNameColumn_TracksIcons(t *testing.T) {
	tr := filterTree(t)
	item := flatNode{Node: findChild(tr.Root, "avocado.txt"), Depth: 0}
	for _, icons := range []bool{false, true} {
		prefix, glyph, tail := nodeRowSegments(item, icons, false)
		row := []rune(prefix + glyph + tail)
		col := nameColumn(item, icons, false)
		if string(row[col:col+3]) != "avo" {
			t.Fatalf("icons=%v: name column %d lands on %q", icons, col, string(row[col:]))
		}
	}
}
