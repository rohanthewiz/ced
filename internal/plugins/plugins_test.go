// =============================================================================
// File: internal/plugins/plugins_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePlugin drops a manifest into <root>/<dir>/plugin.json and
// returns the plugin directory, so each test reads as the manifest it
// is about rather than as filesystem plumbing.
func writePlugin(t *testing.T, root, dir, body string) string {
	t.Helper()
	pdir := filepath.Join(root, dir)
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pdir, ManifestName), []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return pdir
}

// TestLoadDir_MissingIsSilent pins the common case: a user with no
// plugins directory must get no plugins AND no errors. Every
// integration in this codebase degrades silently when it isn't
// configured, and a startup message about a directory the user never
// created is exactly the nagging that contract exists to prevent.
func TestLoadDir_MissingIsSilent(t *testing.T) {
	got, errs := LoadDir(filepath.Join(t.TempDir(), "nope"))
	if got != nil {
		t.Errorf("plugins = %v, want nil", got)
	}
	if errs != nil {
		t.Errorf("errs = %v, want nil", errs)
	}
	// An unresolved config location behaves the same way.
	if got, errs := LoadDir(""); got != nil || errs != nil {
		t.Errorf(`LoadDir("") = %v, %v, want nil, nil`, got, errs)
	}
}

// TestLoadDir_SkipsNonPluginDirs pins that a directory without a
// manifest is not an error. Users keep scripts, notes, and scratch
// folders next to their plugins; only plugin.json makes a directory a
// plugin.
func TestLoadDir_SkipsNonPluginDirs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scratch"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	writePlugin(t, root, "real", `{"commands":[{"label":"Go","command":"true"}]}`)

	got, errs := LoadDir(root)
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none", errs)
	}
	if len(got) != 1 || got[0].Name != "real" {
		t.Fatalf("plugins = %+v, want just \"real\"", got)
	}
}

// TestLoadDir_BadPluginCostsOnlyItself is the per-plugin degradation
// rule (the theme registry's). A plugin is somebody else's code — a
// typo in one must never take out the ones that work.
func TestLoadDir_BadPluginCostsOnlyItself(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "broken", `{"commands":[`)
	writePlugin(t, root, "fine", `{"commands":[{"label":"Go","command":"true"}]}`)

	got, errs := LoadDir(root)
	if len(got) != 1 || got[0].Name != "fine" {
		t.Fatalf("plugins = %+v, want just \"fine\"", got)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly one", errs)
	}
	if !strings.Contains(errs[0].Error(), "broken") {
		t.Errorf("error %q should name the offending plugin", errs[0])
	}
}

// TestLoadDir_SortedByName pins stable ordering. The menu row, the
// palette entry, and the leader table all derive from this slice, so a
// readdir-order shuffle between runs would move rows out from under
// the user's muscle memory.
func TestLoadDir_SortedByName(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "zulu", `{"commands":[{"label":"Z","command":"true"}]}`)
	writePlugin(t, root, "alpha", `{"commands":[{"label":"A","command":"true"}]}`)
	writePlugin(t, root, "mike", `{"commands":[{"label":"M","command":"true"}]}`)

	got, _ := LoadDir(root)
	var names []string
	for _, p := range got {
		names = append(names, p.Name)
	}
	want := []string{"alpha", "mike", "zulu"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", names, want)
	}
}

// TestLoadFile_DefaultsAndDir covers the smallest working manifest: no
// name (falls back to the directory), no input/output (fall back to
// none), no id (falls back to the label). Dir must be stamped from
// discovery so $PLUGIN_DIR points somewhere real.
func TestLoadFile_DefaultsAndDir(t *testing.T) {
	root := t.TempDir()
	pdir := writePlugin(t, root, "tiny", `{"commands":[{"label":"Run it","command":"echo hi"}]}`)

	p, err := LoadFile(filepath.Join(pdir, ManifestName))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if p.Name != "tiny" {
		t.Errorf("Name = %q, want %q (directory fallback)", p.Name, "tiny")
	}
	if p.Dir != pdir {
		t.Errorf("Dir = %q, want %q", p.Dir, pdir)
	}
	if len(p.Commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(p.Commands))
	}
	c := p.Commands[0]
	if c.ID != "Run it" {
		t.Errorf("ID = %q, want the label as fallback", c.ID)
	}
	if c.Input != InputNone || c.Output != OutputNone {
		t.Errorf("modes = %q/%q, want %q/%q", c.Input, c.Output, InputNone, OutputNone)
	}
}

// TestLoadFile_DropsHalfWrittenEntries pins the "drop it" half of the
// validation split: a command missing a label or a command line is an
// unfinished edit, and refusing the whole file over one would cost the
// user every working entry beside it.
func TestLoadFile_DropsHalfWrittenEntries(t *testing.T) {
	root := t.TempDir()
	pdir := writePlugin(t, root, "p", `{"commands":[
		{"label":"","command":"true"},
		{"label":"No command","command":"  "},
		{"label":"Good","command":"true"}
	]}`)

	p, err := LoadFile(filepath.Join(pdir, ManifestName))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(p.Commands) != 1 || p.Commands[0].Label != "Good" {
		t.Fatalf("commands = %+v, want just \"Good\"", p.Commands)
	}
}

