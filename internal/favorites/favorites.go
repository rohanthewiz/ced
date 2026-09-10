// =============================================================================
// File: internal/favorites/favorites.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Package favorites implements named, project-RELATIVE locations:
// `plans` standing for `ai_docs/plans`, `clsess` for
// `ai_docs/claude_sessions`. `ced fav plans` opens the project and
// reveals that folder in the file tree.
//
// The whole design turns on the word RELATIVE. A favorite is not a
// bookmark to one absolute directory — that is what the recent-folders
// list already is (internal/session). It is a name for a place a project
// keeps things, and the convention repeats across every project that
// follows it, which is exactly what makes the name worth typing. So the
// map is stored ONCE, user-scoped, and applied against whatever project
// you are standing in.
//
// House rules:
//
//   - TWO SCOPES, AND THE PROJECT ONE ONLY SHADOWS. `Favorites` is the
//     default map; `Projects[<root>]` overrides individual NAMES for one
//     project that spells the location differently (`docs/plans` rather
//     than `ai_docs/plans`). A project block is a patch over the
//     defaults, never a replacement — a project that renames one entry
//     must not lose the other five.
//
//   - RESOLUTION WALKS UP, AND THE DIRECTORY THAT OWNS THE FAVORITE
//     BECOMES THE ROOT. `ced fav plans` is typed from wherever the user
//     happens to be standing, which for a project of any size is not the
//     project root. So Resolve tries the start directory, then its
//     parent, and so on: the first ancestor where the name resolves to a
//     real path wins, and that ancestor is the project. Looking the name
//     up per CANDIDATE (rather than once against the start directory) is
//     what makes a per-project override reachable from a subdirectory
//     too — the override is keyed by the root that owns it, and that root
//     is one of the candidates being walked.
//
//   - A FAVORITE IS CONFINED TO ITS PROJECT. `Clean` rejects an absolute
//     path and anything that climbs out with "..", because the one thing
//     this feature must not become is a way for a config file to point
//     the editor at /etc. The check is lexical AND repeated after the
//     join resolves symlinks (see Resolve) — a lexical check alone is
//     escapable through a link that lives inside the root.
//
//   - PER-ENTRY DEGRADATION, as everywhere else in ced: an unreadable or
//     malformed file costs the favorites, never the editor, and one
//     invalid entry is reported by name rather than taking its
//     neighbours with it.
//
// The file is ~/.config/ced/favorites.json and is hand-editable, which
// is why it is its own file rather than a key in config.json — the
// mcp.json argument (a nested inventory somebody writes by hand has no
// business in the flat file the ≡ toggles rewrite). `ced fav add` writes
// it back for the times you would rather not open an editor to edit the
// editor.
package favorites

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rohanthewiz/ced/internal/session"
)

// Scope says which half of the file an entry came from. It exists
// because every write and every listing has to be able to SAY it: "rm
// plans" removing a global default when the user meant their project's
// override is a silent, confusing loss, and a list that can't
// distinguish the two can't explain why a name means something
// different here.
type Scope string

const (
	// ScopeGlobal is the top-level `favorites` map — the default set,
	// applied in every project.
	ScopeGlobal Scope = "global"
	// ScopeProject is an entry from `projects[<root>]`, shadowing the
	// global entry of the same name for that one project.
	ScopeProject Scope = "project"
)

// ErrNotFound is returned by Resolve and Remove when no entry of that
// name exists in either scope. A sentinel rather than a formatted error
// so the CLI can tell "you have no favorite called that" (worth listing
// the ones you do have) apart from "the favorite exists but its
// directory doesn't" (worth naming the path that was missing).
var ErrNotFound = errors.New("no such favorite")

