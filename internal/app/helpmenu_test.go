// =============================================================================
// File: internal/app/helpmenu_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestLogFlash_KeepsTheLastAndFoldsRepeats pins N-134's log: every flash
// is kept, a message repeated back to back is one entry with a count, and
// only the newest flashLogMax survive.
func TestLogFlash_KeepsTheLastAndFoldsRepeats(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.flashLog = nil
	a.flash("same")
	a.flash("same")
	if len(a.flashLog) != 1 || a.flashLog[0].count != 2 {
		t.Fatalf("repeat: %+v, want one entry ×2", a.flashLog)
	}
	for i := 0; i < flashLogMax+10; i++ {
		a.flash(fmt.Sprintf("m%d", i))
	}
	if len(a.flashLog) != flashLogMax {
		t.Fatalf("kept %d, want %d", len(a.flashLog), flashLogMax)
	}
	if last := a.flashLog[len(a.flashLog)-1].msg; last != fmt.Sprintf("m%d", flashLogMax+9) {
		t.Errorf("newest = %q", last)
	}
	a.flash("")
	if a.flashLog[len(a.flashLog)-1].msg == "" {
		t.Error("an empty flash was logged")
	}
}

// TestMenuMessages_ListsNewestFirst: the Messages… picker shows the log
// newest first, with the cap in its title.
func TestMenuMessages_ListsNewestFirst(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.flashLog = nil
	a.flash("Save failed: disk full")
	a.flash("Opened main.go")
	a.menuMessages()
	m := paletteOf(a)
	if m == nil {
		t.Fatal("no picker")
	}
	if !strings.Contains(m.title, fmt.Sprint(flashLogMax)) {
		t.Errorf("title %q does not announce the cap", m.title)
	}
	if len(m.items) != 2 || !strings.HasSuffix(m.items[0].label, "Opened main.go") ||
		!strings.HasSuffix(m.items[1].label, "Save failed: disk full") {
		var labels []string
		for _, it := range m.items {
			labels = append(labels, it.label)
		}
		t.Errorf("rows = %q, want newest first", labels)
	}
}

// TestMenuOpenConfig_CreatesAndOpens is N-135: the row opens config.json
// as a tab, creating it as an empty object when missing.
func TestMenuOpenConfig_CreatesAndOpens(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := configPathFn()
	a.menuOpenConfig()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "{}\n" {
		t.Fatalf("config.json = %q, %v; want it created as {}", got, err)
	}
	if tab := a.activeTabPtr(); tab == nil || tab.Path != path {
		t.Errorf("active tab is not config.json")
	}
}

// TestOpenConfigLabel_HoldsTheLoadProblem: a config that failed to load
// names its problem in the row (held, not only flashed at startup).
func TestOpenConfigLabel_HoldsTheLoadProblem(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.openConfigLabel(); got != "Open config.json" {
		t.Errorf("clean label = %q", got)
	}
	a.configLoadErr = `"autosavedelay": not a duration`
	if got := a.openConfigLabel(); !strings.Contains(got, "problem: ") || !strings.Contains(got, "autosavedelay") {
		t.Errorf("label = %q, want the problem named", got)
	}
}

// TestMenuKeyboardReference_OpensTheBand: the row closes the menu and
// opens the which-key band, which refuses to open over the menu.
func TestMenuKeyboardReference_OpensTheBand(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuOpen = true
	a.menuKeyboardReference()
	if a.menuOpen || !a.whichKey.open {
		t.Errorf("menuOpen=%v whichKey.open=%v, want the band up", a.menuOpen, a.whichKey.open)
	}
}
