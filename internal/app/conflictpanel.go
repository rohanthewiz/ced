// =============================================================================
// File: internal/app/conflictpanel.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// conflictpanel.go is the CONFLICTS tool window — the control room for a
// repository parked mid-cherry-pick, merge, revert or rebase. Where the
// in-editor view (conflictview.go) settles one block at a time, this
// panel answers the questions around it: what stopped, how far through
// the sequence it is, which files are still in the way and in what way,
// and the one verb that gets everything moving again.
//
//	─ Conflicts · 2 left ───────────────────────────────────────────── ✕ ─
//	⚠ Cherry-pick 2 of 4 · 2a0370b “Fix parser”      [ Continue ] [ Skip ] [ Abort ] ⟳
//	  current = main (HEAD) · incoming = 2a0370b
//	● internal/parse.go    3 conflicts · both modified   [ All current ] [ All incoming ]
//	● docs/old.md          deleted in incoming                  [ Keep ] [ Delete ]
//	✓ README.md            no markers left · both modified       [ Mark resolved ]
//	✓ go.mod               marked resolved
//
// Why a tool window and not the picker it grew out of (gitconflict.go):
// the picker is a QUESTION — it takes the modal slot, owns the keyboard,
// and is gone the moment you answer it. Resolving a conflict is not one
// answer; it is ten minutes of reading code with the state of the whole
// operation in view. That is furniture, the Problems panel's shape: it
// never takes the keyboard, rows are worklist items you burn down, and
// every button has a keyboard twin (the ≡ Git rows and the picker, which
// stays as the keyboard door to the same verbs).
//
// Decisions worth knowing:
//
//   - THE STOP OPENS IT. A cherry-pick / revert / continue that stops on
//     conflicts used to open the picker; it now opens this panel AND the
//     first conflicted file at its first block, lens showing. No modal to
//     dismiss before you can look at the problem. (cats still hears
//     "blocked": see conflictPanel.unseen.)
//   - STATE IS RE-DERIVED, the gitconflict.go rule. The operation, the
//     stopped commit and the unmerged list are read from the repository on
//     every refresh. The ONE remembered thing is cosmetic: the files that
//     were unmerged at this stop and have since been settled, so the list
//     reads as progress ("✓ marked resolved") instead of rows silently
//     vanishing. It is keyed by (operation, stopped commit) and dropped
//     the moment either changes.
//   - COUNTS READ THE BUFFER. A file open in a tab is counted from its
//     buffer, not from disk (the house rule), so clicking a lens updates
//     the row in the same frame — and "Mark resolved" SAVES a dirty tab
//     before staging, because `git add` reads the disk.
//   - PRESENCE CONFLICTS GET THEIR OWN VERBS. A modify/delete conflict has
//     no markers, so a marker scan calls it "ready" — and staging it
//     silently decides whether the file exists. Those rows offer Keep and
//     Delete instead, and never count toward "resolve all & continue".
//   - CONTINUE EXPLAINS ITSELF. It is always drawn; while files are still
//     unmerged it is dimmed and a click flashes what is holding it up (the
//     unavailable-row rule). When every remaining file is marker-free it
//     becomes "Resolve all & continue" — staging and continuing in one
//     gesture, as one sequence so the continue sees the staging.

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/clipboard"
	"github.com/rohanthewiz/ced/internal/editor"
)

const (
	// conflictPanelMinHeight is the header rule, the two operation rows
	// and room for two files — less is a panel that cannot show the list
	// it exists for.
	conflictPanelMinHeight = 5
	conflictPanelMaxHeight = 14

	// conflictPanelHeadRows are the body's fixed rows above the file list:
	// the operation line and the sides line.
	conflictPanelHeadRows = 2

	// conflictPanelMinText is what the operation line and a row's text
	// keep before buttons start dropping — a button run that ate the
	// whole row would leave a panel of verbs with no subject.
	conflictPanelMinText = 16
)

// conflictPanelState is the panel's whole state, mutated on the main loop.
type conflictPanelState struct {
	open bool

	info     gitOpInfo
	files    []conflictFile // unmerged right now, path order
	toplevel string

	// counts maps an unmerged file's absolute path to its conflict-block
	// count as scanned from DISK at the last refresh; -1 is "could not
	// scan" (too large / unreadable). Only consulted for files with no open
	// tab — see conflictFileCount, which reads an open buffer live.
	counts map[string]int

	// resolved / stopKey are the cosmetic memory described in the file
	// comment: toplevel-relative paths settled since this stop.
	resolved []string
	stopKey  string

	// empty marks a parked cherry-pick / revert with nothing unmerged and
	// nothing staged — the commit turned out to be already applied, and
	// Continue would fail where Skip moves on.
	empty bool

	selected int
	scroll   int

	// unseen is the cats "blocked" phrase for a stop the user has not yet
	// reacted to. A stop used to report blocked because it opened a modal;
	// the panel is not one, so the mark is explicit, and the next key or
	// click clears it (handleEvent) — the user is now looking at it.
	unseen string
}

// conflictRow is one list row: an unmerged file, or one already settled.
type conflictRow struct {
	file conflictFile
	done bool
}

