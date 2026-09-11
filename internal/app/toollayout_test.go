// =============================================================================
// File: internal/app/toollayout_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"path/filepath"
	"testing"

	"github.com/rohanthewiz/ced/internal/session"
)

// layoutApp is the fixture for persistence tests: a test app with the
// session preference on and sessionStatePathFn pointed at a temp file,
// so nothing here can read or write the developer's real state.json.
func layoutApp(t *testing.T) (*App, string) {
	t.Helper()
	a := newTestApp(t, t.TempDir())
	a.sessionEnabled = true
	path := filepath.Join(t.TempDir(), "state.json")
	old := sessionStatePathFn
	sessionStatePathFn = func() string { return path }
	t.Cleanup(func() { sessionStatePathFn = old })
	return a, path
}

// TestEncodeToolLayout_IsSparse pins the shape that goes to disk: a tool
// on its default edge contributes no dock entry, so a project nobody has
// rearranged writes almost nothing — which is what lets a default
// changed in a later version reach users who never touched that tool.
func TestEncodeToolLayout_IsSparse(t *testing.T) {
	a, _ := layoutApp(t)

	l := a.encodeToolLayout()
	if l == nil {
		t.Fatal("a workspace with the tree showing has something to say")
	}
	if len(l.Docks) != 0 {
		t.Errorf("Docks = %v, want empty on a default arrangement", l.Docks)
	}
	if len(l.Open) != 1 || l.Open[0] != string(toolProject) {
		t.Errorf("Open = %v, want just the project tool", l.Open)
	}

	a.moveTool(toolGit, dockRight)
	l = a.encodeToolLayout()
	if got := l.Docks[string(toolGit)]; got != string(dockRight) {
		t.Errorf("Docks[git] = %q, want right", got)
	}
	if _, stored := l.Docks[string(toolTerminal)]; stored {
		t.Error("an untouched tool should still store no dock entry")
	}
}

// TestToolLayout_RoundTrip is the feature's central promise: arrange the
// window, save, and get the same arrangement back on the next visit to
// that project.
func TestToolLayout_RoundTrip(t *testing.T) {
	a, path := layoutApp(t)
	a.gitIsRepo = true

	a.moveTool(toolGit, dockRight)
	a.showTool(toolGit)
	a.setToolWidth(toolGit, 44)
	a.resizeSidebar(26)
	a.saveToolLayout()

	// A second workspace on the same root, as a folder switch would build.
	b, _ := layoutApp(t)
	sessionStatePathFn = func() string { return path }
	b.rootDir = a.rootDir
	store, err := session.Load(path)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	e, ok := store.Find(session.Normalize(a.rootDir))
	if !ok {
		t.Fatal("the root was not recorded")
	}
	b.applyToolLayout(e.Layout)

	if got := b.toolDock(toolGit); got != dockRight {
		t.Errorf("restored git dock = %q, want right", got)
	}
	if !b.gitPanel.open {
		t.Error("git was showing when saved and should be showing now")
	}
	if got := b.toolWidth(toolGit); got != 44 {
		t.Errorf("restored git width = %d, want 44", got)
	}
	if got := b.sidebarWidth; got != 26 {
		t.Errorf("restored sidebarWidth = %d, want 26", got)
	}
}

// TestApplyToolLayout_NilIsTheDefault pins the literal ask: a project
// with no remembered layout starts with the file tree on the left and
// the editor taking the rest.
func TestApplyToolLayout_NilIsTheDefault(t *testing.T) {
	a, _ := layoutApp(t)
	a.moveTool(toolGit, dockLeft)

	a.applyToolLayout(nil)

	if got := a.toolDock(toolGit); got != dockBottom {
		t.Errorf("git docks %q after a nil restore, want its default", got)
	}
	if len(a.tools().dock) != 0 {
		t.Errorf("a nil restore left %v in the dock map", a.tools().dock)
	}
}

// TestApplyToolLayout_HiddenTreeStaysHidden pins the one tool whose
// default is OPEN: a saved layout that does not list it means the user
// deliberately hid it, and a restore that reopened it would quietly
// overrule them.
func TestApplyToolLayout_HiddenTreeStaysHidden(t *testing.T) {
	a, _ := layoutApp(t)
	a.applyToolLayout(&session.Layout{Open: []string{string(toolTerminal)}})
	if a.sidebarShown {
		t.Error("the tree was not in the saved Open list and should stay hidden")
	}
}