// Set is the parsed favorites.json. Both maps are nil-tolerant on read —
// a file with only defaults, or only overrides, is perfectly ordinary —
// and the JSON tags carry omitempty so re-saving a two-line file cannot
// balloon it with empty scaffolding (the theme registry's sparse-palette
// rule).
type Set struct {
	// Favorites is the default map: name → project-relative path.
	Favorites map[string]string `json:"favorites,omitempty"`

	// Projects maps a normalized project root to the names that project
	// spells differently. Keys go through session.Normalize (absolute,
	// cleaned, symlinks resolved) for the reason the session store
	// normalizes its own: `/tmp/proj` and `/private/tmp/proj` are one
	// directory on macOS, and two keys for it means an override that
	// silently applies only when you arrived by the right spelling.
	Projects map[string]map[string]string `json:"projects,omitempty"`
}

// Entry is one resolved name in a listing: what it is called, where it
// points, and which scope stated it. Sortable output rather than a map
// because a listing is read by a person, and Go randomises map order.
type Entry struct {
	Name  string
	Path  string // project-relative, as stated in the file
	Scope Scope
}

// Load reads and parses the favorites file at path.
//
// Contract, matching userconfig.Load's:
//
//   - path == ""            → (empty set, nil). No config location
//     resolved; the feature is simply unavailable, not broken.
//   - file doesn't exist    → (empty set, nil). The overwhelmingly
//     common case — most users have no favorites — so it must not be
//     an error anybody has to handle.
//   - file unreadable/bad   → (empty set, err). Surfaced, because a
//     file the user wrote and ced silently ignored is worse than a
//     message: they would go on believing the name was bound.
//
// An empty-but-present file parses to an empty set: `{}` is a legitimate
// thing to leave behind after removing your last favorite.
func Load(path string) (*Set, error) {
	s := &Set{}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, s); err != nil {
		return &Set{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Save writes the set back to path, creating the config directory when
// it isn't there yet.
//
// Written temp-file-then-rename for editor/fileio.go's reason, in its
// cheap form: the temp file lives in the TARGET's directory, because
// rename is only atomic within one filesystem. A half-written
// favorites.json would be a parse error on the next launch, and the
// process most likely to be interrupted mid-write is a CLI invocation
// the user may well Ctrl-C.
func (s *Set) Save(path string) error {
	if path == "" {
		return errors.New("no config location for favorites.json")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".favorites-*.json")
	if err != nil {
		// An unwritable config directory is a real refusal, but it
		// shouldn't cost the whole invocation a stack trace — the
		// caller reports it and exits non-zero.
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Clean validates and normalises a favorite's relative path. It is the
// single gate every write and every read goes through, which is what
// makes the confinement rule a property of the TYPE rather than of one
// call site somebody remembered.
//
// Refused: empty, absolute, and anything that climbs out of the project
// with "..". Accepted and normalised: "./ai_docs/plans" → "ai_docs/plans",
// trailing slashes trimmed, native separators applied so a Windows-style
// entry pasted into the file still resolves. "." is refused too — the
// project root is what you get without a favorite, so naming it is
// almost certainly a typo for something else.
func Clean(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", errors.New("path is empty")
	}
	// Accept forward slashes whatever the host separator is: these files
	// get copied between machines, and "ai_docs/plans" is how everyone
	// writes it.
	rel = filepath.FromSlash(strings.ReplaceAll(rel, "\\", "/"))
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%s is absolute — a favorite is relative to the project", rel)
	}
	clean := filepath.Clean(rel)
	if clean == "." {
		return "", errors.New("path is the project root itself")
	}
	// Clean has already collapsed interior "..", so a leading one is the
	// only way left to escape, and it survives Clean precisely because
	// it cannot be resolved without knowing the root.
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s climbs outside the project", rel)
	}
	return clean, nil
}

// CleanName validates a favorite's NAME. Deliberately strict about two
// things and indifferent to everything else: it may not be blank, and it
// may not look like a path, because a name that contained a separator
// would read as one in `ced fav <name>` and make the error message for a
// typo ("no such favorite ai_docs/plans") actively misleading. Leading
// dashes are out for the ordinary CLI reason — the name is an argument,
// and one starting with '-' is a flag as far as any parser is concerned.
func CleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", errors.New("name is empty")
	case strings.ContainsAny(name, `/\`):
		return "", fmt.Errorf("%q looks like a path — a favorite's name is a plain word", name)
	case strings.HasPrefix(name, "-"):
		return "", fmt.Errorf("%q starts with a dash, which the command line reads as a flag", name)
	}
	return name, nil
}

// Lookup returns the relative path bound to name for the project rooted
// at root, and the scope that stated it. The project override wins —
// that is the entire point of having one — and root == "" asks the
// global map alone, which is what a listing of the defaults wants.
func (s *Set) Lookup(root, name string) (string, Scope, bool) {
	if s == nil {
		return "", "", false
	}
	if root != "" {
		if m := s.Projects[session.Normalize(root)]; m != nil {
			if rel, ok := m[name]; ok {
				return rel, ScopeProject, true
			}
		}
	}
	if rel, ok := s.Favorites[name]; ok {
		return rel, ScopeGlobal, true
	}
	return "", "", false
}

// List returns every name visible from root — the global defaults with
// the project's overrides applied over them — sorted by name so two runs
// of `ced fav` print the same thing. A shadowed global entry appears
// ONCE, wearing the project scope, for the theme registry's reason: two
// rows with one name in a list is a bug report, and the value a user
// needs to see is the one that will actually be used.
func (s *Set) List(root string) []Entry {
	if s == nil {
		return nil
	}
	merged := make(map[string]Entry, len(s.Favorites))
	for name, rel := range s.Favorites {
		merged[name] = Entry{Name: name, Path: rel, Scope: ScopeGlobal}
	}
	if root != "" {
		for name, rel := range s.Projects[session.Normalize(root)] {
			merged[name] = Entry{Name: name, Path: rel, Scope: ScopeProject}
		}
	}
	out := make([]Entry, 0, len(merged))
	for _, e := range merged {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Add binds name to rel. root == "" writes the global default; anything
// else writes an override for that project. Both the name and the path
// go through their validators first, so an entry that could never
// resolve cannot be written in the first place — the alternative is a
// file that parses, lists, and then fails at the one moment the user
// wanted it.
func (s *Set) Add(root, name, rel string) (Entry, error) {
	name, err := CleanName(name)
	if err != nil {
		return Entry{}, err
	}
	clean, err := Clean(rel)
	if err != nil {
		return Entry{}, err
	}
	if root == "" {
		if s.Favorites == nil {
			s.Favorites = map[string]string{}
		}
		s.Favorites[name] = clean
		return Entry{Name: name, Path: clean, Scope: ScopeGlobal}, nil
	}
	key := session.Normalize(root)
	if s.Projects == nil {
		s.Projects = map[string]map[string]string{}
	}
	if s.Projects[key] == nil {
		s.Projects[key] = map[string]string{}
	}
	s.Projects[key][name] = clean
	return Entry{Name: name, Path: clean, Scope: ScopeProject}, nil
}

// Remove deletes name from one scope and reports which. The scope
// argument is what the caller ASKED for, and "" means "wherever it is",
// which resolves project-first for Lookup's reason: an override is the
// more specific statement, so it is the one `ced fav rm plans` typed
// inside that project almost certainly means.
//
// An emptied project block is deleted rather than left behind as `{}` —
// a file that accumulates empty entries for every project a user has
// ever tried a favorite in is a file nobody can read.
func (s *Set) Remove(root string, scope Scope, name string) (Scope, error) {
	if s == nil {
		return "", ErrNotFound
	}
	key := ""
	if root != "" {
		key = session.Normalize(root)
	}
	tryProject := func() bool {
		if key == "" || s.Projects[key] == nil {
			return false
		}
		if _, ok := s.Projects[key][name]; !ok {
			return false
		}
		delete(s.Projects[key], name)
		if len(s.Projects[key]) == 0 {
			delete(s.Projects, key)
		}
		return true
	}
	tryGlobal := func() bool {
		if _, ok := s.Favorites[name]; !ok {
			return false
		}
		delete(s.Favorites, name)
		return true
	}

	switch scope {
	case ScopeProject:
		if tryProject() {
			return ScopeProject, nil
		}
	case ScopeGlobal:
		if tryGlobal() {
			return ScopeGlobal, nil
		}
	default:
		if tryProject() {
			return ScopeProject, nil
		}
		if tryGlobal() {
			return ScopeGlobal, nil
		}
	}
	return "", ErrNotFound
}

// Resolution is the answer Resolve hands back: which project root owns
// the favorite, where it lives on disk, what the file called it, and
// which scope stated it. Root and Abs are both needed by the caller and
// are not derivable from each other once the walk has moved up a level.
type Resolution struct {
	Root  string // the project the favorite resolved in (absolute)
	Abs   string // the favorite's own absolute path
	Rel   string // as stated in the file, cleaned
	Scope Scope
}

// maxWalkUp bounds Resolve's climb. The walk terminates on its own at
// the filesystem root, so this is a belt-and-braces stop rather than the
// real terminator — but a symlink loop in a path prefix is the kind of
// thing that turns a bounded climb into a hang, and forty levels is
// deeper than any real checkout.
const maxWalkUp = 40

// Resolve finds the project root that owns name, starting at startDir
// and walking up.
//
// The walk is the feature, not a fallback: `ced fav plans` is typed from
// wherever the user is standing — three directories inside the project
// as often as not — and a version that only worked from the root would
// be half a feature. Each candidate directory is asked in full (its own
// project override first, then the global default), because the override
// is keyed by the root that owns it and that root is one of the
// directories being walked; looking the name up once against startDir
// would make an override invisible from any subdirectory of the project
// it belongs to.
//
// The first candidate whose path EXISTS wins, which is also what decides
// the root: the directory that owns the favorite is the project. A name
// that is bound but never resolves reports the last path it looked for,
// so the message names something the user can go and check; a name that
// is bound nowhere returns ErrNotFound, which the caller answers by
// listing what IS bound.
func (s *Set) Resolve(startDir, name string) (Resolution, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return Resolution{}, err
	}

	var bound bool       // the name exists in some scope somewhere on the walk
	var lastTried string // the deepest candidate path, for the error message
	for i := 0; i < maxWalkUp; i++ {
		rel, scope, ok := s.Lookup(dir, name)
		if ok {
			clean, cerr := Clean(rel)
			if cerr != nil {
				// A hand-edited file can hold a path Add would have
				// refused. Name the entry AND the rule it broke — the
				// user is looking at the file, and "invalid" alone
				// would not tell them which line.
				return Resolution{}, fmt.Errorf("favorite %q (%s): %w", name, scope, cerr)
			}
			bound = true
			abs := filepath.Join(dir, clean)
			lastTried = abs
			if _, err := os.Stat(abs); err == nil {
				// Confinement re-checked after the join resolves,
				// because a lexical check alone is escapable through a
				// symlink that lives inside the root (the workspace-edit
				// rule). Best-effort: a path that won't resolve keeps
				// its lexical verdict, which Clean already gave.
				if !within(dir, abs) {
					return Resolution{}, fmt.Errorf("favorite %q resolves outside %s", name, dir)
				}
				return Resolution{Root: dir, Abs: abs, Rel: clean, Scope: scope}, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	if !bound {
		return Resolution{}, ErrNotFound
	}
	return Resolution{}, fmt.Errorf("favorite %q: %s does not exist", name, lastTried)
}

// within reports whether abs sits inside root once both have had their
// symlinks resolved. Resolving the ROOT too is not optional: a project
// under /tmp on macOS lives at /private/tmp, so an unresolved root would
// make every file in it read as outside its own project. Best-effort on
// both sides — a path that cannot be resolved falls back to its lexical
// form, which is the same answer Clean already gave.
func within(root, abs string) bool {
	realRoot := root
	if r, err := filepath.EvalSymlinks(root); err == nil {
		realRoot = r
	}
	realAbs := abs
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		realAbs = r
	}
	rel, err := filepath.Rel(realRoot, realAbs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
