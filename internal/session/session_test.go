// =============================================================================
// File: internal/session/session_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the workspace state store. The store is the only thing
// standing between "reopen my project" and "reopen something that used
// to be my project", so the pins here are about the invariants the app
// layer relies on without re-checking: most-recent-first order, an
// active index that can never point past the tab list, and a broken file
// costing nothing but its own contents.

package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoad_MissingAndBlank pins the two silent cases: no config location
// resolved, and a state file that doesn't exist yet (every first run).
// Both must produce an empty store and NO error — an editor that
// complained on its first launch would be reporting a normal condition
// as a fault.
func TestLoad_MissingAndBlank(t *testing.T) {
	s, err := Load("")
	if err != nil {
		t.Fatalf(`Load(""): %v`, err)
	}
	if len(s.Folders) != 0 {
		t.Fatalf(`Load(""): got %d folders, want 0`, len(s.Folders))
	}

	s, err = Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("Load(missing): %v", err)
	}
	if len(s.Folders) != 0 {
		t.Fatalf("Load(missing): got %d folders, want 0", len(s.Folders))
	}
}

// TestLoad_MalformedReturnsEmptyStoreAndError pins the degradation
// contract: a corrupt state file yields BOTH a usable empty store and
// the error, so the app can flash the reason and still start. Returning
// a nil store would turn a convenience file into a startup failure.
func TestLoad_MalformedReturnsEmptyStoreAndError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(p, []byte("{ not json"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s, err := Load(p)
	if err == nil {
		t.Fatal("malformed state file should report an error")
	}
	if s == nil || len(s.Folders) != 0 {
		t.Fatalf("malformed load must still yield an empty store, got %#v", s)
	}
}

// TestRoundTrip covers the ordinary path: record a folder with tabs,
// save, load it back, and get the same thing. This is the whole feature
// in one test.
func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")

	s := &Store{}
	s.Record(Entry{
		Root:   dir,
		Active: 1,
		Tabs: []TabState{
			{Path: filepath.Join(dir, "a.go"), Line: 3, Col: 7},
			{Path: filepath.Join(dir, "b.go"), Line: 40, ScrollY: 30},
		},
	})
	if err := s.Save(p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	e, ok := got.Find(dir)
	if !ok {
		t.Fatalf("Find(%q) missing after round trip", dir)
	}
	if len(e.Tabs) != 2 || e.Active != 1 {
		t.Fatalf("round trip = %+v, want 2 tabs / active 1", e)
	}
	if e.Tabs[0].Line != 3 || e.Tabs[0].Col != 7 || e.Tabs[1].ScrollY != 30 {
		t.Fatalf("cursor/scroll lost in round trip: %+v", e.Tabs)
	}
}

// TestSave_IsAtomicAndCreatesDir pins that Save makes the config
// directory when it isn't there (first run on a fresh machine) and
// leaves no temp file behind. The temp-file leak matters: this file is
// rewritten on every exit, so a stray state.json.tmp per run would
// accumulate forever.
func TestSave_IsAtomicAndCreatesDir(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "ced", "state.json")
	s := &Store{}
	s.Touch("/tmp/project")
	if err := s.Save(p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Fatal("temp file left behind after Save")
	}
}

// TestSave_NoPathErrors pins the "no config location resolved" case as an
// error rather than a silent no-op — the app flashes it, and a user with
// an unresolvable HOME deserves to know why nothing is being remembered.
func TestSave_NoPathErrors(t *testing.T) {
	if err := (&Store{}).Save(""); err == nil {
		t.Fatal("Save(\"\") should error")
	}
}

