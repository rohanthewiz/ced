// =============================================================================
// File: internal/app/toolwindow.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// toolwindow.go is the editor's TOOL WINDOW layer — the JetBrains idea,
// which is that every auxiliary surface (the file tree, the git panels,
// the terminal, the chat, the problems list, the compare view) is the
// same KIND of thing: a named panel that lives on one of the window's
// three edges, can be moved to another edge, and is remembered per
// project.
//
// WHY THIS EXISTS. Before it, each panel owned its own geometry AND its
// own exclusivity rules, and the two were spelled out again in every
// file: the terminal knew it had to close the git panel, the git panel
// knew it had to close the terminal, the compare panel knew about all
// four, `termDockLeft` flipped the FILE TREE to the other edge, and the
// only way to learn any of it was to read six files. That worked while
// there were two panels and one axis. It could not answer "put the git
// panel on the right", because nothing in the editor had a notion of a
// right edge at all. So the rules move here, once, and the panels keep
// only what is theirs: what they draw and what their rows do.
//
// THE MODEL, in three sentences:
//
//   - Every tool is ASSIGNED to exactly one edge (left, right, bottom).
//     That assignment is the layout, and it is what gets remembered.
//   - An edge shows AT MOST ONE of its tools at a time. That is
//     JetBrains' own rule, and it is also the rule ced already had (the
//     bottom strip was single-occupancy, so was the left edge) — it is
//     just now stated once instead of six times. Two resizable panels on
//     one edge would need circular clamp math on a small window, and the
//     stripe makes switching one click.
//   - The others on that edge are COLLAPSED to their stripe buttons
//     (toolstripe.go), which is what keeps them findable.
//
// WHAT AN EDGE IS WORTH, in columns and rows:
//
//	┌─┬────────┬────────────────────────────────┬─┐
//	│ │        │ tab bar                        │ │
//	│s│  left  ├────────────────────────────────┤s│   s = stripe (1 cell)
//	│t│  dock  │                                │t│
//	│r│        │ editor                         │r│
//	│i│        │                                │i│
//	│p├────────┴────────────────────────────────┤p│
//	│e│ bottom dock                             │e│
//	│ ├─────────────────────────────────────────┤ │
//	│ │ find bar                                │ │
//	├─┴─────────────────────────────────────────┴─┤
//	│ bottom stripe                               │
//	├─────────────────────────────────────────────┤
//	│ status bar                                  │
//	└─────────────────────────────────────────────┘
//
// The vertical stripes are OUTERMOST and full height, so they never
// move: a button that shifted a row when the find bar opened would be a
// button you had to look for. The left and right docks run the full
// height between the tab bar row and the bottom chrome, and the bottom
// dock spans between them — the arrangement ced already had for a
// left-docked terminal, now stated for all three edges at once.
//
// SIZES ARE PER TOOL, PER AXIS, not per edge. A file tree wants ~30
// columns and a terminal wants ~60, so an edge-wide width would make
// every switch a resize. Zero means "auto" and each tool's own
// height/width helper derives it — those helpers are unchanged, they
// just read their stored number from here instead of from a field on
// their own state struct.
//
// WHAT IS DELIBERATELY NOT A TOOL WINDOW. The Find-all list (findall.go)
// is a PEEK, and its own file explains at length why it is not a picker
// either: moving its highlight moves the editor's cursor live, Esc puts
// the view back, and it docks TOP or right — an axis no tool window has.
// Folding it in here would mean giving every tool window a preview hook
// and a cancel that undoes. The find bar is not one for the same kind of
// reason: it owns the keyboard while it is open and belongs to the tab,
// not to the window.

package app

import "sort"

// -----------------------------------------------------------------------------
// Ids and edges
// -----------------------------------------------------------------------------

// toolID names one tool window. It is a string rather than an int
// because it is PERSISTED (state.json, per project) and read by people
// when they look at that file — and because a reordered const block must
// never silently move somebody's git panel to the left edge.
type toolID string

const (
	toolProject  toolID = "project"  // the file tree
	toolGit      toolID = "git"      // the changes panel
	toolGitLog   toolID = "gitlog"   // the history browser
	toolProblems toolID = "problems" // the diagnostics worklist
	toolCompare  toolID = "compare"  // the diff panel
	toolTerminal toolID = "terminal" // the embedded grsh strip
	toolChat     toolID = "chat"     // the ACP chat panel
)

// dockSide names one of the window's three edges. Same string-not-int
// argument as toolID: these values go to disk.
type dockSide string

const (
	dockNone   dockSide = ""       // "nowhere" — the zero value, never stored
	dockLeft   dockSide = "left"   //
	dockRight  dockSide = "right"  //
	dockBottom dockSide = "bottom" //
)

