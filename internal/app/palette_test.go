// =============================================================================
// File: internal/app/palette_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-08
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/customactions"
	"github.com/rohanthewiz/ced/internal/finder"
)

// paletteOf returns the active modal as a *paletteModal, or nil when
// the palette isn't the open modal — same typed-accessor pattern the
// other modal tests use so assertions read cleanly.
func paletteOf(a *App) *paletteModal {
	m, _ := a.modal.(*paletteModal)
	return m
}

// typePalette feeds a string through the palette's key handler one
// rune at a time, the same path real keystrokes take.
func typePalette(a *App, s string) {
	for _, r := range s {
		paletteOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
}

// TestOpenPalette_GathersEnabledActions pins the inventory contract:
// with no tab open, tab-dependent actions ("Save") must not be
// listed, always-available ones ("Quit editor") must be, and the
// palette must never list itself.
func TestOpenPalette_GathersEnabledActions(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openPalette()

	m := paletteOf(a)
	if m == nil {
		t.Fatal("openPalette should install the palette modal")
	}
	labels := map[string]bool{}
	for _, it := range m.items {
		labels[it.label] = true
	}
	if labels["Save"] {
		t.Fatal("'Save' should be excluded with no savable tab")
	}
	if !labels["Quit editor"] {
		t.Fatal("'Quit editor' should always be listed")
	}
	if labels[paletteMenuLabel] {
		t.Fatal("the palette must not list itself")
	}
}

// TestOpenPalette_EmptyQueryKeepsMenuOrder pins the browse mode: with
// no query, every item matches and the order mirrors the action menu
// so the palette doubles as a readable menu.
func TestOpenPalette_EmptyQueryKeepsMenuOrder(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openPalette()

	m := paletteOf(a)
	if len(m.matches) != len(m.items) {
		t.Fatalf("empty query should match everything: %d matches for %d items", len(m.matches), len(m.items))
	}
	for i := range m.items {
		if m.matches[i].item.label != m.items[i].label {
			t.Fatalf("row %d: got %q, want %q (source order)", i, m.matches[i].item.label, m.items[i].label)
		}
	}
}

// TestPaletteKey_TypingFilters walks the keystroke loop: a query must
// narrow the list and rank the obvious target on top, so Enter-after-
// typing does what the user expects.
func TestPaletteKey_TypingFilters(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openPalette()
	typePalette(a, "quit")

	m := paletteOf(a)
	if len(m.matches) == 0 {
		t.Fatal("expected matches for 'quit'")
	}
	if got := m.matches[0].item.label; got != "Quit editor" {
		t.Fatalf("top match: got %q, want %q", got, "Quit editor")
	}
	if len(m.matches) >= len(m.items) {
		t.Fatal("query should narrow the match list")
	}
}

// TestPaletteKey_EnterRunsAction is the headline outcome: Enter on
// the top match actually fires the action and dismisses the modal.
// Uses the sidebar toggle because its effect (sidebarShown flipping)
// is directly observable on App.
func TestPaletteKey_EnterRunsAction(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openPalette()
	typePalette(a, "hide file")
	if got := paletteOf(a).matches[0].item.label; got != "Hide file explorer" {
		t.Fatalf("top match: got %q, want %q", got, "Hide file explorer")
	}

	paletteOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if paletteOf(a) != nil {
		t.Fatal("modal should close after Enter")
	}
	if a.sidebarShown {
		t.Fatal("running 'Hide file explorer' should hide the sidebar")
	}
}

// TestPaletteKey_EnterOnEmptyIsNoop guards the degenerate path: Enter
// with zero matches must neither run anything nor panic.
func TestPaletteKey_EnterOnEmptyIsNoop(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openPalette()
	typePalette(a, "zzzzzzzz no such action")
	if len(paletteOf(a).matches) != 0 {
		t.Fatal("expected zero matches for gibberish query")
	}

	paletteOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if paletteOf(a) == nil {
		t.Fatal("Enter on empty match list should keep the modal open")
	}
	if a.quit {
		t.Fatal("no action should have run")
	}
}

// TestPaletteKey_ArrowsMoveSelection pins the navigation contract so
// Enter can't silently run the wrong row: ↓ advances, ↑ retreats,
// neither runs off the ends.
func TestPaletteKey_ArrowsMoveSelection(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openPalette()

	paletteOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if paletteOf(a).selected != 1 {
		t.Fatalf("selected after ↓: got %d, want 1", paletteOf(a).selected)
	}
	paletteOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	if paletteOf(a).selected != 0 {
		t.Fatalf("selected after ↑: got %d, want 0", paletteOf(a).selected)
	}
	paletteOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	if paletteOf(a).selected != 0 {
		t.Fatalf("selected at top after ↑: got %d, want 0", paletteOf(a).selected)
	}
}

// TestPaletteKey_EscCloses pins the cancel path and that a reopen
// starts fresh — the discarded modal's query and selection must never
// resurrect.
func TestPaletteKey_EscCloses(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openPalette()
	typePalette(a, "x")
	paletteOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))

	if paletteOf(a) != nil {
		t.Fatal("Esc should close the palette")
	}
	a.openPalette()
	if got := paletteOf(a).field.String(); got != "" {
		t.Fatalf("reopened palette should start with an empty query, got %q", got)
	}
	if paletteOf(a).selected != 0 {
		t.Fatalf("reopened palette should start at selection 0, got %d", paletteOf(a).selected)
	}
}