// TestTouchMovesToFrontAndKeepsTabs pins the recency queue's core move:
// re-touching a folder promotes it without discarding what was recorded
// for it. Losing the tabs here would mean every startup wiped the very
// session it was about to restore — Touch runs in New, before restore.
func TestTouchMovesToFrontAndKeepsTabs(t *testing.T) {
	s := &Store{}
	s.Record(Entry{Root: "/a", Tabs: []TabState{{Path: "/a/x.go", Line: 5}}})
	s.Touch("/b")
	if got := s.Last(); got != "/b" {
		t.Fatalf("Last() = %q, want /b", got)
	}

	s.Touch("/a")
	if got := s.Last(); got != "/a" {
		t.Fatalf("after re-touch Last() = %q, want /a", got)
	}
	e, ok := s.Find("/a")
	if !ok || len(e.Tabs) != 1 || e.Tabs[0].Line != 5 {
		t.Fatalf("Touch dropped the recorded tabs: %+v (found=%v)", e, ok)
	}
	if len(s.Folders) != 2 {
		t.Fatalf("re-touch duplicated the entry: %d folders", len(s.Folders))
	}
}

// TestRecentIsMostRecentFirst pins the order the pickers and --last both
// read. Order IS the recency in this file — there are no timestamps to
// fall back on — so a regression here is silent and total.
func TestRecentIsMostRecentFirst(t *testing.T) {
	s := &Store{}
	s.Touch("/one")
	s.Touch("/two")
	s.Touch("/three")
	want := []string{"/three", "/two", "/one"}
	got := s.Recent()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Recent() = %v, want %v", got, want)
	}
}

// TestRecentReturnsACopy pins that a caller sorting or truncating the
// result cannot reorder the store. The recent-folders picker builds rows
// from this slice, and a picker must not be able to rewrite history.
func TestRecentReturnsACopy(t *testing.T) {
	s := &Store{}
	s.Touch("/one")
	s.Touch("/two")
	got := s.Recent()
	got[0] = "/clobbered"
	if s.Last() == "/clobbered" {
		t.Fatal("Recent() aliased the store's own data")
	}
}

// TestEntryCapTrimsOldest pins MaxEntries as a tail trim: the file is
// rewritten every run, so without a cap a scripted ced would grow it
// without bound. The newest entry must survive; the oldest must not.
func TestEntryCapTrimsOldest(t *testing.T) {
	s := &Store{}
	for i := 0; i < MaxEntries+5; i++ {
		s.Touch(filepath.Join("/proj", string(rune('a'+i%26)), string(rune('0'+i/26))))
	}
	if len(s.Folders) > MaxEntries {
		t.Fatalf("stored %d folders, cap is %d", len(s.Folders), MaxEntries)
	}
}

// TestTabCapAndUntitledSkipped pins the two filters Record applies: an
// untitled buffer has no file to reopen, and a runaway tab count would
// turn startup into a stall. Both are enforced in the STORE so no writer
// can bypass them.
func TestTabCapAndUntitledSkipped(t *testing.T) {
	var tabs []TabState
	tabs = append(tabs, TabState{Path: ""}) // untitled — must be dropped
	for i := 0; i < MaxTabs+10; i++ {
		tabs = append(tabs, TabState{Path: filepath.Join("/p", "f.go")})
	}
	s := &Store{}
	s.Record(Entry{Root: "/p", Tabs: tabs})
	e, _ := s.Find("/p")
	if len(e.Tabs) != MaxTabs {
		t.Fatalf("recorded %d tabs, cap is %d", len(e.Tabs), MaxTabs)
	}
	for _, ts := range e.Tabs {
		if ts.Path == "" {
			t.Fatal("an untitled tab survived Record")
		}
	}
}

// TestActiveIndexIsClamped pins the invariant the app leans on without
// re-checking: Active always indexes a real tab. A stored index can
// outlive its tab when the cap trims the list or a hand-edit removes an
// entry, and an out-of-range active tab is a panic on the first render.
func TestActiveIndexIsClamped(t *testing.T) {
	s := &Store{}
	s.Record(Entry{Root: "/p", Active: 99, Tabs: []TabState{{Path: "/p/a.go"}, {Path: "/p/b.go"}}})
	if e, _ := s.Find("/p"); e.Active != 1 {
		t.Fatalf("Active = %d, want clamped to 1", e.Active)
	}

	s.Record(Entry{Root: "/q", Active: -3, Tabs: []TabState{{Path: "/q/a.go"}}})
	if e, _ := s.Find("/q"); e.Active != 0 {
		t.Fatalf("negative Active = %d, want 0", e.Active)
	}

	s.Record(Entry{Root: "/r", Active: 4})
	if e, _ := s.Find("/r"); e.Active != 0 {
		t.Fatalf("Active with no tabs = %d, want 0", e.Active)
	}
}

