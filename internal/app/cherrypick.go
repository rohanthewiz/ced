// =============================================================================
// File: internal/app/cherrypick.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// cherrypick.go is the CHERRY-PICK DIALOG: pick a source branch, see the
// commits it has that the current branch does not, tick the ones you
// want, and apply them in one sequence.
//
//	┌ Cherry-pick onto main ─────────────────────────────────────── esc ┐
//	│ From  ‹ feature/parser ›                              alt+b change │
//	│ 5 commits not on main · 1 already applied · merges hidden          │
//	│ [x] 7c8f3a7   Handle empty input               rohan   2 days ago  │
//	│ [ ] bff5bbb ⚠ Rework the lexer                 rohan   2 days ago  │
//	│ [=] 2a0370b   Bump version (already on main)   rohan   3 days ago  │
//	│ touches lexer.go, parse.go · ⚠ parse.go also changed on main       │
//	│ [ ] -x  note the origin in each message   [ ] stage only, no commit │
//	│ [ Select all ]                       [ Cancel ]  [ Cherry-pick 1 ] │
//	└────────────────────────────────────────────────────────────────────┘
//
// The git log panel already cherry-picks ONE commit (gitlogactions.go);
// this is the other shape of the job, and the more common one: "bring
// these three fixes over from that branch". Five decisions:
//
//   - THE LIST IS `HEAD...source --right-only --cherry-mark`. That is
//     exactly "what does the source have that I do not", and --cherry-mark
//     flags commits whose PATCH is already on HEAD under another hash (a
//     previous cherry-pick, a rebase) with `=`. Those are shown, dimmed and
//     unpickable — hiding them would make the list disagree with `git log`
//     and leave the user wondering where a commit went; picking one would
//     only produce an empty commit.
//   - MERGES ARE HIDDEN. Cherry-picking a merge needs `-m <parent>`, a
//     question nobody can answer from a checkbox. The summary line says
//     they are hidden, so a missing merge is never a mystery.
//   - APPLIED OLDEST FIRST, WHATEVER THE TICK ORDER. The list reads newest
//     first like every log, but a later commit usually builds on an
//     earlier one, so the command always lists the ticked commits in
//     history order. One `git cherry-pick A B C` rather than three runs:
//     git's sequencer then owns the order, the progress ("2 of 3") and
//     the stop, and the Conflicts panel reads all of that back.
//   - PREDICTED CONFLICTS ARE MARKED. A `⚠` on a commit means it touches a
//     file the current branch has also changed since the two diverged —
//     not a promise of a conflict, but the cheapest honest hint there is,
//     and it costs one merge-base and one name-only diff for the whole
//     list.
//   - LOADED OFF THE MAIN LOOP. --cherry-mark computes a patch id for
//     every commit on BOTH sides of the range, which on a long-diverged
//     branch takes seconds. The dialog opens at once saying "Loading…" and
//     fills when the answer arrives; an answer for a source the dialog has
//     since moved away from is dropped by its sequence number.

package app

import (
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
)

const (
	// cherryPickMax caps the list. A branch hundreds of commits ahead is a
	// branch you merge, not one you pick from; the cap is named in the
	// summary line when it bites.
	cherryPickMax = 300

	// Dialog size bounds. Wide because the subject is the point of a row
	// and an author and a date want room beside it.
	cherryPickMaxW = 100
	cherryPickMinW = 60
	cherryPickMaxH = 28
	cherryPickMinH = 14

	// cherryPickFixedRows is everything that is not the list: the frame
	// and title (3), the source and summary rows (2), and below the list
	// the detail, options, warning and button rows plus the border (5).
	cherryPickFixedRows = 10
)

// cherryPickSeqGen numbers commit loads so a late answer for an earlier
// source is recognised and dropped.
var cherryPickSeqGen atomic.Int64

// cherryPickCommit is one row.
type cherryPickCommit struct {
	hash, short, author, age, subject string

	// applied: --cherry-mark said `=` — an equivalent patch is already on
	// HEAD, so picking it would produce an empty commit.
	applied bool

	// files are the paths the commit touches (toplevel-relative); overlap
	// are those the current branch ALSO changed since the merge base.
	files   []string
	overlap []string
}

