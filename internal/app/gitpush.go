// =============================================================================
// File: internal/app/gitpush.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitpush.go is the push dialog — the one git verb the editor could not
// previously perform without dropping to a shell.
//
// Its whole reason to exist is a single rule: NEVER MAKE THE USER TYPE
// THE CURRENT BRANCH. Every other push UI asks "push to which branch?"
// and then hands you an empty text box or a list fetched from the
// remote, which for a brand-new branch does not contain the answer.
// Here the current local branch is always the first option in the
// remote-branch dropdown, present before any network call has finished,
// and pre-selected whenever it is also the tracked one. The dialog is
// four rows and Enter-Enter-Enter away from the common case.
//
// Four decisions worth knowing before reading the code:
//
//  1. It is its own modal rather than a formModal. formModal's rows are
//     customactions.Prompt values — a CONFIG type, describing what an
//     action author can put in a TOML file. Teaching it checkboxes, a
//     live header, option lists that refill from a goroutine, and a
//     button whose label changes with the form's state would push all
//     of that into a package that exists to parse config. The house
//     precedent (problems.go against gitlog.go) is that surfaces which
//     will evolve apart mirror each other's SHAPE and share only the
//     primitives — so this file reuses centeredRect, drawFrame,
//     btnRect, drawButton, and textField, and owns everything else.
//
//  2. Local facts are gathered inline, the network call is not. Remotes
//     and the current branch are two forks against .git — the same
//     budget menuGitSwitchBranch already spends to fill its picker, and
//     the dialog cannot open without them. `git ls-remote` talks to a
//     server over the network, so it runs on a goroutine and posts a
//     gitPushRefsEvent; the dropdown is usable the entire time and
//     simply gains rows when the answer lands.
//
//  3. The dialog never blocks on the network being reachable. An
//     ls-remote failure posts an EMPTY head list, which clears the
//     spinner and leaves the always-present options (current branch,
//     tracked branch, other…) in place. Pushing from a laptop that
//     cannot reach the server until the VPN comes up still works — you
//     were going to find out from git's own error either way.
//
//  4. Force is never bare. The checkbox is `--force-with-lease` (which
//     refuses when the remote moved since your last fetch), and ticking
//     it relabels the submit button to "Force Push" and raises a warning
//     line naming the exact ref at risk. Those two are the confirmation;
//     a second confirm modal on top of a deliberate tick plus a button
//     that says Force would be theatre.
//
// The vertical/horizontal key split is this file's one deviation from
// formModal: Up/Down move BETWEEN rows and Left/Right change the row's
// value. formModal cycles selects with both axes, which it can afford
// because every row is the same kind. Here the remote-branch row can
// become a text field, and a text field owns Left/Right for its caret —
// so the two axes have to mean different things.

package app