// TestPaletteMouse_ClickRunsRow walks the click path: clicking a row
// selects and runs it. Row geometry comes from the same rect helper
// the renderer uses so the math can't drift.
func TestPaletteMouse_ClickRunsRow(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openPalette()
	typePalette(a, "hide file")
	mx, my, _, _ := paletteOf(a).rect(a)

	paletteOf(a).handleMouse(a, mx+5, my+4, tcell.Button1)

	if paletteOf(a) != nil {
		t.Fatal("modal should close after click-run")
	}
	if a.sidebarShown {
		t.Fatal("clicking 'Hide file explorer' should hide the sidebar")
	}
}

// TestPaletteMouse_ClickOutsideCloses guards the dismiss path: a
// stray click outside the modal closes it without running anything.
func TestPaletteMouse_ClickOutsideCloses(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openPalette()

	paletteOf(a).handleMouse(a, 0, 0, tcell.Button1)

	if paletteOf(a) != nil {
		t.Fatal("modal should close on outside click")
	}
	if a.quit || !a.sidebarShown {
		t.Fatal("outside click must not run any action")
	}
}

// TestPalette_CustomActionsListed pins the second half of the
// inventory: user-configured actions from actions.json flow through
// menuLayout into the palette alongside the built-ins.
func TestPalette_CustomActionsListed(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.customActions = []customactions.Action{{Label: "Deploy to prod", Command: "true"}}
	a.openPalette()

	found := false
	for _, it := range paletteOf(a).items {
		if it.label == "Deploy to prod" {
			found = true
		}
	}
	if !found {
		t.Fatal("custom action should be listed in the palette")
	}
}

// TestPalette_TwinRowListedOnce pins that a verb living in two ≡
// sections on purpose (Select all in File and Edit) shows up once in the
// palette — two identical rows would read as a bug.
func TestPalette_TwinRowListedOnce(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "a.txt")
	if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	a.openFile(p)
	a.openPalette()

	n := 0
	for _, it := range paletteOf(a).items {
		if it.label == "Select all" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("palette lists %d 'Select all' rows, want 1", n)
	}
}

// TestPalette_FoldedSectionActionsStillListed pins that the ≡-menu fold
// state is a display concern only: collapsing a section in the menu must
// NOT remove its actions from the command palette, and the palette must
// never surface a section header as a fake command.
func TestPalette_FoldedSectionActionsStillListed(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.toggleMenuSection("File") // fold the File section in the menu
	a.openPalette()

	var sawNewFile, sawHeader bool
	for _, it := range paletteOf(a).items {
		if it.label == "New file" { // a File-section action
			sawNewFile = true
		}
		if it.label == "File" || it.label == "Git" || it.label == "Nav" {
			sawHeader = true // a section title leaked in as a command
		}
	}
	if !sawNewFile {
		t.Error("folding a menu section hid its action from the palette")
	}
	if sawHeader {
		t.Error("a section header leaked into the palette as a command")
	}
}

