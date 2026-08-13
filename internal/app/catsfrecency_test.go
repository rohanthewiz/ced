// =============================================================================
// File: internal/app/catsfrecency_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/cats"
	"github.com/rohanthewiz/ced/internal/session"
)

// mkdirs creates directories under a temp root and returns their paths, for
// the frecency list to point at something real.
func mkdirs(t *testing.T, base string, names ...string) []string {
	t.Helper()
	out := make([]string, 0, len(names))
	for _, n := range names {
		p := filepath.Join(base, n)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		out = append(out, p)
	}
	return out
}

// The host's list is filtered, not merely forwarded: folders ced already
// knows about are dropped (it lists them itself, with tab counts), and
// folders that no longer exist are pruned rather than dimmed — a list of
// places you cannot go is worse than a shorter list.
func TestCatsRecentFoldersDedupesAndPrunes(t *testing.T) {
	base := t.TempDir()
	dirs := mkdirs(t, base, "proj-a", "proj-b")
	gone := filepath.Join(base, "deleted")

	a := newTestApp(t, t.TempDir())
	a.cats.recents = []string{dirs[0], gone, dirs[1], dirs[1]}

	got := a.catsRecentFolders(map[string]bool{session.Normalize(dirs[0]): true})

	if len(got) != 1 || got[0] != session.Normalize(dirs[1]) {
		t.Fatalf("merged = %v, want just %q", got, dirs[1])
	}
}

// The cap keeps a fuzzy picker usable: the host has no upper bound on its
// history, and two hundred rows of shell wandering is a worse instrument
// than a dozen ranked ones.
func TestCatsRecentFoldersIsCapped(t *testing.T) {
	base := t.TempDir()
	names := make([]string, 0, catsRecentsMax+5)
	for i := 0; i < catsRecentsMax+5; i++ {
		names = append(names, "d"+string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	a := newTestApp(t, t.TempDir())
	a.cats.recents = mkdirs(t, base, names...)

	if got := len(a.catsRecentFolders(nil)); got != catsRecentsMax {
		t.Fatalf("kept %d rows, want the cap %d", got, catsRecentsMax)
	}
}

// The merged picker: ced's own places first (they carry tab lists and were
// recorded by this program), the host's after, marked — and the current
// root in neither half.
func TestRecentFoldersPickerMergesTheHostList(t *testing.T) {
	base := t.TempDir()
	dirs := mkdirs(t, base, "edited", "visited")

	a := newTestApp(t, dirs[0])
	withCtlSpy(t, a)
	a.loadSessionStore() // records the current root
	other := mkdirs(t, base, "edited-before")[0]
	a.sessionStore.Touch(other)
	// The host knows about the folder we are IN, one we have edited, and
	// one we have only ever cd'd into.
	a.cats.recents = []string{a.rootDir, other, dirs[1]}

	a.menuRecentFolders()

	labels := pickerLabels(t, a)
	if len(labels) != 2 {
		t.Fatalf("rows = %v, want two (the edited one and the visited one)", labels)
	}
	if strings.Contains(labels[0], "cats") {
		t.Fatalf("row 0 = %q — ced's own places come first", labels[0])
	}
	if !strings.HasSuffix(labels[1], "· cats") || !strings.Contains(labels[1], "visited") {
		t.Fatalf("row 1 = %q, want the marked host row", labels[1])
	}
	for _, l := range labels {
		if strings.Contains(l, "/edited"+string(filepath.Separator)) {
			t.Fatalf("the current root came back as a row: %q", l)
		}
	}
}

// Inside cats the picker is worth opening on the very first run, before
// this editor has been anywhere — which is the whole point of borrowing the
// host's history.
func TestRecentFoldersOpensOnTheHostListAlone(t *testing.T) {
	base := t.TempDir()
	dirs := mkdirs(t, base, "somewhere")

	a := newTestApp(t, t.TempDir())
	withCtlSpy(t, a)
	a.sessionStore = nil
	a.cats.recents = dirs

	if !a.hasRecentFolders() {
		t.Fatal("the row should be live on the host's list alone")
	}
	a.menuRecentFolders()
	if labels := pickerLabels(t, a); len(labels) != 1 {
		t.Fatalf("rows = %v, want the one host row", labels)
	}
}

// The "new tab" path hands cats an argv — no shell, nothing to quote — and
// names the tab after the project, because a strip of tabs all labeled
// "ced" carries no information.
func TestCatsOpenProjectInTabSpawnsAnArgv(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	catsExecutable = func() (string, error) { return "/opt/bin/ced", nil }
	t.Cleanup(func() { catsExecutable = os.Executable })
	dir := mkdirs(t, t.TempDir(), "my-proj")[0]

	a.catsOpenProjectInTab(dir)

	call := waitForCall(t, s, cats.MethodTabCreate)
	var p struct {
		Title   string   `json:"title"`
		Cwd     string   `json:"cwd"`
		Command []string `json:"command"`
	}
	if err := json.Unmarshal(call.Params, &p); err != nil {
		t.Fatalf("params: %v", err)
	}
	if p.Title != "my-proj" || p.Cwd != dir {
		t.Fatalf("tab = %+v", p)
	}
	want := []string{"/opt/bin/ced", "--root", dir}
	if len(p.Command) != len(want) {
		t.Fatalf("command = %v, want %v", p.Command, want)
	}
	for i := range want {
		if p.Command[i] != want[i] {
			t.Fatalf("command = %v, want %v", p.Command, want)
		}
	}
}

// Outside cats the row explains itself instead of doing nothing, and the
// poll that feeds it never dials.
func TestCatsFrecencyIsANoopAtTier0(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.catsPollRecents() // must not panic, must not dial
	if a.hasCatsProjects() {
		t.Fatal("no host, no projects")
	}
	a.menuCatsOpenProject()
	if !strings.Contains(a.statusMsg, "cats") {
		t.Fatalf("status = %q, want an explanation", a.statusMsg)
	}
}

// waitForCall blocks until the spy has seen a method, so a test can assert
// on a request made from a goroutine.
func waitForCall(t *testing.T, s *ctlSpy, method string) ctlCall {
	t.Helper()
	for i := 0; i < 200; i++ {
		if c, ok := s.call(method); ok {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never reached the socket", method)
	return ctlCall{}
}