// dockSides is the enumeration order every surface walks the edges in —
// the stripe layout, the ≡ move rows, the persisted layout. One order,
// so two surfaces can never disagree about which edge comes first.
var dockSides = []dockSide{dockLeft, dockRight, dockBottom}

// validDock reports whether s names a real edge. Anything else (a
// hand-edited state.json, a layout written by a future version that
// grew a fourth edge) resolves to the tool's default rather than being
// rejected — the theme registry's per-item degradation rule.
func validDock(s dockSide) bool {
	return s == dockLeft || s == dockRight || s == dockBottom
}

// dockIsVertical reports whether an edge's docks are sized in COLUMNS.
// The one place the two axes are told apart, so a new edge would have
// exactly one thing to answer.
func dockIsVertical(s dockSide) bool { return s == dockLeft || s == dockRight }

// dockLabel is the human name of an edge, for menu rows and flashes.
func dockLabel(s dockSide) string {
	switch s {
	case dockLeft:
		return "left"
	case dockRight:
		return "right"
	case dockBottom:
		return "bottom"
	}
	return "nowhere"
}

// -----------------------------------------------------------------------------
// The registry
// -----------------------------------------------------------------------------

// toolDef is the STATIC half of a tool window: what it is called, what
// it looks like on a stripe, where it lives before the user says
// otherwise, how small it may get, and the four hooks that connect this
// layer to the panel's own file.
//
// The hooks are deliberately thin. `isOpen` and `setOpen` read and write
// the panel's OWN visibility flag (a.gitPanel.open, a.sidebarShown, …)
// rather than this layer keeping a second copy of it — a duplicate would
// be one more thing that can drift, and every existing call site and
// test that reads those fields keeps working unchanged. `show` and
// `hide` are the panel's real entry points, which do the machinery
// around the flag (start a shell, connect an agent, refresh a status).
type toolDef struct {
	id      toolID
	title   string   // "Project", "Git", … — stripe tooltip, menu rows.
	glyph   rune     // the stripe button. Single-width, per the marker rule.
	defDock dockSide // where it sits in a layout nobody has touched.

	// minW / minH are the tool's own floors on each axis. They are the
	// constants each panel already declared; collecting them here is
	// what lets ONE clamp serve every tool on every edge.
	minW, minH int

	// autoW / autoH are the "unsized" defaults — what the tool asks for
	// when the layout has no remembered number for that axis. A function
	// rather than a constant because most of them are a fraction of the
	// window.
	autoW, autoH func(*App) int

	// isOpen / setOpen are the panel's visibility flag, read and written.
	// setOpen is used for the CLOSE half of exclusivity (evicting the
	// tool an edge is handing over) and must not do machinery — hide
	// does that.
	isOpen  func(*App) bool
	setOpen func(*App, bool)

	// show / hide are the tool's real open and close verbs, side effects
	// and all. show may refuse (an agent with no binary): it reports
	// whether the tool actually came up, and the stripe reads that so a
	// button cannot latch on over a panel that never opened.
	show func(*App) bool
	hide func(*App)
}

// toolDefs is the inventory, in the order tools appear on a stripe and
// in the ≡ menu. Project leads because it is the one open by default and
// the one every project has; the git pair, the worklist and the compare
// view follow in the order their menu groups already sit in; the two
// long-running processes come last, which is also where a user reaches
// for them least often.
//
// A tool's DEFAULT edge is where it was before this file existed, with
// one deliberate change: chat now defaults to the RIGHT edge rather than
// stealing the left one and flipping the file tree across the window.
// That flip was the workaround for having no right edge, and this is the
// thing that replaces it.
//
// toolDefs is filled by init rather than by a composite literal, and
// that is not a style choice: the hooks below call methods that
// eventually read toolDefs itself (a panel's rect asks leftBlockW, which
// asks which tools are on the left edge), and Go's initialisation-cycle
// detector rejects a package-level literal that closes such a loop. The
// cycle is only in the DEPENDENCY GRAPH — nothing here runs at init time
// — so deferring the assignment by one step is the whole fix.
var toolDefs []toolDef

