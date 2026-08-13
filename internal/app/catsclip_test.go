// =============================================================================
// File: internal/app/catsclip_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/cats"
)

// The §4 Tier-1 gesture, end to end: one menu row and the panel is already
// showing the diff. The Tier-0 version of this row arms the panel and waits
// for the user to paste — the whole point of the upstream verb is that those
// two gestures become none.
func TestCatsCompareClipboardOpensThePanelPopulated(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	s.clipText = "package main\n\nfunc main() {}\n"
	openTestFileTab(t, a, "main.go")

	a.menuCatsCompareClipboard()

	waitForCall(t, s, cats.MethodClipboardRead)
	pumpAppEvents(t, a, func() bool { return a.compare.oldLabel != "" })

	if a.compare.oldLabel != "host clipboard" {
		t.Fatalf("old label = %q", a.compare.oldLabel)
	}
	if len(a.compare.oldLines) != 3 {
		t.Fatalf("old side = %q", a.compare.oldLines)
	}
	if !a.compare.open {
		t.Fatal("the compare panel did not open")
	}
	// Nothing was armed: a populated panel waiting for a paste would claim
	// the user's next ⌘V for a diff it has already computed.
	if a.compare.awaitPaste {
		t.Fatal("the panel is still waiting for a paste it does not need")
	}
}

// Outside cats the row does not dim and does not fail — it falls back to the
// clipboard ced CAN see. The ladder's rule is that the feature survives the
// downgrade, not that the row does.
func TestCatsCompareClipboardFallsBackToTheInternalClipboard(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openTestFileTab(t, a, "main.go")
	a.clipBuf = "func main() {}\n"

	a.menuCatsCompareClipboard() // Tier 0: no control socket at all

	if a.compare.oldLabel != "clipboard" {
		t.Fatalf("old label = %q, want the internal clipboard's", a.compare.oldLabel)
	}
	if !a.compare.open {
		t.Fatal("the compare panel did not open")
	}
}

// With no host and nothing copied in ced either, the row becomes the armed
// paste — the original Tier-0 ritual, reached by falling through rather than
// by the user having to know which row to pick.
func TestCatsCompareClipboardArmsAPasteWithNothingToCompare(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openTestFileTab(t, a, "main.go")

	a.menuCatsCompareClipboard()

	if !a.compare.awaitPaste {
		t.Fatal("with no clipboard at all the row should arm a paste")
	}
	if !strings.Contains(a.statusMsg, "Paste now") {
		t.Fatalf("status = %q — an armed mode has to say it is armed", a.statusMsg)
	}
}

// An empty host clipboard is an ordinary answer, not a failure — "I have not
// copied anything yet" is a normal state. It must not open an empty diff,
// which would report the whole buffer as added.
func TestCatsClipboardEmptySaysSo(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	openTestFileTab(t, a, "main.go")
	a.statusMsg = ""

	a.menuCatsCompareClipboard()

	waitForCall(t, s, cats.MethodClipboardRead)
	pumpAppEvents(t, a, func() bool { return strings.Contains(a.statusMsg, "empty") })
	if a.compare.oldLabel != "" {
		t.Fatal("an empty clipboard opened a diff anyway")
	}
}

// A host that cannot read a clipboard at all says which tools would fix it,
// and ced passes that through rather than replacing it with its own guess.
func TestCatsClipboardReportsTheHostsRefusal(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	s.clipFails = true
	openTestFileTab(t, a, "main.go")
	a.statusMsg = ""

	a.menuCatsPasteClipboard()

	waitForCall(t, s, cats.MethodClipboardRead)
	pumpAppEvents(t, a, func() bool { return a.statusMsg != "" })
	if !strings.Contains(a.statusMsg, "no reader on this host") {
		t.Fatalf("status = %q, want the host's own words", a.statusMsg)
	}
}

// The paste lands at the caret in the buffer the user was in.
func TestCatsPasteClipboardInsertsAtTheCaret(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	s.clipText = "INSERTED"
	openTestFileTab(t, a, "main.go")

	a.menuCatsPasteClipboard()

	waitForCall(t, s, cats.MethodClipboardRead)
	pumpAppEvents(t, a, func() bool {
		return strings.Contains(a.activeTabPtr().Buffer.String(), "INSERTED")
	})
	if !strings.Contains(a.activeTabPtr().Buffer.String(), "INSERTED") {
		t.Fatalf("buffer = %q", a.activeTabPtr().Buffer.String())
	}
}

