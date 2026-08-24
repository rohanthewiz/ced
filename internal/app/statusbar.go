// =============================================================================
// File: internal/app/statusbar.go
// Author: Rohan Allison
// =============================================================================

// The status bar as a row of click targets.
//
// The bar used to be two printf'd strings — pure display. Every fact it
// showed had an action the user might want next (a filename suggests
// switching files, a dirty dot suggests saving, "Ln 12" suggests going
// somewhere else) and no way to get there without remembering a chord or
// digging through the ≡ menu. This file rebuilds the bar as a slice of
// statusSegments, each an optional button, so the facts become verbs:
//
//	filename → switch-tab picker      ● → save
//	language → ≡ Code section         Ln,Col → go to line
//	diag counts → (Problems panel, Phase 3)
//	Copilot → ≡ Copilot section       branch → branch switcher
//	≡ (far right) → the menu — a bottom-row mouse path to it, mirroring
//	the tab bar's top-left button, so the menu is reachable from
//	whichever edge of the screen the eye is on.
//
// Geometry follows the tab bar's stamped-rect pattern (lastTabRects)
// rather than the fixed-position btnRect-method idiom: segment widths
// change every frame (filenames, line numbers), so drawStatusBar stamps
// each clickable segment's rect as it lays the text out, and
// statusBarClick hit-tests the stamped slice. Draw and hit share one
// geometry source either way — the rects ARE the layout that was drawn.
//
// Segments are advisory, never load-bearing: every onClick here is a
// verb that already exists in the ≡ menu or the leader table, so a
// terminal that eats clicks loses nothing (the macOS-Terminal rule).

package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

// statusSegment is one span of status-bar text. onClick is nil for
// purely informational spans (separators, line counts); rect is stamped
// by drawStatusBar for the spans it actually drew, so hit-testing can
// never disagree with the layout on screen.
type statusSegment struct {
	text    string
	onClick func(*App)
	rect    btnRect
}

// statusLeftSegments builds the left-hand run of segments: what file
// you're in and where you are inside it. A live flash message owns the
// whole left side while it lasts — it's the editor speaking, and it
// outranks the ambient facts it temporarily covers.
func (a *App) statusLeftSegments() []statusSegment {
	if time.Now().Before(a.statusUntil) && a.statusMsg != "" {
		return []statusSegment{{text: " " + a.statusMsg}}
	}
	tab := a.activeTabPtr()
	if tab == nil {
		return []statusSegment{{text: " " + filepath.Base(a.rootDir)}}
	}
	if tab.IsImage() && tab.Image != nil {
		b := tab.Image.Bounds()
		segs := []statusSegment{
			{text: " " + filepath.Base(tab.Path), onClick: (*App).menuSwitchTab},
		}
		segs = append(segs, a.statusPathSegments(tab.Path)...)
		return append(segs, statusSegment{text: fmt.Sprintf(" · %s · %d×%d",
			strings.ToUpper(tab.ImageFmt), b.Dx(), b.Dy())})
	}

	segs := []statusSegment{
		// The filename leads (it used to live only in the tab strip,
		// which scrolls out from under a dozen open files); clicking it
		// opens the switch-tab picker — "which file?" and "another file,
		// please" are the same thought.
		{text: " " + tab.DisplayName(), onClick: (*App).menuSwitchTab},
	}
	if tab.Dirty {
		// The dirty dot sits against the name it describes, and IS the
		// save button — the state and the verb that clears it, one cell.
		segs = append(segs, statusSegment{text: " ●", onClick: (*App).menuSave})
	}
	// Where the file actually LIVES, and the one-click way to take that
	// answer with you. See statusPathSegments.
	segs = append(segs, a.statusPathSegments(tab.Path)...)
	segs = append(segs,
		// The language label doubles as the door to the ≡ Code section —
		// the LSP-backed verbs — since "what does the editor know about
		// this language?" is the question the label answers one word of.
		statusSegment{text: " · " + detectLangLabel(tab.Path),
			onClick: func(app *App) { app.openMenuAtSection("Code") }},
		statusSegment{text: " · "},
		statusSegment{text: fmt.Sprintf("Ln %d, Col %d",
			tab.Cursor.Line+1, tab.Cursor.Col+1), onClick: (*App).menuGoToLine},
		statusSegment{text: fmt.Sprintf(" · %d lines", tab.Buffer.LineCount())},
	)
	if s := a.caretStatusSuffix(); s != "" {
		segs = append(segs, statusSegment{text: s})
	}
	if s := a.diagStatusSuffix(); s != "" {
		// The counts are the door to the list behind them (problems.go).
		// This segment reports the ACTIVE FILE's diagnostics while the
		// panel opens on the whole project — which is why the panel's
		// scope chip exists, and why opening it lands the highlight on
		// this file's first problem.
		segs = append(segs, statusSegment{text: s,
			onClick: (*App).menuToggleProblems})
	}
	if s := a.syntaxStatusSuffix(); s != "" {
		segs = append(segs, statusSegment{text: s})
	}
	return segs
}

