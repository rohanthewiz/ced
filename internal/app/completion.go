// =============================================================================
// File: internal/app/completion.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// completion.go is the LSP completion popup — the list of what could go
// here, drawn at the token you are typing, filtered as you keep typing.
//
// It is the seventh code-intelligence verb and the first one that is NOT
// A MODAL, and that single structural difference is the whole design.
// Every other verb (hover, signature help, code actions, the symbol
// picker) takes the single modal slot, which means it OWNS THE KEYBOARD;
// a completion list that owned the keyboard would swallow the very
// keystrokes it exists to narrow. So this popup lives in the same layer
// ghost text does: it draws over the editor, it reads a few keys off the
// top of the router, and everything else falls straight through to the
// buffer.
//
//	'.' typed ──► completionAfterEvent ──150ms──► completionTickEvent
//	                                                     │
//	                     flush didChange, textDocument/completion (async)
//	                                                     │
//	   popup ◄── refilter on every keystroke ◄── lspCompletionEvent
//	   Tab/Enter/click ──► ApplyMultiEdit(textEdit + additionalTextEdits)
//
// The five decisions worth spelling out:
//
//   - IT DOES NOT OPEN ON EVERY LETTER. Auto-trigger is the server's
//     trigger characters only (`.` for Go); typing an identifier does
//     not summon it. Copilot's ghost text already occupies "guess what
//     I'm typing", and two overlays racing on every keystroke is noise.
//     Esc-Space invokes it deliberately anywhere, which is the gesture
//     an IDE user reaches for anyway.
//   - FILTERING IS LOCAL. One request per trigger; the prefix the user
//     keeps typing re-scores the list in memory with the same fzy
//     scorer the palette and finder use. The exception is a server that
//     said isIncomplete — that flag means "this answer was only valid
//     for the prefix you had", and it is honoured with a re-request.
//   - ACCEPT GOES THROUGH ApplyMultiEdit. The item's own textEdit plus
//     its additionalTextEdits (the auto-import) land as ONE undo step.
//     Accepting `fmt.Println` in a file without the import must not be
//     two Esc-u's to undo, and must never leave the import behind.
//   - THE POPUP WINS OVER GHOST TEXT. copilotGhostActive is gated on
//     this being closed, so Tab is never ambiguous.
//   - SNIPPETS ARE REFUSED, NOT MANGLED. The client declares
//     snippetSupport:false; an item that arrives as a snippet anyway
//     would write `${1:name}` into the buffer, so accepting one flashes
//     instead. See internal/lsp/completion.go.

package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/finder"
	"github.com/rohanthewiz/ced/internal/icons"
	"github.com/rohanthewiz/ced/internal/lsp"
)

const (
	// completionDebounce is how long after a trigger character the
	// request fires. Half the LSP sync debounce on purpose: this one is
	// in the user's way — the popup should feel like it was already
	// there — while the didChange debounce is invisible bookkeeping.
	completionDebounce = 150 * time.Millisecond

	// completionVisibleRows caps the list. Ten is the finder/palette
	// number, so all three of ced's list surfaces scroll at the same
	// rhythm; past ten rows the answer is to keep typing, not to scroll.
	completionVisibleRows = 10

	// completionMaxWidth / completionMinWidth bound the box. The floor
	// keeps a one-item list from rendering as a sliver; the ceiling
	// keeps a verbose signature from covering the code being written.
	completionMaxWidth = 62
	completionMinWidth = 22

	// completionDetailMax caps the detail pane under the divider. Four
	// lines is a signature plus a sentence — enough to choose between
	// two similarly named symbols, not enough to become a reader.
	completionDetailMax = 4
)

// completionFallbackTriggers is used when the server declares no trigger
// characters of its own. `.` is the member-access operator in every
// language ced is likely to meet, and being wrong about it costs a
// request nobody asked for rather than a broken editor.
var completionFallbackTriggers = []string{"."}

// -----------------------------------------------------------------------------
// State
// -----------------------------------------------------------------------------

// completionMatch is one item surviving the current prefix filter. It
// holds an INDEX rather than a pointer into items: the resolve round
// trip writes documentation back into the item it enriched, and an index
// stays valid across that mutation in a way a copied struct would not.
type completionMatch struct {
	idx   int
	score int
	hits  []int
}

