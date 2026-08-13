// =============================================================================
// File: internal/app/gitlogfilter.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitlogfilter.go turns the git log panel into a history SEARCH: a
// one-row filter bar under the panel header that re-runs `git log` with
// the user's query and replaces the commit list with the hits. Every
// row keeps its full behavior while filtered — click for the diff,
// right-click (or Actions ▾) for the commit verbs — so "search history,
// then cherry-pick the hit" is one uninterrupted flow, which is the
// whole point of putting the search HERE instead of in a picker.
//
// Four search modes, because "search history" means four different
// questions and git answers each with a different flag:
//
//	(default)  --grep -i   which commit MESSAGE mentions this
//	a:         --author -i who wrote it
//	p:         -- <path>   what happened to this file (with --follow)
//	s:         -S<term>    when did this STRING appear or disappear
//
// The pickaxe (`s:`) is the one nothing else in the editor can answer
// and the reason this file exists: "who deleted this line" is a
// question you otherwise leave the editor to ask.
//
// Three design decisions worth the ink:
//
//  1. **The text is the only state.** The mode is not a field — it is
//     parsed out of the query text every time it's needed, and the mode
//     chips REWRITE the prefix rather than setting a flag beside it. A
//     chip that says "author" while the text says `p:foo` is the bug
//     this design cannot have.
//
//  2. **Debounce on the main loop, search on a goroutine.** A 250ms
//     timer posts a tick (goroutines never touch App state — the iron
//     rule); the tick launches the query; the result posts back stamped
//     with the sequence number it was asked under, so a slow pickaxe
//     landing after a newer fast --grep is dropped rather than
//     overwriting it. The PREVIOUS list stays on screen the whole time:
//     a pickaxe over 400 commits takes seconds, and blanking the panel
//     for those seconds would make every keystroke feel like a crash.
//
//  3. **The periodic refresh yields to an applied filter.** The 10s git
//     tick re-reads the log for free when the list is a live view of
//     HEAD; when it is a SEARCH RESULT, re-forking a multi-second
//     pickaxe every ten seconds is not free, and results that shift
//     under the pointer are worse than results that are ten seconds
//     stale. ⟳ (and re-opening the panel) re-runs the query explicitly.
//
// The bar owns the keyboard only while its field is focused, and it
// gives the keyboard back on Esc or on a click outside the panel — the
// mouse-first focus model. That focus check is also what lets Up/Down
// walk the commit list from inside the field: the log panel has no
// keyboard of its own, so without it a filtered list could only be
// reached by mouse.

package app

