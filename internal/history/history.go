// =============================================================================
// File: internal/history/history.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Package history is one repository's navigation history — the folders
// used in it (index.go), the files made active in it, and the searches
// typed in it (searches.go) — kept in the repository itself, at
// <repo>/.ced/history.bytdb (db.go).
//
// WHY IN THE REPO, NOT ~/.config. History answers "where have I been in
// THIS project", so it lives with the project: it moves when the checkout
// moves, disappears when the checkout is deleted, and two clones of one
// repo keep separate histories because they are separate places to work.
// Paths inside the repo are stored RELATIVE to it for the same reason — a
// renamed or moved checkout keeps its history. The file is gitignored (the
// write adds the entry to .ced/.gitignore): it is machine state, while
// .ced/format.json beside it is project config that IS committed.
//
// WHAT THE APP OWNS AND WHAT THIS OWNS. The recent-file ring is still the
// App's live slice (recentfiles.go decides where it is touched, and in
// what order); this package only remembers which entries were touched or
// pruned since the load, so a write can merge them with what another
// instance wrote. The folder index is owned outright here.

package history

import (
	"path/filepath"
	"strings"

	"github.com/rohanthewiz/ced/internal/session"
)

// MaxRecentFiles is how many recent files a repository keeps — the ring's
// own cap (session.MaxRecentFiles), so the stored list and the live one
// can never disagree about how long the list is.
const MaxRecentFiles = session.MaxRecentFiles

// History is one repository's history.
type History struct {
	// root is the repository as the editor spells it (the tab paths are
	// relative to this spelling); folderRoot is it normalized, which is
	// how the folder index keys everything.
	root       string
	folderRoot string

	// Folders is the folder usage index.
	Folders *Index

	// files is the recent-file ring as loaded, most recent first.
	files []string
	// fileSeq is the stored recent-file counter as of the last load or
	// write — the base the next write re-issues from (db.go).
	fileSeq uint64
	// touched and removed are the ring's changes since the last write.
	touched map[string]bool
	removed map[string]bool

	// searches holds the search lists by kind, most recent first
	// (searches.go); searchSeq is their stored counter as of the last
	// load or write, and searchTouched / searchRemoved their changes since,
	// keyed by searchKey.
	searches      map[string][]string
	searchSeq     uint64
	searchTouched map[string]bool
	searchRemoved map[string]bool

	// bmStored is the bookmark table as of the last load or write, stored
	// path → that file's encoded set; bmWant is the set the app last
	// handed over, and bmSet whether it ever did (bookmarks.go).
	bmStored map[string]string
	bmWant   map[string]string
	bmSet    bool
}

// New returns an empty history for the repository at root.
func New(root string) *History {
	return &History{
		root:       filepath.Clean(root),
		folderRoot: session.Normalize(root),
		Folders:    NewIndex(),
		touched:    map[string]bool{},
		removed:    map[string]bool{},

		searches:      map[string][]string{},
		searchTouched: map[string]bool{},
		searchRemoved: map[string]bool{},

		bmStored: map[string]string{},
	}
}

// Files returns the recent-file ring as it was loaded, most recent first.
// A copy: the caller keeps it as its live ring and rewrites it in place.
func (h *History) Files() []string {
	return append([]string(nil), h.files...)
}

// TouchFile notes that path became the active file. The caller moves it
// to the head of its ring; this remembers that the move must reach the
// database.
func (h *History) TouchFile(path string) {
	if path == "" {
		return
	}
	h.touched[path] = true
	delete(h.removed, path)
}

// RemoveFile notes that path was pruned from the ring (deleted or moved on
// disk), so its row goes too.
func (h *History) RemoveFile(path string) {
	if path == "" {
		return
	}
	h.removed[path] = true
	delete(h.touched, path)
}

// Dirty reports whether a write has anything to do.
func (h *History) Dirty() bool {
	return h.Folders.Dirty() || len(h.touched) > 0 || len(h.removed) > 0 || h.searchesDirty() ||
		h.bookmarksDirty()
}

// Inside reports whether path (in either spelling) lies strictly inside
// the repository — the only folders the index records, since a folder
// outside it belongs to some other repository's history.
func (h *History) Inside(path string) bool {
	rel, ok := relInside(h.folderRoot, session.Normalize(path))
	return ok && rel != "."
}

// relInside is filepath.Rel restricted to paths at or under root.
func relInside(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// encodeFile turns a ring entry into its stored form: relative to the
// repository when inside it, absolute otherwise (a go-to-definition jump
// into the module cache is a legitimate recent file, and has no relative
// spelling worth keeping).
func (h *History) encodeFile(path string) string {
	if rel, ok := relInside(h.root, path); ok && rel != "." {
		return rel
	}
	return path
}

// decodeFile is encodeFile's inverse.
func (h *History) decodeFile(stored string) string {
	if filepath.IsAbs(stored) {
		return filepath.Clean(stored)
	}
	return filepath.Join(h.root, stored)
}

// encodeFolder is a folder's stored form: always relative, since only
// folders inside the repository are recorded.
func (h *History) encodeFolder(path string) (string, bool) {
	rel, ok := relInside(h.folderRoot, path)
	return rel, ok && rel != "."
}

// decodeFolder is encodeFolder's inverse, refusing a row that is absolute
// or climbs out — a hand-edit, or a database copied from elsewhere.
func (h *History) decodeFolder(stored string) (string, bool) {
	if stored == "" || filepath.IsAbs(stored) {
		return "", false
	}
	p := filepath.Join(h.folderRoot, stored)
	if _, ok := h.encodeFolder(p); !ok {
		return "", false
	}
	return p, true
}
