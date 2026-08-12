// =============================================================================
// File: internal/app/gitpush_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the push dialog: the dropdown-construction rule that is the
// whole point of the feature (the current branch is always option zero),
// the upstream splitter, the argv builder, the identity-preserving
// refresh when ls-remote lands, and the keyboard / mouse routing —
// against fixtures where possible, and against a real repo with a real
// local bare remote for the end-to-end path (skipped when git is absent,
// same policy as the other git tests).
//
// pushArgs, not submit, is what the argv tests drive: submit fires a
// real `git push`, and no test in this package may do that.

package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// -----------------------------------------------------------------------------
// The dropdown rule
// -----------------------------------------------------------------------------

// TestGitPushBranchOptions pins the dialog's hard requirement and the
// rest of the ordering contract: the current local branch is option
// zero whether or not the remote has ever heard of it, the tracked
// branch follows when it differs, fetched heads come sorted and
// deduplicated against both, and "other…" is always last.
func TestGitPushBranchOptions(t *testing.T) {
	cases := []struct {
		name    string
		local   string
		tracked string
		heads   []string
		want    []string
	}{
		{
			name:  "brand new branch the remote has never seen",
			local: "feature/x",
			heads: []string{"main", "develop"},
			want:  []string{"feature/x", "develop", "main", gitPushOtherLabel},
		},
		{
			name:    "tracked name matches local — no duplicate row",
			local:   "main",
			tracked: "main",
			heads:   []string{"main", "release/1.2"},
			want:    []string{"main", "release/1.2", gitPushOtherLabel},
		},
		{
			name:    "tracked differs — it sits second, ahead of the heads",
			local:   "main",
			tracked: "upstream-main",
			heads:   []string{"aaa", "upstream-main"},
			want:    []string{"main", "upstream-main", "aaa", gitPushOtherLabel},
		},
		{
			name:  "offline: no heads at all, and the list is still usable",
			local: "main",
			want:  []string{"main", gitPushOtherLabel},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := gitPushBranchOptions(tc.local, tc.tracked, tc.heads)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("options = %v, want %v", got, tc.want)
			}
			if got[0] != tc.local {
				t.Errorf("option 0 = %q, want the current branch %q", got[0], tc.local)
			}
		})
	}
}

// TestSplitUpstream pins the remote/branch split against the real
// ambiguity: both halves can contain slashes, so the separator has to be
// a remote name we know exists rather than the first "/".
func TestSplitUpstream(t *testing.T) {
	remotes := []string{"origin", "origin/mirror", "fork"}
	cases := []struct {
		in           string
		wantR, wantB string
	}{
		{"origin/main", "origin", "main"},
		{"origin/feature/x", "origin", "feature/x"},
		// Longest match wins, or "origin" would eat the mirror's prefix
		// and report a branch of "mirror/main".
		{"origin/mirror/main", "origin/mirror", "main"},
		{"fork/main", "fork", "main"},
		// Unknown remote: fall back to the first slash rather than
		// calling the whole string a branch name.
		{"elsewhere/main", "elsewhere", "main"},
		{"", "", ""},
	}
	for _, tc := range cases {
		r, b := splitUpstream(tc.in, remotes)
		if r != tc.wantR || b != tc.wantB {
			t.Errorf("splitUpstream(%q) = (%q, %q), want (%q, %q)", tc.in, r, b, tc.wantR, tc.wantB)
		}
	}
}

// TestParseLsRemoteHeads pins the ref decode: only refs/heads lines
// count, the prefix is stripped, and junk is skipped rather than
// erroring — the best-effort read contract.
func TestParseLsRemoteHeads(t *testing.T) {
	out := []byte(strings.Join([]string{
		"aaaaaaa\trefs/heads/main",
		"bbbbbbb\trefs/heads/feature/x",
		"ccccccc\trefs/tags/v1.0",
		"not a ref line",
		"",
	}, "\n"))
	got := parseLsRemoteHeads(out)
	if strings.Join(got, "|") != "main|feature/x" {
		t.Errorf("heads = %v, want [main feature/x]", got)
	}
}

// -----------------------------------------------------------------------------
// The command
// -----------------------------------------------------------------------------