// statusCopyGlyph is the ⧉ button: one click puts the active file's FULL
// path on the system clipboard. Single-width by the marker rule (runeLen
// counts runes, and a double-width glyph would slide every segment right
// of it out from under its own stamped rect), and deliberately the same
// glyph the chat transcript and the git log already use for copy — the
// editor should only have one symbol for "take this with you".
const statusCopyGlyph = "⧉"

// statusPathMaxShare caps the directory readout at a fraction of the
// window. The left run is clipped against the right one, and it is laid
// out in order — so an unbudgeted path on a deep tree would push Ln/Col
// and the line count off the bar entirely on a narrow terminal, trading a
// fact you consult constantly for one you consult occasionally.
const statusPathMaxShare = 3

// statusPathSegments renders where the active file lives, plus the ⧉
// button that copies its full path.
//
// It exists because the tab strip answers "which file?" with a NAME, and
// a name is not an answer when three of them are open — tablabel.go
// widens colliding tabs just far enough to tell them apart, and this is
// the other half: the whole location, for whichever file is in front of
// you, without opening a menu or a picker.
//
// Three decisions:
//
//   - **The directory only, not the file's whole path.** The name is
//     already the segment to the left of it; repeating it would spend the
//     bar's scarcest columns saying the same thing twice.
//   - **Project-relative when the file is inside the root**, absolute
//     otherwise. Relative is the shortest TRUE answer and the form the
//     user would paste into a shell already sitting in the project; a
//     file outside the root has no short true form, and "../../.." chains
//     are noise (the tabPickerDir rule).
//   - **Truncated from the FRONT**, like the find-all panel's path
//     labels: the distinguishing part of a path is its tail. A file AT
//     the root renders nothing at all — its name is its path, and a lone
//     "." is a cell spent on nothing.
//
// The ⧉ button is drawn whatever the directory came to, because a file at
// the project root still has a full path worth copying — and it is the
// mouse twin of the ≡ File → "Copy absolute path" row, so a terminal that
// eats clicks loses nothing (the macOS-Terminal rule).
func (a *App) statusPathSegments(path string) []statusSegment {
	if path == "" {
		return nil
	}
	var segs []statusSegment
	_, _, sw, _ := a.statusRect()
	if dir := a.statusPathDir(path, sw/statusPathMaxShare); dir != "" {
		segs = append(segs, statusSegment{text: " " + dir})
	}
	// The button TRAILS the path it copies, so the two read as one thing
	// — a location and the way to take it with you — rather than as a
	// glyph wedged between the file name and its directory.
	return append(segs, statusSegment{text: " " + statusCopyGlyph,
		onClick: (*App).menuCopyAbsolutePath})
}

// statusPathDir is the directory half of the readout: project-relative,
// falling back to absolute for a file outside the root, front-truncated
// to budget, and empty for a file sitting at the root itself. Split out
// from statusPathSegments so the text rule can be tested without a
// screen.
func (a *App) statusPathDir(path string, budget int) string {
	if path == "" || budget <= 1 {
		return ""
	}
	dir := filepath.Dir(a.relativePathFor(path))
	if dir == "." || dir == string(filepath.Separator) {
		return ""
	}
	if strings.HasPrefix(dir, "..") {
		// Outside the project: the relative form is a chain of parents
		// that reads as nothing at all, so say where it really is.
		dir = filepath.Dir(absolutePathFor(path))
	}
	if runeLen(dir) > budget {
		r := []rune(dir)
		dir = "…" + string(r[len(r)-(budget-1):])
	}
	return dir
}