func init() {
	toolDefs = []toolDef{
		{
			id: toolProject, title: "Project", glyph: '▤', defDock: dockLeft,
			minW: minSidebarWidth, minH: toolMinBottomRows,
			autoW:   func(a *App) int { return defaultSidebarWidth },
			autoH:   func(a *App) int { return a.height / 3 },
			isOpen:  func(a *App) bool { return a.sidebarShown },
			setOpen: func(a *App, v bool) { a.sidebarShown = v },
			show:    func(a *App) bool { a.sidebarShown = true; return true },
			hide:    func(a *App) { a.sidebarShown = false; a.treeFocus = false },
		},
		{
			id: toolGit, title: "Git", glyph: '⎇', defDock: dockBottom,
			// On a vertical edge the floor is the LIST pane's own, plus
			// the seam columns — not list + diff, which would be 67
			// columns and make the panel undockable beside anything
			// else on a laptop. The Find-all dock's precedence rule
			// says why that is the right half to protect: the list is
			// what you steer the diff with, and gitPanelListWidth
			// already compresses the diff pane to nothing rather than
			// refusing.
			minW: gitPanelMinListW + 4, minH: gitPanelMinHeight,
			autoW:   func(a *App) int { return a.width / 3 },
			autoH:   func(a *App) int { return minInt(a.height/3, gitPanelMaxHeight) },
			isOpen:  func(a *App) bool { return a.gitPanel.open },
			setOpen: func(a *App, v bool) { a.gitPanel.open = v },
			show:    func(a *App) bool { a.openGitPanelTool(); return true },
			hide:    func(a *App) { a.closeGitPanelTool() },
		},
		{
			id: toolGitLog, title: "Git log", glyph: '⑂', defDock: dockBottom,
			// The commit list's floor plus the seam — see the git
			// panel above for why the DETAIL pane's reserve is not
			// part of a vertical dock's floor.
			minW: gitLogMinListW + 4, minH: gitLogMinHeight,
			autoW:   func(a *App) int { return a.width / 3 },
			autoH:   func(a *App) int { return minInt(a.height/3, gitLogMaxHeight) },
			isOpen:  func(a *App) bool { return a.gitLog.open },
			setOpen: func(a *App, v bool) { a.gitLog.open = v },
			show:    func(a *App) bool { a.openGitLogTool(); return true },
			hide:    func(a *App) { a.closeGitLogTool() },
		},
		{
			id: toolProblems, title: "Problems", glyph: '⚠', defDock: dockBottom,
			minW: toolMinSideCols, minH: problemsMinHeight,
			autoW:   func(a *App) int { return a.width / 3 },
			autoH:   func(a *App) int { return minInt(a.height/3, problemsMaxHeight) },
			isOpen:  func(a *App) bool { return a.problems.open },
			setOpen: func(a *App, v bool) { a.problems.open = v },
			show:    func(a *App) bool { a.openProblemsTool(); return true },
			hide:    func(a *App) { a.closeProblemsTool() },
		},
		{
			id: toolCompare, title: "Compare", glyph: '⇄', defDock: dockBottom,
			minW: toolMinSideCols, minH: comparePanelMinHeight,
			autoW:   func(a *App) int { return a.width / 3 },
			autoH:   func(a *App) int { return minInt(a.height/3, comparePanelMaxHeight) },
			isOpen:  func(a *App) bool { return a.compare.open },
			setOpen: func(a *App, v bool) { a.compare.open = v },
			// Compare has no "open it empty" verb: the panel is a diff of
			// two named sides, so opening one without them would be a box
			// saying nothing. The stripe button therefore runs the same
			// picker the ≡ Compare group's rows do.
			show: func(a *App) bool { return a.showCompareTool() },
			hide: func(a *App) { a.closeComparePanel() },
		},
		{
			id: toolTerminal, title: "Terminal", glyph: '❯', defDock: dockBottom,
			minW: termPanelMinWidth, minH: termPanelMinHeight,
			autoW:   func(a *App) int { return a.width / 3 },
			autoH:   func(a *App) int { return minInt(a.height/3, termPanelMaxHeight) },
			isOpen:  func(a *App) bool { return a.term.open },
			setOpen: func(a *App, v bool) { a.term.open = v },
			show:    func(a *App) bool { return a.showTerminalTool() },
			hide:    func(a *App) { a.hideTerminalTool() },
		},
		{
			id: toolChat, title: "Chat", glyph: '✦', defDock: dockRight,
			minW: chatPanelMinWidth, minH: toolMinBottomRows,
			autoW:   func(a *App) int { return a.width / 3 },
			autoH:   func(a *App) int { return a.height / 3 },
			isOpen:  func(a *App) bool { return a.chat.open },
			setOpen: func(a *App, v bool) { a.chat.open = v },
			show:    func(a *App) bool { return a.showChatTool() },
			hide:    func(a *App) { a.chatClosePanel() },
		},
	}
}

const (
	// toolMinSideCols is the floor for a tool that had no width of its
	// own because it had never been dockable to a vertical edge. It is
	// the sidebar's floor: the narrowest strip anybody has judged
	// readable in this editor.
	toolMinSideCols = minSidebarWidth

	// toolMinBottomRows is the same for a tool that had no height —
	// enough for a header rule and a few rows of content.
	toolMinBottomRows = 5

	// toolMinEditorCols / toolMinEditorRows are what the EDITOR keeps
	// when a dock is clamped. Stated as the neighbour's reserve rather
	// than as a cap of the dock's own, per the splitter house rule: a
	// fixed cap makes a pane unable to grow on the very wide terminal
	// where a drag is reached for.
	toolMinEditorCols = minEditorAfterDrag
	toolMinEditorRows = 5
)