import (
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

// Layout. Width is wider than the other modals (60 for the form, 54 for
// the prompt) because a header like
// "feature/long-name → origin/feature/long-name (ahead 3)" is the point
// of the top row and truncating it would hide what is about to happen.
//
// The height is FIXED even though the warning line is usually blank:
// ticking Force with lease must not move the submit button out from
// under the pointer that is on its way to click it.
const (
	gitPushModalWidth  = 64
	gitPushModalHeight = 14
)

// Row-relative Y offsets inside the modal box. Named rather than
// inlined so draw and hit-testing read the same map — the btnRect house
// rule applied to rows.
const (
	gitPushHeaderRow  = 3  // "main → origin/main (ahead 3)"
	gitPushWarnRow    = 10 // force-with-lease warning, blank when off
	gitPushButtonRow  = 12
	gitPushOtherLabel = "other…"
)

// The four focusable rows, in tab order.
const (
	pushRowRemote = iota
	pushRowBranch
	pushRowUpstream
	pushRowForce
	pushRowCount
)

// gitPushRowY maps a row index to the modal-relative line its INPUT
// sits on. The two select rows spend two lines each (label above input,
// formModal's rhythm); the two checkbox rows spend one, because
// "[x] Set upstream" already carries its own label and a separate label
// line above a checkbox reads as a mistake.
var gitPushRowY = [pushRowCount]int{5, 7, 8, 9}

// gitPushHasLabelRow reports whether row i draws a label on the line
// above its input. Only the selects do.
func gitPushHasLabelRow(i int) bool { return i == pushRowRemote || i == pushRowBranch }

// -----------------------------------------------------------------------------
// State
// -----------------------------------------------------------------------------

// gitPushModal is the dialog's live state. local / upRemote / upBranch /
// ahead / behind are the snapshot taken when it opened and never change
// while it is up: they describe where HEAD stood, and re-reading git
// mid-dialog would let the header disagree with the command that is
// about to run.
type gitPushModal struct {
	local    string // current local branch — the ref being pushed
	upRemote string // remote half of the upstream ("origin"), "" when untracked
	upBranch string // branch half of the upstream ("main"), "" when untracked
	ahead    int    // commits HEAD has that the upstream doesn't
	behind   int    // and vice versa

	remotes   []string // `git remote`, in git's order
	remoteIdx int

	// branches is the remote-branch dropdown. Index 0 is ALWAYS the
	// current local branch (the hard requirement); the last entry is
	// always gitPushOtherLabel, which turns the row into `other`.
	branches  []string
	branchIdx int
	other     textField

	setUpstream bool
	force       bool

	focus   int
	loading bool // an ls-remote is in flight for the selected remote
}

// currentRemote is the selected remote name, guarded against an empty
// list so every caller can treat it as a plain string.
func (m *gitPushModal) currentRemote() string {
	if m.remoteIdx < 0 || m.remoteIdx >= len(m.remotes) {
		return ""
	}
	return m.remotes[m.remoteIdx]
}

// selectedOption is the raw dropdown entry — possibly gitPushOtherLabel.
func (m *gitPushModal) selectedOption() string {
	if m.branchIdx < 0 || m.branchIdx >= len(m.branches) {
		return ""
	}
	return m.branches[m.branchIdx]
}

// otherActive reports whether the branch row has become a text field.
func (m *gitPushModal) otherActive() bool {
	return m.selectedOption() == gitPushOtherLabel
}

// targetBranch resolves the remote branch this push would write to, or
// "" when the user picked "other…" and hasn't typed anything yet. The
// empty case is what submit refuses on — everything else treats "" as
// "not decided" and renders a placeholder.
func (m *gitPushModal) targetBranch() string {
	if m.otherActive() {
		return trimSpace(m.other.String())
	}
	return m.selectedOption()
}

// trackedBranch is the upstream's branch half, but ONLY while the
// selected remote is the upstream's remote. Pushing `main` to a
// different remote says nothing about where origin/main sits, so the
// tracked name is neither offered as an option nor used to caption the
// distance counts in that case.
func (m *gitPushModal) trackedBranch() string {
	if m.upRemote != "" && m.upRemote == m.currentRemote() {
		return m.upBranch
	}
	return ""
}

// targetIsUpstream reports whether the currently-selected (remote,
// branch) pair is exactly the tracked upstream — the only configuration
// in which the ahead/behind counts describe this push.
func (m *gitPushModal) targetIsUpstream() bool {
	tb := m.trackedBranch()
	return tb != "" && tb == m.targetBranch()
}

// -----------------------------------------------------------------------------
// Opening
// -----------------------------------------------------------------------------

// menuGitPush is the ≡ menu / command-palette entry point.
func (a *App) menuGitPush() {
	a.closeMenu()
	a.openGitPush()
}

// hasGitPushTarget gates every entry point. A repo is necessary but not
// sufficient: gitHasRemote is stamped by the same status snapshot that
// feeds the status bar, so the row dims in a repo nobody has added a
// remote to instead of opening a dialog whose first dropdown is empty.
func (a *App) hasGitPushTarget() bool {
	return a.gitIsRepo && a.gitHasRemote
}

// openGitPush gathers the local facts and raises the dialog. The three
// refusals are all "the dialog would have nothing to offer" cases, and
// each says which one it is rather than opening a broken form:
//
//   - detached HEAD has no current branch, and this dialog's entire
//     premise is that the current branch is the default answer. Pushing
//     a detached HEAD is a `HEAD:refs/heads/x` refspec typed by someone
//     who already knows they want it — the shell's job, not this form's.
//   - no remotes means nowhere to push.
func (a *App) openGitPush() {
	if !a.gitIsRepo {
		a.flash("Not a git repository")
		return
	}
	local := gitCurrentBranch(a.rootDir)
	if local == "" {
		a.flash("Detached HEAD — check out a branch to push")
		return
	}
	remotes := loadGitRemotes(a.rootDir)
	if len(remotes) == 0 {
		a.flash("No git remotes configured")
		return
	}

	upRemote, upBranch := splitUpstream(a.gitUpstream, remotes)
	m := &gitPushModal{
		local:    local,
		upRemote: upRemote,
		upBranch: upBranch,
		ahead:    a.gitAhead,
		behind:   a.gitBehind,
		remotes:  remotes,
		// Pre-checked exactly when there is no upstream: that is the one
		// case where omitting -u leaves the branch untracked and the
		// user back here next time with the same decision to make.
		setUpstream: a.gitUpstream == "",
	}
	// Default remote: the upstream's, else "origin" if it exists, else
	// whatever git listed first. "origin" is the convention, but a repo
	// that renamed it shouldn't land on an arbitrary row.
	m.remoteIdx = gitPushDefaultRemote(remotes, upRemote)
	m.rebuildBranches(nil)
	m.loading = true

	a.openModal(m)
	a.fetchGitPushHeads(m.currentRemote())
}

// gitPushDefaultRemote picks the initially-selected remote index.
func gitPushDefaultRemote(remotes []string, upRemote string) int {
	if i := indexOfString(remotes, upRemote); i >= 0 {
		return i
	}
	if i := indexOfString(remotes, "origin"); i >= 0 {
		return i
	}
	return 0
}

// rebuildBranches recomputes the dropdown for the currently-selected
// remote and re-finds the selection BY NAME. Name, not index: heads
// arrive from a goroutine and land in the middle of the list, so an
// index-preserving refresh would silently move the selection onto a
// different branch — the same identity rule the problems panel follows
// for exactly the same reason.
func (m *gitPushModal) rebuildBranches(heads []string) {
	prev := m.selectedOption()
	m.branches = gitPushBranchOptions(m.local, m.trackedBranch(), heads)
	m.branchIdx = indexOfString(m.branches, prev)
	if m.branchIdx < 0 {
		// No previous selection to keep (first build, or the remote just
		// changed and the old name isn't offered here). Prefer the
		// tracked branch — that is where a plain `git push` would go —
		// and fall back to the current branch at index 0.
		m.branchIdx = indexOfString(m.branches, m.trackedBranch())
		if m.branchIdx < 0 {
			m.branchIdx = 0
		}
	}
}

// gitPushBranchOptions builds the remote-branch dropdown.
//
// The order is the whole feature:
//
//	0        local            — ALWAYS first, ALWAYS present
//	1        tracked          — when it differs from the local name
//	2..n-2   fetched heads    — what the remote actually has
//	n-1      other…           — the escape hatch, a text field
//
// Index 0 is not "the most likely fetched head sorted to the top" — it
// is inserted unconditionally, because the branch you most want to push
// is very often the one the remote has never heard of. A dropdown built
// only from ls-remote is empty precisely when you need it most.
func gitPushBranchOptions(local, tracked string, heads []string) []string {
	opts := []string{local}
	seen := map[string]bool{local: true}
	if tracked != "" && !seen[tracked] {
		opts = append(opts, tracked)
		seen[tracked] = true
	}
	sorted := append([]string(nil), heads...)
	sort.Strings(sorted)
	for _, h := range sorted {
		if h == "" || seen[h] {
			continue
		}
		opts = append(opts, h)
		seen[h] = true
	}
	return append(opts, gitPushOtherLabel)
}

// indexOfString returns the position of want in list, or -1. Shared by
// the remote default and the by-name selection recovery.
func indexOfString(list []string, want string) int {
	if want == "" {
		return -1
	}
	for i, s := range list {
		if s == want {
			return i
		}
	}
	return -1
}

// -----------------------------------------------------------------------------
// Reading git
// -----------------------------------------------------------------------------

// gitCurrentBranch returns the checked-out branch name, or "" when HEAD
// is detached. Deliberately NOT loadGitBranch: that one falls back to a
// short SHA so the status bar always has something to show, and a SHA
// would sail through this dialog as if it were a branch name.
func gitCurrentBranch(rootDir string) string {
	if rootDir == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", rootDir, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n\r")
}

