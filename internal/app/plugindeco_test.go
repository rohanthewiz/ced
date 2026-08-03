// =============================================================================
// File: internal/app/plugindeco_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/plugins"
	"github.com/rohanthewiz/ced/internal/theme"
)

// TestPluginsOnEvent_FiresOnlyMatchingHooks pins the event + glob gate.
// A hook that fires for the wrong event or the wrong file type is worse
// than one that never fires: it runs the user's shell unasked.
func TestPluginsOnEvent_FiresOnlyMatchingHooks(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	sh := (&fakeShell{}).install(t)
	installPlugin(t, a, "p", `{"hooks":[
		{"on":["save"],"glob":"*.go","command":"go-hook"},
		{"on":["save"],"glob":"*.ts","command":"ts-hook"},
		{"on":["open"],"glob":"*.go","command":"open-hook"}
	]}`)
	tab := openScratch(t, a, "main.go", "package main\n")

	// openScratch already fired "open"; wait for that hook, then check
	// the save event fires exactly the Go save hook.
	pumpAppEvents(t, a, func() bool { return sh.count() >= 1 })
	if got := sh.last(t).command; got != "open-hook" {
		t.Errorf("open fired %q, want open-hook", got)
	}

	before := sh.count()
	a.pluginsOnEvent(plugins.EventSave, tab)
	pumpAppEvents(t, a, func() bool { return sh.count() > before })

	if got := sh.last(t).command; got != "go-hook" {
		t.Errorf("save fired %q, want go-hook", got)
	}
	if sh.count() != before+1 {
		t.Errorf("save fired %d hooks, want exactly 1", sh.count()-before)
	}
}

// TestPluginsOnEvent_KillSwitch pins that turning plugins off stops
// hooks from running at all — this is the path that actually executes a
// user's shell on every save, so "off" has to mean off here first.
func TestPluginsOnEvent_KillSwitch(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	sh := (&fakeShell{}).install(t)
	installPlugin(t, a, "p", `{"hooks":[{"on":["save"],"command":"never"}]}`)
	tab := openScratch(t, a, "x.txt", "hi\n")

	a.plugins.enabled = false
	a.pluginsOnEvent(plugins.EventSave, tab)
	if sh.count() != 0 {
		t.Errorf("a disabled plugin's hook ran (%d calls)", sh.count())
	}
}

// TestPluginDeco_ProviderMarksAreStoredAndReplaced pins the keying rule:
// a re-run replaces its OWN findings and touches nobody else's, which is
// what lets several providers watch one file.
func TestPluginDeco_ProviderMarksAreStoredAndReplaced(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := filepath.Join(a.rootDir, "x.go")

	a.handlePluginDeco(&pluginDecoEvent{
		seq: a.plugins.seq, path: path, key: "p/lint",
		diags: []plugins.Diagnostic{{Line: 1, Col: -1, Message: "first"}},
	})
	a.handlePluginDeco(&pluginDecoEvent{
		seq: a.plugins.seq, path: path, key: "p/todo",
		diags: []plugins.Diagnostic{{Line: 2, Col: -1, Message: "todo"}},
	})
	if got := len(a.plugins.decos[path]); got != 2 {
		t.Fatalf("providers = %d, want 2", got)
	}

	// A re-run of one provider replaces only its own entry.
	a.handlePluginDeco(&pluginDecoEvent{
		seq: a.plugins.seq, path: path, key: "p/lint",
		diags: []plugins.Diagnostic{{Line: 5, Col: -1, Message: "second"}},
	})
	if got := a.plugins.decos[path]["p/lint"][0].Message; got != "second" {
		t.Errorf("lint = %q, want the fresh finding", got)
	}
	if got := a.plugins.decos[path]["p/todo"][0].Message; got != "todo" {
		t.Errorf("todo = %q, the other provider must be untouched", got)
	}

	// An empty result still replaces: that's how findings disappear when
	// the user fixes them.
	a.handlePluginDeco(&pluginDecoEvent{seq: a.plugins.seq, path: path, key: "p/lint"})
	if _, still := a.plugins.decos[path]["p/lint"]; still {
		t.Error("an empty result must clear the provider's marks")
	}
	if len(a.plugins.decos[path]) != 1 {
		t.Error("the other provider should have survived")
	}
}

// TestPluginDeco_StaleGenerationDropped pins the generation check. A
// provider that no longer exists (reloaded, or switched off mid-run)
// must not paint marks nobody can explain or clear.
func TestPluginDeco_StaleGenerationDropped(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := filepath.Join(a.rootDir, "x.go")
	stale := a.plugins.seq
	a.loadPlugins() // bumps the generation

	a.handlePluginDeco(&pluginDecoEvent{
		seq: stale, path: path, key: "gone/lint",
		diags: []plugins.Diagnostic{{Line: 0, Col: -1, Message: "ghost"}},
	})
	if len(a.plugins.decos) != 0 {
		t.Errorf("stale marks were stored: %v", a.plugins.decos)
	}
}