// cherryPickModal is the dialog's state.
type cherryPickModal struct {
	source string // the branch / ref being picked from
	target string // the current branch's label, for the title and rows

	seq     int64
	loading bool
	err     string

	commits []cherryPickCommit // newest first
	capped  bool
	ticked  map[string]bool // by full hash

	cursor int
	scroll int

	recordOrigin bool // -x
	noCommit     bool // --no-commit

	dirty int // uncommitted files at open, for the warning row

	// lastBtn / lastClickRow / lastClickAt make presses edge-triggered and
	// let a second press on the same row toggle it (double-click).
	lastBtn      tcell.ButtonMask
	lastClickRow int
	lastClickAt  time.Time
}

// cherryPickLoadEvent carries a finished commit listing to the main loop.
type cherryPickLoadEvent struct {
	when    time.Time
	seq     int64
	commits []cherryPickCommit
	capped  bool
	err     string
}

// When satisfies tcell.Event.
func (e *cherryPickLoadEvent) When() time.Time { return e.when }

// -----------------------------------------------------------------------------
// Entry points
// -----------------------------------------------------------------------------

// menuCherryPickFromBranch is the ≡ Git row: choose a source branch, then
// the dialog. Refused while an operation is parked — a second cherry-pick
// cannot start until the first is finished, and the Conflicts panel is
// where that happens, so it is put up with the refusal.
func (a *App) menuCherryPickFromBranch() {
	a.closeMenu()
	if op := gitInProgressOp(a.rootDir); op != "" {
		a.flash("A " + op + " is in progress — finish or abort it first")
		a.showTool(toolConflicts)
		return
	}
	a.openCherryPickSourcePicker(nil)
}

// openCherryPickSourcePicker lists the branches to pick from, most
// recently committed first — the branch you just worked on is almost
// always the one you want, and recency puts it on the default row.
// onCancel, when set, runs if the picker is dismissed (the dialog's
// "change source" uses it to come back to the dialog it left).
func (a *App) openCherryPickSourcePicker(onCancel func(*App)) {
	refs := loadCherryPickSources(a.rootDir, a.gitBranch)
	if len(refs) == 0 {
		a.flash("No other branches to cherry-pick from")
		if onCancel != nil {
			onCancel(a)
		}
		return
	}
	items := make([]paletteItem, 0, len(refs))
	for _, r := range refs {
		r := r
		items = append(items, paletteItem{label: r, run: func(app *App) { app.openCherryPick(r) }})
	}
	a.openPickerWithCancel("Cherry-pick from which branch?", items, onCancel)
}

// loadCherryPickSources lists local and remote-tracking branches by
// commit recency, minus the current branch and the symbolic origin/HEAD
// entries (which only alias a branch already in the list).
func loadCherryPickSources(rootDir, current string) []string {
	out, err := exec.Command("git", "-C", rootDir, "for-each-ref", "--sort=-committerdate",
		"--format=%(refname:short)%09%(symref)", "refs/heads", "refs/remotes").Output()
	if err != nil {
		return nil
	}
	return parseCherryPickSources(out, current)
}

// parseCherryPickSources is loadCherryPickSources' parse, split out for
// tests: one "name<TAB>symref" per line, symrefs dropped.
func parseCherryPickSources(out []byte, current string) []string {
	var refs []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		name, symref, _ := strings.Cut(line, "\t")
		if name == "" || symref != "" || name == current {
			continue
		}
		refs = append(refs, name)
	}
	return refs
}

// openCherryPick opens the dialog for source and starts loading its
// commits.
func (a *App) openCherryPick(source string) {
	m := &cherryPickModal{
		source:       source,
		target:       a.gitLogBranchLabel(),
		ticked:       map[string]bool{},
		lastClickRow: -1,
	}
	if a.tree != nil {
		m.dirty = len(a.tree.DirtyFiles)
	}
	a.openModal(m)
	a.loadCherryPickCommits(m)
}

// loadCherryPickCommits starts the listing for m.source on a goroutine.
func (a *App) loadCherryPickCommits(m *cherryPickModal) {
	m.seq = cherryPickSeqGen.Add(1)
	m.loading, m.err = true, ""
	m.commits, m.capped = nil, false
	m.cursor, m.scroll = 0, 0
	m.ticked = map[string]bool{}
	if a.screen == nil {
		return
	}
	scr, root, source, seq := a.screen, a.rootDir, m.source, m.seq
	go func() {
		commits, capped, err := listCherryPickCommits(root, source)
		e := &cherryPickLoadEvent{when: time.Now(), seq: seq, commits: commits, capped: capped}
		if err != nil {
			e.err = err.Error()
		}
		_ = scr.PostEvent(e)
	}()
}

