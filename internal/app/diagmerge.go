// =============================================================================
// File: internal/app/diagmerge.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// diagmerge.go is the one place that answers "what is wrong with this
// file?" — from every producer at once.
//
// # Why this exists
//
// ced grew three diagnostic producers at three different times, each
// wiring itself to whatever surface its author needed. The result was
// a split nobody designed: an LSP diagnostic reached the gutter, the
// tooltip, the Problems panel, next/previous-problem and the status
// counts, while a PLUGIN diagnostic reached the gutter and nothing
// else. A user could see a mark, hover it, and be told nothing —
// because the tooltip read a.lsp.diags directly and a plugin finding
// was not merely absent from it but excluded at the type level.
//
// That is a bad bargain for any producer, and it was fatal for the
// validator: a syntax error whose MESSAGE cannot be read is half an
// answer. "Line 12 is broken" without "mapping values are not allowed
// here" sends the reader hunting for something the editor already knew.
//
// So every consumer that wants a file's diagnostics asks HERE, and the
// producers stay ignorant of who is reading them.
//
// # Why lsp.Diagnostic is the currency
//
// It is the richest of the three shapes and the only one every surface
// already speaks, so adopting it changed the consumers' types not at
// all. Its own doc comment anticipated this — "Raw is the object
// exactly as it arrived, or nil for a diagnostic this client built
// itself" — and Source is the field that names which producer spoke,
// which is what lets a tooltip say where an answer came from.
//
// # The one consumer that must NOT ask here
//
// lspcodeaction.go's diagsForRange still reads a.lsp.diags directly,
// and that is deliberate. A code-action request echoes the diagnostics
// back to the server VERBATIM, because their server-private `data` and
// `code` fields are how a quick fix finds the problem it fixes. A
// synthetic diagnostic has no Raw and means nothing to gopls; handing
// it one would at best be ignored and at worst confuse the matching
// that makes "fix this error" work. TestDiagsForRange_StaysOnLSPOnly
// pins it.

package app

import (
	"sort"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
	"github.com/rohanthewiz/ced/internal/plugins"
)

// validateSourceName is what ced calls itself in a diagnostic's Source
// field. Short and lowercase like a server name ("gopls", "eslint"),
// because that is the column it appears in.
const validateSourceName = "ced"

// diagsFor returns every diagnostic any producer has for path, in a
// stable order: LSP first, then plugins, then ced's own.
//
// Stable is the operative word. The Problems panel lists these, the
// tooltip stacks them, and next/previous-problem walks them — so an
// order that varied between two identical frames would make a row jump
// under the user's cursor. Go randomises map iteration, which is
// exactly how that would happen: plugin findings live in a map keyed by
// provider, so its keys are SORTED before the walk rather than ranged
// over directly.
//
// LSP leads because on a file that has a language server it is the
// authoritative and usually the only answer; the other two are extras
// layered on top.
func (a *App) diagsFor(path string) []lsp.Diagnostic {
	if path == "" {
		return nil
	}
	out := append([]lsp.Diagnostic(nil), a.lsp.diags[path]...)
	out = append(out, a.pluginDiagsFor(path)...)
	out = append(out, a.validateDiagsFor(path)...)
	return out
}

// pluginDiagsFor converts a file's plugin findings into the common
// shape. Returns nothing while the plugin kill switch is off — the
// switch is honoured at every surface, not just at load, and a mark
// that left the gutter must leave the tooltip with it.
func (a *App) pluginDiagsFor(path string) []lsp.Diagnostic {
	byKey := a.plugins.decos[path]
	if len(byKey) == 0 || !a.plugins.enabled {
		return nil
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	t := a.tabForPath(path)
	var out []lsp.Diagnostic
	for _, k := range keys {
		for _, d := range byKey[k] {
			out = append(out, lsp.Diagnostic{
				Range:    pluginDiagLSPRange(t, d),
				Severity: lspSeverityFor(d.Severity),
				Source:   k,
				Message:  d.Message,
			})
		}
	}
	return out
}

// validateDiagsFor converts ced's own syntax findings. Goes through
// liveProblems, so the revision check cannot be bypassed: a finding
// whose columns no longer describe the buffer is not reported here any
// more than it is painted.
func (a *App) validateDiagsFor(path string) []lsp.Diagnostic {
	t := a.tabForPath(path)
	probs := a.liveProblems(t)
	if len(probs) == 0 {
		return nil
	}
	out := make([]lsp.Diagnostic, 0, len(probs))
	for _, p := range probs {
		start, end := validateProblemRange(t, p)
		out = append(out, lsp.Diagnostic{
			Range: lsp.Range{
				Start: lspPosFor(t, start),
				End:   lspPosFor(t, end),
			},
			Severity: lsp.SeverityError,
			Source:   validateSourceName,
			Message:  p.Message,
		})
	}
	return out
}

// pluginDiagLSPRange projects a plugin finding onto an LSP range,
// reusing pluginDiagRange so the tooltip describes exactly the cells
// the gutter underlined. Two implementations of "what does this
// finding cover" would let the mark and the message disagree about
// which token is broken.
//
// With no open tab there is no buffer to measure a word against, so the
// range degrades to the whole line from the reported column. The
// Problems panel lists files that have no tab, and a location is worth
// more than a precise span there.
func pluginDiagLSPRange(t *editor.Tab, d plugins.Diagnostic) lsp.Range {
	if t == nil {
		col := max(d.Col, 0)
		return lsp.Range{
			Start: lsp.Position{Line: d.Line, Character: col},
			End:   lsp.Position{Line: d.Line, Character: col + 1},
		}
	}
	// lspPosFor (lsp.go) re-encodes the rune column as UTF-16, which is
	// not decoration: every consumer runs editorPosFor on the way back
	// out and decodes Character as UTF-16. A synthetic diagnostic
	// carrying a raw rune column would be silently shifted on any line
	// holding an astral-plane rune — an emoji, which a JSON string is
	// perfectly entitled to contain — and the underline would land a
	// cell or two off. A confident wrong answer, not a missing one.
	start, end := pluginDiagRange(t, d)
	return lsp.Range{Start: lspPosFor(t, start), End: lspPosFor(t, end)}
}

// lspSeverityFor maps a plugin severity onto the protocol's numbering,
// which is what every consumer of the merged list compares against.
func lspSeverityFor(sev plugins.Severity) int {
	switch sev {
	case plugins.SevError:
		return lsp.SeverityError
	case plugins.SevWarn:
		return lsp.SeverityWarning
	default:
		return lsp.SeverityInfo
	}
}

// diagPathsWithFindings returns every path any producer has something
// to say about. The Problems panel walks this instead of ranging over
// a.lsp.diags, so a file whose only finding came from a plugin or from
// ced's own parser still gets its rows.
//
// Sorted, for the ordering reason in diagsFor's comment.
func (a *App) diagPathsWithFindings() []string {
	seen := make(map[string]bool, len(a.lsp.diags)+len(a.plugins.decos))
	for p := range a.lsp.diags {
		seen[p] = true
	}
	if a.plugins.enabled {
		for p := range a.plugins.decos {
			seen[p] = true
		}
	}
	for p := range a.validate.probs {
		seen[p] = true
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}