// completionState is everything the popup remembers, owned by App and
// mutated only on the main loop.
type completionState struct {
	open bool
	// path pins the list to the document it was computed for. A response
	// or a keystroke for any other tab is not this list's business.
	path string
	// start is where the token being completed begins — taken from the
	// server's own textEdit range when it sent one, since only the
	// server knows how much of what you typed its items mean to replace.
	// It is both the filter's anchor and the popup's anchor.
	start editor.Position
	// items is the server's list, sorted once by SortText at ingest so
	// indexes are stable for the life of the popup.
	items []lsp.CompletionItem
	// matches is items surviving the live prefix, best first.
	matches []completionMatch
	// prefix is the buffer text between start and the caret as of the
	// last filter. Kept so refilter can no-op when nothing changed —
	// it runs after every single event.
	prefix   string
	selected int
	scroll   int
	// incomplete is the server's isIncomplete flag: the list was
	// computed for one prefix and must be re-asked rather than filtered
	// as the user types on.
	incomplete bool

	// seq generations the requests. A completion response is worthless
	// once superseded — it describes a caret position that has moved —
	// so only the newest may paint.
	seq int
	// asked records which item indexes have already been sent for
	// resolve, so hovering up and down a list asks once per row.
	asked map[int]bool

	// timer is the single debounce; one is enough because completions
	// only ever concern the active document.
	timer *time.Timer
	// armPath/armRev are the auto-trigger bookkeeping: the document and
	// EditRev the last trigger check ran against, so a check happens
	// once per edit rather than once per event.
	armPath string
	armRev  int

	// rows are the clickable row rects stamped by the last draw, and box
	// is the popup's full rectangle — the one-rect-for-draw-and-hit
	// idiom the status bar and which-key overlay use.
	rows []btnRect
	box  struct{ x, y, w, h int }
}

// item returns the underlying item for a match, or nil if the index has
// gone stale (defensive: every mutation path rebuilds matches, but a
// nil-check here costs nothing next to an out-of-range panic in draw).
func (s *completionState) item(m completionMatch) *lsp.CompletionItem {
	if m.idx < 0 || m.idx >= len(s.items) {
		return nil
	}
	return &s.items[m.idx]
}

// selectedItem returns the item under the highlight, or nil.
func (s *completionState) selectedItem() *lsp.CompletionItem {
	if s.selected < 0 || s.selected >= len(s.matches) {
		return nil
	}
	return s.item(s.matches[s.selected])
}

// -----------------------------------------------------------------------------
// Custom tcell events — the goroutine → main-loop bridge
// -----------------------------------------------------------------------------

// completionTickEvent is the debounce firing: "the document has been
// quiet since the trigger — ask now".
type completionTickEvent struct {
	when    time.Time
	path    string
	trigger string
	kind    int
}

// When satisfies the tcell.Event interface.
func (e *completionTickEvent) When() time.Time { return e.when }

// lspCompletionEvent lands a completion response, stamped with
// everything needed to judge staleness on arrival.
type lspCompletionEvent struct {
	when time.Time
	seq  int
	path string
	rev  int             // Tab.EditRev at request time
	pos  editor.Position // caret at request time
	// manual marks a deliberate invocation, which is the only case that
	// says anything out loud: an automatic trigger finding nothing must
	// stay silent, but a user who pressed Esc-Space deserves an answer.
	manual     bool
	items      []lsp.CompletionItem
	incomplete bool
	err        error
}

// When satisfies the tcell.Event interface.
func (e *lspCompletionEvent) When() time.Time { return e.when }

// lspCompletionResolveEvent carries one enriched item back. idx is the
// position in the list that asked, and seq pins it to that list.
type lspCompletionResolveEvent struct {
	when time.Time
	seq  int
	idx  int
	item *lsp.CompletionItem
}

// When satisfies the tcell.Event interface.
func (e *lspCompletionResolveEvent) When() time.Time { return e.when }

// Compile-time checks that both really are tcell events.
var (
	_ tcell.Event = (*completionTickEvent)(nil)
	_ tcell.Event = (*lspCompletionEvent)(nil)
	_ tcell.Event = (*lspCompletionResolveEvent)(nil)
)

// -----------------------------------------------------------------------------
// Trigger
// -----------------------------------------------------------------------------

// completionAvailable reports whether the popup may run right now: a
// server that understands the active document, and no surface that owns
// the keyboard. The menu/modal check is what keeps a list from popping
// in behind a dialog and stealing its Enter.
func (a *App) completionAvailable() bool {
	return a.hasLSPActions() && a.modal == nil && !a.menuOpen && a.screen != nil
}

// completionTriggerChars is the set that auto-opens the popup: whatever
// the server asked to be consulted on, falling back to `.`. Multi-rune
// entries (C++'s `::`) are kept whole — the match is on the text ending
// at the caret, not on a single rune.
func (a *App) completionTriggerChars() []string {
	if a.lspReady() {
		if chars := a.lsp.client.CompletionTriggerChars(); len(chars) > 0 {
			return chars
		}
	}
	return completionFallbackTriggers
}

// completionAfterEvent runs in the dispatch tail beside lspAfterEvent.
// Two jobs, in order: keep an open popup honest against a buffer that
// just moved, then decide whether the edit that just happened should
// open one.
func (a *App) completionAfterEvent() {
	if a.completion.open {
		a.completionSync()
	}
	if !a.completionAvailable() {
		return
	}
	t := a.activeTabPtr()
	if t == nil || t.Path == "" {
		return
	}
	// One check per EDIT, not per event: cursor travel alone must never
	// spend a request (the same rule copilotAfterEvent keeps). A tab
	// switch adopts the new document's rev silently, so arriving in a
	// file never counts as having typed in it.
	if t.Path != a.completion.armPath {
		a.completion.armPath = t.Path
		a.completion.armRev = t.EditRev
		return
	}
	if t.EditRev == a.completion.armRev {
		return
	}
	a.completion.armRev = t.EditRev

	// An incomplete list is the server saying "ask me again as they
	// type" — so a fresh edit re-requests instead of re-filtering.
	if a.completion.open {
		if a.completion.incomplete {
			a.completionArmTimer(t.Path, "", lsp.CompletionTriggerRefresh)
		}
		return
	}
	if trigger, ok := a.completionTriggerBefore(t); ok {
		a.completionArmTimer(t.Path, trigger, lsp.CompletionTriggerChar)
	}
}

