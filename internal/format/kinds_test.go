// =============================================================================
// File: internal/format/kinds_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package format

import (
	"path/filepath"
	"testing"
)

// TestKindFor_ClassifiesByExtension pins the happy path: the two
// languages the built-in path knows, and the default that keeps ced
// silent about everything else.
func TestKindFor_ClassifiesByExtension(t *testing.T) {
	cases := []struct {
		path string
		want kind
	}{
		{"/proj/main.go", kindGo},
		{"/proj/data.json", kindJSON},
		{"/proj/notes.txt", kindNone},
		{"/proj/Makefile", kindNone},
		{"/proj/main.py", kindNone},
		// No extension at all — filepath.Ext returns "", which must not
		// accidentally match any table entry.
		{"/proj/go", kindNone},
	}
	for _, tc := range cases {
		if got := kindFor(tc.path); got != tc.want {
			t.Errorf("kindFor(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestKindFor_IsCaseInsensitive pins the fold on the extension. A
// Windows-authored DATA.JSON is the same language as data.json, and a
// case-sensitive table would silently skip it.
func TestKindFor_IsCaseInsensitive(t *testing.T) {
	for _, p := range []string{"/proj/DATA.JSON", "/proj/Data.Json", "/proj/MAIN.GO"} {
		if got := kindFor(p); got == kindNone {
			t.Errorf("kindFor(%q) = kindNone, want a real kind", p)
		}
	}
}

// TestKindFor_JSONCNamesAreNotJSON is the carve-out that matters most
// in practice: tsconfig.json and friends legitimately carry comments,
// so treating them as strict JSON would underline the first `//` of a
// file that is doing exactly what its ecosystem intends — and then
// refuse to format it, which reads as the editor contradicting itself.
func TestKindFor_JSONCNamesAreNotJSON(t *testing.T) {
	for _, p := range []string{
		"/proj/tsconfig.json",
		"/proj/jsconfig.json",
		"/proj/.eslintrc.json",
		"/proj/.babelrc.json",
		"/proj/devcontainer.json",
		// Case-folded too — TSConfig.json is the same file.
		"/proj/TSConfig.json",
		// And wherever it lives; the rule is the NAME, not the path.
		"/deep/nested/dir/tsconfig.json",
	} {
		if got := kindFor(p); got != kindNone {
			t.Errorf("kindFor(%q) = %v, want kindNone (JSON with comments)", p, got)
		}
	}
}

// TestKindFor_VSCodeDirIsNotJSON pins the directory half of the
// carve-out. The convention there is per-FOLDER: settings.json,
// launch.json and tasks.json all take comments, so naming each one
// would leave the next one ced has not heard of getting underlined on
// sight.
func TestKindFor_VSCodeDirIsNotJSON(t *testing.T) {
	for _, p := range []string{
		filepath.Join("/proj", ".vscode", "settings.json"),
		filepath.Join("/proj", ".vscode", "launch.json"),
		filepath.Join("/proj", ".vscode", "some-future-file.json"),
	} {
		if got := kindFor(p); got != kindNone {
			t.Errorf("kindFor(%q) = %v, want kindNone (.vscode takes comments)", p, got)
		}
	}
	// A .json elsewhere is unaffected — the carve-out must not leak
	// into every file that merely sits near a dotted directory.
	if got := kindFor(filepath.Join("/proj", ".github", "settings.json")); got != kindJSON {
		t.Errorf("kindFor(.github/settings.json) = %v, want kindJSON", got)
	}
}

// TestKindFor_JSONCExtensionIsNotJSON pins the extensions that announce
// in their own name that they are a different language. Treating .jsonc
// as JSON would report every comment in it as a syntax error.
func TestKindFor_JSONCExtensionIsNotJSON(t *testing.T) {
	for _, p := range []string{"/proj/data.jsonc", "/proj/data.json5"} {
		if got := kindFor(p); got != kindNone {
			t.Errorf("kindFor(%q) = %v, want kindNone", p, got)
		}
	}
}

// TestKindFor_BareNameHasNoDirectory pins the one subtle case in the
// .vscode rule: filepath.Dir("settings.json") is ".", not ".vscode",
// so a file passed with no directory at all must be classified on its
// name alone rather than accidentally matching the folder rule.
func TestKindFor_BareNameHasNoDirectory(t *testing.T) {
	if got := kindFor("settings.json"); got != kindJSON {
		t.Errorf("kindFor(\"settings.json\") = %v, want kindJSON", got)
	}
	if got := kindFor("tsconfig.json"); got != kindNone {
		t.Errorf("kindFor(\"tsconfig.json\") = %v, want kindNone", got)
	}
}

// TestKindFor_AgreesWithTheThreeVerbs is the whole reason this table
// exists: the external-tool rung, the in-process rung and the validator
// must never disagree about what a file IS. A file ced validates but
// will not format would be flagged broken and then left alone.
func TestKindFor_AgreesWithTheThreeVerbs(t *testing.T) {
	// A JSON file: validated, formatted in-process, and recognised by
	// the built-in path.
	const jsonPath = "/proj/data.json"
	if !Validates(jsonPath) {
		t.Error("Validates(data.json) = false, want true")
	}
	if _, ok, _ := InProcessFormat(jsonPath, []byte("{}")); !ok {
		t.Error("InProcessFormat(data.json) ok = false, want true")
	}
	if !BuiltinHandles(jsonPath) {
		t.Error("BuiltinHandles(data.json) = false, want true")
	}

	// A JSONC file: none of the three may touch it.
	const tsPath = "/proj/tsconfig.json"
	if Validates(tsPath) {
		t.Error("Validates(tsconfig.json) = true, want false")
	}
	if _, ok, _ := InProcessFormat(tsPath, []byte("{}")); ok {
		t.Error("InProcessFormat(tsconfig.json) ok = true, want false")
	}
	if BuiltinHandles(tsPath) {
		t.Error("BuiltinHandles(tsconfig.json) = true, want false")
	}
}
