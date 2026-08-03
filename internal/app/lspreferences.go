// =============================================================================
// File: internal/app/lspreferences.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// lspreferences.go is "Find references" — every use of the symbol under
// the cursor, across the whole project, listed in the SAME panel that
// Find-all and Find-in-project use.
//
// It is the fourth code-intelligence verb, and it is deliberately the
// smallest of the four, because both halves already existed: internal/lsp
// speaks the request, and findall.go's PROJECT mode is already a
// cross-file result list with a path on every row, a strip that displaces
// the editor instead of covering it, a right dock, scrolling, an Esc
// contract and a mouse story. All this file does is join them.
//
//	Esc R / ≡ Code ──► menuFindReferences ──goroutine──► references
//	                                            │              │
//	                                            │      read each file once
//	                                            ▼              │
//	    findAllModal{project: true} ◄── lspReferencesEvent ◄────┘
//
// Two things it does NOT do, both on purpose:
//
//   - It does not preview. Project mode's rule, for project mode's
//     reason: walking rows across files would open a file per keystroke,
//     firing didOpen, plugin hooks and a syntax pass for every row merely
//     scrolled past. The row IS the preview; Enter opens.
//   - It does not build its own result list. A second cross-file list
//     would have to re-answer every question findall.go already answered,
//     and the two would drift in exactly the places (docking, truncation,
//     path labels) a user would notice.
//
// The one genuinely new problem is CONTEXT TEXT. A Location names a file
// and a range and nothing else, so the line each hit sits on has to be
// fetched — off the main loop, since it is file IO — and then reconciled
// against any tab that has the file open with unsaved edits. gopls
// answered from the text ced synced to it, so the buffer, not the disk,
// is what those coordinates were measured against.

package app

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/diff"
	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
	"github.com/rohanthewiz/ced/internal/search"
)

// maxReferences caps the result list. Well under the project search's
// ten thousand because references are a narrower question by nature: a
// symbol with two thousand uses is a symbol whose call sites nobody is
// going to read one by one, and the cap is REPORTED in the title (the
// project-search rule) so a short list never passes for a complete one.
const maxReferences = 2000

// refEndOfLine is the UTF-16 column a multi-line reference range reports
// for its end. RuneCol clamps a column past the line's end to the line
// length, so this resolves to "the rest of the line" against whichever
// text the main loop finally picks — which is the point: the goroutine
// reads disk text, the main loop may substitute a dirty buffer's line,
// and a column measured against the wrong one would light up the wrong
// cells. Only the SENTINEL survives the hop; the conversion happens once,
// against the text that is actually going to be drawn.
const refEndOfLine = 1 << 30

// refLoc is one reference plus the line it sits on as read from disk.
// Columns stay in LSP's UTF-16 units for the reason above: they are
// converted where the final text is chosen, not here.
type refLoc struct {
	path      string
	line      int
	startChar int
	endChar   int
	text      string
}

// lspReferencesEvent carries a finished references lookup back to the
// main loop. seq is the generation guard every async result in this
// codebase carries: this one OPENS A PANEL, so a stale answer would land
// on top of whatever the user asked for since.
type lspReferencesEvent struct {
	when      time.Time
	seq       int
	query     string
	locs      []refLoc
	truncated bool
	err       error
}

// When satisfies the tcell.Event interface.
func (e *lspReferencesEvent) When() time.Time { return e.when }

// menuFindReferences is the ≡ Code row and the Esc-R leader: ask the
// server who uses the symbol under the cursor.
//
// The word under the cursor is resolved BEFORE the request, and a cursor
// that isn't on one refuses outright. It is not merely a title: the panel
// tints occurrences of it in whatever file the user opens from the list,
// which is what makes a row land on something visible. And an identifier
// is what the server needs anyway — asking about a blank line is a round
// trip guaranteed to come back empty.
func (a *App) menuFindReferences() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil || !a.hasLSPActions() {
		return
	}
	word := cursorWord(t)
	if word == "" {
		a.flash("Find references: put the cursor on a symbol first")
		return
	}
	client, scr := a.lsp.client, a.screen
	if scr == nil {
		return
	}
	path, pos := t.Path, lspPosFor(t, t.Cursor)
	// The question must be asked about what is on screen — the same flush
	// the other three verbs do. Here a stale sync would be worse than a
	// wrong answer: a symbol renamed a keystroke ago would report the
	// OLD name's uses under the new name's title.
	a.lspFlushChange(t)

	a.lsp.refSeq++
	seq := a.lsp.refSeq
	a.flash(fmt.Sprintf("Finding references to %q…", word))
	go func() {
		locs, err := client.References(path, pos, true)
		refs, truncated := collectRefLines(locs)
		_ = scr.PostEvent(&lspReferencesEvent{
			when: time.Now(), seq: seq, query: word,
			locs: refs, truncated: truncated, err: err,
		})
	}()
}