// completionTriggerBefore reports whether the text immediately behind
// the caret ends with a trigger character.
func (a *App) completionTriggerBefore(t *editor.Tab) (string, bool) {
	line := t.Buffer.LineRunes(t.Cursor.Line)
	if t.Cursor.Col <= 0 || t.Cursor.Col > len(line) {
		return "", false
	}
	before := string(line[:t.Cursor.Col])
	for _, trigger := range a.completionTriggerChars() {
		if trigger != "" && strings.HasSuffix(before, trigger) {
			return trigger, true
		}
	}
	return "", false
}

// completionArmTimer (re)starts the debounce. Restarting on every
// further edit is what makes it a debounce: a typing burst through a
// trigger character costs one request, fired once the user pauses.
func (a *App) completionArmTimer(path, trigger string, kind int) {
	a.completionStopTimer()
	scr := a.screen
	a.completion.timer = time.AfterFunc(completionDebounce, func() {
		// Goroutine territory: post, never mutate (the iron rule).
		_ = scr.PostEvent(&completionTickEvent{
			when: time.Now(), path: path, trigger: trigger, kind: kind,
		})
	})
}

// completionStopTimer cancels a pending debounce, if any.
func (a *App) completionStopTimer() {
	if a.completion.timer != nil {
		a.completion.timer.Stop()
		a.completion.timer = nil
	}
}

// handleCompletionTick is the debounce landing on the main loop. Every
// bail-out guards something that can have changed during the wait — tab
// switched, server died, a modal took the keyboard.
func (a *App) handleCompletionTick(e *completionTickEvent) {
	a.completion.timer = nil
	if !a.completionAvailable() {
		return
	}
	t := a.activeTabPtr()
	if t == nil || t.Path != e.path {
		return
	}
	a.completionRequest(t, lsp.CompletionContext{
		TriggerKind: e.kind, TriggerCharacter: e.trigger,
	}, false)
}

// menuCompletion is the deliberate invocation — the ≡ Code row, the
// editor context menu, and Esc-Space. Unlike the automatic path it
// skips the debounce (the user already waited by deciding to press it)
// and it reports an empty result out loud.
func (a *App) menuCompletion() {
	a.closeMenu()
	if !a.completionAvailable() {
		return
	}
	t := a.activeTabPtr()
	if t == nil {
		return
	}
	a.completionStopTimer()
	a.completionRequest(t, lsp.CompletionContext{TriggerKind: lsp.CompletionInvoked}, true)
}

// completionRequest syncs the document and fires the async call,
// stamped with the state it was asked for.
//
// The flush matters as much here as it does for signature help: the
// answer is a function of the half-typed line the caret sits in, and a
// server that has not been told about the `.` just typed will complete
// against the identifier before it.
func (a *App) completionRequest(t *editor.Tab, ctx lsp.CompletionContext, manual bool) {
	a.lspFlushChange(t)
	a.completion.seq++
	seq := a.completion.seq
	client, scr := a.lsp.client, a.screen
	path, rev, pos := t.Path, t.EditRev, t.Cursor
	lspPos := lspPosFor(t, pos)
	go func() {
		items, incomplete, err := client.Completion(path, lspPos, ctx)
		_ = scr.PostEvent(&lspCompletionEvent{
			when: time.Now(), seq: seq, path: path, rev: rev, pos: pos,
			manual: manual, items: items, incomplete: incomplete, err: err,
		})
	}()
}

// -----------------------------------------------------------------------------
// Response
// -----------------------------------------------------------------------------

// handleLSPCompletion lands a response and opens the popup.
//
// The staleness rule is looser than hover's deliberately. A completion
// list stays useful while the user KEEPS TYPING THE WORD — that is the
// normal case at a 150ms debounce, and dropping those responses would
// make the popup appear only for people who type slowly. So a response
// is accepted when the caret is still on its line, at or past where it
// was, with nothing but identifier characters typed since. Anything else
// (a newline, a jump, a deletion behind the request point) means the
// list describes a position that no longer exists.
func (a *App) handleLSPCompletion(e *lspCompletionEvent) {
	if e.seq != a.completion.seq {
		return
	}
	t := a.activeTabPtr()
	if t == nil || t.Path != e.path || !a.completionAvailable() {
		return
	}
	if e.rev != t.EditRev && !completionTypedOn(t, e.pos) {
		return
	}
	if e.err != nil || len(e.items) == 0 {
		a.completionClose()
		if e.manual {
			// "here" is load-bearing, as it is for signature help: the
			// usual reason is a position with nothing to offer, which is
			// a fact about the caret rather than about the server.
			a.flash("No completions here")
		}
		return
	}

	items := append([]lsp.CompletionItem(nil), e.items...)
	// Sort ONCE, by the server's own relevance key, so every later index
	// is stable — the resolve round trip writes back by index. SortText
	// encodes ranking the client cannot recompute (gopls puts locals and
	// already-imported symbols first); Label breaks ties so the order is
	// total and the list never shuffles between identical keys.
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].SortText != items[j].SortText {
			return items[i].SortText < items[j].SortText
		}
		return items[i].Label < items[j].Label
	})

	a.completion.path = e.path
	a.completion.items = items
	a.completion.incomplete = e.incomplete
	a.completion.asked = map[int]bool{}
	a.completion.start = a.completionStart(t, items, e.pos)
	a.completion.prefix = ""
	a.completion.selected = 0
	a.completion.scroll = 0
	a.completion.open = true
	// Prefix is recomputed from the CURRENT caret, not the request's:
	// the user may have typed two more letters while this was in flight,
	// and those letters are exactly what should narrow the list.
	a.completionRefilter(t, true)
	if !a.completion.open {
		// Everything filtered away — the user typed past the list.
		if e.manual {
			a.flash("No completions here")
		}
		return
	}
	// A visible ghost and a visible popup both claim Tab. The popup
	// wins (see the file comment), so the ghost goes now rather than
	// lingering as a suggestion the key no longer accepts.
	a.copilotClearGhost()
	a.completionPreselect()
	a.completionResolveSelected()
}