// splitUpstream splits "origin/feature/x" into its remote and branch
// halves by matching against the repo's actual remote list rather than
// cutting at the first "/". Branch names routinely contain slashes
// (feature/x, release/1.2) and remote names occasionally do, so the only
// reliable separator is a name we know exists. Longest match wins so a
// remote called "origin" can't shadow one called "origin/mirror".
//
// The fallback — no remote matches — cuts at the first "/" anyway: a
// half-right answer beats treating the whole string as a branch name.
func splitUpstream(upstream string, remotes []string) (remote, branch string) {
	if upstream == "" {
		return "", ""
	}
	best := ""
	for _, r := range remotes {
		if strings.HasPrefix(upstream, r+"/") && len(r) > len(best) {
			best = r
		}
	}
	if best != "" {
		return best, upstream[len(best)+1:]
	}
	if i := strings.Index(upstream, "/"); i > 0 {
		return upstream[:i], upstream[i+1:]
	}
	return "", upstream
}

// -----------------------------------------------------------------------------
// The async head listing
// -----------------------------------------------------------------------------

// gitPushRefsEvent carries one finished `git ls-remote --heads` from the
// background goroutine to the main loop. remote is echoed back so a
// stale answer for a remote the user has since switched away from can be
// dropped rather than repopulating the wrong dropdown.
type gitPushRefsEvent struct {
	when   time.Time
	remote string
	heads  []string
}

