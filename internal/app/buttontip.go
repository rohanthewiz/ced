// =============================================================================
// File: internal/app/buttontip.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Hover labels for the editor's chrome: rest the pointer on a button —
// the red ✕, the ≡, a tab's ×, ⧉ on the status bar, a panel's ⟳ — and a
// small box says what a click will do, plus the keyboard twin when there
// is one.
//
//	│ ✕  ≡ │ main.go × │ app.go × │
//	┌──────────────────────────────┐
//	│ Quit ced — asks first        │  ← what the click does
//	│ Esc q                        │  ← the keyboard door, in Accent
//	└──────────────────────────────┘
//
// WHY. ced's chrome is single-width glyphs by house rule (◇ ◨ ⟳ ⧉ ⊘ —
// the runeLen rule rules out words and emoji alike), and a glyph is only
// self-explanatory to the person who chose it. The ✕ in particular sits
// in a corner cell beside the ≡ and closes the whole EDITOR, while every
// other ✕ on screen closes a panel; the colour says "different", the
// label says what.
//
// Four rules, borrowed from the overflow popup (overflow.go), which was
// the first passive popup that described chrome rather than code:
//
//   - ONE ENUMERATOR. buttonTips() lists every labelled button with the
//     rect its click router hit-tests. Arming, the tick and the draw all
//     ask it, so a label can only ever appear over a button that is
//     really there — and re-resolving at draw time means a label whose
//     button vanished (panel closed by a key, tab closed) simply stops
//     being drawn.
//   - PASSIVE. Never the modal slot, never a key, never a press. It
//     dismisses on any key, button, resize or opened modal, and every
//     one of those events then does what it always did.
//   - ANCHORED TO THE BUTTON, NOT THE CELL. Motion inside the same
//     button keeps an open label open and a pending clock running; a
//     multi-cell button like " Actions ▾ " would otherwise flicker as
//     the pointer crossed it.
//   - EVERYWHERE. Unlike hover-on-dwell (Tier 1 only: its answer costs a
//     server round trip), the answer here is a constant string, so it
//     runs in every terminal that reports motion. A terminal that does
//     not simply never shows one — every button still works and every
//     verb still has its ≡ row, so nothing depends on the label.
//
// It speaks over two modals. The unpinned Find-all list: its title
// buttons (⟳ ◇ ◨) are the most cryptic glyphs in the editor, and a
// label is the cheapest way to learn them. And a picker's section
// headings (Recent folders' "MRU 5" / "MFU 10" and their ⧉ copy): the
// initialisms say nothing to someone who hasn't met them, and the
// heading is a divider row with no room to spell them out. That is why
// the pointer hook runs BEFORE handleMouse's modal dispatch and the box
// is drawn after the overlay layer — for any other modal, or with the ≡
// menu up, the enumerator returns nothing and the layer is inert.

package app