// completionTypedOn reports whether the only thing that happened since
// the request is more of the same word: same line, caret at or past the
// request point, and every rune in between an identifier rune.
func completionTypedOn(t *editor.Tab, at editor.Position) bool {
	if t.Cursor.Line != at.Line || t.Cursor.Col < at.Col {
		return false
	}
	line := t.Buffer.LineRunes(t.Cursor.Line)
	if at.Col > len(line) || t.Cursor.Col > len(line) {
		return false
	}
	for _, r := range line[at.Col:t.Cursor.Col] {
		if !editor.IsWordRune(r) {
			return false
		}
	}
	return true
}

// completionStart decides where the token being completed begins.
//
// The server's own textEdit range wins whenever one is offered, because
// only the server knows how much of the line its items mean to consume —
// gopls completing a struct literal field can hand back a range covering
// text the editor would never have guessed at. The fallback (no item
// carried an edit) walks back over identifier runes from the caret,
// which is the same word boundary cursorWord uses.
func (a *App) completionStart(t *editor.Tab, items []lsp.CompletionItem, at editor.Position) editor.Position {
	for i := range items {
		if items[i].Edit == nil {
			continue
		}
		start := editorPosFor(t, items[i].Edit.Range.Start)
		// A range on another line, or one starting after the caret, is a
		// server disagreeing with the buffer the editor actually has.
		// Fall through to the word walk rather than anchoring the popup
		// somewhere the user is not.
		if start.Line == at.Line && start.Col <= at.Col {
			return start
		}
		break
	}
	line := t.Buffer.LineRunes(at.Line)
	col := at.Col
	if col > len(line) {
		col = len(line)
	}
	for col > 0 && editor.IsWordRune(line[col-1]) {
		col--
	}
	return editor.Position{Line: at.Line, Col: col}
}

// completionPreselect honours the server's preselect hint, if any item
// carried one and it survived the filter. Servers use it sparingly (the
// obvious continuation of what you were writing), so ignoring it would
// throw away the one ranking signal the score cannot express.
func (a *App) completionPreselect() {
	for i, m := range a.completion.matches {
		if item := a.completion.item(m); item != nil && item.Preselect {
			a.completion.selected = i
			a.completionScrollToSelected()
			return
		}
	}
}

// -----------------------------------------------------------------------------
// Live filtering
// -----------------------------------------------------------------------------

// completionSync keeps an open popup honest after an event: it closes
// when the world it described is gone (different tab, caret off the
// token's line or behind its start) and re-filters otherwise.
func (a *App) completionSync() {
	t := a.activeTabPtr()
	if t == nil || t.Path != a.completion.path || a.modal != nil || a.menuOpen {
		a.completionClose()
		return
	}
	if t.Cursor.Line != a.completion.start.Line || t.Cursor.Col < a.completion.start.Col {
		a.completionClose()
		return
	}
	a.completionRefilter(t, false)
}

// completionRefilter re-scores the list against the text between the
// token start and the caret.
//
// force is for the moment the list arrives, when the prefix has not
// "changed" (there was no list before) but must still be computed. Every
// other call is a no-op unless the user actually typed, which matters
// because this runs after EVERY event.
//
// Closing on an empty result is the standard contract: a popup with no
// rows is a popup that has been typed past, and leaving it up would
// leave Tab and Enter hijacked by a list offering nothing.
func (a *App) completionRefilter(t *editor.Tab, force bool) {
	prefix := t.Buffer.Substring(a.completion.start, t.Cursor)
	if !force && prefix == a.completion.prefix {
		return
	}
	a.completion.prefix = prefix

	a.completion.matches = a.completion.matches[:0]
	for i := range a.completion.items {
		score, hits := finder.Score(prefix, a.completion.items[i].FilterText)
		if score == 0 {
			continue
		}
		a.completion.matches = append(a.completion.matches,
			completionMatch{idx: i, score: score, hits: hits})
	}
	if prefix != "" {
		// Stable, so items scoring equally keep the server's ordering —
		// which is the ranking the client has no way to reproduce.
		sort.SliceStable(a.completion.matches, func(i, j int) bool {
			return a.completion.matches[i].score > a.completion.matches[j].score
		})
	}
	if len(a.completion.matches) == 0 {
		a.completionClose()
		return
	}
	// The best match is the answer to the prefix that was just typed, so
	// the highlight goes back to the top rather than chasing whatever
	// row happens to sit at the old index.
	a.completion.selected = 0
	a.completion.scroll = 0
	a.completionResolveSelected()
}

