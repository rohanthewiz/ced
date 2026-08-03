// =============================================================================
// File: internal/remote/remote_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for remote.go — the socket handoff, root-based discovery, the
// wait leg, and the stale-socket cleanup.
//
// Every test runs against a private socket directory so nothing here can
// probe (or unlink) a real running editor's socket.

package remote

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sockDir points socketDirFn at a throwaway directory for the duration
// of one test.
//
// It deliberately does NOT use t.TempDir(): a unix socket path is capped
// at ~104 bytes by the kernel, and on macOS t.TempDir() alone burns most
// of that on $TMPDIR plus the test's own name, so a socket created
// inside it fails to bind. A short base is a hard requirement of the
// transport, not a preference.
func sockDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(shortTempBase(), "ced")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	prev := socketDirFn
	socketDirFn = func() string { return dir }
	t.Cleanup(func() { socketDirFn = prev })
	return dir
}

// shortTempBase returns the shortest writable scratch base available, so
// socket paths stay inside the kernel's limit. See sockDir.
func shortTempBase() string {
	if fi, err := os.Stat("/tmp"); err == nil && fi.IsDir() {
		return "/tmp"
	}
	return os.TempDir()
}

// recorder is a Handler that captures what it was asked to open and
// hands back a channel the test controls.
type recorder struct {
	path string
	wait bool
	done chan struct{}
	err  error
}

func (r *recorder) handle(path string, wait bool) (<-chan struct{}, error) {
	r.path, r.wait = path, wait
	if r.err != nil {
		return nil, r.err
	}
	return r.done, nil
}

// TestOpen_HandsTheFileToTheRunningInstance is the happy path: a server
// is listening for a root, and a client asking for a file inside that
// root reaches it.
func TestOpen_HandsTheFileToTheRunningInstance(t *testing.T) {
	sockDir(t)
	root := t.TempDir()
	file := filepath.Join(root, "note.txt")
	if err := os.WriteFile(file, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := &recorder{}
	srv, err := Listen(root, rec.handle)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = srv.Close() }()

	if err := Open(file, false); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if rec.path != normalizeFile(file) {
		t.Fatalf("handler saw %q, want %q", rec.path, normalizeFile(file))
	}
	if rec.wait {
		t.Error("wait should be false for a plain --remote open")
	}
}

// TestOpen_NoInstanceWhenNothingIsListening pins the fallback signal:
// the CLI branches on ErrNoInstance to start a local editor, so it must
// be distinguishable from a real failure.
func TestOpen_NoInstanceWhenNothingIsListening(t *testing.T) {
	sockDir(t)
	file := filepath.Join(t.TempDir(), "a.txt")
	if err := Open(file, false); !errors.Is(err, ErrNoInstance) {
		t.Fatalf("Open with no server = %v, want ErrNoInstance", err)
	}
}

// TestOpen_RefusesAFileOutsideEveryRoot proves discovery is scoped by
// project: an instance rooted elsewhere must not be handed the file,
// because its tree, index and language server all describe a different
// workspace.
func TestOpen_RefusesAFileOutsideEveryRoot(t *testing.T) {
	sockDir(t)
	rec := &recorder{}
	srv, err := Listen(t.TempDir(), rec.handle)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = srv.Close() }()

	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	if err := Open(outside, false); !errors.Is(err, ErrNoInstance) {
		t.Fatalf("Open outside every root = %v, want ErrNoInstance", err)
	}
	if rec.path != "" {
		t.Fatalf("handler was called with %q — the file left its project", rec.path)
	}
}

// TestOpen_PrefersTheMostSpecificRoot pins the tie-break: with an
// instance on a project and another on a subdirectory of it, a file in
// the subdirectory belongs to the inner one.
func TestOpen_PrefersTheMostSpecificRoot(t *testing.T) {
	sockDir(t)
	outer := t.TempDir()
	inner := filepath.Join(outer, "sub")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}

	outerRec, innerRec := &recorder{}, &recorder{}
	s1, err := Listen(outer, outerRec.handle)
	if err != nil {
		t.Fatalf("listen outer: %v", err)
	}
	defer func() { _ = s1.Close() }()
	// Same pid, so the two sockets must differ by their root hash alone
	// — which is exactly what per-root naming buys.
	s2, err := Listen(inner, innerRec.handle)
	if err != nil {
		t.Fatalf("listen inner: %v", err)
	}
	defer func() { _ = s2.Close() }()

	file := filepath.Join(inner, "deep.txt")
	if err := Open(file, false); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if innerRec.path == "" {
		t.Fatal("the inner instance should have received the file")
	}
	if outerRec.path != "" {
		t.Fatalf("the outer instance also received %q", outerRec.path)
	}
}

// TestOpen_WaitBlocksUntilTheEditorReleases is the --wait contract: the
// client must not return while the editor still has the file, or `git
// commit` would commit an empty message the moment the tab opened.
func TestOpen_WaitBlocksUntilTheEditorReleases(t *testing.T) {
	sockDir(t)
	root := t.TempDir()
	rec := &recorder{done: make(chan struct{})}
	srv, err := Listen(root, rec.handle)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = srv.Close() }()

	returned := make(chan error, 1)
	go func() { returned <- Open(filepath.Join(root, "COMMIT_EDITMSG"), true) }()

	select {
	case err := <-returned:
		t.Fatalf("Open returned early (%v) — the wait leg did not block", err)
	case <-time.After(150 * time.Millisecond):
	}

	close(rec.done)
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Open after release = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Open did not return after the editor released the file")
	}
	if !rec.wait {
		t.Error("handler should have been told the client is waiting")
	}
}