// TestLeader_KFiresPalette pins the Esc-k binding so a leader-table
// refactor can't quietly drop the palette shortcut. It is the palette's
// only leader since 'a' became the AI prefix, which makes this the one
// test standing between the palette and a keyboard-unreachable state.
func TestLeader_KFiresPalette(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	action := leaderActionFor('k')
	if action == nil {
		t.Fatal("Esc-k has no leader binding")
	}
	action(a)
	if paletteOf(a) == nil {
		t.Fatal("Esc-k should open the palette")
	}
}

// TestMenu_CommandPaletteEntry pins the mouse-first rule: the palette
// must be reachable from the ≡ menu, not just the leader key.
func TestMenu_CommandPaletteEntry(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	items, _, _ := a.menuLayout()
	for _, it := range items {
		if it.label == paletteMenuLabel {
			it.action(a)
			if paletteOf(a) == nil {
				t.Fatal("menu entry should open the palette")
			}
			return
		}
	}
	t.Fatalf("no %q entry in the action menu", paletteMenuLabel)
}

// TestPaletteDraw_RendersTitleAndRows smoke-tests the renderer on a
// simulation screen: the frame title and the first action label must
// land in the drawn cells. Catches geometry regressions (off-by-one
// row starts) that the logic tests can't see.
func TestPaletteDraw_RendersTitleAndRows(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openPalette()
	paletteOf(a).draw(a)
	a.screen.Show()

	content := screenText(a)
	if !strings.Contains(content, paletteMenuLabel) {
		t.Fatalf("drawn screen should contain the title %q", paletteMenuLabel)
	}
	first := paletteOf(a).matches[0].item.label
	if !strings.Contains(content, first) {
		t.Fatalf("drawn screen should contain the first row %q", first)
	}
}

// screenText flattens the simulation screen into one string per row,
// joined by newlines, so tests can substring-match drawn output
// without caring about styles.
func screenText(a *App) string {
	scr := a.screen.(tcell.SimulationScreen)
	cells, w, h := scr.GetContents()
	out := make([]rune, 0, (w+1)*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) > 0 {
				out = append(out, c.Runes[0])
			} else {
				out = append(out, ' ')
			}
		}
		out = append(out, '\n')
	}
	return string(out)
}

// buildFinderT wires a ready file index onto the app, blocking until
// the background build lands so tests see a deterministic path list.
func buildFinderT(t *testing.T, a *App) {
	t.Helper()
	a.finder = finder.New(a.rootDir)
	done := make(chan struct{})
	a.finder.Rebuild(func() { close(done) })
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("finder build did not finish within 3s")
	}
}

// TestOpenPalette_ExcludesFileItems pins that project files are NOT a
// palette source: even with a ready index, no path row is listed and a
// query naming a file matches nothing from the index. In a large repo
// the path rows used to bury the actions the palette exists to reach;
// files belong to the finder modal.
func TestOpenPalette_ExcludesFileItems(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFileT(t, filepath.Join(dir, "alpha.go"), "package a\n")
	writeFileT(t, filepath.Join(sub, "beta.txt"), "b\n")

	a := newTestApp(t, dir)
	buildFinderT(t, a)
	a.openPalette()
	m := paletteOf(a)

	for _, it := range m.items {
		if it.label == "alpha.go" || it.label == filepath.Join("sub", "beta.txt") {
			t.Fatalf("palette must not list project files; found %q", it.label)
		}
	}
	// Actions are still all there — the source seam kept working.
	var sawQuit bool
	for _, it := range m.items {
		if it.label == "Quit editor" {
			sawQuit = true
		}
	}
	if !sawQuit {
		t.Fatal("palette should still list actions (Quit editor missing)")
	}
}

// TestOpenPalette_LeavesIndexIdle verifies opening the palette no longer
// kicks a background walk of the project: with files out of the palette
// that build would be pure cost on every open.
func TestOpenPalette_LeavesIndexIdle(t *testing.T) {
	dir := t.TempDir()
	writeFileT(t, filepath.Join(dir, "alpha.go"), "package a\n")

	a := newTestApp(t, dir)
	a.finder = finder.New(a.rootDir)
	a.openPalette()
	if got := a.finder.State(); got != finder.StateIdle {
		t.Fatalf("finder state after openPalette = %v, want StateIdle", got)
	}
}