// minInt is the two-argument minimum. Go's builtin `min` arrived in
// 1.21 and this package still states its own so the file reads the same
// as its neighbours.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// toolDefFor returns the registry entry for id, and whether there is
// one. An unknown id is a hand-edited state.json or a layout from a
// version that had a tool this one doesn't — it costs that entry, never
// the layout (the per-item degradation rule).
func toolDefFor(id toolID) (toolDef, bool) {
	for _, d := range toolDefs {
		if d.id == id {
			return d, true
		}
	}
	return toolDef{}, false
}

// toolTitle is the tool's display name, or its raw id when the registry
// has never heard of it — a label that says something beats a blank.
func toolTitle(id toolID) string {
	if d, ok := toolDefFor(id); ok {
		return d.title
	}
	return string(id)
}

// -----------------------------------------------------------------------------
// The layout state
// -----------------------------------------------------------------------------

// toolSize is one tool's remembered extent on each axis. Zero on an axis
// means "auto" — the tool's autoW/autoH decides — and that is the value
// a tool the user has never resized keeps forever, so a window that
// changes shape re-derives instead of preserving a number chosen for a
// different screen.
type toolSize struct {
	W int `json:"w,omitempty"`
	H int `json:"h,omitempty"`
}

// toolLayout is the MUTABLE half: where each tool currently sits and how
// big it is. It is the thing persisted per project (see toollayout.go).
//
// `dock` is deliberately sparse — an entry exists only for a tool whose
// edge differs from its default, so a layout nobody has rearranged
// serialises to almost nothing and a tool added in a later version lands
// on its own default rather than nowhere.
type toolLayout struct {
	dock map[toolID]dockSide
	size map[toolID]toolSize
}

// newToolLayout returns the default layout: every tool on its registry
// edge, nothing resized. This IS "a new project" — the file tree on the
// left and the editor taking the rest, because Project is the only tool
// whose default is open.
func newToolLayout() *toolLayout {
	return &toolLayout{
		dock: map[toolID]dockSide{},
		size: map[toolID]toolSize{},
	}
}

// tools returns the App's layout, creating the default on first use.
// Every reader goes through this rather than touching App.toolLayoutState
// directly, because tests build App as a struct literal (the newTestApp
// rule) and a nil map read is fine but a nil map WRITE panics.
func (a *App) tools() *toolLayout {
	if a.toolLayoutState == nil {
		a.toolLayoutState = newToolLayout()
	}
	return a.toolLayoutState
}

// toolDock reports which edge a tool lives on: the layout's entry, or
// the registry default when the layout has nothing to say (which is the
// common case — see toolLayout.dock).
func (a *App) toolDock(id toolID) dockSide {
	if a.toolLayoutState != nil {
		if s, ok := a.toolLayoutState.dock[id]; ok && validDock(s) {
			return s
		}
	}
	if d, ok := toolDefFor(id); ok {
		return d.defDock
	}
	return dockBottom
}

// toolsOn returns every tool assigned to an edge, in registry order.
// This is what a stripe draws and what the "one visible per edge" rule
// is enforced over.
func (a *App) toolsOn(side dockSide) []toolID {
	var out []toolID
	for _, d := range toolDefs {
		if a.toolDock(d.id) == side {
			out = append(out, d.id)
		}
	}
	return out
}

// toolOpen reports whether a tool's own panel is showing.
func (a *App) toolOpen(id toolID) bool {
	d, ok := toolDefFor(id)
	if !ok {
		return false
	}
	return d.isOpen(a)
}

// visibleTool reports which of an edge's tools is currently showing, and
// whether any is. The invariant is "at most one", enforced by claimDock;
// this reports the FIRST in registry order, so even a state that somehow
// broke the invariant (a test setting two .open flags by hand) still
// produces one answer rather than an inconsistent layout.
func (a *App) visibleTool(side dockSide) (toolID, bool) {
	for _, id := range a.toolsOn(side) {
		if a.toolOpen(id) {
			return id, true
		}
	}
	return "", false
}

// -----------------------------------------------------------------------------
// Show / hide / move — the verbs
// -----------------------------------------------------------------------------

