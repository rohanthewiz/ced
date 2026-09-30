// =============================================================================
// File: internal/app/lsp.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-09
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// lsp.go bridges the minimal LSP client (internal/lsp) into the editor:
// server lifecycle, document sync, diagnostics, go-to-definition, and
// hover. The other verbs built on this plumbing live next door —
// document symbols in lspsymbols.go, references in lspreferences.go,
// signature help in lspsignature.go. It follows the same house rules as
// every other subsystem:
//
//   - Silent degradation (format.go's rule): no gopls on PATH, server
//     crash, request failure — the editor keeps working, nothing nags.
//   - Custom tcell events for goroutine → main-loop messaging: the
//     client's read loop, the start handshake, the debounce timers,
//     and the definition/hover calls all run off-loop and post events;
//     only the main loop touches lspState.
//
// Lifecycle: the first opened .go file kicks off an async start
// (LookPath → spawn → initialize handshake on a goroutine). When
// lspReadyEvent lands, every already-open Go tab gets its didOpen —
// so documents opened during the handshake aren't lost. Edits re-arm
// a per-document debounce timer; the timer posts lspSyncEvent and the
// main loop sends one full-text didChange. Saves flush any pending
// change first so the server never sees a didSave for stale content.
//
//	openFile(.go) ──► lspEnsureStarted ──goroutine──► spawn + initialize
//	                                                        │
//	   didOpen all open .go tabs  ◄── lspReadyEvent ◄───────┘
//	   keystroke → EditRev bump → debounce timer ──► lspSyncEvent → didChange
//	   gopls ──publishDiagnostics──► lspDiagsEvent → App.lsp.diags → gutter/underline

package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
	"github.com/rohanthewiz/ced/internal/theme"
)

// lspSyncDebounce is how long after the last edit the didChange fires.
// Long enough to coalesce a typing burst into one sync, short enough
// that diagnostics feel live when the user pauses.
const lspSyncDebounce = 300 * time.Millisecond

// lspConn is the slice of the lsp.Client surface the app layer uses.
// An interface so tests can substitute a recording fake without
// spawning a real server process.
type lspConn interface {
	DidOpen(path, languageID string, version int, text string) error
	DidChange(path string, version int, text string) error
	DidSave(path string) error
	DidClose(path string) error
	Definition(path string, pos lsp.Position) ([]lsp.Location, error)
	// Locations is definition's shape under another method name —
	// implementation and typeDefinition (lspgoto.go).
	Locations(method, path string, pos lsp.Position) ([]lsp.Location, error)
	// IncomingCalls is the call hierarchy's two requests behind one
	// call: every call site of the function at pos.
	IncomingCalls(path string, pos lsp.Position) ([]lsp.Location, error)
	References(path string, pos lsp.Position, includeDecl bool) ([]lsp.Location, error)
	Rename(path string, pos lsp.Position, newName string) (*lsp.WorkspaceEdit, error)
	CodeActions(path string, rng lsp.Range, diags []lsp.Diagnostic) ([]lsp.CodeAction, error)
	ExecuteCommand(cmd string, args []json.RawMessage) error
	HoverAt(path string, pos lsp.Position) (*lsp.Hover, error)
	SignatureHelpAt(path string, pos lsp.Position) (*lsp.Signature, error)
	DocumentSymbols(path string) ([]lsp.Symbol, error)
	DocumentHighlights(path string, pos lsp.Position) ([]lsp.DocumentHighlight, error)
	InlayHints(path string, rng lsp.Range) ([]lsp.InlayHint, error)
	WorkspaceSymbols(query string) ([]lsp.WorkspaceSymbol, error)
	// The completion quartet (completion.go). Two of the four ask the
	// SERVER'S OPINION rather than sending a request, which is new to
	// this interface: which characters open the popup, and whether
	// resolve is worth calling, are facts the server declared at
	// handshake time and the editor would otherwise be guessing at.
	Completion(path string, pos lsp.Position, ctx lsp.CompletionContext) ([]lsp.CompletionItem, bool, error)
	ResolveCompletion(raw json.RawMessage) (*lsp.CompletionItem, error)
	CompletionTriggerChars() []string
	CompletionResolves() bool
	Close()
}

// lspState is everything the LSP integration remembers, owned by App
// and mutated only on the main loop. Maps are lazily created so tests
// that assemble an App by hand need no extra setup.
type lspState struct {
	// servers holds one connection slot per language server, keyed by
	// lspServerDef.id and created on first need. See lspservers.go.
	servers map[string]*lspServer

	// dead switches the WHOLE integration off: editor shutdown, and the
	// test harness, which must never spawn a real server. A server that
	// is missing or crashed is dead in its own slot instead, so one bad
	// install costs one language.
	dead bool

	versions  map[string]int // per-path didChange version counter
	syncedRev map[string]int // per-path Tab.EditRev last sent to the server
	timers    map[string]*time.Timer

	// refSeq generations the references lookups (lspreferences.go). That
	// verb is the only one whose answer OPENS A PANEL, so unlike
	// definition and hover — which are content with a path check — it
	// needs to know which request it belongs to.
	refSeq int

	// inlayAsked records, per path, the EditRev (+1) inlay hints were
	// last requested at — one ask per revision. See lspinlay.go.
	inlayAsked map[string]int

	// symSeq generations the workspace-symbol queries
	// (lspworkspacesymbols.go) — the answer opens a picker.
	symSeq int

	// renameSeq generations the rename requests (lsprename.go), for a
	// harder version of refSeq's reason: that answer opens a panel, this
	// one WRITES FILES. Two renames in flight together must not have the
	// older one's edit planned against a buffer the newer one has already
	// rewritten.
	renameSeq int

	// actionSeq generations the code-action lookups (lspcodeaction.go).
	// Same reason as renameSeq: a picked row can write files, so a list
	// from an older ask must not outlive a newer one and hand back ranges
	// measured against a document that has since been rewritten.
	actionSeq int

	// diags is keyed by absolute path. gopls publishes for any file in
	// the workspace, not just open ones; keeping them all costs little
	// and means a file opened later shows its problems immediately.
	diags map[string][]lsp.Diagnostic
}