// conflictRows is the list as drawn: the files still unmerged, then the
// ones settled since this stop.
func (a *App) conflictRows() []conflictRow {
	st := &a.conflictPanel
	rows := make([]conflictRow, 0, len(st.files)+len(st.resolved))
	for _, f := range st.files {
		rows = append(rows, conflictRow{file: f})
	}
	for _, rel := range st.resolved {
		rows = append(rows, conflictRow{
			file: conflictFile{rel: rel, abs: filepath.Join(st.toplevel, rel)},
			done: true,
		})
	}
	return rows
}

// -----------------------------------------------------------------------------
// Open / close / refresh
// -----------------------------------------------------------------------------

// openConflictPanelTool is the tool window's show verb.
func (a *App) openConflictPanelTool() {
	a.conflictPanel.open = true
	a.refreshConflictPanel()
}

// closeConflictPanelTool collapses the panel. State stays; it is re-read
// on the next open anyway.
func (a *App) closeConflictPanelTool() {
	a.conflictPanel.open = false
}

// menuToggleConflictPanel is the ≡ Git row and the status bar ⚠ click.
func (a *App) menuToggleConflictPanel() {
	a.closeMenu()
	a.toggleTool(toolConflicts)
}

// conflictPanelToggleLabel names the row by what clicking it does.
func (a *App) conflictPanelToggleLabel() string {
	if a.conflictPanel.open {
		return "Hide conflicts panel"
	}
	return "Show conflicts panel"
}

// refreshConflictPanel re-reads the operation and the unmerged list.
// Called on open, from refreshGitStatus while the panel is up (the 10s
// tick and every finished git command ride that), and after the panel's
// own verbs.
func (a *App) refreshConflictPanel() {
	st := &a.conflictPanel
	gitDir := a.gitDir
	if gitDir == "" {
		gitDir = gitAbsoluteDir(a.rootDir)
	}
	op := gitOpFromGitDir(gitDir)
	top, files := loadConflictFiles(a.rootDir)
	info := loadGitOpInfo(a.rootDir, gitDir, op)

	// The settled-since-this-stop memory: kept while the stop is the same
	// one, and grown by every file that was unmerged a refresh ago and is
	// not any more.
	key := op + "\x00" + info.commit
	if key != st.stopKey {
		st.resolved, st.stopKey = nil, key
	} else {
		now := make(map[string]bool, len(files))
		for _, f := range files {
			now[f.rel] = true
		}
		for _, f := range st.files {
			if !now[f.rel] && !containsString(st.resolved, f.rel) {
				st.resolved = append(st.resolved, f.rel)
			}
		}
	}
	st.info, st.files, st.toplevel = info, files, top
	st.empty = len(files) == 0 && (op == "cherry-pick" || op == "revert") && !gitIndexHasChanges(a.rootDir)
	a.refreshConflictPanelCounts()
	a.conflictPanelClamp()
}

// refreshConflictPanelCounts rescans the unmerged files that have no open
// tab — no forks, so it is cheap enough to run after every resolve.
func (a *App) refreshConflictPanelCounts() {
	st := &a.conflictPanel
	if !st.open {
		return
	}
	st.counts = make(map[string]int, len(st.files))
	for _, f := range st.files {
		if isPresenceConflict(f.xy) || a.tabForPath(f.abs) != nil {
			continue
		}
		st.counts[f.abs] = a.conflictCountFor(f.abs)
	}
}

// conflictFileCount is a row's block count as of THIS frame: read live
// from the open buffer when the file has a tab, from the last disk scan
// otherwise.
//
// Live rather than cached for open files because a buffer changes behind
// the panel's back in more ways than its own verbs: a keystroke deleting
// a marker by hand, an Undo bringing a block back, and — the case that
// forced this — the reconcile pass reloading a tab after `--continue`
// wrote the NEXT commit's markers into it. A cached count there said "no
// markers left" beside a file full of them. Tab.Conflicts is memoized per
// revision, so the live read costs a map lookup per row per frame.
func (a *App) conflictFileCount(abs string) int {
	if t := a.tabForPath(abs); t != nil {
		return len(t.Conflicts())
	}
	n, ok := a.conflictPanel.counts[abs]
	if !ok {
		return a.conflictCountFor(abs) // a file opened and closed since the scan
	}
	return n
}