// claimDock evicts whatever else is showing on `id`'s edge, so the
// caller can show it. This is the single statement of the
// single-occupancy rule that used to be spelled out in six panel files,
// and it is why those files no longer close each other by name.
//
// Eviction uses the registry's `hide` (the real close verb) rather than
// setOpen, because a tool being pushed off an edge should shut down the
// same way it does when the user closes it — the terminal keeps its
// session, the chat keeps its transcript, but focus and drags are
// dropped.
func (a *App) claimDock(id toolID) {
	side := a.toolDock(id)
	for _, other := range a.toolsOn(side) {
		if other == id || !a.toolOpen(other) {
			continue
		}
		if d, ok := toolDefFor(other); ok {
			d.hide(a)
		}
	}
}

// showTool brings a tool up on its edge, evicting whatever that edge was
// showing, and reports whether it actually came up. A tool may refuse
// (an agent with no binary on PATH, a compare with nothing to compare),
// and the caller — a stripe button above all — has to know, or the
// button would latch on over a panel that never opened.
func (a *App) showTool(id toolID) bool {
	d, ok := toolDefFor(id)
	if !ok {
		return false
	}
	if d.isOpen(a) {
		return true
	}
	a.claimDock(id)
	ok = d.show(a)
	a.saveToolLayout()
	return ok
}

// hideTool closes a tool through its own close verb. A no-op when it is
// already closed, so the stripe and the ≡ row can both call it blind.
func (a *App) hideTool(id toolID) {
	d, ok := toolDefFor(id)
	if !ok || !d.isOpen(a) {
		return
	}
	d.hide(a)
	a.saveToolLayout()
}

// toggleTool is the stripe button's whole gesture and the ≡ row's:
// showing means hide, hidden means show. Reports the state it left the
// tool in.
func (a *App) toggleTool(id toolID) bool {
	if a.toolOpen(id) {
		a.hideTool(id)
		return false
	}
	return a.showTool(id)
}

// moveTool re-docks a tool to another edge, keeping it visible if it
// was. The old edge is simply left with nothing showing (its other tools
// stay collapsed on their stripe — moving one tool is not a statement
// about the others), and the new edge is claimed the ordinary way.
//
// Moving a tool that is CLOSED is legal and does nothing visible: it is
// how a user arranges an edge before opening anything on it.
func (a *App) moveTool(id toolID, side dockSide) {
	if !validDock(side) {
		return
	}
	d, ok := toolDefFor(id)
	if !ok || a.toolDock(id) == side {
		return
	}
	wasOpen := d.isOpen(a)
	if wasOpen {
		// Take it off the screen first, so the edge it is leaving does
		// not briefly hold two visible tools and so the panel's own
		// close verb runs while its OLD geometry is still in force —
		// a drag or a focus flag released against the wrong rect is
		// the kind of bug that only shows up on somebody else's
		// terminal.
		d.setOpen(a, false)
	}
	a.setToolDock(id, side)
	if wasOpen {
		a.claimDock(id)
		d.setOpen(a, true)
	}
	a.clampToolSizes()
}

// setToolDock records an assignment, storing nothing when the tool is
// going back to its registry default — that is what keeps a layout
// nobody has rearranged serialising to almost nothing, and what lets a
// changed default reach users who never touched that tool.
func (a *App) setToolDock(id toolID, side dockSide) {
	t := a.tools()
	if d, ok := toolDefFor(id); ok && d.defDock == side {
		delete(t.dock, id)
		return
	}
	t.dock[id] = side
}

// resetToolLayout puts every tool back on its default edge at its
// default size and closes everything but the default-open Project tool —
// the ≡ "Reset layout" row, and the state a project with no remembered
// layout starts in.
func (a *App) resetToolLayout() {
	for _, d := range toolDefs {
		if d.isOpen(a) && d.id != toolProject {
			d.hide(a)
		}
	}
	a.toolLayoutState = newToolLayout()
	a.sidebarShown = true
	a.sidebarWidth = defaultSidebarWidth
}

// -----------------------------------------------------------------------------
// Sizes
// -----------------------------------------------------------------------------

// toolWidth is a tool's column count when docked to a vertical edge —
// the whole BLOCK, splitter column included. That is the convention
// every width in this editor already used (sidebarWidth has always
// counted the seam, and so did the terminal and chat strips), and
// keeping it means a stored number means the same thing before and after
// this rewrite. The panel's own rect is one column narrower; see
// toolRect. The
// stored number wins, auto falls back to the registry's autoW, and both
// are re-clamped against the live window on every call so a terminal
// resize can never leave a remembered width that squeezes the editor
// out. Same contract every panel's own width helper already had; it is
// stated once here so a tool moved to an edge it was never written for
// inherits it.
func (a *App) toolWidth(id toolID) int {
	d, ok := toolDefFor(id)
	if !ok {
		return 0
	}
	w := a.storedToolWidth(id)
	if w == 0 && d.autoW != nil {
		w = d.autoW(a)
	}
	return a.clampToolWidth(id, w)
}