// -----------------------------------------------------------------------------
// Custom tcell events — the goroutine → main-loop bridge
// -----------------------------------------------------------------------------

// lspReadyEvent is posted once the async spawn + initialize handshake
// completes successfully; it carries the live connection.
type lspReadyEvent struct {
	when   time.Time
	server string // lspServerDef.id the handshake was for
	client lspConn
	// gen is the slot generation the spawn ran under (lspServer.gen). A
	// restart bumps it, so a handshake the user has since superseded is
	// closed instead of installed.
	gen int
}

// When satisfies the tcell.Event interface.
func (e *lspReadyEvent) When() time.Time { return e.when }

// lspExitEvent is posted when the server dies or fails to start.
type lspExitEvent struct {
	when   time.Time
	server string // lspServerDef.id that went away
	// gen is the slot generation of the process that exited. Closing the
	// old client during a restart fires its onExit; without this stamp
	// that late event would mark the freshly started server dead.
	gen int
}

// When satisfies the tcell.Event interface.
func (e *lspExitEvent) When() time.Time { return e.when }

// lspDiagsEvent carries one document's fresh diagnostics from the
// client's read loop to the main loop.
type lspDiagsEvent struct {
	when  time.Time
	path  string
	diags []lsp.Diagnostic
}

// When satisfies the tcell.Event interface.
func (e *lspDiagsEvent) When() time.Time { return e.when }

// lspSyncEvent is posted by a debounce timer: "this document has been
// quiet for lspSyncDebounce — sync it now".
type lspSyncEvent struct {
	when time.Time
	path string
}

// When satisfies the tcell.Event interface.
func (e *lspSyncEvent) When() time.Time { return e.when }

// lspDefinitionEvent carries a definition response. fromPath/fromPos
// remember where the request was made so the nav stack records the
// true origin even if the user moved while the request was in flight.
type lspDefinitionEvent struct {
	when     time.Time
	fromPath string
	fromPos  editor.Position
	locs     []lsp.Location
	err      error
}

// When satisfies the tcell.Event interface.
func (e *lspDefinitionEvent) When() time.Time { return e.when }

// lspHoverEvent carries a hover response, already flattened to text.
//
// Two surfaces land here, and the dwell fields are what tells them
// apart: the keyboard verb below (modal, flashes when there is nothing
// to say) and the mouse-dwell tooltip (hoverdwell.go — ambient, silent).
// One event type rather than two because the request, the flattening and
// the "is this still the right tab" question are identical; only the
// destination differs.
type lspHoverEvent struct {
	when time.Time
	path string
	text string
	err  error
	// markdown is the content kind the server answered with (see
	// lsp.Hover.Markup); hoverLines reads the two kinds differently.
	markdown bool

	// dwell marks a request made by the pointer rather than the caret.
	dwell bool
	// seq is the dwell generation this answer was asked under, and ax/ay
	// the screen cell it was asked about. Meaningless when dwell is false
	// — the caret flavour has no pointer to have moved.
	seq    int
	ax, ay int
}

// When satisfies the tcell.Event interface.
func (e *lspHoverEvent) When() time.Time { return e.when }

// -----------------------------------------------------------------------------
// Lifecycle
// -----------------------------------------------------------------------------

// lspEnsureStarted kicks off one server's async start the first time a
// file it handles opens. A missing binary marks THAT server dead without
// a word to the user — same silent-degradation contract as missing
// formatters — and leaves every other server alone. Idempotent; later
// calls are no-ops.
func (a *App) lspEnsureStarted(def *lspServerDef) {
	if def == nil || a.lsp.dead || a.screen == nil {
		return
	}
	sv := a.lsp.server(def.id)
	if sv.client != nil || sv.starting || sv.dead {
		return
	}
	bin, args, ok := def.resolveCommand()
	if !ok {
		sv.dead = true
		return
	}
	sv.starting = true
	scr := a.screen
	root := a.rootDir
	id := def.id
	gen := sv.gen
	initOptions := def.initOptions
	go func() {
		// onNotify runs on the client's read loop — post, don't touch.
		onNotify := func(method string, params json.RawMessage) {
			if lspPostServerNote(scr, id, method, params) {
				return
			}
			if method != "textDocument/publishDiagnostics" {
				return
			}
			var p lsp.PublishDiagnosticsParams
			if err := json.Unmarshal(params, &p); err != nil {
				return
			}
			path := lsp.URIToPath(p.URI)
			if path == "" {
				return
			}
			_ = scr.PostEvent(&lspDiagsEvent{when: time.Now(), path: path, diags: p.Diagnostics})
		}
		// onRequest answers server→client REQUESTS, and it answers exactly
		// one: workspace/applyEdit, the route a command-only code action
		// uses to deliver what it computed. Everything else declines with
		// ErrRequestUnhandled and falls through to the client's built-in
		// auto-responder — which is not a formality, because
		// workspace/configuration is in that set and gopls BLOCKS on it
		// while type-checking. Each invocation gets its own goroutine, so
		// blocking here for a user's confirmation is safe.
		onRequest := func(method string, params json.RawMessage) (any, error) {
			if method != "workspace/applyEdit" {
				return nil, lsp.ErrRequestUnhandled
			}
			// scr, not a — the App must not be touched off-loop, and the
			// screen is the only thing this needs to post an event.
			return lspServeApplyEdit(scr, params)
		}
		onExit := func(error) {
			_ = scr.PostEvent(&lspExitEvent{when: time.Now(), server: id, gen: gen})
		}
		client, err := lsp.StartWithRequests(root, bin, args, onNotify, onRequest, onExit)
		if err != nil {
			_ = scr.PostEvent(&lspExitEvent{when: time.Now(), server: id, gen: gen})
			return
		}
		if err := client.InitializeWithOptions(root, initOptionsOrNil(initOptions)); err != nil {
			client.Close()
			// The failed handshake already fires onExit via the read
			// loop in most cases, but a timeout leaves the process
			// running — post explicitly so the state machine settles.
			_ = scr.PostEvent(&lspExitEvent{when: time.Now(), server: id, gen: gen})
			return
		}
		_ = scr.PostEvent(&lspReadyEvent{when: time.Now(), server: id, client: client, gen: gen})
	}()
}

