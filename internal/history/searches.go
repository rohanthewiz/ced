// =============================================================================
// File: internal/history/searches.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-29
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Search history: the queries typed into this repository's find surfaces,
// most recent first, one list per KIND of question.
//
// WHY PER KIND. A find query, a replacement and a symbol name answer
// different questions. The find bar, Find all and Find in project all ask
// "where does this TEXT occur", so they share one list — a string searched
// in one file is the string wanted across the project a minute later. A
// replacement is not something you search for, and a symbol query is a
// fuzzy name matched by a language server rather than literal text, so
// each keeps its own list instead of crowding the find list's rows.
//
// WHY IN THE REPOSITORY'S HISTORY. What you searched for is about THIS
// project, like the recent files and folders beside it: it moves with the
// checkout, and two projects never pollute each other's lists. It rides
// the same database, the same open-briefly discipline and the same
// add-don't-overwrite write (db.go), so two editors on one repository merge
// their searches rather than the last to close winning.
//
// Unlike the recent-file ring, the lists are OWNED here (the app only
// reads them back), so the order the write stamps comes from these lists
// directly.

package history

import (
	"sort"
	"strings"
)

// The search kinds. Each is a separate most-recent-first list.
const (
	// SearchFind is literal text searched for: the find bar, Find all in
	// file, Find in project.
	SearchFind = "find"
	// SearchReplace is replacement text: the find bar's replace row and
	// the Find-all list's "Replace in N results" box.
	SearchReplace = "replace"
	// SearchSymbol is a workspace-symbol query sent to a language server.
	SearchSymbol = "symbol"
)

// MaxSearches caps each kind's list. A history dropdown is scanned by eye,
// not scrolled through at length: past a couple of dozen entries the one
// you want is faster retyped than found, and a longer tail would only push
// the database toward its compaction ceiling sooner.
const MaxSearches = 25

// MaxSearchBytes refuses to remember a query longer than this. Queries are
// single-line, but a long single-line selection seeds the find bar too, and
// a paragraph-sized "search" is not something anybody picks from a list
// again — it would only cost a row of the dropdown and a fat database row.
const MaxSearchBytes = 512

// searchKeySep separates the kind from the text in a stored key. A kind
// never contains it, so splitting at the FIRST one is unambiguous whatever
// the text holds.
const searchKeySep = ":"

// searchKey is a search's stored primary key: kind and text in one column,
// because bytdb's primary key is a single column and a (kind, text) pair is
// what identifies a row.
func searchKey(kind, text string) string {
	return kind + searchKeySep + text
}

// splitSearchKey is searchKey's inverse. ok is false for a key with no
// separator or an empty half — only a hand-edit could produce one.
func splitSearchKey(k string) (kind, text string, ok bool) {
	i := strings.Index(k, searchKeySep)
	if i <= 0 || i == len(k)-1 {
		return "", "", false
	}
	return k[:i], k[i+1:], true
}

// Searches returns kind's list, most recent first. A copy: the caller may
// hold it across a RecordSearch without seeing it shift underneath.
func (h *History) Searches(kind string) []string {
	return append([]string(nil), h.searches[kind]...)
}

// RecordSearch moves text to the head of kind's list (adding it when new)
// and notes that the move must reach the database. Empty text, text with a
// line break, and text past MaxSearchBytes are ignored — see MaxSearchBytes.
//
// Exact-match dedupe on purpose: "Foo" and "foo" are different searches
// under match-case, and folding them would hand back a query that no
// longer finds what it found.
func (h *History) RecordSearch(kind, text string) {
	if kind == "" || text == "" || len(text) > MaxSearchBytes || strings.ContainsAny(text, "\r\n") {
		return
	}
	list := h.searches[kind]
	out := make([]string, 0, min(len(list)+1, MaxSearches))
	out = append(out, text)
	for _, s := range list {
		if s != text && len(out) < MaxSearches {
			out = append(out, s)
		}
	}
	h.searches[kind] = out
	k := searchKey(kind, text)
	h.searchTouched[k] = true
	delete(h.searchRemoved, k)
}

// ForgetSearch drops text from kind's list, and its row with the next
// write — the dropdown's "not that one again" gesture.
func (h *History) ForgetSearch(kind, text string) {
	list := h.searches[kind]
	out := list[:0:0]
	for _, s := range list {
		if s != text {
			out = append(out, s)
		}
	}
	h.searches[kind] = out
	k := searchKey(kind, text)
	h.searchRemoved[k] = true
	delete(h.searchTouched, k)
}

// searchesDirty reports whether the search lists have anything to write.
func (h *History) searchesDirty() bool {
	return len(h.searchTouched) > 0 || len(h.searchRemoved) > 0
}

// searchRow is one stored search as the load and the trim read it.
type searchRow struct {
	key  string
	last int64
}

// loadSearchRows groups stored rows into the per-kind lists, newest first,
// each capped at MaxSearches. Sorted here rather than by ORDER BY so the
// key tie-break makes two loads of one database agree.
func (h *History) loadSearchRows(rows []searchRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].last != rows[j].last {
			return rows[i].last > rows[j].last
		}
		return rows[i].key < rows[j].key
	})
	for _, r := range rows {
		kind, text, ok := splitSearchKey(r.key)
		if !ok || len(h.searches[kind]) >= MaxSearches {
			continue
		}
		h.searches[kind] = append(h.searches[kind], text)
	}
}

// overflowSearchKeys returns the keys past each kind's newest MaxSearches —
// what the write's trim deletes. rows need not be sorted.
func overflowSearchKeys(rows []searchRow) []string {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].last != rows[j].last {
			return rows[i].last > rows[j].last
		}
		return rows[i].key < rows[j].key
	})
	seen := map[string]int{}
	var drop []string
	for _, r := range rows {
		kind, _, ok := splitSearchKey(r.key)
		if !ok {
			// Unreadable key: nothing can ever load it, so it only costs space.
			drop = append(drop, r.key)
			continue
		}
		seen[kind]++
		if seen[kind] > MaxSearches {
			drop = append(drop, r.key)
		}
	}
	return drop
}

// searchKinds returns the kinds with a live list, sorted — a deterministic
// statement order for the write.
func (h *History) searchKinds() []string {
	kinds := make([]string, 0, len(h.searches))
	for k := range h.searches {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}