// A buffer that MOVED while the read was in flight is not written to. On a
// local socket this is close to unreachable; the case it exists for is a host
// that has wedged and answers seconds later, when the user is mid-word
// somewhere else. The text is not thrown away either — it goes to the
// internal clipboard, so the answer is "press ⌘V" rather than "gone".
func TestCatsPasteClipboardDeclinesAMovedBuffer(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openTestFileTab(t, a, "main.go")
	tab := a.activeTabPtr()
	before := tab.Buffer.String()

	// The arrival as it would look after the user kept typing: the request's
	// revision is one the tab has already left behind.
	a.catsClipArrived(&catsEvent{
		kind: catsKindClip, text: "LATE", clipUse: catsClipPasteAt,
		clipTab: tab, clipRev: tab.EditRev - 1,
	})

	if got := tab.Buffer.String(); got != before {
		t.Fatalf("a stale paste was inserted anyway: %q", got)
	}
	if a.clipBuf != "LATE" {
		t.Fatalf("clipBuf = %q — a declined paste must still be reachable", a.clipBuf)
	}
	if !strings.Contains(a.statusMsg, "⌘V") {
		t.Fatalf("status = %q — the refusal has to say what to do instead", a.statusMsg)
	}
}

// A tab switch is the other half of the same guard: the revision could match
// by coincidence in a fresh buffer, so identity is checked too.
func TestCatsPasteClipboardDeclinesADifferentTab(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openTestFileTab(t, a, "one.go")
	first := a.activeTabPtr()
	openTestFileTab(t, a, "two.go")
	second := a.activeTabPtr()
	if first == second {
		t.Fatal("setup: the second open did not become the active tab")
	}
	before := second.Buffer.String()

	a.catsClipArrived(&catsEvent{
		kind: catsKindClip, text: "WRONG", clipUse: catsClipPasteAt,
		clipTab: first, clipRev: first.EditRev,
	})

	if got := second.Buffer.String(); got != before {
		t.Fatalf("text landed in the wrong tab: %q", got)
	}
	if strings.Contains(first.Buffer.String(), "WRONG") {
		t.Fatal("text landed in a background tab")
	}
}

// A truncated clipboard is used, and said so. A partial diff is still useful;
// silently presenting four megabytes of a larger paste as the whole thing is
// not.
func TestCatsClipboardTruncationIsLabelled(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openTestFileTab(t, a, "main.go")

	a.catsClipArrived(&catsEvent{
		kind: catsKindClip, text: "half a file\n", clipUse: catsClipCompare, truncated: true,
	})

	if !strings.Contains(a.compare.oldLabel, "truncated") {
		t.Fatalf("old label = %q — a partial side has to say so", a.compare.oldLabel)
	}
}

// Below Tier 1 the paste row explains itself instead of dialling a socket
// that is not there, and points at the key that does work.
func TestCatsPasteClipboardIsANoopAtTier0(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openTestFileTab(t, a, "main.go")
	before := a.activeTabPtr().Buffer.String()

	a.menuCatsPasteClipboard()

	if a.activeTabPtr().Buffer.String() != before {
		t.Fatal("Tier 0 pasted something")
	}
	if !strings.Contains(a.statusMsg, "⌘V") {
		t.Fatalf("status = %q — the refusal has to name the fallback", a.statusMsg)
	}
}

// The transport prefix this package puts on every error is noise on a status
// line; the host's own sentence is not.
func TestCatsClipError(t *testing.T) {
	cases := map[string]string{
		"cats: clipboard.read: pbpaste: no such file": "pbpaste: no such file",
		"cats: dial /tmp/x: connection refused":       "dial /tmp/x: connection refused",
		"clipboard unavailable: no reader":            "clipboard unavailable: no reader",
	}
	for in, want := range cases {
		if got := catsClipError(errString(in)); got != want {
			t.Errorf("catsClipError(%q) = %q, want %q", in, got, want)
		}
	}
}

// errString is a one-line error literal for the table above.
type errString string

func (e errString) Error() string { return string(e) }