// TestPluginDecoSource_SpansAndMarks pins what actually gets painted: an
// underline per finding and one gutter mark per line, colored by
// severity.
func TestPluginDecoSource_SpansAndMarks(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "x.go", "package main\nfunc broken() {\n\tvar unused int\n}\n")
	a.plugins.decos = map[string]map[string][]plugins.Diagnostic{
		tab.Path: {"p/vet": {
			{Line: 1, Col: 5, Severity: plugins.SevError, Message: "bad"},
			{Line: 2, Col: -1, Severity: plugins.SevInfo, Message: "note"},
		}},
	}
	th := theme.Default()
	spans, marks := pluginDecoSource{app: a}.Decorations(tab, th, 0, 3)

	if len(spans) != 2 || len(marks) != 2 {
		t.Fatalf("spans/marks = %d/%d, want 2/2", len(spans), len(marks))
	}
	// A column-carrying finding underlines the WORD there, not one cell:
	// "func broken" — col 5 starts "broken", six runes.
	var word spanRange
	for _, s := range spans {
		if s.Start.Line == 1 {
			word = spanRange{s.Start.Col, s.End.Col}
		}
	}
	if word.start != 5 || word.end != 11 {
		t.Errorf("column span = [%d,%d), want [5,11) — the whole word", word.start, word.end)
	}
	// A column-less finding underlines the line's text, skipping indent.
	for _, s := range spans {
		if s.Start.Line == 2 && s.Start.Col != 1 {
			t.Errorf("line span starts at %d, want 1 (past the tab)", s.Start.Col)
		}
	}
	for _, m := range marks {
		if m.Glyph != pluginDecoMark {
			t.Errorf("gutter glyph = %q, want %q", m.Glyph, pluginDecoMark)
		}
		if m.Line == 1 && m.FG != th.DiagError {
			t.Error("an error line should use the error color")
		}
	}
}

// spanRange is a tiny local pair so the span assertions above read as
// ranges rather than as index arithmetic.
type spanRange struct{ start, end int }

// TestPluginDecoSource_LoudestSeverityWinsTheGutter pins the one-cell
// rule: several providers can land on a line, and the mark has to show
// the most urgent of them.
func TestPluginDecoSource_LoudestSeverityWinsTheGutter(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "x.go", "package main\n")
	a.plugins.decos = map[string]map[string][]plugins.Diagnostic{
		tab.Path: {
			"p/todo": {{Line: 0, Col: -1, Severity: plugins.SevInfo}},
			"p/vet":  {{Line: 0, Col: -1, Severity: plugins.SevError}},
		},
	}
	th := theme.Default()
	_, marks := pluginDecoSource{app: a}.Decorations(tab, th, 0, 0)

	if len(marks) != 1 {
		t.Fatalf("marks = %d, want 1 — there is only one gutter cell", len(marks))
	}
	if marks[0].FG != th.DiagError {
		t.Error("the error should win the gutter cell over the info finding")
	}
}

// TestPluginDecoSource_CulledAndSilentWhenOff pins the two cheap exits:
// findings outside the visible window cost nothing, and the kill switch
// clears the paint immediately.
func TestPluginDecoSource_CulledAndSilentWhenOff(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "x.go", "a\nb\nc\nd\ne\n")
	a.plugins.decos = map[string]map[string][]plugins.Diagnostic{
		tab.Path: {"p/lint": {
			{Line: 0, Col: -1, Message: "visible"},
			{Line: 4, Col: -1, Message: "off screen"},
		}},
	}
	th := theme.Default()
	spans, _ := pluginDecoSource{app: a}.Decorations(tab, th, 0, 2)
	if len(spans) != 1 {
		t.Errorf("spans = %d, want only the one in the window", len(spans))
	}

	a.plugins.enabled = false
	if spans, marks := (pluginDecoSource{app: a}).Decorations(tab, th, 0, 4); spans != nil || marks != nil {
		t.Error("a disabled plugin still painted")
	}
}

// TestFilterDiagsForFile pins the per-file confinement. Painting another
// file's errors on this one is worse than dropping them, so anything
// that names a different file goes.
func TestFilterDiagsForFile(t *testing.T) {
	root := "/proj"
	target := "/proj/internal/app/main.go"
	in := []plugins.Diagnostic{
		{Path: "", Message: "no path — grep -n"},
		{Path: "internal/app/main.go", Message: "relative to root"},
		{Path: "/proj/internal/app/main.go", Message: "absolute"},
		{Path: "main.go", Message: "bare base name"},
		{Path: "internal/other/thing.go", Message: "a different file"},
	}
	got := filterDiagsForFile(in, target, root)
	if len(got) != 4 {
		t.Fatalf("kept %d, want 4: %+v", len(got), got)
	}
	for _, d := range got {
		if strings.Contains(d.Message, "different file") {
			t.Error("a diagnostic about another file was kept")
		}
	}
	if filterDiagsForFile(nil, target, root) != nil {
		t.Error("no diagnostics should stay nil")
	}
}

