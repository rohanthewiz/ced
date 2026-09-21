// =============================================================================
// File: internal/app/toolmenu.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// toolmenu.go is the ≡ menu's Tool windows group and the pickers behind
// it: show or hide any tool, move one to another edge, and reset the
// whole arrangement.
//
// WHY IT EXISTS BESIDE THE STRIPE. The stripe is the primary surface and
// it is a mouse one, which in this editor is never allowed to be the
// only one: macOS Terminal swallows clicks, a modal owns the keyboard
// while it is up, and the stripes can be switched off entirely. So every
// gesture the rail offers has a row here — the same redundancy rule that
// puts every file action in the ≡ menu even though the tree's right-click
// already has it.
//
// WHY IT IS A GROUP AND NOT A DOZEN ROWS. Seven tools times three edges
// is twenty-one moves, and the ≡ menu already scrolls on a short window
// where every row above the fold is contested. So the group carries THREE
// rows and the branching lives in pickers (the house rule that every
// choose-one-from-a-list UI reuses the palette): pick a tool, then pick
// an edge. That also means a tool added to the registry appears here
// with no menu change at all.
//
// The individual Show/Hide rows each panel already had (Show terminal,
// Show git panel, …) are untouched and stay where they are. They are the
// rows people reach for daily and they name their tool directly; this
// group is for the arranging, which is a rarer and more deliberate act.

package app

// menuToolWindows opens the tool picker: every registered tool, showing
// where it lives and whether it is up, and picking one TOGGLES it.
//
// Toggle rather than "show", because that is what the row's twin on the
// stripe does and because a picker whose rows only ever opened things
// would need a second picker to close them. The label carries the state
// so the row says what it will do before it is picked.
func (a *App) menuToolWindows() {
	a.closeMenu()
	items := make([]paletteItem, 0, len(toolDefs))
	for _, d := range toolDefs {
		id := d.id
		items = append(items, paletteItem{
			label: a.toolPickerLabel(id),
			run: func(app *App) {
				app.toggleTool(id)
				app.saveToolLayout()
			},
		})
	}
	a.openPicker("Tool windows", items)
}

// toolPickerLabel describes one tool in a picker row: its name, whether
// it is showing, and which edge it lives on.
//
// The name comes FIRST and the annotations trail, for the go-to-symbol
// rule: the fuzzy scorer rewards early matches, so a leading "shown" or
// "left" would make every row score alike on the first letters typed.
// Trailing, they still narrow by state or edge while the name keeps the
// position that ranks.
func (a *App) toolPickerLabel(id toolID) string {
	label := toolTitle(id)
	if a.toolOpen(id) {
		label += " · shown"
	} else {
		label += " · hidden"
	}
	return label + " · " + dockLabel(a.toolDock(id))
}

// menuMoveToolWindow opens the same tool list, and picking one asks for
// an edge. Two steps rather than twenty-one rows — see the file header.
func (a *App) menuMoveToolWindow() {
	a.closeMenu()
	items := make([]paletteItem, 0, len(toolDefs))
	for _, d := range toolDefs {
		id := d.id
		items = append(items, paletteItem{
			label: a.toolPickerLabel(id),
			run:   func(app *App) { app.openToolDockPicker(id) },
		})
	}
	a.openPicker("Move which tool window", items)
}

// openToolDockPicker asks which edge a tool should move to. The edge it
// is ALREADY on is omitted rather than dimmed — the palette has no
// disabled state to borrow, and a row answering Enter with "it is
// already there" is worse than one never offered (the code-actions
// rule).
//
// The move is followed by a show, deliberately: picking an edge for a
// tool is a "put it THERE" gesture, and moving something invisible would
// read as the row doing nothing. That is the same call menuToggleTermDock
// makes, which is now just this verb with the edge pre-chosen.
func (a *App) openToolDockPicker(id toolID) {
	cur := a.toolDock(id)
	items := make([]paletteItem, 0, len(dockSides))
	for _, side := range dockSides {
		if side == cur {
			continue
		}
		s := side
		items = append(items, paletteItem{
			label: "Move " + toolTitle(id) + " to the " + dockLabel(s),
			run: func(app *App) {
				app.moveTool(id, s)
				app.showTool(id)
				app.flash(toolTitle(id) + " · " + dockLabel(s))
				app.saveToolLayout()
			},
		})
	}
	a.openPicker(toolTitle(id)+" — currently "+dockLabel(cur), items)
}

// menuResetToolLayout puts every tool back where it started: the file
// tree on the left, the editor taking the rest, everything else closed
// at its default size.
//
// It does NOT confirm. Nothing is destroyed — the tabs, the terminal
// session, the chat transcript and the git panels' state all survive a
// re-arrangement, since a layout is where things are drawn and not what
// they hold — and a dialog in front of a reversible action trains people
// to dismiss dialogs (the favorites rule).
func (a *App) menuResetToolLayout() {
	a.closeMenu()
	a.resetToolLayout()
	// The flash carries the arrangement the reset produced. Most of what
	// a reset changes is OFF screen — tools that were moved and are now
	// closed again — so "reset" alone leaves the user to open each one
	// to learn where it went. A "*" marks what is showing.
	a.flash("Tool window layout reset — " + a.toolLayoutSummary())
	a.saveToolLayout()
}

// toolWindowsLabel names the Tool windows row with a short census of the
// arrangement, so the row answers "where is everything?" without being
// clicked — the theme row's trick, and the reason that row is worth its
// line. It counts what is SHOWING, because that is the part of the
// layout the user can already see and is checking their memory against.
func (a *App) toolWindowsLabel() string {
	shown := 0
	for _, d := range toolDefs {
		if d.isOpen(a) {
			shown++
		}
	}
	switch shown {
	case 0:
		return "Tool windows… (none shown)"
	case 1:
		return "Tool windows… (1 shown)"
	}
	// itoa is the package's own small integer formatter (finder.go).
	return "Tool windows… (" + itoa(shown) + " shown)"
}