// rawToolWidth is a tool's DESIRED column count — the stored number, or
// the registry's auto value — with no clamping at all. It exists for one
// caller: the clamp of the OPPOSITE edge, which needs to know what this
// edge wants without asking a question that would come straight back.
// See clampToolWidth.
func (a *App) rawToolWidth(id toolID) int {
	d, ok := toolDefFor(id)
	if !ok {
		return 0
	}
	if w := a.storedToolWidth(id); w != 0 {
		return w
	}
	if d.autoW != nil {
		return d.autoW(a)
	}
	return d.minW
}

// rawDockCols is dockCols measured through rawToolWidth; see it and
// clampToolWidth for why the unclamped form is the one an opposite edge
// may ask for.
func (a *App) rawDockCols(side dockSide) int {
	if !dockIsVertical(side) {
		return 0
	}
	id, ok := a.visibleTool(side)
	if !ok {
		return 0
	}
	return a.rawToolWidth(id)
}

// clampToolWidth pins a desired width into the legal band for the tool's
// current edge: never below the tool's own floor, never past what the
// editor needs beside it once the OTHER vertical edge (stripes and dock
// both) has taken its share.
//
// The floor is applied LAST and wins on a window too narrow for both —
// the Find-all dock's precedence rule, for its reason: a panel too
// narrow to read is worse than a cramped editor, and a zero-width panel
// reads as the feature being broken.
func (a *App) clampToolWidth(id toolID, want int) int {
	d, ok := toolDefFor(id)
	if !ok {
		return want
	}
	side := a.toolDock(id)
	// What the other side already spends, so the two docks cannot each
	// think they have the whole window to grow into.
	//
	// The other side is measured RAW — its stored or auto width, before
	// its own clamp. That is not laziness: dockCols would clamp, and its
	// clamp asks this side the same question back, which is a stack
	// overflow rather than a layout. Each edge therefore clamps against
	// the other's INTENT, and the only case the two can jointly overrun
	// is a window too narrow for both floors plus the editor's — where
	// the floor-wins rule below was always going to squeeze the editor
	// anyway.
	other := a.stripeCols(dockLeft) + a.stripeCols(dockRight)
	if side == dockLeft {
		other += a.rawDockCols(dockRight)
	} else {
		other += a.rawDockCols(dockLeft)
	}
	max := a.width - other - toolMinEditorCols
	if want > max {
		want = max
	}
	if want < d.minW {
		want = d.minW
	}
	return want
}

// toolHeight is a tool's row count when docked to the bottom edge, on
// the same contract as toolWidth.
func (a *App) toolHeight(id toolID) int {
	d, ok := toolDefFor(id)
	if !ok {
		return 0
	}
	h := 0
	if a.toolLayoutState != nil {
		h = a.toolLayoutState.size[id].H
	}
	if h == 0 && d.autoH != nil {
		h = d.autoH(a)
	}
	return a.clampToolHeight(id, h)
}

// clampToolHeight is toolWidth's twin on the row axis. What the bottom
// dock competes with is the editor's minimum plus every pinned strip
// below it: the status bar, the bottom stripe, and the find bar.
func (a *App) clampToolHeight(id toolID, want int) int {
	d, ok := toolDefFor(id)
	if !ok {
		return want
	}
	// The status bar, the tool stripe, the find bar, the tab bar, and the
	// editor's own reserve — everything the bottom edge is not allowed to
	// eat, in the order it stacks up the window.
	max := a.height - 1 - a.stripeRows() - a.findBarRows() - 1 - toolMinEditorRows
	if want > max {
		want = max
	}
	if want < d.minH {
		want = d.minH
	}
	return want
}

// storedToolWidth reads a tool's remembered column count, unclamped.
//
// It is a function rather than a map read because of ONE tool: the file
// tree's width lives on App.sidebarWidth, not in the layout's size map.
// That is not an oversight — auto-fit (treeautofit.go) re-derives that
// field on every frame, so it is the LIVE number, and a second copy here
// would be a number that could disagree with what is on screen. So the
// layer reads through to it, and the size map holds the tree's width
// only on the axis auto-fit has no opinion about (a bottom dock's rows).
func (a *App) storedToolWidth(id toolID) int {
	if id == toolProject {
		return a.sidebarWidth
	}
	if a.toolLayoutState == nil {
		return 0
	}
	return a.toolLayoutState.size[id].W
}

// setToolWidth / setToolHeight record a user-chosen extent (a splitter
// drag, a grow/shrink leader), clamped on the way in. They are the
// single write path for the stored numbers, which is what lets the
// layout be persisted without anyone having to remember to sync it.
func (a *App) setToolWidth(id toolID, w int) {
	w = a.clampToolWidth(id, w)
	if id == toolProject {
		// See storedToolWidth: the tree's live width is this field.
		a.sidebarWidth = w
		return
	}
	t := a.tools()
	s := t.size[id]
	s.W = w
	t.size[id] = s
}

