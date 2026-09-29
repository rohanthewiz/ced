// =============================================================================
// File: internal/history/searches_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-29
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the search lists: in-memory ordering and caps, the key
// encoding, and the persistence claims they share with the recent-file
// ring — a round trip keeps the order, two instances ADD to each other,
// a forgotten entry's row goes, and each kind is trimmed on its own.

package history

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// TestRecordSearch_MovesToFrontAndDedupes pins most-recent-first order
// with an exact-match dedupe, per kind.
func TestRecordSearch_MovesToFrontAndDedupes(t *testing.T) {
	h := New(t.TempDir())
	h.RecordSearch(SearchFind, "alpha")
	h.RecordSearch(SearchFind, "beta")
	h.RecordSearch(SearchFind, "alpha")
	h.RecordSearch(SearchFind, "Alpha") // case differs: a different search
	h.RecordSearch(SearchReplace, "gamma")
	if got, want := h.Searches(SearchFind), []string{"Alpha", "alpha", "beta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("find list = %q, want %q", got, want)
	}
	if got := h.Searches(SearchReplace); !reflect.DeepEqual(got, []string{"gamma"}) {
		t.Fatalf("replace list = %q, want [gamma]", got)
	}
	if !h.Dirty() {
		t.Fatal("recording a search left the history clean")
	}
}

// TestRecordSearch_RefusesUnrememberable pins the refusals: empty text,
// line breaks, over-long text, and a missing kind record nothing.
func TestRecordSearch_RefusesUnrememberable(t *testing.T) {
	h := New(t.TempDir())
	h.RecordSearch(SearchFind, "")
	h.RecordSearch(SearchFind, "two\nlines")
	h.RecordSearch(SearchFind, strings.Repeat("x", MaxSearchBytes+1))
	h.RecordSearch("", "no kind")
	if got := h.Searches(SearchFind); len(got) != 0 {
		t.Fatalf("refused searches were recorded: %q", got)
	}
	if h.Dirty() {
		t.Fatal("refused searches left the history dirty")
	}
}

// TestRecordSearch_CapsTheList pins MaxSearches: the oldest falls off.
func TestRecordSearch_CapsTheList(t *testing.T) {
	h := New(t.TempDir())
	for i := 0; i < MaxSearches+5; i++ {
		h.RecordSearch(SearchFind, fmt.Sprintf("q%d", i))
	}
	got := h.Searches(SearchFind)
	if len(got) != MaxSearches {
		t.Fatalf("list length = %d, want %d", len(got), MaxSearches)
	}
	if got[0] != fmt.Sprintf("q%d", MaxSearches+4) || got[len(got)-1] != "q5" {
		t.Fatalf("list ends = %q … %q", got[0], got[len(got)-1])
	}
}

// TestSearchKey_RoundTrips pins that a text containing the separator
// still splits back at the kind, and that malformed keys refuse.
func TestSearchKey_RoundTrips(t *testing.T) {
	kind, text, ok := splitSearchKey(searchKey(SearchFind, "a:b:c"))
	if !ok || kind != SearchFind || text != "a:b:c" {
		t.Fatalf("split = %q %q %v", kind, text, ok)
	}
	for _, bad := range []string{"nosep", ":text", "find:"} {
		if _, _, ok := splitSearchKey(bad); ok {
			t.Errorf("splitSearchKey(%q) accepted a malformed key", bad)
		}
	}
}

// TestSearches_RoundTripThroughTheDatabase pins that a written list loads
// back in its order, kind by kind.
func TestSearches_RoundTripThroughTheDatabase(t *testing.T) {
	root := repo(t)
	h := mustLoad(t, root)
	h.RecordSearch(SearchFind, "one")
	h.RecordSearch(SearchFind, "two")
	h.RecordSearch(SearchSymbol, "Handler")
	mustWrite(t, h, root, nil)
	if h.Dirty() {
		t.Fatal("a committed write left searches pending")
	}
	back := mustLoad(t, root)
	if got, want := back.Searches(SearchFind), []string{"two", "one"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("find list = %q, want %q", got, want)
	}
	if got := back.Searches(SearchSymbol); !reflect.DeepEqual(got, []string{"Handler"}) {
		t.Fatalf("symbol list = %q", got)
	}
}

// TestSearches_TwoInstancesAdd pins the add-don't-overwrite write: two
// editors that loaded the same database both keep their searches, and
// the one written last is newest.
func TestSearches_TwoInstancesAdd(t *testing.T) {
	root := repo(t)
	seed := mustLoad(t, root)
	seed.RecordSearch(SearchFind, "old")
	mustWrite(t, seed, root, nil)

	a, b := mustLoad(t, root), mustLoad(t, root)
	a.RecordSearch(SearchFind, "fromA")
	b.RecordSearch(SearchFind, "fromB")
	mustWrite(t, a, root, nil)
	mustWrite(t, b, root, nil)

	got := mustLoad(t, root).Searches(SearchFind)
	if want := []string{"fromB", "fromA", "old"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("merged list = %q, want %q", got, want)
	}
}

// TestForgetSearch_DeletesTheRow pins that a forgotten search is gone
// from memory at once and from the database after the write.
func TestForgetSearch_DeletesTheRow(t *testing.T) {
	root := repo(t)
	h := mustLoad(t, root)
	h.RecordSearch(SearchFind, "keep")
	h.RecordSearch(SearchFind, "drop")
	mustWrite(t, h, root, nil)

	h.ForgetSearch(SearchFind, "drop")
	if got := h.Searches(SearchFind); !reflect.DeepEqual(got, []string{"keep"}) {
		t.Fatalf("after forget = %q", got)
	}
	mustWrite(t, h, root, nil)
	if got := mustLoad(t, root).Searches(SearchFind); !reflect.DeepEqual(got, []string{"keep"}) {
		t.Fatalf("reloaded after forget = %q", got)
	}
}

// TestSearches_TrimmedPerKind pins that the merged table keeps each
// kind's newest MaxSearches — a full find list must not evict the
// replace list's rows.
func TestSearches_TrimmedPerKind(t *testing.T) {
	root := repo(t)
	a, b := mustLoad(t, root), mustLoad(t, root)
	for i := 0; i < MaxSearches; i++ {
		a.RecordSearch(SearchFind, fmt.Sprintf("a%d", i))
		b.RecordSearch(SearchFind, fmt.Sprintf("b%d", i))
	}
	b.RecordSearch(SearchReplace, "r")
	mustWrite(t, a, root, nil)
	mustWrite(t, b, root, nil)

	back := mustLoad(t, root)
	find := back.Searches(SearchFind)
	if len(find) != MaxSearches || find[0] != fmt.Sprintf("b%d", MaxSearches-1) {
		t.Fatalf("find list = %d rows, head %q", len(find), find[0])
	}
	rows, err := readSearchRows(mustOpen(t, root))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != MaxSearches+1 {
		t.Fatalf("stored rows = %d, want %d (trim per kind)", len(rows), MaxSearches+1)
	}
	if got := back.Searches(SearchReplace); !reflect.DeepEqual(got, []string{"r"}) {
		t.Fatalf("replace list = %q", got)
	}
}

// mustOpen opens the repository's database for a direct look at its rows,
// closed with the test.
func mustOpen(t *testing.T, root string) *sql.DB {
	t.Helper()
	db, err := open(DBPath(root))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
