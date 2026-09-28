// =============================================================================
// File: internal/history/index_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the folder usage index. The load-bearing claim is that the
// pruned best-first search returns EXACTLY what sorting every folder would
// — TestIndex_SearchMatchesBruteForce holds it to that on random data —
// and the rest pin the edges the picker leans on: strict "below", the veto
// that doesn't shorten the list, eviction that spares the recent, and the
// change tracking a database write reads.

package history

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// p builds an absolute path from segments. These paths don't exist on
// disk, so session.Normalize keeps them as spelled.
func p(segs ...string) string {
	return string(filepath.Separator) + filepath.Join(segs...)
}

// TestIndex_RecentAndFrequent pins the two rankings on a small history,
// including the tie-break: equal counts go to the more recent folder.
func TestIndex_RecentAndFrequent(t *testing.T) {
	u := NewIndex()
	a, b, c := p("zz", "a"), p("zz", "b"), p("zz", "c")
	for _, x := range []string{a, a, a, b, c, c, c, b} {
		u.Record(x)
	}
	if got := u.Recent(p("zz"), 3, nil); strings.Join(got, ",") != b+","+c+","+a {
		t.Fatalf("Recent = %v, want b, c, a", got)
	}
	// a and c both have 3; c was used later, so c leads.
	if got := u.Frequent(p("zz"), 3, nil); strings.Join(got, ",") != c+","+a+","+b {
		t.Fatalf("Frequent = %v, want c, a, b", got)
	}
}

// TestIndex_BelowIsStrict pins that "subfolders of X" never lists X
// itself — a drill-in's own folder has its own Reveal row.
func TestIndex_BelowIsStrict(t *testing.T) {
	u := NewIndex()
	proj := p("zz", "proj")
	u.Record(proj)
	u.Record(p("zz", "proj", "internal", "app"))
	u.Record(p("zz", "other"))

	got := u.Recent(proj, 10, nil)
	if len(got) != 1 || got[0] != p("zz", "proj", "internal", "app") {
		t.Fatalf("below proj = %v, want only internal/app", got)
	}
	if !u.HasUsesBelow(proj) {
		t.Fatal("HasUsesBelow(proj) = false with a used subfolder")
	}
	if u.HasUsesBelow(p("zz", "other")) {
		t.Fatal("HasUsesBelow(other) = true with nothing under it")
	}
}

// TestIndex_VetoKeepsSearching pins that a rejected candidate costs
// itself, not a slot — the picker vetoes folders already listed and
// folders gone from disk, and must still fill its ten rows.
func TestIndex_VetoKeepsSearching(t *testing.T) {
	u := NewIndex()
	for i := 0; i < 6; i++ {
		u.Record(p("zz", fmt.Sprintf("d%d", i)))
	}
	skip := map[string]bool{p("zz", "d5"): true, p("zz", "d4"): true}
	got := u.Recent(p("zz"), 3, func(s string) bool { return !skip[s] })
	want := []string{p("zz", "d3"), p("zz", "d2"), p("zz", "d1")}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Recent with veto = %v, want %v", got, want)
	}
}

// TestIndex_RemoveDropsSubtreeAndRepairsBounds pins that pruning a folder
// takes its subfolders with it AND lowers the ancestors' bounds — a stale
// bound would not return wrong answers, but it would make the search
// expand a subtree that holds nothing.
func TestIndex_RemoveDropsSubtreeAndRepairsBounds(t *testing.T) {
	u := NewIndex()
	u.Record(p("zz", "keep"))
	for i := 0; i < 5; i++ {
		u.Record(p("zz", "gone", "deep"))
	}
	u.Remove(p("zz", "gone"))

	if u.Len() != 1 {
		t.Fatalf("Len = %d, want 1 after removing a subtree", u.Len())
	}
	if got := u.Frequent(p("zz"), 5, nil); len(got) != 1 || got[0] != p("zz", "keep") {
		t.Fatalf("Frequent = %v, want only keep", got)
	}
	if u.root.sub.hits != 1 {
		t.Fatalf("root bound hits = %d, want 1 — the removed subtree's max leaked", u.root.sub.hits)
	}
}