// When satisfies the tcell.Event interface.
func (e *gitPushRefsEvent) When() time.Time { return e.when }

// fetchGitPushHeads lists the remote's branches on a goroutine. A
// failure (offline, no such remote, auth prompt suppressed) posts an
// EMPTY list rather than an error: the dropdown's load-bearing rows are
// synthesized locally, so the honest outcome is "we learned nothing
// extra", not "the dialog is broken". git's own diagnosis shows up in
// the info modal if the push itself then fails.
//
// GIT_TERMINAL_PROMPT=0 keeps a credential prompt from blocking this
// goroutine forever on an HTTPS remote with no cached credentials —
// nothing is attached to a terminal it could prompt on.
func (a *App) fetchGitPushHeads(remote string) {
	if a.screen == nil || a.rootDir == "" || remote == "" {
		return
	}
	scr, root := a.screen, a.rootDir
	go func() {
		cmd := exec.Command("git", "-C", root, "ls-remote", "--heads", remote)
		cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0")
		var heads []string
		if out, err := cmd.Output(); err == nil {
			heads = parseLsRemoteHeads(out)
		}
		_ = scr.PostEvent(&gitPushRefsEvent{when: time.Now(), remote: remote, heads: heads})
	}()
}

// parseLsRemoteHeads pulls the branch names out of ls-remote's
// "<sha>\trefs/heads/<name>" lines. Anything that doesn't match the
// shape is skipped — the best-effort read contract every git loader
// here follows.
func parseLsRemoteHeads(out []byte) []string {
	var heads []string
	for _, ln := range strings.Split(string(out), "\n") {
		i := strings.Index(ln, "\trefs/heads/")
		if i < 0 {
			continue
		}
		name := strings.TrimSpace(ln[i+len("\trefs/heads/"):])
		if name != "" {
			heads = append(heads, name)
		}
	}
	return heads
}

// handleGitPushRefs merges a finished head listing into the open
// dialog. Runs on the main loop only. Both guards matter: the user may
// have closed the dialog (or opened a different modal) while the network
// call was out, and may have switched remotes — in which case a second
// fetch is already in flight and this answer describes a different
// server.
func (a *App) handleGitPushRefs(e *gitPushRefsEvent) {
	m, ok := a.modal.(*gitPushModal)
	if !ok || m.currentRemote() != e.remote {
		return
	}
	m.loading = false
	m.rebuildBranches(e.heads)
}

// -----------------------------------------------------------------------------
// Geometry
// -----------------------------------------------------------------------------

// rect returns the dialog's on-screen rectangle.
func (m *gitPushModal) rect(a *App) (x, y, w, h int) {
	return a.centeredRect(gitPushModalWidth, gitPushModalHeight)
}

// rowSpan returns row i's label line, input line, and the input's
// [start, end) columns. Checkbox rows report labelRow == inputRow;
// callers that care use gitPushHasLabelRow.
func (m *gitPushModal) rowSpan(a *App, i int) (labelRow, inputRow, fieldStart, fieldEnd int) {
	mx, my, mw, _ := m.rect(a)
	inputRow = my + gitPushRowY[i]
	labelRow = inputRow
	if gitPushHasLabelRow(i) {
		labelRow = inputRow - 1
	}
	return labelRow, inputRow, mx + 3, mx + mw - 3
}

// submitLabel is the submit button's text — and the loudest signal in
// the dialog that a tick in the Force row changed what the button does.
func (m *gitPushModal) submitLabel() string {
	if m.force {
		return "[ Force Push ]"
	}
	return "[ Push ]"
}

// buttons returns the (Cancel, Submit) rects. Submit is anchored by its
// RIGHT edge so relabeling it to "Force Push" grows it leftward instead
// of sliding it out from under the pointer.
func (m *gitPushModal) buttons(a *App) (cancel, submit btnRect) {
	mx, my, mw, _ := m.rect(a)
	btnY := my + gitPushButtonRow
	lbl := m.submitLabel()
	return btnRect{x: mx + 4, y: btnY, w: 10},
		btnRect{x: mx + mw - 4 - runeLen(lbl), y: btnY, w: runeLen(lbl)}
}

// -----------------------------------------------------------------------------
// Text
// -----------------------------------------------------------------------------