// handleLSPReady installs the live connection and announces every
// already-open handled document — the tabs the user opened while the
// handshake was still in flight.
func (a *App) handleLSPReady(e *lspReadyEvent) {
	sv := a.lsp.server(e.server)
	if a.lsp.dead || sv.dead || e.gen != sv.gen {
		// Server died between the ready post and now (or the editor
		// shut the integration down), or a restart superseded this
		// handshake (lsprestart.go). Don't resurrect.
		e.client.Close()
		return
	}
	a.lspInstall(e.server, e.client)
	// lspOpenDoc skips every tab this server doesn't speak for (their own
	// server isn't the one that just came up, or is already announced).
	for _, t := range a.tabs {
		if def := lspServerFor(t.Path); def != nil && def.id == e.server {
			a.lspOpenDoc(t)
		}
	}
}

// handleLSPExit marks ONE server dead and clears every diagnostic it
// published — stale squiggles from a crashed server would otherwise
// linger forever with nothing left to retract them. The other servers'
// state is untouched: degradation is per server. Deliberately no
// auto-restart: a crashing server would flap, and the user can
// restart the editor when they've fixed their install — or, without
// leaving it, use the ≡ Code "Restart language server" row
// (lsprestart.go), which is the deliberate retry gesture.
func (a *App) handleLSPExit(e *lspExitEvent) {
	sv := a.lsp.server(e.server)
	if e.gen != sv.gen {
		// The process a restart replaced, reporting its own death late.
		// The slot now belongs to its successor.
		return
	}
	a.lspDropServer(e.server)
	sv.dead = true
}

// lspDropServer empties one server's slot and everything per-document
// that belonged to it, WITHOUT passing a verdict: the caller decides
// whether the slot is now dead (a crash) or about to be refilled (a
// restart). Shared so the two teardowns cannot drift.
func (a *App) lspDropServer(id string) {
	sv := a.lsp.server(id)
	if sv.client != nil {
		sv.client.Close()
	}
	sv.client = nil
	sv.starting = false
	sv.progress, sv.progressLast = nil, ""
	// Everything per-document is keyed by path, and a path names its
	// server, so "this server's share" is a filter rather than a second
	// set of maps.
	mine := func(path string) bool {
		def := lspServerFor(path)
		return def != nil && def.id == id
	}
	for path := range a.lsp.diags {
		if mine(path) {
			delete(a.lsp.diags, path)
		}
	}
	for path, tm := range a.lsp.timers {
		if mine(path) {
			tm.Stop()
			delete(a.lsp.timers, path)
		}
	}
	for path := range a.lsp.versions {
		if mine(path) {
			delete(a.lsp.versions, path)
			delete(a.lsp.syncedRev, path)
		}
	}
	// Same contract as a publish: the panel's cached rows describe a map
	// that no longer exists, and an open panel must not go on offering
	// jumps into a list the server has taken back.
	if a.problems.open {
		a.refreshProblems()
	}
}

// lspShutdown tears the connection down on editor exit.
func (a *App) lspShutdown() {
	a.lspStopTimers()
	for _, sv := range a.lsp.servers {
		if sv.client != nil {
			sv.client.Close()
			sv.client = nil
		}
	}
	a.lsp.dead = true
}

// lspStopTimers cancels every pending debounce timer.
func (a *App) lspStopTimers() {
	for path, tm := range a.lsp.timers {
		tm.Stop()
		delete(a.lsp.timers, path)
	}
}

// -----------------------------------------------------------------------------
// Document sync
// -----------------------------------------------------------------------------

// lspOpenDoc announces one tab's document to the server (and starts
// the server on first need). Safe to call for any tab — non-Go files,
// images, and untitled tabs are skipped.
func (a *App) lspOpenDoc(t *editor.Tab) {
	if t == nil || t.Path == "" || t.IsImage() || !lspHandles(t.Path) {
		return
	}
	a.lspEnsureStarted(lspServerFor(t.Path))
	client := a.lspClientFor(t.Path)
	if client == nil {
		return // queued implicitly: handleLSPReady re-announces open tabs
	}
	if a.lsp.versions == nil {
		a.lsp.versions = map[string]int{}
	}
	if a.lsp.syncedRev == nil {
		a.lsp.syncedRev = map[string]int{}
	}
	a.lsp.versions[t.Path] = 1
	a.lsp.syncedRev[t.Path] = t.EditRev
	_ = client.DidOpen(t.Path, languageIDFor(t.Path), 1, t.Buffer.String())
}

