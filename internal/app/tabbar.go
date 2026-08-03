// =============================================================================
// File: internal/app/tabbar.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// tabbar.go owns the strip of open files across the top of the editor:
// its geometry, its painting, its click handling, and — new here — its
// scrolling and the keyboard paths to it.
//
// What this replaced: layoutTabs walked the tabs left to right without
// bound and drawTabBar clipped whatever ran past the band edge. Open a
// dozen files and the tabs simply ran off the screen — including,
// routinely, the ACTIVE one, which then had no affordance at all. There
// was also no key binding to switch tabs, of any kind, which in an editor
// whose own notes say macOS Terminal can swallow Button3 meant the tab
// strip could become entirely unreachable.
//
// The model now:
//
//	 ≡ │ main.go × │ app.go × │ tab.go ×          … │ +4 │
//	   └── tabScroll ──┴── drawn while they fit ──┘  └ hidden count,
//	                                                   click to pick
//
//   - tabScroll is the index of the leftmost DRAWN tab. It is derived,
//     never a user setting: ensureActiveTabVisible pushes it just far
//     enough that the active tab fits, and pulls it back when closing
//     tabs leaves dead space on the right.
//   - The overflow button reports how many tabs aren't drawn (both sides
//     summed) and opens the switcher when clicked, which is what makes
//     the hidden ones reachable by mouse. Its geometry is one rect shared
//     by draw and hit-test, per the btnRect rule.
//   - Switching goes through switchToTab so the navigation history is
//     recorded in exactly one place — the click path used to do that
//     inline, and every new surface would have had to remember to.
package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/icons"
)

// tabOverflowGap is the blank column kept between the last drawn tab and
// the overflow button, so a full strip doesn't read as one run-on word.
const tabOverflowGap = 1

// tabStripRect returns where tabs may be drawn: the tab bar minus the ≡
// button on the left, and minus the overflow button on the right when one
// is showing. Everything else in this file measures against it.
func (a *App) tabStripRect() (x, y, w int) {
	tx, ty, tw, _ := a.tabBarRect()
	x = tx + menuButtonWidth
	w = tw - menuButtonWidth - a.tabOverflowReserve()
	return x, ty, max(w, 0)
}

// tabOverflowReserve is how many columns the overflow button takes, or 0
// when every tab fits. Reserved against the WIDEST label the button could
// end up showing (every tab hidden) rather than the count it will
// actually show, because the count depends on the scroll and the scroll
// depends on the space left over — one cell of slack beats a fixed point.
func (a *App) tabOverflowReserve() int {
	if len(a.tabs) == 0 {
		return 0
	}
	tx, _, tw, _ := a.tabBarRect()
	_ = tx
	total := 0
	for i := range a.tabs {
		total += a.tabWidth(i)
	}
	if total <= tw-menuButtonWidth {
		return 0
	}
	return tabOverflowGap + len(fmt.Sprintf(" +%d ", len(a.tabs)))
}

// tabWidth is the rendered width of one tab:
//
//	" <dirty><icon? ><name> × " — a single space pad, two-cell dirty slot
//	(dot+space, or two spaces), an optional Nerd Font glyph + 1-space
//	separator (only when icons are enabled), the file name, a separator
//	space, the close ×, and a trailing space.
func (a *App) tabWidth(i int) int {
	if i < 0 || i >= len(a.tabs) {
		return 0
	}
	iconW := 0
	if a.iconsOn() {
		iconW = 2 // glyph + space
	}
	nameLen := len([]rune(a.tabs[i].DisplayName()))
	return 1 + 2 + iconW + nameLen + 1 + 1 + 1
}

// ensureActiveTabVisible adjusts tabScroll so the active tab is drawn in
// full. Two directions, and both matter: scroll forward until the active
// tab's right edge fits, then back off while the tabs from one earlier
// would still all fit — otherwise closing tabs would leave the strip
// scrolled with empty space trailing it.
func (a *App) ensureActiveTabVisible() {
	if len(a.tabs) == 0 {
		a.tabScroll = 0
		return
	}
	a.tabScroll = clampInt(a.tabScroll, 0, len(a.tabs)-1)
	_, _, avail := a.tabStripRect()

	if a.activeTab < a.tabScroll {
		a.tabScroll = max(a.activeTab, 0)
	}
	for a.tabScroll < a.activeTab && a.widthOfTabs(a.tabScroll, a.activeTab) > avail {
		a.tabScroll++
	}
	for a.tabScroll > 0 && a.widthOfTabs(a.tabScroll-1, len(a.tabs)-1) <= avail {
		a.tabScroll--
	}
}

// widthOfTabs sums the rendered widths of tabs [first, last].
func (a *App) widthOfTabs(first, last int) int {
	total := 0
	for i := max(first, 0); i <= last && i < len(a.tabs); i++ {
		total += a.tabWidth(i)
	}
	return total
}