import (
	"os/exec"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

// gitLogFilterDebounce is the pause after typing that fires the search.
// Longer than completion's 150ms on purpose: every keystroke here costs
// a `git log` fork over the whole history (and a pickaxe reads every
// blob), where completion's cost is one message to a warm server.
const gitLogFilterDebounce = 250 * time.Millisecond

// Search modes. The zero value is the message search, so a bare query
// with no prefix — the common case — needs no special casing anywhere.
const (
	gitLogModeMessage = iota
	gitLogModeAuthor
	gitLogModePath
	gitLogModePickaxe
)

// gitLogModeChip describes one mode: the prefix that selects it in the
// query text, and the chip label that selects it with the mouse. One
// table drives parsing, the prefix rewrite, the chip rects, and the
// chip drawing, so a fifth mode is one row here.
type gitLogModeChip struct {
	mode   int
	prefix string
	label  string
}

// gitLogModeChips is that table, in chip order (left to right). The
// message mode has an empty prefix and so is never MATCHED by the
// parser — it is what a query falls through to.
var gitLogModeChips = []gitLogModeChip{
	{gitLogModeMessage, "", " msg "},
	{gitLogModeAuthor, "a:", " author "},
	{gitLogModePath, "p:", " path "},
	{gitLogModePickaxe, "s:", " string "},
}

// gitLogQuery is a parsed query: which question is being asked, and the
// term to ask it with. An empty term means "no filter" regardless of
// mode — a lone `s:` is a user mid-typing, not a search for nothing.
type gitLogQuery struct {
	mode int
	term string
}

// active reports whether this query should replace the commit list.
func (q gitLogQuery) active() bool { return q.term != "" }

// gitLogFilter is the bar's state. Zero value = closed, which is what
// makes it safe to embed in gitLogState without an initializer.
type gitLogFilter struct {
	open    bool
	focused bool
	field   textField

	// timer is the single debounce; re-armed (never stacked) on every
	// edit, so a typing burst costs one search.
	timer *time.Timer

	// seq stamps every query. Both the debounce tick and the result
	// carry the seq they were born under, and anything that doesn't
	// match the current one is dropped — that is the whole staleness
	// story for a surface where a slow query can outlive two fast ones.
	seq int

	// running is true between launching a search and its result
	// landing; the only thing the "searching…" indicator reads.
	running bool

	// applied is the query text the DISPLAYED list belongs to, "" while
	// the list is the unfiltered history. It answers three questions:
	// whether the title says "matches" or "commits", whether the
	// periodic refresh should stand down, and whether closing the bar
	// owes the user a reload.
	applied string
}

// parseGitLogQuery splits query text into mode + term.
//
// Leading whitespace is trimmed (from the text, and again after a
// prefix, so "a: rohan" searches for "rohan"); TRAILING whitespace is
// deliberately kept — `s:"foo "` is a legitimate pickaxe search for a
// string with a trailing space, and silently trimming it would return
// the wrong commits with no way for the user to see why.
func parseGitLogQuery(text string) gitLogQuery {
	t := strings.TrimLeft(text, " \t")
	for _, c := range gitLogModeChips {
		if c.prefix != "" && strings.HasPrefix(t, c.prefix) {
			return gitLogQuery{
				mode: c.mode,
				term: strings.TrimLeft(t[len(c.prefix):], " \t"),
			}
		}
	}
	// A term of nothing but spaces is nothing.
	if strings.TrimSpace(t) == "" {
		return gitLogQuery{}
	}
	return gitLogQuery{mode: gitLogModeMessage, term: t}
}

// gitLogModePrefix returns the prefix that selects mode in query text.
func gitLogModePrefix(mode int) string {
	for _, c := range gitLogModeChips {
		if c.mode == mode {
			return c.prefix
		}
	}
	return ""
}

// gitLogSetQueryMode rewrites text to carry mode's prefix, keeping the
// term. This is how a chip click (and Tab) change modes: the text stays
// the single source of truth, so the chips can never disagree with it.
func gitLogSetQueryMode(text string, mode int) string {
	return gitLogModePrefix(mode) + parseGitLogQuery(text).term
}

// -----------------------------------------------------------------------------
// The git command
// -----------------------------------------------------------------------------

// gitLogFilterArgs builds the full `git log` argument list for q — the
// complete argv minus the leading `git -C <root>`. Split out from the
// runner (the pushArgs rule) so every mode's flags are pinned by a test
// that never forks git.
//
// Terms are always attached to their flag (`--grep=x`, `-Sx`) or fenced
// behind `--`, so a term that begins with a dash is a search term and
// not an option, whatever the user typed.
func gitLogFilterArgs(q gitLogQuery) []string {
	args := []string{"log", "--all", "--date=relative",
		"-n", itoa(gitLogMaxCommits + 1), "--format=" + gitLogFormat}
	if !q.active() {
		return args
	}
	switch q.mode {
	case gitLogModeAuthor:
		// -i makes --author case-insensitive too; nobody remembers
		// whether the commits they want say "Rohan" or "rohan".
		args = append(args, "-i", "--author="+q.term)
	case gitLogModePath:
		// --follow walks renames, which is the entire reason to search
		// by path rather than eyeballing the file's blame. git tolerates
		// a directory or a glob here (it just has nothing to follow), so
		// the term needs no shape-checking — a path that matches nothing
		// yields no commits, same as any other miss.
		args = append(args, "--follow", "--", q.term)
	case gitLogModePickaxe:
		// -S is a literal string search across every diff: it lists the
		// commits where the NUMBER of occurrences changed, i.e. where
		// the string appeared or disappeared. Deliberately not
		// --pickaxe-regex: "when did this line die" is asked with the
		// line pasted in, not with a pattern.
		args = append(args, "-S"+q.term)
	default:
		args = append(args, "-i", "--grep="+q.term)
	}
	return args
}

// loadGitLogQuery runs one prepared log command and decodes it, plus
// whether the cap truncated the result. Best-effort like every git read
// here: a failure is an empty list, not an error path.
func loadGitLogQuery(rootDir string, args []string) ([]gitLogCommit, bool) {
	if rootDir == "" {
		return nil, false
	}
	full := append([]string{"-C", rootDir}, args...)
	out, err := exec.Command("git", full...).Output()
	if err != nil {
		return nil, false
	}
	commits := parseGitLogCommits(out)
	if len(commits) > gitLogMaxCommits {
		return commits[:gitLogMaxCommits], true
	}
	return commits, false
}

// -----------------------------------------------------------------------------
// Events — debounce tick and result
// -----------------------------------------------------------------------------

// gitLogFilterTickEvent is the debounce firing: "the query has been
// still for 250ms, go ask git". It is an event rather than a direct
// call because the timer runs off the main loop.
type gitLogFilterTickEvent struct {
	when time.Time
	seq  int
}

// When satisfies the tcell.Event interface.
func (e *gitLogFilterTickEvent) When() time.Time { return e.when }

// gitLogFilterEvent carries one finished search back to the main loop,
// stamped with the sequence number it was asked under.
type gitLogFilterEvent struct {
	when      time.Time
	seq       int
	query     string
	commits   []gitLogCommit
	truncated bool
}

// When satisfies the tcell.Event interface.
func (e *gitLogFilterEvent) When() time.Time { return e.when }

// gitLogFilterArm (re)starts the debounce under a fresh sequence
// number. Bumping seq here is what invalidates every tick and every
// in-flight result from before this keystroke.
func (a *App) gitLogFilterArm() {
	a.gitLogFilterStopTimer()
	a.gitLog.filter.seq++
	seq := a.gitLog.filter.seq
	scr := a.screen
	if scr == nil {
		return
	}
	a.gitLog.filter.timer = time.AfterFunc(gitLogFilterDebounce, func() {
		// Goroutine territory: post, never mutate.
		_ = scr.PostEvent(&gitLogFilterTickEvent{when: time.Now(), seq: seq})
	})
}

// gitLogFilterStopTimer cancels a pending debounce, if any.
func (a *App) gitLogFilterStopTimer() {
	if a.gitLog.filter.timer != nil {
		a.gitLog.filter.timer.Stop()
		a.gitLog.filter.timer = nil
	}
}

// handleGitLogFilterTick is the debounce landing on the main loop. The
// guards cover everything that can have changed during the wait: the
// panel closed, the bar closed, or another keystroke re-armed under a
// newer sequence number.
func (a *App) handleGitLogFilterTick(e *gitLogFilterTickEvent) {
	a.gitLog.filter.timer = nil
	if !a.gitLog.open || !a.gitLog.filter.open || e.seq != a.gitLog.filter.seq {
		return
	}
	a.gitLogFilterRun()
}

// gitLogFilterRun launches the current query on a goroutine under the
// current sequence number. An empty query is answered by reloading the
// unfiltered history through the same path, so clearing the box and
// typing a new query behave identically.
func (a *App) gitLogFilterRun() {
	if a.screen == nil || a.rootDir == "" {
		return
	}
	text := a.gitLog.filter.field.String()
	args := gitLogFilterArgs(parseGitLogQuery(text))
	seq := a.gitLog.filter.seq
	a.gitLog.filter.running = true
	scr, root := a.screen, a.rootDir
	go func() {
		commits, truncated := loadGitLogQuery(root, args)
		_ = scr.PostEvent(&gitLogFilterEvent{
			when: time.Now(), seq: seq, query: text,
			commits: commits, truncated: truncated,
		})
	}()
}

// handleGitLogFilterResult installs a completed search. Stale results
// (the user typed on while a slow pickaxe ran) are dropped by sequence
// number, which is also why `running` is only cleared by the result the
// panel is actually waiting for.
func (a *App) handleGitLogFilterResult(e *gitLogFilterEvent) {
	if !a.gitLog.open || !a.gitLog.filter.open || e.seq != a.gitLog.filter.seq {
		return
	}
	a.gitLog.filter.running = false
	a.gitLog.filter.applied = ""
	if parseGitLogQuery(e.query).active() {
		a.gitLog.filter.applied = e.query
	}
	a.applyGitLogCommits(e.commits, e.truncated)
}

// -----------------------------------------------------------------------------
// Open / close / modes
// -----------------------------------------------------------------------------

// openGitLogFilter shows the bar and takes the keyboard. Called with a
// selection in the editor, it pre-seeds a PICKAXE search for the
// selected text — "when did this line appear" is the question a
// selection plus a history search almost always means, and typing the
// line back in by hand is exactly the friction this phase exists to
// remove.
//
// A multi-line selection is deliberately not seeded: the field is one
// line, so it could only show a mangled version of what it searched for.
func (a *App) openGitLogFilter() {
	if !a.gitLog.open {
		return
	}
	a.gitLog.filter.open = true
	a.gitLog.filter.focused = true
	if seed := a.gitLogFilterSeed(); seed != "" {
		a.gitLog.filter.field = newTextField("s:" + seed)
		// A deliberate invocation carrying a term skips the debounce —
		// the user already waited by deciding to press the key.
		a.gitLog.filter.seq++
		a.gitLogFilterRun()
	}
	a.gitLogClampScrolls()
}

// gitLogFilterSeed returns the active tab's selection when it is a
// single line of non-blank text short enough to read in the field, ""
// otherwise.
func (a *App) gitLogFilterSeed() string {
	t := a.activeTabPtr()
	if t == nil || !t.HasSelection() {
		return ""
	}
	sel := t.SelectionText()
	if strings.ContainsAny(sel, "\r\n") {
		return ""
	}
	sel = strings.TrimSpace(sel)
	if sel == "" || runeLen(sel) > 120 {
		return ""
	}
	return sel
}

// closeGitLogFilter hides the bar, drops the query, and restores the
// unfiltered history — the same contract as closing the find bar, where
// Esc means "I'm done searching" and the highlights go with it. A list
// left filtered under a bar that is no longer on screen is a panel
// lying about what it shows.
func (a *App) closeGitLogFilter() {
	a.gitLogFilterStopTimer()
	wasFiltered := a.gitLog.filter.applied != ""
	// Bump the sequence so an in-flight search can't repopulate the
	// list after the bar is gone.
	a.gitLog.filter = gitLogFilter{seq: a.gitLog.filter.seq + 1}
	if wasFiltered {
		a.refreshGitLogCommits()
	}
	a.gitLogClampScrolls()
}

// toggleGitLogFilter is the ⌕ header button: open and focus, or close.
func (a *App) toggleGitLogFilter() {
	if a.gitLog.filter.open {
		a.closeGitLogFilter()
		return
	}
	a.openGitLogFilter()
}

// menuGitLogSearch is the ≡ Git row / Esc-S leader entry. It opens the
// log panel first when it's closed, because "search history" is a
// complete thought — making the user open the panel first would be the
// menu telling them to do half the work themselves.
func (a *App) menuGitLogSearch() {
	a.closeMenu()
	if !a.gitIsRepo {
		return
	}
	if !a.gitLog.open {
		a.menuToggleGitLog()
	}
	if !a.gitLog.open {
		return // no repo / refused
	}
	a.openGitLogFilter()
}

// gitLogFilterMode is the mode the current query text selects — the
// derived value the chips render from.
func (a *App) gitLogFilterMode() int {
	return parseGitLogQuery(a.gitLog.filter.field.String()).mode
}

// gitLogFilterSetMode rewrites the query under a new mode and re-runs
// it. Switching modes with a term already typed is the gesture the
// chips exist for ("not the message — the file"), so it searches
// immediately rather than waiting out a debounce nobody is typing
// through.
func (a *App) gitLogFilterSetMode(mode int) {
	a.gitLog.filter.field = newTextField(
		gitLogSetQueryMode(a.gitLog.filter.field.String(), mode))
	a.gitLog.filter.focused = true
	a.gitLogFilterStopTimer()
	a.gitLog.filter.seq++
	a.gitLogFilterRun()
}

// gitLogFilterCycleMode is Tab inside the field: the four modes in chip
// order. The bar has exactly one input, so Tab has no field to move to
// and this is the most useful thing it can mean.
func (a *App) gitLogFilterCycleMode() {
	next := (a.gitLogFilterMode() + 1) % len(gitLogModeChips)
	a.gitLogFilterSetMode(next)
}

// -----------------------------------------------------------------------------
// Keyboard
// -----------------------------------------------------------------------------

// handleGitLogFilterKey dispatches a keystroke while the field owns the
// keyboard:
//
//	Esc          close the bar and restore the full history
//	Enter        search NOW (a pickaxe user watching a debounce is
//	             owed a way to say "yes, that one, go")
//	Tab          cycle the search mode
//	Up/Down      move the commit selection — the panel has no keyboard
//	             of its own, so this is the only way to reach a hit
//	             without the mouse, and reaching it is the point
//	everything else  single-line editing, re-arming the debounce on edits
func (a *App) handleGitLogFilterKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		a.closeGitLogFilter()
		return
	case tcell.KeyEnter:
		a.gitLogFilterStopTimer()
		a.gitLog.filter.seq++
		a.gitLogFilterRun()
		return
	case tcell.KeyTab, tcell.KeyBacktab:
		a.gitLogFilterCycleMode()
		return
	case tcell.KeyUp:
		a.gitLogMoveSelection(-1)
		return
	case tcell.KeyDown:
		a.gitLogMoveSelection(1)
		return
	}
	if _, edited := a.gitLog.filter.field.handleKey(ev); edited {
		a.gitLogFilterArm()
	}
}

