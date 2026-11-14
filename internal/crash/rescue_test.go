// =============================================================================
// File: internal/crash/rescue_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package crash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rescueAt is a fixed moment, so folder names are predictable.
var rescueAt = time.Date(2026, 11, 13, 14, 25, 1, 0, time.Local)

// TestRescue_WritesTheLayoutAndAnIndex is the happy path: project files
// keep their root-relative path, outside files go under _outside/, an
// untitled buffer gets a name, every byte lands as given, and INDEX.txt
// names each original.
func TestRescue_WritesTheLayoutAndAnIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "recover")
	root := filepath.Join(string(filepath.Separator), "work", "proj")
	bufs := []Buffer{
		{Path: filepath.Join(root, "internal", "app", "app.go"), Data: []byte("package app\r\n")},
		{Path: filepath.Join(string(filepath.Separator), "etc", "hosts"), Data: []byte("127.0.0.1 x\n")},
		{Path: "", Data: []byte("scratch"), Note: "written as UTF-8"},
	}

	folder, out, err := Rescue(dir, root, "received SIGTERM", rescueAt, bufs)
	if err != nil {
		t.Fatalf("Rescue: %v", err)
	}
	if want := filepath.Join(dir, "20261113-142501-proj"); folder != want {
		t.Fatalf("folder = %q, want %q", folder, want)
	}
	wantFiles := map[string]string{
		filepath.Join("internal", "app", "app.go"): "package app\r\n",
		filepath.Join("_outside", "etc", "hosts"):  "127.0.0.1 x\n",
		"untitled-1.txt": "scratch",
	}
	if len(out) != len(wantFiles) {
		t.Fatalf("rescued %d, want %d: %+v", len(out), len(wantFiles), out)
	}
	for rel, body := range wantFiles {
		p := filepath.Join(folder, rel)
		got, err := os.ReadFile(p)
		if err != nil || string(got) != body {
			t.Errorf("%s = %q (%v), want %q", rel, got, err, body)
		}
		if info, err := os.Stat(p); err == nil && info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600 (it may hold secrets)", rel, info.Mode().Perm())
		}
	}
	index, err := os.ReadFile(filepath.Join(folder, IndexName))
	if err != nil {
		t.Fatalf("no %s: %v", IndexName, err)
	}
	for _, want := range []string{"received SIGTERM", root, bufs[0].Path, "/etc/hosts", "(untitled)", "note: written as UTF-8"} {
		if !strings.Contains(string(index), want) {
			t.Errorf("INDEX.txt lacks %q:\n%s", want, index)
		}
	}
}

// TestRescue_NothingDirtyWritesNothing pins that a clean exit leaves no
// empty folders behind.
func TestRescue_NothingDirtyWritesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "recover")
	folder, out, err := Rescue(dir, "/p", "why", rescueAt, nil)
	if err != nil || folder != "" || out != nil {
		t.Fatalf("Rescue(nil) = %q, %v, %v; want nothing", folder, out, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("an empty rescue created %s", dir)
	}
}

// TestRescue_NoConfigDirectoryIsAnError pins the obvious failure: with
// no HOME and no XDG_CONFIG_HOME there is nowhere to write, and the
// caller must be told rather than believe the edits are safe.
func TestRescue_NoConfigDirectoryIsAnError(t *testing.T) {
	if _, _, err := Rescue("", "/p", "why", rescueAt, []Buffer{{Path: "/p/a", Data: []byte("x")}}); err == nil {
		t.Fatal("Rescue with no directory should fail")
	}
}

// TestRescue_SameSecondGetsItsOwnFolder pins the two-instance case: two
// editors on one project hung up by the same tmux kill must not write
// into each other's folder.
func TestRescue_SameSecondGetsItsOwnFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "recover")
	bufs := []Buffer{{Path: "/p/a.txt", Data: []byte("one")}}
	first, _, err := Rescue(dir, "/p", "why", rescueAt, bufs)
	if err != nil {
		t.Fatal(err)
	}
	bufs[0].Data = []byte("two")
	second, _, err := Rescue(dir, "/p", "why", rescueAt, bufs)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasSuffix(second, "-2") {
		t.Fatalf("second rescue went to %q (first %q)", second, first)
	}
	if got, _ := os.ReadFile(filepath.Join(first, "a.txt")); string(got) != "one" {
		t.Fatalf("first rescue was overwritten: %q", got)
	}
}

// TestPrune_RemovesOnlyOldRescueFolders pins the housekeeping's limits:
// only folders whose name is a rescue stamp older than the cutoff go;
// a recent rescue, a folder the user renamed and a loose file stay.
func TestPrune_RemovesOnlyOldRescueFolders(t *testing.T) {
	dir := t.TempDir()
	old := rescueAt.Add(-KeepFor-time.Hour).Format(stampLayout) + "-proj"
	recent := rescueAt.Add(-time.Hour).Format(stampLayout) + "-proj"
	for _, name := range []string{old, recent, "keep-this"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Prune(dir, rescueAt, KeepFor); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, old)); !os.IsNotExist(err) {
		t.Error("an expired rescue folder survived")
	}
	for _, name := range []string{recent, "keep-this", "notes.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
}

// TestRescueRel_SiblingFolderIsOutside pins the prefix trap: /p-other is
// not inside /p, even though the strings share a prefix.
func TestRescueRel_SiblingFolderIsOutside(t *testing.T) {
	n := 0
	cases := map[string]string{
		"/p/a/b.go":  filepath.Join("a", "b.go"),
		"/p-other/x": filepath.Join("_outside", "p-other", "x"),
		"/elsewhere": filepath.Join("_outside", "elsewhere"),
	}
	for in, want := range cases {
		if got := rescueRel("/p", filepath.FromSlash(in), &n); got != want {
			t.Errorf("rescueRel(%q) = %q, want %q", in, got, want)
		}
	}
	if got := rescueRel("/p", "", &n); got != "untitled-1.txt" {
		t.Errorf("first untitled = %q", got)
	}
	if got := rescueRel("/p", "", &n); got != "untitled-2.txt" {
		t.Errorf("second untitled = %q", got)
	}
}

// TestWriteExclusive_NeverOverwrites pins that a name clash inside a
// rescue folder gets a suffix rather than replacing a rescued buffer.
func TestWriteExclusive_NeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	first, err := writeExclusive(dir, "a.txt", []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := writeExclusive(dir, "a.txt", []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("second write reused the first name")
	}
	if got, _ := os.ReadFile(first); string(got) != "first" {
		t.Fatalf("first file overwritten: %q", got)
	}
}

// TestProjectLabel_IsFolderNameSafe pins the folder-name hint.
func TestProjectLabel_IsFolderNameSafe(t *testing.T) {
	cases := map[string]string{
		"/work/ced":      "ced",
		"/work/my proj!": "my_proj_",
		"":               "ced",
		"/":              "ced",
	}
	for in, want := range cases {
		if got := projectLabel(in); got != want {
			t.Errorf("projectLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