// handleCherryPickLoad lands a listing in the dialog that asked for it —
// and nowhere else: a closed dialog, or one that has since switched
// source, drops the answer.
func (a *App) handleCherryPickLoad(e *cherryPickLoadEvent) {
	m, ok := a.modal.(*cherryPickModal)
	if !ok || m.seq != e.seq {
		return
	}
	m.loading = false
	m.err = e.err
	m.commits, m.capped = e.commits, e.capped
	// The first pickable commit starts highlighted, so Enter on a fresh
	// dialog picks the newest commit not already applied — the commonest
	// single-pick, one key away.
	for i, c := range m.commits {
		if !c.applied {
			m.cursor = i
			break
		}
	}
}

// listCherryPickCommits runs the log and the overlap probe. Runs off the
// main loop.
func listCherryPickCommits(rootDir, source string) ([]cherryPickCommit, bool, error) {
	out, err := exec.Command("git", "-C", rootDir, "log", "--cherry-mark", "--right-only", "--no-merges",
		"--format=%x1e%m%x1f%H%x1f%h%x1f%an%x1f%ar%x1f%s", "--name-only",
		"-n", itoa(cherryPickMax+1), "HEAD..."+source, "--").Output()
	if err != nil {
		return nil, false, err
	}
	commits := parseCherryPickLog(out)
	capped := len(commits) > cherryPickMax
	if capped {
		commits = commits[:cherryPickMax]
	}
	markCherryPickOverlap(commits, loadOurSideChanges(rootDir, source))
	return commits, capped, nil
}

// parseCherryPickLog parses the log format above: records split on RS
// (0x1e), the first line of each holding US-separated fields, the rest
// the --name-only file list.
func parseCherryPickLog(out []byte) []cherryPickCommit {
	var commits []cherryPickCommit
	for _, rec := range strings.Split(string(out), "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		head, rest, _ := strings.Cut(rec, "\n")
		f := strings.Split(head, "\x1f")
		if len(f) < 6 {
			continue
		}
		c := cherryPickCommit{
			applied: f[0] == "=",
			hash:    f[1], short: f[2], author: f[3], age: f[4], subject: f[5],
		}
		for _, p := range strings.Split(rest, "\n") {
			if p = strings.TrimSpace(p); p != "" {
				c.files = append(c.files, p)
			}
		}
		commits = append(commits, c)
	}
	return commits
}

// loadOurSideChanges returns the set of files the CURRENT branch changed
// since it and source diverged. Best effort: no merge base (unrelated
// histories) or any failure yields an empty set, and no ⚠ is shown.
func loadOurSideChanges(rootDir, source string) map[string]bool {
	base, err := exec.Command("git", "-C", rootDir, "merge-base", "HEAD", source).Output()
	if err != nil {
		return nil
	}
	out, err := exec.Command("git", "-C", rootDir, "diff", "--name-only",
		strings.TrimSpace(string(base)), "HEAD", "--").Output()
	if err != nil {
		return nil
	}
	set := make(map[string]bool)
	for _, p := range strings.Split(string(out), "\n") {
		if p = strings.TrimSpace(p); p != "" {
			set[p] = true
		}
	}
	return set
}

// markCherryPickOverlap fills each commit's overlap list.
func markCherryPickOverlap(commits []cherryPickCommit, ours map[string]bool) {
	if len(ours) == 0 {
		return
	}
	for i := range commits {
		for _, f := range commits[i].files {
			if ours[f] {
				commits[i].overlap = append(commits[i].overlap, f)
			}
		}
	}
}

// -----------------------------------------------------------------------------
// State helpers
// -----------------------------------------------------------------------------

// pickable reports how many commits can be ticked.
func (m *cherryPickModal) pickable() int {
	n := 0
	for _, c := range m.commits {
		if !c.applied {
			n++
		}
	}
	return n
}

// tickedInOrder returns the ticked hashes OLDEST FIRST — the list is
// newest first, so this walks it backwards. See the file comment.
func (m *cherryPickModal) tickedInOrder() []string {
	var out []string
	for i := len(m.commits) - 1; i >= 0; i-- {
		if m.ticked[m.commits[i].hash] {
			out = append(out, m.commits[i].hash)
		}
	}
	return out
}

