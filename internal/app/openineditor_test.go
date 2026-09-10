// =============================================================================
// File: internal/app/openineditor_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/filetree"
)

// withEditorEnv states $VISUAL / $EDITOR for one test. newTestApp pins
// them empty, so every test that wants an editor has to say so — which
// is what keeps the tree's conditional row deterministic on any
// developer's machine.
func withEditorEnv(t *testing.T, visual, editor string) {
	t.Helper()
	prev := editorEnv
	editorEnv = func(key string) string {
		switch key {
		case "VISUAL":
			return visual
		case "EDITOR":
			return editor
		}
		return ""
	}
	t.Cleanup(func() { editorEnv = prev })
}

// TestEditorCommand_VisualBeatsEditor pins the convention's own answer to
// which variable wins: $VISUAL is what you set when a full-screen program
// is welcome, $EDITOR is the line-editor fallback. Opening a pane is the
// full-screen case.
func TestEditorCommand_VisualBeatsEditor(t *testing.T) {
	withEditorEnv(t, "nvim", "ed")
	if got := editorCommand(); got != "nvim" {
		t.Fatalf("editorCommand() = %q, want the $VISUAL value", got)
	}
	withEditorEnv(t, "", "vim")
	if got := editorCommand(); got != "vim" {
		t.Fatalf("editorCommand() = %q, want the $EDITOR fallback", got)
	}
	withEditorEnv(t, "   ", "  vim  ")
	if got := editorCommand(); got != "vim" {
		t.Fatalf("editorCommand() = %q — a blank $VISUAL is not a setting", got)
	}
	withEditorEnv(t, "", "")
	if got := editorCommand(); got != "" {
		t.Fatalf("editorCommand() = %q, want empty", got)
	}
}

// TestEditorDisplayName_IsTheNameNotTheCommandLine pins the label rule:
// the row says what will happen ("Open in nvim"), and the flags a user
// exported are noise in a popup row. A command line is a perfectly
// ordinary $EDITOR value, so it must not be treated as a program name.
func TestEditorDisplayName_IsTheNameNotTheCommandLine(t *testing.T) {
	for _, c := range []struct{ env, want string }{
		{"vim", "vim"},
		{"/usr/local/bin/nvim -u NONE", "nvim"},
		{"ced --wait", "ced"},
		{"emacsclient -nw", "emacsclient"},
	} {
		withEditorEnv(t, c.env, "")
		if got := editorDisplayName(); got != c.want {
			t.Errorf("editorDisplayName() for %q = %q, want %q", c.env, got, c.want)
		}
	}
}

// TestOpenInEditorLabel_NamesTheEditorOrTheVariable covers the ≡ row's
// two states. With an editor the label says what the row does; without
// one it names what to set — which is the only useful thing a dimmed row
// can say, and the reason this row dims where the tree's vanishes.
func TestOpenInEditorLabel_NamesTheEditorOrTheVariable(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.openInEditorLabel(); got != "Open in $EDITOR" {
		t.Fatalf("label with no editor = %q", got)
	}
	if a.hasOpenInEditor() {
		t.Fatal("the ≡ row should be disabled with no editor configured")
	}
	withEditorEnv(t, "helix", "")
	if got := a.openInEditorLabel(); got != "Open in helix" {
		t.Fatalf("label = %q, want the editor named", got)
	}
	if !a.hasOpenInEditor() {
		t.Fatal("the ≡ row should be enabled — the root is always a target")
	}
}

// TestOpenInEditor_Tier0StagesRatherThanRuns pins the Tier-0 half, and
// it is structural rather than careful: ced's terminal panel is a REPL
// strip, explicitly not a pty, so a full-screen editor cannot run in it.
// What it CAN do is put the command where the user can see it, edit it
// and decide.
func TestOpenInEditor_Tier0StagesRatherThanRuns(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "notes.md")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	withEditorEnv(t, "nvim", "")

	a.openInEditor(file)

	staged := a.term.input.String()
	if !strings.HasPrefix(staged, "nvim ") {
		t.Fatalf("staged line = %q, want it to start with the editor", staged)
	}
	// ABSOLUTE, because ced's terminal has its own working directory
	// (grsh's cd moves it) and a relative path would silently mean
	// somewhere else.
	if !strings.Contains(staged, file) {
		t.Fatalf("staged line = %q, want the absolute path %q", staged, file)
	}
	if !a.term.open || !a.term.focused {
		t.Fatal("staging should open and focus the terminal panel")
	}
	if !strings.Contains(a.statusMsg, "Enter") {
		t.Fatalf("statusMsg = %q, want it to say the command is staged", a.statusMsg)
	}
}

// TestOpenInEditor_RefusesWithNothingConfigured keeps the one refusal
// honest. It names BOTH variables, because a user who set $EDITOR and
// expected it to be read deserves to know $VISUAL exists rather than to
// wonder why nothing happened.
func TestOpenInEditor_RefusesWithNothingConfigured(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)

	a.openInEditor(root)

	if a.term.open {
		t.Fatal("nothing should have been staged")
	}
	if !strings.Contains(a.statusMsg, "$VISUAL") || !strings.Contains(a.statusMsg, "$EDITOR") {
		t.Fatalf("statusMsg = %q, want both variables named", a.statusMsg)
	}
}

// TestOpenInEditorTarget_FallsBackToTheRoot pins the ≡ row's target. The
// fallback is not a consolation: `code .` on the project root is one of
// the two things people actually want from this row, so an editor with
// no file open must still have something to hand over.
func TestOpenInEditorTarget_FallsBackToTheRoot(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a.go")
	if err := os.WriteFile(file, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	if got := a.openInEditorTarget(); got != a.rootDir {
		t.Fatalf("target with no tab = %q, want the root", got)
	}
	a.openFile(file)
	if got := a.openInEditorTarget(); got != file {
		t.Fatalf("target = %q, want the active file %q", got, file)
	}
}

// TestTreeContext_EditorRowAppearsOnlyWhenThereIsOne pins the popup's
// conditional-row rule. Its fixed vocabulary is something users learn
// positions in, so a permanently dimmed row that could only ever say
// "$EDITOR isn't set" would be worse than its absence — and the row must
// appear on files AND directories, since `vim .` means something.
func TestTreeContext_EditorRowAppearsOnlyWhenThereIsOne(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)

	hasEditorRow := func(n *filetree.Node) bool {
		a.openTreeContext(n, 5, 5)
		defer a.closeModal()
		for _, it := range contextOf(a).items {
			if strings.HasPrefix(it.label, "Open in ") {
				return true
			}
		}
		return false
	}

	file, dir := a.tree.Root.Children[1], a.tree.Root.Children[0]
	if file.IsDir {
		file, dir = dir, file
	}
	if hasEditorRow(file) || hasEditorRow(dir) || hasEditorRow(a.tree.Root) {
		t.Fatal("no editor configured — the row must not be offered")
	}

	withEditorEnv(t, "", "vim")
	if !hasEditorRow(file) {
		t.Error("a file should offer the editor row")
	}
	if !hasEditorRow(dir) {
		t.Error("a directory should offer it too — `vim .` means something")
	}
	if !hasEditorRow(a.tree.Root) {
		t.Error("the root is the most useful target of all")
	}
}