// TestLoadClampsHandEditedActive pins the same clamp on the READ side.
// state.json isn't meant to be hand-edited, but it is JSON in a config
// directory, so somebody will — and the clamp is cheaper than a crash.
func TestLoadClampsHandEditedActive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	body := `{"folders":[{"root":"/p","active":9,"tabs":[{"path":"/p/a.go"}]}]}`
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	e, ok := s.Find("/p")
	if !ok {
		t.Fatal("entry missing after load")
	}
	if e.Active != 0 {
		t.Fatalf("Active = %d, want clamped to 0", e.Active)
	}
}

// TestLoadDropsRootlessEntries pins that an entry with no root never
// enters the store: every lookup keys on the root, so a blank one is an
// entry nothing can ever address.
func TestLoadDropsRootlessEntries(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	body := `{"folders":[{"root":"","tabs":[{"path":"/x"}]},{"root":"/real"}]}`
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.Folders) != 1 || s.Folders[0].Root != "/real" {
		t.Fatalf("rootless entry survived: %+v", s.Folders)
	}
}

// TestRemoveForgetsAFolder pins the prune path the recent-folders picker
// uses when a directory has been deleted — a list of places you can't go
// is worse than a shorter list.
func TestRemoveForgetsAFolder(t *testing.T) {
	s := &Store{}
	s.Touch("/keep")
	s.Touch("/drop")
	s.Remove("/drop")
	if _, ok := s.Find("/drop"); ok {
		t.Fatal("Remove left the entry behind")
	}
	if _, ok := s.Find("/keep"); !ok {
		t.Fatal("Remove took the wrong entry")
	}
}

// TestFindDistinguishesEmptyFromAbsent pins the bool Find returns: a
// folder opened and left with no tabs is not the same as a folder never
// opened, and the restore path branches on exactly that.
func TestFindDistinguishesEmptyFromAbsent(t *testing.T) {
	s := &Store{}
	s.Touch("/seen")
	if e, ok := s.Find("/seen"); !ok || len(e.Tabs) != 0 {
		t.Fatalf("Find(/seen) = %+v, %v — want found with no tabs", e, ok)
	}
	if _, ok := s.Find("/never"); ok {
		t.Fatal("Find(/never) reported a folder that was never opened")
	}
}

// TestRootsAreNormalized pins that two spellings of one directory are
// one entry. Without it a folder opened as "." and later as its absolute
// path would keep two half-sessions that overwrite each other in turn.
func TestRootsAreNormalized(t *testing.T) {
	dir := t.TempDir()
	s := &Store{}
	s.Record(Entry{Root: dir, Tabs: []TabState{{Path: filepath.Join(dir, "a.go")}}})
	s.Touch(filepath.Join(dir, "sub", ".."))
	if len(s.Folders) != 1 {
		t.Fatalf("equivalent paths made %d entries: %v", len(s.Folders), s.Recent())
	}
	if e, ok := s.Find(dir); !ok || len(e.Tabs) != 1 {
		t.Fatalf("normalized touch lost the tabs: %+v (found=%v)", e, ok)
	}
}

// TestLastEmptyStore pins --last's answer on a first run: "" rather than
// a panic or a bogus path, so main falls back to the current directory.
func TestLastEmptyStore(t *testing.T) {
	if got := (&Store{}).Last(); got != "" {
		t.Fatalf("Last() on empty store = %q, want empty", got)
	}
}

