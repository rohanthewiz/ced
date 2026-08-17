// =============================================================================
// File: internal/app/treeautofit.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// File-tree auto-fit: the sidebar re-derives its own width from the
// tree's longest row, so expanding internal/app/ stops truncating the
// names inside it — but only while the editor can spare the columns.
// The measurement lives in internal/filetree (Tree.ContentWidth, which
// shares the row-text construction with the renderer); everything here
// is policy: how much room the editor keeps, when the panel is allowed
// to move, and how a manual splitter drag takes the width back.
package app

import "github.com/rohanthewiz/ced/internal/userconfig"

const (
	// autoFitMinEditor is the editor width auto-fit refuses to go below.
	// Deliberately far above minEditorAfterDrag (40, the floor a DRAG may
	// squeeze the editor to): a drag is the user asking for a narrow
	// editor, while auto-fit happens on its own — so it may only spend
	// columns the editor was never going to miss. 80 is the column count
	// code is written to.
	autoFitMinEditor = 80

	// autoFitMaxShareDen caps the sidebar at 1/autoFitMaxShareDen of the
	// columns it shares with the editor. Without it a wide terminal would
	// let one deeply nested path hand the tree half the window — the
	// editor's floor alone is not a proportion, and on a 240-column
	// screen 80 columns of code is not "reasonable room" in any sense the
	// user means.
	autoFitMaxShareDen = 3
)

// autoFitSidebar re-derives the sidebar's width from the tree's content,
// once per frame, while the auto-fit preference is on. Called at the top
// of draw() — before any rect helper is read — so the whole frame lays
// out against the final number. Deriving it a frame LATE would paint the
// truncated row the user just expanded and then leave it there until the
// next event happened to arrive.
//
// The width only ever lands inside [defaultSidebarWidth, cap]:
//
//   - The floor is the default width, not the tree's own measurement. A
//     panel that also SHRANK to hug a shallow tree would jump twice on
//     every expand/collapse, and 30 columns is already the width the
//     editor ships with — nobody is asking for it back.
//   - The cap is the editor's comfortable floor, further capped to a
//     share of the band. If the window is too small to give the editor
//     that floor at the DEFAULT sidebar width, auto-fit does nothing at
//     all rather than fighting for columns that aren't there — the "if
//     there is reasonable room" half of the feature.
func (a *App) autoFitSidebar() {
	if !a.treeAutoFit || !a.sidebarShown || a.tree == nil {
		return
	}
	// Belt and braces: a drag turns the preference off on its first
	// motion, so this only covers the press-and-hold before that.
	if a.dragMode == "sidebar" {
		return
	}

	// Columns the OTHER docked strip has first claim on. In the classic
	// layout that's nothing — the sidebar IS the left block — while in the
	// flipped one (chat or a left-docked terminal) the strip owns the left
	// edge and the tree shares what's left with the editor. Same
	// bookkeeping resizeSidebar's clamp does.
	room := a.width
	if a.treeOnRight() {
		room -= a.leftBlockW()
	}

	max := room - autoFitMinEditor
	if share := room / autoFitMaxShareDen; max > share {
		max = share
	}
	if max < defaultSidebarWidth {
		return
	}

	// +1 for the splitter: sidebarWidth covers the block, and sidebarRect
	// hands the tree one column less than that.
	want := a.tree.ContentWidth() + 1
	if want < defaultSidebarWidth {
		want = defaultSidebarWidth
	}
	if want > max {
		want = max
	}
	if want != a.sidebarWidth {
		// Through resizeSidebar rather than assigning: it is the single
		// clamp every width change goes through, and want is already
		// inside its stricter range, so this is a pass-through that keeps
		// one write path.
		a.resizeSidebar(want)
	}
}

// lockTreeAutoFit turns auto-fit off because the user just dragged the
// splitter, and persists the choice.
//
// The two cannot share the number: whatever width a drag lands on would
// be overwritten by the next folder the user expanded, which reads as
// the splitter being broken rather than as a feature. So the gesture
// itself is the opt-out — no dialog, and the flash names the ≡ row that
// undoes it, because the preference outlives the session. A no-op once
// auto-fit is already off, so the drag handler can call it per motion.
func (a *App) lockTreeAutoFit() {
	if !a.treeAutoFit {
		return
	}
	a.treeAutoFit = false
	a.flash("File tree width locked — auto-fit off (≡ View to re-enable)")
	if err := userconfig.SaveTreeAutoFit(userconfig.DefaultPath(), false); err != nil {
		a.flash("config: " + err.Error())
	}
}

// menuToggleTreeAutoFit flips auto-fit from the ≡ View group and persists
// the choice. Turning it ON takes effect on the very next draw (the
// sidebar snaps to the tree); turning it off simply leaves the current
// width in place, which is what makes the splitter usable again.
func (a *App) menuToggleTreeAutoFit() {
	a.closeMenu()
	a.treeAutoFit = !a.treeAutoFit
	if a.treeAutoFit {
		a.flash("File tree auto-fit on")
	} else {
		a.flash("File tree auto-fit off — width locked")
	}
	if err := userconfig.SaveTreeAutoFit(userconfig.DefaultPath(), a.treeAutoFit); err != nil {
		a.flash("config: " + err.Error())
	}
}

// treeAutoFitToggleLabel names the action the row will perform, not the
// state it's in — the same toggle-in-place rule the sidebar and
// exec-marks rows follow.
func (a *App) treeAutoFitToggleLabel() string {
	if a.treeAutoFit {
		return "Lock file tree width"
	}
	return "Auto-fit file tree width"
}
