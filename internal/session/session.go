// =============================================================================
// File: internal/session/session.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Package session persists the editor's per-folder workspace state: which
// folders have been opened (most recent first) and, for each, the tabs
// that were open and where their cursors were.
//
// WHY A SEPARATE FILE from config.json. config.json holds flat
// preferences a user may hand-edit and the ≡ toggles write back one key
// at a time; state.json is machine-written churn — every folder switch
// and every exit rewrites it. Keeping them apart means session churn
// can never scramble a hand-edited preference file, and a corrupt state
// file costs the user their tab list, not their settings. Same line
// mcp.json and actions.json are on, drawn for the opposite reason: those
// are hand-authored inventories ced only reads, this is machine state
// nobody is expected to edit.
//
// WHY ORDER IS THE RECENCY. The entry list is stored most-recent-first
// rather than carrying timestamps that would have to be sorted on load.
// Nothing in the UI shows "opened 2 hours ago", so a timestamp would be
// a field with no reader and one more thing to get wrong across
// timezones and clock skew. Moving an entry to the front IS the record.
//
// The store is a plain value with no IO of its own beyond Load/Save, so
// the app layer can hold one, mutate it, and decide when it hits disk.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// MaxEntries caps how many folders the store remembers. The list is
	// a recency queue, so the cap is a trim from the tail — a user who
	// works in three projects never notices it exists, and one who
	// scripts ced over a thousand directories doesn't grow an unbounded
	// file in their config directory.
	MaxEntries = 20

	// MaxTabs caps how many tabs one folder's session records. Restoring
	// is not free — each tab is a file read, a syntax pass, an LSP
	// didOpen and a diff request — so a session with sixty tabs would
	// turn startup into a visible stall. Thirty is past any working set
	// somebody navigates by clicking tabs.
	MaxTabs = 30
)

// TabState is one restored tab: the file and where the user was in it.
// Anchor is deliberately absent — a selection is a transient gesture,
// not part of "where I was", and restoring one would put the editor in
// a state where the next keystroke replaces text the user doesn't
// remember highlighting.
type TabState struct {
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	Col     int    `json:"col,omitempty"`
	ScrollY int    `json:"scrolly,omitempty"`
	ScrollX int    `json:"scrollx,omitempty"`
}

// Entry is one folder's remembered workspace. Root is always an
// absolute, cleaned path — it's the map key everything else looks up by,
// and two spellings of one directory would silently split a session in
// half.
type Entry struct {
	Root   string     `json:"root"`
	Tabs   []TabState `json:"tabs,omitempty"`
	Active int        `json:"active,omitempty"`
}

// Store is the whole state file: folders in most-recently-opened order.
// A JSON array rather than an object keyed by path, because the order is
// the data — a map would round-trip in whatever order Go felt like.
type Store struct {
	Folders []Entry `json:"folders"`
}

// Load reads the state file at path. Contract mirrors userconfig.Load:
//
//   - path == ""          → empty store, no error ("no config location")
//   - file doesn't exist  → empty store, no error (first run)
//   - unreadable / bad JSON → empty store AND the error, so the caller
//     can flash it. A malformed state file must never stop the editor
//     starting — it holds convenience, not the user's work.
//
// Entries with a blank root are dropped on the way in: they can only
// come from a hand-edit or a truncated write, and every lookup below
// keys on the root.
func Load(path string) (*Store, error) {
	s := &Store{}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return s, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return s, nil
	}
	var raw Store
	if err := json.Unmarshal(data, &raw); err != nil {
		return s, fmt.Errorf("parse %s: %w", path, err)
	}
	for _, e := range raw.Folders {
		e.Root = normalize(e.Root)
		if e.Root == "" {
			continue
		}
		e.Tabs = trimTabs(e.Tabs)
		e.Active = clampActive(e.Active, len(e.Tabs))
		s.Folders = append(s.Folders, e)
		if len(s.Folders) >= MaxEntries {
			break
		}
	}
	return s, nil
}