// TestNormalizeResolvesSymlinks pins the dedupe that keeps one folder
// from keeping two half-sessions. `ced /tmp/proj` roots at the path as
// typed; `cd /tmp/proj && ced` roots at what the kernel reports, which
// on macOS is the resolved /private/tmp/proj. Both must key the same
// entry.
func TestNormalizeResolvesSymlinks(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if Normalize(link) != Normalize(real) {
		t.Fatalf("Normalize(link) = %q, Normalize(real) = %q — want the same entry",
			Normalize(link), Normalize(real))
	}

	s := &Store{}
	s.Record(Entry{Root: real, Tabs: []TabState{{Path: filepath.Join(real, "a.go")}}})
	s.Touch(link)
	if len(s.Folders) != 1 {
		t.Fatalf("link and target made %d entries: %v", len(s.Folders), s.Recent())
	}
	if e, ok := s.Find(link); !ok || len(e.Tabs) != 1 {
		t.Fatalf("lookup through the link lost the session: %+v (found=%v)", e, ok)
	}
}

// TestNormalizeKeepsMissingPaths pins the best-effort half: a folder
// recorded before it was deleted must keep an addressable key, or Remove
// could never prune it from the recent list.
func TestNormalizeKeepsMissingPaths(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "deleted")
	if got := Normalize(gone); got != gone {
		t.Fatalf("Normalize(missing) = %q, want %q", got, gone)
	}
	s := &Store{}
	s.Touch(gone)
	s.Remove(gone)
	if _, ok := s.Find(gone); ok {
		t.Fatal("a missing folder could not be pruned")
	}
}

// TestTouchRecentMovesRatherThanAppends is the ring's whole contract: a
// file visited twice occupies one slot, at the front. Get this wrong and
// a single file bounced between two others fills the list with itself.
func TestTouchRecentMovesRatherThanAppends(t *testing.T) {
	var ring []string
	for _, p := range []string{"/a", "/b", "/c", "/a"} {
		ring = TouchRecent(ring, p, 0)
	}
	want := []string{"/a", "/c", "/b"}
	if len(ring) != len(want) {
		t.Fatalf("ring = %v, want %v", ring, want)
	}
	for i := range want {
		if ring[i] != want[i] {
			t.Fatalf("ring = %v, want %v", ring, want)
		}
	}
	if got := TouchRecent(ring, "", 0); len(got) != len(ring) {
		t.Fatalf("an empty path entered the ring: %v", got)
	}
}

// The cap trims the TAIL — the oldest visit — and never the head.
func TestTouchRecentCapsTheTail(t *testing.T) {
	ring := []string{"/a", "/b", "/c"}
	ring = TouchRecent(ring, "/d", 3)
	if len(ring) != 3 {
		t.Fatalf("ring = %v, want 3 entries", ring)
	}
	if ring[0] != "/d" || ring[2] != "/b" {
		t.Fatalf("ring = %v, want the newest at the head and /c dropped", ring)
	}
}

// TestRecentSurvivesTheStateFile is the round trip that makes the picker
// useful across restarts, plus the hand-edit defence: duplicates collapse
// to their most recent position rather than showing up as two identical
// picker rows.
func TestRecentSurvivesTheStateFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "state.json")
	s := &Store{}
	s.Record(Entry{Root: root, Recent: []string{"/x", "/y", "", "/x"}})
	if err := s.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	back, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	e, ok := back.Find(root)
	if !ok {
		t.Fatal("entry did not survive the round trip")
	}
	if len(e.Recent) != 2 || e.Recent[0] != "/x" || e.Recent[1] != "/y" {
		t.Fatalf("Recent = %v, want [/x /y] — blanks dropped, duplicate collapsed", e.Recent)
	}
}

// A folder's ring survives Touch: startup marks the folder most-recent
// before anything else runs, and it must not cost the list it carries.
func TestTouchKeepsTheRecentRing(t *testing.T) {
	root := t.TempDir()
	s := &Store{}
	s.Record(Entry{Root: root, Recent: []string{"/x"}})
	s.Touch(root)
	if e, _ := s.Find(root); len(e.Recent) != 1 {
		t.Fatalf("Touch dropped the ring: %+v", e)
	}
}