// TestServerClose_ReleasesWaitingClients pins the other half of the wait
// contract: an editor that exits must unblock everyone waiting on it. A
// client left hanging here is a shell prompt in another pane that never
// comes back.
func TestServerClose_ReleasesWaitingClients(t *testing.T) {
	sockDir(t)
	root := t.TempDir()
	rec := &recorder{done: make(chan struct{})} // never closed by the test
	srv, err := Listen(root, rec.handle)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	returned := make(chan error, 1)
	go func() { returned <- Open(filepath.Join(root, "x.txt"), true) }()
	// Let the request land before tearing the server down.
	time.Sleep(100 * time.Millisecond)
	_ = srv.Close()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("closing the server left a --wait client blocked")
	}
}

// TestOpen_SurfacesAHandlerRefusal proves an editor that declines the
// file says so, rather than the client reporting success and exiting
// while nothing opened.
func TestOpen_SurfacesAHandlerRefusal(t *testing.T) {
	sockDir(t)
	root := t.TempDir()
	rec := &recorder{err: errors.New("no thanks")}
	srv, err := Listen(root, rec.handle)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = srv.Close() }()

	err = Open(filepath.Join(root, "x.txt"), false)
	if err == nil {
		t.Fatal("a refused open must return an error")
	}
	if errors.Is(err, ErrNoInstance) {
		t.Fatal("a refusal must not read as ErrNoInstance — that would silently start a second editor")
	}
}

// TestFindInstance_UnlinksDeadSockets pins the cleanup: a socket file
// left behind by a crashed instance is removed by the next client, so
// the directory can't accumulate probes nobody will ever answer.
func TestFindInstance_UnlinksDeadSockets(t *testing.T) {
	dir := sockDir(t)
	dead := filepath.Join(dir, "deadbeef-1.sock")
	if err := os.WriteFile(dead, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := findInstance(filepath.Join(t.TempDir(), "x.txt"))
	if !errors.Is(err, ErrNoInstance) {
		t.Fatalf("findInstance = %v, want ErrNoInstance", err)
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatal("a socket nobody answers should have been unlinked")
	}
}

// TestListen_RemovesTheSocketOnClose keeps the runtime directory honest
// for the common case — a clean exit leaves nothing behind for the next
// client to probe.
func TestListen_RemovesTheSocketOnClose(t *testing.T) {
	sockDir(t)
	srv, err := Listen(t.TempDir(), (&recorder{}).handle)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	path := srv.Path()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("socket not created: %v", err)
	}
	_ = srv.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("Close should unlink the socket")
	}
	// Idempotent — Close runs from both the ≡ toggle and App.Close.
	if err := srv.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}
}

// TestContains_RequiresASeparatorBoundary pins the containment rule that
// keeps /home/ro/proj from claiming files in /home/ro/project-notes — a
// plain prefix test gets this wrong and delivers files to the wrong
// workspace.
func TestContains_RequiresASeparatorBoundary(t *testing.T) {
	cases := []struct {
		root, target string
		want         bool
	}{
		{"/a/proj", "/a/proj/main.go", true},
		{"/a/proj", "/a/proj", true},
		{"/a/proj", "/a/project-notes/main.go", false},
		{"/a/proj", "/a/other/main.go", false},
		{"/a/proj/", "/a/proj/sub/deep.go", true},
	}
	for _, c := range cases {
		if got := contains(c.root, c.target); got != c.want {
			t.Errorf("contains(%q, %q) = %v, want %v", c.root, c.target, got, c.want)
		}
	}
}

// TestOwns_ResolvesSymlinksOnBothSides proves the exported guard agrees
// with the client's discovery even when the two processes named the
// project differently (a symlinked project directory, or macOS's
// /tmp → /private/tmp).
func TestOwns_ResolvesSymlinksOnBothSides(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if !Owns(real, filepath.Join(link, "file.go")) {
		t.Error("a file reached through a symlinked root should still be owned")
	}
	if !Owns(link, filepath.Join(real, "file.go")) {
		t.Error("a symlinked root should own the real path's files")
	}
	if Owns(real, filepath.Join(t.TempDir(), "file.go")) {
		t.Error("an unrelated directory must not be owned")
	}
}

// TestNormalizeFile_HandlesAMissingFile pins the new-file case: `ced
// --wait notes.md` on a path that doesn't exist yet must still resolve
// to something the containment test can use.
func TestNormalizeFile_HandlesAMissingFile(t *testing.T) {
	dir := t.TempDir()
	got := normalizeFile(filepath.Join(dir, "not-there.md"))
	if filepath.Base(got) != "not-there.md" {
		t.Fatalf("normalizeFile dropped the base name: %q", got)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("normalizeFile = %q, want an absolute path", got)
	}
}

// TestSocketName_DiffersByRootAndPid pins the naming rule that lets two
// instances on one project both listen, and one instance's socket be
// recognisable by project.
func TestSocketName_DiffersByRootAndPid(t *testing.T) {
	if socketName("/a", 1) == socketName("/b", 1) {
		t.Error("different roots must produce different socket names")
	}
	if socketName("/a", 1) == socketName("/a", 2) {
		t.Error("two instances on one root must produce different socket names")
	}
	// Deterministic: a client's probe cleanup and a restarting instance
	// both depend on the same (root, pid) producing the same file.
	first := socketName("/a", 1)
	if again := socketName("/a", 1); first != again {
		t.Errorf("socket name is not deterministic: %q then %q", first, again)
	}
}