// gitAbsoluteDir asks git for the absolute git directory — the fallback
// when the status snapshot has not filled a.gitDir yet.
func gitAbsoluteDir(rootDir string) string {
	if rootDir == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", rootDir, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// containsString reports whether list holds s.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// -----------------------------------------------------------------------------
// Derived facts
// -----------------------------------------------------------------------------

// conflictOpTitle is the operation line's sentence: what is running, how
// far along, and on what — "Cherry-pick 2 of 4 · 2a0370b “Fix parser”".
func (a *App) conflictOpTitle() string {
	info := a.conflictPanel.info
	if info.op == "" {
		if len(a.conflictPanel.files) > 0 {
			return "Unmerged files · no operation in progress"
		}
		return "No merge conflicts"
	}
	s := strings.ToUpper(info.op[:1]) + info.op[1:]
	if info.total > 1 {
		s += " " + itoa(info.step) + " of " + itoa(info.total)
	}
	switch {
	case info.op == "merge" && info.mergeMsg != "":
		s += " · " + info.mergeMsg
	case info.short != "":
		s += " · " + info.short
		if info.subject != "" {
			s += " “" + elide(info.subject, gitLogSubjectMax) + "”"
		}
	}
	if a.conflictPanel.empty {
		s += " · nothing left to apply"
	}
	return s
}

// conflictSidesNote names the two sides in the operation's own terms —
// the line that makes "current" and "incoming" mean something. In a
// rebase the sides are the reverse of what most people expect (current is
// the upstream being replayed onto), which is exactly why this line
// exists rather than leaving it to the marker labels.
func (a *App) conflictSidesNote() string {
	info := a.conflictPanel.info
	branch := a.gitLogBranchLabel()
	inc := info.short
	if inc == "" {
		inc = "the incoming commit"
	}
	switch info.op {
	case "cherry-pick", "revert":
		return "current = " + branch + " (HEAD) · incoming = " + inc
	case "merge":
		return "current = " + branch + " (HEAD) · incoming = the branch being merged"
	case "rebase":
		onto := info.onto
		if onto == "" {
			onto = "the upstream"
		}
		return "current = " + onto + " (rebasing onto) · incoming = your commit " + inc
	}
	if len(a.conflictPanel.files) > 0 {
		return "resolve each file, then mark it resolved"
	}
	return ""
}

// conflictPending reports the unmerged files still needing a DECISION:
// content conflicts with markers (or unscannable), and presence conflicts,
// which need Keep or Delete whatever their contents.
func (a *App) conflictPending() (pending, ready []conflictFile) {
	for _, f := range a.conflictPanel.files {
		if isPresenceConflict(f.xy) || a.conflictFileCount(f.abs) != 0 {
			pending = append(pending, f)
		} else {
			ready = append(ready, f)
		}
	}
	return pending, ready
}

// conflictPanelHeaderTitle is the dock header's count — "2 left" while
// files are unmerged, the operation's name while it waits on Continue.
func (a *App) conflictPanelHeaderTitle() string {
	if n := len(a.conflictPanel.files); n > 0 {
		return " · " + itoa(n) + " left"
	}
	if a.conflictPanel.info.op != "" {
		return " · ready to continue"
	}
	return ""
}

// -----------------------------------------------------------------------------
// Geometry
// -----------------------------------------------------------------------------

// conflictPanelBody is the content rectangle under the generic header.
func (a *App) conflictPanelBody() (x, y, w, h int) { return a.toolBodyRect(toolConflicts) }

// conflictPanelContains reports whether (x, y) falls inside the open
// panel's body (the header is the generic one's and routed before this).
func (a *App) conflictPanelContains(x, y int) bool {
	if !a.conflictPanel.open {
		return false
	}
	px, py, pw, ph := a.conflictPanelBody()
	return x >= px && x < px+pw && y >= py && y < py+ph
}

// conflictListRows is how many file rows fit under the two head rows.
func (a *App) conflictListRows() int {
	_, _, _, ph := a.conflictPanelBody()
	return max(ph-conflictPanelHeadRows, 0)
}

// conflictPanelClamp keeps the selection and scroll in range.
func (a *App) conflictPanelClamp() {
	st := &a.conflictPanel
	n := len(a.conflictRows())
	if st.selected >= n {
		st.selected = n - 1
	}
	if st.selected < 0 {
		st.selected = 0
	}
	maxScroll := max(n-a.conflictListRows(), 0)
	st.scroll = min(max(st.scroll, 0), maxScroll)
}

// conflictPanelScroll wheels the list without moving the selection.
func (a *App) conflictPanelScroll(delta int) {
	a.conflictPanel.scroll += delta
	a.conflictPanelClamp()
}

// conflictRowAt maps a screen cell to the row index drawn there, or -1.
func (a *App) conflictRowAt(x, y int) int {
	px, py, pw, ph := a.conflictPanelBody()
	top := py + conflictPanelHeadRows
	if x < px || x >= px+pw || y < top || y >= py+ph {
		return -1
	}
	idx := a.conflictPanel.scroll + (y - top)
	if idx < 0 || idx >= len(a.conflictRows()) {
		return -1
	}
	return idx
}

// conflictBtn is one clickable button: where it is, what it says, whether
// it is live (and if not, why), and what it does. One builder per button
// run returns these, and both draw and the press router read that one
// slice — the btnRect rule.
type conflictBtn struct {
	rect    btnRect
	label   string
	fg      tcell.Color
	enabled bool
	why     string
	run     func(*App)
}

// layoutButtonsRight places a run of buttons right-aligned so it ends at
// `right` (exclusive) on row y, with one blank cell between buttons. When
// the run would leave less than conflictPanelMinText cells of the row
// (which starts at `left`), buttons are shed in `drop` order — the
// least-wanted named first — before placing. Right alignment means a
// button that changes its label grows leftward, so its neighbours never
// slide out from under a pointer on its way to them (gitpush.go's rule).
func layoutButtonsRight(btns []conflictBtn, drop []string, left, right, y int) []conflictBtn {
	width := func(bs []conflictBtn) int {
		n := 0
		for i, b := range bs {
			if i > 0 {
				n++
			}
			n += runeLen(b.label)
		}
		return n
	}
	for _, d := range drop {
		if width(btns) <= right-left-conflictPanelMinText {
			break
		}
		for i, b := range btns {
			if b.label == d {
				btns = append(btns[:i:i], btns[i+1:]...)
				break
			}
		}
	}
	if width(btns) > right-left-conflictPanelMinText {
		return nil
	}
	x := right - width(btns)
	for i := range btns {
		btns[i].rect = btnRect{x: x, y: y, w: runeLen(btns[i].label)}
		x += btns[i].rect.w + 1
	}
	return btns
}

// conflictOpButtons is the operation row's button run, right-aligned:
// Continue, Skip, Abort and ⟳. Skip exists for the operations git can
// skip in (a merge has nothing to skip to); with no operation at all only
// ⟳ remains — a stash-pop conflict has nothing to continue or abort.
func (a *App) conflictOpButtons() []conflictBtn {
	px, py, pw, _ := a.conflictPanelBody()
	th := a.theme
	op := a.conflictPanel.info.op
	var btns []conflictBtn
	if op != "" {
		btns = append(btns, a.conflictContinueBtn())
		if op != "merge" {
			btns = append(btns, conflictBtn{label: "[ Skip ]", fg: th.Modified, enabled: true,
				run: (*App).confirmConflictSkip})
		}
		btns = append(btns, conflictBtn{label: "[ Abort ]", fg: th.Error, enabled: true,
			run: func(app *App) { app.gitConflictAbort(op) }})
	}
	btns = append(btns, conflictBtn{label: "⟳", fg: th.Accent, enabled: true,
		run: func(app *App) { app.refreshConflictPanel(); app.flash("Conflicts refreshed") }})
	return layoutButtonsRight(btns, []string{"⟳", "[ Skip ]", "[ Abort ]"}, px+1, px+pw-1, py)
}

// conflictContinueBtn builds the Continue button for the current state —
// its label, whether it is live, and the reason when it is not.
func (a *App) conflictContinueBtn() conflictBtn {
	th := a.theme
	op := a.conflictPanel.info.op
	pending, ready := a.conflictPending()
	b := conflictBtn{label: "[ Continue ]", fg: th.GitAdded}
	switch {
	case a.conflictPanel.empty:
		b.why = "Nothing to commit — this commit is already applied. Skip moves on."
	case len(pending) > 0:
		b.why = plural(len(pending), "file still needs", "files still need") +
			" resolving — " + pending[0].rel
	case len(ready) > 0:
		b.label = "[ Resolve all & continue ]"
		b.enabled = true
		b.run = func(app *App) { app.conflictStageAllAndContinue(op) }
	default:
		b.enabled = true
		b.run = func(app *App) { app.gitConflictContinue(op) }
	}
	return b
}

// conflictRowButtons is one row's button run, by the row's state.
func (a *App) conflictRowButtons(idx int) []conflictBtn {
	rows := a.conflictRows()
	if idx < 0 || idx >= len(rows) || idx < a.conflictPanel.scroll || idx >= a.conflictPanel.scroll+a.conflictListRows() {
		return nil
	}
	r := rows[idx]
	if r.done {
		return nil
	}
	th := a.theme
	f := r.file
	var btns []conflictBtn
	var drop []string
	switch {
	case f.xy == "DD":
		btns = []conflictBtn{{label: "[ Delete ]", fg: th.Error, enabled: true,
			run: func(app *App) { app.conflictDeleteFile(f) }}}
	case isPresenceConflict(f.xy):
		btns = []conflictBtn{
			{label: "[ Keep ]", fg: th.GitAdded, enabled: true, run: func(app *App) { app.conflictKeepFile(f) }},
			{label: "[ Delete ]", fg: th.Error, enabled: true, run: func(app *App) { app.conflictDeleteFile(f) }},
		}
	default:
		switch n := a.conflictFileCount(f.abs); {
		case n > 0:
			btns = []conflictBtn{
				{label: "[ All current ]", fg: th.GitAdded, enabled: true,
					run: func(app *App) { app.conflictAcceptAllIn(f, editor.ChooseCurrent) }},
				{label: "[ All incoming ]", fg: th.AccentSoft, enabled: true,
					run: func(app *App) { app.conflictAcceptAllIn(f, editor.ChooseIncoming) }},
			}
			drop = []string{"[ All incoming ]", "[ All current ]"}
		case n == 0:
			btns = []conflictBtn{{label: "[ Mark resolved ]", fg: th.GitAdded, enabled: true,
				run: func(app *App) { app.conflictMarkResolved(f) }}}
		}
	}
	px, py, pw, _ := a.conflictPanelBody()
	y := py + conflictPanelHeadRows + idx - a.conflictPanel.scroll
	return layoutButtonsRight(btns, drop, px+1, px+pw-1, y)
}

// -----------------------------------------------------------------------------
// Mouse
// -----------------------------------------------------------------------------

// conflictPanelPress routes a left press in the body: buttons first, then
// a row, which is selected and opened at its first conflict.
func (a *App) conflictPanelPress(x, y int) {
	for _, b := range a.conflictOpButtons() {
		if b.rect.contains(x, y) {
			a.pressConflictBtn(b)
			return
		}
	}
	idx := a.conflictRowAt(x, y)
	if idx < 0 {
		return
	}
	a.conflictPanel.selected = idx
	for _, b := range a.conflictRowButtons(idx) {
		if b.rect.contains(x, y) {
			a.pressConflictBtn(b)
			return
		}
	}
	a.conflictOpenRow(idx)
}

// pressConflictBtn runs a live button or explains a dimmed one.
func (a *App) pressConflictBtn(b conflictBtn) {
	if !b.enabled {
		if b.why != "" {
			a.flash(b.why)
		}
		return
	}
	if b.run != nil {
		b.run(a)
	}
}

// tryConflictPanelContextClick opens a row's verbs at the pointer —
// every verb the row's buttons offer plus the rarer ones (whole-file
// takes, copy path) — reporting whether it consumed the event.
func (a *App) tryConflictPanelContextClick(x, y int) bool {
	if !a.conflictPanelContains(x, y) {
		return false
	}
	idx := a.conflictRowAt(x, y)
	if idx < 0 {
		return true // the head rows are the panel's, not the editor's
	}
	a.conflictPanel.selected = idx
	items := a.conflictRowContextItems(a.conflictRows()[idx])
	if len(items) == 0 {
		return true
	}
	w := a.editorContextMenuWidth(items)
	cx, cy := a.placeContextSized(x, y, len(items), w)
	a.openModal(&editorContextModal{x: cx, y: cy, w: w, items: items})
	return true
}

// conflictRowContextItems lists a row's verbs for the anchored menu.
func (a *App) conflictRowContextItems(r conflictRow) []editorContextItem {
	f := r.file
	open := editorContextItem{label: "Open " + filepath.Base(f.rel), enabled: alwaysTrue,
		action: func(app *App) { app.conflictOpenFile(f) }}
	copyPath := editorContextItem{label: "Copy path", enabled: alwaysTrue,
		action: func(app *App) { app.conflictCopyPath(f) }}
	if r.done {
		return []editorContextItem{open, copyPath}
	}
	items := []editorContextItem{open}
	switch {
	case f.xy == "DD":
		items = append(items, editorContextItem{label: "Delete (both sides deleted it)", enabled: alwaysTrue,
			action: func(app *App) { app.conflictDeleteFile(f) }})
	case isPresenceConflict(f.xy):
		items = append(items,
			editorContextItem{label: "Keep file (" + conflictKindLabel(f.xy) + ")", enabled: alwaysTrue,
				action: func(app *App) { app.conflictKeepFile(f) }},
			editorContextItem{label: "Delete file", enabled: alwaysTrue,
				action: func(app *App) { app.conflictDeleteFile(f) }})
	default:
		items = append(items,
			editorContextItem{label: "Accept all current", enabled: alwaysTrue,
				action: func(app *App) { app.conflictAcceptAllIn(f, editor.ChooseCurrent) }},
			editorContextItem{label: "Accept all incoming", enabled: alwaysTrue,
				action: func(app *App) { app.conflictAcceptAllIn(f, editor.ChooseIncoming) }},
			editorContextItem{label: "Mark resolved", enabled: alwaysTrue,
				action: func(app *App) { app.conflictMarkResolved(f) }},
			editorContextItem{label: "Use current version of the whole file…", enabled: alwaysTrue,
				action: func(app *App) { app.conflictTakeWholeFile(f, "--ours", "current") }},
			editorContextItem{label: "Use incoming version of the whole file…", enabled: alwaysTrue,
				action: func(app *App) { app.conflictTakeWholeFile(f, "--theirs", "incoming") }})
	}
	return append(items, copyPath)
}

// -----------------------------------------------------------------------------
// Row verbs
// -----------------------------------------------------------------------------

// conflictOpenRow opens row idx's file at its first conflict.
func (a *App) conflictOpenRow(idx int) {
	rows := a.conflictRows()
	if idx < 0 || idx >= len(rows) {
		return
	}
	a.conflictOpenFile(rows[idx].file)
}

// conflictOpenFile opens f and parks the caret on its first block. A
// presence conflict whose file is gone from the work tree has nothing to
// open, and says what to do instead.
func (a *App) conflictOpenFile(f conflictFile) {
	if !pathInside(f.abs, a.rootDir) {
		a.flash(f.rel + " is outside this project")
		return
	}
	if _, err := os.Stat(f.abs); err != nil {
		a.flash(f.rel + " is " + conflictKindLabel(f.xy) + " — Keep or Delete it")
		return
	}
	a.openFile(f.abs)
	t := a.activeTabPtr()
	if t == nil || t.Path != f.abs {
		return
	}
	if len(t.Conflicts()) > 0 {
		a.jumpToConflict(t, 0)
	}
}

// conflictAcceptAllIn settles every block in f with one choice, opening
// the file first when it is not open — the result lands in a buffer the
// user can read, Undo, and save, rather than being written behind their
// back.
func (a *App) conflictAcceptAllIn(f conflictFile, choice editor.ConflictChoice) {
	t := a.tabForPath(f.abs)
	if t == nil {
		a.conflictOpenFile(f)
		if t = a.tabForPath(f.abs); t == nil {
			return
		}
	}
	a.resolveAllConflictsIn(t, choice)
}

// conflictMarkResolved stages one marker-free file. A dirty tab is saved
// first — `git add` reads the disk, and staging the pre-edit file is how a
// half-resolved conflict gets committed — and the marker count is checked
// AFTER that save, from the buffer that was just written.
func (a *App) conflictMarkResolved(f conflictFile) {
	if t := a.tabForPath(f.abs); t != nil && t.Dirty && !a.saveForStaging(t) {
		return // saveForStaging flashed why
	}
	switch n := a.conflictCountFor(f.abs); {
	case n < 0:
		a.flash("Can't check " + f.rel + " for markers (too large or unreadable) — stage it from a terminal")
		return
	case n > 0:
		a.flash(f.rel + " still has " + plural(n, "conflict", "conflicts"))
		return
	}
	a.runGitCmd("Mark "+filepath.Base(f.rel)+" resolved", "add", "--", f.abs)
}

// saveForStaging writes a tab's buffer to disk EXACTLY as it stands, for
// a `git add` that follows at once: the save-guard and the bookkeeping a
// save owes (git status, the gutter diff, the language server), but not
// format-on-save or plugin save hooks.
//
// Those two are the point. Both run asynchronously and rewrite the file
// after the save returns, and here a `git add` — and often a
// `--continue` that writes the NEXT commit's markers into the same file —
// is already on its way. A formatter landing between the add and the
// continue leaves the work tree differing from what was staged (git then
// refuses, or commits the unformatted copy); one landing after the
// continue runs over a file of conflict markers; and either way the
// in-flight format holds off the reconcile, so the tab shows the old
// text beside a panel describing the new. What gets staged must be what
// the user resolved and is looking at — nothing else may touch the file.
func (a *App) saveForStaging(t *editor.Tab) bool {
	if !a.saveGuard(t, func(app *App) { app.saveForStaging(t) }) {
		return false
	}
	if err := t.Save(); err != nil {
		a.flash("Save failed: " + err.Error())
		return false
	}
	a.refreshGitStatus()
	a.requestFileDiff(t.Path)
	a.lspDidSave(t)
	return true
}

// conflictKeepFile settles a presence conflict by keeping the file as it
// stands in the work tree.
func (a *App) conflictKeepFile(f conflictFile) {
	if _, err := os.Stat(f.abs); err != nil {
		a.flash(f.rel + " is not in the work tree — nothing to keep")
		return
	}
	a.runGitCmd("Keep "+filepath.Base(f.rel), "add", "--", f.abs)
}

// conflictDeleteFile settles a presence conflict by deleting the file.
// Not confirmed: the operation's Abort still restores it, and the row's
// label already said which side deleted it.
func (a *App) conflictDeleteFile(f conflictFile) {
	a.runGitCmd("Delete "+filepath.Base(f.rel), "rm", "--quiet", "--", f.abs)
}

// conflictTakeWholeFile replaces f with one side's ENTIRE version and
// marks it resolved — JetBrains' "accept yours / theirs". Different from
// "accept all current", which keeps git's clean auto-merged hunks and
// only settles the conflicting ones; this discards the other side's
// changes to the file wholesale, so it confirms, and says so.
//
// --ours / --theirs name index stages 2 and 3, which are current and
// incoming in EVERY operation (rebase included), so the positional words
// stay true here.
func (a *App) conflictTakeWholeFile(f conflictFile, flag, side string) {
	lines := []string{
		"Replace " + filepath.Base(f.rel) + " with the " + side + " side's whole file?",
		"Every change from the other side to this file is dropped.",
	}
	if t := a.tabForPath(f.abs); t != nil && t.Dirty {
		lines = append(lines, "Your unsaved edits to it are discarded too.")
	}
	a.openConfirmLines("Use "+side+" version", lines, func(app *App) {
		app.runGitCmdSeq("Use "+side+" version of "+filepath.Base(f.rel), [][]string{
			{"checkout", flag, "--", f.abs},
			{"add", "--", f.abs},
		})
	})
}

// conflictCopyPath puts a row's absolute path on the clipboard.
func (a *App) conflictCopyPath(f conflictFile) {
	if err := clipboard.CopyToSystem(f.abs); err != nil {
		a.flash("Copy failed: " + err.Error())
		return
	}
	a.flash("Copied " + f.rel)
}

// -----------------------------------------------------------------------------
// Operation verbs
// -----------------------------------------------------------------------------

// conflictStageAllAndContinue is "Resolve all & continue": save the dirty
// tabs among the ready files, re-check them from the saved buffers, then
// stage and continue as one sequence (gitConflictStageAndContinue).
func (a *App) conflictStageAllAndContinue(op string) {
	_, ready := a.conflictPending()
	rels := make([]string, 0, len(ready))
	for _, f := range ready {
		if t := a.tabForPath(f.abs); t != nil && t.Dirty && !a.saveForStaging(t) {
			return
		}
		if a.conflictCountFor(f.abs) != 0 {
			a.refreshConflictPanelCounts()
			a.flash(f.rel + " still has conflicts")
			return
		}
		rels = append(rels, f.rel)
	}
	a.gitConflictStageAndContinue(op, a.conflictPanel.toplevel, rels)
}

// confirmConflictSkip drops the stopped commit from the operation, behind
// a confirm naming it: skip is the one verb that quietly loses a commit
// (and whatever resolutions were typed into it).
func (a *App) confirmConflictSkip() {
	info := a.conflictPanel.info
	what := info.short
	if what == "" {
		what = "the current commit"
	}
	lines := []string{"Skip " + what + "?"}
	if info.subject != "" {
		lines = append(lines, "“"+elide(info.subject, gitLogSubjectMax)+"”")
	}
	lines = append(lines, "Its changes are left out of the "+info.op+".")
	op := info.op
	a.openConfirmLines("Skip commit", lines, func(app *App) {
		app.gitConflictSkip(op)
	})
}

// -----------------------------------------------------------------------------
// Drawing
// -----------------------------------------------------------------------------

// drawConflictPanel paints the body: the operation line with its buttons,
// the sides line, and the file rows (or the empty state).
func (a *App) drawConflictPanel() {
	px, py, pw, ph := a.conflictPanelBody()
	if pw <= 0 || ph <= 0 {
		return
	}
	th := a.theme
	bg := tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Text)
	for y := py; y < py+ph; y++ {
		for x := px; x < px+pw; x++ {
			a.screen.SetContent(x, y, ' ', nil, bg)
		}
	}

	// Operation line.
	btns := a.conflictOpButtons()
	textEnd := px + pw - 1
	if len(btns) > 0 {
		textEnd = btns[0].rect.x - 1
	}
	glyph, glyphFG := "⚠ ", th.Modified
	if a.conflictPanel.info.op == "" && len(a.conflictPanel.files) == 0 {
		glyph, glyphFG = "✓ ", th.GitAdded
	}
	drawAt(a.screen, px+1, py, glyph, tcell.StyleDefault.Background(th.SidebarBG).Foreground(glyphFG).Bold(true))
	drawStatusText(a.screen, px+3, py, textEnd-px-3, a.conflictOpTitle(),
		tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Text).Bold(true))
	for _, b := range btns {
		a.drawConflictBtn(b)
	}

	// Sides line.
	if ph > 1 {
		drawStatusText(a.screen, px+3, py+1, pw-4, a.conflictSidesNote(),
			tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Muted))
	}

	// Rows.
	rows := a.conflictRows()
	vis := a.conflictListRows()
	if len(rows) == 0 && vis > 0 {
		drawStatusText(a.screen, px+3, py+conflictPanelHeadRows, pw-4, a.conflictEmptyText(),
			tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Muted))
		return
	}
	labelW := a.conflictLabelWidth(rows)
	for i := 0; i < vis; i++ {
		idx := a.conflictPanel.scroll + i
		if idx >= len(rows) {
			break
		}
		a.drawConflictRow(idx, rows[idx], py+conflictPanelHeadRows+i, labelW)
	}
}