// statusRightSegments builds the right-hand run: ambient session state
// (Copilot, git branch) and the ≡ button pinned to the far corner. The
// order matters to the overflow rule in drawStatusBar — segments are
// dropped from the FRONT of this slice on a narrow window, so the list
// runs least-vital first and the menu button, last, survives longest.
func (a *App) statusRightSegments() []statusSegment {
	var segs []statusSegment
	// The sibling agent leads, and so is the first thing dropped on a
	// narrow window: it is the only fact up here about a program that
	// isn't this editor. Clicking it focuses that pane, so the editor
	// stops being a place you have to LEAVE to find out what the agent two
	// panes over is doing (catsagents.go). Empty outside cats, which is
	// the whole Tier-0 story for this segment.
	if s := a.catsAgentStatusSegment(); s != "" {
		segs = append(segs, statusSegment{text: " " + s, onClick: (*App).catsFocusAgent})
	}
	if s := a.copilotStatusSegment(); s != "" {
		if len(segs) > 0 {
			segs = append(segs, statusSegment{text: " ·"})
		}
		segs = append(segs, statusSegment{text: " " + s,
			onClick: func(app *App) { app.openMenuAtSection("Copilot") }})
	}
	if a.gitBranch != "" {
		if len(segs) > 0 {
			segs = append(segs, statusSegment{text: " ·"})
		}
		segs = append(segs, statusSegment{text: " " + a.gitBranch,
			onClick: (*App).menuGitSwitchBranch})
		if s := a.gitPushStatusSuffix(); s != "" {
			// The unpushed-commit count IS the push button — the same
			// pairing as ● and Save on the left side: the fact, and the
			// verb that clears it, in one target. It gets its own segment
			// rather than annexing the branch's click so neither verb has
			// to be stolen from the other, and so the arrow can vanish
			// (level with the upstream, or no remote) without taking the
			// branch name with it.
			segs = append(segs, statusSegment{text: s, onClick: (*App).menuGitPush})
		}
	}
	// Two-cell gap so the branch name never reads as part of the button.
	segs = append(segs,
		statusSegment{text: "  "},
		statusSegment{text: "≡ ", onClick: (*App).openMenu},
	)
	return segs
}

// drawStatusBar paints the bottom status bar and re-stamps statusSegs,
// the clickable spans statusBarClick hit-tests. Right side lays out
// first so the left side can be clipped against it and the two runs
// never overlap on a narrow window (the rule the old two-string version
// followed; segments inherit it).
func (a *App) drawStatusBar() {
	sx, sy, sw, _ := a.statusRect()
	style := tcell.StyleDefault.Background(a.theme.StatusBG).
		Foreground(a.theme.BG).Bold(true)
	for cx := sx; cx < sx+sw; cx++ {
		a.screen.SetContent(cx, sy, ' ', nil, style)
	}
	a.statusSegs = a.statusSegs[:0]

	// Right side. When the window can't fit the whole run, whole
	// segments drop from the front (least-vital first — see
	// statusRightSegments) rather than truncating mid-word: a clipped
	// branch name is noise, an absent one is just a narrow terminal.
	right := a.statusRightSegments()
	rightWidth := 0
	for _, s := range right {
		rightWidth += runeLen(s.text)
	}
	for len(right) > 0 && rightWidth >= sw {
		rightWidth -= runeLen(right[0].text)
		right = right[1:]
	}
	x := sx + sw - rightWidth
	for _, seg := range right {
		drawAt(a.screen, x, sy, seg.text, style)
		if seg.onClick != nil {
			seg.rect = btnRect{x: x, y: sy, w: runeLen(seg.text)}
			a.statusSegs = append(a.statusSegs, seg)
		}
		x += runeLen(seg.text)
	}

	// Left side, clipped against the right block plus one breathing
	// cell. A segment cut by the clip keeps its on-screen width as its
	// hit width — you can click exactly what you can see of it.
	leftMax := sw - rightWidth
	if rightWidth > 0 {
		leftMax--
	}
	x = sx
	for _, seg := range a.statusLeftSegments() {
		avail := leftMax - (x - sx)
		if avail <= 0 {
			break
		}
		w := runeLen(seg.text)
		if w > avail {
			w = avail
		}
		drawStatusText(a.screen, x, sy, avail, seg.text, style)
		if seg.onClick != nil {
			seg.rect = btnRect{x: x, y: sy, w: w}
			a.statusSegs = append(a.statusSegs, seg)
		}
		x += w
	}
}

// statusBarClick routes a left-click on the status-bar row to the
// segment under it, if that segment carries an action. Reads the rects
// stamped by the most recent drawStatusBar — the geometry that is
// actually on screen.
func (a *App) statusBarClick(x, y int) {
	for _, seg := range a.statusSegs {
		if seg.onClick != nil && seg.rect.contains(x, y) {
			seg.onClick(a)
			return
		}
	}
}

// openMenuAtSection opens the ≡ menu with the named collapsible section
// unfolded and its first usable row hovered. This is what makes a
// status segment like "Copilot ✓" a real door rather than a shortcut to
// scrolling: the startup default folds every section, so opening the
// menu cold would land the user two clicks away from the thing the
// segment named.
func (a *App) openMenuAtSection(title string) {
	a.openMenu()
	if a.sectionCollapsed(title) {
		a.toggleMenuSection(title)
	}
	items, _, _ := a.menuLayout()
	for i, it := range items {
		if !it.header || it.label != title {
			continue
		}
		// Hover the first enabled row inside the section; fall back to
		// the header itself when everything in it is dimmed.
		a.hoveredMenuRow = i
		for j := i + 1; j < len(items) && !items[j].header; j++ {
			if items[j].enabled(a) {
				a.hoveredMenuRow = j
				break
			}
		}
		a.menuEnsureHoveredVisible()
		return
	}
}