// -----------------------------------------------------------------------------
// Geometry — one source for draw AND mouse routing
// -----------------------------------------------------------------------------

// gitLogFilterRows is the bar's height inside the panel: one row when
// open, zero when not. Every body-row calculation in gitlog.go goes
// through it, so opening the bar costs the commit list exactly one row.
func (a *App) gitLogFilterRows() int {
	if a.gitLog.open && a.gitLog.filter.open {
		return 1
	}
	return 0
}

// gitLogFilterY is the bar's screen row — directly under the header.
func (a *App) gitLogFilterY() int {
	_, py, _, _ := a.gitLogRect()
	return py + 1
}

// gitLogFilterLabel is the field's prompt. Single-width glyph (the
// runeLen house rule): ⌕ is the search mark that doesn't cost two cells
// the way 🔍 would.
const gitLogFilterLabel = " ⌕ "

// gitLogChipsX is the column the mode chips start at, and whether they
// fit at all. The chips are dropped whole rather than clipped when the
// panel is too narrow to leave the field a usable 12 cells — the prefix
// syntax still reaches every mode, and a half-drawn chip row would
// leave click targets under labels that no longer describe them.
func (a *App) gitLogChipsX() (int, bool) {
	px, _, pw, _ := a.gitLogRect()
	total := 0
	for _, c := range gitLogModeChips {
		total += runeLen(c.label)
	}
	x := px + pw - 1 - total
	if x < px+runeLen(gitLogFilterLabel)+12 {
		return 0, false
	}
	return x, true
}