// headerText is the one-line summary of what Submit will do, rebuilt on
// every draw from the CURRENT selection rather than from the upstream
// recorded at open. Change the remote dropdown and the header follows —
// it is the sentence the buttons execute, not a caption about the past.
//
// The ahead/behind counts are appended only when the selected target IS
// the tracked upstream, because that is the only ref they were measured
// against. Printing "(ahead 3)" next to a hand-picked branch would
// assert a relationship nobody computed.
func (m *gitPushModal) headerText() string {
	target := m.targetBranch()
	if target == "" {
		target = "…"
	}
	s := m.local + " → " + m.currentRemote() + "/" + target
	if !m.targetIsUpstream() {
		return s
	}
	var parts []string
	if m.ahead > 0 {
		parts = append(parts, "ahead "+itoa(m.ahead))
	}
	if m.behind > 0 {
		parts = append(parts, "behind "+itoa(m.behind))
	}
	if len(parts) == 0 {
		return s + " (up to date)"
	}
	return s + " (" + strings.Join(parts, ", ") + ")"
}

// headerFit renders the header for a given width, dropping the LOCAL
// branch name before it will let the tail be clipped.
//
// Two long branch names plus a remote overflow 60 cells easily, and a
// plain tail-truncate eats the counts — which are the only thing on this
// line that no other row restates. The target ref is echoed by the
// Remote branch dropdown and the Set upstream row; the local name is
// echoed by the status bar, the tab strip's git label, and the log
// panel. So when something has to go, it is the local name, and the
// arrow stays to keep the line reading as a push rather than a caption.
func (m *gitPushModal) headerFit(width int) string {
	full := m.headerText()
	if runeLen(full) <= width {
		return full
	}
	if i := strings.Index(full, " → "); i >= 0 {
		if short := "→ " + full[i+len(" → "):]; runeLen(short) <= width {
			return short
		}
	}
	return gitPushClip(full, width)
}

// upstreamRowText labels the Set upstream checkbox with the tracking
// relationship it would create, so the row answers "upstream to WHAT?"
// without the user having to reconstruct it from the two dropdowns.
func (m *gitPushModal) upstreamRowText() string {
	target := m.targetBranch()
	if target == "" {
		return "Set upstream"
	}
	return "Set upstream — track " + m.currentRemote() + "/" + target
}

// forceWarnText is the sentence that appears the moment Force with
// lease is ticked. It names the exact ref at risk and states the lease
// in the same breath, because "--force-with-lease" is precisely the
// flag whose safety property nobody remembers under pressure.
func (m *gitPushModal) forceWarnText() string {
	target := m.targetBranch()
	if target == "" {
		target = "the remote branch"
	} else {
		target = m.currentRemote() + "/" + target
	}
	return "⚠ Overwrites " + target + " unless it moved since your last fetch."
}

// -----------------------------------------------------------------------------
// Drawing
// -----------------------------------------------------------------------------

// draw paints the dialog.
//
// Rows (relY):
//
//	0   top border
//	1   title — "Push   esc"
//	2   divider
//	3   header — "main → origin/main (ahead 3)"
//	4   "Remote" label
//	5   remote select        < origin >
//	6   "Remote branch" label
//	7   branch select/input  < main >
//	8   [x] Set upstream — track origin/main
//	9   [ ] Force with lease
//	10  force warning (blank while unticked — the row is reserved)
//	11  blank
//	12  buttons              [ Cancel ]        [ Push ]
//	13  bottom border
func (m *gitPushModal) draw(a *App) {
	mx, my, mw, mh := m.rect(a)
	c := a.chrome()
	c.drawFrame(a.screen, mx, my, mw, mh, "Push")
	a.screen.HideCursor()

	drawAt(a.screen, mx+2, my+gitPushHeaderRow, m.headerFit(mw-4), c.title)

	m.drawSelectRow(a, pushRowRemote, "Remote", m.currentRemote())
	m.drawBranchRow(a)
	m.drawCheckRow(a, pushRowUpstream, m.setUpstream, m.upstreamRowText())
	m.drawCheckRow(a, pushRowForce, m.force, "Force with lease")

	if m.force {
		warn := tcell.StyleDefault.Background(c.bg).Foreground(a.theme.Error).Bold(true)
		drawAt(a.screen, mx+2, my+gitPushWarnRow,
			gitPushClip(m.forceWarnText(), mw-4), warn)
	}

	cancel, submit := m.buttons(a)
	drawButton(a.screen, cancel.x, cancel.y, "[ Cancel ]", c.bg, a.theme.Text, false)
	// The submit button turns red when it means force — the same color
	// the dirty-close modal gives Discard, so "this one destroys
	// something" is one visual language across the editor.
	fg := a.theme.Accent
	if m.force {
		fg = a.theme.Error
	}
	drawButton(a.screen, submit.x, submit.y, m.submitLabel(), c.bg, fg, true)
}

// rowStyles returns the (label, input) styles for row i, tinted when it
// owns the focus. Pulled out so all four rows highlight identically.
func (m *gitPushModal) rowStyles(a *App, i int) (labelSt, inputSt tcell.Style) {
	c := a.chrome()
	focused := i == m.focus
	labelSt = c.muted
	if focused {
		labelSt = c.title
	}
	bg := a.theme.BG
	if focused {
		bg = a.theme.Subtle
	}
	return labelSt, tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
}