// TestOpenPicker_TitleAndCallerItems pins the picker mode: the given
// title heads the frame, the caller's items are taken verbatim (no
// sources), and Enter runs the chosen item's action.
func TestOpenPicker_TitleAndCallerItems(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	ran := ""
	a.openPicker("Pick a thing", []paletteItem{
		{label: "one", run: func(*App) { ran = "one" }},
		{label: "two", run: func(*App) { ran = "two" }},
	})

	m := paletteOf(a)
	if m == nil || m.sourced {
		t.Fatalf("picker should be a non-sourced paletteModal, got %T", a.modal)
	}
	if len(m.items) != 2 {
		t.Fatalf("picker items = %d, want the 2 given", len(m.items))
	}

	m.draw(a)
	a.screen.Show()
	if !strings.Contains(screenText(a), "Pick a thing") {
		t.Fatal("picker should draw its own title")
	}

	typePalette(a, "two")
	m.runSelected(a)
	if ran != "two" {
		t.Fatalf("ran = %q, want the filtered selection to fire", ran)
	}
	if a.modal != nil {
		t.Fatal("running a picker item should close the modal")
	}
}

// TestMenuTypingOpensSearch pins the searchable-≡-menu contract: a rune
// typed while the menu is open switches to the fuzzy action search,
// seeded with that rune, listing rows from COLLAPSED sections too.
func TestMenuTypingOpensSearch(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.setAllMenuSections(true) // startup default: everything folded
	a.openMenu()

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone))
	if a.menuOpen {
		t.Fatal("typing should close the menu proper")
	}
	m := paletteOf(a)
	if m == nil {
		t.Fatalf("typing should open the search picker, got %T", a.modal)
	}
	if got := m.field.String(); got != "q" {
		t.Fatalf("query should be seeded with the typed rune, got %q", got)
	}
	// "q" must find Quit even though every section is folded.
	found := false
	for _, match := range m.matches {
		if match.item.label == "Quit editor" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("search should reach rows inside collapsed sections")
	}
}

// TestMenuTypingIgnoresModifiedRunes keeps Alt/Meta chords out of the
// search seed — those belong to the leader and Cmd layers.
func TestMenuTypingIgnoresModifiedRunes(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openMenu()
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModMeta))
	if !a.menuOpen {
		t.Fatal("a modified rune must not enter search mode")
	}
}

// spacerPicker opens a picker of two named sections split by a spacer —
// the Recent folders shape, without the filesystem.
func spacerPicker(a *App, before, after int) *paletteModal {
	var items []paletteItem
	for i := 0; i < before; i++ {
		items = append(items, paletteItem{label: "recent" + string(rune('a'+i)), run: func(*App) {}})
	}
	items = append(items, paletteSpacer())
	for i := 0; i < after; i++ {
		items = append(items, paletteItem{label: "frequent" + string(rune('a'+i)), run: func(*App) {}})
	}
	return a.openPickerRows("Sections", items, 0, nil)
}

// TestPaletteSpacer_ArrowsSkipIt pins that a divider can never hold the
// highlight: ↓ from the last row above it lands on the first row below,
// and ↑ comes straight back.
func TestPaletteSpacer_ArrowsSkipIt(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := spacerPicker(a, 2, 2)
	down := tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
	up := tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)

	m.handleKey(a, down) // recentb
	m.handleKey(a, down) // over the spacer
	if got := m.matches[m.selected].item.label; got != "frequenta" {
		t.Fatalf("after ↓↓ selected %q, want frequenta", got)
	}
	m.handleKey(a, up)
	if got := m.matches[m.selected].item.label; got != "recentb" {
		t.Fatalf("after ↑ selected %q, want recentb", got)
	}
}