// setToolHeight records a user-chosen row count; see setToolWidth.
func (a *App) setToolHeight(id toolID, h int) {
	t := a.tools()
	s := t.size[id]
	s.H = a.clampToolHeight(id, h)
	t.size[id] = s
}

// clampToolSizes re-clamps every stored extent against the current
// window. Called after a move and after a resize event, because a number
// legal on the edge a tool came from can be illegal on the one it
// arrived at — and because a stored number that is merely READ through
// the clamp would spring back the moment the window grew again, which
// reads as the panel resisting the drag.
func (a *App) clampToolSizes() {
	if a.toolLayoutState == nil {
		return
	}
	// A window with no size yet cannot answer what fits in it, and
	// clamping against one would floor every stored extent — which is
	// exactly what happened to a layout restored during New, before the
	// first resize event had told the App how big the terminal is. Reads
	// go through the clamp anyway (toolWidth / toolHeight), so skipping
	// the write-back here costs nothing and is what makes a remembered
	// size survive startup.
	if a.width <= 0 || a.height <= 0 {
		return
	}
	if a.sidebarWidth != 0 {
		a.sidebarWidth = a.clampToolWidth(toolProject, a.sidebarWidth)
	}
	for id, s := range a.toolLayoutState.size {
		if s.W != 0 {
			s.W = a.clampToolWidth(id, s.W)
		}
		if s.H != 0 {
			s.H = a.clampToolHeight(id, s.H)
		}
		a.toolLayoutState.size[id] = s
	}
}

// -----------------------------------------------------------------------------
// Geometry — the one source draw, hit-testing and every panel rect read
// -----------------------------------------------------------------------------

// dockCols is how many columns a vertical edge consumes: the visible
// tool's width plus the one-column splitter beside it, or zero when the
// edge shows nothing. It does NOT include the stripe — that is
// stripeCols, and the two are separate because a stripe is furniture
// that stays whether or not a panel is up.
func (a *App) dockCols(side dockSide) int {
	if !dockIsVertical(side) {
		return 0
	}
	id, ok := a.visibleTool(side)
	if !ok {
		return 0
	}
	return a.toolWidth(id)
}

// dockRows is how many rows the bottom edge's visible tool consumes.
func (a *App) dockRows() int {
	id, ok := a.visibleTool(dockBottom)
	if !ok {
		return 0
	}
	return a.toolHeight(id)
}

// toolRect returns a tool's on-screen content rectangle — the panel
// ITSELF, with the dock's splitter column already excluded. A tool that
// is not showing gets a zero rect, which is what every xxxContains
// helper needs to answer "no" without a second flag check.
//
// This is the single geometry source the whole feature turns on: each
// panel's own rect helper now delegates here, so a panel drawn on an
// edge it was never written for still lands where hit-testing looks for
// it.
func (a *App) toolRect(id toolID) (x, y, w, h int) {
	if !a.toolOpen(id) {
		return 0, 0, 0, 0
	}
	switch a.toolDock(id) {
	case dockLeft:
		// One column narrower than the block: the column nearest the
		// editor belongs to the resize seam.
		return a.stripeCols(dockLeft), 0, a.toolWidth(id) - 1, a.sideDockRows()
	case dockRight:
		// Mirrored — the seam is the block's LEFTMOST column, so the
		// panel starts one past it.
		w = a.toolWidth(id) - 1
		return a.width - a.stripeCols(dockRight) - w, 0, w, a.sideDockRows()
	case dockBottom:
		h = a.toolHeight(id)
		lw := a.leftBlockW()
		return lw, a.bottomDockTop(), a.width - lw - a.rightBlockW(), h
	}
	return 0, 0, 0, 0
}

// sideDockRows is how tall a left/right dock runs: from the top of the
// window down to the bottom chrome (the bottom stripe and the status
// bar). It deliberately runs PAST the bottom dock and the find bar, both
// of which sit inside the editor's column band — the arrangement ced
// already had for a left-docked terminal, now stated once.
func (a *App) sideDockRows() int {
	return a.height - 1 - a.stripeRows()
}

// bottomDockTop is the screen row the bottom dock's first row sits on.
// Everything pinned below it — the find bar, the bottom stripe, the
// status bar — is subtracted here, which is why no other file has to
// know the order they stack in.
func (a *App) bottomDockTop() int {
	return a.height - 1 - a.stripeRows() - a.findBarRows() - a.dockRows()
}