// TestGitPushArgs pins the argv for every flag combination. The refspec
// is explicit even when both names match — that is what keeps the
// command independent of the repo's push.default setting.
func TestGitPushArgs(t *testing.T) {
	cases := []struct {
		name           string
		upstream, forc bool
		want           string
	}{
		{"plain", false, false, "push origin main:main"},
		{"set upstream", true, false, "push --set-upstream origin main:main"},
		{"force", false, true, "push --force-with-lease origin main:main"},
		{"both", true, true, "push --set-upstream --force-with-lease origin main:main"},
	}
	for _, tc := range cases {
		m := &gitPushModal{local: "main", setUpstream: tc.upstream, force: tc.forc}
		if got := strings.Join(m.pushArgs("origin", "main"), " "); got != tc.want {
			t.Errorf("%s: args = %q, want %q", tc.name, got, tc.want)
		}
	}
	// A rename push carries both names, and -u still points the local
	// branch at the right-hand side.
	m := &gitPushModal{local: "main", setUpstream: true}
	if got := strings.Join(m.pushArgs("fork", "feature-x"), " "); got != "push --set-upstream fork main:feature-x" {
		t.Errorf("rename push args = %q", got)
	}
	// Nothing here may ever be a bare --force.
	m = &gitPushModal{local: "main", force: true}
	for _, arg := range m.pushArgs("origin", "main") {
		if arg == "--force" || arg == "-f" {
			t.Fatalf("bare force reached argv: %v", m.pushArgs("origin", "main"))
		}
	}
}

// -----------------------------------------------------------------------------
// Header and row text
// -----------------------------------------------------------------------------

// TestGitPushHeaderText pins the rule that the ahead/behind counts only
// appear when the selected target IS the tracked upstream — they were
// measured against that ref and nothing else.
func TestGitPushHeaderText(t *testing.T) {
	newModal := func() *gitPushModal {
		m := &gitPushModal{
			local: "main", upRemote: "origin", upBranch: "main",
			ahead: 3, behind: 1,
			remotes: []string{"origin", "fork"},
		}
		m.rebuildBranches(nil)
		return m
	}

	m := newModal()
	if got := m.headerText(); got != "main → origin/main (ahead 3, behind 1)" {
		t.Errorf("tracked header = %q", got)
	}

	// Switch to the other remote: same branch name, but nothing measured
	// the distance to it, so the counts must go.
	m = newModal()
	m.remoteIdx = 1
	m.rebuildBranches(nil)
	if got := m.headerText(); got != "main → fork/main" {
		t.Errorf("untracked-remote header = %q", got)
	}

	// A level branch says so rather than showing a bare arrow.
	m = newModal()
	m.ahead, m.behind = 0, 0
	if got := m.headerText(); got != "main → origin/main (up to date)" {
		t.Errorf("level header = %q", got)
	}

	// "other…" with nothing typed yet renders a placeholder, not a
	// sentence that ends in a bare slash.
	m = newModal()
	m.branchIdx = len(m.branches) - 1
	if got := m.headerText(); got != "main → origin/…" {
		t.Errorf("empty-other header = %q", got)
	}
}

// TestGitPushSubmitLabel pins the loudest force signal: the button says
// what it will do.
func TestGitPushSubmitLabel(t *testing.T) {
	m := &gitPushModal{}
	if m.submitLabel() != "[ Push ]" {
		t.Errorf("unforced label = %q", m.submitLabel())
	}
	m.force = true
	if m.submitLabel() != "[ Force Push ]" {
		t.Errorf("forced label = %q", m.submitLabel())
	}
}

// TestGitPushSubmitButtonRightAnchored pins the geometry rule behind the
// relabel: growing the button must not slide it out from under a pointer
// already on its way to click it.
func TestGitPushSubmitButtonRightAnchored(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := &gitPushModal{}
	_, plain := m.buttons(a)
	m.force = true
	_, forced := m.buttons(a)
	if plain.x+plain.w != forced.x+forced.w {
		t.Errorf("right edge moved: %d → %d", plain.x+plain.w, forced.x+forced.w)
	}
	if forced.w <= plain.w {
		t.Errorf("forced button should be wider: %d vs %d", forced.w, plain.w)
	}
}

