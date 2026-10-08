// =============================================================================
// File: internal/app/hostopen_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-08
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// hostCall is one recorded hostRun invocation.
type hostCall struct {
	dir  string
	argv []string
}

// recordHostRun swaps hostRun for a recorder that answers with err and
// out, and returns the channel each call is reported on. Buffered so the
// verb's goroutine never blocks on a test that reads only the first call.
func recordHostRun(t *testing.T, out string, err error) <-chan hostCall {
	t.Helper()
	calls := make(chan hostCall, 8)
	prev := hostRun
	hostRun = func(dir string, argv []string) ([]byte, error) {
		calls <- hostCall{dir: dir, argv: append([]string(nil), argv...)}
		return []byte(out), err
	}
	t.Cleanup(func() { hostRun = prev })
	return calls
}

// withHost pins the platform and environment the host verbs read.
func withHost(t *testing.T, goos string, env map[string]string) {
	t.Helper()
	prevGOOS, prevEnv := hostGOOS, hostEnv
	hostGOOS = goos
	hostEnv = func(k string) string { return env[k] }
	t.Cleanup(func() { hostGOOS, hostEnv = prevGOOS, prevEnv })
}

// nextHostCall waits briefly for the verb's goroutine to call hostRun.
func nextHostCall(t *testing.T, calls <-chan hostCall) hostCall {
	t.Helper()
	select {
	case c := <-calls:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("hostRun was never called")
	}
	return hostCall{}
}

// TestFileManagerArgv_MacRevealsFilesAndOpensFolders pins the macOS
// split: a file is REVEALED (-R, selected in its folder) — plain `open`
// would launch its default app instead — and a folder is opened.
func TestFileManagerArgv_MacRevealsFilesAndOpensFolders(t *testing.T) {
	withHost(t, "darwin", nil)
	if got, _ := fileManagerArgv("/p/a.go", false); !reflect.DeepEqual(got, []string{"open", "-R", "/p/a.go"}) {
		t.Fatalf("file argv = %v", got)
	}
	if got, _ := fileManagerArgv("/p/pkg", true); !reflect.DeepEqual(got, []string{"open", "/p/pkg"}) {
		t.Fatalf("folder argv = %v", got)
	}
}

// TestFileManagerArgv_LinuxOpensTheFolder pins xdg-open's shape: it has
// no select-a-file form, so a file opens its parent folder.
func TestFileManagerArgv_LinuxOpensTheFolder(t *testing.T) {
	withHost(t, "linux", map[string]string{"DISPLAY": ":0"})
	if got, _ := fileManagerArgv("/p/a.go", false); !reflect.DeepEqual(got, []string{"xdg-open", "/p"}) {
		t.Fatalf("file argv = %v", got)
	}
	if got, _ := fileManagerArgv("/p/pkg", true); !reflect.DeepEqual(got, []string{"xdg-open", "/p/pkg"}) {
		t.Fatalf("folder argv = %v", got)
	}
}

// TestFileManagerArgv_NoDesktopIsAReason pins the SSH-into-a-Linux-box
// case: nothing to open a window on, so a reason comes back instead of a
// command that would only fail obscurely.
func TestFileManagerArgv_NoDesktopIsAReason(t *testing.T) {
	withHost(t, "linux", nil)
	argv, reason := fileManagerArgv("/p", true)
	if argv != nil || !strings.Contains(reason, "$DISPLAY") {
		t.Fatalf("argv=%v reason=%q, want a $DISPLAY reason", argv, reason)
	}
	// Wayland alone is a desktop too.
	withHost(t, "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"})
	if argv, reason := fileManagerArgv("/p", true); argv == nil || reason != "" {
		t.Fatalf("wayland: argv=%v reason=%q", argv, reason)
	}
}

// TestFileManagerLabel_NamesFinderOnlyOnMac pins the label per platform.
func TestFileManagerLabel_NamesFinderOnlyOnMac(t *testing.T) {
	withHost(t, "darwin", nil)
	if got := fileManagerLabel(); got != "Open in Finder" {
		t.Fatalf("darwin label = %q", got)
	}
	withHost(t, "linux", nil)
	if got := fileManagerLabel(); got != "Open file manager" {
		t.Fatalf("linux label = %q", got)
	}
}

