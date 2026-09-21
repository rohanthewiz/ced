// =============================================================================
// File: internal/app/lspinlay.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// lspinlay.go is inlay hints — inferred types and parameter names — shown
// as end-of-line notes. WHY end-of-line rather than inside the line is
// editor/linenote.go's whole header; this file is the request, the
// refresh policy, and the one piece of real logic: turning a hint that
// was designed to be read IN PLACE into one that still makes sense read
// at the end of the line.
//
//	dispatch tail ──► inlayAfterEvent ── synced? not yet asked at this rev?
//	                                          │
//	                  goroutine ── textDocument/inlayHint (whole document)
//	                                          │
//	   Tab.SetLineNotes ◄── lspInlayEvent ◄───┘   pinned to (path, EditRev)
//
// The decisions worth spelling out:
//
//   - A HINT LOSES ITS ANCHOR WHEN IT MOVES, SO THE NOTE RESTATES IT. In
//     place, `: int` needs no subject — it sits against `x`. At the end of
//     the line it would be a bare `: int` about nothing. So a type hint is
//     prefixed with the word it followed (`x: int`) and a parameter hint
//     is suffixed with the argument it preceded (`level: 3`). inlayNote is
//     that reconstruction, and it is a pure function of the line's text.
//   - IT RIDES THE EXISTING SYNC, WITH NO TIMER OF ITS OWN. A request is
//     only worth making once the server has the text on screen, and
//     lsp.syncedRev already says when that is: the dispatch-tail check
//     fires the first time the active tab is in sync at a revision nobody
//     has asked about. The LSP debounce is therefore this feature's
//     debounce too, and an idle editor arms nothing (the caret-blink
//     constraint).
//   - ONE ASK PER (path, revision), recorded BEFORE the answer arrives and
//     kept whatever the answer was. Without that an empty or failed answer
//     would be re-requested on every event forever. A server that answers
//     with an ERROR is not asked again at all — "method not found" does
//     not get better — and a server finishing its load clears the record,
//     because a cold server's empty answer was not its real one.
//   - THE ACTIVE TAB ONLY, the whole document. Whole-document because a
//     window-scoped request would have to re-ask on every scroll, and
//     scrolling must stay free; capped by line count so a generated
//     ten-thousand-liner costs nothing rather than a little.
//   - Hints have to be switched ON in some servers (gopls ships with all
//     of them off), which is what lspServerDef.initOptions is for.

package app