// completionClose hides the popup and forgets its list. Idempotent; the
// debounce goes with it so a tick armed before the close can't reopen
// something the user dismissed.
func (a *App) completionClose() {
	if !a.completion.open && a.completion.items == nil {
		return
	}
	a.completionStopTimer()
	a.completion.open = false
	a.completion.items = nil
	a.completion.matches = nil
	a.completion.asked = nil
	a.completion.rows = nil
	a.completion.prefix = ""
	a.completion.path = ""
	a.completion.selected = 0
	a.completion.scroll = 0
	a.completion.incomplete = false
	// Bumping the generation invalidates any response still in flight:
	// a list the user dismissed must not reappear 200ms later.
	a.completion.seq++
}

// completionMove walks the highlight by delta rows, clamping at both
// ends (no wrap — a list you can run off the end of makes "how far down
// am I" unanswerable at a glance).
func (a *App) completionMove(delta int) {
	if len(a.completion.matches) == 0 {
		return
	}
	a.completion.selected += delta
	if a.completion.selected < 0 {
		a.completion.selected = 0
	}
	if a.completion.selected >= len(a.completion.matches) {
		a.completion.selected = len(a.completion.matches) - 1
	}
	a.completionScrollToSelected()
	a.completionResolveSelected()
}

// completionScrollToSelected nudges the visible window just enough to
// contain the highlight.
func (a *App) completionScrollToSelected() {
	if a.completion.selected < a.completion.scroll {
		a.completion.scroll = a.completion.selected
	}
	if a.completion.selected >= a.completion.scroll+completionVisibleRows {
		a.completion.scroll = a.completion.selected - completionVisibleRows + 1
	}
	if max := len(a.completion.matches) - completionVisibleRows; a.completion.scroll > max {
		a.completion.scroll = max
	}
	if a.completion.scroll < 0 {
		a.completion.scroll = 0
	}
}

// -----------------------------------------------------------------------------
// Resolve — documentation for the row being looked at
// -----------------------------------------------------------------------------

// completionResolveSelected asks the server to enrich the highlighted
// item, once per item per list.
//
// This is a DISPLAY concern only and the code is written to stay that
// way: the client never declares resolveSupport, so an item's edits are
// always complete on arrival and accepting one never waits for this.
// Failures are swallowed — a detail pane that stays empty is the same
// outcome as a server with nothing to add.
func (a *App) completionResolveSelected() {
	if !a.completion.open || !a.lspReady() {
		return
	}
	if a.completion.selected < 0 || a.completion.selected >= len(a.completion.matches) {
		return
	}
	idx := a.completion.matches[a.completion.selected].idx
	item := a.completion.item(a.completion.matches[a.completion.selected])
	if item == nil || a.completion.asked[idx] || len(item.Raw) == 0 {
		return
	}
	// Nothing to gain when the server already sent documentation, and a
	// server that doesn't advertise resolve would just answer an error.
	if item.Doc != "" {
		return
	}
	if !a.lsp.client.CompletionResolves() {
		return
	}
	a.completion.asked[idx] = true
	client := a.lsp.client
	seq, raw, scr := a.completion.seq, item.Raw, a.screen
	go func() {
		enriched, err := client.ResolveCompletion(raw)
		if err != nil || enriched == nil {
			return
		}
		_ = scr.PostEvent(&lspCompletionResolveEvent{
			when: time.Now(), seq: seq, idx: idx, item: enriched,
		})
	}()
}

// handleLSPCompletionResolve folds an enriched item back into the list.
// Only the two display fields are taken: the edits already on the
// original are the ones the accept path was going to use, and a resolve
// response is not a licence to change what pressing Enter will do.
func (a *App) handleLSPCompletionResolve(e *lspCompletionResolveEvent) {
	if !a.completion.open || e.seq != a.completion.seq {
		return
	}
	if e.idx < 0 || e.idx >= len(a.completion.items) || e.item == nil {
		return
	}
	if e.item.Doc != "" {
		a.completion.items[e.idx].Doc = e.item.Doc
	}
	if a.completion.items[e.idx].Detail == "" && e.item.Detail != "" {
		a.completion.items[e.idx].Detail = e.item.Detail
	}
}

// -----------------------------------------------------------------------------
// Accept
// -----------------------------------------------------------------------------