// conflictEmptyText explains an empty list — which is good news in every
// case, but different good news.
func (a *App) conflictEmptyText() string {
	switch {
	case a.conflictPanel.empty:
		return "This commit's changes are already on " + a.gitLogBranchLabel() + " — Skip it to move on."
	case a.conflictPanel.info.op != "":
		return "All conflicts resolved — Continue to finish the " + a.conflictPanel.info.op + "."
	default:
		return "Nothing to resolve. Conflicts from a cherry-pick, merge, revert or rebase appear here."
	}
}

// conflictLabelWidth is the path column's width: the widest path, capped
// at two fifths of the panel and floored so a narrow dock still names
// files (problems.go's rule).
func (a *App) conflictLabelWidth(rows []conflictRow) int {
	_, _, pw, _ := a.conflictPanelBody()
	widest := 0
	for _, r := range rows {
		widest = max(widest, runeLen(r.file.rel))
	}
	return min(widest, max(pw*2/5, 14))
}

// drawConflictRow paints one file row: state glyph, path, what is wrong
// with it, and its buttons.
func (a *App) drawConflictRow(idx int, r conflictRow, y, labelW int) {
	px, _, pw, _ := a.conflictPanelBody()
	th := a.theme
	rowBG := th.SidebarBG
	if idx == a.conflictPanel.selected {
		rowBG = th.Selection
		for x := px; x < px+pw; x++ {
			a.screen.SetContent(x, y, ' ', nil, tcell.StyleDefault.Background(rowBG))
		}
	}
	st := func(fg tcell.Color) tcell.Style { return tcell.StyleDefault.Background(rowBG).Foreground(fg) }

	glyph, glyphFG, detail := a.conflictRowState(r)
	a.screen.SetContent(px+1, y, glyph, nil, st(glyphFG).Bold(true))
	pathFG := th.Text
	if r.done {
		pathFG = th.Muted
	}
	drawAt(a.screen, px+3, y, rowLabelText(r.file.rel, labelW), st(pathFG))

	btns := a.conflictRowButtons(idx)
	textEnd := px + pw - 1
	if len(btns) > 0 {
		textEnd = btns[0].rect.x - 1
	}
	if dx := px + 3 + labelW + 2; textEnd > dx {
		drawStatusText(a.screen, dx, y, textEnd-dx, detail, st(th.Muted))
	}
	for _, b := range btns {
		a.drawConflictBtnOn(b, rowBG)
	}
}