import (
	"sort"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// inlayMaxLines is the document size past which hints are not requested.
const inlayMaxLines = 8000

// inlayArgMaxRunes caps the argument text restated beside a parameter
// name; a long expression is cut rather than doubling the line.
const inlayArgMaxRunes = 18

// lspInlayEvent carries one document's hints to the main loop.
type lspInlayEvent struct {
	when  time.Time
	path  string
	rev   int // Tab.EditRev the request was made at
	hints []lsp.InlayHint
	err   error
}

// When satisfies the tcell.Event interface.
func (e *lspInlayEvent) When() time.Time { return e.when }

// inlayAfterEvent runs in the dispatch tail and asks for the active
// tab's hints the first time it is in sync at an unasked revision. A
// handful of compares in the common case.
func (a *App) inlayAfterEvent() {
	if !a.inlayEnabled || a.screen == nil {
		return
	}
	t := a.activeTabPtr()
	if t == nil || t.Path == "" || t.IsImage() || t.Buffer == nil {
		return
	}
	def := lspServerFor(t.Path)
	client := a.lspClientFor(t.Path)
	if def == nil || client == nil || a.lsp.server(def.id).noInlay {
		return
	}
	if _, open := a.lsp.versions[t.Path]; !open || a.lsp.syncedRev[t.Path] != t.EditRev {
		return // the server has not seen this text yet; the sync will bring us back
	}
	// Stored as rev+1 so the map's zero value means "never asked".
	if a.lsp.inlayAsked[t.Path] == t.EditRev+1 {
		return
	}
	if a.lsp.inlayAsked == nil {
		a.lsp.inlayAsked = map[string]int{}
	}
	a.lsp.inlayAsked[t.Path] = t.EditRev + 1
	if t.Buffer.LineCount() > inlayMaxLines {
		return
	}
	scr, path, rev := a.screen, t.Path, t.EditRev
	rng := lsp.Range{End: lsp.Position{Line: t.Buffer.LineCount()}}
	go func() {
		hints, err := client.InlayHints(path, rng)
		_ = scr.PostEvent(&lspInlayEvent{when: time.Now(), path: path, rev: rev, hints: hints, err: err})
	}()
}

// handleLSPInlay turns an answer into end-of-line notes on the tab it
// was asked about. A buffer that moved drops it — the next sync asks
// again.
func (a *App) handleLSPInlay(e *lspInlayEvent) {
	if e.err != nil {
		// Almost always "method not found". Stop asking this server.
		if def := lspServerFor(e.path); def != nil {
			a.lsp.server(def.id).noInlay = true
		}
		return
	}
	t := a.tabByPath(e.path)
	if t == nil || t.EditRev != e.rev || !a.inlayEnabled {
		return
	}
	byLine := map[int][]lsp.InlayHint{}
	for _, h := range e.hints {
		byLine[h.Pos.Line] = append(byLine[h.Pos.Line], h)
	}
	notes := make(map[int]string, len(byLine))
	for line, hs := range byLine {
		if line < 0 || line >= t.Buffer.LineCount() {
			continue
		}
		if note := inlayNote(t.Buffer.LineRunes(line), hs); note != "" {
			notes[line] = note
		}
	}
	t.SetLineNotes(notes)
}

// inlayNote renders one line's hints as a single end-of-line remark,
// left to right, restating what each hint was anchored to.
func inlayNote(runes []rune, hints []lsp.InlayHint) string {
	sort.SliceStable(hints, func(i, j int) bool { return hints[i].Pos.Character < hints[j].Pos.Character })
	parts := make([]string, 0, len(hints))
	seen := map[string]bool{}
	for _, h := range hints {
		col := lsp.RuneCol(runes, h.Pos.Character)
		var part string
		switch {
		case h.Kind == lsp.InlayParameter:
			part = strings.TrimSpace(h.Label + " " + inlayArgAt(runes, col))
		case h.Kind == lsp.InlayType || strings.HasPrefix(h.Label, ":"):
			part = inlayTypePart(inlayWordBefore(runes, col), h.Label)
		default:
			part = h.Label
		}
		// Two identical remarks on one line (`a, b := f()` both int64
		// still differ by subject; `f(x, x)` does not) say it once.
		if part != "" && !seen[part] {
			seen[part] = true
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " · ")
}

// inlayTypePart joins a subject and a type label as `subject: type`.
// Servers disagree about who supplies the colon — rust-analyzer's label
// is ": i32", gopls' is a bare "float64" with a padding flag — so it is
// normalised here rather than trusted. No subject leaves the label alone.
func inlayTypePart(subject, label string) string {
	// The blank identifier's type is a fact nobody asked about — the code
	// just said it is being thrown away.
	if subject == "_" {
		return ""
	}
	typ := strings.TrimSpace(strings.TrimPrefix(label, ":"))
	if subject == "" || typ == "" {
		return label
	}
	return subject + ": " + typ
}

// inlayWordBefore is the identifier ending at col — the subject a type
// hint was written against. WordRange's after-the-word courtesy is
// exactly this lookup.
func inlayWordBefore(runes []rune, col int) string {
	if col < 0 || col > len(runes) {
		return ""
	}
	start, end, ok := editor.WordRange(runes, col)
	if !ok || end != col {
		return ""
	}
	return string(runes[start:end])
}

// inlayArgAt is the argument expression starting at col: up to the next
// top-level comma or the bracket that closes the call, capped. Nesting is
// counted so `f(g(a, b), c)` restates `g(a, b)` rather than `g(a`.
// Strings are not tracked — a comma inside a literal cuts the restated
// text short, which costs a few characters of a remark, not correctness.
func inlayArgAt(runes []rune, col int) string {
	if col < 0 || col >= len(runes) {
		return ""
	}
	depth := 0
	end := col
scan:
	for end < len(runes) {
		switch runes[end] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				break scan
			}
			depth--
		case ',':
			if depth == 0 {
				break scan
			}
		}
		end++
	}
	arg := []rune(strings.TrimSpace(string(runes[col:end])))
	if len(arg) > inlayArgMaxRunes {
		arg = append(arg[:inlayArgMaxRunes-1:inlayArgMaxRunes-1], '…')
	}
	return string(arg)
}

// inlayReask forgets which revisions were asked about, so the next
// dispatch tail asks again. Called when a server finishes loading (its
// earlier empty answer was not its real one) and when the toggle turns
// on.
func (a *App) inlayReask() { a.lsp.inlayAsked = nil }

// menuToggleInlayHints is the ≡ View row.
func (a *App) menuToggleInlayHints() {
	a.closeMenu()
	a.setInlayHints(!a.inlayEnabled)
}

// setInlayHints is the single write path for the preference: state,
// screen, config. Off takes the notes off every tab NOW rather than at
// their next edit.
func (a *App) setInlayHints(on bool) {
	a.inlayEnabled = on
	a.inlayReask()
	if on {
		a.flash("Inlay hints on")
	} else {
		for _, t := range a.tabs {
			t.SetLineNotes(nil)
		}
		a.flash("Inlay hints off")
	}
	if err := userconfig.SaveInlayHints(userconfig.DefaultPath(), on); err != nil {
		a.flash("config: " + err.Error())
	}
}

// inlayHintsToggleLabel names the row by what clicking it does.
func (a *App) inlayHintsToggleLabel() string {
	if a.inlayEnabled {
		return "Hide inlay hints"
	}
	return "Show inlay hints"
}