// -----------------------------------------------------------------------------
// The async refill
// -----------------------------------------------------------------------------

// TestGitPushRebuildKeepsSelectionByName is the identity rule: heads
// arrive from a goroutine and land in the MIDDLE of the list, so an
// index-preserving refresh would silently move the selection onto a
// different branch.
func TestGitPushRebuildKeepsSelectionByName(t *testing.T) {
	m := &gitPushModal{local: "zeta", remotes: []string{"origin"}}
	m.rebuildBranches(nil)
	// Options are [zeta, other…]; pick the escape hatch.
	m.branchIdx = 1
	if m.selectedOption() != gitPushOtherLabel {
		t.Fatalf("setup: selected %q", m.selectedOption())
	}
	// Four heads sort ahead of "other…" and after "zeta".
	m.rebuildBranches([]string{"alpha", "beta", "gamma", "delta"})
	if got := m.selectedOption(); got != gitPushOtherLabel {
		t.Errorf("selection moved to %q after refill; want %q", got, gitPushOtherLabel)
	}
	if m.branches[0] != "zeta" {
		t.Errorf("current branch lost its seat at index 0: %v", m.branches)
	}
}

// TestHandleGitPushRefs pins both staleness guards. A late answer for a
// remote the user has switched away from describes a different server
// and must not repopulate the dropdown; an answer that arrives after the
// dialog closed must not panic on a nil / mismatched modal.
func TestHandleGitPushRefs(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	// No modal at all.
	a.handleGitPushRefs(&gitPushRefsEvent{remote: "origin", heads: []string{"main"}})

	m := &gitPushModal{local: "main", remotes: []string{"origin", "fork"}, loading: true}
	m.rebuildBranches(nil)
	a.modal = m

	// Wrong remote — dropped, spinner still up.
	a.handleGitPushRefs(&gitPushRefsEvent{remote: "fork", heads: []string{"stale"}})
	if !m.loading {
		t.Error("stale answer cleared the loading flag")
	}
	if len(m.branches) != 2 {
		t.Errorf("stale answer landed in the dropdown: %v", m.branches)
	}

	// Right remote — merged.
	a.handleGitPushRefs(&gitPushRefsEvent{remote: "origin", heads: []string{"develop"}})
	if m.loading {
		t.Error("loading flag survived the answer")
	}
	if indexOfString(m.branches, "develop") < 0 {
		t.Errorf("head missing after merge: %v", m.branches)
	}
}

// TestGitPushRemoteSwitchDropsHeads pins that the previous remote's
// branches are dropped, not kept until the new listing lands: offering
// branches that don't exist where you're pushing is worse than offering
// fewer. Uses the single-remote early return to keep git off the wire —
// the drop itself is rebuildBranches(nil), tested directly.
func TestGitPushRemoteSwitchDropsHeads(t *testing.T) {
	m := &gitPushModal{local: "main", remotes: []string{"origin", "fork"}}
	m.rebuildBranches([]string{"develop", "release"})
	if len(m.branches) != 4 {
		t.Fatalf("setup: %v", m.branches)
	}
	m.remoteIdx = 1
	m.rebuildBranches(nil)
	if indexOfString(m.branches, "develop") >= 0 {
		t.Errorf("old remote's heads survived the switch: %v", m.branches)
	}
	if m.selectedOption() != "main" {
		t.Errorf("selection = %q, want the current branch", m.selectedOption())
	}
}

// -----------------------------------------------------------------------------
// Keyboard
// -----------------------------------------------------------------------------

// key builds a bare key event for the routing tests.
func pushKey(k tcell.Key) *tcell.EventKey { return tcell.NewEventKey(k, 0, tcell.ModNone) }