// drawSelectRow paints a "‹ value ›" dropdown row with its label above.
// The chevrons double as click targets (see handleMouse) and sit inside
// the field edges so they never land on the modal border.
func (m *gitPushModal) drawSelectRow(a *App, i int, label, value string) {
	mx, _, _, _ := m.rect(a)
	labelRow, inputRow, fieldStart, fieldEnd := m.rowSpan(a, i)
	labelSt, inputSt := m.rowStyles(a, i)

	drawAt(a.screen, mx+2, labelRow, label, labelSt)
	for cx := fieldStart - 1; cx <= fieldEnd; cx++ {
		a.screen.SetContent(cx, inputRow, ' ', nil, inputSt)
	}
	drawAt(a.screen, fieldStart, inputRow, "<", inputSt)
	drawAt(a.screen, fieldEnd-1, inputRow, ">", inputSt)
	value = gitPushClip(value, fieldEnd-fieldStart-4)
	drawAt(a.screen, fieldStart+(fieldEnd-fieldStart-runeLen(value))/2, inputRow, value, inputSt)
}

// drawBranchRow paints the remote-branch row, which is a dropdown until
// the user selects "other…" and a text field after. The label carries
// the loading state: an ls-remote in flight says so rather than leaving
// the user to wonder whether the list they see is the whole list.
func (m *gitPushModal) drawBranchRow(a *App) {
	label := "Remote branch"
	if m.otherActive() {
		label += " (new)"
	}
	if m.loading {
		label += " · listing…"
	}
	if !m.otherActive() {
		m.drawSelectRow(a, pushRowBranch, label, m.selectedOption())
		return
	}
	mx, _, _, _ := m.rect(a)
	labelRow, inputRow, fieldStart, fieldEnd := m.rowSpan(a, pushRowBranch)
	labelSt, inputSt := m.rowStyles(a, pushRowBranch)
	drawAt(a.screen, mx+2, labelRow, label, labelSt)

	// The chevrons stay even in text mode, and they are not decoration:
	// they are the mouse's only way BACK to the dropdown once "other…"
	// has taken the row over. Without them a click-driven user who picks
	// the escape hatch by accident is stuck in it until they cancel the
	// whole dialog.
	for cx := fieldStart - 1; cx <= fieldEnd; cx++ {
		a.screen.SetContent(cx, inputRow, ' ', nil, inputSt)
	}
	drawAt(a.screen, fieldStart, inputRow, "<", inputSt)
	drawAt(a.screen, fieldEnd-1, inputRow, ">", inputSt)
	ts, te := gitPushOtherSpan(fieldStart, fieldEnd)
	m.other.draw(a.screen, inputRow, ts, te, inputSt, m.focus == pushRowBranch)
}

// gitPushOtherSpan carves the text field's columns out of the branch
// row, leaving the chevrons their cells plus one of padding each side so
// the caret never sits on top of one. One helper so draw and the click
// hit-test can't disagree about where the field starts.
func gitPushOtherSpan(fieldStart, fieldEnd int) (start, end int) {
	return fieldStart + 2, fieldEnd - 2
}

// drawCheckRow paints a one-line "[x] label" checkbox. The whole line is
// the click target (see handleMouse) — a 3-cell bracket is a cruel thing
// to ask a mouse for, and the label is what the user is aiming at anyway.
func (m *gitPushModal) drawCheckRow(a *App, i int, on bool, label string) {
	mx, _, mw, _ := m.rect(a)
	_, inputRow, _, _ := m.rowSpan(a, i)
	_, inputSt := m.rowStyles(a, i)
	if i != m.focus {
		// Unfocused checkbox rows sit on the modal background, not the
		// input background: they aren't a box you type into, and a dark
		// slab across two rows would read as a second field stack.
		inputSt = a.chrome().bgSt
	}
	box := "[ ] "
	if on {
		box = "[x] "
	}
	text := gitPushClip(box+label, mw-4)
	for cx := mx + 1; cx < mx+mw-1; cx++ {
		a.screen.SetContent(cx, inputRow, ' ', nil, inputSt)
	}
	drawAt(a.screen, mx+2, inputRow, text, inputSt)
}

// gitPushClip truncates s to at most w cells. Rune-sliced, not
// byte-sliced: branch names carry non-ASCII often enough (and the
// warning line starts with ⚠) that a byte cut would emit a broken rune.
func gitPushClip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}

// -----------------------------------------------------------------------------
// Keyboard
// -----------------------------------------------------------------------------