// toolSplitterX returns the screen column of the seam beside a vertical
// dock, or -1 when that edge shows nothing. Left docks put their seam on
// their right (between panel and editor); right docks on their left —
// both times, the rule stands BETWEEN the panel and the editor, which is
// what makes the drag mean the same thing on both edges.
func (a *App) toolSplitterX(side dockSide) int {
	if !dockIsVertical(side) {
		return -1
	}
	if _, ok := a.visibleTool(side); !ok {
		return -1
	}
	if side == dockLeft {
		return a.stripeCols(dockLeft) + a.dockCols(dockLeft) - 1
	}
	return a.width - a.stripeCols(dockRight) - a.dockCols(dockRight)
}

// -----------------------------------------------------------------------------
// Resize gestures
// -----------------------------------------------------------------------------

// dragDockTo resizes an edge's visible tool so its seam tracks the
// pointer. `pos` is the seam's target column (vertical edges) or row
// (the bottom edge), already corrected by dragSplitOffset at the call
// site — the splitter house rule, so a seam grabbed off-centre tracks
// from where it was actually seized instead of jumping under the mouse.
func (a *App) dragDockTo(side dockSide, pos int) {
	id, ok := a.visibleTool(side)
	if !ok {
		return
	}
	switch side {
	case dockLeft:
		a.resizeToolOnDock(id, pos-a.stripeCols(dockLeft)+1)
	case dockRight:
		a.resizeToolOnDock(id, a.width-a.stripeCols(dockRight)-pos)
	case dockBottom:
		a.resizeToolOnDock(id, a.height-1-a.stripeRows()-a.findBarRows()-pos)
	}
}

// resizeToolOnDock applies a new extent on whichever axis the tool's
// current edge is sized in. Every resize path — drag, leader, menu —
// funnels through here so an extent can never be written to the axis the
// tool is not currently using.
func (a *App) resizeToolOnDock(id toolID, want int) {
	if dockIsVertical(a.toolDock(id)) {
		a.setToolWidth(id, want)
		a.afterToolResize(id)
		return
	}
	a.setToolHeight(id, want)
	a.afterToolResize(id)
}

// afterToolResize lets a panel re-clamp whatever it derives from its own
// size — scroll offsets, mostly, which would otherwise be left pointing
// past the end of a viewport that just shrank. It is the one place this
// layer reaches back into the panels, and it does so by name because
// there is no honest generic version: what a shorter viewport invalidates
// is different for a diff pane, a scrollback and a commit list.
func (a *App) afterToolResize(id toolID) {
	switch id {
	case toolGit:
		a.gitPanelClampScrolls()
	case toolGitLog:
		a.gitLogClampScrolls()
	case toolTerminal:
		a.termAfterResize()
	case toolProblems:
		a.problemsClampScroll()
	}
}

// growDock / shrinkDock are the Esc-= / Esc-- targets, generalised: they
// step whichever tool the given edge is showing, on whichever axis that
// edge is sized in, so the leader keeps meaning "more of the thing I am
// looking at" wherever it has been docked.
func (a *App) growDock(side dockSide, step int) {
	id, ok := a.visibleTool(side)
	if !ok {
		return
	}
	if dockIsVertical(side) {
		a.resizeToolOnDock(id, a.toolWidth(id)+step)
		return
	}
	a.resizeToolOnDock(id, a.toolHeight(id)+step)
}

// shrinkDock steps the edge's tool smaller; see growDock.
func (a *App) shrinkDock(side dockSide, step int) { a.growDock(side, -step) }

// -----------------------------------------------------------------------------
// Reporting
// -----------------------------------------------------------------------------

// toolLayoutSummary is a stable one-line description of where everything
// sits, for the ≡ menu's label and for tests that want to assert on a
// whole layout without reaching into two maps. Sorted by edge (the
// dockSides order) then by registry order, so it can be compared.
func (a *App) toolLayoutSummary() string {
	out := ""
	for _, side := range dockSides {
		ids := a.toolsOn(side)
		if len(ids) == 0 {
			continue
		}
		names := make([]string, 0, len(ids))
		for _, id := range ids {
			n := toolTitle(id)
			if a.toolOpen(id) {
				n += "*"
			}
			names = append(names, n)
		}
		if out != "" {
			out += "  "
		}
		out += dockLabel(side) + ": " + joinStrings(names, ", ")
	}
	return out
}

// joinStrings is strings.Join without the import — this file otherwise
// needs none, and the helper keeps the summary readable.
func joinStrings(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// sortedToolIDs returns the ids of every registered tool in id order,
// which is what the persisted layout is written in — a map would
// round-trip in whatever order Go felt like, and a state file that
// changed shape on every save is one nobody can diff.
func sortedToolIDs() []toolID {
	out := make([]toolID, 0, len(toolDefs))
	for _, d := range toolDefs {
		out = append(out, d.id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
