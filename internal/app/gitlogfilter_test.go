// =============================================================================
// File: internal/app/gitlogfilter_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the git log search bar: the query parser and the mode
// rewrite (pure), the argv every mode builds (pinned without forking
// git — the pushArgs rule: the thing with consequences is testable on
// its own), the staleness guards on the async result, the geometry the
// bar takes out of the commit list, the keyboard and mouse routing, and
// one real-repo pass that proves all four modes actually select the
// commits they claim to.

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/editor"
)

// TestParseGitLogQuery pins the prefix grammar: each prefix picks its
// mode, no prefix is a message search, the space after a prefix is
// eaten, a TRAILING space is kept (it can be part of a pickaxe term),
// and anything that leaves an empty term is not a search at all.
func TestParseGitLogQuery(t *testing.T) {
	cases := []struct {
		text     string
		wantMode int
		wantTerm string
	}{
		{"fix the thing", gitLogModeMessage, "fix the thing"},
		{"  fix", gitLogModeMessage, "fix"},
		{"a:rohan", gitLogModeAuthor, "rohan"},
		{"a: rohan", gitLogModeAuthor, "rohan"},
		{"p:internal/app/gitlog.go", gitLogModePath, "internal/app/gitlog.go"},
		{"s:gitLogMaxCommits", gitLogModePickaxe, "gitLogMaxCommits"},
		{"s:foo ", gitLogModePickaxe, "foo "},
		{"", gitLogModeMessage, ""},
		{"   ", gitLogModeMessage, ""},
		{"s:", gitLogModePickaxe, ""},
		// A colon that isn't one of ours is just text to search for.
		{"fix: the thing", gitLogModeMessage, "fix: the thing"},
	}
	for _, c := range cases {
		got := parseGitLogQuery(c.text)
		if got.mode != c.wantMode || got.term != c.wantTerm {
			t.Errorf("parseGitLogQuery(%q) = {%d, %q}, want {%d, %q}",
				c.text, got.mode, got.term, c.wantMode, c.wantTerm)
		}
		if got.active() != (c.wantTerm != "") {
			t.Errorf("parseGitLogQuery(%q).active() = %v", c.text, got.active())
		}
	}
}

// TestGitLogSetQueryMode pins the chip rewrite — the mechanism that
// keeps the text the single source of truth. Switching modes carries
// the term across, and switching back to the message mode drops the
// prefix entirely rather than leaving a dead one in the box.
func TestGitLogSetQueryMode(t *testing.T) {
	cases := []struct {
		text string
		mode int
		want string
	}{
		{"rohan", gitLogModeAuthor, "a:rohan"},
		{"a:rohan", gitLogModePath, "p:rohan"},
		{"p:x.go", gitLogModePickaxe, "s:x.go"},
		{"s:magic", gitLogModeMessage, "magic"},
		{"", gitLogModeAuthor, "a:"},
	}
	for _, c := range cases {
		if got := gitLogSetQueryMode(c.text, c.mode); got != c.want {
			t.Errorf("gitLogSetQueryMode(%q, %d) = %q, want %q",
				c.text, c.mode, got, c.want)
		}
	}
}

