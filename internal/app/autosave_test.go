// =============================================================================
// File: internal/app/autosave_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-09
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/userconfig"
)

// autoSaveFixture builds an enabled-auto-save app with one open tab
// backed by a real file, since every scenario below starts from
// exactly that state.
func autoSaveFixture(t *testing.T) (*App, string) {
	t.Helper()
	useTestTrustFile(t)
	root := t.TempDir()
	a := newTestApp(t, root)
	a.autoSaveEnabled = true
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	openTabAtPath(t, a, target)
	t.Cleanup(a.stopAutoSave)
	return a, target
}

// TestAutoSaveAfterEvent_ArmsOnEdit pins the debounce trigger: a
// buffer mutation (EditRev bump) must arm the idle countdown.
func TestAutoSaveAfterEvent_ArmsOnEdit(t *testing.T) {
	a, _ := autoSaveFixture(t)
	a.tabs[0].InsertRune('x')

	a.autoSaveAfterEvent()

	if a.autoSaveTimer == nil {
		t.Fatal("edit should arm the auto-save timer")
	}
}

// TestAutoSaveAfterEvent_DisabledIsInert pins the off switch: with
// auto-save disabled, no amount of editing may arm a timer.
func TestAutoSaveAfterEvent_DisabledIsInert(t *testing.T) {
	a, _ := autoSaveFixture(t)
	a.autoSaveEnabled = false
	a.tabs[0].InsertRune('x')

	a.autoSaveAfterEvent()

	if a.autoSaveTimer != nil {
		t.Fatal("disabled auto-save must not arm a timer")
	}
}

// TestAutoSaveAfterEvent_NoEditNoRearm verifies the EditRev-sum
// signature actually debounces: events that don't mutate any buffer
// (mouse moves, redraw ticks) must not keep re-arming the countdown,
// or the save would never fire while the user scrolls.
func TestAutoSaveAfterEvent_NoEditNoRearm(t *testing.T) {
	a, _ := autoSaveFixture(t)
	a.tabs[0].InsertRune('x')
	a.autoSaveAfterEvent()
	a.stopAutoSave() // clear so a re-arm is observable

	a.autoSaveAfterEvent() // same EditRev sum — a no-edit event

	if a.autoSaveTimer != nil {
		t.Fatal("event without edits re-armed the timer")
	}
}

// TestHandleAutoSave_WritesDirtyTab is the core promise: the debounce
// firing writes the buffer to disk and clears Dirty, with no flash
// (the status bar would flicker on every idle pause otherwise).
func TestHandleAutoSave_WritesDirtyTab(t *testing.T) {
	a, target := autoSaveFixture(t)
	tab := a.tabs[0]
	tab.InsertString("// edited\n")
	a.statusMsg = ""

	a.handleAutoSave()

	if tab.Dirty {
		t.Fatal("auto-save should clear Dirty")
	}
	got, _ := os.ReadFile(target)
	if want := tab.Buffer.String(); string(got) != want {
		t.Fatalf("disk = %q, want buffer contents %q", got, want)
	}
	if a.statusMsg != "" {
		t.Fatalf("auto-save flashed %q, want silence", a.statusMsg)
	}
}

