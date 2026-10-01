// =============================================================================
// File: internal/history/bookmarks.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Bookmarks: the repository's bookmarked lines (app/bookmarks.go), stored
// beside the recent files and searches because they are the same kind of
// thing — where you were working in THIS checkout — and should move with
// it, open briefly, and never block a sibling editor.
//
// ONE ROW PER FILE. A bookmark has no stable identity: its line moves with
// every edit above it, so a (path, line) key would turn each keystroke
// into a delete plus an insert. The unit that IS stable is the file, so
// the table holds one row per file whose value is that file's whole set,
// as JSON:
//
//	bookmarks(path PK, marks)   marks = [{"line":41,"text":"func main() {"}, …]
//
// STATE, NOT DELTAS — PER FILE. The app does not report each toggle; it
// hands over the complete set (SetBookmarks) before every write, and the
// write compares it with what was stored at the last load or write:
//
//	want ≠ stored  → upsert that file's row
//	stored, no want → delete that file's row
//	want == stored → untouched
//
// That keeps db.go's promise that two editors on one repository ADD to
// each other at file granularity: a file this instance never changed is
// never written, so a sibling's bookmarks in it survive this instance's
// Close. Two instances changing the SAME file's bookmarks is last writer
// wins for that one file — the honest answer, since their buffers (and so
// their line numbers) may disagree anyway.
//
// Paths are stored like recent files: relative inside the repository,
// absolute outside it (a bookmark in a file a go-to-definition opened in
// the module cache).

package history

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
)

// MaxBookmarks caps one repository's bookmarks. A backstop, not a
// budget: the app refuses (with a flash) the bookmark that would pass it.
// With MaxBookmarkText it bounds the table to ~100KB, which keeps the
// whole live set under db.go's compactAbove ceiling.
const MaxBookmarks = 500

// MaxBookmarkText is the longest line text kept with a bookmark. The
// text is only a re-anchoring key (editor.Tab.SetBookmarks): a line
// longer than this is stored without it and simply comes back on its
// recorded line, rather than putting a minified file's 100KB line in
// the database.
const MaxBookmarkText = 160

// Bookmark is one stored bookmark. Line is 0-based; Text is the line's
// content when it was written.
type Bookmark struct {
	Line int    `json:"line"`
	Text string `json:"text,omitempty"`
}

// Bookmarks returns the stored bookmarks by absolute path, as of the
// last load or write. A fresh map: the caller adopts it as its own.
// A row whose JSON does not parse (only a hand-edit could produce one)
// is skipped.
func (h *History) Bookmarks() map[string][]Bookmark {
	out := map[string][]Bookmark{}
	for k, v := range h.bmStored {
		var bms []Bookmark
		if err := json.Unmarshal([]byte(v), &bms); err != nil || len(bms) == 0 {
			continue
		}
		out[h.decodeFile(k)] = bms
	}
	return out
}

// SetBookmarks records the complete bookmark set the next write should
// leave in the database, by absolute path. Lists are normalized here —
// sorted, de-duplicated, negative lines dropped, over-long text cleared —
// so an unchanged set encodes byte-for-byte as it was stored and is not
// rewritten.
func (h *History) SetBookmarks(all map[string][]Bookmark) {
	want := make(map[string]string, len(all))
	for path, bms := range all {
		if path == "" {
			continue
		}
		if enc, ok := encodeMarks(bms); ok {
			want[h.encodeFile(path)] = enc
		}
	}
	h.bmWant = want
	h.bmSet = true
}

// bookmarksDirty reports whether the wanted set differs from the stored
// one. Nothing is dirty until SetBookmarks has been called: a history the
// app never handed bookmarks to must not delete the rows it loaded.
func (h *History) bookmarksDirty() bool {
	return h.bmSet && !maps.Equal(h.bmWant, h.bmStored)
}

// encodeMarks is a file's set in its stored form; ok is false for an
// empty set (no row).
func encodeMarks(bms []Bookmark) (string, bool) {
	clean := make([]Bookmark, 0, len(bms))
	for _, b := range bms {
		if b.Line < 0 {
			continue
		}
		if len(b.Text) > MaxBookmarkText {
			b.Text = ""
		}
		clean = append(clean, b)
	}
	sort.SliceStable(clean, func(i, j int) bool { return clean[i].Line < clean[j].Line })
	out := clean[:0]
	for i, b := range clean {
		if i > 0 && b.Line == out[len(out)-1].Line {
			continue
		}
		out = append(out, b)
	}
	if len(out) == 0 {
		return "", false
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// loadBookmarks reads the bookmark rows into bmStored.
func (h *History) loadBookmarks(db *sql.DB) error {
	rows, err := db.Query(`SELECT path, marks FROM bookmarks`)
	if err != nil {
		return fmt.Errorf("read bookmarks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var path, marks string
		if err := rows.Scan(&path, &marks); err != nil {
			return fmt.Errorf("read bookmark row: %w", err)
		}
		if path != "" {
			h.bmStored[path] = marks
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read bookmarks: %w", err)
	}
	return nil
}

// writeBookmarks applies the per-file comparison in the header. Keys are
// visited sorted so two identical writes issue identical statements.
func (h *History) writeBookmarks(tx *sql.Tx) error {
	if !h.bookmarksDirty() {
		return nil
	}
	keys := make([]string, 0, len(h.bmWant)+len(h.bmStored))
	for k := range h.bmWant {
		keys = append(keys, k)
	}
	for k := range h.bmStored {
		if _, ok := h.bmWant[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		want, wok := h.bmWant[k]
		stored, sok := h.bmStored[k]
		switch {
		case wok && (!sok || want != stored):
			if _, err := tx.Exec(`INSERT INTO bookmarks (path, marks) VALUES ($1, $2) ON CONFLICT (path) DO UPDATE SET marks = excluded.marks`,
				k, want); err != nil {
				return fmt.Errorf("write bookmarks %s: %w", k, err)
			}
		case !wok && sok:
			if _, err := tx.Exec(`DELETE FROM bookmarks WHERE path = $1`, k); err != nil {
				return fmt.Errorf("delete bookmarks %s: %w", k, err)
			}
		}
	}
	return nil
}

// adoptBookmarkWrite makes the written set the stored one, once the
// commit has landed.
func (h *History) adoptBookmarkWrite() {
	if h.bmSet {
		h.bmStored = maps.Clone(h.bmWant)
	}
}