// TestPaletteSpacer_OnlyWhileUnfiltered pins that the divider belongs to
// the sectioned view: a query ranks by score and drops it, and the
// count tail never counts it.
func TestPaletteSpacer_OnlyWhileUnfiltered(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := spacerPicker(a, 2, 2)
	if len(m.matches) != 5 || m.countMatches() != 4 || countItems(m.items) != 4 {
		t.Fatalf("unfiltered: %d matches, %d counted, %d items — want 5 / 4 / 4",
			len(m.matches), m.countMatches(), countItems(m.items))
	}
	typePalette(a, "freq")
	for _, mt := range m.matches {
		if mt.item.spacer {
			t.Fatal("a spacer survived filtering")
		}
	}
}

// TestPaletteSpacer_ClickDoesNothing pins that clicking the divider row
// neither runs nor selects anything — it is not a choice.
func TestPaletteSpacer_ClickDoesNothing(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := spacerPicker(a, 1, 1)
	m.draw(a)
	mx, my, _, _ := m.rect(a)
	m.handleMouse(a, mx+5, my+4+1, tcell.Button1) // row 1 is the spacer
	if paletteOf(a) == nil {
		t.Fatal("clicking the spacer closed the picker")
	}
	if m.selected != 0 {
		t.Fatalf("selected = %d, want the click ignored", m.selected)
	}
}

// TestPalette_ScrollsToTheSelection pins that rows past the visible
// window are reachable: ↓ past the last drawn row scrolls the list, and
// the highlighted row is the one drawn — the palette used to leave the
// highlight off-screen and ask for a narrower query.
func TestPalette_ScrollsToTheSelection(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := spacerPicker(a, 5, 10) // 16 rows, 10 visible
	down := tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
	for i := 0; i < 14; i++ {
		m.handleKey(a, down)
	}
	m.draw(a)
	a.screen.Show()
	want := m.matches[m.selected].item.label
	if want != "frequentj" {
		t.Fatalf("selected %q after 14 ↓, want the last row", want)
	}
	if !strings.Contains(screenText(a), want) {
		t.Fatalf("the selected row %q is not on screen", want)
	}
	if m.top == 0 {
		t.Fatal("the window never scrolled")
	}
	// A click now maps through the scroll offset.
	mx, my, _, _ := m.rect(a)
	m.handleMouse(a, mx+5, my+4, 0)
	if m.selected != m.top {
		t.Fatalf("hover on the first drawn row selected %d, want top %d", m.selected, m.top)
	}
}

// TestOpenPickerRows_AsksForTheHeight pins that a sized picker grows its
// frame to the rows requested (and an ordinary one keeps the usual ten).
func TestOpenPickerRows_AsksForTheHeight(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.width, a.height = 100, 40
	m := a.openPickerRows("Tall", []paletteItem{{label: "x", run: func(*App) {}}}, 17, nil)
	if _, _, _, h := m.rect(a); h != 17+6 {
		t.Fatalf("height = %d, want 23", h)
	}
	a.openPicker("Short", []paletteItem{{label: "x", run: func(*App) {}}})
	if _, _, _, h := paletteOf(a).rect(a); h != paletteResultsVisible+6 {
		t.Fatalf("ordinary picker height = %d, want %d", h, paletteResultsVisible+6)
	}
}

// TestPaletteRow_Swatch pins the swatch: a row with one paints two cells
// of that colour where the label would start and shifts the label past
// them; a row without one is unchanged.
func TestPaletteRow_Swatch(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openPicker("Colours", []paletteItem{
		{label: "red", swatch: tcell.ColorRed, run: func(*App) {}},
		{label: "none", run: func(*App) {}},
	})
	m := paletteOf(a)
	m.draw(a)
	mx, my, _, _ := m.rect(a)
	row := my + 4
	for _, x := range []int{mx + 2, mx + 3} {
		if _, _, st, _ := a.screen.GetContent(x, row); true {
			if _, bg, _ := st.Decompose(); bg != tcell.ColorRed {
				t.Errorf("swatch cell %d bg = %v, want red", x, bg)
			}
		}
	}
	if ru, _, _, _ := a.screen.GetContent(mx+2+paletteSwatchW, row); ru != 'r' {
		t.Errorf("label starts with %q, want it after the swatch", ru)
	}
	if ru, _, _, _ := a.screen.GetContent(mx+2, row+1); ru != 'n' {
		t.Errorf("unswatched row starts with %q, want its label at the usual column", ru)
	}
}