// gitLogChipRect returns the rect of chip i (index into
// gitLogModeChips). A zero-width rect means the chips are hidden, and
// btnRect.contains is false for every point on one.
func (a *App) gitLogChipRect(i int) btnRect {
	x, ok := a.gitLogChipsX()
	if !ok || i < 0 || i >= len(gitLogModeChips) {
		return btnRect{}
	}
	for j := 0; j < i; j++ {
		x += runeLen(gitLogModeChips[j].label)
	}
	return btnRect{x: x, y: a.gitLogFilterY(), w: runeLen(gitLogModeChips[i].label)}
}

// gitLogFilterFieldSpan returns the row and the [start, end) columns of
// the input — the single source the caret, the drawing, and the click
// mapping all read, so a click can't land on a different rune than the
// one it appears to.
func (a *App) gitLogFilterFieldSpan() (y, start, end int) {
	px, _, pw, _ := a.gitLogRect()
	y = a.gitLogFilterY()
	start = px + runeLen(gitLogFilterLabel)
	end = px + pw - 1
	if x, ok := a.gitLogChipsX(); ok {
		end = x - 1
	}
	if status := a.gitLogFilterStatus(); status != "" {
		if e := end - runeLen(status); e > start+8 {
			end = e
		}
	}
	if end < start {
		end = start
	}
	return y, start, end
}