// lspCloseDoc announces a closed tab and drops its bookkeeping. The
// diagnostics entry goes too — the server will retract them anyway,
// and holding paint data for an invisible file helps nobody.
func (a *App) lspCloseDoc(path string) {
	if path == "" || !lspHandles(path) {
		return
	}
	if tm := a.lsp.timers[path]; tm != nil {
		tm.Stop()
		delete(a.lsp.timers, path)
	}
	delete(a.lsp.versions, path)
	delete(a.lsp.syncedRev, path)
	delete(a.lsp.diags, path)
	if client := a.lspClientFor(path); client != nil {
		_ = client.DidClose(path)
	}
}

// lspDidSave flushes any unsent buffer content and then announces the
// save. The flush matters: with a pending debounce the server would
// otherwise see didSave for text it hasn't been given yet and
// diagnose a phantom version of the file.
func (a *App) lspDidSave(t *editor.Tab) {
	if t == nil {
		return
	}
	client := a.lspClientFor(t.Path)
	if client == nil {
		return
	}
	a.lspFlushChange(t)
	_ = client.DidSave(t.Path)
}

// lspAfterEvent runs after every event dispatch and (re-)arms the
// debounce timer for any open document whose EditRev moved past what
// the server has. Cheap — a handful of integer compares — so calling
// it unconditionally keeps the trigger logic in one place instead of
// sprinkled through every mutation path.
func (a *App) lspAfterEvent() {
	if !a.lspAnyReady() || a.screen == nil {
		return
	}
	for _, t := range a.tabs {
		if t.Path == "" || t.IsImage() || !a.lspReadyFor(t.Path) {
			continue
		}
		if _, open := a.lsp.versions[t.Path]; !open {
			continue // never announced (opened pre-ready and missed? — didOpen handles)
		}
		if t.EditRev == a.lsp.syncedRev[t.Path] {
			continue
		}
		a.lspArmTimer(t.Path)
	}
}

// lspArmTimer starts (or restarts) the per-document debounce clock.
// Restarting on every further edit is what makes it a debounce rather
// than a throttle: the sync fires once the document goes quiet.
func (a *App) lspArmTimer(path string) {
	if a.lsp.timers == nil {
		a.lsp.timers = map[string]*time.Timer{}
	}
	if tm := a.lsp.timers[path]; tm != nil {
		tm.Reset(lspSyncDebounce)
		return
	}
	scr := a.screen
	a.lsp.timers[path] = time.AfterFunc(lspSyncDebounce, func() {
		_ = scr.PostEvent(&lspSyncEvent{when: time.Now(), path: path})
	})
}

// handleLSPSync is the debounce firing on the main loop: send the
// document's current full text if it's still open and still ahead of
// the server. The timer entry is dropped so the next edit arms a
// fresh one.
func (a *App) handleLSPSync(e *lspSyncEvent) {
	delete(a.lsp.timers, e.path)
	t := a.tabByPath(e.path)
	if t == nil {
		return
	}
	a.lspFlushChange(t)
}

// lspFlushChange sends one full-text didChange if the buffer is ahead
// of what the server has seen. No-op when already in sync.
func (a *App) lspFlushChange(t *editor.Tab) {
	if t == nil {
		return
	}
	client := a.lspClientFor(t.Path)
	if client == nil {
		return
	}
	if _, open := a.lsp.versions[t.Path]; !open {
		return
	}
	if t.EditRev == a.lsp.syncedRev[t.Path] {
		return
	}
	a.lsp.versions[t.Path]++
	a.lsp.syncedRev[t.Path] = t.EditRev
	_ = client.DidChange(t.Path, a.lsp.versions[t.Path], t.Buffer.String())
}

// tabByPath returns the open tab backing path, or nil. Events resolve
// tabs by path rather than index because tabs reorder and close while
// background work is in flight — same rule formatDoneEvent follows.
func (a *App) tabByPath(path string) *editor.Tab {
	for _, t := range a.tabs {
		if t.Path == path {
			return t
		}
	}
	return nil
}

// handleLSPDiags stores one document's fresh diagnostics. An empty
// list retracts — gopls sends that when the last problem is fixed.
func (a *App) handleLSPDiags(e *lspDiagsEvent) {
	if a.lsp.diags == nil {
		a.lsp.diags = map[string][]lsp.Diagnostic{}
	}
	if len(e.diags) == 0 {
		delete(a.lsp.diags, e.path)
	} else {
		a.lsp.diags[e.path] = e.diags
	}
	// The Problems panel caches its rows, so a publish has to tell it —
	// but only while it is on screen. A closed panel's list is rebuilt by
	// whoever next asks for it (the next/previous verbs do), so a
	// background publish never pays to sort a list nobody is reading.
	if a.problems.open {
		a.refreshProblems()
	}
}

// -----------------------------------------------------------------------------
// Position conversion — the editor speaks runes, LSP speaks UTF-16
// -----------------------------------------------------------------------------

// lspPosFor converts a buffer position to an LSP position.
func lspPosFor(t *editor.Tab, p editor.Position) lsp.Position {
	return lsp.Position{
		Line:      p.Line,
		Character: lsp.UTF16Col(t.Buffer.LineRunes(p.Line), p.Col),
	}
}

// editorPosFor converts an LSP position into a clamped buffer position
// for tab t.
func editorPosFor(t *editor.Tab, p lsp.Position) editor.Position {
	pos := editor.Position{
		Line: p.Line,
		Col:  lsp.RuneCol(t.Buffer.LineRunes(p.Line), p.Character),
	}
	return t.Buffer.Clamp(pos)
}

// -----------------------------------------------------------------------------
// Go-to-definition
// -----------------------------------------------------------------------------

// hasLSPActions is the menu predicate for definition / hover: the
// server is up and the active tab is a document it understands.
func (a *App) hasLSPActions() bool {
	t := a.activeTabPtr()
	return t != nil && t.Path != "" && !t.IsImage() && a.lspReadyFor(t.Path)
}