// completionAccept applies the highlighted item. Returns true when it
// consumed the keystroke — a Tab or Enter with nothing acceptable under
// it falls through to its normal job.
//
// The whole edit set — the item's own replacement plus every
// additionalTextEdit — goes through ApplyMultiEdit as ONE undo step.
// That is the difference between a completion and an insertion:
// accepting `fmt.Println` in a file that doesn't import fmt has to add
// the import too, and one Esc-u has to take back both halves.
func (a *App) completionAccept() bool {
	if !a.completion.open {
		return false
	}
	t := a.activeTabPtr()
	if t == nil || t.Path != a.completion.path {
		a.completionClose()
		return false
	}
	item := a.completion.selectedItem()
	if item == nil {
		a.completionClose()
		return false
	}
	// Refused rather than mangled — see the file comment. The keystroke
	// is still consumed: the user aimed at a row, and silently indenting
	// instead would read as the popup having ignored them.
	if item.Snippet {
		a.flash("Snippet completions aren't supported yet")
		a.completionClose()
		return true
	}

	primary := a.completionPrimaryEdit(t, item)
	// Convert the primary alone first so the same normalisation that the
	// full set gets produces the value to look for inside it. Positions
	// and text are comparable, so finding it afterwards is an equality
	// test rather than an index this code would have to keep in step.
	one, err := convertEdits(t.Buffer, []lsp.TextEdit{primary})
	if err != nil || len(one) != 1 {
		a.flash("Completion: " + completionEditError(err))
		a.completionClose()
		return true
	}
	all, err := convertEdits(t.Buffer, append(append([]lsp.TextEdit(nil), item.Additional...), primary))
	if err != nil {
		a.flash("Completion: " + completionEditError(err))
		a.completionClose()
		return true
	}
	target := -1
	for i := range all {
		if all[i] == one[0] {
			target = i
			break
		}
	}

	results, ok := t.ApplyMultiEdit(all)
	if !ok {
		a.flash("Completion could not be applied")
		a.completionClose()
		return true
	}
	// The caret lands after the text that was just typed — results are
	// in the applied (sorted) order, and target is where the item's own
	// edit ended up inside it. An auto-import above the caret shifts
	// every following line, which is exactly why the END position comes
	// from the result rather than from the request.
	if target >= 0 && target < len(results) {
		t.MoveCursorTo(results[target].End, false)
	}
	if n := len(item.Additional); n > 0 {
		// Worth saying out loud: the accept just wrote somewhere the user
		// is not looking (the import block), and a silent edit off-screen
		// is the kind of thing that erodes trust in the feature.
		a.flash(fmt.Sprintf("%s · %d %s elsewhere in the file",
			item.Label, n, plural(n, "edit", "edits")))
	}
	a.completionClose()
	a.copilotClearGhost()
	return true
}

// completionPrimaryEdit builds the item's own replacement.
//
// When the server sent a textEdit it is authoritative, with one
// correction: its end was measured when the request was made, and the
// user may have typed further into the same token since. Extending the
// end to the caret is what stops `fmt.Prin|` + two more letters from
// leaving `fmt.Printlntln` behind. Without an edit, the range is the
// token the popup has been filtering on, which is the same span the
// prefix was read from.
func (a *App) completionPrimaryEdit(t *editor.Tab, item *lsp.CompletionItem) lsp.TextEdit {
	if item.Edit == nil {
		return lsp.TextEdit{
			Range: lsp.Range{
				Start: lspPosFor(t, a.completion.start),
				End:   lspPosFor(t, t.Cursor),
			},
			NewText: item.InsertText,
		}
	}
	edit := *item.Edit
	end := editorPosFor(t, edit.Range.End)
	if end.Line == t.Cursor.Line && t.Cursor.Col > end.Col {
		edit.Range.End = lspPosFor(t, t.Cursor)
	}
	return edit
}

// completionEditError renders a refusal reason. convertEdits already
// explains the one failure it has (overlapping edits); a nil error here
// means the conversion produced nothing usable at all.
func completionEditError(err error) string {
	if err != nil {
		return err.Error()
	}
	return "the server's edit didn't fit this buffer"
}

// -----------------------------------------------------------------------------
// Keyboard
// -----------------------------------------------------------------------------

// completionKey takes the handful of keys the popup owns off the top of
// the router and reports whether it consumed the keystroke.
//
// The set is deliberately tiny: navigation, accept, dismiss. EVERYTHING
// ELSE FALLS THROUGH — the letters the user keeps typing must reach the
// buffer, because the buffer is what the filter reads. That is the whole
// reason this is not a modal.
func (a *App) completionKey(ev *tcell.EventKey) bool {
	if !a.completion.open {
		return false
	}
	switch ev.Key() {
	case tcell.KeyEsc:
		// Esc is the universal "drop that", and here it must not also arm
		// the leader: the user is dismissing a popup, not starting a
		// gesture. Consuming it is what makes that true.
		a.completionClose()
		return true
	case tcell.KeyUp:
		a.completionMove(-1)
		return true
	case tcell.KeyDown:
		a.completionMove(1)
		return true
	case tcell.KeyPgUp:
		a.completionMove(-completionVisibleRows)
		return true
	case tcell.KeyPgDn:
		a.completionMove(completionVisibleRows)
		return true
	case tcell.KeyTab, tcell.KeyEnter:
		return a.completionAccept()
	}
	return false
}