// TestGitLogFilterArgs pins the argv per mode. This is the file's
// consequential surface — the difference between --grep and -S is the
// difference between two entirely different answers — so it is tested
// without git anywhere near it.
func TestGitLogFilterArgs(t *testing.T) {
	base := []string{"log", "--all", "--date=relative",
		"-n", itoa(gitLogMaxCommits + 1), "--format=" + gitLogFormat}

	if got := gitLogFilterArgs(gitLogQuery{}); !equalStrings(got, base) {
		t.Errorf("empty query args = %v, want the bare log command %v", got, base)
	}

	cases := []struct {
		q    gitLogQuery
		tail []string
	}{
		{gitLogQuery{gitLogModeMessage, "fix"}, []string{"-i", "--grep=fix"}},
		{gitLogQuery{gitLogModeAuthor, "rohan"}, []string{"-i", "--author=rohan"}},
		{gitLogQuery{gitLogModePath, "a.go"}, []string{"--follow", "--", "a.go"}},
		{gitLogQuery{gitLogModePickaxe, "magic"}, []string{"-Smagic"}},
		// A term that starts with a dash must stay a TERM: every mode
		// either attaches it to its flag or fences it behind "--".
		{gitLogQuery{gitLogModeMessage, "--all"}, []string{"-i", "--grep=--all"}},
		{gitLogQuery{gitLogModePickaxe, "-rf"}, []string{"-S-rf"}},
	}
	for _, c := range cases {
		want := append(append([]string{}, base...), c.tail...)
		if got := gitLogFilterArgs(c.q); !equalStrings(got, want) {
			t.Errorf("args for %+v = %v, want %v", c.q, got, want)
		}
	}
}