// handleKey routes keystrokes. The axis split (see the file header):
// Tab / Up / Down move BETWEEN rows, Left / Right change the focused
// row's value, Space toggles a checkbox, Enter advances and submits on
// the last row. The branch row's text-field mode takes Left/Right for
// its caret and every printable rune for its value — which is exactly
// why the vertical axis, not the horizontal one, owns navigation here.
func (m *gitPushModal) handleKey(a *App, ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		a.closeModal()
		return
	case tcell.KeyTab, tcell.KeyDown:
		m.moveFocus(+1)
		return
	case tcell.KeyBacktab, tcell.KeyUp:
		m.moveFocus(-1)
		return
	case tcell.KeyEnter:
		if m.focus == pushRowCount-1 {
			m.submit(a)
			return
		}
		m.moveFocus(+1)
		return
	}

	switch m.focus {
	case pushRowRemote:
		if d := gitPushValueDelta(ev); d != 0 {
			m.cycleRemote(a, d)
		}
	case pushRowBranch:
		if m.otherActive() {
			// In text mode the field owns everything the modal didn't
			// already claim above — including Space and, in the middle of
			// the value, Left/Right. The exception is an arrow pressed at
			// the END it points toward: there the caret has nowhere to go
			// and the key would be dead, so it steps the option instead.
			// That is the keyboard's only way BACK out of "other…", and
			// arrowing off the end of a field is the conventional gesture
			// for leaving one.
			d := gitPushValueDelta(ev)
			if (d < 0 && m.other.cursor == 0) || (d > 0 && m.other.cursor == len(m.other.value)) {
				m.cycleBranch(d)
				return
			}
			m.other.handleKey(ev)
			return
		}
		if d := gitPushValueDelta(ev); d != 0 {
			m.cycleBranch(d)
		}
	case pushRowUpstream:
		if gitPushIsToggle(ev) {
			m.setUpstream = !m.setUpstream
		}
	case pushRowForce:
		if gitPushIsToggle(ev) {
			m.force = !m.force
		}
	}
}

// gitPushValueDelta maps a keystroke to a select's step: Left is -1,
// Right is +1, anything else 0.
func gitPushValueDelta(ev *tcell.EventKey) int {
	switch ev.Key() {
	case tcell.KeyLeft:
		return -1
	case tcell.KeyRight:
		return +1
	}
	return 0
}

// gitPushIsToggle reports whether ev flips a checkbox: Space (the
// universal one) or Left/Right, which are "change this row's value" on
// every other row and would be dead keys here otherwise.
func gitPushIsToggle(ev *tcell.EventKey) bool {
	if ev.Key() == tcell.KeyRune && ev.Rune() == ' ' {
		return true
	}
	return gitPushValueDelta(ev) != 0
}

// moveFocus shifts focus by delta with wrapping — the same
// never-stuck-at-the-end behavior as every other list in the editor.
func (m *gitPushModal) moveFocus(delta int) {
	i := (m.focus + delta) % pushRowCount
	if i < 0 {
		i += pushRowCount
	}
	m.focus = i
}

// cycleRemote steps the remote selection and restarts the head listing.
// The previous remote's heads are DROPPED (rebuildBranches with nil)
// rather than kept until the new answer lands: they describe a different
// server, and a dropdown that briefly offers branches which don't exist
// where you're pushing is worse than one that briefly offers fewer.
func (m *gitPushModal) cycleRemote(a *App, delta int) {
	n := len(m.remotes)
	if n <= 1 {
		return
	}
	m.remoteIdx = (m.remoteIdx + delta + n) % n
	m.rebuildBranches(nil)
	m.loading = true
	a.fetchGitPushHeads(m.currentRemote())
}

// cycleBranch steps the remote-branch selection, wrapping. Selecting
// "other…" seeds the text field with the current local branch name so
// the common "same name plus a suffix" edit starts from something.
func (m *gitPushModal) cycleBranch(delta int) {
	n := len(m.branches)
	if n == 0 {
		return
	}
	i := (m.branchIdx + delta + n) % n
	m.branchIdx = i
	if m.otherActive() && m.other.String() == "" {
		m.other = newTextField(m.local)
	}
}

// -----------------------------------------------------------------------------
// Mouse
// -----------------------------------------------------------------------------