// cursorWord is the identifier the cursor sits on, or "". A selection
// that spans a single line wins over the word beneath it — the narrower
// statement of intent, the rule chat attachments and Find-all seeding
// both follow.
func cursorWord(t *editor.Tab) string {
	if t == nil || t.Buffer == nil {
		return ""
	}
	if t.HasSelection() {
		// A multi-line selection is not a symbol; fall through to the word
		// under the cursor rather than titling the panel with a blob.
		if sel := t.SelectionText(); sel != "" && !strings.ContainsRune(sel, '\n') {
			return sel
		}
	}
	if t.Cursor.Line < 0 || t.Cursor.Line >= len(t.Buffer.Lines) {
		return ""
	}
	runes := []rune(t.Buffer.Lines[t.Cursor.Line])
	if start, end, ok := editor.WordRange(runes, t.Cursor.Col); ok {
		return string(runes[start:end])
	}
	return ""
}

// collectRefLines sorts, caps, and attaches the on-disk line text to a
// references response. It runs on the request goroutine because it reads
// files, and it reads each file exactly ONCE however many hits it holds —
// a symbol used forty times in one file must not cost forty reads.
//
// Sorting happens BEFORE the cap so the truncation is a prefix of the
// order the user will see, not an arbitrary forty percent of it. Order is
// path, then line, then column — the same stable order search.Project
// returns, so the two lists read alike.
//
// A file that can't be read (deleted since, too big, no permission) still
// contributes its rows, with empty text. The location is the answer; the
// line is context, and losing the context is not a reason to drop a hit
// the server is telling us about.
func collectRefLines(locs []lsp.Location) (out []refLoc, truncated bool) {
	refs := make([]refLoc, 0, len(locs))
	for _, l := range locs {
		path := lsp.URIToPath(l.URI)
		if path == "" {
			// A non-file URI (some servers report stdlib sources inside
			// archives). There is nothing here the editor could open.
			continue
		}
		end := l.Range.End.Character
		if l.Range.End.Line != l.Range.Start.Line {
			end = refEndOfLine
		}
		refs = append(refs, refLoc{
			path:      path,
			line:      l.Range.Start.Line,
			startChar: l.Range.Start.Character,
			endChar:   end,
		})
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].path != refs[j].path {
			return refs[i].path < refs[j].path
		}
		if refs[i].line != refs[j].line {
			return refs[i].line < refs[j].line
		}
		return refs[i].startChar < refs[j].startChar
	})
	if len(refs) > maxReferences {
		refs = refs[:maxReferences]
		truncated = true
	}

	var curPath string
	var curLines []string
	for i := range refs {
		if refs[i].path != curPath {
			curPath, curLines = refs[i].path, readContextLines(refs[i].path)
		}
		if refs[i].line >= 0 && refs[i].line < len(curLines) {
			refs[i].text = curLines[refs[i].line]
		}
	}
	return refs, truncated
}

// readContextLines reads one file's lines for context, or nil. The size
// ceiling is the project search's, and for its reason: a hit inside a
// generated megabyte is noise, and holding the file to render one row
// from it is worse. diff.SplitLines rather than a hand-rolled split
// because it is the one in this codebase that does NOT invent a trailing
// empty line for a file ending in a newline.
func readContextLines(path string) []string {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > search.DefaultMaxFileBytes {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return diff.SplitLines(string(data))
}

// handleLSPReferences opens the result list on the main loop.
//
// The guards are project search's, and each answers a different way for
// the request to have outlived its welcome: a stale generation is dropped
// silently, an empty answer says so rather than opening a blank panel,
// and a result arriving while a modal or the menu owns the screen reports
// the count instead of stealing the slot from something the user is
// typing into.
func (a *App) handleLSPReferences(e *lspReferencesEvent) {
	if e.seq != a.lsp.refSeq {
		return
	}
	if e.err != nil {
		a.flash("Find references: " + e.err.Error())
		return
	}
	if len(e.locs) == 0 {
		a.flash(fmt.Sprintf("No references to %q", e.query))
		return
	}
	if a.modal != nil || a.menuOpen {
		a.flash(fmt.Sprintf("Find references: %d for %q — run it again", len(e.locs), e.query))
		return
	}
	m := &findAllModal{
		query:     e.query,
		tabIdx:    -1, // no single tab behind this list
		project:   true,
		heading:   "References to",
		truncated: e.truncated,
		rows:      a.projectSearchRows(a.referenceHits(e.locs)),
	}
	a.openModal(m)
	m.ensureRowVisible(a)
}

// referenceHits turns the fetched locations into the hit shape the
// Find-all panel already renders, converting LSP's UTF-16 columns to rune
// columns against the text each row will actually show.
//
// An OPEN TAB's buffer outranks the disk copy, and that is the whole
// reason this step is on the main loop. The server answered from the text
// ced synced to it, which includes unsaved edits; rendering those hits
// against the stale on-disk line would put the highlight on the wrong
// columns of the wrong text — the one failure mode that makes a results
// list quietly lie.
func (a *App) referenceHits(refs []refLoc) []search.Hit {
	hits := make([]search.Hit, 0, len(refs))
	for _, r := range refs {
		text := r.text
		if t := a.tabByPath(r.path); t != nil && t.Buffer != nil &&
			r.line >= 0 && r.line < t.Buffer.LineCount() {
			text = t.Buffer.Lines[r.line]
		}
		runes := []rune(text)
		col := lsp.RuneCol(runes, r.startChar)
		width := lsp.RuneCol(runes, r.endChar) - col
		hits = append(hits, search.Hit{
			Path: r.path, Line: r.line, Col: col, Width: max(width, 0), Text: text,
		})
	}
	return hits
}
