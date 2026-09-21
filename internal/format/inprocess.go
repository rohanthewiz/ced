// =============================================================================
// File: internal/format/inprocess.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// inprocess.go formats a file without shelling out to anything.
//
// # Why this exists beside the external-tool table
//
// Go got built-in formatting for free because gofmt ships with the
// toolchain: anyone editing Go already has it. JSON has no such
// guarantee — prettier, biome and deno are all reasonable answers and
// NONE of them is on a typical Go developer's machine. An
// external-tool-only implementation would therefore be a feature that
// is inert on the majority of the machines it shipped to, which is the
// trap the decoration-format note warns about: a capability nobody can
// observe is indistinguishable from one that was never built.
//
// So the built-in path is a LADDER, and this is its floor:
//
//	project .ced/format.json     the repo's own answer, trust-gated
//	an installed external tool   the ecosystem's answer (builtin.go)
//	this file                    ced's answer — always available
//
// The floor is only reached when the two rungs above it decline, which
// is what keeps ced from arguing with a repo that has already said how
// it wants its JSON formatted.
//
// # Why json.Indent and not Marshal
//
// Marshalling through a map would reorder every object's keys — Go
// randomises map iteration, so two saves of one file could produce two
// different orderings — and would rewrite every number through a float
// round trip, turning `1e3` into `1000` and risking precision on large
// integers. json.Indent is a pure whitespace pass over the original
// bytes: key order, number spelling, string escapes and unicode all
// survive exactly as written. The ONLY thing that changes is layout,
// which is the only thing a formatter was asked to change.

package format

import (
	"bytes"
	"encoding/json"
)

// jsonIndent is the indentation one level of JSON nesting gets. Two
// spaces because that is what every tool in this file's ladder emits by
// default (prettier, biome, deno, `jq --indent 2`), so a project that
// later installs one of them sees no churn on its first save.
//
// Not configurable, and that is the same call the rest of ced makes:
// there is no settings dialog by design, and a project that wants
// something else says so in .ced/format.json, which overrides this
// whole path.
const jsonIndent = "  "

// InProcessFormat formats src as whatever language filePath names.
//
// It returns the formatted bytes and ok=true when ced formatted the
// file, ok=false when it has no in-process formatter for this kind —
// which the caller treats as "not my job", not as a failure. An error
// means the file IS one ced formats but does not currently parse; the
// caller must leave the file untouched, because the validator has
// already underlined the reason and rewriting unparseable text is how
// a formatter destroys work.
//
// The returned slice is always a fresh allocation, never a window onto
// src, so callers may retain either independently.
func InProcessFormat(filePath string, src []byte) (out []byte, ok bool, err error) {
	switch kindFor(filePath) {
	case kindJSON:
		out, err = formatJSON(src)
		return out, true, err
	default:
		return nil, false, nil
	}
}

// formatJSON re-indents src, leaving every token exactly as written.
func formatJSON(src []byte) ([]byte, error) {
	// An empty file formats to itself rather than to an error. It is
	// the file someone just created, and a formatter that refused to
	// run on save would flash a syntax complaint at a user who has not
	// typed anything yet. validateJSON makes the same call, so the two
	// halves never disagree about whether an empty file is broken.
	trimmed := bytes.TrimSpace(src)
	if len(trimmed) == 0 {
		return append([]byte(nil), src...), nil
	}

	// Indent is fed the TRIMMED bytes: it preserves whatever leading
	// whitespace it is handed, so passing src verbatim would indent the
	// document one level deeper on every single save — a formatter that
	// is not idempotent is a formatter that fights the file.
	var buf bytes.Buffer
	if err := json.Indent(&buf, trimmed, "", jsonIndent); err != nil {
		return nil, err
	}

	// Indent emits no trailing newline. Every POSIX text tool expects
	// one, git renders its absence as "\ No newline at end of file" on
	// every diff, and the editor's own save path writes buffers that
	// end in one. Adding it here keeps a formatted file from showing up
	// as modified the moment anything else touches it.
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// FormatsInProcess reports whether InProcessFormat would handle this
// file. Callers use it to decide whether to READ the file at all — the
// in-process pass is the last rung of the ladder, so it is asked about
// every save in the editor, and a Go file must not pay a whole-file
// read to be told "not mine".
//
// It must agree with InProcessFormat's ok result exactly; both switch
// on the same kindFor, and a test pins them together.
func FormatsInProcess(filePath string) bool {
	return kindFor(filePath) == kindJSON
}

// InProcessName names the formatter for status messages — what ced
// calls itself when it is the one doing the formatting, where the
// external rung would name a binary ("goimports", "prettier").
func InProcessName(filePath string) string {
	switch kindFor(filePath) {
	case kindJSON:
		return "json"
	default:
		return ""
	}
}