// TestApplyToolLayout_DegradesPerEntry pins the theme registry's rule as
// it applies here: a tool this version has never heard of, and an edge
// it has never heard of, each cost that one entry and nothing else.
func TestApplyToolLayout_DegradesPerEntry(t *testing.T) {
	a, _ := layoutApp(t)
	a.applyToolLayout(&session.Layout{
		Docks: map[string]string{
			"holodeck":         "left",      // no such tool
			string(toolGit):    "northeast", // no such edge
			string(toolGitLog): "left",      // fine
		},
		Sizes: map[string]session.ToolSize{"holodeck": {W: 40}},
		Open:  []string{"holodeck", string(toolProject)},
	})

	if got := a.toolDock(toolGit); got != dockBottom {
		t.Errorf("git took the bogus edge (%q)", got)
	}
	if got := a.toolDock(toolGitLog); got != dockLeft {
		t.Errorf("the valid entry beside the bogus ones was dropped (%q)", got)
	}
	if !a.sidebarShown {
		t.Error("the valid Open entry beside a bogus one was dropped")
	}
}

// TestApplyToolLayout_ReclampsForThisWindow pins the guard against a
// layout saved on a much wider screen: a width that would leave no
// editor is clamped on the way in, rather than being read through the
// clamp and springing back the moment the window grows.
func TestApplyToolLayout_ReclampsForThisWindow(t *testing.T) {
	a, _ := layoutApp(t)
	a.applyToolLayout(&session.Layout{
		Sizes: map[string]session.ToolSize{string(toolProject): {W: 4000}},
		Open:  []string{string(toolProject)},
	})
	if got, max := a.sidebarWidth, a.width-toolMinEditorCols; got != max {
		t.Errorf("restored sidebarWidth = %d, want it clamped to %d", got, max)
	}
	if _, _, ew, _ := a.editorRect(); ew < toolMinEditorCols {
		t.Errorf("editor kept %d columns, want >= %d", ew, toolMinEditorCols)
	}
}

// TestApplyToolLayout_RestoreDoesNotWriteBack pins the loading latch:
// restoring SHOWS each remembered tool through the ordinary verb, and
// those verbs persist — so without the latch, reading a layout would
// rewrite the state file once per tool, each time recording a
// half-restored arrangement.
func TestApplyToolLayout_RestoreDoesNotWriteBack(t *testing.T) {
	a, path := layoutApp(t)
	a.applyToolLayout(&session.Layout{Open: []string{string(toolProject)}})

	store, err := session.Load(path)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(store.Folders) != 0 {
		t.Errorf("a restore wrote %d entries, want none", len(store.Folders))
	}
	if a.toolLayoutLoading {
		t.Error("the loading latch was left armed")
	}
}

// TestSaveToolLayout_RespectsTheSessionToggle: the layout rides the same
// preference the tab list does. Off means the file is not touched at
// all, which is the whole point of that toggle.
func TestSaveToolLayout_RespectsTheSessionToggle(t *testing.T) {
	a, path := layoutApp(t)
	a.sessionEnabled = false
	a.moveTool(toolGit, dockLeft)
	a.saveToolLayout()

	store, _ := session.Load(path)
	if len(store.Folders) != 0 {
		t.Errorf("session off still wrote %d entries", len(store.Folders))
	}
}

// TestApplyToolLayout_SurvivesAnUnsizedWindow is a regression pin. A
// layout is restored during New, BEFORE the first resize event has told
// the App how many columns it has — so clamping the stored extents there
// measured them against a zero-column window and floored every one of
// them. The remembered sizes came back as minimums, which reads as the
// editor forgetting the layout it had just promised to remember.
func TestApplyToolLayout_SurvivesAnUnsizedWindow(t *testing.T) {
	a, _ := layoutApp(t)
	w, h := a.width, a.height
	a.width, a.height = 0, 0 // as New sees it

	a.applyToolLayout(&session.Layout{
		Docks: map[string]string{string(toolTerminal): string(dockLeft)},
		Sizes: map[string]session.ToolSize{
			string(toolTerminal): {W: 34},
			string(toolProject):  {W: 26},
		},
		Open: []string{string(toolTerminal)},
	})

	a.width, a.height = w, h // the first resize event arrives
	if got := a.toolWidth(toolTerminal); got != 34 {
		t.Errorf("terminal width = %d after the window was sized, want the remembered 34", got)
	}
	if got := a.sidebarWidth; got != 26 {
		t.Errorf("sidebarWidth = %d, want the remembered 26", got)
	}
}