// menuGoToDefinition fires an async definition request for the symbol
// under the cursor. The result lands as an lspDefinitionEvent.
func (a *App) menuGoToDefinition() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil || !a.hasLSPActions() {
		return
	}
	client := a.lspClientFor(t.Path)
	scr := a.screen
	path, from := t.Path, t.Cursor
	pos := lspPosFor(t, from)
	a.lspFlushChange(t) // definition must resolve against what's on screen
	go func() {
		locs, err := client.Definition(path, pos)
		_ = scr.PostEvent(&lspDefinitionEvent{
			when: time.Now(), fromPath: path, fromPos: from, locs: locs, err: err,
		})
	}()
}

// handleLSPDefinition lands a definition response: jump to the first
// location, recording where we came from in the app-wide navigation
// history so Go back (Esc-o / Alt+Left) can retrace. The open runs
// suppressed and the origin is recorded explicitly with the request's
// exact cursor position — a same-file jump moves only the cursor, which
// openFile's path-change recording would miss.
//
// WHEN THE ANSWER IS WHERE THE QUESTION WAS ASKED, THE VERB FLIPS TO
// USAGES. A server asked for the definition of a declaration answers with
// the declaration itself, so a plain jump would move nothing and read as
// the key doing nothing. JetBrains' ⌘B makes the same turn — at the
// declaration, "go to definition" shows the usages — and it is the
// natural second half of the question: standing on a symbol, the two
// places a reader wants to be are where it is defined and where it is
// used, and one of those is always somewhere else. The references list
// opens through menuFindReferences, the one spelling of that verb, from
// the request's own position (re-placed, in case a keystroke landed in
// the round trip) — and only while the tab that asked is still in front,
// because a list about a file the user has since left would be answering
// a question nobody is looking at.
func (a *App) handleLSPDefinition(e *lspDefinitionEvent) {
	if e.err != nil || len(e.locs) == 0 {
		a.flash("No definition found" + a.lspLoadingNote(e.fromPath))
		return
	}
	target := lsp.URIToPath(e.locs[0].URI)
	if target == "" {
		a.flash("Definition is not in a plain file")
		return
	}
	if a.definitionIsHere(e, target) {
		t := a.activeTabPtr()
		if t == nil || t.Path != e.fromPath {
			return
		}
		t.MoveCursorTo(e.fromPos, false)
		a.menuFindReferences()
		return
	}
	a.lspJumpTo(e.fromPath, e.fromPos, target, e.locs[0].Range.Start)
}

// lspJumpTo is the landing every "go to" verb shares: open the target
// with nav recording suppressed, then record the REQUEST's origin
// explicitly — a same-file jump moves only the cursor, which openFile's
// path-change recording would miss — and place the caret. Reports
// whether the jump landed; a false means openFile refused and has
// already flashed why.
func (a *App) lspJumpTo(fromPath string, fromPos editor.Position, target string, at lsp.Position) bool {
	a.nav.suppress = true
	a.openFile(target)
	a.nav.suppress = false
	t := a.activeTabPtr()
	if t == nil || t.Path != target {
		return false
	}
	// An untitled origin has no path to come back to (currentNavLoc's rule).
	if fromPath != "" {
		a.recordNav(navLoc{path: fromPath, pos: fromPos})
	}
	t.MoveCursorTo(editorPosFor(t, at), false)
	return true
}

// definitionIsHere reports whether the definition the server named is
// the very symbol the request was made on: same file, and the request's
// position inside the location's range. The end is INCLUSIVE — the caret
// sits past the last rune of a word you just typed or clicked the tail
// of, and WordRange's courtesy for that case is the one every other
// symbol verb here extends.
//
// The range is measured against the tab that asked, since a Range is
// UTF-16 columns and only that buffer can turn them into rune columns.
// A tab that has since closed cannot be "here" — the caller then falls
// through to a plain jump, which reopens it.
func (a *App) definitionIsHere(e *lspDefinitionEvent, target string) bool {
	if target != e.fromPath {
		return false
	}
	var t *editor.Tab
	for _, tab := range a.tabs {
		if tab.Path == e.fromPath {
			t = tab
			break
		}
	}
	if t == nil || t.Buffer == nil {
		return false
	}
	start := editorPosFor(t, e.locs[0].Range.Start)
	end := editorPosFor(t, e.locs[0].Range.End)
	return !editor.PosLess(e.fromPos, start) && !editor.PosLess(end, e.fromPos)
}

// isMetaClick reports whether a mouse event's modifiers spell ⌘: a real
// ModMeta, or the CTRL+ALT pair cats' encoder substitutes for it because
// the mouse wire has no Super bit (metakeys.go's header has the wire
// details). Ctrl alone and Alt alone are deliberately NOT ⌘ — Alt+click
// is multicaret's, and Ctrl+click is left unbound so a host that CAN
// deliver a plain Ctrl+click keeps a modified click of its own.
func isMetaClick(mods tcell.ModMask) bool {
	if mods&tcell.ModMeta != 0 {
		return true
	}
	return mods&(tcell.ModCtrl|tcell.ModAlt) == tcell.ModCtrl|tcell.ModAlt
}

