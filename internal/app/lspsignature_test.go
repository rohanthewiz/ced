// =============================================================================
// File: internal/app/lspsignature_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/lsp"
)

// TestSignatureTipLines_EmphasisMarksActiveParam pins the feature
// itself. Without the emphasis this verb is hover on the enclosing
// function, which the user could already get by moving the cursor — so
// the run has to land exactly on the active parameter's runes.
func TestSignatureTipLines_EmphasisMarksActiveParam(t *testing.T) {
	label := "Fprintf(w io.Writer, format string, a ...any)"
	start := strings.Index(label, "format string")
	sig := lsp.Signature{
		Label:      label,
		ParamStart: start,
		ParamEnd:   start + len("format string"),
		Count:      1,
	}
	lines, emph := signatureTipLines(sig, 80)
	if len(lines) != 1 || lines[0] != label {
		t.Fatalf("lines = %q, want the label on one row", lines)
	}
	if len(emph) != 1 {
		t.Fatalf("emph = %+v, want one run", emph)
	}
	got := string([]rune(lines[0])[emph[0].start:emph[0].end])
	if got != "format string" {
		t.Errorf("emphasised %q, want %q", got, "format string")
	}
}

// TestSignatureTipLines_EmphasisSurvivesWrap pins the reason the label
// is HARD-wrapped: only an exact rune-per-column mapping lets an offset
// become a (row, column) by division. A parameter straddling a break
// gets a run on each row rather than half of it going dark.
func TestSignatureTipLines_EmphasisSurvivesWrap(t *testing.T) {
	// Width 10, so the label breaks every 10 runes; the active parameter
	// spans offsets 8..14 and therefore both sides of the first break.
	sig := lsp.Signature{
		Label:      "abcdefghXXXXXXijklmnop",
		ParamStart: 8,
		ParamEnd:   14,
		Count:      1,
	}
	lines, emph := signatureTipLines(sig, 10)
	if len(lines) != 3 {
		t.Fatalf("lines = %q, want 3 hard-wrapped rows", lines)
	}
	var got string
	for _, e := range emph {
		got += string([]rune(lines[e.line])[e.start:e.end])
	}
	if got != "XXXXXX" {
		t.Errorf("emphasised %q across %d runs, want the whole parameter", got, len(emph))
	}
	if len(emph) != 2 {
		t.Errorf("runs = %d, want one per wrapped row", len(emph))
	}
}

// TestSignatureTipLines_NoActiveParam pins the quiet case: a server that
// can't say which parameter applies still gets its signature shown, with
// nothing lit. Half an answer beats none.
func TestSignatureTipLines_NoActiveParam(t *testing.T) {
	sig := lsp.Signature{Label: "f(a int)", ParamStart: -1, ParamEnd: -1, Count: 1}
	lines, emph := signatureTipLines(sig, 40)
	if len(lines) != 1 || lines[0] != "f(a int)" {
		t.Errorf("lines = %q, want the bare signature", lines)
	}
	if emph != nil {
		t.Errorf("emph = %+v, want none", emph)
	}
}

// TestSignatureTipLines_OverloadCounter pins that the counter appears
// only when there is a set to be positioned in — a "1 of 1" is noise,
// and Go (the language ced ships a server for) has no overloads at all.
func TestSignatureTipLines_OverloadCounter(t *testing.T) {
	single, _ := signatureTipLines(lsp.Signature{Label: "f()", ParamStart: -1, Count: 1}, 40)
	for _, ln := range single {
		if strings.Contains(ln, "of") {
			t.Errorf("lines = %q, want no counter for a lone signature", single)
		}
	}
	many, _ := signatureTipLines(lsp.Signature{Label: "f()", ParamStart: -1, Index: 1, Count: 3}, 40)
	if !containsLine(many, "(2 of 3)") {
		t.Errorf("lines = %q, want a 1-based overload counter", many)
	}
}

// TestSignatureTipLines_ParamDocBeatsSignatureDoc pins the ordering: the
// active parameter's documentation is the answer to the question that
// made the user press the key, and the tooltip is capped, so it must not
// be the part that gets cut.
func TestSignatureTipLines_ParamDocBeatsSignatureDoc(t *testing.T) {
	sig := lsp.Signature{
		Label: "f(a int)", ParamStart: 2, ParamEnd: 7, Count: 1,
		ParamDoc: "PARAMDOC the count of things",
		Doc:      "SIGDOC what f does",
	}
	lines, _ := signatureTipLines(sig, 60)
	pi, si := lineIndexContaining(lines, "PARAMDOC"), lineIndexContaining(lines, "SIGDOC")
	if pi < 0 || si < 0 {
		t.Fatalf("lines = %q, want both docs present", lines)
	}
	if pi > si {
		t.Errorf("parameter doc at row %d, signature doc at %d — want the parameter first", pi, si)
	}
}

// TestFirstParagraph pins the doc trimming: a Go doc comment leads with
// the sentence that says what the function does, so taking the opening
// paragraph beats truncating mid-sentence at a line count.
func TestFirstParagraph(t *testing.T) {
	got := firstParagraph("Does the thing.\nOn two lines.\n\nDetails nobody\nreads from a tooltip.")
	if got != "Does the thing.\nOn two lines." {
		t.Errorf("firstParagraph = %q, want the opening paragraph", got)
	}
	if got := firstParagraph("  only one  "); got != "only one" {
		t.Errorf("single paragraph = %q, want it trimmed", got)
	}
}