// TestLoadFile_RejectsMisconfiguration pins the "reject the file" half:
// anything that would LOAD and then misbehave gets named at load time
// instead of turning into a menu row that quietly does the wrong thing.
func TestLoadFile_RejectsMisconfiguration(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "replace with no input",
			body: `{"commands":[{"label":"X","command":"true","output":"replace"}]}`,
			want: "no range to replace",
		},
		{
			name: "unknown input",
			body: `{"commands":[{"label":"X","command":"true","input":"buffer"}]}`,
			want: "unknown input",
		},
		{
			name: "unknown output",
			body: `{"commands":[{"label":"X","command":"true","output":"shout"}]}`,
			want: "unknown output",
		},
		{
			name: "multi-rune leader",
			body: `{"commands":[{"label":"X","command":"true","leader":"pp"}]}`,
			want: "exactly one character",
		},
		{
			name: "duplicate command id",
			body: `{"commands":[
				{"id":"a","label":"X","command":"true"},
				{"id":"a","label":"Y","command":"true"}]}`,
			want: "duplicate command id",
		},
		{
			name: "hook with no events",
			body: `{"hooks":[{"command":"true"}]}`,
			want: "on is required",
		},
		{
			name: "hook with unknown event",
			body: `{"hooks":[{"on":["close"],"command":"true"}]}`,
			want: "unknown event",
		},
		{
			name: "hook wanting a cursor",
			body: `{"hooks":[{"on":["save"],"command":"true","output":"insert"}]}`,
			want: "not valid for a hook",
		},
		{
			name: "decoration without id",
			body: `{"decorations":[{"on":["save"],"command":"true"}]}`,
			want: "id is required",
		},
		{
			name: "duplicate decoration id",
			body: `{"decorations":[
				{"id":"d","on":["save"],"command":"true"},
				{"id":"d","on":["open"],"command":"true"}]}`,
			want: "duplicate decoration id",
		},
		{
			name: "bad glob",
			body: `{"hooks":[{"on":["save"],"glob":"[","command":"true"}]}`,
			want: "bad glob",
		},
		{
			name: "bad prompt key",
			body: `{"commands":[{"label":"X","command":"true",
				"prompts":[{"key":"lower","label":"L","type":"text"}]}]}`,
			want: "must match",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			pdir := writePlugin(t, root, "p", tc.body)
			_, err := LoadFile(filepath.Join(pdir, ManifestName))
			if err == nil {
				t.Fatalf("want an error mentioning %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestLoadFile_FullManifest walks a manifest exercising every section
// at once, which is the shape the documentation shows — if this drifts,
// the docs are lying.
func TestLoadFile_FullManifest(t *testing.T) {
	root := t.TempDir()
	pdir := writePlugin(t, root, "kitchen", `{
		"name": "prettier",
		"description": "Format through prettier",
		"commands": [
			{"id":"sel","label":"Prettier: selection","leader":"p",
			 "input":"selection","output":"replace","command":"prettier"}
		],
		"hooks": [
			{"on":["save"],"glob":"*.ts","command":"eslint --fix \"$FILE\"","output":"reload"}
		],
		"decorations": [
			{"id":"todo","on":["open","save","edit"],"command":"grep -n TODO \"$FILE\""}
		]
	}`)

	p, err := LoadFile(filepath.Join(pdir, ManifestName))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if p.Name != "prettier" {
		t.Errorf("Name = %q, want the declared name to beat the directory", p.Name)
	}
	if r, ok := p.Commands[0].LeaderRune(); !ok || r != 'p' {
		t.Errorf("LeaderRune = %q/%v, want 'p'/true", r, ok)
	}
	if p.Hooks[0].Output != OutputReload {
		t.Errorf("hook output = %q, want %q", p.Hooks[0].Output, OutputReload)
	}
	if len(p.Decorations[0].On) != 3 {
		t.Errorf("decoration events = %v, want all three", p.Decorations[0].On)
	}
}

// TestMatches covers the event + glob gate both hooks and providers
// run through. The glob is matched against the BASE NAME, which is the
// reading a user writing "*.go" expects — matching the full path would
// make that pattern silently never fire.
func TestMatches(t *testing.T) {
	h := Hook{On: []Event{EventSave}, Glob: "*.go"}
	if !h.Matches(EventSave, "main.go") {
		t.Error("save on main.go should match")
	}
	if h.Matches(EventOpen, "main.go") {
		t.Error("open should not match a save-only hook")
	}
	if h.Matches(EventSave, "main.rs") {
		t.Error("*.go should not match main.rs")
	}

	// An empty glob is "every file", which is what a hook that only
	// declares events means.
	all := Provider{ID: "x", On: []Event{EventOpen, EventEdit}}
	if !all.Matches(EventEdit, "anything.xyz") {
		t.Error("empty glob should match every file")
	}
	if all.Matches(EventSave, "anything.xyz") {
		t.Error("an undeclared event must not fire")
	}
}

// TestLeaderRune_Unbound pins the no-leader case — most commands have
// none, and the caller distinguishes that from a bound key rather than
// comparing against a zero rune.
func TestLeaderRune_Unbound(t *testing.T) {
	if r, ok := (Command{}).LeaderRune(); ok {
		t.Errorf("LeaderRune = %q/%v, want unbound", r, ok)
	}
}