// editorGoToPress is ⌘+click: the definition verb at the pointer. The
// caret is placed FIRST, so the request asks about the symbol under the
// pointer rather than wherever the cursor was, and so a click on a file
// with no language server still does what a click does. It starts no
// drag, for Alt+click's reason: the press was a verb, and a stray wiggle
// afterwards must not turn it into a selection.
//
// The verb is menuGoToDefinition itself, not a copy: at a declaration the
// same click therefore opens the usages, which is the whole gesture.
func (a *App) editorGoToPress(x, y int) bool {
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() || tab.IsMarkdownView() {
		return false
	}
	ex, ey, ew, eh := a.editorRect()
	pos, ok := tab.HitTest(x-ex, y-ey, ew, eh)
	if !ok {
		return false
	}
	tab.MoveCursorTo(pos, false)
	if !a.hasLSPActions() {
		a.flash("Go to definition: no language server for this file")
		return true
	}
	a.menuGoToDefinition()
	return true
}

// -----------------------------------------------------------------------------
// Hover
// -----------------------------------------------------------------------------

// menuHoverInfo fires an async hover request for the symbol under the
// cursor; the response lands as an lspHoverEvent.
func (a *App) menuHoverInfo() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil {
		return
	}
	// No server for this file does NOT mean nothing to say. ced's own
	// validator and the plugin layer both produce diagnostics for files
	// no language server handles — a .json file is the whole point —
	// and this key is the only keyboard path to their message short of
	// opening the Problems panel. Answering with the diagnostics alone
	// is the same fallback handleLSPHover already makes when a server
	// has no hover text for the spot; the only thing that changes here
	// is that there may be no server to ask in the first place.
	if !a.hasLSPActions() {
		if dl := a.diagLinesAtCaret(); len(dl) > 0 {
			a.openModal(&hoverModal{lines: dl})
		}
		// Still silent when there is nothing at all to report, which is
		// what this branch did before diagnostics had other producers.
		return
	}
	client := a.lspClientFor(t.Path)
	scr := a.screen
	path := t.Path
	pos := lspPosFor(t, t.Cursor)
	a.lspFlushChange(t)
	go func() {
		h, err := client.HoverAt(path, pos)
		text, markdown := "", false
		if h != nil {
			text, markdown = h.Markup()
		}
		_ = scr.PostEvent(&lspHoverEvent{when: time.Now(), path: path, text: text, markdown: markdown, err: err})
	}()
}

// handleLSPHover lands a hover response: open the near-cursor modal,
// or flash when the server has nothing to say. A response for a tab
// the user has already left is dropped — popping a modal about a
// different file would be disorienting.
func (a *App) handleLSPHover(e *lspHoverEvent) {
	// A pointer-driven answer belongs to the ambient tooltip, which has
	// its own staleness question (has the mouse moved?) and its own
	// answer to an empty response (say nothing).
	if e.dwell {
		a.handleHoverDwellResult(e)
		return
	}
	t := a.activeTabPtr()
	if t == nil || t.Path != e.path {
		return
	}
	var lines []string
	if e.err == nil {
		lines = hoverLines(e.text, e.markdown)
	}
	// Diagnostics under the caret lead the tooltip: on a red line, "what
	// is wrong here" is the question the key was most likely pressed to
	// answer, and it is the only keyboard path to the message short of
	// opening the Problems panel. They also answer ALONE when the server
	// has no hover text for the spot (a caret on punctuation, a column-0
	// landing after a Problems jump). See diagtip.go.
	if dl := a.diagLinesAtCaret(); len(dl) > 0 {
		if len(lines) > 0 {
			dl = append(dl, "")
		}
		// Both halves are already capped (diagTipMaxLines, hoverLines'
		// own limit), so the sum needs no cap of its own.
		lines = append(dl, lines...)
	}
	if len(lines) == 0 {
		a.flash("No hover info")
		return
	}
	a.openModal(&hoverModal{lines: lines})
}

// hoverLines flattens hover text for the modal: markdown code fences
// dropped (the modal is monospace already — fence markers are pure
// noise), prose paragraphs re-joined (hoverReflow), markdown's inline
// syntax rendered down to plain text when the server sent markdown,
// trailing blank lines trimmed, and length capped so a huge doc comment
// can't swallow the screen.
//
// The cap counts ROWS as the tooltip will wrap them at its widest
// (hoverModalTextWidth), not source lines: once a paragraph is one long
// line, "12 lines" would say nothing about how tall the box gets. A
// paragraph that straddles the cap is cut at a row boundary, so the
// reader keeps every row that fit rather than losing the whole thing.
func hoverLines(text string, markdown bool) []string {
	const maxRows = 16
	if strings.TrimSpace(text) == "" {
		return nil
	}
	out := hoverReflow(strings.Split(text, "\n"), markdown)
	// Trim leading/trailing blanks left behind by dropped fences.
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}

	segs := make([][]tooltipSeg, len(out))
	total := 0
	for i, ln := range out {
		segs[i] = wrapTooltipLine([]rune(ln), hoverModalTextWidth)
		total += len(segs[i])
	}
	if total <= maxRows {
		return out
	}
	// Cutting: one row is reserved for the marker, so the budget for
	// text is a row short — and an over-long first line still leaves
	// room to say it was cut.
	budget, rows := maxRows-1, 0
	for i, ln := range out {
		if rows+len(segs[i]) <= budget {
			rows += len(segs[i])
			continue
		}
		kept := out[:i:i]
		if fit := budget - rows; fit > 0 {
			kept = append(kept, string([]rune(ln)[:segs[i][fit-1].end]))
		}
		return append(kept, "…")
	}
	return out // unreachable: total > maxRows means some line overflows
}