// toggle flips row i's tick; an already-applied commit refuses and says
// why rather than silently doing nothing.
func (m *cherryPickModal) toggle(a *App, i int) {
	if i < 0 || i >= len(m.commits) {
		return
	}
	c := m.commits[i]
	if c.applied {
		a.flash(c.short + " is already on " + m.target + " — nothing to pick")
		return
	}
	if m.ticked[c.hash] {
		delete(m.ticked, c.hash)
	} else {
		m.ticked[c.hash] = true
	}
}

// toggleAll ticks every pickable commit, or clears them all when they
// already are — one button, labelled by what it will do.
func (m *cherryPickModal) toggleAll() {
	if len(m.ticked) == m.pickable() && len(m.ticked) > 0 {
		m.ticked = map[string]bool{}
		return
	}
	for _, c := range m.commits {
		if !c.applied {
			m.ticked[c.hash] = true
		}
	}
}

// allLabel is the select-all button's label.
func (m *cherryPickModal) allLabel() string {
	if len(m.ticked) == m.pickable() && len(m.ticked) > 0 {
		return "[ Select none ]"
	}
	return "[ Select all ]"
}

// submitLabel names the button by what it will do — and how many.
func (m *cherryPickModal) submitLabel() string {
	n := len(m.ticked)
	if n == 0 {
		return "[ Cherry-pick ]"
	}
	return "[ Cherry-pick " + itoa(n) + " ]"
}

// -----------------------------------------------------------------------------
// Geometry
// -----------------------------------------------------------------------------

// rect sizes the dialog: width to the window within its bounds, height
// to the LIST — three commits get a three-row list, not a screenful of
// blank rows — between the floor and what the window allows. The floor
// holds while loading, so the one resize happens when the list lands
// rather than as rows trickle in.
func (m *cherryPickModal) rect(a *App) (x, y, w, h int) {
	w = min(max(a.width-4, cherryPickMinW), cherryPickMaxW)
	h = max(cherryPickFixedRows+len(m.commits), cherryPickMinH)
	h = min(h, cherryPickMaxH, max(a.height-2, cherryPickMinH))
	return a.centeredRect(min(w, a.width), min(h, a.height))
}

// listRows is how many commit rows fit.
func (m *cherryPickModal) listRows(a *App) int {
	_, _, _, h := m.rect(a)
	return max(h-cherryPickFixedRows, 1)
}

// rowY maps the dialog's named rows to screen lines. One map for draw and
// hit-test (the btnRect rule applied to rows).
func (m *cherryPickModal) rowY(a *App) (source, summary, listTop, detail, options, warn, buttons int) {
	_, my, _, mh := m.rect(a)
	source, summary, listTop = my+3, my+4, my+5
	buttons = my + mh - 2
	warn = buttons - 1
	options = warn - 1
	detail = options - 1
	return
}

// optionRects are the two option checkboxes on the options row.
func (m *cherryPickModal) optionRects(a *App) (origin, stage btnRect) {
	mx, _, _, _ := m.rect(a)
	_, _, _, _, oy, _, _ := m.rowY(a)
	ol := runeLen(m.originLabel())
	return btnRect{x: mx + 2, y: oy, w: ol},
		btnRect{x: mx + 2 + ol + 3, y: oy, w: runeLen(m.stageLabel())}
}

// originLabel / stageLabel are the option checkboxes' text.
func (m *cherryPickModal) originLabel() string {
	return checkbox(m.recordOrigin) + " -x  note the origin in each message"
}

// stageLabel describes --no-commit in the words of what it does.
func (m *cherryPickModal) stageLabel() string {
	return checkbox(m.noCommit) + " stage only, don't commit"
}

// checkbox renders a tick box.
func checkbox(on bool) string {
	if on {
		return "[x]"
	}
	return "[ ]"
}

// buttons returns the Select-all, Cancel and submit rects. Submit is
// anchored by its RIGHT edge so its growing count never slides it out
// from under the pointer (gitpush.go's rule).
func (m *cherryPickModal) buttons(a *App) (all, cancel, submit btnRect) {
	mx, _, mw, _ := m.rect(a)
	_, _, _, _, _, _, by := m.rowY(a)
	sl := runeLen(m.submitLabel())
	submit = btnRect{x: mx + mw - 3 - sl, y: by, w: sl}
	cancel = btnRect{x: submit.x - 2 - 10, y: by, w: 10}
	all = btnRect{x: mx + 2, y: by, w: runeLen(m.allLabel())}
	return
}