// conflictRowState is a row's glyph, its colour, and its detail text.
func (a *App) conflictRowState(r conflictRow) (rune, tcell.Color, string) {
	th := a.theme
	if r.done {
		return '✓', th.Muted, "marked resolved"
	}
	f := r.file
	kind := conflictKindLabel(f.xy)
	if isPresenceConflict(f.xy) {
		return '●', th.Error, kind
	}
	switch n := a.conflictFileCount(f.abs); {
	case n < 0:
		return '●', th.Error, "can't scan (too large) · " + kind
	case n == 0:
		return '✓', th.GitAdded, "no markers left · " + kind
	default:
		return '●', th.Error, plural(n, "conflict", "conflicts") + " · " + kind
	}
}

// drawConflictBtn paints a head-row button on the panel background.
func (a *App) drawConflictBtn(b conflictBtn) { a.drawConflictBtnOn(b, a.theme.SidebarBG) }

// drawConflictBtnOn paints a button on a given background: its own colour
// when live, the separator grey when dimmed — drawn either way, because a
// button that vanished would be a verb nobody could ask about.
func (a *App) drawConflictBtnOn(b conflictBtn, bg tcell.Color) {
	fg := b.fg
	if !b.enabled {
		fg = a.theme.Subtle
	}
	drawAt(a.screen, b.rect.x, b.rect.y, b.label,
		tcell.StyleDefault.Background(bg).Foreground(fg).Bold(b.enabled))
}

