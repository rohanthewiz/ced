// =============================================================================
// File: internal/app/diagmerge_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
	"github.com/rohanthewiz/ced/internal/plugins"
)

// TestDiagsFor_MergesEveryProducer is the headline: one question, every
// answer. Before the seam existed a plugin's finding reached the gutter
// and nothing else, and ced's own would have done the same.
func TestDiagsFor_MergesEveryProducer(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "conf.json", "{\n  \"a\": 1,\n}\n")
	a.plugins.enabled = true
	a.plugins.decos = map[string]map[string][]plugins.Diagnostic{
		tab.Path: {"p/lint": {{Line: 0, Col: 0, Severity: plugins.SevWarn, Message: "plugin says"}}},
	}
	a.lsp.diags = map[string][]lsp.Diagnostic{
		tab.Path: {{Range: lsp.Range{Start: lsp.Position{Line: 0}}, Severity: lsp.SeverityError, Source: "gopls", Message: "server says"}},
	}

	got := a.diagsFor(tab.Path)
	if len(got) != 3 {
		t.Fatalf("diagsFor returned %d diagnostics, want 3 (one per producer): %+v", len(got), got)
	}
	// LSP leads, then plugins, then ced's own — a stable order, because
	// the Problems panel lists these and rows must not shuffle.
	wantSources := []string{"gopls", "p/lint", validateSourceName}
	for i, want := range wantSources {
		if got[i].Source != want {
			t.Errorf("diagsFor[%d].Source = %q, want %q", i, got[i].Source, want)
		}
	}
}

// TestDiagsFor_IsStableAcrossCalls pins the ordering guarantee against
// Go's randomised map iteration. Plugin findings live in a map keyed by
// provider, so an unsorted walk would reorder rows between two
// otherwise identical frames — and a row that jumps under the cursor is
// how a list becomes unusable.
func TestDiagsFor_IsStableAcrossCalls(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "x.go", "package main\n")
	a.plugins.enabled = true
	a.plugins.decos = map[string]map[string][]plugins.Diagnostic{
		tab.Path: {
			"p/zebra": {{Line: 0, Col: 0, Message: "z"}},
			"p/alpha": {{Line: 0, Col: 0, Message: "a"}},
			"p/mango": {{Line: 0, Col: 0, Message: "m"}},
			"p/beta":  {{Line: 0, Col: 0, Message: "b"}},
		},
	}
	first := sourcesOf(a.diagsFor(tab.Path))
	for i := 0; i < 20; i++ {
		if got := sourcesOf(a.diagsFor(tab.Path)); got != first {
			t.Fatalf("order changed between calls: %q then %q", first, got)
		}
	}
}

// TestDiagsFor_HonoursThePluginKillSwitch pins that the switch is
// honoured at every surface, not just at load. A mark that left the
// gutter must leave the tooltip and the Problems panel with it.
func TestDiagsFor_HonoursThePluginKillSwitch(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "x.go", "package main\n")
	a.plugins.decos = map[string]map[string][]plugins.Diagnostic{
		tab.Path: {"p/lint": {{Line: 0, Col: 0, Message: "hi"}}},
	}

	a.plugins.enabled = false
	if got := a.diagsFor(tab.Path); len(got) != 0 {
		t.Errorf("diagsFor = %+v with plugins off, want none", got)
	}
	a.plugins.enabled = true
	if got := a.diagsFor(tab.Path); len(got) != 1 {
		t.Errorf("diagsFor = %+v with plugins on, want one", got)
	}
}

// TestDiagsFor_RespectsTheRevisionGate pins that the merge seam cannot
// be used to sneak past the validator's staleness rule. liveProblems is
// the single read path precisely so a future consumer can't forget it.
func TestDiagsFor_RespectsTheRevisionGate(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "conf.json", "{\n  \"a\": 1,\n}\n")
	if len(a.diagsFor(tab.Path)) != 1 {
		t.Fatal("expected ced's finding before the edit")
	}

	tab.InsertRune('x')
	if got := a.diagsFor(tab.Path); len(got) != 0 {
		t.Errorf("diagsFor = %+v against a moved buffer, want none", got)
	}
}

// TestDiagTooltip_ReadsEveryProducer is the reason the seam was built.
// A syntax error whose MESSAGE cannot be read is half an answer: the
// underline says line 2 is broken, and only the tooltip can say why.
func TestDiagTooltip_ReadsEveryProducer(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "conf.json", "{\n  \"a\": 1,\n}\n")
	// Park the caret on the broken line — the tooltip answers for the
	// whole line when the cursor isn't inside a range.
	tab.MoveCursorTo(editor.Position{Line: 2, Col: 0}, false)

	lines := a.diagLinesAtCaret()
	if len(lines) == 0 {
		t.Fatal("diagLinesAtCaret returned nothing; the underline would be mute")
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "invalid character") {
		t.Errorf("tooltip = %q, want the parser's own wording", joined)
	}
}

// TestDiagTooltip_ShowsPluginFindings pins the gap this seam closed on
// the way past. A plugin mark was previously visible in the gutter and
// unreadable everywhere — hovering it said nothing at all.
func TestDiagTooltip_ShowsPluginFindings(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "x.go", "package main\nfunc broken() {}\n")
	a.plugins.enabled = true
	a.plugins.decos = map[string]map[string][]plugins.Diagnostic{
		tab.Path: {"p/vet": {{Line: 1, Col: 5, Severity: plugins.SevError, Message: "vet found something"}}},
	}
	tab.MoveCursorTo(editor.Position{Line: 1, Col: 5}, false)

	joined := strings.Join(a.diagLinesAtCaret(), "\n")
	if !strings.Contains(joined, "vet found something") {
		t.Errorf("tooltip = %q, want the plugin's message", joined)
	}
}

