// =============================================================================
// File: internal/app/folder_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the workspace layer: folder switching, the recent list, and
// session record/restore. The App under test is built by newTestApp,
// which already points sessionStatePathFn and sessionConfigPathFn at a
// temp directory — nothing here can touch the developer's real state.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/session"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// seedSessionStore gives the App a live store and returns it, mirroring
// what loadSessionStore does at startup without the disk read.
func seedSessionStore(a *App) *session.Store {
	a.sessionStore = &session.Store{}
	return a.sessionStore
}

// newUntitledDirtyTab builds a pathless buffer with unsaved changes —
// the cheapest way to make saveAllDirty fail, since an untitled tab can
// never be written and the failure needs no broken filesystem.
func newUntitledDirtyTab() *editor.Tab {
	t, _ := editor.NewTab("")
	t.Dirty = true
	return t
}

// TestResolveFolder_AbsoluteRelativeAndTilde pins the three shapes a
// user types. The relative case is the load-bearing one: it resolves
// against rootDir, NOT the process working directory, because the
// embedded grsh terminal's `cd` chdirs the whole editor process — so
// "the current directory" is a value the user can't see.
func TestResolveFolder_AbsoluteRelativeAndTilde(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	a := newTestApp(t, root)

	got, err := a.resolveFolder(sub)
	if err != nil {
		t.Fatalf("absolute: %v", err)
	}
	if got != sub {
		t.Fatalf("absolute = %q, want %q", got, sub)
	}

	got, err = a.resolveFolder("sub")
	if err != nil {
		t.Fatalf("relative: %v", err)
	}
	if got != sub {
		t.Fatalf("relative resolved to %q, want %q (must be relative to rootDir)", got, sub)
	}

	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		got, err = a.resolveFolder("~")
		if err != nil {
			t.Fatalf("tilde: %v", err)
		}
		if got != filepath.Clean(home) {
			t.Fatalf("~ = %q, want %q", got, home)
		}
	}
}