// TestGitLogFilterArgs_CommitModeDropsAll is the one mode whose argv is
// not the common base, and the reason is easy to get wrong: `--all`
// adds every ref to the rev list, so `git log --all <hash>` walks the
// whole repository and buries the commit that was asked for. The rev
// also has to be fenced behind `--`, like every other term here.
func TestGitLogFilterArgs_CommitModeDropsAll(t *testing.T) {
	args := gitLogFilterArgs(gitLogQuery{gitLogModeCommit, "a3f2c1d"})

	for _, a := range args {
		if a == "--all" {
			t.Fatalf("commit mode must not carry --all: %v", args)
		}
	}
	want := []string{"log", "--date=relative", "-n", itoa(gitLogMaxCommits + 1),
		"--format=" + gitLogFormat, "a3f2c1d", "--"}
	if !equalStrings(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

// TestParseGitLogQuery_CommitPrefix pins the prefix that reaches it, and
// the collision it deliberately avoids: `#42` stays a MESSAGE search,
// because searching a log for an issue number is at least as common as
// naming a revision, and a prefix that quietly reinterpreted it would
// return the wrong commits with no way to see why.
func TestParseGitLogQuery_CommitPrefix(t *testing.T) {
	if q := parseGitLogQuery("c:a3f2c1d"); q.mode != gitLogModeCommit || q.term != "a3f2c1d" {
		t.Fatalf("c: query = %+v", q)
	}
	if q := parseGitLogQuery("#42"); q.mode != gitLogModeMessage || q.term != "#42" {
		t.Fatalf("#42 = %+v, want a message search", q)
	}
	// And the chips still round-trip the mode through the text, which is
	// the invariant that keeps a chip from disagreeing with the query.
	if got := gitLogSetQueryMode("p:main.go", gitLogModeCommit); got != "c:main.go" {
		t.Fatalf("mode rewrite = %q", got)
	}
}

// TestRevealGitLogCommit_FallsBackToAQueryForAnUnloadedCommit covers the
// path a blame click takes on any file older than the loaded history:
// the commit is not in the list, so it is asked for by name — and the
// hash is remembered so the RESULT lands on it rather than on whatever
// applyGitLogCommits' identity rule would have preferred.
func TestRevealGitLogCommit_FallsBackToAQueryForAnUnloadedCommit(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.gitLog.open = true
	a.gitLog.commits = []gitLogCommit{{Hash: "1111", Short: "1111"}}

	a.revealGitLogCommit("deadbee")
	defer a.gitLogFilterStopTimer()

	if !a.gitLog.filter.open {
		t.Fatal("the query the panel is showing has to be visible")
	}
	if got := a.gitLog.filter.field.String(); got != "c:deadbee" {
		t.Fatalf("filter text = %q, want c:deadbee", got)
	}
	if a.gitLog.filter.focused {
		t.Fatal("the answer is the list, not the box — the keyboard stays with the panel")
	}
	if a.gitLog.filter.reveal != "deadbee" {
		t.Fatalf("reveal = %q, want the hash", a.gitLog.filter.reveal)
	}

	// The result lands on the revealed commit even though the previous
	// selection survived into the new list.
	a.handleGitLogFilterResult(&gitLogFilterEvent{
		seq:   a.gitLog.filter.seq,
		query: "c:deadbee",
		commits: []gitLogCommit{
			{Hash: "deadbee", Short: "deadbee"},
			{Hash: "1111", Short: "1111"},
		},
	})
	c, ok := a.gitLogSelectedCommit()
	if !ok || c.Hash != "deadbee" {
		t.Fatalf("selected %+v, want the revealed commit", c)
	}
	if a.gitLog.filter.reveal != "" {
		t.Fatal("the reveal is spent once it lands")
	}
}

// TestRevealGitLogCommit_PrefersTheLoadedList: a commit already on
// screen costs no fork and must not disturb a list the user may have
// arranged with a search of their own.
func TestRevealGitLogCommit_PrefersTheLoadedList(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.gitLog.open = true
	a.gitLog.filter.applied = "a:ada"
	a.gitLog.commits = []gitLogCommit{
		{Hash: "1111", Short: "1111"},
		{Hash: "2222", Short: "2222"},
	}

	a.revealGitLogCommit("2222")

	if a.gitLog.selected != 1 {
		t.Fatalf("selected index %d, want the matching row", a.gitLog.selected)
	}
	if a.gitLog.filter.open || a.gitLog.filter.applied != "a:ada" {
		t.Fatal("a reveal that found its commit must leave the user's search alone")
	}
}

// equalStrings compares two argv slices element by element.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestGitLogFilterResult_DropsStale is the staleness contract: a result
// stamped with an older sequence number is discarded. Without it a slow
// pickaxe would land on top of the fast --grep the user has since typed
// — the exact failure the sequence number exists to prevent.
func TestGitLogFilterResult_DropsStale(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitLog.open = true
	a.gitLog.filter.open = true
	a.gitLog.filter.seq = 7
	a.gitLog.commits = []gitLogCommit{{Hash: "aaaa", Short: "a1"}}

	a.handleGitLogFilterResult(&gitLogFilterEvent{
		when: time.Now(), seq: 6, query: "old",
		commits: []gitLogCommit{{Hash: "bbbb", Short: "b2"}},
	})
	if len(a.gitLog.commits) != 1 || a.gitLog.commits[0].Hash != "aaaa" {
		t.Fatalf("stale result overwrote the list: %+v", a.gitLog.commits)
	}
	if a.gitLog.filter.applied != "" {
		t.Errorf("stale result marked the list filtered (%q)", a.gitLog.filter.applied)
	}

	a.handleGitLogFilterResult(&gitLogFilterEvent{
		when: time.Now(), seq: 7, query: "s:magic",
		commits: []gitLogCommit{{Hash: "bbbb", Short: "b2"}},
	})
	if len(a.gitLog.commits) != 1 || a.gitLog.commits[0].Hash != "bbbb" {
		t.Fatalf("current result did not land: %+v", a.gitLog.commits)
	}
	if a.gitLog.filter.applied != "s:magic" {
		t.Errorf("applied = %q, want the query that produced the list", a.gitLog.filter.applied)
	}
	if a.gitLog.filter.running {
		t.Error("the awaited result should have cleared the searching indicator")
	}

	// An empty query is a result too — it restores the unfiltered list
	// and must clear `applied`, or the panel would keep claiming to be
	// filtered and the periodic refresh would stay stood down forever.
	a.gitLog.filter.seq = 8
	a.handleGitLogFilterResult(&gitLogFilterEvent{
		when: time.Now(), seq: 8, query: "",
		commits: []gitLogCommit{{Hash: "cccc", Short: "c3"}},
	})
	if a.gitLog.filter.applied != "" {
		t.Errorf("empty query left applied = %q", a.gitLog.filter.applied)
	}
}

// TestGitLogFilterTick_Guards pins the debounce's bail-outs: a tick from
// before the last keystroke, or one that arrives after the bar closed,
// must not fire a search.
func TestGitLogFilterTick_Guards(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitLog.open = true
	a.gitLog.filter.open = true
	a.gitLog.filter.seq = 3

	a.handleGitLogFilterTick(&gitLogFilterTickEvent{when: time.Now(), seq: 2})
	if a.gitLog.filter.running {
		t.Error("a stale tick started a search")
	}
	a.gitLog.filter.open = false
	a.handleGitLogFilterTick(&gitLogFilterTickEvent{when: time.Now(), seq: 3})
	if a.gitLog.filter.running {
		t.Error("a tick for a closed bar started a search")
	}
}

// TestRefreshGitLogCommits_YieldsToFilter pins decision 3: the pipeline
// refresh (10s tick, saves, finished git commands) leaves search results
// alone. A tick that silently replaced the user's search with the head
// of the log would be indistinguishable from the search having failed.
func TestRefreshGitLogCommits_YieldsToFilter(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitLog.open = true
	a.gitLog.filter.open = true
	a.gitLog.filter.applied = "s:magic"
	a.gitLog.commits = []gitLogCommit{{Hash: "aaaa", Short: "a1"}}

	a.refreshGitLogCommits()
	if len(a.gitLog.commits) != 1 || a.gitLog.commits[0].Hash != "aaaa" {
		t.Fatalf("the periodic refresh clobbered the search result: %+v", a.gitLog.commits)
	}
}

// TestCloseGitLogFilter_ClearsEverything pins the close contract: the
// bar, the text, the focus and the applied marker all go, and an
// in-flight search can no longer land (its sequence number is stale).
func TestCloseGitLogFilter_ClearsEverything(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitLog.open = true
	a.gitLog.filter = gitLogFilter{
		open: true, focused: true, seq: 4, applied: "fix",
		field: newTextField("fix"),
	}
	a.gitLog.commits = []gitLogCommit{{Hash: "aaaa", Short: "a1"}}
	inflight := a.gitLog.filter.seq

	a.closeGitLogFilter()
	if a.gitLog.filter.open || a.gitLog.filter.focused {
		t.Error("close left the bar open or focused")
	}
	if a.gitLog.filter.field.String() != "" || a.gitLog.filter.applied != "" {
		t.Errorf("close left state behind: %+v", a.gitLog.filter)
	}
	if a.gitLog.filter.seq == inflight {
		t.Error("close must invalidate an in-flight search")
	}
	// Closing reloaded the unfiltered history, which for this temp dir
	// (no repo) is empty — and the invalidated search must not refill
	// it even if the bar is somehow open again by the time it lands.
	if len(a.gitLog.commits) != 0 {
		t.Fatalf("close did not reload the unfiltered history: %+v", a.gitLog.commits)
	}
	a.gitLog.filter.open = true
	a.handleGitLogFilterResult(&gitLogFilterEvent{
		when: time.Now(), seq: inflight, query: "fix",
		commits: []gitLogCommit{{Hash: "bbbb"}},
	})
	if len(a.gitLog.commits) != 0 {
		t.Error("a search from before the close repopulated the list")
	}
}

// TestGitLogFilterGeometry pins what the bar costs the panel: exactly
// one body row while open, the body origin moves down with it, and the
// chips are dropped whole (never clipped) when the panel is too narrow
// to leave the field a usable width.
func TestGitLogFilterGeometry(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitLog.open = true

	closedRows, closedTop := a.gitLogBodyRows(), a.gitLogBodyTop()
	a.gitLog.filter.open = true
	if got := a.gitLogBodyRows(); got != closedRows-1 {
		t.Errorf("body rows with the bar open = %d, want %d", got, closedRows-1)
	}
	if got := a.gitLogBodyTop(); got != closedTop+1 {
		t.Errorf("body top with the bar open = %d, want %d", got, closedTop+1)
	}
	if a.gitLogFilterY() != closedTop {
		t.Errorf("the bar should occupy the row the body used to start on")
	}

	if _, ok := a.gitLogChipsX(); !ok {
		t.Fatal("chips should fit on a 120-column screen")
	}
	y, start, end := a.gitLogFilterFieldSpan()
	chip := a.gitLogChipRect(0)
	if y != a.gitLogFilterY() || start >= end || end > chip.x {
		t.Errorf("field span (%d,%d..%d) overlaps the chips at %d", y, start, end, chip.x)
	}

	// Squeeze the panel until the chips can no longer coexist with a
	// usable field: they vanish, and every chip rect becomes unhittable.
	a.sidebarWidth = a.width - gitLogMinListW - 6
	if _, ok := a.gitLogChipsX(); ok {
		t.Fatalf("chips should have been dropped on a %d-wide panel", a.width-a.sidebarWidth)
	}
	for i := range gitLogModeChips {
		if r := a.gitLogChipRect(i); r.contains(r.x, r.y) {
			t.Errorf("hidden chip %d is still hittable at %+v", i, r)
		}
	}
}

// TestGitLogFilterKeys walks the keyboard contract: typing edits and
// arms the debounce, Tab cycles the mode through the prefix, Up/Down
// move the commit selection (the panel's only keyboard route into its
// own list), and Esc closes the bar.
func TestGitLogFilterKeys(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitLog.open = true
	a.gitLog.filter.open = true
	a.gitLog.filter.focused = true
	a.gitLog.commits = []gitLogCommit{
		{Hash: "aaaa", Short: "a1"}, {Hash: "bbbb", Short: "b2"},
	}
	t.Cleanup(a.gitLogFilterStopTimer)

	for _, r := range "fix" {
		a.handleGitLogFilterKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	if got := a.gitLog.filter.field.String(); got != "fix" {
		t.Fatalf("typed text = %q, want %q", got, "fix")
	}
	if a.gitLog.filter.timer == nil {
		t.Error("typing did not arm the debounce")
	}

	a.handleGitLogFilterKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	if got := a.gitLog.filter.field.String(); got != "a:fix" {
		t.Errorf("Tab from the message mode gave %q, want a:fix", got)
	}
	if a.gitLogFilterMode() != gitLogModeAuthor {
		t.Errorf("mode after Tab = %d, want author", a.gitLogFilterMode())
	}

	a.handleGitLogFilterKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if a.gitLog.selected != 1 {
		t.Errorf("Down moved selection to %d, want 1", a.gitLog.selected)
	}
	a.handleGitLogFilterKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if a.gitLog.selected != 1 {
		t.Errorf("Down past the end moved selection to %d, want it clamped at 1", a.gitLog.selected)
	}
	a.handleGitLogFilterKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	if a.gitLog.selected != 0 {
		t.Errorf("Up moved selection to %d, want 0", a.gitLog.selected)
	}

	a.handleGitLogFilterKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if a.gitLog.filter.open {
		t.Error("Esc did not close the bar")
	}
}

// TestGitLogFilterPress covers the mouse: the ⌕ header button toggles
// the bar, a chip click switches mode, a click in the field takes focus
// and positions the caret, and a click on the bar never falls through
// to the commit list or starts a divider drag.
func TestGitLogFilterPress(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.gitLog.open = true
	a.gitLog.commits = []gitLogCommit{{Hash: "aaaa", Short: "a1"}}
	t.Cleanup(a.gitLogFilterStopTimer)

	search := a.gitLogSearchRect()
	if got := a.gitLogPress(search.x+1, search.y); got != "" {
		t.Errorf("⌕ press started drag %q", got)
	}
	if !a.gitLog.filter.open || !a.gitLog.filter.focused {
		t.Fatal("⌕ press did not open and focus the bar")
	}

	a.gitLog.filter.field = newTextField("fix")
	chip := a.gitLogChipRect(2) // path
	if got := a.gitLogPress(chip.x+1, chip.y); got != "" {
		t.Errorf("chip press started drag %q", got)
	}
	if got := a.gitLog.filter.field.String(); got != "p:fix" {
		t.Errorf("path chip gave %q, want p:fix", got)
	}

	// A press on the bar's own row must be consumed even where the
	// list/detail divider crosses it.
	a.gitLog.filter.focused = false
	if got := a.gitLogPress(a.gitLogDividerX(), a.gitLogFilterY()); got != "" {
		t.Errorf("press on the divider column of the bar row started drag %q", got)
	}
	if !a.gitLog.filter.focused {
		t.Error("a click on the bar did not take focus")
	}
	if a.gitLog.selected != 0 {
		t.Error("a click on the bar changed the commit selection")
	}

	// Toggling it shut restores the panel.
	a.gitLogPress(search.x+1, search.y)
	if a.gitLog.filter.open {
		t.Error("second ⌕ press did not close the bar")
	}
}

// TestGitLogFilterSeed pins the pickaxe pre-seed: a one-line selection
// becomes an `s:` query (the "when did this line appear" reflex), while
// a multi-line one is refused rather than mangled into a single-line
// field.
func TestGitLogFilterSeed(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.gitLogFilterSeed(); got != "" {
		t.Errorf("no tab should seed nothing, got %q", got)
	}

	path := t.TempDir() + "/seed.txt"
	writeFileT(t, path, "alpha beta\ngamma\n")
	tab, err := editor.NewTab(path)
	if err != nil {
		t.Fatalf("new tab: %v", err)
	}
	a.tabs = append(a.tabs, tab)
	a.activeTab = len(a.tabs) - 1
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 10}
	if got := a.gitLogFilterSeed(); got != "alpha beta" {
		t.Errorf("single-line seed = %q, want %q", got, "alpha beta")
	}
	tab.Cursor = editor.Position{Line: 1, Col: 5}
	if got := a.gitLogFilterSeed(); got != "" {
		t.Errorf("multi-line selection seeded %q, want nothing", got)
	}
}

// TestDrawGitLog_FilterBar renders the panel with the bar open and
// checks what the user actually sees: the ⌕ button, the placeholder
// that teaches the prefix syntax, the mode chips, the match count, and
// the title switching from "commits" to "matches" so a filtered panel
// never misreports the repository.
func TestDrawGitLog_FilterBar(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.gitLog.open = true
	a.gitLog.filter.open = true
	a.gitLog.filter.focused = true
	a.gitLog.filter.applied = "s:magic"
	a.gitLog.commits = []gitLogCommit{
		{Hash: "aaaa", Short: "abc1234", Author: "Alice", Subject: "add magic"},
	}
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()
	cells, w, _ := scr.GetContents()
	rowText := func(y int) string {
		var b strings.Builder
		for x := 0; x < w; x++ {
			if c := cells[y*w+x]; len(c.Runes) > 0 {
				b.WriteRune(c.Runes[0])
			}
		}
		return b.String()
	}

	_, py, _, _ := a.gitLogRect()
	header := rowText(py)
	for _, want := range []string{"⌕ search", "Git log · 1 match"} {
		if !strings.Contains(header, want) {
			t.Errorf("header = %q, missing %q", header, want)
		}
	}
	bar := rowText(a.gitLogFilterY())
	for _, want := range []string{"⌕", "a:author", "msg", "author", "path", "string", "1 match"} {
		if !strings.Contains(bar, want) {
			t.Errorf("filter bar = %q, missing %q", bar, want)
		}
	}
	// The commit row moved down by one and is still whole.
	if body := rowText(a.gitLogBodyTop()); !strings.Contains(body, "abc1234") {
		t.Errorf("first body row = %q, missing the commit", body)
	}

	// An empty result says so, in place of the count.
	a.gitLog.commits = nil
	a.draw()
	scr.Show()
	cells, w, _ = scr.GetContents()
	if bar := rowText(a.gitLogFilterY()); !strings.Contains(bar, "no matches") {
		t.Errorf("empty result bar = %q, missing 'no matches'", bar)
	}
}

// TestLoadGitLogQuery_RealRepo is the one that proves the flags mean
// what the chips say. Three commits by two authors touching two files,
// then every mode is asked a question only the right commits can
// answer — including the pickaxe, which must find BOTH the commit that
// introduced a string and the one that removed it.
func TestLoadGitLogQuery_RealRepo(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not installed")
	}
	dir := initRepo(t)
	writeFileT(t, dir+"/alpha.txt", "alpha\n")
	gitRun(t, dir, "add", "alpha.txt")
	gitRun(t, dir, "commit", "-q", "-m", "feat: add alpha")

	writeFileT(t, dir+"/beta.txt", "beta magic\n")
	gitRun(t, dir, "add", "beta.txt")
	gitRun(t, dir, "-c", "user.name=Bobbie", "-c", "user.email=b@example.com",
		"commit", "-q", "-m", "feat: add beta")

	writeFileT(t, dir+"/beta.txt", "beta plain\n")
	gitRun(t, dir, "add", "beta.txt")
	gitRun(t, dir, "commit", "-q", "-m", "chore: drop magic")

	subjects := func(q gitLogQuery) []string {
		commits, _ := loadGitLogQuery(dir, gitLogFilterArgs(q))
		var out []string
		for _, c := range commits {
			out = append(out, c.Subject)
		}
		return out
	}

	if got := subjects(gitLogQuery{}); len(got) != 3 {
		t.Fatalf("unfiltered log = %v, want all 3 commits", got)
	}
	if got := subjects(gitLogQuery{gitLogModeMessage, "DROP"}); len(got) != 1 ||
		got[0] != "chore: drop magic" {
		t.Errorf("--grep (case-insensitive) = %v, want just the chore commit", got)
	}
	if got := subjects(gitLogQuery{gitLogModeAuthor, "bobbie"}); len(got) != 1 ||
		got[0] != "feat: add beta" {
		t.Errorf("--author (case-insensitive) = %v, want just Bobbie's commit", got)
	}
	if got := subjects(gitLogQuery{gitLogModePath, "alpha.txt"}); len(got) != 1 ||
		got[0] != "feat: add alpha" {
		t.Errorf("path filter = %v, want just the alpha commit", got)
	}
	// The pickaxe finds the appearance AND the disappearance — that
	// pair is the whole reason the mode exists.
	got := subjects(gitLogQuery{gitLogModePickaxe, "magic"})
	if len(got) != 2 {
		t.Fatalf("pickaxe = %v, want the commit that added the string and the one that removed it", got)
	}
	if got[0] != "chore: drop magic" || got[1] != "feat: add beta" {
		t.Errorf("pickaxe = %v, want newest-first [drop, add]", got)
	}
	// A term nothing matches is an empty list, not an error.
	if got := subjects(gitLogQuery{gitLogModePickaxe, "nowhere-string"}); len(got) != 0 {
		t.Errorf("pickaxe for an absent string = %v, want nothing", got)
	}
}

// TestMenuGitLogSearch_OpensPanel pins the "one thought" entry point:
// Esc-S / the ≡ row opens the log panel when it's shut and lands the
// keyboard in the search field, rather than asking the user to open the
// panel first.
func TestMenuGitLogSearch_OpensPanel(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not installed")
	}
	dir := initRepo(t)
	writeFileT(t, dir+"/a.txt", "a\n")
	gitRun(t, dir, "add", "a.txt")
	gitRun(t, dir, "commit", "-q", "-m", "first")

	a := newTestApp(t, dir)
	a.gitIsRepo = true
	t.Cleanup(a.gitLogFilterStopTimer)

	a.menuGitLogSearch()
	if !a.gitLog.open {
		t.Fatal("search did not open the log panel")
	}
	if !a.gitLog.filter.open || !a.gitLog.filter.focused {
		t.Fatal("search did not open and focus the filter bar")
	}
	// And the panel really loaded — the search is over something.
	if len(a.gitLog.commits) == 0 {
		t.Error("the log panel opened empty")
	}
}