// -----------------------------------------------------------------------------
// Mouse
// -----------------------------------------------------------------------------

// completionMouse routes a mouse event while the popup is up and reports
// whether it consumed it.
//
// Hover tracks the pointer (the row under it becomes the selection, so
// clicking never lands on a different row than the one that was lit),
// the wheel scrolls, a click on a row accepts, a click inside the box
// but between rows is swallowed, and a click outside dismisses and falls
// through — the same contract the which-key overlay keeps, because a
// click aimed at the code underneath should still reach it.
func (a *App) completionMouse(x, y int, btn tcell.ButtonMask) bool {
	if !a.completion.open {
		return false
	}
	b := a.completion.box
	inside := x >= b.x && x < b.x+b.w && y >= b.y && y < b.y+b.h

	if btn&(tcell.WheelUp|tcell.WheelDown) != 0 {
		if !inside {
			return false
		}
		delta := 1
		if btn&tcell.WheelUp != 0 {
			delta = -1
		}
		a.completionMove(delta)
		return true
	}

	for i, rect := range a.completion.rows {
		if !rect.contains(x, y) {
			continue
		}
		row := a.completion.scroll + i
		if row >= len(a.completion.matches) {
			break
		}
		a.completion.selected = row
		if btn&tcell.Button1 != 0 {
			a.completionResolveSelected()
			return a.completionAccept()
		}
		a.completionResolveSelected()
		return true
	}

	if btn&(tcell.Button1|tcell.Button2|tcell.Button3) == 0 {
		// Pure motion outside the rows: not ours, but not worth
		// dismissing over either.
		return false
	}
	if inside {
		return true
	}
	a.completionClose()
	return false
}

// -----------------------------------------------------------------------------
// Draw
// -----------------------------------------------------------------------------

// completionRect computes the popup's rectangle, anchored at the START
// OF THE TOKEN rather than at the caret: an IDE's list lines up with the
// beginning of the word being completed, so each label sits directly
// above the text it would replace.
//
// Preferred position is one row below the anchor; when that would run
// off the bottom it flips above, the same rule the hover tooltip uses.
// ok=false when there is nothing to anchor to (the token scrolled out of
// view), which the draw path treats as "don't paint" — an unanchored
// completion list is a list about nothing.
func (a *App) completionRect() (x, y, w, h int, ok bool) {
	t := a.activeTabPtr()
	if t == nil || len(a.completion.matches) == 0 {
		return 0, 0, 0, 0, false
	}
	ex, ey, ew, eh := a.editorRect()
	dx, dy, visible := t.PosScreenCell(a.completion.start, ew, eh)
	if !visible {
		return 0, 0, 0, 0, false
	}

	rows := len(a.completion.matches)
	if rows > completionVisibleRows {
		rows = completionVisibleRows
	}

	w = completionMinWidth
	for _, m := range a.completion.matches {
		item := a.completion.item(m)
		if item == nil {
			continue
		}
		if lw := a.completionRowWidth(item) + 4; lw > w {
			w = lw
		}
	}
	if w > completionMaxWidth {
		w = completionMaxWidth
	}
	if w > a.width {
		w = a.width
	}

	// The detail pane is measured at the width the box actually got, not
	// at the cap: word wrapping is a function of the column count, so
	// measuring at one width and drawing at another would size the box
	// for a different number of lines than the draw produces.
	detail := a.completionDetailLines(w - 4)

	h = rows + 2 // list rows plus the two border rows
	if len(detail) > 0 {
		h += len(detail) + 1 // detail rows plus the divider above them
	}

	x = ex + dx
	if x+w > a.width {
		x = a.width - w
	}
	if x < 0 {
		x = 0
	}
	y = ey + dy + 1 // below the token
	if y+h > a.height-1 {
		y = ey + dy - h // flip above
	}
	if y < 0 {
		y = 0
	}
	return x, y, w, h, true
}

// completionRowWidth is one row's natural width: glyph, label, and a
// gap plus the detail tail when there is one. Used only for sizing —
// drawRow elides against whatever width the box actually got.
func (a *App) completionRowWidth(item *lsp.CompletionItem) int {
	w := runeLen(item.Label)
	if a.iconsOn() {
		w += 2
	}
	if item.Detail != "" {
		w += 2 + runeLen(item.Detail)
	}
	return w
}

// completionDetailLines renders the pane under the divider for the
// selected item: its signature, then the opening paragraph of its
// documentation. Empty when the server said nothing worth a pane, in
// which case the popup is just the list.
//
// The doc is word-wrapped and the signature is not: a signature is code,
// and collapsing its runs of whitespace would misrepresent it — the same
// split signatureTipLines makes.
func (a *App) completionDetailLines(width int) []string {
	item := a.completion.selectedItem()
	if item == nil || width < 4 {
		return nil
	}
	var out []string
	if item.Detail != "" {
		out = append(out, item.Detail)
	} else if name := lsp.CompletionKindName(item.Kind); name != "" {
		out = append(out, name)
	}
	if doc := strings.TrimSpace(firstParagraph(item.Doc)); doc != "" {
		out = append(out, wrapChatText(doc, width)...)
	}
	if len(out) > completionDetailMax {
		out = capLines(out, completionDetailMax)
	}
	return out
}