// sourceRect is the clickable "‹ branch ›" on the source row.
func (m *cherryPickModal) sourceRect(a *App) btnRect {
	mx, _, mw, _ := m.rect(a)
	sy, _, _, _, _, _, _ := m.rowY(a)
	return btnRect{x: mx + 8, y: sy, w: min(runeLen(m.source)+4, mw-10)}
}

// ensureCursorVisible scrolls just enough to show the cursor row.
func (m *cherryPickModal) ensureCursorVisible(a *App) {
	vis := m.listRows(a)
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+vis {
		m.scroll = m.cursor - vis + 1
	}
	m.clampScroll(a)
}

// clampScroll keeps the scroll offset in range.
func (m *cherryPickModal) clampScroll(a *App) {
	m.scroll = min(max(m.scroll, 0), max(len(m.commits)-m.listRows(a), 0))
}

// moveCursor steps the highlight, clamped.
func (m *cherryPickModal) moveCursor(a *App, delta int) {
	if len(m.commits) == 0 {
		return
	}
	m.cursor = min(max(m.cursor+delta, 0), len(m.commits)-1)
	m.ensureCursorVisible(a)
}

// -----------------------------------------------------------------------------
// Drawing
// -----------------------------------------------------------------------------

// draw paints the dialog.
func (m *cherryPickModal) draw(a *App) {
	mx, my, mw, mh := m.rect(a)
	c := a.chrome()
	c.drawFrame(a.screen, mx, my, mw, mh, "Cherry-pick onto "+m.target)
	a.screen.HideCursor()
	th := a.theme
	sy, sumY, listTop, dy, _, wy, _ := m.rowY(a)
	inner := mw - 4

	// Source row: the ref, as a select you can click.
	drawAt(a.screen, mx+2, sy, "From", c.muted)
	src := m.sourceRect(a)
	drawAt(a.screen, src.x, sy, gitPushClip("‹ "+m.source+" ›", src.w), c.title)
	hint := "alt+b change"
	if hx := mx + mw - 2 - runeLen(hint); hx > src.x+src.w+1 {
		drawAt(a.screen, hx, sy, hint, c.muted)
	}

	// Summary row.
	drawAt(a.screen, mx+2, sumY, gitPushClip(m.summaryText(), inner), c.muted)

	// The list.
	vis := m.listRows(a)
	for i := 0; i < vis; i++ {
		idx := m.scroll + i
		if idx >= len(m.commits) {
			break
		}
		m.drawRow(a, idx, listTop+i)
	}

	// Detail of the highlighted commit.
	if d := m.detailText(); d != "" {
		drawAt(a.screen, mx+2, dy, gitPushClip(d, inner), c.muted)
	}

	// Options.
	origin, stage := m.optionRects(a)
	drawAt(a.screen, origin.x, origin.y, m.originLabel(), c.body)
	drawAt(a.screen, stage.x, stage.y, m.stageLabel(), c.body)

	// The dirty-tree warning: git refuses a pick whose files have
	// uncommitted changes, and says so after the fact; this says it first.
	if m.dirty > 0 {
		warn := tcell.StyleDefault.Background(c.bg).Foreground(th.Modified)
		drawAt(a.screen, mx+2, wy, gitPushClip("⚠ "+plural(m.dirty, "uncommitted change", "uncommitted changes")+
			" — git refuses a commit that touches them", inner), warn)
	}

	all, cancel, submit := m.buttons(a)
	drawButton(a.screen, all.x, all.y, m.allLabel(), c.bg, th.Text, false)
	drawButton(a.screen, cancel.x, cancel.y, "[ Cancel ]", c.bg, th.Text, false)
	fg := th.Accent
	if len(m.ticked) == 0 {
		fg = th.Subtle
	}
	drawButton(a.screen, submit.x, submit.y, m.submitLabel(), c.bg, fg, len(m.ticked) > 0)
}

// summaryText is the line under the source: how many, how many already
// applied, and the two things the list deliberately leaves out.
func (m *cherryPickModal) summaryText() string {
	switch {
	case m.loading:
		return "Loading commits…"
	case m.err != "":
		return "Can't list " + m.source + ": " + firstLine(m.err)
	case len(m.commits) == 0:
		return "Nothing on " + m.source + " that " + m.target + " doesn't already have"
	}
	s := plural(len(m.commits), "commit", "commits") + " not on " + m.target
	if m.capped {
		s = "newest " + itoa(cherryPickMax) + " commits not on " + m.target + " (list capped)"
	}
	if n := len(m.commits) - m.pickable(); n > 0 {
		s += " · " + itoa(n) + " already applied"
	}
	return s + " · merges hidden · applied oldest first"
}