// panePicker opens a two-pane picker (the Recent folders shape, without
// the filesystem): "alpha" holds a1, a2; "beta" holds b1, ab.
func panePicker(a *App) *paletteModal {
	row := func(l string) paletteItem { return paletteItem{label: l, run: func(*App) {}} }
	return a.openPickerPanes("Panes", []palettePane{
		{name: "alpha", items: []paletteItem{row("a1"), row("a2")}},
		{name: "beta", items: []paletteItem{row("b1"), row("ab")}},
	}, 0, 0)
}

// TestPalettePanes_TabCyclesAndKeepsTheQuery pins the keyboard switch:
// Tab / Shift-Tab cycle the panes (wrapping), the query survives the
// switch and re-filters the new list, and the highlight restarts.
func TestPalettePanes_TabCyclesAndKeepsTheQuery(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := panePicker(a)
	tab := tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)
	back := tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone)

	typePalette(a, "a")
	m.selected = 1
	m.handleKey(a, tab)
	if m.pane != 1 || m.field.String() != "a" || m.selected != 0 {
		t.Fatalf("after Tab: pane %d, query %q, selected %d — want 1, \"a\", 0", m.pane, m.field.String(), m.selected)
	}
	if got := pickerLabels(t, a); len(got) != 1 || got[0] != "ab" {
		t.Fatalf("beta under \"a\" = %v, want [ab]", got)
	}
	m.handleKey(a, tab) // wraps
	if m.pane != 0 {
		t.Fatalf("Tab past the last pane = %d, want 0", m.pane)
	}
	m.handleKey(a, back) // wraps the other way
	if m.pane != 1 {
		t.Fatalf("Shift-Tab from the first pane = %d, want 1", m.pane)
	}
}

