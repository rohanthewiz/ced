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
//	│ 5 commits not on main · 1 already applied · merges as -m 1         │
//	│ [x] 7c8f3a7    Handle empty input              rohan   2 days ago  │
//	│ [ ] bff5bbb ⚠  Rework the lexer                rohan   2 days ago  │
//	│ [ ] 41c09de  ⑂ Merge branch 'fix/eof'          rohan   2 days ago  │
//	│ [=] 2a0370b    Bump version (already on main)  rohan   3 days ago  │
//	│ touches lexer.go, parse.go · ⚠ parse.go also …        ▸ diff alt+d │
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
//   - MERGES ARE PICKED AGAINST THEIR FIRST PARENT (N-051). Cherry-picking
//     a merge needs `-m <parent>`, and the dialog always answers 1. The
//     first parent is the branch the merge was made ON, so `-m 1` replays
//     "what this merge brought into the source branch" — the only reading
//     of "bring that merge over" that fits a branch-to-branch pick. `-m 2`
//     would replay the source branch's OWN side against the merged-in one,
//     which nobody means from a checkbox, so no parent picker is offered
//     (the terminal is the door for that). One parent number covers the
//     whole command, and since git 2.21 `-m 1` on a non-merge commit just
//     picks it normally — so merges and ordinary commits still go in ONE
//     `git cherry-pick`, the order rule below intact. `-m` is passed only
//     when a merge is among the picks, so a merge-free pick is the exact
//     argv it always was. The file list and ⚠ hint of a merge are its
//     diff against the first parent (`--diff-merges=first-parent`) — the
//     same diff `-m 1` applies.
//   - A MERGE THAT ONLY BRINGS IN WHAT HEAD HAS IS NOT PICKABLE. The
//     commonest merge on a feature branch is "merge main into feature":
//     its first-parent diff is main's own work, already on main, and git
//     stops the sequence on it as an empty pick. --cherry-mark never marks
//     a merge (merges have no patch id), so the dialog asks itself: a
//     merge whose every OTHER parent is already an ancestor of HEAD is
//     shown `[=]` like an applied commit, with the reason. One `rev-list`
//     answers it for every merge in the list.
//   - OLD GIT DEGRADES TO THE OLD LIST. `--diff-merges` needs git 2.31; if
//     the merge-aware log fails, the listing reruns without merges and the
//     summary says they are hidden — the dialog as it was before merges
//     could be picked, rather than an error.
//   - A DIFF PREVIEW FOLLOWS THE HIGHLIGHT (cherrypickdiff.go): alt+d, or
//     the "diff" toggle on the detail row, opens a pane with the
//     highlighted commit's message, stat and patch.
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
//   - TYPE TO FILTER (N-051). A long list (the cap is 300) narrows as you
//     type, the file tree's type-to-find model: printable letters build
//     the filter (Space stays the tick), Backspace edits it, and Esc
//     clears it before a second Esc closes the dialog. Matching is
//     case-insensitive over subject, short hash and author. The filter
//     is a VIEW (m.view) over m.commits: ticks survive narrowing, the
//     submit still picks every tick, hidden ones included, and the
//     summary says how many ticks the filter is hiding. Select all acts
//     on the visible rows, the tree marks' bulk rule.
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
	"unicode"

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

	// merge: the commit has more than one parent, so it is picked with
	// `-m 1`. synced: a merge whose every non-first parent is already on
	// HEAD — it only brings in what HEAD has, so it is not pickable.
	merge  bool
	synced bool
	// parents are the commit's parent hashes, first parent first; only
	// read to work out synced.
	parents []string

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
	// mergesHidden: git was too old for the merge-aware listing, so the
	// fallback left merges out and the summary says so.
	mergesHidden bool
	ticked       map[string]bool // by full hash

	// cursor indexes COMMITS (the highlighted commit; -1 when a filter
	// leaves nothing to highlight); scroll is a position in VIEW.
	cursor int
	scroll int

	// filter is the typed narrowing text; view lists the indices of
	// m.commits it keeps, newest first. Rebuilt by refilter.
	filter string
	view   []int
	// viewBuilt marks view as derived from the current commits; a fresh
	// load clears it, and vis() rebuilds on demand.
	viewBuilt bool

	recordOrigin bool // -x
	noCommit     bool // --no-commit

	// diff is the preview pane's state (cherrypickdiff.go).
	diff cherryPickDiffState

	dirty int // uncommitted files at open, for the warning row

	// lastBtn / lastClickRow / lastClickAt make presses edge-triggered and
	// let a second press on the same row toggle it (double-click).
	lastBtn      tcell.ButtonMask
	lastClickRow int
	lastClickAt  time.Time
}

