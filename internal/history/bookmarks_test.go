// =============================================================================
// File: internal/history/bookmarks_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the bookmark table: a round trip by absolute path, the
// per-file comparison (unchanged files untouched, cleared files deleted),
// two instances keeping each other's files, and the stored-form rules.

package history

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestBookmarks_RoundTrip pins that a set written comes back by absolute
// path, normalized: sorted, de-duplicated, negative lines dropped.
func TestBookmarks_RoundTrip(t *testing.T) {
	root := repo(t)
	a := filepath.Join(root, "a.go")
	h := mustLoad(t, root)
	h.SetBookmarks(map[string][]Bookmark{
		a:  {{Line: 9, Text: "nine"}, {Line: 3, Text: "three"}, {Line: 3, Text: "dup"}, {Line: -1}},
		"": {{Line: 1}},
	})
	if !h.Dirty() {
		t.Fatal("a new bookmark set should make the history dirty")
	}
	mustWrite(t, h, root, nil)
	if h.Dirty() {
		t.Error("the history should be clean after the write")
	}

	got := mustLoad(t, root).Bookmarks()
	want := map[string][]Bookmark{a: {{Line: 3, Text: "three"}, {Line: 9, Text: "nine"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded = %+v, want %+v", got, want)
	}
}

// TestBookmarks_StoredRelativeInsideAbsoluteOutside pins the path form:
// the recent-file rule.
func TestBookmarks_StoredRelativeInsideAbsoluteOutside(t *testing.T) {
	root := repo(t)
	outside := filepath.Join(t.TempDir(), "x.go")
	h := mustLoad(t, root)
	h.SetBookmarks(map[string][]Bookmark{
		filepath.Join(root, "pkg", "a.go"): {{Line: 1}},
		outside:                            {{Line: 2}},
	})
	mustWrite(t, h, root, nil)
	back := mustLoad(t, root)
	if _, ok := back.bmStored[filepath.Join("pkg", "a.go")]; !ok {
		t.Errorf("inside path not stored relative: %v", back.bmStored)
	}
	if _, ok := back.bmStored[outside]; !ok {
		t.Errorf("outside path not stored absolute: %v", back.bmStored)
	}
	if got := back.Bookmarks(); len(got[filepath.Join(root, "pkg", "a.go")]) != 1 || len(got[outside]) != 1 {
		t.Errorf("decoded = %+v", got)
	}
}

// TestBookmarks_UnchangedIsCleanAndNeverSetDeletesNothing pins the two
// quiet cases: handing back exactly what was loaded writes nothing, and
// a history the app never handed bookmarks to leaves the rows alone.
func TestBookmarks_UnchangedIsCleanAndNeverSetDeletesNothing(t *testing.T) {
	root := repo(t)
	a := filepath.Join(root, "a.go")
	seed := mustLoad(t, root)
	seed.SetBookmarks(map[string][]Bookmark{a: {{Line: 1, Text: "x"}}})
	mustWrite(t, seed, root, nil)

	h := mustLoad(t, root)
	if h.Dirty() {
		t.Fatal("a fresh load must not be dirty")
	}
	h.SetBookmarks(h.Bookmarks())
	if h.Dirty() {
		t.Error("handing back the loaded set must not be a change")
	}

	other := mustLoad(t, root)
	other.TouchFile(a) // something else to write
	mustWrite(t, other, root, []string{a})
	if got := mustLoad(t, root).Bookmarks(); len(got[a]) != 1 {
		t.Errorf("a write that never set bookmarks deleted them: %+v", got)
	}
}

// TestBookmarks_ClearedFileIsDeleted pins the delete half of the
// comparison.
func TestBookmarks_ClearedFileIsDeleted(t *testing.T) {
	root := repo(t)
	a, b := filepath.Join(root, "a.go"), filepath.Join(root, "b.go")
	h := mustLoad(t, root)
	h.SetBookmarks(map[string][]Bookmark{a: {{Line: 1}}, b: {{Line: 2}}})
	mustWrite(t, h, root, nil)
	h.SetBookmarks(map[string][]Bookmark{b: {{Line: 2}}})
	mustWrite(t, h, root, nil)
	got := mustLoad(t, root).Bookmarks()
	if _, ok := got[a]; ok || len(got[b]) != 1 {
		t.Errorf("after clearing a.go = %+v, want only b.go", got)
	}
}

// TestBookmarks_TwoInstancesKeepEachOthersFiles pins the multi-editor
// promise at file granularity: each instance writes only the files it
// changed, so neither's Close erases the other's.
func TestBookmarks_TwoInstancesKeepEachOthersFiles(t *testing.T) {
	root := repo(t)
	a, b := filepath.Join(root, "a.go"), filepath.Join(root, "b.go")
	first := mustLoad(t, root)
	second := mustLoad(t, root)
	first.SetBookmarks(map[string][]Bookmark{a: {{Line: 1}}})
	mustWrite(t, first, root, nil)
	second.SetBookmarks(map[string][]Bookmark{b: {{Line: 2}}})
	mustWrite(t, second, root, nil)
	got := mustLoad(t, root).Bookmarks()
	if len(got[a]) != 1 || len(got[b]) != 1 {
		t.Errorf("merged = %+v, want both files", got)
	}
}

// TestEncodeMarks_DropsOverlongText pins MaxBookmarkText: the bookmark
// is kept, its re-anchoring text is not.
func TestEncodeMarks_DropsOverlongText(t *testing.T) {
	enc, ok := encodeMarks([]Bookmark{{Line: 4, Text: strings.Repeat("x", MaxBookmarkText+1)}})
	if !ok || enc != `[{"line":4}]` {
		t.Errorf("encoded = %q (ok=%v)", enc, ok)
	}
	if _, ok := encodeMarks(nil); ok {
		t.Error("an empty set should have no row")
	}
}