// TestProblemsPanel_ListsEveryProducer pins the second consumer. A file
// whose only finding came from ced's own parser still belongs in the
// list — and in next/previous-problem, which walks the same rows.
func TestProblemsPanel_ListsEveryProducer(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "conf.json", "{\n  \"a\": 1,\n}\n")

	if !a.hasAnyDiagnostics() {
		t.Error("hasAnyDiagnostics = false with a broken JSON file open")
	}
	rows := a.buildProblemRows()
	if len(rows) != 1 {
		t.Fatalf("buildProblemRows = %d rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].path != tab.Path {
		t.Errorf("row path = %q, want %q", rows[0].path, tab.Path)
	}
	if rows[0].src != validateSourceName {
		t.Errorf("row source = %q, want %q", rows[0].src, validateSourceName)
	}
	if rows[0].msg == "" {
		t.Error("row carries no message")
	}
}

// TestDiagCounts_IncludeEveryProducer pins the status bar. The counts
// and the marks on screen have to describe the same set, or the bar
// reads as undercounting what the user can plainly see.
func TestDiagCounts_IncludeEveryProducer(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openScratch(t, a, "conf.json", "{\n  \"a\": 1,\n}\n")

	errs, _, _ := a.diagCounts()
	if errs != 1 {
		t.Errorf("diagCounts errors = %d, want 1", errs)
	}
}

// TestDiagsForRange_StaysOnLSPOnly is the trap this whole change had to
// avoid, and it is pinned deliberately.
//
// A code-action request echoes diagnostics back to the server VERBATIM,
// because their server-private `data` and `code` fields are how a quick
// fix finds the problem it fixes. A diagnostic ced synthesised has no
// Raw and means nothing to gopls; handing it one would at best be
// ignored and at worst confuse that matching. So diagsForRange must
// keep reading a.lsp.diags directly, even though every other consumer
// moved to the merge seam.
func TestDiagsForRange_StaysOnLSPOnly(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "conf.json", "{\n  \"a\": 1,\n}\n")
	a.plugins.enabled = true
	a.plugins.decos = map[string]map[string][]plugins.Diagnostic{
		tab.Path: {"p/lint": {{Line: 0, Col: 0, Message: "plugin"}}},
	}
	raw := json.RawMessage(`{"message":"real","data":{"fix":1}}`)
	a.lsp.diags = map[string][]lsp.Diagnostic{
		tab.Path: {{
			Range:   lsp.Range{Start: lsp.Position{Line: 0}, End: lsp.Position{Line: 0, Character: 1}},
			Message: "real",
			Source:  "gopls",
			Raw:     raw,
		}},
	}

	// The whole document, so nothing is excluded for merely not
	// overlapping — the only reason to drop a row here is provenance.
	whole := lsp.Range{Start: lsp.Position{Line: 0}, End: lsp.Position{Line: 3}}
	got := a.diagsForRange(tab.Path, whole)
	if len(got) != 1 {
		t.Fatalf("diagsForRange returned %d, want exactly the 1 server diagnostic: %+v", len(got), got)
	}
	if got[0].Source != "gopls" {
		t.Errorf("diagsForRange returned a %q diagnostic; only the server's may go back on the wire", got[0].Source)
	}
	if len(got[0].Raw) == 0 {
		t.Error("the echoed diagnostic lost its Raw bytes, which is what a quick fix matches on")
	}
}

// TestValidateDiags_ColumnsRoundTripThroughUTF16 pins the conversion
// that makes the merge seam safe.
//
// lsp.Diagnostic's Character is defined in UTF-16 code units, and every
// consumer decodes it back with editorPosFor. A synthetic diagnostic
// carrying a raw RUNE column would therefore be silently shifted on any
// line holding an astral-plane rune — an emoji, which a JSON string is
// perfectly entitled to contain. This walks the full round trip and
// checks it lands back where the validator put it.
func TestValidateDiags_ColumnsRoundTripThroughUTF16(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	// The emoji is ONE rune but TWO UTF-16 code units, so a missing
	// conversion shows up as a one-column drift and nothing else.
	tab := openScratch(t, a, "conf.json", "{\n  \"k\": \"😀\", bad\n}\n")

	probs := a.liveProblems(tab)
	if len(probs) != 1 {
		t.Fatalf("liveProblems = %v, want one finding", probs)
	}
	wantStart, _ := validateProblemRange(tab, probs[0])

	diags := a.validateDiagsFor(tab.Path)
	if len(diags) != 1 {
		t.Fatalf("validateDiagsFor = %+v, want one", diags)
	}
	// editorPosFor is exactly what every consumer runs on the way out.
	back := editorPosFor(tab, diags[0].Range.Start)
	if back != wantStart {
		t.Errorf("round trip landed at %+v, want %+v — the UTF-16 conversion is wrong", back, wantStart)
	}
	// And prove the test would catch a raw-rune-column regression: past
	// the emoji the two encodings genuinely differ.
	if diags[0].Range.Start.Character == wantStart.Col {
		t.Errorf("UTF-16 column (%d) equals the rune column (%d); the fixture no longer exercises the conversion",
			diags[0].Range.Start.Character, wantStart.Col)
	}
}

// sourcesOf joins a diagnostic list's sources so an order can be
// compared as one value.
func sourcesOf(ds []lsp.Diagnostic) string {
	parts := make([]string, len(ds))
	for i, d := range ds {
		parts[i] = d.Source
	}
	return strings.Join(parts, ",")
}