import (
	"reflect"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// buttonTipDelay is how long the pointer must rest on a button before
// its label appears. Longer than the overflow popup's 250ms: that popup
// answers a question the user is visibly asking (they are pointing at
// a "there is more" marker), while a button is usually just on the way
// to being clicked, and a label that flashed up on every click would be
// noise. Half a second is the conventional desktop tooltip delay.
const buttonTipDelay = 500 * time.Millisecond

// buttonTip is one labelled button: where it is, what to say, and the
// keyboard door to the same verb.
//
// The door is held as the VERB (keyFn) and resolved against the leader
// table only when the label actually opens (leaderKeysFor), rather than
// written as a literal "Esc q": the enumerator runs on every pointer
// motion, and a literal would drift the day a leader is rebound. keys
// is for doors that are not leaders at all (Alt chords inside the find
// bar, double-Esc for the menu).
type buttonTip struct {
	rect  btnRect
	text  []string
	keyFn func(*App)
	keys  string
}

// tipLines renders the label's rows and the emphasis run for its key
// line (the last row, painted in Accent so the door stands apart from
// the description).
func (t buttonTip) tipLines() ([]string, []hoverEmph) {
	lines := append([]string(nil), t.text...)
	keys := t.keys
	if keys == "" {
		keys = leaderKeysFor(t.keyFn)
	}
	if keys == "" {
		return lines, nil
	}
	lines = append(lines, keys)
	return lines, []hoverEmph{{line: len(lines) - 1, start: 0, end: runeLen(keys)}}
}

// leaderKeysFor names the Esc-leader chord bound to a verb — "Esc q",
// "Esc a c" for a namespaced one — or "" when the verb has none.
//
// Functions are not comparable in Go, so the match is on the code
// pointer. That is exact for the method expressions the leader table
// uses ((*App).menuQuit and friends) and simply never matches a closure,
// which is the honest answer: a closure is not the bound verb.
func leaderKeysFor(fn func(*App)) string {
	if fn == nil {
		return ""
	}
	want := reflect.ValueOf(fn).Pointer()
	same := func(f func(*App)) bool {
		return f != nil && reflect.ValueOf(f).Pointer() == want
	}
	for _, b := range leaderBindings() {
		if same(b.action) {
			return "Esc " + b.displayKey()
		}
		// One level of namespace, the depth the table allows. Dynamic
		// namespaces (subFor) are skipped: plugins and cats verbs are
		// never what a chrome button runs.
		for _, s := range b.sub {
			if same(s.action) {
				return "Esc " + b.displayKey() + " " + s.displayKey()
			}
		}
	}
	return ""
}

// buttonTipEvent is the dwell timer's tick, pinned by seq to the
// pointer rest that scheduled it — a pointer that has moved to another
// button invalidates it without any timer being cancelled.
type buttonTipEvent struct {
	when time.Time
	seq  int
}

// When satisfies the tcell.Event interface.
func (e *buttonTipEvent) When() time.Time { return e.when }

// Compile-time check that buttonTipEvent really is a tcell.Event.
var _ tcell.Event = (*buttonTipEvent)(nil)

// buttonTipState is the layer's whole state. armed is the button a
// pending tick is for; open/rect describe the label on screen. Both are
// rects rather than indexes into buttonTips(), because the enumeration
// is rebuilt on every ask and an index would name a different button
// the moment a panel opened.
type buttonTipState struct {
	seq   int
	x, y  int // the cell the pointer was last seen on
	armed btnRect
	open  bool
	rect  btnRect
	box   struct{ x, y, w, h int }
}

// noteButtonTipPointer is handleMouse's per-event hook. It never claims
// an event: the label sits beside its button, not over it, and leaving
// the button closes it — so a press can only land inside the box in a
// terminal that skipped the motion report, and there the press should
// still reach whatever the user aimed at.
func (a *App) noteButtonTipPointer(x, y int, btn tcell.ButtonMask) {
	if btn != tcell.ButtonNone {
		// A press is an action, not a rest. x/y are left alone so the
		// release (a same-cell report) doesn't re-arm the label over the
		// button that was just clicked.
		a.closeButtonTip()
		return
	}
	if x == a.buttonTip.x && y == a.buttonTip.y {
		return // same cell: some hosts repeat motion reports
	}
	a.buttonTip.x, a.buttonTip.y = x, y
	// Still on the button the label (or the pending clock) is about:
	// nothing to do. This is the anchored-to-the-button rule.
	if a.buttonTip.open && a.buttonTip.rect.contains(x, y) {
		return
	}
	if !a.buttonTip.open && a.buttonTip.armed.w > 0 && a.buttonTip.armed.contains(x, y) {
		return
	}
	a.closeButtonTip()
	a.armButtonTip()
}

// armButtonTip schedules a tick when the pointer's cell carries a
// labelled button. Crossing anything else schedules nothing at all.
func (a *App) armButtonTip() {
	if a.screen == nil {
		return
	}
	t, ok := a.buttonTipAt(a.buttonTip.x, a.buttonTip.y)
	if !ok {
		return
	}
	a.buttonTip.armed = t.rect
	a.buttonTip.seq++
	seq := a.buttonTip.seq
	scr := a.screen
	time.AfterFunc(buttonTipDelay, func() {
		// Goroutine territory: post, never mutate.
		_ = scr.PostEvent(&buttonTipEvent{when: time.Now(), seq: seq})
	})
}

// closeButtonTip hides the label and invalidates any tick in flight.
// Safe to call blind, which is how the key/resize/modal paths use it.
func (a *App) closeButtonTip() {
	a.buttonTip.open = false
	a.buttonTip.armed = btnRect{}
	a.buttonTip.rect = btnRect{}
	a.buttonTip.box = struct{ x, y, w, h int }{}
	a.buttonTip.seq++
}

// handleButtonTipTick opens the label if the tick is current and the
// pointer is still on the button it was armed for. The button is
// re-resolved rather than trusted: a panel may have opened or closed
// under a stationary pointer.
func (a *App) handleButtonTipTick(e *buttonTipEvent) {
	if e.seq != a.buttonTip.seq {
		return
	}
	t, ok := a.buttonTipAt(a.buttonTip.x, a.buttonTip.y)
	if !ok || t.rect != a.buttonTip.armed {
		return
	}
	a.buttonTip.open = true
	a.buttonTip.rect = t.rect
}

// buttonTipAt is the hit-test half of buttonTips: the first labelled
// button covering the cell. First wins, which is why the enumerator
// lists a tab's × and status marker before the tab body they sit in.
func (a *App) buttonTipAt(x, y int) (buttonTip, bool) {
	for _, t := range a.buttonTips() {
		if t.rect.w > 0 && t.rect.contains(x, y) {
			return t, true
		}
	}
	return buttonTip{}, false
}

// drawButtonTip paints the label. Called last in draw — after the
// overlay layer, for the Find-all modal's sake (see the file header) —
// and always called, because clearing the stamped box when nothing is
// shown is part of its job.
//
// The text is re-resolved here rather than captured at open, so a
// toggle's label ("Match case: off") tracks a state that changed under
// a resting pointer, and a button that is gone takes its label with it.
func (a *App) drawButtonTip() {
	a.buttonTip.box = struct{ x, y, w, h int }{}
	if !a.buttonTip.open {
		return
	}
	t, ok := a.buttonTipAt(a.buttonTip.x, a.buttonTip.y)
	if !ok || t.rect != a.buttonTip.rect {
		return
	}
	lines, emph := t.tipLines()
	w, h := tooltipSize(a, lines)
	mx, my, mw, mh := tooltipPlace(a, w, h, t.rect.x, t.rect.y)
	a.buttonTip.box = struct{ x, y, w, h int }{mx, my, mw, mh}
	drawTooltipBox(a, lines, emph, mx, my, mw, mh)
}

// buttonTipVisible reports whether a label is on screen (as of the last
// draw).
func (a *App) buttonTipVisible() bool {
	return a.buttonTip.box.w > 0
}

// -----------------------------------------------------------------------------
// The enumerator
// -----------------------------------------------------------------------------

// buttonTips lists every labelled button currently on screen, each with
// the rect its click handler hit-tests — the btnRect methods and the
// rects the last frame stamped (tab strip, status bar), never a second
// copy of the geometry.
//
// With the ≡ menu up nothing is labelled (it covers the chrome and has
// its own text); with a modal up only the unpinned Find-all list's
// buttons and a picker's section headings are, for the reasons in the
// file header.
func (a *App) buttonTips() []buttonTip {
	if a.menuOpen {
		return nil
	}
	if a.modal != nil {
		switch m := a.modal.(type) {
		case *findAllModal:
			return a.findAllButtonTips(m)
		case *paletteModal:
			return a.paletteHeadingButtonTips(m)
		}
		return nil
	}
	var tips []buttonTip
	if a.findAllPin != nil {
		// The pinned list floats over the editor band, so it is listed
		// before anything it could sit on top of.
		tips = append(tips, a.findAllButtonTips(a.findAllPin)...)
	}
	tips = append(tips, a.tabBarButtonTips()...)
	tips = append(tips, a.statusBarButtonTips()...)
	tips = append(tips, a.findBarButtonTips()...)
	tips = append(tips, a.toolButtonTips()...)
	return tips
}

// tabBarButtonTips labels the tab bar: ✕, ≡, the +N switcher, and per
// tab its group chip, × and status marker, then the tab itself (its
// project-relative path — the label strip shows only enough of a name
// to tell open tabs apart).
func (a *App) tabBarButtonTips() []buttonTip {
	var tips []buttonTip

	qx, qy, qw, _ := a.quitButtonRect()
	quit := "Quit ced — asks first"
	if n := a.dirtyTabCount(); n > 0 {
		quit = "Quit ced — " + plural(n, "unsaved tab", "unsaved tabs") + ", asks what to do"
	}
	tips = append(tips, buttonTip{rect: btnRect{x: qx, y: qy, w: qw},
		text: []string{quit}, keyFn: (*App).menuQuit})

	mx, my, mw, _ := a.menuButtonRect()
	tips = append(tips, buttonTip{rect: btnRect{x: mx, y: my, w: mw},
		text: []string{"Action menu"}, keys: "Esc Esc · right-click"})

	if ox, oy, ow, hidden, ok := a.tabOverflowRect(); ok {
		tips = append(tips, buttonTip{rect: btnRect{x: ox, y: oy, w: ow},
			text:  []string{plural(hidden, "more tab", "more tabs") + " — switch tab…"},
			keyFn: (*App).menuSwitchTab})
	}

	_, ty, _, _ := a.tabBarRect()
	for _, r := range a.lastTabRects {
		if r.Index < 0 || r.Index >= len(a.tabs) {
			continue
		}
		tab := a.tabs[r.Index]
		if r.ChipW > 0 && r.Group != nil {
			verb := "fold"
			if r.Group.collapsed {
				verb = "unfold"
			}
			tips = append(tips, buttonTip{rect: btnRect{x: r.ChipX, y: ty, w: r.ChipW},
				text: []string{"Tab group " + r.Group.name + " — click to " + verb,
					"right-click for the group menu"}})
		}
		if r.Width == 0 {
			continue // folded into a collapsed group: nothing drawn
		}
		closeText := "Close tab"
		if tab.Dirty {
			closeText = "Close tab — unsaved, asks first"
		}
		tips = append(tips, buttonTip{rect: btnRect{x: r.CloseX, y: ty, w: 1},
			text: []string{closeText}, keyFn: (*App).menuClose})
		if mark, _, ok := a.tabMarker(tab); ok {
			var mt buttonTip
			switch mark {
			case diskGoneMarker:
				mt = buttonTip{text: []string{"Deleted on disk — save to write it back"}, keyFn: (*App).menuSave}
			case conflictMarker:
				mt = buttonTip{text: []string{"Changed on disk — click to decide"}}
			default:
				mt = buttonTip{text: []string{"Unsaved changes"}, keyFn: (*App).menuSave}
			}
			mt.rect = btnRect{x: r.MarkerX, y: ty, w: 1}
			tips = append(tips, mt)
		}
		if tab.Path != "" {
			tips = append(tips, buttonTip{rect: btnRect{x: r.X, y: ty, w: r.Width},
				text: []string{a.relativePathFor(tab.Path), "right-click for the tab menu"}})
		}
	}
	return tips
}

// statusBarButtonTips labels the status bar's clickable segments from
// the rects the last drawStatusBar stamped. Each segment carries its own
// text (statusSegment.tip), because the segment list is the one place
// that knows what each span IS; the key door comes from its onClick.
func (a *App) statusBarButtonTips() []buttonTip {
	var tips []buttonTip
	for _, seg := range a.statusSegs {
		if seg.tip == "" || seg.rect.w <= 0 {
			continue
		}
		t := buttonTip{rect: seg.rect, text: []string{seg.tip}, keyFn: seg.onClick}
		if seg.text == "≡ " {
			t.keys = "Esc Esc · right-click"
		}
		tips = append(tips, t)
	}
	return tips
}

// findBarButtonTips labels the find bar's toggles, history labels and
// replace buttons — each with the in-bar key that does the same thing
// (the bar owns the keyboard, so its doors are Alt chords and arrows,
// not leaders).
func (a *App) findBarButtonTips() []buttonTip {
	if !a.findOpen {
		return nil
	}
	onOff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	tips := []buttonTip{
		{rect: a.findCaseRect(), text: []string{"Match case: " + onOff(a.findCase)}, keys: "Alt+c"},
		{rect: a.findWordRect(), text: []string{"Whole word: " + onOff(a.findWord)}, keys: "Alt+w"},
		{rect: a.findRegexRect(), text: []string{"Regex: " + onOff(a.findRegex), "Replace takes $1, ${name}"}, keys: "Alt+r"},
		{rect: a.findHistBtnRect(findFocusQuery), text: []string{"Recent searches"}, keys: "↑ in the field"},
	}
	if a.findReplaceOpen {
		tips = append(tips,
			buttonTip{rect: a.findHistBtnRect(findFocusReplace), text: []string{"Recent replacements"}, keys: "↑ in the field"},
			buttonTip{rect: a.findReplaceBtnRect(), text: []string{"Replace this match, go to the next"}, keys: "Enter in Repl"},
			buttonTip{rect: a.findAllBtnRect(), text: []string{"Replace every match (one undo step)"}, keys: "Alt+a"},
		)
	}
	return tips
}

// findAllButtonTips labels the Find-all list's title buttons and its
// replace button. The ◇/◆ and ◨/⬒ glyphs name the state a click
// switches TO, so the labels do too.
func (a *App) findAllButtonTips(m *findAllModal) []buttonTip {
	_, _, mw, mh := m.rect(a)
	if mw < findAllMinDrawWidth || mh < findAllMinHeight {
		return nil // draw painted nothing, so there is nothing to label
	}
	pin := "Pin: keep the list open while you edit"
	if m.pinned {
		pin = "Unpin: back to a list Esc closes"
	}
	dock := "Dock the list on the right"
	if a.findAllDockRight {
		dock = "Dock the list across the top"
	}
	// r is the revert button's key twin only while the list holds the
	// keyboard; pinned, the editor owns the keys and the button is the door.
	revertKeys := "r"
	if m.pinned {
		revertKeys = ""
	}
	tips := []buttonTip{
		{rect: m.revertRect(a), text: []string{"Revert location: back to where the list opened"}, keys: revertKeys},
		{rect: m.rerunRect(a), text: []string{"Run the search again"}},
		{rect: m.pinRect(a), text: []string{pin}},
		{rect: m.dockRect(a), text: []string{dock}},
	}
	if m.pinned {
		tips = append(tips, buttonTip{rect: m.closePinRect(a), text: []string{"Close the list"}})
	}
	if f := m.fieldsLayout(a); f.button.w > 0 {
		tips = append(tips, buttonTip{rect: f.button,
			text: []string{"Replace every listed match (one undo step)"}})
	}
	return tips
}

// paletteHeadingButtonTips labels a picker's section headings — Recent
// folders' and Recent locations' "MRU 5" / "MFU 10" — on the rows the
// last draw put them: the name with the row's tip, the ⧉ with its
// copyTip and Alt twin. Rows are walked exactly as draw walks them (from
// m.top, visibleRows deep, rows from my+4), and both rects come from the
// helpers draw itself uses (headingNameRect, headingCopyRect). A picker
// with no headings — almost all of them, and every picker while its
// query filters, since headings vanish then — labels nothing.
func (a *App) paletteHeadingButtonTips(m *paletteModal) []buttonTip {
	mx, my, mw, mh := m.rect(a)
	var tips []buttonTip
	for i := 0; i < m.visibleRows(mh) && m.top+i < len(m.matches); i++ {
		it := m.matches[m.top+i].item
		if it.heading == "" {
			continue
		}
		ry := my + 4 + i
		if len(it.tip) > 0 {
			tips = append(tips, buttonTip{rect: headingNameRect(mx, ry, mw, it), text: it.tip})
		}
		if r, ok := headingCopyRect(mx, ry, mw, it); ok {
			text := it.copyTip
			if len(text) == 0 {
				text = []string{"Copy this section"}
			}
			t := buttonTip{rect: r, text: text}
			if it.copyKey != 0 {
				t.keys = "Alt+" + string(it.copyKey)
			}
			tips = append(tips, t)
		}
	}
	return tips
}

// toolButtonTips labels the tool windows' header buttons: the generic
// header's ✕ (the tree, the Conflicts panel), and each hand-drawn
// panel's ✕ / ⟳ / ⏹ / Actions ▾ and friends, gated on the same
// conditions their draw code uses.
func (a *App) toolButtonTips() []buttonTip {
	var tips []buttonTip

	// The generic header's ✕, wherever a tool wears it.
	for _, d := range toolDefs {
		if !a.toolNeedsHeader(d.id) {
			continue
		}
		t := buttonTip{rect: a.toolHeaderCloseRect(d.id), text: []string{"Hide " + d.title}}
		if d.id == toolProject {
			t.keyFn = (*App).menuToggleSidebar
		}
		tips = append(tips, t)
	}

	// The tree's ⭑ after the project name.
	if r, ok := a.recentStarRect(); ok {
		tips = append(tips, buttonTip{rect: r, text: []string{"Recent folders…"},
			keyFn: (*App).menuRecentFolders})
	}

	if a.toolOpen(toolGit) {
		tips = append(tips,
			buttonTip{rect: a.gitPanelCloseRect(), text: []string{"Close the git panel"}, keyFn: (*App).menuToggleGitPanel},
			buttonTip{rect: a.gitPanelActionsRect(), text: []string{"Git actions on the ticked files", "(or the highlighted one)"}},
			buttonTip{rect: a.gitPanelReviewRect(), text: []string{"Review the changes one file at a time"}},
		)
		tips = append(tips, a.diffToolbarTips(a.gitPanelToolbar())...)
	}
	if a.toolOpen(toolGitLog) {
		tips = append(tips,
			buttonTip{rect: a.gitLogCloseRect(), text: []string{"Close the git log"}, keyFn: (*App).menuToggleGitLog},
			buttonTip{rect: a.gitLogRefreshRect(), text: []string{"Reload the history"}},
			buttonTip{rect: a.gitLogCopyRect(), text: []string{"Copy the selected commit's full hash"}},
			buttonTip{rect: a.gitLogActionsRect(), text: []string{"Actions on the selected commit"}},
			buttonTip{rect: a.gitLogPushRect(), text: []string{"Push the current branch…"}, keyFn: (*App).menuGitPush},
			buttonTip{rect: a.gitLogSearchRect(), text: []string{"Search the history"}, keyFn: (*App).menuGitLogSearch},
		)
		tips = append(tips, a.diffToolbarTips(a.gitLogToolbar())...)
		if a.gitLogFilterRows() > 0 {
			for i, c := range gitLogModeChips {
				text := gitLogChipTip(c.mode)
				if c.prefix != "" {
					text += " (or type " + c.prefix + ")"
				}
				tips = append(tips, buttonTip{rect: a.gitLogChipRect(i), text: []string{text}})
			}
		}
	}
	if a.toolOpen(toolConflicts) {
		op := a.conflictPanel.info.op
		for _, b := range a.conflictOpButtons() {
			var text string
			switch b.label {
			case "[ Continue ]":
				text = "Continue the " + op
				if !b.enabled && b.why != "" {
					text = b.why
				}
			case "[ Skip ]":
				text = "Skip this commit and go on"
			case "[ Abort ]":
				text = "Abort the " + op + " — back to where it started"
			case "⟳":
				text = "Re-read the conflict state"
			default:
				continue
			}
			tips = append(tips, buttonTip{rect: b.rect, text: []string{text}})
		}
	}
	if a.toolOpen(toolProblems) {
		tips = append(tips,
			buttonTip{rect: a.problemsCloseRect(), text: []string{"Close the problems list"}, keyFn: (*App).menuToggleProblems},
			buttonTip{rect: a.problemsRefreshRect(), text: []string{"Rebuild the list"}},
		)
		for _, c := range a.problemsChips() {
			tips = append(tips, buttonTip{rect: c.rect, text: []string{problemsChipTip(c)}})
		}
	}
	if a.toolOpen(toolCompare) {
		tips = append(tips,
			buttonTip{rect: a.compareCloseRect(), text: []string{"Close the compare view"}},
			buttonTip{rect: a.compareRefreshRect(), text: []string{"Diff again against the buffer as it is now"}},
		)
		tips = append(tips, a.diffToolbarTips(a.compareToolbar())...)
		tips = append(tips, a.compareRevertChipTips()...)
	}
	if a.toolOpen(toolTerminal) {
		tips = append(tips, buttonTip{rect: a.termCloseRect(), text: []string{"Close the terminal"},
			keyFn: (*App).leaderTerminal})
		if a.term.running {
			tips = append(tips, buttonTip{rect: a.termStopRect(),
				text: []string{"Stop the command (interrupt, then kill)"}})
		}
	}
	if a.toolOpen(toolChat) {
		tips = append(tips, buttonTip{rect: a.chatCloseRect(), text: []string{"Close the chat"},
			keyFn: (*App).leaderChat})
		if a.chat.turnActive {
			tips = append(tips, buttonTip{rect: a.chatStopRect(), text: []string{"Stop the agent's turn"}})
		}
		for i := range a.chatAttachRowsView() {
			tips = append(tips, buttonTip{rect: a.chatAttachRemoveRect(i),
				text: []string{"Remove this attachment"}})
		}
	}
	return tips
}

// recentStarRect is the tree's ⭑ in screen cells. RecentStarAt answers
// in tree-local cells and the star is stamped by the tree's own render,
// so it is found by asking along the rows where a project row can sit
// rather than by duplicating the tree's layout here.
func (a *App) recentStarRect() (btnRect, bool) {
	if !a.sidebarShown || a.tree == nil {
		return btnRect{}, false
	}
	sx, sy, sw, sh := a.sidebarRect()
	for ly := 0; ly < min(2, sh); ly++ {
		for lx := 0; lx < sw; lx++ {
			if a.tree.RecentStarAt(lx, ly) {
				return btnRect{x: sx + lx, y: sy + ly, w: 1}, true
			}
		}
	}
	return btnRect{}, false
}

// gitLogChipTip says what a git-log search mode matches.
func gitLogChipTip(mode int) string {
	switch mode {
	case gitLogModeMessage:
		return "Search commit messages"
	case gitLogModeAuthor:
		return "Search by author"
	case gitLogModePath:
		return "Commits that touched a path"
	case gitLogModePickaxe:
		return "Commits that added or removed a string"
	case gitLogModeCommit:
		return "Find a commit by hash or ref"
	}
	return "Search mode"
}

// problemsChipTip says what clicking a Problems header chip will do. A
// lit severity chip means its rows are IN the list, so the click hides
// them; the scope chip names where it is and where it goes.
func problemsChipTip(c problemsChip) string {
	if c.kind == problemsChipScope {
		if c.on {
			return "Showing this file — click for all files"
		}
		return "Showing all files — click for this file only"
	}
	noun := map[int]string{
		lsp.SeverityError:   "errors",
		lsp.SeverityWarning: "warnings",
		lsp.SeverityInfo:    "info notes",
	}[c.kind]
	if noun == "" {
		noun = "these"
	}
	if c.on {
		return "Hide " + noun
	}
	return "Show " + noun
}