// detailText describes the highlighted commit's footprint: which files it
// touches, and which of them the current branch has also changed.
func (m *cherryPickModal) detailText() string {
	if m.cursor < 0 || m.cursor >= len(m.commits) {
		return ""
	}
	c := m.commits[m.cursor]
	if c.applied {
		return c.short + " is already on " + m.target + " (same patch, different hash)"
	}
	if len(c.files) == 0 {
		return c.short + " changes no files"
	}
	s := "touches " + listFew(c.files, 3)
	if len(c.overlap) > 0 {
		s += " · ⚠ " + listFew(c.overlap, 2) + " also changed on " + m.target + " — may conflict"
	}
	return s
}

// listFew joins up to n items and counts the rest — "a.go, b.go +3".
func listFew(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + " +" + itoa(len(items)-n)
}

// drawRow paints one commit row.
func (m *cherryPickModal) drawRow(a *App, idx, y int) {
	mx, _, mw, _ := m.rect(a)
	th := a.theme
	c := m.commits[idx]
	bg := a.chrome().bg
	if idx == m.cursor {
		bg = th.Selection
	}
	for x := mx + 1; x < mx+mw-1; x++ {
		a.screen.SetContent(x, y, ' ', nil, tcell.StyleDefault.Background(bg))
	}
	st := func(fg tcell.Color) tcell.Style { return tcell.StyleDefault.Background(bg).Foreground(fg) }

	box := checkbox(m.ticked[c.hash])
	subjFG := th.Text
	if c.applied {
		box, subjFG = "[=]", th.Muted
	}
	x := mx + 2
	drawAt(a.screen, x, y, box, st(th.Accent))
	x += 4
	drawAt(a.screen, x, y, c.short, st(th.AccentSoft))
	x += runeLen(c.short) + 1
	if len(c.overlap) > 0 && !c.applied {
		a.screen.SetContent(x, y, '⚠', nil, st(th.Modified))
	}
	x += 2

	// Author and age right-aligned, when the subject keeps a readable
	// share of the row (gitlog's rule: the subject wins).
	end := mx + mw - 2
	tail := c.author + "  " + c.age
	if end-runeLen(tail)-x > 24 {
		drawAt(a.screen, end-runeLen(tail), y, tail, st(th.Muted))
		end -= runeLen(tail) + 2
	}
	subj := c.subject
	if c.applied {
		subj += "  (already on " + m.target + ")"
	}
	drawStatusText(a.screen, x, y, end-x, subj, st(subjFG))
}

// -----------------------------------------------------------------------------
// Keyboard
// -----------------------------------------------------------------------------

// handleKey: arrows and paging move, Space ticks, Enter picks, Esc
// cancels, and the Alt chords are the keyboard twins of every button —
// the modal owns the keyboard, so the ≡ menu cannot be their door.
func (m *cherryPickModal) handleKey(a *App, ev *tcell.EventKey) {
	if ev.Modifiers()&tcell.ModAlt != 0 && ev.Key() == tcell.KeyRune {
		switch ev.Rune() {
		case 'a':
			m.toggleAll()
		case 'x':
			m.recordOrigin = !m.recordOrigin
		case 's':
			m.noCommit = !m.noCommit
		case 'b':
			m.changeSource(a)
		}
		return
	}
	switch ev.Key() {
	case tcell.KeyEsc:
		a.closeModal()
	case tcell.KeyUp:
		m.moveCursor(a, -1)
	case tcell.KeyDown:
		m.moveCursor(a, 1)
	case tcell.KeyPgUp:
		m.moveCursor(a, -m.listRows(a))
	case tcell.KeyPgDn:
		m.moveCursor(a, m.listRows(a))
	case tcell.KeyHome:
		m.moveCursor(a, -len(m.commits))
	case tcell.KeyEnd:
		m.moveCursor(a, len(m.commits))
	case tcell.KeyEnter:
		m.submit(a)
	case tcell.KeyRune:
		if ev.Rune() == ' ' {
			m.toggle(a, m.cursor)
		}
	}
}

