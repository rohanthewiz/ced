// =============================================================================
// File: internal/skills/skills_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkill creates dir/<name>/SKILL.md with body and returns the
// SKILL.md path — the fixture every test here builds on.
func writeSkill(t *testing.T, dir, name, body string) string {
	t.Helper()
	sd := filepath.Join(dir, name)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(sd, FileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// TestLoadFile_Frontmatter pins the ordinary case: name and description
// come out of the frontmatter, the description is collapsed onto one
// line, and both the directory and the file path are recorded (the app
// layer needs the directory to tell an agent where the supporting files
// live).
func TestLoadFile_Frontmatter(t *testing.T) {
	root := t.TempDir()
	path := writeSkill(t, root, "run-ced", "---\nname: run-ced\ndescription: \"Drive the real binary\"\n---\n\n# Running ced\n")

	s, err := LoadFile(path, SourceProject)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if s.Name != "run-ced" {
		t.Errorf("Name = %q, want run-ced", s.Name)
	}
	if s.Description != "Drive the real binary" {
		t.Errorf("Description = %q", s.Description)
	}
	if s.Path != path || s.Dir != filepath.Dir(path) {
		t.Errorf("Path/Dir = %q / %q", s.Path, s.Dir)
	}
	if s.Source != SourceProject {
		t.Errorf("Source = %q, want project", s.Source)
	}
}

// TestLoadFile_NoFrontmatterFallsBackToDirName is the degradation rule:
// a SKILL.md whose author skipped (or mistyped) the frontmatter is still
// a usable skill, identified by its folder. Refusing it would hide a
// file the user deliberately put there.
func TestLoadFile_NoFrontmatterFallsBackToDirName(t *testing.T) {
	root := t.TempDir()
	path := writeSkill(t, root, "Bare-Skill", "# Just markdown, no frontmatter\n")

	s, err := LoadFile(path, SourceUser)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if s.Name != "bare-skill" { // lower-cased: it's the shadowing key
		t.Errorf("Name = %q, want bare-skill", s.Name)
	}
	if s.Description != "" {
		t.Errorf("Description = %q, want empty", s.Description)
	}
}

// TestLoadFile_Unreadable is the one real failure mode — a missing file
// must report, not return a half-built skill.
func TestLoadFile_Unreadable(t *testing.T) {
	if _, err := LoadFile(filepath.Join(t.TempDir(), "nope", FileName), SourceUser); err == nil {
		t.Fatal("expected an error for a missing SKILL.md")
	}
}

// TestParseFrontmatter_Shapes covers the frontmatter forms real skill
// files use, since this is a hand-rolled parser standing in for YAML:
// quoted and unquoted scalars, block scalars, wrapped continuations, and
// nested structures that must be skipped rather than mangled.
func TestParseFrontmatter_Shapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]string
	}{
		{
			name: "plain and quoted",
			in:   "---\nname: alpha\ndescription: 'quoted value'\n---\nbody",
			want: map[string]string{"name": "alpha", "description": "quoted value"},
		},
		{
			name: "folded block scalar",
			in:   "---\nname: beta\ndescription: >-\n  first line\n  second line\n---\n",
			want: map[string]string{"name": "beta", "description": "first line second line"},
		},
		{
			name: "literal block scalar",
			in:   "---\nname: gamma\ndescription: |\n  one\n  two\n---\n",
			want: map[string]string{"name": "gamma", "description": "one\ntwo"},
		},
		{
			name: "wrapped plain scalar",
			in:   "---\nname: delta\ndescription: starts here\n  and wraps\n---\n",
			want: map[string]string{"name": "delta", "description": "starts here and wraps"},
		},
		{
			name: "nested structure is skipped, siblings survive",
			in:   "---\nname: epsilon\nmetadata:\n  version: 2\n  tags:\n    - go\ndescription: kept\n---\n",
			want: map[string]string{"name": "epsilon", "description": "kept"},
		},
		{
			name: "no frontmatter",
			in:   "# heading\nname: not-frontmatter\n",
			want: map[string]string{},
		},
		{
			name: "leading blank lines and BOM",
			in:   "\ufeff\n---\nname: zeta\n---\n",
			want: map[string]string{"name": "zeta"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseFrontmatter(tc.in)
			for k, want := range tc.want {
				if got[k] != want {
					t.Errorf("field %q = %q, want %q", k, got[k], want)
				}
			}
			if tc.want["description"] == "" && got["description"] != "" {
				t.Errorf("unexpected description %q", got["description"])
			}
		})
	}
}

// TestRegistry_ShadowsByPrecedence pins the merge rule that makes a
// project skill worth checking in: same name, later directory wins, one
// row in the result — never two entries the user has to tell apart.
func TestRegistry_ShadowsByPrecedence(t *testing.T) {
	userDir, projDir := t.TempDir(), t.TempDir()
	writeSkill(t, userDir, "shared", "---\nname: shared\ndescription: personal copy\n---\n")
	writeSkill(t, userDir, "personal-only", "---\nname: personal-only\ndescription: mine\n---\n")
	writeSkill(t, projDir, "shared", "---\nname: shared\ndescription: project copy\n---\n")

	list, errs := Registry([]Dir{
		{Path: userDir, Source: SourceUser},
		{Path: projDir, Source: SourceProject},
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(list) != 2 {
		t.Fatalf("got %d skills, want 2: %+v", len(list), list)
	}
	// Sorted by name, so the shadowed entry is deterministic.
	if list[0].Name != "personal-only" || list[1].Name != "shared" {
		t.Fatalf("unsorted registry: %q, %q", list[0].Name, list[1].Name)
	}
	if list[1].Description != "project copy" || list[1].Source != SourceProject {
		t.Errorf("project skill did not shadow the personal one: %+v", list[1])
	}
}

// TestRegistry_MissingDirIsSilent is the common case — most users have
// never created two of the three directories — and a loose file or a
// folder without a SKILL.md is not an error either.
func TestRegistry_MissingDirIsSilent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "not-a-skill"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	writeSkill(t, dir, "real", "---\nname: real\n---\n")

	list, errs := Registry([]Dir{
		{Path: "", Source: SourceCed},
		{Path: filepath.Join(dir, "gone"), Source: SourceUser},
		{Path: dir, Source: SourceProject},
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(list) != 1 || list[0].Name != "real" {
		t.Fatalf("got %+v, want just the real skill", list)
	}
}

// TestFind_CaseInsensitive pins lookup by hand-typed name.
func TestFind_CaseInsensitive(t *testing.T) {
	list := []Skill{{Name: "run-ced"}}
	if _, ok := Find(list, "  RUN-CED "); !ok {
		t.Fatal("Find should match case- and whitespace-insensitively")
	}
	if _, ok := Find(list, "absent"); ok {
		t.Fatal("Find matched a skill that isn't there")
	}
}

// TestSummary_Clips keeps picker rows scannable: descriptions are
// written for a model to match on and run long, so the row form is
// capped with an ellipsis.
func TestSummary_Clips(t *testing.T) {
	s := Skill{Description: strings.Repeat("x", 200)}
	got := s.Summary(20)
	if len([]rune(got)) != 20 || !strings.HasSuffix(got, "…") {
		t.Fatalf("Summary(20) = %q (%d runes)", got, len([]rune(got)))
	}
	if short := (Skill{Description: "brief"}).Summary(20); short != "brief" {
		t.Fatalf("short description was altered: %q", short)
	}
}