// layoutTabs computes the tabRect geometry for the tabs that fit, starting
// at tabScroll. A tab that would overrun the strip is simply not laid out
// — which is what makes hit-testing honest, since lastTabRects then holds
// exactly what the user can see and click.
func (a *App) layoutTabs() []tabRect {
	a.ensureActiveTabVisible()
	sx, _, sw := a.tabStripRect()
	out := make([]tabRect, 0, len(a.tabs))
	iconW := 0
	if a.iconsOn() {
		iconW = 2
	}
	cursor := sx
	for i := a.tabScroll; i < len(a.tabs); i++ {
		w := a.tabWidth(i)
		if cursor+w > sx+sw {
			// The active tab must always make it in: on a strip too
			// narrow for even one tab, draw it clipped rather than
			// leaving the bar blank.
			if i != a.activeTab || cursor > sx {
				break
			}
		}
		nameLen := len([]rune(a.tabs[i].DisplayName()))
		out = append(out, tabRect{
			Index:  i,
			X:      cursor,
			Width:  w,
			CloseX: cursor + 1 + 2 + iconW + nameLen + 1,
		})
		cursor += w
	}
	return out
}

// tabOverflowRect returns the overflow button's geometry and the number
// of tabs it stands for, or ok=false when everything is on screen. The
// single source draw and hit-testing share, per the btnRect rule.
func (a *App) tabOverflowRect() (x, y, w, hidden int, ok bool) {
	reserve := a.tabOverflowReserve()
	if reserve == 0 {
		return 0, 0, 0, 0, false
	}
	drawn := len(a.lastTabRects)
	hidden = len(a.tabs) - drawn
	if hidden <= 0 {
		return 0, 0, 0, 0, false
	}
	label := a.tabOverflowLabel(hidden)
	tx, ty, tw, _ := a.tabBarRect()
	w = len([]rune(label))
	return tx + tw - w, ty, w, hidden, true
}

// tabOverflowLabel is the button's text — the count of tabs not currently
// drawn, on either side of the visible run.
func (a *App) tabOverflowLabel(hidden int) string {
	return fmt.Sprintf(" +%d ", hidden)
}

// drawTabBar paints the tab bar across the top of the editor area: the
// menu button (≡), the tabs that fit, and the overflow button.
func (a *App) drawTabBar() {
	tx, ty, tw, _ := a.tabBarRect()
	barStyle := tcell.StyleDefault.Background(a.theme.SidebarBG).Foreground(a.theme.Muted)
	for cx := tx; cx < tx+tw; cx++ {
		a.screen.SetContent(cx, ty, ' ', nil, barStyle)
	}

	a.drawMenuButton()

	rects := a.layoutTabs()
	a.lastTabRects = rects
	for _, r := range rects {
		active := r.Index == a.activeTab
		bg := a.theme.SidebarBG
		fg := a.theme.Muted
		if active {
			bg = a.theme.BG
			fg = a.theme.Text
		}
		st := tcell.StyleDefault.Background(bg).Foreground(fg)
		if active {
			st = st.Bold(true)
		}
		// Background.
		for cx := r.X; cx < r.X+r.Width; cx++ {
			if cx >= tx+tw {
				break
			}
			a.screen.SetContent(cx, ty, ' ', nil, st)
		}
		tab := a.tabs[r.Index]
		col := r.X + 1
		if tab.Dirty {
			a.screen.SetContent(col, ty, '●', nil, st.Foreground(a.theme.Modified))
		}
		col += 2 // skip dirty slot.
		// Per-language Nerd Font glyph between the dirty dot and the
		// filename — only when icons are enabled. Coloured the same
		// way the file tree glyphs are (icons.ColorFor) so the eye
		// connects "this tab" to "that row in the tree" instantly.
		if a.iconsOn() {
			name := tab.DisplayName()
			glyph := icons.For(name, false, false)
			gfg := icons.ColorFor(name, false, fg)
			gst := tcell.StyleDefault.Background(bg).Foreground(gfg)
			if active {
				gst = gst.Bold(true)
			}
			for _, gr := range glyph {
				if col >= tx+tw {
					break
				}
				a.screen.SetContent(col, ty, gr, nil, gst)
				col++
			}
			col++ // separator space after glyph
		}
		for _, ru := range tab.DisplayName() {
			if col >= tx+tw {
				break
			}
			a.screen.SetContent(col, ty, ru, nil, st)
			col++
		}
		col++ // separator space before ×
		if col < tx+tw {
			closeStyle := st.Foreground(a.theme.Muted)
			if active {
				closeStyle = st.Foreground(a.theme.Subtle)
			}
			a.screen.SetContent(col, ty, '×', nil, closeStyle)
		}
	}

	a.drawTabOverflow()
}