// TestIndex_EvictionSparesTheRecent pins the size cap and its one policy
// promise: a folder used a moment ago survives eviction even with a single
// hit, while old one-hit folders go first — and each eviction is queued
// as an exact (not subtree) deletion for the database.
func TestIndex_EvictionSparesTheRecent(t *testing.T) {
	u := NewIndex()
	busy := p("zz", "busy")
	for i := 0; i < 50; i++ {
		u.Record(busy)
	}
	for i := 0; i < MaxFolders; i++ {
		u.Record(p("zz", "old", fmt.Sprintf("d%03d", i)))
	}
	fresh := p("zz", "fresh")
	u.Record(fresh)

	if u.Len() > MaxFolders {
		t.Fatalf("Len = %d, over the cap %d", u.Len(), MaxFolders)
	}
	if n := u.find(fresh); n == nil || !n.used() {
		t.Fatal("the most recent folder was evicted")
	}
	if n := u.find(busy); n == nil || !n.used() {
		t.Fatal("the most frequent folder was evicted")
	}
	if u.find(p("zz", "old", "d000")) != nil {
		t.Fatal("the oldest one-hit folder survived eviction")
	}
	if u.Len() != countUsed(u.root) {
		t.Fatalf("count drifted: Len %d vs walk %d", u.Len(), countUsed(u.root))
	}
	if len(u.deletes) == 0 || u.deletes[0].subtree {
		t.Fatalf("deletes = %+v, want exact deletions queued", u.deletes)
	}
}

// TestIndex_TracksWhatAWriteMustDo pins the bookkeeping db.go reads: a use
// leaves its node dirty with a delta, and a Remove replaces any pending
// upsert under the folder with one subtree deletion.
func TestIndex_TracksWhatAWriteMustDo(t *testing.T) {
	u := NewIndex()
	u.Record(p("zz", "proj", "a"))
	u.Record(p("zz", "proj", "a"))
	if !u.Dirty() {
		t.Fatal("a recorded use left nothing pending")
	}
	n := u.find(p("zz", "proj", "a"))
	if n.delta != 2 || !u.dirty[n] {
		t.Fatalf("delta = %d dirty = %v, want 2 pending hits", n.delta, u.dirty[n])
	}
	u.Remove(p("zz", "proj"))
	if len(u.dirty) != 0 {
		t.Fatal("a removed folder is still queued for an upsert — the write would resurrect it")
	}
	if len(u.deletes) != 1 || !u.deletes[0].subtree || u.deletes[0].path != p("zz", "proj") {
		t.Fatalf("deletes = %+v, want one subtree deletion of proj", u.deletes)
	}
}

// TestIndex_SearchMatchesBruteForce is the algorithm's proof by
// comparison: on a random history the pruned best-first search must
// return exactly the prefix a full sort of every candidate would, for both
// orderings and several starting points.
func TestIndex_SearchMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	u := NewIndex()
	names := []string{"a", "b", "c", "d", "e"}
	for i := 0; i < 3000; i++ {
		depth := 1 + rng.Intn(4)
		segs := []string{"zz"}
		for d := 0; d < depth; d++ {
			segs = append(segs, names[rng.Intn(len(names))])
		}
		u.Record(p(segs...))
	}

	type cand struct {
		path string
		st   useStat
	}
	brute := func(under string, byFreq bool, k int) []string {
		var all []cand
		var walk func(*useNode)
		walk = func(n *useNode) {
			if n.used() {
				path := nodePath(n)
				if strings.HasPrefix(path, under+string(filepath.Separator)) {
					all = append(all, cand{path, n.self})
				}
			}
			for _, c := range n.children {
				walk(c)
			}
		}
		walk(u.root)
		sort.Slice(all, func(i, j int) bool { return all[i].st.beats(all[j].st, byFreq) })
		out := []string{}
		for i := 0; i < len(all) && i < k; i++ {
			out = append(out, all[i].path)
		}
		return out
	}
	for _, under := range []string{p("zz"), p("zz", "a"), p("zz", "c", "d")} {
		for _, byFreq := range []bool{false, true} {
			want := brute(under, byFreq, 10)
			got := u.top(under, byFreq, 10, nil)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("under %q freq %v:\n got %v\nwant %v", under, byFreq, got, want)
			}
		}
	}
}