// drawCompletion paints the popup and stamps the clickable row rects.
// Draw is the single geometry source; completionMouse only reads what
// this leaves behind.
//
// It deliberately does NOT hide the hardware cursor, unlike every modal.
// The user is still typing into the buffer underneath, and a caret that
// vanished while a list was up would be the clearest possible signal
// that the editor had stopped listening — the exact opposite of what is
// true here.
func (a *App) drawCompletion() {
	mx, my, mw, mh, ok := a.completionRect()
	a.completion.rows = a.completion.rows[:0]
	if !ok {
		// Nothing painted, so nothing may be hit: a box left over from
		// the last draw would swallow clicks over a region that no
		// longer has a popup in it.
		a.completion.box = struct{ x, y, w, h int }{}
		return
	}
	a.completion.box = struct{ x, y, w, h int }{mx, my, mw, mh}

	c := a.chrome()
	fillRect(a.screen, mx, my, mw, mh, c.bgSt)
	drawBorder(a.screen, mx, my, mw, mh, c.border)

	hitStyle := tcell.StyleDefault.Foreground(a.theme.FindCurrent).Bold(true)
	rows := len(a.completion.matches) - a.completion.scroll
	if rows > completionVisibleRows {
		rows = completionVisibleRows
	}
	for i := 0; i < rows; i++ {
		m := a.completion.matches[a.completion.scroll+i]
		item := a.completion.item(m)
		if item == nil {
			continue
		}
		ry := my + 1 + i
		a.drawCompletionRow(item, m, mx, ry, mw, a.completion.scroll+i == a.completion.selected, hitStyle, c)
		a.completion.rows = append(a.completion.rows, btnRect{x: mx + 1, y: ry, w: mw - 2})
	}

	// The count tail rides the bottom border when the list is longer
	// than the window, so "there is more below" needs no scrollbar.
	if len(a.completion.matches) > completionVisibleRows {
		tail := fmt.Sprintf(" %d/%d ", a.completion.selected+1, len(a.completion.matches))
		if runeLen(tail) < mw-2 {
			drawAt(a.screen, mx+mw-1-runeLen(tail), my+mh-1, tail, c.muted)
		}
	}

	detail := a.completionDetailLines(mw - 4)
	if len(detail) == 0 {
		return
	}
	dy := my + 1 + rows
	drawHDivider(a.screen, mx, dy, mw, c.border)
	for i, ln := range detail {
		if dy+1+i >= my+mh-1 {
			break
		}
		drawStatusText(a.screen, mx+2, dy+1+i, mw-4, ln, c.muted)
	}
}

// drawCompletionRow paints one row: kind glyph, label with the matched
// runes lit, and the detail tail right-aligned in whatever space is
// left. The selected row flips its whole background, the same
// block-highlight the finder and palette use so selection reads without
// having to look for a marker.
func (a *App) drawCompletionRow(item *lsp.CompletionItem, m completionMatch,
	mx, ry, mw int, selected bool, hitStyle tcell.Style, c modalChrome) {

	rowBG := c.bg
	if selected {
		rowBG = a.theme.BG
	}
	rowStyle := tcell.StyleDefault.Background(rowBG).Foreground(a.theme.Text)
	if item.Deprecated {
		// The protocol's own word for "this exists but don't use it".
		// Dimming says it without spending a column on a marker.
		rowStyle = rowStyle.Foreground(a.theme.Muted).StrikeThrough(true)
	}
	mutedStyle := tcell.StyleDefault.Background(rowBG).Foreground(a.theme.Muted)
	hitOnRow := hitStyle.Background(rowBG)

	for cx := mx + 1; cx < mx+mw-1; cx++ {
		a.screen.SetContent(cx, ry, ' ', nil, rowStyle)
	}

	col := mx + 2
	avail := mw - 4
	if a.iconsOn() {
		drawAt(a.screen, col, ry, icons.CompletionKind(item.Kind), mutedStyle)
		col += 2
		avail -= 2
	}
	if avail < 1 {
		return
	}

	// The detail tail gets whatever is left after the label, and only
	// when at least a few cells remain — a two-character sliver of a
	// signature is noise, not information.
	label := []rune(item.Label)
	labelW := len(label)
	if labelW > avail {
		labelW = avail
	}
	tail := ""
	if item.Detail != "" {
		if room := avail - labelW - 2; room >= 6 {
			tail = elide(item.Detail, room)
		}
	}

	hits := map[int]bool{}
	for _, h := range m.hits {
		hits[h] = true
	}
	for i := 0; i < labelW; i++ {
		st := rowStyle
		if hits[i] {
			st = hitOnRow
		}
		a.screen.SetContent(col+i, ry, label[i], nil, st)
	}
	if tail != "" {
		drawAt(a.screen, mx+mw-2-runeLen(tail), ry, tail, mutedStyle)
	}
}

// elide truncates to w cells, marking the cut with an ellipsis. An
// unmarked truncation reads as the server having said less than it did —
// the same rule capLines follows one dimension over.
func elide(s string, w int) string {
	runes := []rune(s)
	if len(runes) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	return string(runes[:w-1]) + "…"
}