// handleMouse routes clicks: outside dismisses, the buttons resolve, a
// click on any row focuses it, a chevron cycles a select, a click on a
// checkbox row's line toggles it, and a click inside the "other…" text
// field positions the caret.
func (m *gitPushModal) handleMouse(a *App, x, y int, btn tcell.ButtonMask) {
	if btn&tcell.Button1 == 0 {
		return
	}
	mx, my, mw, mh := m.rect(a)
	if x < mx || x >= mx+mw || y < my || y >= my+mh {
		a.closeModal()
		return
	}

	cancel, submit := m.buttons(a)
	switch {
	case cancel.contains(x, y):
		a.closeModal()
		return
	case submit.contains(x, y):
		m.submit(a)
		return
	}

	for i := 0; i < pushRowCount; i++ {
		labelRow, inputRow, fieldStart, fieldEnd := m.rowSpan(a, i)
		if y != labelRow && y != inputRow {
			continue
		}
		m.focus = i
		if y != inputRow {
			return // a label click focuses the row and stops there
		}
		switch i {
		case pushRowRemote:
			m.chevronClick(a, x, fieldStart, fieldEnd, func(d int) { m.cycleRemote(a, d) })
		case pushRowBranch:
			if m.otherActive() {
				// Chevrons first: they are the way out of text mode, so
				// they outrank caret placement on the two cells they own.
				m.chevronClick(a, x, fieldStart, fieldEnd, m.cycleBranch)
				if !m.otherActive() {
					return
				}
				ts, te := gitPushOtherSpan(fieldStart, fieldEnd)
				m.other.clickAt(ts, te, x)
				return
			}
			m.chevronClick(a, x, fieldStart, fieldEnd, m.cycleBranch)
		case pushRowUpstream:
			m.setUpstream = !m.setUpstream
		case pushRowForce:
			m.force = !m.force
		}
		return
	}
}

// chevronClick applies a select's ‹ / › click if x landed on one.
func (m *gitPushModal) chevronClick(a *App, x, fieldStart, fieldEnd int, step func(int)) {
	switch x {
	case fieldStart:
		step(-1)
	case fieldEnd - 1:
		step(+1)
	}
}

// -----------------------------------------------------------------------------
// Submit
// -----------------------------------------------------------------------------

// submit builds and fires the push.
//
// The refspec is ALWAYS explicit — `<local>:<remote-branch>` — even when
// the two names match. Bare `git push origin main` pushes whatever
// push.default resolves `main` to, which is configuration this dialog
// has no view of; the explicit form means the command that runs is the
// sentence the header showed. `-u` with an explicit refspec still sets
// the upstream to the right side, which is what makes "push main to
// feature-x and track it" one gesture.
//
// An empty "other…" name keeps the dialog OPEN and moves focus to the
// row that needs filling, rather than closing on a no-op the way an
// empty prompt submit does — the user has three other answers in here
// they'd have to re-enter.
func (m *gitPushModal) submit(a *App) {
	remote := m.currentRemote()
	branch := m.targetBranch()
	if remote == "" {
		a.flash("No remote selected")
		return
	}
	if branch == "" {
		a.flash("Enter a remote branch name")
		m.focus = pushRowBranch
		return
	}

	args := m.pushArgs(remote, branch)
	label := "Push " + m.local + " → " + remote + "/" + branch
	a.closeModal()
	a.runGitCmd(label, args...)
	// A push is the slowest thing in the git menu — seconds against a
	// remote — so it announces itself on the way out. The done-event's
	// own flash replaces this one when it lands.
	a.flash(label + "…")
}

// pushArgs assembles the git argv. Split out from submit so the exact
// command — the one thing here with real consequences — can be pinned by
// a test without a test ever running `git push`.
//
// Order matters to git only in that the refspec comes last; the flags
// are listed in the order the dialog presents them so a reader can map
// argv back onto the form.
func (m *gitPushModal) pushArgs(remote, branch string) []string {
	args := []string{"push"}
	if m.setUpstream {
		args = append(args, "--set-upstream")
	}
	if m.force {
		args = append(args, "--force-with-lease")
	}
	return append(args, remote, m.local+":"+branch)
}

// -----------------------------------------------------------------------------
// The status-bar indicator
// -----------------------------------------------------------------------------

// gitPushStatusSuffix renders the tracking distance for the status bar:
// " ↑3", " ↓2", " ↑3↓2", or a bare " ↑" for a branch that has never been
// pushed. Nothing at all when the branch is level with its upstream, or
// when the repo has no remote to push to — an always-present "↑0" would
// be noise on the surface with the least room to spare.
//
// The bare arrow is the untracked case: there is no count to give
// (nothing to measure against), but "there is a remote and this branch
// isn't on it" is exactly the moment this dialog exists for, and it is
// the moment a count-only indicator would stay silent for. Both arrows
// are single-width, which keeps the stamped segment rects honest.
func (a *App) gitPushStatusSuffix() string {
	if !a.gitHasRemote {
		return ""
	}
	if a.gitUpstream == "" {
		return " ↑"
	}
	s := ""
	if a.gitAhead > 0 {
		s += "↑" + itoa(a.gitAhead)
	}
	if a.gitBehind > 0 {
		s += "↓" + itoa(a.gitBehind)
	}
	if s == "" {
		return ""
	}
	return " " + s
}