// -----------------------------------------------------------------------------
// The stop, and the status bar
// -----------------------------------------------------------------------------

// showConflictStop is what an operation that just stopped on conflicts
// does to the screen: the panel comes up with the stop in it, the first
// conflicted file opens at its first block (lens showing), and the flash
// says what happened in one line. Reports whether there was anything
// unmerged to show — a stop with no conflicts (an empty pick, a dirty
// tree refusing) is git's own message's job, and the caller falls through
// to it.
func (a *App) showConflictStop() bool {
	// showTool refreshes a panel it opens and leaves an open one alone, so
	// the explicit refresh is only owed in the second case — the stop has
	// changed what an already-open panel should say.
	if a.conflictPanel.open {
		a.refreshConflictPanel()
	} else {
		a.showTool(toolConflicts)
	}
	op := a.conflictPanel.info.op
	files := a.conflictPanel.files
	if op != "" {
		a.conflictPanel.unseen = op + " stopped on conflicts"
	}
	if len(files) == 0 {
		return false
	}
	// The first file that actually has a block to show — a presence
	// conflict first in path order has nothing to put a lens on.
	for _, f := range files {
		if !isPresenceConflict(f.xy) && a.conflictFileCount(f.abs) > 0 {
			a.conflictOpenFile(f)
			break
		}
	}
	what := "Conflicts"
	if op != "" {
		what = strings.ToUpper(op[:1]) + op[1:] + " stopped"
	}
	// Short on purpose: the status bar's right half (branch, the ⚠ segment)
	// eats a long flash, and the panel and the lens already say what to do.
	a.flash(what + " on " + plural(len(files), "conflicted file", "conflicted files"))
	return true
}

// conflictStatusSegment is the status bar's ⚠ — the standing answer to
// "is this repo parked?" that the 4.3 picker never left on screen. Empty
// on a clean repo, which is every repo almost all of the time.
func (a *App) conflictStatusSegment() string {
	n := len(a.gitConflicted)
	switch {
	case a.gitOp != "" && n > 0:
		return "⚠ " + a.gitOp + ": " + plural(n, "conflict", "conflicts")
	case a.gitOp != "":
		return "⚠ " + a.gitOp + " in progress"
	case n > 0:
		return "⚠ " + plural(n, "conflicted file", "conflicted files")
	}
	return ""
}

// refreshConflictPanelInfo re-reads the operation details even while the
// panel is shut — the keyboard's Skip row (gitconflict.go) confirms with
// the stopped commit's name, which lives in the panel's state.
func (a *App) refreshConflictPanelInfo() {
	gitDir := a.gitDir
	if gitDir == "" {
		gitDir = gitAbsoluteDir(a.rootDir)
	}
	a.conflictPanel.info = loadGitOpInfo(a.rootDir, gitDir, gitOpFromGitDir(gitDir))
}