// changeSource swaps the dialog for the branch picker; picking a branch
// opens a fresh dialog on it, and dismissing the picker brings this one
// back exactly as it was (ticks and options included).
func (m *cherryPickModal) changeSource(a *App) {
	a.openCherryPickSourcePicker(func(app *App) { app.openModal(m) })
}

// -----------------------------------------------------------------------------
// Mouse
// -----------------------------------------------------------------------------

// handleMouse: wheel scrolls; a PRESS (edge-triggered — tcell repeats
// Button1 while it is held and the pointer moves) on the box ticks, on the
// row highlights (a second press on the same row ticks it), and on the
// buttons, options and source does what they say. Outside dismisses.
func (m *cherryPickModal) handleMouse(a *App, x, y int, btn tcell.ButtonMask) {
	pressed := btn&tcell.Button1 != 0 && m.lastBtn&tcell.Button1 == 0
	m.lastBtn = btn
	switch {
	case btn&tcell.WheelUp != 0:
		m.scroll -= wheelLines
		m.clampScroll(a)
		return
	case btn&tcell.WheelDown != 0:
		m.scroll += wheelLines
		m.clampScroll(a)
		return
	case !pressed:
		return
	}
	mx, my, mw, mh := m.rect(a)
	if x < mx || x >= mx+mw || y < my || y >= my+mh {
		a.closeModal()
		return
	}
	all, cancel, submit := m.buttons(a)
	origin, stage := m.optionRects(a)
	switch {
	case all.contains(x, y):
		m.toggleAll()
		return
	case cancel.contains(x, y):
		a.closeModal()
		return
	case submit.contains(x, y):
		m.submit(a)
		return
	case origin.contains(x, y):
		m.recordOrigin = !m.recordOrigin
		return
	case stage.contains(x, y):
		m.noCommit = !m.noCommit
		return
	case m.sourceRect(a).contains(x, y):
		m.changeSource(a)
		return
	}
	_, _, listTop, _, _, _, _ := m.rowY(a)
	row := y - listTop
	if row < 0 || row >= m.listRows(a) {
		return
	}
	idx := m.scroll + row
	if idx >= len(m.commits) {
		return
	}
	now := time.Now()
	again := m.lastClickRow == idx && now.Sub(m.lastClickAt) < doubleClickMs
	m.cursor, m.lastClickRow, m.lastClickAt = idx, idx, now
	if x < mx+2+4 || again { // the [x] box, or a double-click anywhere on the row
		m.toggle(a, idx)
		m.lastClickRow = -1
	}
}

// -----------------------------------------------------------------------------
// Submit
// -----------------------------------------------------------------------------

// submit runs the pick. With nothing ticked, Enter picks the highlighted
// commit alone — the one-commit case should not need a tick first.
func (m *cherryPickModal) submit(a *App) {
	if m.loading {
		a.flash("Still loading commits…")
		return
	}
	hashes := m.tickedInOrder()
	if len(hashes) == 0 {
		if m.cursor < 0 || m.cursor >= len(m.commits) || m.commits[m.cursor].applied {
			a.flash("Tick the commits to cherry-pick (Space)")
			return
		}
		hashes = []string{m.commits[m.cursor].hash}
	}
	args := cherryPickArgs(hashes, m.recordOrigin, m.noCommit)
	n := len(hashes)
	target, staged := m.target, m.noCommit
	a.closeModal()
	label := "Cherry-pick " + plural(n, "commit", "commits")
	a.runGitCmdFull(label, gitNoEditorEnv(), gitConflictFailHook, func(app *App) {
		if staged {
			app.flash("Applied " + plural(n, "commit", "commits") + " to the index — review, then commit")
			return
		}
		app.flash("Cherry-picked " + plural(n, "commit", "commits") + " onto " + target)
	}, nil, args...)
}

// cherryPickArgs is the argv, split out for the pushArgs rule: it is the
// thing with consequences, so it is testable without forking git. -x is
// dropped with --no-commit — git only writes the "(cherry picked from …)"
// line into a commit it makes, so passing both would promise a note that
// never appears.
func cherryPickArgs(hashes []string, recordOrigin, noCommit bool) []string {
	args := []string{"cherry-pick"}
	if noCommit {
		args = append(args, "--no-commit")
	} else if recordOrigin {
		args = append(args, "-x")
	}
	return append(args, hashes...)
}