// gitLogFilterStatus is the short right-hand indicator: what the bar is
// doing, or what it found. "" while the box is empty and idle — an
// unfiltered list needs no count, the title already carries one.
func (a *App) gitLogFilterStatus() string {
	f := a.gitLog.filter
	switch {
	case f.running:
		return " searching… "
	case f.applied == "":
		return ""
	case len(a.gitLog.commits) == 0:
		return " no matches "
	default:
		return " " + plural(len(a.gitLog.commits), "match", "matches") + " "
	}
}

// -----------------------------------------------------------------------------
// Mouse
// -----------------------------------------------------------------------------

// gitLogFilterPress routes a left press on the bar row, reporting
// whether it was consumed. A chip switches mode; anywhere else on the
// row takes focus and positions the caret (click where you want to
// type), so a stray click on the bar can never fall through to the
// commit list underneath.
func (a *App) gitLogFilterPress(x, y int) bool {
	if a.gitLogFilterRows() == 0 || y != a.gitLogFilterY() {
		return false
	}
	for i, c := range gitLogModeChips {
		if a.gitLogChipRect(i).contains(x, y) {
			a.gitLogFilterSetMode(c.mode)
			return true
		}
	}
	a.gitLog.filter.focused = true
	_, start, end := a.gitLogFilterFieldSpan()
	a.gitLog.filter.field.clickAt(start, end, x)
	return true
}