// TestCapLines pins that a cut is MARKED. An unmarked truncation reads
// as the server having said less than it did.
func TestCapLines(t *testing.T) {
	if got := capLines([]string{"a", "b"}, 5); len(got) != 2 {
		t.Errorf("under the cap = %q, want it untouched", got)
	}
	got := capLines([]string{"a", "b", "c", "d"}, 2)
	if len(got) != 3 || got[2] != "…" {
		t.Errorf("capLines = %q, want two rows plus the ellipsis", got)
	}
}

// TestHardWrap pins the exactness the emphasis mapping rests on: every
// rune survives, including the whitespace a word-wrapper would collapse.
func TestHardWrap(t *testing.T) {
	got := hardWrap("ab  cd ef", 4)
	want := []string{"ab  ", "cd e", "f"}
	if len(got) != len(want) {
		t.Fatalf("hardWrap = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hardWrap = %q, want %q", got, want)
		}
	}
	if got := hardWrap("", 4); len(got) != 1 || got[0] != "" {
		t.Errorf("hardWrap(\"\") = %q, want one empty row", got)
	}
}

// TestMenuSignatureHelp_FlushesAndRequests pins the request side. The
// flush matters more here than for any other verb: a server that hasn't
// been told about the '(' the user just typed will report that they are
// not inside a call at all.
func TestMenuSignatureHelp_FlushesAndRequests(t *testing.T) {
	a, fake, goPath := newLSPTestApp(t)
	a.openFile(goPath)
	a.activeTabPtr().InsertRune('(')

	a.menuSignatureHelp()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		calls := fake.callLog()
		if len(calls) > 0 && calls[len(calls)-1] == "signatureHelp:main.go" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	calls := fake.callLog()
	if len(calls) < 3 {
		t.Fatalf("calls = %v, want didOpen + didChange + signatureHelp", calls)
	}
	if calls[1] != "didChange:main.go:2" {
		t.Errorf("calls[1] = %q, want the pre-request flush", calls[1])
	}
	if calls[2] != "signatureHelp:main.go" {
		t.Errorf("calls[2] = %q, want signatureHelp", calls[2])
	}
}

// TestHandleLSPSignature_OpensTooltip pins the landing: the shared
// caret-anchored tooltip, carrying the emphasis that makes this verb
// different from hover.
func TestHandleLSPSignature_OpensTooltip(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	a.openFile(goPath)

	a.handleLSPSignature(&lspSignatureEvent{
		when: time.Now(), path: goPath,
		sig: &lsp.Signature{Label: "f(a int)", ParamStart: 2, ParamEnd: 7, Count: 1},
	})
	m, ok := a.modal.(*hoverModal)
	if !ok {
		t.Fatalf("modal = %T, want *hoverModal", a.modal)
	}
	if len(m.emph) != 1 {
		t.Fatalf("emph = %+v, want the active parameter marked", m.emph)
	}
}

// TestHandleLSPSignature_NoAnswerFlashes pins the empty case. "here" is
// load-bearing in the message: the usual reason is a cursor outside any
// call, which is a fact about the POSITION, and a bare "no signature
// help" reads as a fact about the server.
func TestHandleLSPSignature_NoAnswerFlashes(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	a.openFile(goPath)

	a.handleLSPSignature(&lspSignatureEvent{when: time.Now(), path: goPath})
	if a.modal != nil {
		t.Fatalf("nil signature opened %T", a.modal)
	}
	if !strings.Contains(a.statusMsg, "No signature help here") {
		t.Errorf("status = %q, want the position-scoped message", a.statusMsg)
	}

	// A signature whose label is blank is the same non-answer wearing a
	// struct — it would draw an empty box.
	a.handleLSPSignature(&lspSignatureEvent{
		when: time.Now(), path: goPath, sig: &lsp.Signature{Label: "   "},
	})
	if a.modal != nil {
		t.Errorf("blank label opened %T", a.modal)
	}
}

// TestHandleLSPSignature_DropsOtherTab pins the staleness rule hover
// keeps: a parameter list anchored to a caret in a different file
// describes nothing.
func TestHandleLSPSignature_DropsOtherTab(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	a.openFile(goPath)

	a.handleLSPSignature(&lspSignatureEvent{
		when: time.Now(), path: goPath + ".other",
		sig: &lsp.Signature{Label: "f()", ParamStart: -1, Count: 1},
	})
	if a.modal != nil {
		t.Errorf("response for another tab opened %T", a.modal)
	}
}

// TestSignatureHelp_MenuAndLeaderAgree pins the two surfaces against
// each other — the ≡ hint column is display-only, so a rebind that
// updated one would leave the menu naming a key that does something
// else.
func TestSignatureHelp_MenuAndLeaderAgree(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if sc := menuItemByLabel(t, a, "Signature help").shortcut; sc != "esc I" {
		t.Errorf("menu shortcut = %q, want %q", sc, "esc I")
	}
	found := false
	for _, b := range leaderBindings() {
		if b.key == 'I' {
			found = true
		}
	}
	if !found {
		t.Error("no leader binding on 'I'")
	}
}

// containsLine reports whether any row equals s exactly.
func containsLine(lines []string, s string) bool {
	return slices.Contains(lines, s)
}

// lineIndexContaining returns the first row index holding sub, or -1.
func lineIndexContaining(lines []string, sub string) int {
	for i, ln := range lines {
		if strings.Contains(ln, sub) {
			return i
		}
	}
	return -1
}