// TestResolveFolder_RejectsFilesAndMissing pins the two refusals, each
// naming its reason. "Open folder" that silently accepted a file would
// root the editor at that file's parent without saying so.
func TestResolveFolder_RejectsFilesAndMissing(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a.go")
	if err := os.WriteFile(file, []byte("package a\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)

	if _, err := a.resolveFolder(file); err == nil {
		t.Fatal("a file should not resolve as a folder")
	} else if !strings.Contains(err.Error(), "not a folder") {
		t.Fatalf("file error = %q, want it to say why", err)
	}

	if _, err := a.resolveFolder(filepath.Join(root, "nope")); err == nil {
		t.Fatal("a missing path should not resolve as a folder")
	}
	if _, err := a.resolveFolder("   "); err == nil {
		t.Fatal("a blank path should not resolve as a folder")
	}
}

// TestRequestOpenFolder_CleanSetsNextRootAndQuits pins the switch
// mechanism: nextRoot plus quit, which is what asks main to tear this
// App down and build a fresh one. Both halves matter — nextRoot without
// quit does nothing, quit without nextRoot exits the editor.
func TestRequestOpenFolder_CleanSetsNextRootAndQuits(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	a := newTestApp(t, root)

	a.requestOpenFolder(other)
	if !a.quit {
		t.Fatal("a folder switch must ask the loop to exit")
	}
	if got := a.NextRoot(); got != other {
		t.Fatalf("NextRoot() = %q, want %q", got, other)
	}
}

// TestRequestOpenFolder_SameFolderIsANoOp pins that reopening the folder
// you're already in doesn't restart the editor. The restart is cheap but
// not free, and it would look like a flicker with no result.
func TestRequestOpenFolder_SameFolderIsANoOp(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	a.requestOpenFolder(root)
	if a.quit || a.NextRoot() != "" {
		t.Fatalf("switching to the current folder restarted: quit=%v next=%q", a.quit, a.NextRoot())
	}
	if !strings.Contains(a.statusMsg, "Already in") {
		t.Fatalf("status = %q, want it to say we're already there", a.statusMsg)
	}
}

// TestRequestOpenFolder_DirtyTabsPrompt pins the guard: a folder switch
// discards the whole workspace, so it owes the user the same
// unsaved-changes modal an exit does. Nothing may be set until they
// answer.
func TestRequestOpenFolder_DirtyTabsPrompt(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	file := filepath.Join(root, "a.txt")
	if err := os.WriteFile(file, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)
	a.openFile(file)
	a.tabs[0].InsertRune('x')
	if a.dirtyTabCount() != 1 {
		t.Fatalf("fixture is not dirty: %d", a.dirtyTabCount())
	}

	a.requestOpenFolder(other)
	if a.quit || a.NextRoot() != "" {
		t.Fatalf("dirty switch happened without an answer: quit=%v next=%q", a.quit, a.NextRoot())
	}
	m, ok := a.modal.(*dirtyModal)
	if !ok {
		t.Fatalf("modal = %T, want the unsaved-changes prompt", a.modal)
	}

	// Discard proceeds with the switch.
	m.discard(a)
	if !a.quit || a.NextRoot() != other {
		t.Fatalf("discard did not switch: quit=%v next=%q", a.quit, a.NextRoot())
	}
}

// TestRequestOpenFolder_SaveFailureBlocksTheSwitch pins the half of the
// dirty guard that's easy to get wrong: Save that FAILS must not switch.
// The workspace goes away with the switch, and so would the unsaved work.
func TestRequestOpenFolder_SaveFailureBlocksTheSwitch(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	a := newTestApp(t, root)
	// An untitled tab can never save, which is the cheapest way to make
	// saveAllDirty fail without breaking the filesystem.
	a.tabs = append(a.tabs, newUntitledDirtyTab())
	a.activeTab = 0

	a.requestOpenFolder(other)
	m, ok := a.modal.(*dirtyModal)
	if !ok {
		t.Fatalf("modal = %T, want the unsaved-changes prompt", a.modal)
	}
	m.save(a)
	if a.quit || a.NextRoot() != "" {
		t.Fatalf("a failed save still switched: quit=%v next=%q", a.quit, a.NextRoot())
	}
}

// TestRecordSession_CapturesTabsAndActive pins what Close writes: every
// titled tab, its cursor, its scroll, and which one was active — indexed
// against the RECORDED list, not the tab list, since untitled tabs are
// skipped on the way out.
func TestRecordSession_CapturesTabsAndActive(t *testing.T) {
	root := t.TempDir()
	one := filepath.Join(root, "one.txt")
	two := filepath.Join(root, "two.txt")
	for _, p := range []string{one, two} {
		if err := os.WriteFile(p, []byte("a\nb\nc\nd\n"), 0644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	a := newTestApp(t, root)
	seedSessionStore(a)
	a.openFile(one)
	a.openFile(two)
	a.tabs[1].Cursor.Line, a.tabs[1].Cursor.Col = 2, 1
	a.tabs[1].ScrollY = 1
	a.activeTab = 1

	a.recordSession()

	e, ok := a.sessionStore.Find(root)
	if !ok {
		t.Fatalf("no entry recorded for %q", root)
	}
	if len(e.Tabs) != 2 {
		t.Fatalf("recorded %d tabs, want 2", len(e.Tabs))
	}
	if e.Active != 1 {
		t.Fatalf("Active = %d, want 1", e.Active)
	}
	if e.Tabs[1].Line != 2 || e.Tabs[1].Col != 1 || e.Tabs[1].ScrollY != 1 {
		t.Fatalf("cursor/scroll not captured: %+v", e.Tabs[1])
	}
}

// TestRecordSession_SkipsUntitledAndReindexesActive pins the reindex: an
// untitled tab isn't recorded, so an Active taken from the LIVE tab list
// would point one past the end (or at the wrong file) on restore.
func TestRecordSession_SkipsUntitledAndReindexesActive(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "real.txt")
	if err := os.WriteFile(file, []byte("x\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)
	seedSessionStore(a)
	a.tabs = append(a.tabs, newUntitledDirtyTab())
	a.openFile(file) // becomes tab index 1
	a.activeTab = 1

	a.recordSession()

	e, _ := a.sessionStore.Find(root)
	if len(e.Tabs) != 1 {
		t.Fatalf("recorded %d tabs, want just the titled one", len(e.Tabs))
	}
	if e.Active != 0 {
		t.Fatalf("Active = %d, want 0 after the untitled tab was skipped", e.Active)
	}
}

// TestRestoreSession_ReopensTabsWithCursors is the round trip the whole
// feature exists for: record, throw the App away, restore into a fresh
// one, and land on the same file at the same place.
func TestRestoreSession_ReopensTabsWithCursors(t *testing.T) {
	root := t.TempDir()
	one := filepath.Join(root, "one.txt")
	two := filepath.Join(root, "two.txt")
	for _, p := range []string{one, two} {
		if err := os.WriteFile(p, []byte("l1\nl2\nl3\nl4\nl5\n"), 0644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	store := &session.Store{}
	store.Record(session.Entry{
		Root:   root,
		Active: 1,
		Tabs: []session.TabState{
			{Path: one, Line: 1, Col: 1},
			{Path: two, Line: 3, Col: 2, ScrollY: 2},
		},
	})

	a := newTestApp(t, root)
	a.sessionStore = store
	a.restoreSession()

	if len(a.tabs) != 2 {
		t.Fatalf("restored %d tabs, want 2", len(a.tabs))
	}
	if a.activeTab != 1 {
		t.Fatalf("activeTab = %d, want 1", a.activeTab)
	}
	if a.tabs[1].Cursor.Line != 3 || a.tabs[1].Cursor.Col != 2 {
		t.Fatalf("cursor = %+v, want line 3 col 2", a.tabs[1].Cursor)
	}
	if a.tabs[1].ScrollY != 2 {
		t.Fatalf("ScrollY = %d, want 2 (the stored scroll is part of the restore)", a.tabs[1].ScrollY)
	}
	// A restored tab must be wired like a clicked one — the git gutter,
	// plugin marks and diagnostics all ride DecoSources, and a tab that
	// came back without them would be silently second-class.
	if len(a.tabs[0].DecoSources) == 0 {
		t.Fatal("restored tab has no decoration sources — wireTab was skipped")
	}
}

// TestRestoreSession_SkipsVanishedFiles pins the quiet degradation: a
// file deleted since the last session is skipped, the rest still come
// back, and the active index still lands on a real tab.
func TestRestoreSession_SkipsVanishedFiles(t *testing.T) {
	root := t.TempDir()
	alive := filepath.Join(root, "alive.txt")
	if err := os.WriteFile(alive, []byte("x\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	store := &session.Store{}
	store.Record(session.Entry{
		Root:   root,
		Active: 1,
		Tabs: []session.TabState{
			{Path: filepath.Join(root, "gone.txt")},
			{Path: alive},
		},
	})

	a := newTestApp(t, root)
	a.sessionStore = store
	a.restoreSession()

	if len(a.tabs) != 1 {
		t.Fatalf("restored %d tabs, want 1", len(a.tabs))
	}
	if a.activeTab != 0 {
		t.Fatalf("activeTab = %d, want 0 — the index must follow what actually opened", a.activeTab)
	}
	if a.tabs[0].Path != alive {
		t.Fatalf("restored %q, want %q", a.tabs[0].Path, alive)
	}
}

// TestRestoreSession_DisabledDoesNothing pins the preference. Folders are
// still recorded with it off (the recent list reads the same file) — only
// the reopening stops.
func TestRestoreSession_DisabledDoesNothing(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a.txt")
	if err := os.WriteFile(file, []byte("x\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	store := &session.Store{}
	store.Record(session.Entry{Root: root, Tabs: []session.TabState{{Path: file}}})

	a := newTestApp(t, root)
	a.sessionStore = store
	a.sessionEnabled = false
	a.restoreSession()

	if len(a.tabs) != 0 {
		t.Fatalf("restore ran with the preference off: %d tabs", len(a.tabs))
	}
}

// TestMenuToggleSession_PersistsAndRelabels pins the ≡ row: it flips the
// flag, writes the config key, and names the action it will perform next
// (the toggle-in-place label rule).
func TestMenuToggleSession_PersistsAndRelabels(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.sessionToggleLabel(); got != "Disable session restore" {
		t.Fatalf("label with restore on = %q", got)
	}

	a.menuToggleSession()
	if a.sessionEnabled {
		t.Fatal("toggle did not flip the flag")
	}
	if got := a.sessionToggleLabel(); got != "Enable session restore" {
		t.Fatalf("label with restore off = %q", got)
	}

	cfg, err := userconfig.Load(sessionConfigPathFn())
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	if cfg.Session {
		t.Fatal("preference was not persisted")
	}
}

// TestRecentFolders_ExcludesCurrentAndPrunesMissing pins both filters on
// the picker's list. The current root is excluded because re-picking it
// would rebuild an identical workspace; a deleted folder is PRUNED from
// the store, because a row you can't open is worse than a shorter list.
func TestRecentFolders_ExcludesCurrentAndPrunesMissing(t *testing.T) {
	root := t.TempDir()
	live := t.TempDir()
	dead := filepath.Join(t.TempDir(), "removed")

	a := newTestApp(t, root)
	store := seedSessionStore(a)
	store.Touch(dead)
	store.Touch(live)
	store.Touch(root)

	a.menuRecentFolders()

	m, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want the picker", a.modal)
	}
	if len(m.items) != 1 {
		t.Fatalf("picker rows = %d, want just the one live folder: %+v", len(m.items), m.items)
	}
	if !strings.Contains(m.items[0].label, filepath.Base(live)) {
		t.Fatalf("row = %q, want the live folder", m.items[0].label)
	}
	if _, ok := store.Find(dead); ok {
		t.Fatal("a deleted folder survived the walk — it should be pruned")
	}
}

// TestRecentFolders_EmptyFlashesInsteadOfOpening pins that a picker with
// nothing in it never opens: an empty modal is a dead end, and the flash
// names the row that CAN get the user somewhere.
func TestRecentFolders_EmptyFlashesInsteadOfOpening(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	store := seedSessionStore(a)
	store.Touch(root)

	a.menuRecentFolders()
	if a.modal != nil {
		t.Fatalf("modal = %T, want none", a.modal)
	}
	if !strings.Contains(a.statusMsg, "Open folder") {
		t.Fatalf("status = %q, want it to point at Open folder…", a.statusMsg)
	}
	if a.hasRecentFolders() {
		t.Fatal("hasRecentFolders() should gate the row off with only the current folder recorded")
	}
}

// TestRecentFolders_PickSwitches pins the wiring from a picker row to the
// restart request — the row has to do the same thing Open folder… does.
func TestRecentFolders_PickSwitches(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	a := newTestApp(t, root)
	store := seedSessionStore(a)
	store.Touch(other)
	store.Touch(root)

	a.menuRecentFolders()
	m := a.modal.(*paletteModal)
	m.items[0].run(a)

	// Compared through Normalize: the store hands back resolved paths, so
	// on macOS the picker's row is /private/var/... while t.TempDir()
	// reported /var/... — the same folder either way.
	if session.Normalize(a.NextRoot()) != session.Normalize(other) || !a.quit {
		t.Fatalf("picking a recent folder did not switch: next=%q quit=%v", a.NextRoot(), a.quit)
	}
}

// TestDisplayPath_CollapsesHome pins the UI spelling of a path. Picker
// rows are read at a glance, and a long absolute prefix the user already
// knows is a third of the row spent saying nothing.
func TestDisplayPath_CollapsesHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory resolved")
	}
	if got := displayPath(home); got != "~" {
		t.Fatalf("displayPath(home) = %q, want ~", got)
	}
	if got, want := displayPath(filepath.Join(home, "projs")), "~/projs"; got != want {
		t.Fatalf("displayPath = %q, want %q", got, want)
	}
	// A path that merely SHARES a prefix with home must not be rewritten —
	// "/Users/robert" is not inside "/Users/rob".
	outside := home + "-elsewhere"
	if got := displayPath(outside); got != outside {
		t.Fatalf("displayPath(%q) = %q, want it unchanged", outside, got)
	}
	if got := displayPath("/etc"); got != "/etc" {
		t.Fatalf("displayPath(/etc) = %q, want unchanged", got)
	}
}

// TestExpandHome pins the tilde rules: bare ~ and ~/… expand, and
// anything else — including the ~user form a real shell supports — is
// left exactly as typed rather than half-handled.
func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory resolved")
	}
	if got := expandHome("~"); got != home {
		t.Fatalf("expandHome(~) = %q, want %q", got, home)
	}
	if got, want := expandHome("~/x"), filepath.Join(home, "x"); got != want {
		t.Fatalf("expandHome(~/x) = %q, want %q", got, want)
	}
	for _, in := range []string{"~other/x", "/abs/~", "rel/path"} {
		if got := expandHome(in); got != in {
			t.Fatalf("expandHome(%q) = %q, want unchanged", in, got)
		}
	}
}

// TestLoadSessionStore_RecordsTheVisitImmediately pins the crash-safety
// half of the design: being in a folder is written at STARTUP, so
// --last and the recent list are right even for a run that never exits
// cleanly. Only the tab list waits for Close.
func TestLoadSessionStore_RecordsTheVisitImmediately(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	a.loadSessionStore()

	if got := a.sessionStore.Last(); got != session.Normalize(root) {
		t.Fatalf("in-memory Last() = %q, want %q", got, session.Normalize(root))
	}
	onDisk, err := session.Load(sessionStatePathFn())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := onDisk.Last(); got != session.Normalize(root) {
		t.Fatalf("on-disk Last() = %q, want %q — the visit must be written now, not at exit",
			got, session.Normalize(root))
	}
}

// TestLoadSessionStore_BrokenFileStartsAnyway pins the degradation
// contract at the app level: a corrupt state file flashes and leaves a
// usable store, because it holds convenience and never the user's work.
func TestLoadSessionStore_BrokenFileStartsAnyway(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	if err := os.MkdirAll(filepath.Dir(sessionStatePathFn()), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(sessionStatePathFn(), []byte("{{{"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	a.loadSessionStore()
	if a.sessionStore == nil {
		t.Fatal("a broken state file left the app with no store")
	}
	if !strings.Contains(a.statusMsg, "session:") {
		t.Fatalf("status = %q, want the parse error surfaced", a.statusMsg)
	}
	// And it self-heals: the visit we just recorded is now valid JSON.
	if got := a.sessionStore.Last(); got != session.Normalize(root) {
		t.Fatalf("Last() = %q, want %q", got, session.Normalize(root))
	}
}

// TestRestoreSession_ActiveFileVanished pins the fallback when the tab
// the user was ON is the one that didn't come back: the last tab that
// DID is the closest thing to where they were. Leaving activeTab at its
// zero value would silently land them on an unrelated file.
func TestRestoreSession_ActiveFileVanished(t *testing.T) {
	root := t.TempDir()
	var alive []string
	for _, name := range []string{"a.txt", "b.txt"} {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte("x\n"), 0644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		alive = append(alive, p)
	}
	store := &session.Store{}
	store.Record(session.Entry{
		Root:   root,
		Active: 2, // the vanished one
		Tabs: []session.TabState{
			{Path: alive[0]},
			{Path: alive[1]},
			{Path: filepath.Join(root, "gone.txt")},
		},
	})

	a := newTestApp(t, root)
	a.sessionStore = store
	a.restoreSession()

	if len(a.tabs) != 2 {
		t.Fatalf("restored %d tabs, want 2", len(a.tabs))
	}
	if a.activeTab != 1 {
		t.Fatalf("activeTab = %d, want 1 (the last tab that did open)", a.activeTab)
	}
}