// cherryPickLoadEvent carries a finished commit listing to the main loop.
type cherryPickLoadEvent struct {
	when         time.Time
	seq          int64
	commits      []cherryPickCommit
	capped       bool
	mergesHidden bool
	err          string
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
	m.commits, m.capped, m.mergesHidden = nil, false, false
	m.cursor, m.scroll = 0, 0
	m.ticked = map[string]bool{}
	m.filter, m.view, m.viewBuilt = "", nil, false
	if a.screen == nil {
		return
	}
	scr, root, source, seq := a.screen, a.rootDir, m.source, m.seq
	go func() {
		l, err := listCherryPickCommits(root, source)
		e := &cherryPickLoadEvent{when: time.Now(), seq: seq,
			commits: l.commits, capped: l.capped, mergesHidden: l.mergesHidden}
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
	m.commits, m.capped, m.mergesHidden = e.commits, e.capped, e.mergesHidden
	m.refilter()
	// The first pickable commit starts highlighted, so Enter on a fresh
	// dialog picks the newest commit not already applied — the commonest
	// single-pick, one key away.
	for i, c := range m.commits {
		if c.pickable() {
			m.cursor = i
			break
		}
	}
}

// cherryPickListing is listCherryPickCommits' answer.
type cherryPickListing struct {
	commits      []cherryPickCommit
	capped       bool
	mergesHidden bool
}

// listCherryPickCommits runs the log, the synced-merge probe and the
// overlap probe. Runs off the main loop. The merge-aware log is tried
// first; if git rejects it (older than 2.31, no --diff-merges) the
// merge-free log of earlier releases runs instead and the listing says
// merges were hidden. The second run's error is the one reported: when
// both fail the cause is the source, not the flag.
func listCherryPickCommits(rootDir, source string) (cherryPickListing, error) {
	var l cherryPickListing
	out, err := exec.Command("git", cherryPickLogArgs(rootDir, source, false)...).Output()
	if err != nil {
		out, err = exec.Command("git", cherryPickLogArgs(rootDir, source, true)...).Output()
		if err != nil {
			return l, err
		}
		l.mergesHidden = true
	}
	l.commits = parseCherryPickLog(out)
	l.capped = len(l.commits) > cherryPickMax
	if l.capped {
		l.commits = l.commits[:cherryPickMax]
	}
	markCherryPickSynced(l.commits, loadMergeParentsNotOnHead(rootDir, l.commits))
	markCherryPickOverlap(l.commits, loadOurSideChanges(rootDir, source))
	return l, nil
}

// cherryPickLogArgs is the listing's argv. legacy is the pre-merge form
// (git older than 2.31): merges left out, so --diff-merges is not needed.
// Otherwise merges are listed, and --diff-merges=first-parent makes
// --name-only list a merge's files against its first parent — the diff
// `-m 1` will apply (by default git log shows no files for a merge).
func cherryPickLogArgs(rootDir, source string, legacy bool) []string {
	args := []string{"-C", rootDir, "log", "--cherry-mark", "--right-only"}
	if legacy {
		args = append(args, "--no-merges")
	} else {
		args = append(args, "--diff-merges=first-parent")
	}
	return append(args, "--format=%x1e%m%x1f%H%x1f%h%x1f%an%x1f%ar%x1f%P%x1f%s", "--name-only",
		"-n", itoa(cherryPickMax+1), "HEAD..."+source, "--")
}

// parseCherryPickLog parses the log format above: records split on RS
// (0x1e), the first line of each holding US-separated fields (the
// parents space-separated, so two or more of them make a merge), the
// rest the --name-only file list.
func parseCherryPickLog(out []byte) []cherryPickCommit {
	var commits []cherryPickCommit
	for _, rec := range strings.Split(string(out), "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		head, rest, _ := strings.Cut(rec, "\n")
		// SplitN, not Split: the subject is last and may itself hold a US
		// byte (nothing stops a commit message carrying one).
		f := strings.SplitN(head, "\x1f", 7)
		if len(f) < 7 {
			continue
		}
		c := cherryPickCommit{
			applied: f[0] == "=",
			hash:    f[1], short: f[2], author: f[3], age: f[4], subject: f[6],
			parents: strings.Fields(f[5]),
		}
		c.merge = len(c.parents) > 1
		for _, p := range strings.Split(rest, "\n") {
			if p = strings.TrimSpace(p); p != "" {
				c.files = append(c.files, p)
			}
		}
		commits = append(commits, c)
	}
	return commits
}

// loadMergeParentsNotOnHead returns, of the listed merges' non-first
// parents, the ones that are NOT ancestors of HEAD. One `rev-list P… --not
// HEAD` answers for every merge at once: it walks what those parents
// reach that HEAD does not, and a parent appears in that walk exactly
// when it is not already on HEAD. The walk is bounded by the source's own
// side of the range, which the listing already walked. nil (no merges, or
// git failing) leaves every merge pickable — git is then the one to say
// a pick is empty, as it would have before.
func loadMergeParentsNotOnHead(rootDir string, commits []cherryPickCommit) map[string]bool {
	args := []string{"-C", rootDir, "rev-list"}
	n := len(args)
	for _, c := range commits {
		if c.merge {
			args = append(args, c.parents[1:]...)
		}
	}
	if len(args) == n {
		return nil
	}
	out, err := exec.Command("git", append(args, "--not", "HEAD", "--")...).Output()
	if err != nil {
		return nil
	}
	set := make(map[string]bool)
	for _, h := range strings.Fields(string(out)) {
		set[h] = true
	}
	return set
}

// markCherryPickSynced flags each merge whose every non-first parent is
// already on HEAD (absent from notOnHead). A nil set means the probe did
// not run or failed, and marks nothing.
func markCherryPickSynced(commits []cherryPickCommit, notOnHead map[string]bool) {
	if notOnHead == nil {
		return
	}
	for i := range commits {
		c := &commits[i]
		if !c.merge {
			continue
		}
		c.synced = true
		for _, p := range c.parents[1:] {
			if notOnHead[p] {
				c.synced = false
				break
			}
		}
	}
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

// pickable reports whether c can be ticked: neither its patch nor (for
// a merge) everything it brings in is already on HEAD.
func (c cherryPickCommit) pickable() bool { return !c.applied && !c.synced }

// pickable reports how many commits can be ticked.
func (m *cherryPickModal) pickable() int {
	n := 0
	for _, c := range m.commits {
		if c.pickable() {
			n++
		}
	}
	return n
}

// picksMerge reports whether any of hashes is a merge — the one case
// the command needs `-m 1`.
func (m *cherryPickModal) picksMerge(hashes []string) bool {
	want := make(map[string]bool, len(hashes))
	for _, h := range hashes {
		want[h] = true
	}
	for _, c := range m.commits {
		if c.merge && want[c.hash] {
			return true
		}
	}
	return false
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
	if c.synced {
		a.flash(c.short + " only merges in what " + m.target + " already has — nothing to pick")
		return
	}
	if m.ticked[c.hash] {
		delete(m.ticked, c.hash)
	} else {
		m.ticked[c.hash] = true
	}
}

// toggleAll ticks every VISIBLE pickable commit, or clears them when
// they already are — one button, labelled by what it will do. With no
// filter the view is the whole list, so this is the old select-all;
// under a filter it touches only what the user can see (the tree marks'
// bulk rule — a hidden row is never ticked or unticked behind their back).
func (m *cherryPickModal) toggleAll() {
	all := m.allVisibleTicked()
	for _, i := range m.vis() {
		c := m.commits[i]
		if !c.pickable() {
			continue
		}
		if all {
			delete(m.ticked, c.hash)
		} else {
			m.ticked[c.hash] = true
		}
	}
}

// allVisibleTicked reports whether every visible pickable commit is
// ticked (and there is at least one) — the state in which Select all
// becomes Select none.
func (m *cherryPickModal) allVisibleTicked() bool {
	n := 0
	for _, i := range m.vis() {
		c := m.commits[i]
		if !c.pickable() {
			continue
		}
		if !m.ticked[c.hash] {
			return false
		}
		n++
	}
	return n > 0
}

// allLabel is the select-all button's label.
func (m *cherryPickModal) allLabel() string {
	if m.allVisibleTicked() {
		return "[ Select none ]"
	}
	return "[ Select all ]"
}

// refilter rebuilds the view from the filter and keeps the highlight on
// a visible commit: the current one if it survived, else the first
// visible pickable one, else the first visible one, else none (-1).
func (m *cherryPickModal) refilter() {
	m.viewBuilt = true
	m.view = m.view[:0]
	q := strings.ToLower(m.filter)
	for i, c := range m.commits {
		if q == "" || cherryPickMatches(c, q) {
			m.view = append(m.view, i)
		}
	}
	if m.viewPos(m.cursor) >= 0 {
		return
	}
	m.cursor = -1
	for _, i := range m.view {
		if m.commits[i].pickable() {
			m.cursor = i
			return
		}
	}
	if len(m.view) > 0 {
		m.cursor = m.view[0]
	}
}

// cherryPickMatches reports whether c matches the lower-cased query:
// a substring of its subject, short hash or author, case-insensitively.
func cherryPickMatches(c cherryPickCommit, q string) bool {
	return strings.Contains(strings.ToLower(c.subject), q) ||
		strings.Contains(strings.ToLower(c.short), q) ||
		strings.Contains(strings.ToLower(c.author), q)
}

// vis is the view, built on first use after a load (or after a test sets
// commits directly) so no path can read a view that lags the commits.
func (m *cherryPickModal) vis() []int {
	if !m.viewBuilt {
		m.refilter()
	}
	return m.view
}

// viewPos is commit idx's position in the view, -1 when hidden.
func (m *cherryPickModal) viewPos(idx int) int {
	for p, i := range m.vis() {
		if i == idx {
			return p
		}
	}
	return -1
}

// hiddenTicks counts ticked commits the filter is hiding — said in the
// summary, because the submit will pick them too.
func (m *cherryPickModal) hiddenTicks() int {
	if m.filter == "" {
		return 0
	}
	shown := 0
	for _, i := range m.vis() {
		if m.ticked[m.commits[i].hash] {
			shown++
		}
	}
	return len(m.ticked) - shown
}

// setFilter replaces the filter, rebuilds the view and brings the
// highlight back on screen.
func (m *cherryPickModal) setFilter(a *App, f string) {
	m.filter = f
	m.refilter()
	m.scroll = 0
	m.ensureCursorVisible(a)
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
// rather than as rows trickle in. With the diff preview open the dialog
// takes the whole window instead (cherrypickdiff.go): a patch wants
// every row and column it can get, and a dialog that resized with each
// commit's diff would move the list under the pointer.
func (m *cherryPickModal) rect(a *App) (x, y, w, h int) {
	if m.diff.open {
		w = min(max(a.width-4, cherryPickMinW), cherryPickDiffMaxW)
		h = max(a.height-2, cherryPickMinH)
		return a.centeredRect(min(w, a.width), min(h, a.height))
	}
	w = min(max(a.width-4, cherryPickMinW), cherryPickMaxW)
	h = max(cherryPickFixedRows+len(m.commits), cherryPickMinH)
	h = min(h, cherryPickMaxH, max(a.height-2, cherryPickMinH))
	return a.centeredRect(min(w, a.width), min(h, a.height))
}

// listRows is how many commit rows fit — all the room there is, or with
// the diff preview open the list's share of it (cherryPickSplitRows).
func (m *cherryPickModal) listRows(a *App) int {
	_, _, _, h := m.rect(a)
	avail := max(h-cherryPickFixedRows, 1)
	if !m.diff.open {
		return avail
	}
	return cherryPickSplitRows(avail, len(m.commits))
}

// rowY maps the dialog's named rows to screen lines. One map for draw and
// hit-test (the btnRect rule applied to rows). Without the preview the
// list fills down to the detail row; with it the detail row follows the
// list and the diff pane (diffPaneRows) takes what is left above the
// options row.
func (m *cherryPickModal) rowY(a *App) (source, summary, listTop, detail, options, warn, buttons int) {
	_, my, _, mh := m.rect(a)
	source, summary, listTop = my+3, my+4, my+5
	buttons = my + mh - 2
	warn = buttons - 1
	options = warn - 1
	detail = options - 1
	if m.diff.open {
		detail = listTop + m.listRows(a)
	}
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

// ensureCursorVisible scrolls just enough to show the cursor row. Both
// sides are VIEW positions — the cursor is mapped through viewPos.
func (m *cherryPickModal) ensureCursorVisible(a *App) {
	vis := m.listRows(a)
	if p := m.viewPos(m.cursor); p >= 0 {
		if p < m.scroll {
			m.scroll = p
		}
		if p >= m.scroll+vis {
			m.scroll = p - vis + 1
		}
	}
	m.clampScroll(a)
}

// clampScroll keeps the scroll offset in range.
func (m *cherryPickModal) clampScroll(a *App) {
	m.scroll = min(max(m.scroll, 0), max(len(m.vis())-m.listRows(a), 0))
}

// moveCursor steps the highlight through the VIEW, clamped.
func (m *cherryPickModal) moveCursor(a *App, delta int) {
	if len(m.vis()) == 0 {
		return
	}
	p := max(m.viewPos(m.cursor), 0)
	p = min(max(p+delta, 0), len(m.vis())-1)
	m.cursor = m.vis()[p]
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
		p := m.scroll + i
		if p >= len(m.vis()) {
			break
		}
		m.drawRow(a, m.vis()[p], listTop+i)
	}

	// Detail of the highlighted commit, then the diff toggle at the row's
	// right end — the preview is OF the commit this row describes.
	tg := m.diffToggleRect(a)
	if d := m.detailText(); d != "" {
		drawAt(a.screen, mx+2, dy, gitPushClip(d, max(tg.x-1-(mx+2), 0)), c.muted)
	}
	drawAt(a.screen, tg.x, tg.y, m.diffToggleLabel(), c.title)
	if m.diff.open {
		m.drawDiffPane(a)
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
	if m.filter != "" {
		s := "filter “" + m.filter + "”: " + itoa(len(m.vis())) + " of " + itoa(len(m.commits))
		if n := m.hiddenTicks(); n > 0 {
			s += " · " + itoa(n) + " ticked hidden"
		}
		return s + " · Esc clears"
	}
	s := plural(len(m.commits), "commit", "commits") + " not on " + m.target
	if m.capped {
		s = "newest " + itoa(cherryPickMax) + " commits not on " + m.target + " (list capped)"
	}
	if n := len(m.commits) - m.pickable(); n > 0 {
		s += " · " + itoa(n) + " already applied"
	}
	// Merges: hidden only when git was too old to list them; otherwise
	// named only when the list holds one, with the parent it is picked
	// against.
	switch {
	case m.mergesHidden:
		s += " · merges hidden (git < 2.31)"
	case m.hasMerge():
		s += " · merges as -m 1"
	}
	s += " · applied oldest first"
	// The filter is invisible until typed into, so a list longer than the
	// tallest dialog can show says it can be narrowed.
	if len(m.commits) > cherryPickMaxH-cherryPickFixedRows {
		s += " · type to filter"
	}
	return s
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
	if c.synced {
		return c.short + " only merges in commits " + m.target + " already has"
	}
	if len(c.files) == 0 {
		return c.short + " changes no files"
	}
	s := "touches " + listFew(c.files, 3)
	if c.merge {
		// The merge's files are its first-parent diff — say so, since
		// that is also exactly what `-m 1` will apply.
		s = "merge, picked as -m 1 · " + s
	}
	if len(c.overlap) > 0 {
		s += " · ⚠ " + listFew(c.overlap, 2) + " also changed on " + m.target + " — may conflict"
	}
	return s
}

// hasMerge reports whether the list holds a pickable merge.
func (m *cherryPickModal) hasMerge() bool {
	for _, c := range m.commits {
		if c.merge && c.pickable() {
			return true
		}
	}
	return false
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
	if !c.pickable() {
		box, subjFG = "[=]", th.Muted
	}
	x := mx + 2
	drawAt(a.screen, x, y, box, st(th.Accent))
	x += 4
	drawAt(a.screen, x, y, c.short, st(th.AccentSoft))
	x += runeLen(c.short) + 1
	// Two marker cells, each with a fixed home so subjects stay aligned:
	// ⚠ (may conflict) then ⑂ (a merge, picked as -m 1).
	if len(c.overlap) > 0 && c.pickable() {
		a.screen.SetContent(x, y, '⚠', nil, st(th.Modified))
	}
	if c.merge {
		a.screen.SetContent(x+1, y, '⑂', nil, st(th.AccentSoft))
	}
	x += 3

	// Author and age right-aligned, when the subject keeps a readable
	// share of the row (gitlog's rule: the subject wins).
	end := mx + mw - 2
	tail := c.author + "  " + c.age
	if end-runeLen(tail)-x > 24 {
		drawAt(a.screen, end-runeLen(tail), y, tail, st(th.Muted))
		end -= runeLen(tail) + 2
	}
	subj := c.subject
	switch {
	case c.applied:
		subj += "  (already on " + m.target + ")"
	case c.synced:
		subj += "  (nothing " + m.target + " lacks)"
	}
	drawStatusText(a.screen, x, y, end-x, subj, st(subjFG))
}

// -----------------------------------------------------------------------------
// Keyboard
// -----------------------------------------------------------------------------

// handleKey: arrows and paging move, Space ticks, Enter picks, Esc
// cancels, and the Alt chords are the keyboard twins of every button —
// the modal owns the keyboard, so the ≡ menu cannot be their door
// (alt+d is the diff toggle's).
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
		case 'd':
			m.toggleDiff(a)
		case 'v':
			// Side by side is one switch for every pane; flipping it here
			// flips it everywhere, and setDiffSplit keeps this pane's
			// top line in view.
			a.setDiffSplit(!a.diffSplit)
		case 'n':
			m.stepDiff(a, 1)
		case 'p':
			m.stepDiff(a, -1)
		}
		return
	}
	// Alt/Shift + arrows and paging scroll the diff pane while it is open;
	// the bare keys still walk the list, so both stay one key away.
	if m.diff.open && ev.Modifiers()&(tcell.ModAlt|tcell.ModShift) != 0 && m.scrollDiffKey(a, ev.Key()) {
		return
	}
	switch ev.Key() {
	case tcell.KeyEsc:
		// Esc clears a filter first: dropping a narrowed view is the
		// smaller step back, and closing would lose the ticks too.
		if m.filter != "" {
			m.setFilter(a, "")
			return
		}
		a.closeModal()
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if r := []rune(m.filter); len(r) > 0 {
			m.setFilter(a, string(r[:len(r)-1]))
		}
	case tcell.KeyUp:
		m.moveCursor(a, -1)
	case tcell.KeyDown:
		m.moveCursor(a, 1)
	case tcell.KeyPgUp:
		m.moveCursor(a, -m.listRows(a))
	case tcell.KeyPgDn:
		m.moveCursor(a, m.listRows(a))
	case tcell.KeyHome:
		m.moveCursor(a, -len(m.vis()))
	case tcell.KeyEnd:
		m.moveCursor(a, len(m.vis()))
	case tcell.KeyEnter:
		m.submit(a)
	case tcell.KeyRune:
		// Space ticks; every other printable rune narrows the list.
		// Ctrl-modified runes are not text (the filter never sees a
		// stray control chord).
		switch r := ev.Rune(); {
		case r == ' ':
			m.toggle(a, m.cursor)
		case ev.Modifiers()&tcell.ModCtrl == 0 && unicode.IsPrint(r):
			m.setFilter(a, m.filter+string(r))
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
	if btn&(tcell.WheelUp|tcell.WheelDown) != 0 && m.diffPaneContains(a, x, y) {
		m.scrollDiffWheel(a, btn)
		return
	}
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
	case m.diffToggleRect(a).contains(x, y):
		m.toggleDiff(a)
		return
	}
	_, _, listTop, _, _, _, _ := m.rowY(a)
	row := y - listTop
	if row < 0 || row >= m.listRows(a) {
		return
	}
	if m.scroll+row >= len(m.vis()) {
		return
	}
	idx := m.vis()[m.scroll+row]
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
		if m.cursor < 0 || m.cursor >= len(m.commits) || !m.commits[m.cursor].pickable() {
			a.flash("Tick the commits to cherry-pick (Space)")
			return
		}
		hashes = []string{m.commits[m.cursor].hash}
	}
	args := cherryPickArgs(hashes, m.recordOrigin, m.noCommit, m.picksMerge(hashes))
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
// never appears. mainline adds `-m 1` for a set holding a merge (see the
// file comment: it covers every commit in the command, and git ≥ 2.21
// picks a non-merge under it unchanged).
func cherryPickArgs(hashes []string, recordOrigin, noCommit, mainline bool) []string {
	args := []string{"cherry-pick"}
	if noCommit {
		args = append(args, "--no-commit")
	} else if recordOrigin {
		args = append(args, "-x")
	}
	if mainline {
		args = append(args, "-m", "1")
	}
	return append(args, hashes...)
}
