// =============================================================================
// File: internal/app/catscapture_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/cats"
)

// The whole verb, end to end: read a sibling pane and diff it against the
// buffer. The captured text is the OLD side — compare.go's rule is that the
// active buffer is always "new", which is also how the question is asked
// ("what has the agent got that I haven't?").
func TestCatsCaptureOpensTheCompareePanel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	s.captureText = "package main\n\nfunc main() { println(\"hi\") }\n\n\n"
	a.cats.panes = siblingPanes()
	a.cats.self, a.cats.selfOK = 9, true
	openTestFileTab(t, a, "main.go")

	a.catsCapturePane(cats.PaneInfo{Pane: 11, Handle: "w1:p4", Agent: "claude"})

	call := waitForCall(t, s, cats.MethodCapture)
	var p struct {
		Pane   uint32 `json:"pane"`
		Scope  uint8  `json:"scope"`
		Lines  uint32 `json:"lines"`
		Unwrap bool   `json:"unwrap"`
	}
	if err := json.Unmarshal(call.Params, &p); err != nil {
		t.Fatalf("capture params: %v", err)
	}
	if p.Pane != 11 || p.Scope != cats.CaptureRecent || p.Lines != catsCaptureLines || !p.Unwrap {
		t.Fatalf("capture params = %+v", p)
	}

	pumpAppEvents(t, a, func() bool { return a.compare.oldLabel != "" })

	if !strings.Contains(a.compare.oldLabel, "claude in w1:p4") {
		t.Fatalf("old label = %q — it has to read as a place", a.compare.oldLabel)
	}
	// The pane's trailing blank screen rows are gone; its interior is not.
	if n := len(a.compare.oldLines); n != 3 {
		t.Fatalf("old side has %d lines: %q", n, a.compare.oldLines)
	}
	if !a.compare.open {
		t.Fatal("the compare panel did not open")
	}
}

// Only the TAIL is trimmed. A diff whose old side had its interior blank
// lines silently removed would report changes the user never made.
func TestCatsTrimCapture(t *testing.T) {
	got := catsTrimCapture("a\r\n\r\nb\n   \n\n")
	if got != "a\n\nb" {
		t.Fatalf("trim = %q", got)
	}
	if catsTrimCapture("   \n\n") != "" {
		t.Fatal("an empty screen must trim to nothing")
	}
}

// An empty pane is said so rather than opening an empty diff — and never
// reaches the compare panel, which would otherwise report the whole buffer
// as added.
func TestCatsCaptureOfAnEmptyPaneSaysSo(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	s.captureText = "\n\n   \n"
	openTestFileTab(t, a, "main.go")
	a.statusMsg = "" // the open's own message would satisfy the pump below

	a.catsCapturePane(cats.PaneInfo{Pane: 11, Handle: "w1:p4"})

	waitForCall(t, s, cats.MethodCapture)
	pumpAppEvents(t, a, func() bool { return a.statusMsg != "" })
	if !strings.Contains(a.statusMsg, "no output") {
		t.Fatalf("status = %q", a.statusMsg)
	}
	if a.compare.oldLabel != "" {
		t.Fatal("an empty capture opened a diff anyway")
	}
}

// The picker offers agent panes when there are any — "compare with the
// agent" is the question this exists for — and every other pane when there
// are none. Our own pane is in neither list: ced reports itself to cats as
// the agent "ced", so without the check the buffer would be diffed against a
// picture of itself.
func TestCatsComparablePanes(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.cats.self, a.cats.selfOK = 9, true
	a.cats.panes = siblingPanes()

	got := a.catsComparablePanes()
	if len(got) != 2 || got[0].Agent != "claude" {
		t.Fatalf("with agents present: %+v", got)
	}

	// No agents: the plain shells become the offer rather than nothing.
	a.cats.panes = []cats.PaneInfo{{Pane: 5, Handle: "w1:p2"}, {Pane: 9, Handle: "w1:p9", Agent: "ced"}}
	got = a.catsComparablePanes()
	if len(got) != 1 || got[0].Pane != 5 {
		t.Fatalf("with no agents: %+v", got)
	}
}

// With several candidates it asks which, like every other choose-one surface.
func TestCatsComparePaneAsksWhich(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	withCtlSpy(t, a)
	a.cats.self, a.cats.selfOK = 9, true
	a.cats.panes = siblingPanes()
	openTestFileTab(t, a, "main.go")

	a.menuCatsComparePane()

	if labels := pickerLabels(t, a); len(labels) != 2 {
		t.Fatalf("rows = %v, want one per sibling agent", labels)
	}
}

// At Tier 0 the row explains itself instead of failing silently, and the
// gate is closed.
func TestCatsComparePaneIsANoopAtTier0(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.cats.panes = siblingPanes()
	openTestFileTab(t, a, "main.go")

	if a.hasCatsComparePane() {
		t.Fatal("the row was live without a control socket")
	}
	a.menuCatsComparePane()
	if !strings.Contains(a.statusMsg, "plain terminal") {
		t.Fatalf("status = %q", a.statusMsg)
	}
}