// TestGitPushKeyAxes pins this file's one deviation from formModal: the
// vertical axis moves between rows and the horizontal axis changes the
// focused row's value, because the branch row can become a text field
// that owns Left/Right for its caret.
func TestGitPushKeyAxes(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := &gitPushModal{local: "main", remotes: []string{"origin"}}
	m.rebuildBranches([]string{"develop"})
	a.modal = m

	m.handleKey(a, pushKey(tcell.KeyDown))
	if m.focus != pushRowBranch {
		t.Errorf("Down should move rows, focus = %d", m.focus)
	}
	m.handleKey(a, pushKey(tcell.KeyRight))
	if m.focus != pushRowBranch {
		t.Errorf("Right should not move rows, focus = %d", m.focus)
	}
	if m.selectedOption() != "develop" {
		t.Errorf("Right should cycle the value, selected = %q", m.selectedOption())
	}

	// Space toggles a checkbox; the two boxes are independent.
	m.focus = pushRowForce
	m.handleKey(a, tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone))
	if !m.force {
		t.Error("Space did not tick Force with lease")
	}
	if m.setUpstream {
		t.Error("Space on Force also ticked Set upstream")
	}

	// Focus wraps rather than sticking at the end — every list in the
	// editor behaves this way.
	m.focus = pushRowCount - 1
	m.moveFocus(+1)
	if m.focus != 0 {
		t.Errorf("focus did not wrap, = %d", m.focus)
	}

	// Esc closes.
	m.handleKey(a, pushKey(tcell.KeyEsc))
	if a.modal != nil {
		t.Error("Esc left the dialog open")
	}
}