// TestPalettePanes_TabIsInertWithoutPanes pins that an ordinary picker
// ignores Tab: no pane to switch to, and nothing typed into the query.
func TestPalettePanes_TabIsInertWithoutPanes(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := spacerPicker(a, 2, 2)
	m.handleKey(a, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	if m.field.String() != "" || len(m.matches) != 5 {
		t.Fatalf("Tab changed a single-list picker: query %q, %d matches", m.field.String(), len(m.matches))
	}
	if _, rects, _ := m.paneTabs(a); rects != nil {
		t.Fatal("a single-list picker must draw no pane tabs")
	}
}

// TestPalettePanes_CountsFollowTheQuery pins the numbers on the tabs:
// every row unfiltered, the MATCH count under a query — for the inactive
// pane too, which is what tells you to switch.
func TestPalettePanes_CountsFollowTheQuery(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := panePicker(a)
	if m.paneCount(0) != 2 || m.paneCount(1) != 2 {
		t.Fatalf("unfiltered counts = %d / %d, want 2 / 2", m.paneCount(0), m.paneCount(1))
	}
	typePalette(a, "b")
	if m.paneCount(0) != 0 || m.paneCount(1) != 2 {
		t.Fatalf("counts under \"b\" = %d / %d, want 0 / 2", m.paneCount(0), m.paneCount(1))
	}
}

// TestPalettePanes_ClickSwitchesAndDraws pins the mouse door and the one
// geometry: the tabs are painted where paneTabs says, and a press on the
// second one shows its list without closing the picker.
func TestPalettePanes_ClickSwitchesAndDraws(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := panePicker(a)
	m.draw(a)
	a.screen.Show()
	labels, rects, showTitle := m.paneTabs(a)
	if len(rects) != 2 || !showTitle {
		t.Fatalf("tabs = %v (title shown %v), want both beside the title", rects, showTitle)
	}
	rows := strings.Split(screenText(a), "\n")
	if got := string([]rune(rows[rects[1].y])[rects[1].x : rects[1].x+rects[1].w]); got != labels[1] {
		t.Fatalf("drawn tab = %q, want %q", got, labels[1])
	}
	m.handleMouse(a, rects[1].x+1, rects[1].y, tcell.Button1)
	if a.modal != m || m.pane != 1 {
		t.Fatalf("after clicking beta: modal %T, pane %d — want the picker on pane 1", a.modal, m.pane)
	}
	// Hover alone never switches.
	m.handleMouse(a, rects[0].x+1, rects[0].y, tcell.ButtonNone)
	if m.pane != 1 {
		t.Fatal("hovering a tab switched panes")
	}
}

// TestPalettePanes_TitleGivesWayWhenNarrow pins the squeeze rule: when
// the tabs and the title don't both fit, the title is dropped, never the
// tabs, and no tab runs into the esc hint.
func TestPalettePanes_TitleGivesWayWhenNarrow(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	row := paletteItem{label: "x", run: func(*App) {}}
	m := a.openPickerPanes("A rather long picker title", []palettePane{
		{name: "Global", items: []paletteItem{row}},
		{name: "Local", items: []paletteItem{row}},
	}, 0, 0)
	a.width = 34
	_, rects, showTitle := m.paneTabs(a)
	if showTitle {
		t.Fatal("the title should give way at this width")
	}
	mx, _, mw, _ := m.rect(a)
	if len(rects) == 0 || rects[0].x != mx+2 {
		t.Fatalf("tabs = %v, want the first at the title's spot", rects)
	}
	last := rects[len(rects)-1]
	if last.x+last.w > mx+mw-1-runeLen("esc ")-1 {
		t.Fatalf("tab %v runs into the esc hint", last)
	}
}

// labelPicker opens a single-list picker over the given labels, each a
// no-op row — the smallest fixture that exercises refresh's filtering.
func labelPicker(a *App, labels ...string) *paletteModal {
	var items []paletteItem
	for _, l := range labels {
		items = append(items, paletteItem{label: l, run: func(*App) {}})
	}
	return a.openPickerRows("Labels", items, 0, nil)
}

// matchLabels lists the labels of the current matches in order.
func matchLabels(m *paletteModal) []string {
	var out []string
	for _, mt := range m.matches {
		out = append(out, mt.item.label)
	}
	return out
}

// TestPaletteRefresh_HidesScatteredWhenContiguousExists pins the finder's
// rule in the palette: once a row holds the query whole, rows that only
// hold its letters in order ("Recent folders…" for "ref") are dropped,
// not just ranked below.
func TestPaletteRefresh_HidesScatteredWhenContiguousExists(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := labelPicker(a, "Recent folders…", "Find references…", "Remote open: off")
	typePalette(a, "ref")
	if got := matchLabels(m); len(got) != 1 || got[0] != "Find references…" {
		t.Fatalf("matches = %q, want only the contiguous hit", got)
	}
}

// TestPaletteRefresh_KeepsScatteredWithoutContiguous is the fallback
// side: with no row holding the query whole, an abbreviation still
// finds its row.
func TestPaletteRefresh_KeepsScatteredWithoutContiguous(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := labelPicker(a, "Close tab", "Recent files…")
	typePalette(a, "rcf")
	if got := matchLabels(m); len(got) != 1 || got[0] != "Recent files…" {
		t.Fatalf("matches = %q, want the scattered hit kept", got)
	}
}

// TestPalettePanes_CountsHideScatteredLikeTheList pins that a tab's
// number is the row count a switch would show: the pane holding the
// query whole counts only that row, the pane with nothing contiguous
// counts its scattered hit.
func TestPalettePanes_CountsHideScatteredLikeTheList(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	row := func(l string) paletteItem { return paletteItem{label: l, run: func(*App) {}} }
	m := a.openPickerPanes("Panes", []palettePane{
		{name: "alpha", items: []paletteItem{row("Recent folders…"), row("Find references…")}},
		{name: "beta", items: []paletteItem{row("Recent folders…")}},
	}, 0, 0)
	typePalette(a, "ref")
	if m.paneCount(0) != 1 || m.paneCount(1) != 1 {
		t.Fatalf("counts under \"ref\" = %d / %d, want 1 / 1", m.paneCount(0), m.paneCount(1))
	}
	if len(m.matches) != m.paneCount(0) {
		t.Fatalf("active list has %d rows, its tab says %d", len(m.matches), m.paneCount(0))
	}
}

// TestDropScatteredMatches_NoMatches is the trivial edge: nothing in,
// nothing out, no panic on the empty slice.
func TestDropScatteredMatches_NoMatches(t *testing.T) {
	if got := dropScatteredMatches(nil, "abc"); len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}
