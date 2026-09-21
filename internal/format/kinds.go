// =============================================================================
// File: internal/format/kinds.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// kinds.go answers ONE question — "what data language is this file, as
// far as ced's built-in handling is concerned?" — and every built-in
// surface routes through it: the external-tool table (builtin.go), the
// in-process formatter (inprocess.go) and the validator (validate.go).
//
// One table rather than three switches on filepath.Ext, because these
// three must never disagree. A file the validator calls JSON but the
// formatter does not would be flagged as broken and then left alone,
// which reads as the editor refusing to fix what it just complained
// about — and the inverse is worse: rewriting a file nobody vetted the
// syntax of.
//
// # Why JSONC is carved out by NAME
//
// `tsconfig.json` and friends are JSON with comments. They carry the
// .json extension, and no amount of parsing gets around the fact that
// encoding/json will reject `//` — correctly, since strict JSON has no
// comments. If those files were treated as JSON, ced would put a red
// underline on the first comment of a file that is doing exactly what
// its ecosystem intends, and then offer to "fix" it by refusing.
//
// So they are kindNone: ced has nothing to say about them. That is the
// honest answer, and it costs nothing — the project's own format.json
// entry still overrides everything here, which is the escape hatch for
// a team that really does want prettier on its tsconfig.

package format

import (
	"path/filepath"
	"strings"
)

// kind names a data language ced handles in-process. Deliberately not
// exported: the outside world asks the three verbs (external commands,
// in-process format, validate), never "what kind is this".
type kind int

const (
	// kindNone means ced has no built-in opinion about this file.
	// Every unknown extension lands here, which is what keeps the
	// built-in path silent on the overwhelming majority of files.
	kindNone kind = iota
	kindGo
	kindJSON
)

// jsoncNames are the .json files that are conventionally JSON **with
// comments**. Matched on the base name, case-folded, because that is
// how the convention is actually expressed — the extension says .json
// for all of them.
//
// This list is deliberately short and literal rather than a pattern. A
// heuristic ("does it contain //?") would have to read the file before
// deciding what the file is, and would misfire on a perfectly valid
// JSON string containing a URL.
var jsoncNames = map[string]bool{
	"tsconfig.json":     true,
	"jsconfig.json":     true,
	"devcontainer.json": true,
	".eslintrc.json":    true,
	".babelrc.json":     true,
}

// kindFor classifies filePath. It looks at the extension and, for
// .json, at the base name and the containing directory.
//
// The .vscode/ carve-out is a directory rule because the convention
// there is per-FOLDER, not per-file: settings.json, launch.json,
// tasks.json and keybindings.json all accept comments, and listing
// each one by name would leave the next one ced has not heard of
// getting underlined on sight.
func kindFor(filePath string) kind {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filePath), "."))
	switch ext {
	case "go":
		return kindGo
	case "json":
		base := strings.ToLower(filepath.Base(filePath))
		if jsoncNames[base] {
			return kindNone
		}
		// filepath.Dir on a bare "settings.json" yields ".", which is
		// not ".vscode" — so a file passed with no directory at all is
		// classified on its name alone, as it should be.
		if strings.EqualFold(filepath.Base(filepath.Dir(filePath)), ".vscode") {
			return kindNone
		}
		return kindJSON
	default:
		// .jsonc and .json5 are explicitly NOT kindJSON: they announce
		// in their own extension that they are a different language,
		// and falling through to kindNone means ced leaves them alone
		// rather than reporting every comment as a syntax error.
		return kindNone
	}
}
