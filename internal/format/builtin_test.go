// =============================================================================
// File: internal/format/builtin_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-09
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package format

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// stubLookPath swaps the package's PATH resolver for one that only
// knows the given tools, restoring the real resolver when the test
// ends. Keys are tool names, values the fake resolved binary paths.
func stubLookPath(t *testing.T, tools map[string]string) {
	t.Helper()
	orig := lookPath
	lookPath = func(name string) (string, error) {
		if p, ok := tools[name]; ok {
			return p, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { lookPath = orig })
}

// TestBuiltinCommandsFor_PrefersGoimports pins the preference order:
// when everything is installed, goimports alone must win because it
// formats AND fixes imports in one pass — no chain needed.
func TestBuiltinCommandsFor_PrefersGoimports(t *testing.T) {
	stubLookPath(t, map[string]string{
		"goimports": "/fake/bin/goimports",
		"gopls":     "/fake/bin/gopls",
		"gofmt":     "/fake/bin/gofmt",
	})
	cmds := BuiltinCommandsFor("", "/proj/main.go")
	if len(cmds) != 1 {
		t.Fatalf("cmds = %v, want single goimports command", cmds)
	}
	argv := cmds[0]
	if len(argv) != 3 || argv[0] != "/fake/bin/goimports" || argv[1] != "-w" {
		t.Fatalf("argv = %v, want [/fake/bin/goimports -w <file>]", argv)
	}
	if !filepath.IsAbs(argv[2]) {
		t.Errorf("file arg %q should be absolute", argv[2])
	}
}

// TestBuiltinCommandsFor_GoplsChain covers the increasingly common
// machine that has gopls (for the LSP) but never installed the
// standalone goimports: import fixing must not silently vanish. The
// pipeline is `gopls imports -w` for the imports, then `gofmt -w` for
// the formatting goimports would otherwise have applied.
func TestBuiltinCommandsFor_GoplsChain(t *testing.T) {
	stubLookPath(t, map[string]string{
		"gopls": "/fake/bin/gopls",
		"gofmt": "/fake/bin/gofmt",
	})
	cmds := BuiltinCommandsFor("", "/proj/main.go")
	if len(cmds) != 2 {
		t.Fatalf("cmds = %v, want gopls-imports + gofmt chain", cmds)
	}
	if cmds[0][0] != "/fake/bin/gopls" || cmds[0][1] != "imports" || cmds[0][2] != "-w" {
		t.Fatalf("cmds[0] = %v, want [gopls imports -w <file>]", cmds[0])
	}
	if cmds[1][0] != "/fake/bin/gofmt" || cmds[1][1] != "-w" {
		t.Fatalf("cmds[1] = %v, want [gofmt -w <file>]", cmds[1])
	}
}

// TestBuiltinCommandsFor_GoplsOnly pins the gopls-without-gofmt case
// (a machine with a gopls binary but no Go toolchain dir on PATH):
// import fixing still runs alone rather than being dropped.
func TestBuiltinCommandsFor_GoplsOnly(t *testing.T) {
	stubLookPath(t, map[string]string{"gopls": "/fake/bin/gopls"})
	cmds := BuiltinCommandsFor("", "/proj/main.go")
	if len(cmds) != 1 || cmds[0][0] != "/fake/bin/gopls" || cmds[0][1] != "imports" {
		t.Fatalf("cmds = %v, want lone gopls imports command", cmds)
	}
}

// TestBuiltinCommandsFor_FallsBackToGofmt covers the machine that has
// a Go toolchain but neither goimports nor gopls — formatting should
// still happen, just without import management.
func TestBuiltinCommandsFor_FallsBackToGofmt(t *testing.T) {
	stubLookPath(t, map[string]string{"gofmt": "/fake/bin/gofmt"})
	cmds := BuiltinCommandsFor("", "/proj/main.go")
	if len(cmds) != 1 || cmds[0][0] != "/fake/bin/gofmt" {
		t.Fatalf("cmds = %v, want gofmt fallback", cmds)
	}
}

// TestBuiltinCommandsFor_NoToolsIsNil pins silent degradation: no Go
// tools on PATH means no builtin formatting and no error — the save
// must behave exactly as it did before this feature existed.
func TestBuiltinCommandsFor_NoToolsIsNil(t *testing.T) {
	stubLookPath(t, nil)
	if cmds := BuiltinCommandsFor("", "/proj/main.go"); cmds != nil {
		t.Fatalf("cmds = %v, want nil when nothing is installed", cmds)
	}
}

// TestBuiltinCommandsFor_NonGoIsNil ensures the external rung only
// fires for the kinds ced actually knows. Everything else stays on the
// opt-in format.json path, prompts and all.
func TestBuiltinCommandsFor_NonGoIsNil(t *testing.T) {
	stubLookPath(t, map[string]string{
		"goimports": "/fake/bin/goimports",
		"gofmt":     "/fake/bin/gofmt",
	})
	for _, p := range []string{"/proj/notes.txt", "/proj/main.py", "/proj/Makefile", "/proj/go"} {
		if cmds := BuiltinCommandsFor("", p); cmds != nil {
			t.Errorf("BuiltinCommandsFor(%q) = %v, want nil", p, cmds)
		}
	}
}

// -----------------------------------------------------------------------------
// JSON — the second kind on the external rung.
// -----------------------------------------------------------------------------

// TestBuiltinCommandsFor_JSONPrefersPrettier pins the preference order
// for JSON. prettier leads because it is by a wide margin the most
// commonly installed and the one a project is likeliest to have pinned.
func TestBuiltinCommandsFor_JSONPrefersPrettier(t *testing.T) {
	stubLookPath(t, map[string]string{
		"prettier": "/fake/bin/prettier",
		"biome":    "/fake/bin/biome",
		"deno":     "/fake/bin/deno",
	})
	cmds := BuiltinCommandsFor("", "/proj/data.json")
	if len(cmds) != 1 {
		t.Fatalf("cmds = %v, want exactly one command", cmds)
	}
	want := []string{"/fake/bin/prettier", "--write", "/proj/data.json"}
	if !equalArgv(cmds[0], want) {
		t.Errorf("cmds[0] = %v, want %v", cmds[0], want)
	}
}

// TestBuiltinCommandsFor_JSONFallsDownTheList pins that each formatter
// is reached when the ones above it are missing, with the argv each
// tool actually needs to rewrite in place.
func TestBuiltinCommandsFor_JSONFallsDownTheList(t *testing.T) {
	cases := []struct {
		name  string
		tools map[string]string
		want  []string
	}{
		{
			"biome when no prettier",
			map[string]string{"biome": "/fake/bin/biome", "deno": "/fake/bin/deno"},
			[]string{"/fake/bin/biome", "format", "--write", "/proj/data.json"},
		},
		{
			"deno when nothing else",
			map[string]string{"deno": "/fake/bin/deno"},
			[]string{"/fake/bin/deno", "fmt", "/proj/data.json"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubLookPath(t, tc.tools)
			cmds := BuiltinCommandsFor("", "/proj/data.json")
			if len(cmds) != 1 {
				t.Fatalf("cmds = %v, want exactly one command", cmds)
			}
			if !equalArgv(cmds[0], tc.want) {
				t.Errorf("cmds[0] = %v, want %v", cmds[0], tc.want)
			}
		})
	}
}

// TestBuiltinCommandsFor_JSONNeverChains pins the difference from the
// Go pipeline. Each JSON formatter formats completely on its own, so
// running two would just mean the second reformatting the first one's
// output to a different house style.
func TestBuiltinCommandsFor_JSONNeverChains(t *testing.T) {
	stubLookPath(t, map[string]string{
		"prettier": "/fake/bin/prettier",
		"biome":    "/fake/bin/biome",
		"deno":     "/fake/bin/deno",
	})
	if cmds := BuiltinCommandsFor("", "/proj/data.json"); len(cmds) != 1 {
		t.Errorf("cmds = %v, want exactly one — JSON formatters do not chain", cmds)
	}
}

// TestBuiltinCommandsFor_JSONNoToolsIsNil pins the fall-through to the
// in-process rung. Nil here does NOT mean "do not format" — it means
// this rung declined, and InProcessFormat takes over. That is what
// keeps the feature working on a machine with no Node toolchain.
func TestBuiltinCommandsFor_JSONNoToolsIsNil(t *testing.T) {
	stubLookPath(t, nil)
	if cmds := BuiltinCommandsFor("", "/proj/data.json"); cmds != nil {
		t.Fatalf("cmds = %v, want nil so the in-process rung runs", cmds)
	}
}

// TestBuiltinCommandsFor_JSONCIsUntouched pins the carve-out at the
// command layer too. A tsconfig.json is JSON with comments; handing it
// to prettier would be defensible, but ced has declared it none of its
// business, and all three verbs must agree on that.
func TestBuiltinCommandsFor_JSONCIsUntouched(t *testing.T) {
	stubLookPath(t, map[string]string{"prettier": "/fake/bin/prettier"})
	for _, p := range []string{"/proj/tsconfig.json", "/proj/.vscode/settings.json"} {
		if cmds := BuiltinCommandsFor("", p); cmds != nil {
			t.Errorf("BuiltinCommandsFor(%q) = %v, want nil", p, cmds)
		}
	}
}

// TestResolveTool_PrefersTheRepoCopy is the point of taking a rootDir
// at all. A prettier pinned in the repo's node_modules is a statement
// BY THIS REPO about how it formats; one on $PATH is a statement about
// the developer's laptop, which may have nothing to do with the
// project being edited.
func TestResolveTool_PrefersTheRepoCopy(t *testing.T) {
	root := t.TempDir()
	local := writeFakeTool(t, root, "prettier", 0o755)
	stubLookPath(t, map[string]string{"prettier": "/global/bin/prettier"})

	cmds := BuiltinCommandsFor(root, filepath.Join(root, "data.json"))
	if len(cmds) != 1 {
		t.Fatalf("cmds = %v, want one command", cmds)
	}
	if cmds[0][0] != local {
		t.Errorf("used %q, want the repo-local %q", cmds[0][0], local)
	}
}

// TestResolveTool_SkipsANonExecutableLocalCopy pins the guard against a
// half-finished npm install. A dangling or non-executable .bin entry
// must fall through to $PATH rather than turning a missing dependency
// into a formatter error on every save.
func TestResolveTool_SkipsANonExecutableLocalCopy(t *testing.T) {
	root := t.TempDir()
	writeFakeTool(t, root, "prettier", 0o644) // present, but not runnable
	stubLookPath(t, map[string]string{"prettier": "/global/bin/prettier"})

	cmds := BuiltinCommandsFor(root, filepath.Join(root, "data.json"))
	if len(cmds) != 1 {
		t.Fatalf("cmds = %v, want one command", cmds)
	}
	if cmds[0][0] != "/global/bin/prettier" {
		t.Errorf("used %q, want the $PATH copy", cmds[0][0])
	}
}

// TestResolveTool_EmptyRootSkipsTheLocalLookup pins that a caller with
// no project root (tests, and any future rootless entry point) still
// gets the $PATH answer rather than a path built from "".
func TestResolveTool_EmptyRootSkipsTheLocalLookup(t *testing.T) {
	stubLookPath(t, map[string]string{"prettier": "/global/bin/prettier"})
	if bin, ok := resolveTool("", "prettier"); !ok || bin != "/global/bin/prettier" {
		t.Errorf("resolveTool(\"\", prettier) = (%q, %v), want the $PATH copy", bin, ok)
	}
}

// writeFakeTool creates <root>/node_modules/.bin/<name> with the given
// mode and returns its path.
func writeFakeTool(t *testing.T, root, name string, mode os.FileMode) string {
	t.Helper()
	dir := filepath.Join(root, "node_modules", ".bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	// WriteFile's perm is masked by umask, so state the mode outright —
	// the executable bit is exactly what one of these tests is about.
	if err := os.Chmod(p, mode); err != nil {
		t.Fatalf("chmod %s: %v", p, err)
	}
	return p
}

// equalArgv compares two argvs element by element.
func equalArgv(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