// hoverReflow drops fence markers and re-joins the soft line breaks of
// markdown prose. Servers hand doc comments over with the SOURCE's line
// breaks (gopls' plaintext: ~70 columns; other servers' markdown too),
// which in markdown are soft — the paragraph is one unit. Wrapped again
// at the tooltip's narrower width each of those breaks would leave an
// orphan row of one or two words:
//
//	source (70 cols)                 tooltip (62 cols), not reflowed
//	"…to textW columns and carries any"  → "…to textW columns and"
//	"emphasis runs along onto the rows…"   "carries any"          ← orphan
//	                                       "emphasis runs along…"
//
// Only plain prose joins. Anything whose line shape carries meaning is
// kept as-is: fenced code (the signature), indented code, list items,
// headings, quotes, tables, and a markdown hard break (a line ending in
// two spaces or a backslash).
//
// The two content kinds differ in where the structure comes from:
//
//   - MARKDOWN (what ced asks for first) carries it in syntax. gopls
//     fences the signature and a type's method list, splits sections
//     with `---`, and sends each doc paragraph as one line already. A
//     thematic break becomes a blank line, runs of blanks collapse to
//     one, and every line that is not code is rendered through the
//     preview's inline scanner (hoverInline): escapes resolved, links
//     reduced to their text, code-span backticks dropped. Rendering
//     happens AFTER joining, so a link or code span a server broke over
//     two source lines is whole again when it is read.
//   - PLAINTEXT (servers that ignore the preference) has no fence and no
//     blank line between the signature and the doc — gopls separates its
//     sections with a single newline. So the text's first line, when it
//     is not fenced, is taken to be the header and never absorbs the
//     next line; and a line ending like code (`{`, `}`, `;` — a type
//     declaration's body) never absorbs one either. A doc line that
//     happens to end that way just stays unjoined, which costs a short
//     row, never a mangled signature. Markdown needs neither guess, so
//     neither applies to it. Plaintext text is shown literally: a
//     backslash or bracket in it is the server's own character.
func hoverReflow(src []string, markdown bool) []string {
	var out []string
	// render[i] marks out[i] as markdown text still to go through
	// hoverInline — false for fenced and indented code, whose characters
	// are literal, and for every line of a plaintext answer.
	var render []bool
	inFence := false
	joinable := false // may the next prose line join out[len(out)-1]?
	for _, raw := range src {
		header := len(out) == 0 // first line of the text, fenced or not
		if strings.HasPrefix(strings.TrimSpace(raw), "```") {
			inFence = !inFence
			joinable = false
			continue
		}
		ln := strings.TrimRight(raw, " \t")
		if inFence {
			out = append(out, ln)
			render = append(render, false)
			continue
		}
		if markdown && hoverIsRule(ln) {
			// A section break reads as a paragraph break; drawing the
			// dashes would spend a row on a line a blank already says.
			ln = ""
		}
		prose := hoverIsProse(ln)
		if markdown && prose {
			// A line that is one link and nothing else is a footer
			// ("`strings` on pkg.go.dev") — its own line, never glued
			// onto the sentence above, as the bare URL of the plaintext
			// form already is.
			if _, _, lone := editor.MarkdownLinkOnly(ln); lone {
				prose = false
			}
		}
		hardBreak := strings.HasSuffix(raw, "  ") || strings.HasSuffix(ln, "\\")
		if markdown && hardBreak {
			// The break is honoured by not joining; the backslash that
			// asked for it is syntax, not text.
			ln = strings.TrimSuffix(ln, "\\")
		}
		if prose && joinable {
			out[len(out)-1] += " " + ln
		} else {
			out = append(out, ln)
			render = append(render, markdown && !hoverIsIndentedCode(ln))
		}
		if markdown {
			joinable = prose && !hardBreak
			continue
		}
		codeEnd := strings.HasSuffix(ln, "{") || strings.HasSuffix(ln, "}") || strings.HasSuffix(ln, ";")
		joinable = prose && !hardBreak && !codeEnd && !header
	}
	if !markdown {
		return out
	}
	// Render, then collapse blank runs: "\n\n---\n\n" between gopls'
	// sections is three blank lines by now, and markdown reads any run
	// of them as one break. A blank inside a fence is exempt — it is part
	// of the code — and is the only kind of blank render leaves false.
	kept := out[:0]
	for i, ln := range out {
		if !render[i] {
			kept = append(kept, ln)
			continue
		}
		ln = hoverInline(ln)
		if ln == "" && len(kept) > 0 && kept[len(kept)-1] == "" {
			continue
		}
		kept = append(kept, ln)
	}
	return kept
}

// hoverInline renders one markdown line to what the tooltip shows.
// Leading indentation (a nested list item) is kept as it is; a heading
// loses its hashes; a line that is only an http(s) link shows its URL,
// since in a footer the destination is the content and the label
// ("`json.Unmarshal` on pkg.go.dev") only says where it points; any
// other link shows its label (a doc link's file:// destination is
// noise). The rest is the preview's own inline scanner.
func hoverInline(ln string) string {
	body := strings.TrimLeft(ln, " \t")
	indent := ln[:len(ln)-len(body)]
	if _, dest, ok := editor.MarkdownLinkOnly(body); ok &&
		(strings.HasPrefix(dest, "https://") || strings.HasPrefix(dest, "http://")) {
		return indent + dest
	}
	if h := strings.TrimLeft(body, "#"); h != body && len(body)-len(h) <= 6 && strings.HasPrefix(h, " ") {
		body = strings.TrimSpace(h)
	}
	return indent + editor.MarkdownInlineText(body)
}

// hoverIsRule reports whether a markdown line is a thematic break:
// three or more of one of - * _, optionally spaced ("---", "* * *").
func hoverIsRule(ln string) bool {
	t := strings.ReplaceAll(strings.TrimSpace(ln), " ", "")
	if len(t) < 3 || !strings.ContainsRune("-*_", rune(t[0])) {
		return false
	}
	return strings.Count(t, t[:1]) == len(t)
}