// TestGitPushOtherModeOwnsTyping pins the text-field takeover: once
// "other…" is selected, printable runes edit the name instead of doing
// nothing, and the resolved target follows what was typed.
func TestGitPushOtherModeOwnsTyping(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := &gitPushModal{local: "main", remotes: []string{"origin"}}
	m.rebuildBranches(nil)
	a.modal = m

	m.focus = pushRowBranch
	m.handleKey(a, pushKey(tcell.KeyRight)) // main → other…
	if !m.otherActive() {
		t.Fatalf("selected %q, want %q", m.selectedOption(), gitPushOtherLabel)
	}
	// Selecting the escape hatch seeds it with the local name so the
	// common "same name plus a suffix" edit starts from something.
	if m.targetBranch() != "main" {
		t.Errorf("seeded target = %q, want main", m.targetBranch())
	}
	for _, r := range "-v2" {
		m.handleKey(a, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	if m.targetBranch() != "main-v2" {
		t.Errorf("typed target = %q", m.targetBranch())
	}
}

// TestGitPushSubmitRefusesEmptyName pins that an unfilled "other…"
// keeps the dialog OPEN and points at the row that needs filling — the
// user has three other answers in here they'd have to re-enter.
func TestGitPushSubmitRefusesEmptyName(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := &gitPushModal{local: "main", remotes: []string{"origin"}}
	m.rebuildBranches(nil)
	m.branchIdx = len(m.branches) - 1 // other…, empty field
	m.focus = pushRowForce
	a.modal = m

	m.submit(a)
	if a.modal != m {
		t.Error("empty submit closed the dialog")
	}
	if m.focus != pushRowBranch {
		t.Errorf("focus = %d, want the branch row", m.focus)
	}
}

// -----------------------------------------------------------------------------
// Mouse
// -----------------------------------------------------------------------------

// TestGitPushMouse pins the click targets: a chevron cycles, a checkbox
// row toggles anywhere along its line (a 3-cell bracket is a cruel thing
// to ask a mouse for), and a click outside dismisses.
func TestGitPushMouse(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := &gitPushModal{local: "main", remotes: []string{"origin"}}
	m.rebuildBranches([]string{"develop"})
	a.modal = m

	_, inputRow, fieldStart, fieldEnd := m.rowSpan(a, pushRowBranch)
	m.handleMouse(a, fieldEnd-1, inputRow, tcell.Button1)
	if m.selectedOption() != "develop" {
		t.Errorf("› chevron: selected %q", m.selectedOption())
	}
	m.handleMouse(a, fieldStart, inputRow, tcell.Button1)
	if m.selectedOption() != "main" {
		t.Errorf("‹ chevron: selected %q", m.selectedOption())
	}

	// Mid-label click on the Force row — nowhere near a bracket.
	_, forceRow, _, _ := m.rowSpan(a, pushRowForce)
	m.handleMouse(a, fieldStart+8, forceRow, tcell.Button1)
	if !m.force {
		t.Error("click on the Force row's label did not toggle it")
	}
	if m.focus != pushRowForce {
		t.Errorf("click did not move focus, = %d", m.focus)
	}

	mx, my, _, _ := m.rect(a)
	if mx > 0 && my > 0 {
		m.handleMouse(a, mx-1, my-1, tcell.Button1)
		if a.modal != nil {
			t.Error("click outside did not dismiss")
		}
	}
}

// -----------------------------------------------------------------------------
// The status-bar indicator
// -----------------------------------------------------------------------------

// TestGitPushStatusSuffix pins what the bar shows. The bare arrow for an
// untracked branch is the case a count-only indicator would stay silent
// for — and it is exactly the moment the dialog exists to serve.
func TestGitPushStatusSuffix(t *testing.T) {
	cases := []struct {
		name          string
		hasRemote     bool
		upstream      string
		ahead, behind int
		want          string
	}{
		{"no remote at all", false, "", 0, 0, ""},
		{"never pushed", true, "", 0, 0, " ↑"},
		{"level", true, "origin/main", 0, 0, ""},
		{"ahead", true, "origin/main", 3, 0, " ↑3"},
		{"behind", true, "origin/main", 0, 2, " ↓2"},
		{"diverged", true, "origin/main", 3, 2, " ↑3↓2"},
	}
	for _, tc := range cases {
		a := &App{gitHasRemote: tc.hasRemote, gitUpstream: tc.upstream,
			gitAhead: tc.ahead, gitBehind: tc.behind}
		if got := a.gitPushStatusSuffix(); got != tc.want {
			t.Errorf("%s: suffix = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestStatusBarPushSegment pins that the indicator is a real click
// target next to the branch, and that the branch keeps its own verb —
// neither segment steals the other's click.
func TestStatusBarPushSegment(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitBranch = "main"
	a.gitHasRemote = true
	a.gitUpstream = "origin/main"
	a.gitAhead = 3
	a.drawStatusBar()

	var branch, arrow *statusSegment
	for i := range a.statusSegs {
		switch a.statusSegs[i].text {
		case " main":
			branch = &a.statusSegs[i]
		case " ↑3":
			arrow = &a.statusSegs[i]
		}
	}
	if branch == nil {
		t.Fatal("branch segment missing")
	}
	if arrow == nil {
		t.Fatal("push indicator missing from the status bar")
	}
	if arrow.rect.x < branch.rect.x {
		t.Error("push indicator drew to the left of the branch it annotates")
	}
	if arrow.onClick == nil {
		t.Error("push indicator is not clickable")
	}
}

// -----------------------------------------------------------------------------
// End to end, against a real repo and a real (local, bare) remote
// -----------------------------------------------------------------------------

// initRepoWithRemote builds a repo on branch "main" with one commit and
// a bare remote wired up as "origin", pushed and tracking. Returns the
// work tree. A local bare repo exercises every code path a network
// remote would — ls-remote, upstream resolution, ahead/behind — without
// touching the network.
func initRepoWithRemote(t *testing.T) string {
	t.Helper()
	dir := initRepo(t)
	bare := filepath.Join(t.TempDir(), "remote.git")
	gitRun(t, filepath.Dir(bare), "init", "--bare", "-q", bare)

	writeFileT(t, filepath.Join(dir, "f.txt"), "one\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "seed")
	gitRun(t, dir, "remote", "add", "origin", bare)
	gitRun(t, dir, "push", "-q", "-u", "origin", "main")
	return dir
}

// TestLoadGitTracking_EndToEnd exercises the real upstream read: a
// freshly-pushed branch is level, a new local commit puts it one ahead,
// and the column order (behind, then ahead) is the thing easiest to get
// backwards.
func TestLoadGitTracking_EndToEnd(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	dir := initRepoWithRemote(t)

	up, ahead, behind := loadGitTracking(dir)
	if up != "origin/main" {
		t.Errorf("upstream = %q, want origin/main", up)
	}
	if ahead != 0 || behind != 0 {
		t.Errorf("fresh push: ahead=%d behind=%d, want 0/0", ahead, behind)
	}

	writeFileT(t, filepath.Join(dir, "f.txt"), "two\n")
	gitRun(t, dir, "commit", "-q", "-am", "second")
	if _, ahead, behind = loadGitTracking(dir); ahead != 1 || behind != 0 {
		t.Errorf("after one local commit: ahead=%d behind=%d, want 1/0", ahead, behind)
	}

	// A branch with no upstream reports nothing rather than guessing.
	gitRun(t, dir, "checkout", "-q", "-b", "orphan")
	if up, ahead, behind = loadGitTracking(dir); up != "" || ahead != 0 || behind != 0 {
		t.Errorf("untracked branch = (%q, %d, %d), want empty", up, ahead, behind)
	}
}

// TestParseTrackingCounts pins the column order without forking git.
func TestParseTrackingCounts(t *testing.T) {
	ahead, behind := parseTrackingCounts([]byte("2\t5\n"))
	if ahead != 5 || behind != 2 {
		t.Errorf("got ahead=%d behind=%d, want ahead=5 behind=2 (left is behind)", ahead, behind)
	}
	if a, b := parseTrackingCounts([]byte("garbage")); a != 0 || b != 0 {
		t.Errorf("malformed output should degrade to 0/0, got %d/%d", a, b)
	}
}

// TestLoadGitStatusTracking pins that the snapshot carries the tracking
// facts the status bar and the dialog both read from fields.
func TestLoadGitStatusTracking(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	dir := initRepoWithRemote(t)
	st := loadGitStatus(dir)
	if st.Upstream != "origin/main" || !st.HasRemote {
		t.Errorf("status tracking = (%q, hasRemote=%v)", st.Upstream, st.HasRemote)
	}

	// A repo with no remote at all: HasRemote is what dims the push row,
	// and it must be false here even though everything else is fine.
	plain := initRepo(t)
	writeFileT(t, filepath.Join(plain, "a.txt"), "x\n")
	gitRun(t, plain, "add", ".")
	gitRun(t, plain, "commit", "-q", "-m", "seed")
	if st := loadGitStatus(plain); st.HasRemote || st.Upstream != "" {
		t.Errorf("remote-less repo reported hasRemote=%v upstream=%q", st.HasRemote, st.Upstream)
	}
}

// TestLoadGitRemotes pins the remote listing and its best-effort
// contract on a non-repo.
func TestLoadGitRemotes(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	dir := initRepoWithRemote(t)
	if got := loadGitRemotes(dir); len(got) != 1 || got[0] != "origin" {
		t.Errorf("remotes = %v, want [origin]", got)
	}
	if got := loadGitRemotes(t.TempDir()); got != nil {
		t.Errorf("non-repo remotes = %v, want nil", got)
	}
	if got := loadGitRemotes(""); got != nil {
		t.Errorf("empty root remotes = %v, want nil", got)
	}
}

// TestGitCurrentBranch pins the difference from loadGitBranch: a
// detached HEAD has no branch, and must NOT come back as a short SHA
// that would sail through the dialog as if it were a branch name.
func TestGitCurrentBranch(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	dir := initRepoWithRemote(t)
	if got := gitCurrentBranch(dir); got != "main" {
		t.Errorf("branch = %q, want main", got)
	}
	gitRun(t, dir, "checkout", "-q", "--detach", "HEAD")
	if got := gitCurrentBranch(dir); got != "" {
		t.Errorf("detached HEAD reported branch %q, want empty", got)
	}
	if got := loadGitBranch(dir); got == "" {
		t.Error("loadGitBranch should still report a SHA when detached — that is the difference being pinned")
	}
}

// TestOpenGitPush_EndToEnd raises the real dialog against a real repo:
// the defaults land on the tracked target, Set upstream stays OFF
// because one already exists, and the ls-remote goroutine's answer
// merges in without moving the selection.
func TestOpenGitPush_EndToEnd(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	dir := initRepoWithRemote(t)
	a := newTestApp(t, dir)
	a.refreshGitStatus()

	if !a.hasGitPushTarget() {
		t.Fatal("push should be offered in a repo with a remote")
	}
	a.openGitPush()
	m, ok := a.modal.(*gitPushModal)
	if !ok {
		t.Fatalf("modal = %T, want *gitPushModal", a.modal)
	}
	if m.local != "main" || m.currentRemote() != "origin" || m.targetBranch() != "main" {
		t.Errorf("defaults = %s → %s/%s", m.local, m.currentRemote(), m.targetBranch())
	}
	if m.setUpstream {
		t.Error("Set upstream pre-checked despite an existing upstream")
	}
	if m.branches[0] != "main" {
		t.Errorf("option 0 = %q, want the current branch", m.branches[0])
	}

	// The head listing is a local bare repo, so it lands immediately;
	// pull it off the sim screen's queue and route it as the loop would.
	drainPushRefs(t, a)
	if m.loading {
		t.Error("loading flag never cleared")
	}
	if m.targetBranch() != "main" {
		t.Errorf("selection moved to %q when the heads landed", m.targetBranch())
	}

	// A brand-new branch: no upstream, so Set upstream arrives ticked
	// and the branch is option zero even though the remote has never
	// heard of it. This is the case the whole dialog exists for.
	gitRun(t, dir, "checkout", "-q", "-b", "feature/x")
	a.refreshGitStatus()
	a.openGitPush()
	m, ok = a.modal.(*gitPushModal)
	if !ok {
		t.Fatalf("modal = %T", a.modal)
	}
	if m.branches[0] != "feature/x" {
		t.Errorf("unpushed branch not offered first: %v", m.branches)
	}
	if !m.setUpstream {
		t.Error("Set upstream should be pre-checked for an untracked branch")
	}
	drainPushRefs(t, a)
	if m.branches[0] != "feature/x" {
		t.Errorf("unpushed branch lost its seat once the heads landed: %v", m.branches)
	}
	if strings.Join(m.pushArgs(m.currentRemote(), m.targetBranch()), " ") !=
		"push --set-upstream origin feature/x:feature/x" {
		t.Errorf("argv = %v", m.pushArgs(m.currentRemote(), m.targetBranch()))
	}
}

// drainPushRefs pumps the simulation screen until the pending
// gitPushRefsEvent has been routed, standing in for the main loop. It
// gives up rather than hanging if the goroutine never posts — a failing
// assertion downstream is a better signal than a stuck test.
func drainPushRefs(t *testing.T, a *App) {
	t.Helper()
	for i := 0; i < 50; i++ {
		ev := a.screen.PollEvent()
		if ev == nil {
			return
		}
		if e, ok := ev.(*gitPushRefsEvent); ok {
			a.handleGitPushRefs(e)
			return
		}
	}
}

// TestOpenGitPushRefusals pins the three "nothing to offer" cases: each
// says which one it is instead of opening a broken form.
func TestOpenGitPushRefusals(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}

	// Not a repo.
	a := newTestApp(t, t.TempDir())
	a.refreshGitStatus()
	a.openGitPush()
	if a.modal != nil {
		t.Error("dialog opened outside a repo")
	}

	// A repo with commits but no remote.
	plain := initRepo(t)
	writeFileT(t, filepath.Join(plain, "a.txt"), "x\n")
	gitRun(t, plain, "add", ".")
	gitRun(t, plain, "commit", "-q", "-m", "seed")
	a = newTestApp(t, plain)
	a.refreshGitStatus()
	if a.hasGitPushTarget() {
		t.Error("push offered in a repo with no remote")
	}
	a.openGitPush()
	if a.modal != nil {
		t.Error("dialog opened with no remotes")
	}

	// Detached HEAD — a repo with a remote, but no current branch to
	// default to, which is this dialog's whole premise.
	dir := initRepoWithRemote(t)
	gitRun(t, dir, "checkout", "-q", "--detach", "HEAD")
	a = newTestApp(t, dir)
	a.refreshGitStatus()
	a.openGitPush()
	if a.modal != nil {
		t.Error("dialog opened on a detached HEAD")
	}
}

// TestGitLogPushButtonPlacement pins that the new header button sits
// between Actions and the right-anchored trio, and that adding it left
// that trio's geometry alone.
func TestGitLogPushButtonPlacement(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitLog.open = true
	act := a.gitLogActionsRect()
	push := a.gitLogPushRect()
	copyB := a.gitLogCopyRect()
	if push.x != act.x+act.w {
		t.Errorf("push at %d, want immediately after Actions (%d)", push.x, act.x+act.w)
	}
	if push.x+push.w > copyB.x {
		t.Errorf("push button overlaps the ⧉ hash button (%d > %d)", push.x+push.w, copyB.x)
	}
	if push.y != act.y {
		t.Error("push button left the header row")
	}
}

// TestGitPushHeaderFit pins the truncation priority. Two long branch
// names overflow the modal easily, and a plain tail-clip eats the
// ahead/behind counts — the only fact on that line no other row
// restates. The local name goes first instead.
func TestGitPushHeaderFit(t *testing.T) {
	m := &gitPushModal{
		local: "feature/long-branch-name", upRemote: "origin",
		upBranch: "feature/long-branch-name", ahead: 3, behind: 1,
		remotes: []string{"origin"},
	}
	m.rebuildBranches(nil)

	// Wide enough: nothing is dropped.
	full := m.headerText()
	if got := m.headerFit(runeLen(full)); got != full {
		t.Errorf("fitting header was altered: %q", got)
	}

	// The real modal width. The counts must survive.
	got := m.headerFit(gitPushModalWidth - 4)
	if !strings.Contains(got, "ahead 3, behind 1") {
		t.Errorf("counts truncated away at modal width: %q", got)
	}
	if !strings.Contains(got, "origin/feature/long-branch-name") {
		t.Errorf("target ref lost: %q", got)
	}
	if strings.HasPrefix(got, m.local) {
		t.Errorf("local name kept at the counts' expense: %q", got)
	}
	if runeLen(got) > gitPushModalWidth-4 {
		t.Errorf("header overflows: %d cells", runeLen(got))
	}

	// Absurdly narrow: it clips rather than returning something wider
	// than it was asked for.
	if got := m.headerFit(10); runeLen(got) > 10 {
		t.Errorf("narrow header = %q (%d cells)", got, runeLen(got))
	}
}

// TestGitPushOtherModeEscape pins the way BACK out of "other…" on both
// input paths. Without it a user who reaches the escape hatch — by a
// stray arrow key or a mis-aimed click — is stuck in a text field until
// they cancel the whole dialog and start over.
func TestGitPushOtherModeEscape(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	enterOther := func() *gitPushModal {
		m := &gitPushModal{local: "main", remotes: []string{"origin"}}
		m.rebuildBranches(nil)
		m.branchIdx = len(m.branches) - 1
		m.other = newTextField("main-v2")
		m.focus = pushRowBranch
		a.modal = m
		return m
	}

	// Left with the caret at 0 — the arrow has nowhere to go, so it
	// steps the option instead.
	m := enterOther()
	m.other.cursor = 0
	m.handleKey(a, pushKey(tcell.KeyLeft))
	if m.otherActive() {
		t.Error("Left at the start of the field did not leave other… mode")
	}

	// Right with the caret at the end wraps forward to option zero.
	m = enterOther()
	m.other.cursor = len(m.other.value)
	m.handleKey(a, pushKey(tcell.KeyRight))
	if m.otherActive() {
		t.Error("Right at the end of the field did not leave other… mode")
	}

	// Mid-value, the arrows still belong to the caret.
	m = enterOther()
	m.other.cursor = 3
	m.handleKey(a, pushKey(tcell.KeyLeft))
	if !m.otherActive() {
		t.Error("Left mid-value left other… mode instead of moving the caret")
	}
	if m.other.cursor != 2 {
		t.Errorf("caret = %d, want 2", m.other.cursor)
	}

	// And the mouse has the same exit: the chevrons stay drawn.
	m = enterOther()
	_, inputRow, fieldStart, _ := m.rowSpan(a, pushRowBranch)
	m.handleMouse(a, fieldStart, inputRow, tcell.Button1)
	if m.otherActive() {
		t.Error("‹ chevron click did not leave other… mode")
	}

	// A click inside the field still places the caret rather than
	// escaping — the chevrons own exactly two cells.
	m = enterOther()
	ts, _ := gitPushOtherSpan(fieldStart, 0)
	m.handleMouse(a, ts+2, inputRow, tcell.Button1)
	if !m.otherActive() {
		t.Error("click inside the field left other… mode")
	}
	if m.other.cursor != 2 {
		t.Errorf("click placed caret at %d, want 2", m.other.cursor)
	}
}