// drawTabOverflow paints the "+N" button at the right edge of the tab bar
// when some tabs aren't drawn. Accent-colored: it is the only thing on
// screen saying files are open that you can't see, and it must not read
// as chrome.
func (a *App) drawTabOverflow() {
	x, y, _, hidden, ok := a.tabOverflowRect()
	if !ok {
		return
	}
	st := tcell.StyleDefault.Background(a.theme.SidebarBG).
		Foreground(a.theme.Accent).Bold(true)
	drawAt(a.screen, x, y, a.tabOverflowLabel(hidden), st)
}

// tabBarClick dispatches clicks in the tab bar: the leftmost menuButtonWidth
// cells open the action menu, the overflow button opens the switcher, and
// remaining cells switch or close tabs based on where the click landed
// within their rendered geometry.
func (a *App) tabBarClick(x, _ int) {
	mx, _, mw, _ := a.menuButtonRect()
	if x >= mx && x < mx+mw {
		a.openMenu()
		return
	}
	// Tested before the tabs: the button sits past the last drawn tab, so
	// the two can't overlap, but checking it first keeps that independent
	// of how the strip happened to be laid out.
	if ox, _, ow, _, ok := a.tabOverflowRect(); ok && x >= ox && x < ox+ow {
		a.menuSwitchTab()
		return
	}
	for _, r := range a.lastTabRects {
		if x >= r.X && x < r.X+r.Width {
			if x == r.CloseX {
				a.requestCloseTab(r.Index)
				return
			}
			a.switchToTab(r.Index)
			return
		}
	}
}

// switchToTab makes idx active and records the departure into the
// navigation history. Every tab-switching surface goes through here: the
// click path used to record inline, which meant each new surface had to
// remember to, and the keyboard ones would have quietly not.
//
// A switch to the tab already showing is not navigation and records
// nothing — otherwise Go back would spend its first press on a no-op.
func (a *App) switchToTab(idx int) {
	if idx < 0 || idx >= len(a.tabs) || idx == a.activeTab {
		return
	}
	if from, ok := a.currentNavLoc(); ok && from.path != a.tabs[idx].Path {
		a.recordNav(from)
	}
	a.activeTab = idx
}

// menuNextTab / menuPrevTab step through the open files, wrapping at the
// ends. Wrapping is deliberate: with the strip scrolled, "next" is a
// gesture you repeat without looking, and stopping dead at the last tab
// reads as a stuck key.
func (a *App) menuNextTab() {
	a.closeMenu()
	if len(a.tabs) < 2 {
		return
	}
	a.switchToTab((a.activeTab + 1) % len(a.tabs))
}

// menuPrevTab is the backwards twin of menuNextTab.
func (a *App) menuPrevTab() {
	a.closeMenu()
	if len(a.tabs) < 2 {
		return
	}
	a.switchToTab((a.activeTab - 1 + len(a.tabs)) % len(a.tabs))
}

// menuSwitchTab opens the open-file list as a fuzzy picker — the house
// rule that every choose-one-from-a-list UI reuses the palette, and the
// surface that scales when the strip can no longer show everything.
//
// Unlike the theme picker, the current tab is left OUT: picking it is the
// one choice guaranteed to do nothing, and the title already names it.
func (a *App) menuSwitchTab() {
	a.closeMenu()
	if len(a.tabs) < 2 {
		a.flash("No other tabs")
		return
	}
	items := make([]paletteItem, 0, len(a.tabs))
	for i, t := range a.tabs {
		if i == a.activeTab {
			continue
		}
		idx := i
		items = append(items, paletteItem{
			label: a.tabPickerLabel(t.DisplayName(), t.Path, t.Dirty),
			run:   func(app *App) { app.switchToTab(idx) },
		})
	}
	title := "Switch tab"
	if cur := a.activeTabPtr(); cur != nil {
		title += " (current: " + cur.DisplayName() + ")"
	}
	a.openPicker(title, items)
}

// tabPickerLabel renders one row of the switcher: the dirty marker, the
// file name, and its directory relative to the project root. The
// directory is what disambiguates the four main.go files a Go project
// tends to have open at once, so it is not decoration.
func (a *App) tabPickerLabel(name, path string, dirty bool) string {
	var b strings.Builder
	if dirty {
		b.WriteString("● ")
	}
	b.WriteString(name)
	if dir := a.tabPickerDir(path); dir != "" {
		b.WriteString("  ")
		b.WriteString(dir)
	}
	return b.String()
}

// tabPickerDir is the tab's directory relative to the project root, or ""
// for an untitled tab, a file at the root, or anything outside it (where
// a "../../.." chain would be noise rather than context).
func (a *App) tabPickerDir(path string) string {
	if path == "" {
		return ""
	}
	rel, err := filepath.Rel(a.rootDir, filepath.Dir(path))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return rel
}

// hasMultipleTabs gates the switching menu rows — every one of them is a
// no-op with fewer than two files open.
func (a *App) hasMultipleTabs() bool { return len(a.tabs) > 1 }

// clampInt confines v to [lo, hi].
func clampInt(v, lo, hi int) int {
	return min(max(v, lo), hi)
}
