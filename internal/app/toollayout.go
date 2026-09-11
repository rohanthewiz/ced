// =============================================================================
// File: internal/app/toollayout.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// toollayout.go remembers where you put your tool windows, PER PROJECT.
//
// WHY PER PROJECT AND NOT PER USER. A layout is an answer to "what am I
// doing in this repository". A Go service wants the terminal at the
// bottom and the problems list one click away; a notes repo wants
// neither and a wide editor; a repo you only ever read wants the git log
// on the right. A single global arrangement would be wrong for most of
// them most of the time, and the editor already knows how to store
// per-project state — the tab list and cursor positions have lived in
// state.json since folder switching landed, and they are exactly the
// same KIND of thing: what the editor did here, not what the user
// prefers everywhere.
//
// WHERE IT LIVES. session.Entry.Layout, beside that folder's tabs. It is
// deliberately NOT in config.json: that file is hand-edited preferences
// and this is machine churn rewritten on every folder switch and every
// exit — the split state.json was created to make. There is no global
// half at all: everything about a tool window is a statement about THIS
// project, and the ≡ menu is how you change it.
//
// HOUSE RULES:
//
//   - A PROJECT WITH NO RECORD GETS THE DEFAULT, which is the file tree
//     on the left and the editor taking the rest. That is the literal
//     ask, and it is also what newToolLayout returns, so there is one
//     definition of "default" rather than a restore path with opinions
//     of its own.
//
//   - RESTORE IS BEST-EFFORT AND SILENT. An unknown tool id, an edge
//     ced no longer has, a width from a much wider monitor — each costs
//     that one entry and nothing else, the theme registry's per-item
//     degradation rule. The user asked to open a FOLDER; a wall of
//     messages about panels they do not remember arranging is noise.
//
//   - RESTORING AN OPEN TOOL GOES THROUGH showTool, not through the
//     panel's flag. A tool can REFUSE to come up (a chat agent with no
//     binary on this machine, a git panel outside a repository), and a
//     restore that set the flag anyway would draw an empty box the user
//     has to work out how to close. Refusing quietly leaves the stripe
//     button, which is exactly where the tool went.
//
//   - THE SAVE IS EXPLICIT, NOT A TICK. Layout changes are deliberate
//     gestures — a move, a resize, a show, a hide — so each one saves,
//     and Close saves whatever the session ended holding. There is no
//     debounce because there is nothing to debounce: nobody drags a
//     splitter a hundred times a second, and a drag writes on release
//     rather than per pixel.

package app

import "github.com/rohanthewiz/ced/internal/session"

// encodeToolLayout flattens the live layout — plus which tools are on
// screen and the file tree's width, which lives on App — into the
// session's on-disk shape.
//
// The tree's width is the one special case in this whole feature and it
// is deliberate: auto-fit (treeautofit.go) re-derives App.sidebarWidth on
// every frame, so THAT field is the live number and the layout's copy
// would be a second one that could disagree. Reading it here at save
// time means the number that goes to disk is the one that was on screen.
func (a *App) encodeToolLayout() *session.Layout {
	out := &session.Layout{
		Docks: map[string]string{},
		Sizes: map[string]session.ToolSize{},
	}
	t := a.tools()
	for _, d := range toolDefs {
		if side, ok := t.dock[d.id]; ok && validDock(side) {
			out.Docks[string(d.id)] = string(side)
		}
		sz := t.size[d.id]
		// The tree's width is read through storedToolWidth, which knows
		// it lives on App.sidebarWidth — see the doc comment above.
		sz.W = a.storedToolWidth(d.id)
		if sz.W != 0 || sz.H != 0 {
			out.Sizes[string(d.id)] = session.ToolSize{W: sz.W, H: sz.H}
		}
		if d.isOpen(a) {
			out.Open = append(out.Open, string(d.id))
		}
	}
	if len(out.Docks) == 0 && len(out.Sizes) == 0 && len(out.Open) == 0 {
		// Nothing to say. A nil layout is how "this project has never
		// been arranged" is spelled, and it keeps state.json readable.
		return nil
	}
	return out
}

// applyToolLayout installs a restored layout. It runs BEFORE any panel
// is on screen (during New, right after the tabs are restored), so
// nothing here has to evict anything: the docks are assigned first and
// only then are the remembered tools shown, one per edge, through the
// ordinary showTool path.
//
// A nil layout is the default and is not an error — it is what every new
// project passes.
func (a *App) applyToolLayout(l *session.Layout) {
	t := newToolLayout()
	a.toolLayoutState = t
	if l == nil {
		return
	}

	for id, side := range l.Docks {
		if _, ok := toolDefFor(toolID(id)); !ok {
			continue // A tool this version has never heard of.
		}
		if !validDock(dockSide(side)) {
			continue // An edge this version has never heard of.
		}
		a.setToolDock(toolID(id), dockSide(side))
	}

	for id, sz := range l.Sizes {
		if _, ok := toolDefFor(toolID(id)); !ok {
			continue
		}
		t.size[toolID(id)] = toolSize{W: sz.W, H: sz.H}
		if toolID(id) == toolProject && sz.W != 0 {
			// See storedToolWidth: the tree's width is a field on App,
			// so restoring it means writing that field.
			a.sidebarWidth = sz.W
		}
	}
	// Every remembered number is re-clamped against THIS window before
	// anything reads it: a width saved on a 240-column monitor would
	// otherwise leave no editor at all on a laptop, and a stored number
	// merely read through the clamp would spring back the moment the
	// window grew — which reads as the panel resisting the drag.
	a.clampToolSizes()

	// The tree is open by default, so a remembered layout that does not
	// list it means it was deliberately hidden. Everything else starts
	// closed and is opened below.
	a.sidebarShown = false
	a.toolLayoutLoading = true
	defer func() { a.toolLayoutLoading = false }()
	for _, id := range l.Open {
		// showTool, not the flag: a tool may refuse to come up on this
		// machine, and a restore that insisted would draw an empty box.
		a.showTool(toolID(id))
	}
}

// saveToolLayout writes the current arrangement into this project's
// session entry. Every layout gesture calls it — moving a tool, showing
// or hiding one, finishing a resize — because each of those is the user
// stating something they will expect to find again, and because a run
// that dies must cost as little as possible of what actually happened
// (session.go's own record-the-visit-at-startup argument).
//
// Silent on failure, and gated on the session preference: this is
// bookkeeping nobody asked for, and the failure surfaces where it means
// something — a project that opens with the default layout.
func (a *App) saveToolLayout() {
	// A restore SHOWS every remembered tool through the ordinary verb,
	// so without this guard reading the layout would write it back one
	// tool at a time — several file rewrites during startup, each one
	// recording a half-restored arrangement.
	if a.toolLayoutLoading || !a.sessionEnabled || a.rootDir == "" {
		return
	}
	path := sessionStatePathFn()
	if path == "" {
		return
	}
	store, err := session.Load(path)
	if err != nil {
		return
	}
	root := session.Normalize(a.rootDir)
	entry, _ := store.Find(root)
	entry.Root = root
	entry.Layout = a.encodeToolLayout()
	store.Record(entry)
	_ = store.Save(path)
}