// -----------------------------------------------------------------------------
// Drawing
// -----------------------------------------------------------------------------

// drawGitLogFilterBar paints the bar:
//
//	⌕ fix: the thing         12 matches   msg  author  path  string
//
// It borrows the find bar's palette deliberately — the two are the same
// gesture aimed at different corpora, and a user who has met one should
// recognize the other without reading it.
func (a *App) drawGitLogFilterBar() {
	if a.gitLogFilterRows() == 0 {
		return
	}
	px, _, pw, _ := a.gitLogRect()
	th := a.theme
	ry := a.gitLogFilterY()

	barSt := tcell.StyleDefault.Background(th.LineHL).Foreground(th.Text)
	labelSt := tcell.StyleDefault.Background(th.LineHL).Foreground(th.Accent).Bold(true)
	mutedSt := tcell.StyleDefault.Background(th.LineHL).Foreground(th.Muted)
	emptySt := tcell.StyleDefault.Background(th.LineHL).Foreground(th.Error).Bold(true)

	for cx := px; cx < px+pw; cx++ {
		a.screen.SetContent(cx, ry, ' ', nil, barSt)
	}
	// The prompt is accented only while the field owns the keyboard, so
	// the bar says where the next keystroke lands without the user
	// having to hunt for the caret.
	promptSt := mutedSt
	if a.gitLog.filter.focused {
		promptSt = labelSt
	}
	drawAt(a.screen, px, ry, gitLogFilterLabel, promptSt)

	if status := a.gitLogFilterStatus(); status != "" {
		st := mutedSt
		if !a.gitLog.filter.running && a.gitLog.filter.applied != "" &&
			len(a.gitLog.commits) == 0 {
			st = emptySt // an empty result is news; say it in red
		}
		_, _, end := a.gitLogFilterFieldSpan()
		if sx := end + 1; sx+runeLen(status) <= px+pw {
			drawAt(a.screen, sx, ry, status, st)
		}
	}

	active := a.gitLogFilterMode()
	for i, c := range gitLogModeChips {
		r := a.gitLogChipRect(i)
		if r.w == 0 {
			continue
		}
		st := mutedSt
		if c.mode == active {
			st = tcell.StyleDefault.Background(th.Selection).
				Foreground(th.Accent).Bold(true)
		}
		drawAt(a.screen, r.x, r.y, c.label, st)
	}

	_, start, end := a.gitLogFilterFieldSpan()
	a.gitLog.filter.field.draw(a.screen, ry, start, end, barSt, a.gitLog.filter.focused)
	// The placeholder teaches the prefix syntax the chips can only
	// gesture at, and it stays up while FOCUSED (unlike find-all's,
	// which hides on focus): an empty focused box is precisely the
	// moment the user is deciding what to type.
	if len(a.gitLog.filter.field.value) == 0 {
		hint := "search messages · a:author · p:path · s:string"
		if runeLen(hint) <= end-start {
			drawAt(a.screen, start, ry, hint, mutedSt)
		}
	}
}
