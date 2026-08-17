// =============================================================================
// File: internal/app/hostident_test.go
// Author: Rohan Allison
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureIdent installs a capturing writer on a test App and returns the
// slice of emitted sequences. newTestApp leaves hostIdentWrite nil on
// purpose (no test may write titles to the developer's terminal), so
// every test that wants emissions opts in through this.
func captureIdent(a *App) *[]string {
	var got []string
	a.hostIdentWrite = func(seq string) error {
		got = append(got, seq)
		return nil
	}
	return &got
}

func TestFileURLPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/me/projs", "/Users/me/projs"},
		{"/tmp/a b", "/tmp/a%20b"},                    // space encodes
		{"/x/naïve", "/x/na%C3%AFve"},                 // unicode encodes byte-wise
		{"/x/semi;colon", "/x/semi%3Bcolon"},          // reserved encodes
		{"/x/esc\x1bseq", "/x/esc%1Bseq"},             // control bytes can't break the OSC
		{"/a-b._~/ok", "/a-b._~/ok"},                  // unreserved passes through
	}
	for _, c := range cases {
		if got := fileURLPath(c.in); got != c.want {
			t.Errorf("fileURLPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestOSC7CwdSeq(t *testing.T) {
	got := osc7CwdSeq("mybox", "/Users/me/proj x")
	want := "\x1b]7;file://mybox/Users/me/proj%20x\x07"
	if got != want {
		t.Errorf("osc7CwdSeq = %q, want %q", got, want)
	}
}

func TestOSC2TitleSeqStripsControlBytes(t *testing.T) {
	// An ESC or BEL smuggled in via a filename must not terminate the
	// sequence early and leak the rest into the host parser.
	got := osc2TitleSeq("bad\x1b]2;evil\x07name")
	want := "\x1b]2;bad]2;evilname\x07"
	if got != want {
		t.Errorf("osc2TitleSeq = %q, want %q", got, want)
	}
}

func TestHostIdentTitle(t *testing.T) {
	if got := hostIdentTitle("main.go", false); got != "main.go · ced" {
		t.Errorf("clean title = %q", got)
	}
	if got := hostIdentTitle("main.go", true); got != "● main.go · ced" {
		t.Errorf("dirty title = %q", got)
	}
}

// TestHostIdentAfterEventLifecycle walks the identity through its state
// changes — no tab, file opened, buffer dirtied, tab closed — and
// asserts one emission per change and zero for the no-change events in
// between (the allocation-free early return).
func TestHostIdentAfterEventLifecycle(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "hello.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	got := captureIdent(a)

	// First reconcile: no tab open, title names the workspace folder.
	a.hostIdentAfterEvent()
	if len(*got) != 1 || !strings.Contains((*got)[0], filepath.Base(root)+" · ced") {
		t.Fatalf("workspace title: got %q", *got)
	}

	// Same state again: no emission.
	a.hostIdentAfterEvent()
	if len(*got) != 1 {
		t.Fatalf("dedupe failed: %q", *got)
	}

	// Open a file: title switches to the file name.
	a.openFile(target)
	a.hostIdentAfterEvent()
	if len(*got) != 2 || (*got)[1] != "\x1b]2;hello.go · ced\x07" {
		t.Fatalf("open title: got %q", *got)
	}

	// Dirty the buffer: dot appears.
	a.activeTabPtr().Dirty = true
	a.hostIdentAfterEvent()
	if len(*got) != 3 || (*got)[2] != "\x1b]2;● hello.go · ced\x07" {
		t.Fatalf("dirty title: got %q", *got)
	}

	// Idle events while dirty: no re-emission.
	a.hostIdentAfterEvent()
	a.hostIdentAfterEvent()
	if len(*got) != 3 {
		t.Fatalf("dirty dedupe failed: %q", *got)
	}
}

// TestHostIdentCloseClearsThenPops pins the exit contract: the title is
// cleared BEFORE the stack pop. A terminal without a title stack ignores
// the pop, so the empty OSC 2 is the only thing that stops the pane
// keeping "somefile.go · ced" after ced is gone; a terminal with a stack
// overwrites it with the restored title a byte later. Reversing the order
// would put the stale title back on every stackless terminal.
func TestHostIdentCloseClearsThenPops(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	got := captureIdent(a)

	a.hostIdentClose()

	if len(*got) != 2 {
		t.Fatalf("expected clear + pop, got %q", *got)
	}
	if (*got)[0] != "\x1b]2;\x07" {
		t.Fatalf("first sequence should be an empty OSC 2 title, got %q", (*got)[0])
	}
	if (*got)[1] != "\x1b[23;0t" {
		t.Fatalf("second sequence should be XTPOPTITLE, got %q", (*got)[1])
	}
}

// TestHostIdentNilWriterIsOff proves the tests-and-degradation contract:
// with no writer installed nothing panics and nothing is tracked.
func TestHostIdentNilWriterIsOff(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.hostIdentAfterEvent()
	a.hostIdentClose()
	if a.identSent {
		t.Fatal("identSent should stay false with a nil writer")
	}
}