// hoverIsIndentedCode reports whether a markdown line is an indented
// code block line (a tab, or four spaces), whose text is literal. A list
// item indented by two spaces is not — gopls nests its bullets that way.
func hoverIsIndentedCode(ln string) bool {
	return strings.HasPrefix(ln, "\t") || strings.HasPrefix(ln, "    ")
}

// hoverIsProse reports whether a markdown line is plain paragraph text —
// the only kind hoverReflow will join to its neighbour. Blank lines end
// a paragraph; leading whitespace means indented code or a list item's
// body; the rest are block markers whose line breaks are the structure.
func hoverIsProse(ln string) bool {
	if ln == "" || ln[0] == ' ' || ln[0] == '\t' {
		return false
	}
	// A bare URL (gopls' plaintext pkg.go.dev link) is its own line: glued
	// onto the doc's last sentence it would read as part of it.
	for _, p := range []string{"#", ">", "|", "- ", "* ", "+ ", "---", "===", "http://", "https://"} {
		if strings.HasPrefix(ln, p) {
			return false
		}
	}
	// Ordered list item: digits then ". " or ") ".
	i := 0
	for i < len(ln) && ln[i] >= '0' && ln[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(ln) && (ln[i] == '.' || ln[i] == ')') && ln[i+1] == ' ' {
		return false
	}
	return true
}

// -----------------------------------------------------------------------------
// Diagnostics — decoration source + status bar summary
// -----------------------------------------------------------------------------

// diagSeverityColor maps an LSP severity to its theme color. Unknown /
// omitted severities are treated as errors per the spec.
func diagSeverityColor(th theme.Theme, severity int) tcell.Color {
	switch severity {
	case lsp.SeverityWarning:
		return th.DiagWarning
	case lsp.SeverityInfo, lsp.SeverityHint:
		return th.DiagInfo
	default:
		return th.DiagError
	}
}

// lspDiagSource adapts App.lsp.diags into decorations: an underline
// span per diagnostic plus one gutter dot per afflicted line. It
// registers after gitDiffSource so a diagnostic dot outranks a git
// mark on the same line — "this line is broken" matters more than
// "this line changed".
type lspDiagSource struct {
	app *App
}

// Decorations emits the visible window's diagnostic spans and marks.
// Line indexes are validated against the live buffer because
// diagnostics lag edits by a debounce + type-check — a stale range
// past EOF must cull, not panic.
func (s lspDiagSource) Decorations(t *editor.Tab, th theme.Theme, firstLine, lastLine int) ([]editor.Span, []editor.GutterMark) {
	diags := s.app.lsp.diags[t.Path]
	if len(diags) == 0 {
		return nil, nil
	}
	var spans []editor.Span
	// Track the worst (lowest-numbered) severity per marked line so
	// one gutter cell summarises overlapping diagnostics honestly.
	lineSev := map[int]int{}
	for _, d := range diags {
		start, end := d.Range.Start.Line, d.Range.End.Line
		if end < firstLine || start > lastLine || start >= t.Buffer.LineCount() {
			continue
		}
		sev := d.Severity
		if sev == 0 {
			sev = lsp.SeverityError
		}
		sp := editor.Span{
			Start: editorPosFor(t, d.Range.Start),
			End:   editorPosFor(t, d.Range.End),
			Delta: editor.StyleDelta{Underline: true, SetFG: true, FG: diagSeverityColor(th, sev)},
		}
		// A zero-width range (some servers point at a single position)
		// still deserves a visible cell — stretch it one rune right.
		if sp.Start == sp.End {
			sp.End.Col++
		}
		spans = append(spans, sp)
		if cur, ok := lineSev[start]; !ok || sev < cur {
			lineSev[start] = sev
		}
	}
	var marks []editor.GutterMark
	for line, sev := range lineSev {
		if line < firstLine || line > lastLine {
			continue
		}
		marks = append(marks, editor.GutterMark{Line: line, Glyph: '●', FG: diagSeverityColor(th, sev)})
	}
	return spans, marks
}

// diagCounts tallies the active tab's diagnostics by bucket for the
// status bar: errors, warnings, and everything milder lumped together.
func (a *App) diagCounts() (errs, warns, infos int) {
	t := a.activeTabPtr()
	if t == nil {
		return 0, 0, 0
	}
	// Every producer (diagmerge.go): the counts in the status bar and the
	// gutter marks beside them must describe the same set, or the bar
	// reads as undercounting what is visibly on screen.
	for _, d := range a.diagsFor(t.Path) {
		switch d.Severity {
		case lsp.SeverityWarning:
			warns++
		case lsp.SeverityInfo, lsp.SeverityHint:
			infos++
		default:
			errs++
		}
	}
	return errs, warns, infos
}

// diagStatusSuffix renders the status-bar summary (" · ✗ 2 ⚠ 1"), or
// "" when the active tab is clean — the common case stays uncluttered.
func (a *App) diagStatusSuffix() string {
	errs, warns, infos := a.diagCounts()
	if errs == 0 && warns == 0 && infos == 0 {
		return ""
	}
	// One " · " lead-in, then each non-zero bucket with its glyph.
	var b strings.Builder
	b.WriteString(" ·")
	if errs > 0 {
		fmt.Fprintf(&b, " ✗ %d", errs)
	}
	if warns > 0 {
		fmt.Fprintf(&b, " ⚠ %d", warns)
	}
	if infos > 0 {
		fmt.Fprintf(&b, " ℹ %d", infos)
	}
	return b.String()
}

// initOptionsOrNil keeps a nil map from becoming a typed non-nil `any`,
// which would put `"initializationOptions": null` on the wire for every
// server that has none.
func initOptionsOrNil(m map[string]any) any {
	if len(m) == 0 {
		return nil
	}
	return m
}