// TestRevealInFileManager_RunsOpenOnTheClickedFile is the happy path end
// to end: the tree row's verb hands `open -R <abs>` to the host.
func TestRevealInFileManager_RunsOpenOnTheClickedFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a.go")
	if err := os.WriteFile(file, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	withHost(t, "darwin", nil)
	calls := recordHostRun(t, "", nil)

	a.revealInFileManager(file)

	c := nextHostCall(t, calls)
	if !reflect.DeepEqual(c.argv, []string{"open", "-R", file}) {
		t.Fatalf("argv = %v", c.argv)
	}
	if !strings.Contains(a.statusMsg, "Finder") {
		t.Fatalf("statusMsg = %q, want it to say Finder", a.statusMsg)
	}
}

// TestRevealInFileManager_MissingPathRefuses: a node deleted since the
// tree's last refresh must refuse, not hand `open` a dead name.
func TestRevealInFileManager_MissingPathRefuses(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	withHost(t, "darwin", nil)
	calls := recordHostRun(t, "", nil)

	a.revealInFileManager(filepath.Join(root, "gone.txt"))

	if !strings.Contains(a.statusMsg, "no longer exists") {
		t.Fatalf("statusMsg = %q", a.statusMsg)
	}
	select {
	case c := <-calls:
		t.Fatalf("hostRun called with %v for a missing path", c.argv)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestRevealInFileManager_SSHOnMacSaysWhereItWent: over SSH the window
// opens on the Mac's own screen, which the flash has to say.
func TestRevealInFileManager_SSHOnMacSaysWhereItWent(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	withHost(t, "darwin", map[string]string{"SSH_CONNECTION": "1.2.3.4 5 6.7.8.9 22"})
	recordHostRun(t, "", nil)

	a.revealInFileManager(root)

	if !strings.Contains(a.statusMsg, "SSH") {
		t.Fatalf("statusMsg = %q, want it to mention SSH", a.statusMsg)
	}
}

// TestHostRunAsync_PostsAPromptFailureWithTheToolsOwnWords: a failure
// comes back as a notice event carrying the command's first output line.
func TestHostRunAsync_PostsAPromptFailureWithTheToolsOwnWords(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	recordHostRun(t, "\nxdg-open: no method available\nmore\n", errors.New("exit status 3"))

	a.hostRunAsync(a.rootDir, []string{"xdg-open", a.rootDir}, "Open file manager")

	scr := a.screen.(tcell.SimulationScreen)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("no failure notice posted")
		default:
		}
		ev := scr.PollEvent()
		ce, ok := ev.(*catsEvent)
		if !ok {
			continue
		}
		want := "Open file manager failed: exit status 3 — xdg-open: no method available"
		if ce.notice != want {
			t.Fatalf("notice = %q, want %q", ce.notice, want)
		}
		return
	}
}

// TestTreeContext_HostRowsOnEveryNode pins the three new rows' presence
// and order: on files, folders and the root, directly under the $EDITOR
// row they extend.
func TestTreeContext_HostRowsOnEveryNode(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	withHost(t, "darwin", nil)
	for _, n := range []string{"", "pkg", "a.go"} {
		node := a.tree.Root
		if n != "" {
			node = treeNodeFor(t, a, n)
		}
		a.openTreeContext(node, 1, 1)
		m, ok := a.modal.(*contextModal)
		if !ok {
			t.Fatalf("%q: no context modal", n)
		}
		var labels []string
		for _, it := range m.items {
			labels = append(labels, it.label)
		}
		at := -1
		for i, l := range labels {
			if l == a.openInEditorLabel() {
				at = i
			}
		}
		if at < 0 || at+3 >= len(labels) {
			t.Fatalf("%q: rows = %v", n, labels)
		}
		want := []string{"Open in Finder", "Open in terminal", "Shell command…"}
		if got := labels[at+1 : at+4]; !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: rows after the editor row = %v, want %v", n, got, want)
		}
		a.closeModal()
	}
}

// TestContextMenuWidthFor_FitsTheWidestLabel is the regression test for
// rows running over the popup's right border: the width now grows to the
// widest label, never below the classic width, never past the screen.
func TestContextMenuWidthFor_FitsTheWidestLabel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.contextMenuWidthFor([]contextItem{{label: "Zip"}}); got != contextMenuWidth {
		t.Fatalf("short labels: width %d, want the floor %d", got, contextMenuWidth)
	}
	long := "Add to favorites…"
	if got := a.contextMenuWidthFor([]contextItem{{label: long}}); got != runeLen(long)+6 {
		t.Fatalf("long label: width %d, want %d", got, runeLen(long)+6)
	}
	a.width = 12
	if got := a.contextMenuWidthFor([]contextItem{{label: long}}); got != 12 {
		t.Fatalf("narrow screen: width %d, want 12", got)
	}
}