// Save writes the store to path atomically (temp + rename), creating the
// config directory if it doesn't exist. Atomic because this file is
// rewritten on every exit: a crash mid-write would otherwise leave a
// half-JSON file that Load then reports as a parse error on the next
// three startups.
func (s *Store) Save(path string) error {
	if path == "" {
		return errors.New("no config directory resolved — cannot persist session state")
	}
	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Find returns the remembered entry for root, if any. The bool is what
// distinguishes "never opened" from "opened with no tabs" — the caller
// wants the difference: one is a fresh folder, the other is a folder the
// user deliberately left empty.
func (s *Store) Find(root string) (Entry, bool) {
	root = normalize(root)
	for _, e := range s.Folders {
		if e.Root == root {
			return e, true
		}
	}
	return Entry{}, false
}

// Touch marks root as the most recently opened folder, preserving any
// tabs already recorded for it. Called at startup so a folder is in the
// recent list — and reachable by `ced --last` — from the moment it
// opens, rather than only after a clean exit. A crash then costs the
// tab list, not the fact that you were there at all.
func (s *Store) Touch(root string) {
	root = normalize(root)
	if root == "" {
		return
	}
	e, ok := s.Find(root)
	if !ok {
		e = Entry{Root: root}
	}
	s.put(e)
}

// Record stores e as the most recent entry, replacing whatever was there
// for the same root. Tabs are capped here rather than at the call site so
// no writer can grow the file past MaxTabs by accident.
func (s *Store) Record(e Entry) {
	e.Root = normalize(e.Root)
	if e.Root == "" {
		return
	}
	e.Tabs = trimTabs(e.Tabs)
	e.Active = clampActive(e.Active, len(e.Tabs))
	s.put(e)
}

// put upserts e at the head of the list and trims the tail to MaxEntries.
// The single mutation path for both Touch and Record, so "most recent
// first" can't drift between them.
func (s *Store) put(e Entry) {
	out := make([]Entry, 0, len(s.Folders)+1)
	out = append(out, e)
	for _, old := range s.Folders {
		if old.Root == e.Root {
			continue
		}
		out = append(out, old)
		if len(out) >= MaxEntries {
			break
		}
	}
	s.Folders = out
}

// Remove forgets a folder entirely. The way a user drops a project they
// no longer want offered — and the way the app prunes a root that has
// since been deleted from disk.
func (s *Store) Remove(root string) {
	root = normalize(root)
	out := s.Folders[:0]
	for _, e := range s.Folders {
		if e.Root == root {
			continue
		}
		out = append(out, e)
	}
	s.Folders = out
}

// Recent returns the remembered roots, most recent first. A copy, not a
// view onto the store's slice — callers build pickers from this and must
// not be able to reorder the store by sorting what they got back.
func (s *Store) Recent() []string {
	out := make([]string, 0, len(s.Folders))
	for _, e := range s.Folders {
		out = append(out, e.Root)
	}
	return out
}

// Last returns the most recently opened folder, or "" when nothing has
// been recorded. This is what `ced --last` resolves.
func (s *Store) Last() string {
	if len(s.Folders) == 0 {
		return ""
	}
	return s.Folders[0].Root
}

// Normalize makes a root path comparable: absolute, cleaned, and with
// symlinks resolved. Exported because the app layer has to ask the same
// question ("is this entry the folder I'm in?") and two normalisers
// would answer it differently.
//
// The symlink resolution is not cosmetic. `ced /tmp/proj` roots at the
// path as typed, while `cd /tmp/proj && ced` roots at what the kernel
// reports for the working directory — which on macOS is the RESOLVED
// /private/tmp/proj. Without this, the same directory entered two ways
// keeps two half-sessions that overwrite each other in turn. Resolution
// is best-effort: a path that doesn't exist (a folder recorded before it
// was deleted) keeps its absolute form rather than being dropped, since
// a wrong-but-stable key still round-trips and Remove can still find it.
func Normalize(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

// normalize is the unexported alias the store's own methods use, kept so
// the call sites below read as internal detail rather than as a public
// API call into their own package.
func normalize(p string) string { return Normalize(p) }

// trimTabs drops nameless tabs (an untitled buffer has nothing to
// reopen) and caps the list at MaxTabs.
func trimTabs(tabs []TabState) []TabState {
	out := make([]TabState, 0, len(tabs))
	for _, t := range tabs {
		if t.Path == "" {
			continue
		}
		out = append(out, t)
		if len(out) >= MaxTabs {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// clampActive keeps the active index inside the tab list. A stored index
// can outlive its tab when the cap trims the list or a hand-edit removes
// an entry, and an out-of-range active tab is a panic waiting for the
// first render.
func clampActive(active, n int) int {
	if n == 0 || active < 0 {
		return 0
	}
	if active >= n {
		return n - 1
	}
	return active
}