// TestHandleAutoSave_DefersWhileModalOpen pins the modal guard: a
// save landing mid-dialog could invalidate the question the dialog is
// asking (dirty-close's "save or discard?"), so the handler re-arms
// instead of saving.
func TestHandleAutoSave_DefersWhileModalOpen(t *testing.T) {
	a, target := autoSaveFixture(t)
	tab := a.tabs[0]
	tab.InsertString("// edited\n")
	a.modal = &confirmModal{}

	a.handleAutoSave()

	if !tab.Dirty {
		t.Fatal("auto-save must not run while a modal is open")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "package main\n" {
		t.Fatalf("disk changed under an open modal: %q", got)
	}
	if a.autoSaveTimer == nil {
		t.Fatal("deferred auto-save should re-arm the timer")
	}
}

// TestHandleAutoSave_SkipsExternallyChangedFile guards the conflict
// case: another tool wrote the file after we loaded it, so a silent
// background save would clobber that edit before the reconcile tick
// could warn. Explicit Save stays the overwrite path.
func TestHandleAutoSave_SkipsExternallyChangedFile(t *testing.T) {
	a, target := autoSaveFixture(t)
	tab := a.tabs[0]
	tab.InsertString("// edited\n")
	// Simulate an external write landing after our load: push the
	// file's mtime past the tab's recorded one.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(target, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	a.handleAutoSave()

	if !tab.Dirty {
		t.Fatal("auto-save must skip a file that changed on disk")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "package main\n" {
		t.Fatalf("auto-save clobbered an external edit: %q", got)
	}
}

// TestFocusLost_FlushesPendingAutoSave pins the trigger that makes a
// long idle window comfortable: leaving the terminal window is the
// clearest "I'm done for now" a terminal can report, so the pending save
// happens then rather than waiting out the rest of the countdown.
func TestFocusLost_FlushesPendingAutoSave(t *testing.T) {
	a, target := autoSaveFixture(t)
	tab := a.tabs[0]
	tab.InsertString("// edited\n")
	want := tab.Buffer.String()

	// Through handleEvent, so the dispatch wiring is pinned too — and
	// note the event is built as a literal: tcell.NewEventFocus leaves
	// the embedded *EventTime nil, so When() on one of these panics.
	a.handleEvent(&tcell.EventFocus{Focused: false})

	if tab.Dirty {
		t.Fatal("losing focus should have flushed the pending save")
	}
	got, _ := os.ReadFile(target)
	if string(got) != want {
		t.Fatalf("disk = %q, want the buffer %q", got, want)
	}
}

// TestFocusGained_DoesNothing pins the other half: the countdown is
// armed by edits, not by attention, so coming back has nothing pending
// that it should change.
func TestFocusGained_DoesNothing(t *testing.T) {
	a, target := autoSaveFixture(t)
	a.tabs[0].InsertString("// edited\n")

	a.handleEvent(&tcell.EventFocus{Focused: true})

	if !a.tabs[0].Dirty {
		t.Fatal("regaining focus must not write anything")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "package main\n" {
		t.Fatalf("disk changed on focus-in: %q", got)
	}
}

// TestFocusLost_RespectsDisabledToggle pins that a focus-out write IS an
// auto-save: the ≡ toggle means "don't write behind my back", which
// losing focus does not make less true.
func TestFocusLost_RespectsDisabledToggle(t *testing.T) {
	a, target := autoSaveFixture(t)
	a.autoSaveEnabled = false
	a.tabs[0].InsertString("// edited\n")

	a.handleEvent(&tcell.EventFocus{Focused: false})

	if !a.tabs[0].Dirty {
		t.Fatal("disabled auto-save must not write on focus loss")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "package main\n" {
		t.Fatalf("disk changed with auto-save off: %q", got)
	}
}

// TestFocusLost_DefersWhileModalOpen pins that the flush obeys the same
// guards rather than bypassing them — it reuses handleAutoSave precisely
// so it cannot drift from the idle path — and that the work is not lost:
// the deferral re-arms the countdown.
func TestFocusLost_DefersWhileModalOpen(t *testing.T) {
	a, target := autoSaveFixture(t)
	a.tabs[0].InsertString("// edited\n")
	a.modal = &confirmModal{}

	a.handleEvent(&tcell.EventFocus{Focused: false})

	if !a.tabs[0].Dirty {
		t.Fatal("a focus flush must not write under an open modal")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "package main\n" {
		t.Fatalf("disk changed under an open modal: %q", got)
	}
	if a.autoSaveTimer == nil {
		t.Fatal("the deferred flush must leave a countdown armed, or the work strands")
	}
}

// TestAutoSaveInterval_ResolvesConfiguredValueAndDefault pins the
// zero-value mapping. A hand-built App (every test) or a config that
// never loaded would otherwise arm time.AfterFunc(0), which fires
// immediately — auto-save on every keystroke.
func TestAutoSaveInterval_ResolvesConfiguredValueAndDefault(t *testing.T) {
	a, _ := autoSaveFixture(t)

	a.autoSaveDelay = 12 * time.Second
	if got := a.autoSaveInterval(); got != 12*time.Second {
		t.Fatalf("interval = %v, want the configured 12s", got)
	}
	a.autoSaveDelay = 0
	if got := a.autoSaveInterval(); got != defaultAutoSaveDelay {
		t.Fatalf("interval = %v, want the default %v", got, defaultAutoSaveDelay)
	}
}

// TestLoadUserConfig_AppliesAutoSaveDelay pins the app-level plumbing:
// the parsed, clamped preference has to reach the field armAutoSave
// reads, or the key is a setting that loads and does nothing.
func TestLoadUserConfig_AppliesAutoSaveDelay(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	cfgPath := filepath.Join(cfgDir, "ced", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// icons:"off" keeps Resolve from shelling out to font detection.
	if err := os.WriteFile(cfgPath, []byte(`{"icons":"off","autosavedelay":"17s"}`+"\n"), 0644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	a := newTestApp(t, t.TempDir())

	a.loadUserConfig()

	if got := a.autoSaveInterval(); got != 17*time.Second {
		t.Fatalf("interval = %v, want the configured 17s", got)
	}
}

// TestWelcomeIfQuiet_YieldsToAConfigError pins that a config typo is
// actually seen. userconfig deliberately reports an unparseable key
// rather than ignoring it, and an unconditional startup greeting
// overwrote that message — leaving the user with a setting they believe
// is in effect and nothing on screen to say it isn't.
func TestWelcomeIfQuiet_YieldsToAConfigError(t *testing.T) {
	a, _ := autoSaveFixture(t)

	// Nothing pending: the greeting is the useful thing to say.
	a.statusMsg = ""
	a.welcomeIfQuiet()
	if !strings.Contains(a.statusMsg, "Welcome") {
		t.Fatalf("status = %q, want the greeting on a quiet start", a.statusMsg)
	}

	// A config complaint outranks it.
	problem := "config: autosavedelay must be a duration like \"5s\""
	a.flash(problem)
	a.welcomeIfQuiet()
	if a.statusMsg != problem {
		t.Fatalf("status = %q, want the config error to survive", a.statusMsg)
	}
}

// TestHandleAutoSave_RunsQuietFormat closes the loop between the two
// features: an auto-save still runs the builtin Go formatter (that's
// the point of shipping them together), but through the quiet path —
// no prompt may open.
func TestHandleAutoSave_RunsQuietFormat(t *testing.T) {
	a, _ := autoSaveFixture(t)
	tab := a.tabs[0]
	tab.InsertString("// edited\n")
	calls := stubBuiltinFormatter(t, []string{"sh", "-c", "true"})

	a.handleAutoSave()

	if *calls == 0 {
		t.Fatal("auto-save should consult the builtin formatter")
	}
	if confirmOf(a) != nil {
		t.Fatal("auto-save formatting must never prompt")
	}
	// Drain the async done-event so the goroutine can't post to a
	// finalized screen after the test ends.
	if ev := waitForFormatEvent(t, a); !ev.quiet {
		t.Fatal("auto-save format run should be marked quiet")
	}
}

// TestMenuToggleAutoSave_PersistsAndPreservesConfig drives the menu
// toggle end to end: the in-memory flag flips, the choice lands in
// config.json, and hand-set keys the toggle doesn't own (icons)
// survive the rewrite.
func TestMenuToggleAutoSave_PersistsAndPreservesConfig(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	cfgPath := filepath.Join(cfgDir, "ced", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"icons":"off"}`+"\n"), 0644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	a, _ := autoSaveFixture(t)

	a.menuToggleAutoSave()

	if a.autoSaveEnabled {
		t.Fatal("toggle should flip auto-save off")
	}
	cfg, err := userconfig.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if cfg.AutoSave {
		t.Fatal("persisted config should say auto-save off")
	}
	if cfg.Icons != userconfig.IconsOff {
		t.Fatalf("icons setting lost in rewrite: got %q", cfg.Icons)
	}
}

// TestAutoSaveToggleLabel pins the menu row's action-naming rule: the
// label describes what clicking will do, not the current state.
func TestAutoSaveToggleLabel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.autoSaveEnabled = true
	if got := a.autoSaveToggleLabel(); got != "Disable auto-save" {
		t.Errorf("enabled label = %q, want Disable auto-save", got)
	}
	a.autoSaveEnabled = false
	if got := a.autoSaveToggleLabel(); got != "Enable auto-save" {
		t.Errorf("disabled label = %q, want Enable auto-save", got)
	}
}