// TestPluginsWantEdit_ArmsOnlyWhenSomethingListens pins the idle-editor
// rule the caret blink is also built around: a standing timer in an
// event-driven loop wakes a resting editor forever.
func TestPluginsWantEdit_ArmsOnlyWhenSomethingListens(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	installPlugin(t, a, "saveonly", `{"hooks":[{"on":["save"],"command":"true"}]}`)
	if a.pluginsWantEdit() {
		t.Error("a save-only plugin must not arm the edit timer")
	}
	openScratch(t, a, "x.txt", "hi\n")
	a.pluginsAfterEvent()
	if a.plugins.editTimer != nil {
		t.Error("edit timer armed with nothing listening for edits")
	}

	installPlugin(t, a, "watcher", `{"decorations":[{"id":"d","on":["edit"],"command":"true"}]}`)
	if !a.pluginsWantEdit() {
		t.Fatal("an edit provider should arm the timer")
	}
	a.plugins.editRev = -1 // force the signature to look changed
	a.pluginsAfterEvent()
	if a.plugins.editTimer == nil {
		t.Error("edit timer should be armed once something listens")
	}
	a.stopPluginEditTimer()
	if a.plugins.editTimer != nil {
		t.Error("stopPluginEditTimer left a timer behind")
	}
}

// TestPluginHookDone_OutputModes pins where a hook's output goes.
// Success is silent by default because hooks run unasked on every save;
// only a failure is allowed to interrupt.
func TestPluginHookDone_OutputModes(t *testing.T) {
	t.Run("silent by default", func(t *testing.T) {
		a := newTestApp(t, t.TempDir())
		a.handlePluginHookDone(&pluginHookDoneEvent{
			seq: a.plugins.seq, label: "p", stdout: []byte("chatter\n"),
		})
		if a.statusMsg != "" {
			t.Errorf("a silent hook flashed %q", a.statusMsg)
		}
		if a.modal != nil {
			t.Errorf("a silent hook opened %T", a.modal)
		}
	})

	t.Run("empty info opens nothing", func(t *testing.T) {
		a := newTestApp(t, t.TempDir())
		a.handlePluginHookDone(&pluginHookDoneEvent{
			seq: a.plugins.seq, label: "p", output: plugins.OutputInfo,
		})
		if a.modal != nil {
			t.Error("an info hook with no output must not open an empty modal on every save")
		}
	})

	t.Run("failure is surfaced", func(t *testing.T) {
		a := newTestApp(t, t.TempDir())
		a.handlePluginHookDone(&pluginHookDoneEvent{
			seq: a.plugins.seq, label: "linter", err: errShellFailed,
		})
		if !strings.Contains(a.statusMsg, "linter") {
			t.Errorf("flash = %q, want the failing hook named", a.statusMsg)
		}
	})

	t.Run("stale generation dropped", func(t *testing.T) {
		a := newTestApp(t, t.TempDir())
		stale := a.plugins.seq
		a.loadPlugins()
		a.handlePluginHookDone(&pluginHookDoneEvent{
			seq: stale, label: "p", output: plugins.OutputFlash, stdout: []byte("late"),
		})
		if strings.Contains(a.statusMsg, "late") {
			t.Errorf("a stale hook result was surfaced: %q", a.statusMsg)
		}
	})
}

// TestPluginDecoProvider_ParsesStderrAndIgnoresExitStatus pins the two
// concessions to how real linters behave: `go vet` reports on stderr,
// and every linter worth wiring up exits non-zero exactly when it has
// something to say.
func TestPluginDecoProvider_ParsesStderrAndIgnoresExitStatus(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	(&fakeShell{
		stderr: "x.go:2:5: error: undefined: foo\n",
		err:    errShellFailed,
	}).install(t)
	installPlugin(t, a, "vet", `{"decorations":[{"id":"vet","on":["save"],"command":"go vet"}]}`)
	tab := openScratch(t, a, "x.go", "package main\nfunc a() {}\n")

	a.pluginsOnEvent(plugins.EventSave, tab)
	pumpAppEvents(t, a, func() bool { return len(a.plugins.decos[tab.Path]) > 0 })

	diags := a.plugins.decos[tab.Path]["vet/vet"]
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want the one from stderr", diags)
	}
	if diags[0].Severity != plugins.SevError || diags[0].Line != 1 {
		t.Errorf("diag = %+v, want an error on line index 1", diags[0])
	}
}

// errShellFailed stands in for a non-zero exit in tests that only care
// that the command failed, not how.
var errShellFailed = &shellExitError{}

// shellExitError is a minimal error type for the fake shell.
type shellExitError struct{}

// Error implements error.
func (*shellExitError) Error() string { return "exit status 1" }
