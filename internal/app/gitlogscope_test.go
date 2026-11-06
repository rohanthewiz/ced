// =============================================================================
// File: internal/app/gitlogscope_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-04
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// scopeRepo builds main (two commits) plus a side branch with one
// commit of its own, checked back out on main — the smallest repo where
// "current branch" and "all branches" give different lists.
func scopeRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "a.txt", "a\n", "main one")
	writeCommit(t, repo, "b.txt", "b\n", "main two")
	gitRun(t, repo, "checkout", "-q", "-b", "side")
	writeCommit(t, repo, "s.txt", "s\n", "side work")
	gitRun(t, repo, "checkout", "-q", "main")
	return repo
}

// logSubjects is the loaded list's subjects, newest first.
func logSubjects(commits []gitLogCommit) []string {
	var out []string
	for _, c := range commits {
		out = append(out, c.Subject)
	}
	return out
}

// TestGitLogScopeRevs pins each scope's revision argument: nothing for
// HEAD (git's default walk), --all, or the refname verbatim.
func TestGitLogScopeRevs(t *testing.T) {
	if got := gitLogScopeRevs(gitLogScopeHead); got != nil {
		t.Errorf("HEAD scope revs = %v, want none", got)
	}
	if got := gitLogScopeRevs(gitLogScopeAll); !equalStrings(got, []string{"--all"}) {
		t.Errorf("all scope revs = %v, want [--all]", got)
	}
	if got := gitLogScopeRevs("refs/heads/feat"); !equalStrings(got, []string{"refs/heads/feat"}) {
		t.Errorf("ref scope revs = %v, want the refname", got)
	}
}

// TestGitLogFilterArgs_ScopeRidesBeforeTheFence pins where the scope's
// revision lands: git reads revisions only BEFORE `--`, so under path
// mode the ref must precede the fence or git would take it for a path.
func TestGitLogFilterArgs_ScopeRidesBeforeTheFence(t *testing.T) {
	args := gitLogFilterArgs(gitLogQuery{gitLogModePath, "a.go"}, "refs/heads/feat")
	tail := args[len(args)-4:]
	want := []string{"refs/heads/feat", "--follow", "--", "a.go"}
	if !equalStrings(tail, want) {
		t.Fatalf("args tail = %v, want %v (full %v)", tail, want, args)
	}
	// The default scope adds nothing at all: `git log` walks HEAD.
	for _, a := range gitLogFilterArgs(gitLogQuery{}, gitLogScopeHead) {
		if a == "--all" || strings.HasPrefix(a, "refs/") {
			t.Fatalf("HEAD scope leaked a revision: %v", a)
		}
	}
}

// TestGitLogFilterArgs_CommitModeIgnoresScope pins the `c:` exception:
// a branch scope added to the rev list would bury the named commit in
// that branch's history, and a commit off the branch must stay findable.
func TestGitLogFilterArgs_CommitModeIgnoresScope(t *testing.T) {
	got := gitLogFilterArgs(gitLogQuery{gitLogModeCommit, "abc123"}, "refs/heads/feat")
	for _, a := range got {
		if a == "refs/heads/feat" || a == "--all" {
			t.Fatalf("commit mode carried the scope: %v", got)
		}
	}
	if got[len(got)-2] != "abc123" || got[len(got)-1] != "--" {
		t.Fatalf("commit mode args = %v, want the rev fenced at the end", got)
	}
}

// TestGitLogRefShort covers both namespaces and the pass-through.
func TestGitLogRefShort(t *testing.T) {
	cases := map[string]string{
		"refs/heads/feat/x":       "feat/x",
		"refs/remotes/origin/dev": "origin/dev",
		"refs/tags/v1":            "refs/tags/v1",
	}
	for in, want := range cases {
		if got := gitLogRefShort(in); got != want {
			t.Errorf("gitLogRefShort(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestParseGitLogRefs pins the picker's list: locals before remotes
// whatever order git printed, symbolic refs (origin/HEAD) dropped, and
// blank or foreign lines ignored.
func TestParseGitLogRefs(t *testing.T) {
	out := []byte("refs/remotes/origin/HEAD\trefs/remotes/origin/main\n" +
		"refs/remotes/origin/main\t\n" +
		"refs/heads/main\t\n" +
		"refs/heads/feat\t\n" +
		"\n" +
		"refs/tags/v1\t\n")
	refs := parseGitLogRefs(out)
	var got []string
	for _, r := range refs {
		got = append(got, r.short)
	}
	want := []string{"main", "feat", "origin/main"}
	if !equalStrings(got, want) {
		t.Fatalf("refs = %v, want %v", got, want)
	}
	if refs[0].remote || !refs[2].remote {
		t.Errorf("remote flags wrong: %+v", refs)
	}
}

// TestLoadGitLogCommits_DefaultsToCurrentBranch is the headline: the
// default scope lists only HEAD's history, --all adds the other
// branch's commit, and a named branch lists that branch.
func TestLoadGitLogCommits_DefaultsToCurrentBranch(t *testing.T) {
	repo := scopeRepo(t)

	head, _ := loadGitLogCommits(repo, gitLogScopeHead)
	if got := logSubjects(head); !equalStrings(got, []string{"main two", "main one"}) {
		t.Errorf("current-branch log = %v, want main's two commits only", got)
	}
	all, _ := loadGitLogCommits(repo, gitLogScopeAll)
	if len(all) != 3 {
		t.Errorf("all-branches log = %v, want 3 commits", logSubjects(all))
	}
	side, _ := loadGitLogCommits(repo, "refs/heads/side")
	if len(side) != 3 || side[0].Subject != "side work" {
		t.Errorf("side log = %v, want side work on top of main's", logSubjects(side))
	}
	if got, _ := loadGitLogCommits(repo, "refs/heads/nope"); got != nil {
		t.Errorf("missing branch should load nil, got %v", logSubjects(got))
	}
}

// TestGitLogScopeItems pins the picker rows: Current and All lead, the
// checked-out branch is not repeated, the viewed row says so, and
// "Check out" appears only for a local branch you are not on.
func TestGitLogScopeItems(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitBranch = "main"
	refs := []gitLogRef{
		{full: "refs/heads/main", short: "main", upstream: "refs/remotes/origin/main"},
		{full: "refs/heads/feat", short: "feat"},
		{full: "refs/remotes/origin/main", short: "origin/main", remote: true},
	}
	labels := func() []string {
		var out []string
		for _, it := range a.gitLogScopeItems(refs) {
			out = append(out, it.label)
		}
		return out
	}

	want := []string{"Current branch (main) (viewing)", "All branches", "feat", "origin/main"}
	if got := labels(); !equalStrings(got, want) {
		t.Errorf("default rows = %v, want %v", got, want)
	}

	a.gitLog.scope = "refs/heads/feat"
	want = []string{"Check out feat", "Current branch (main)", "All branches", "feat (viewing)", "origin/main"}
	if got := labels(); !equalStrings(got, want) {
		t.Errorf("viewing feat rows = %v, want %v", got, want)
	}

	// A remote branch whose local copy is the checked-out branch gets no
	// Check out row: there is nothing to switch to.
	a.gitLog.scope = "refs/remotes/origin/main"
	if got := labels(); strings.HasPrefix(got[0], "Check out") {
		t.Errorf("remote scope tracked by the current branch offered %q", got[0])
	}
}

// TestParseGitLogRefs_ReadsUpstream pins the third field: a local
// branch carries its upstream refname, a remote one never does, and a
// two-field line (no upstream column) reads as tracking nothing.
func TestParseGitLogRefs_ReadsUpstream(t *testing.T) {
	out := []byte("refs/heads/feat\t\trefs/remotes/origin/feat\n" +
		"refs/heads/solo\t\n" +
		"refs/remotes/origin/feat\t\trefs/remotes/origin/x\n")
	refs := parseGitLogRefs(out)
	if len(refs) != 3 {
		t.Fatalf("refs = %+v, want 3", refs)
	}
	if refs[0].upstream != "refs/remotes/origin/feat" {
		t.Errorf("feat upstream = %q", refs[0].upstream)
	}
	if refs[1].upstream != "" || refs[2].upstream != "" {
		t.Errorf("solo / remote upstreams = %q / %q, want none", refs[1].upstream, refs[2].upstream)
	}
}

// TestGitLogScopeRemoteCheckout_Cases walks the three answers for a
// viewed remote branch: switch to the local branch already tracking it
// (preferring the same name), create a local one when the name is free,
// and ask for a name when an unrelated local owns it. Local scopes and
// a nameless remote ref get no row.
func TestGitLogScopeRemoteCheckout_Cases(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitBranch = "main"
	remote := gitLogRef{full: "refs/remotes/origin/feat", short: "origin/feat", remote: true}
	label := func(refs ...gitLogRef) string {
		it, ok := a.gitLogScopeRemoteCheckout(refs)
		if !ok {
			return ""
		}
		return it.label
	}
	a.gitLog.scope = remote.full

	// Free name → one-click create.
	if got := label(gitLogRef{full: "refs/heads/main", short: "main"}, remote); got != "Check out as local feat" {
		t.Errorf("free name row = %q", got)
	}
	// A differently-named tracker wins over creating a second copy.
	mine := gitLogRef{full: "refs/heads/my-feat", short: "my-feat", upstream: remote.full}
	if got := label(mine, remote); got != "Check out my-feat (tracks origin/feat)" {
		t.Errorf("tracker row = %q", got)
	}
	// Two trackers → the one named like the remote branch.
	same := gitLogRef{full: "refs/heads/feat", short: "feat", upstream: remote.full}
	if got := label(mine, same, remote); got != "Check out feat (tracks origin/feat)" {
		t.Errorf("two-tracker row = %q, want the same-named one", got)
	}
	// Already on the tracker → nothing to offer.
	a.gitBranch = "feat"
	if got := label(same, remote); got != "" {
		t.Errorf("on the tracker, row = %q, want none", got)
	}
	a.gitBranch = "main"
	// The name belongs to an unrelated branch → ask.
	other := gitLogRef{full: "refs/heads/feat", short: "feat", upstream: "refs/remotes/fork/feat"}
	if got := label(other, remote); got != "Check out as local branch…" {
		t.Errorf("collision row = %q", got)
	}
	// Not a remote scope, or a remote ref with no branch part → no row.
	a.gitLog.scope = "refs/heads/feat"
	if got := label(remote); got != "" {
		t.Errorf("local scope got remote row %q", got)
	}
	a.gitLog.scope = "refs/remotes/stray"
	if got := label(); got != "" {
		t.Errorf("nameless remote ref got row %q", got)
	}
}

// TestGitLogRemoteLocalName strips the remote and keeps slashes in the
// branch part; a ref with no branch part has no name.
func TestGitLogRemoteLocalName(t *testing.T) {
	cases := map[string]string{
		"origin/feat":   "feat",
		"origin/feat/x": "feat/x",
		"stray":         "",
	}
	for in, want := range cases {
		if got := gitLogRemoteLocalName(in); got != want {
			t.Errorf("gitLogRemoteLocalName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGitLogTrackArgs pins the argv: the name glued to --create so a
// leading "-" can't become an option, the full refname as start point.
func TestGitLogTrackArgs(t *testing.T) {
	got := gitLogTrackArgs("-x", "refs/remotes/origin/feat")
	want := []string{"switch", "--create=-x", "--track", "refs/remotes/origin/feat"}
	if !equalStrings(got, want) {
		t.Errorf("args = %v, want %v", got, want)
	}
}

// remoteScopeRepo clones scopeRepo so origin/side exists as a
// remote-tracking branch with no local copy in the clone.
func remoteScopeRepo(t *testing.T) string {
	t.Helper()
	up := scopeRepo(t)
	clone := t.TempDir()
	gitRun(t, clone, "clone", "-q", up, ".")
	return clone
}

// runScopeRow runs the picker row at index 0 and waits for git to finish.
func runScopeRow(t *testing.T, a *App, want string) {
	t.Helper()
	items := a.gitLogScopeItems(loadGitLogRefs(a.rootDir))
	if items[0].label != want {
		t.Fatalf("first row = %q, want %q", items[0].label, want)
	}
	a.statusMsg = ""
	items[0].run(a)
}

// TestGitLogScopeRemoteCheckout_CreatesTrackingBranch runs the one-click
// row against real git: viewing origin/side creates local side tracking
// it, switches to it, and puts the log back on the current branch.
func TestGitLogScopeRemoteCheckout_CreatesTrackingBranch(t *testing.T) {
	repo := remoteScopeRepo(t)
	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	a.gitLog.open = true
	a.setGitLogScope("refs/remotes/origin/side")

	runScopeRow(t, a, "Check out as local side")
	pumpAppEvents(t, a, func() bool { return strings.HasSuffix(a.statusMsg, "— done") })
	if got := strings.TrimSpace(gitOut(t, repo, "branch", "--show-current")); got != "side" {
		t.Errorf("current branch = %q, want side", got)
	}
	if got := strings.TrimSpace(gitOut(t, repo, "rev-parse", "--abbrev-ref", "side@{upstream}")); got != "origin/side" {
		t.Errorf("side's upstream = %q, want origin/side", got)
	}
	if a.gitLog.scope != gitLogScopeHead {
		t.Errorf("scope = %q, want the current branch after checkout", a.gitLog.scope)
	}
}

// TestGitLogScopeRemoteCheckout_CollisionPrompts covers an unrelated
// local branch owning the name: the row opens a prompt seeded with
// "origin-side", and submitting it creates that tracking branch.
func TestGitLogScopeRemoteCheckout_CollisionPrompts(t *testing.T) {
	repo := remoteScopeRepo(t)
	gitRun(t, repo, "branch", "-q", "--no-track", "side", "main") // same name, different history
	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	a.gitLog.scope = "refs/remotes/origin/side"

	runScopeRow(t, a, "Check out as local branch…")
	p, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want the name prompt", a.modal)
	}
	if got := p.field.String(); got != "origin-side" {
		t.Errorf("prompt seed = %q, want origin-side", got)
	}
	a.closeModal()
	p.callback(a, "origin-side")
	pumpAppEvents(t, a, func() bool { return strings.HasSuffix(a.statusMsg, "— done") })
	if got := strings.TrimSpace(gitOut(t, repo, "rev-parse", "--abbrev-ref", "origin-side@{upstream}")); got != "origin/side" {
		t.Errorf("origin-side's upstream = %q, want origin/side", got)
	}
}

// TestSetGitLogScope_ReloadsTheList drives a pick end to end: picking
// All re-lists with the side commit, picking Current puts it back, and
// re-picking the shown scope is a no-op.
func TestSetGitLogScope_ReloadsTheList(t *testing.T) {
	repo := scopeRepo(t)
	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	a.gitLog.open = true
	a.gitLogRefreshNow()
	if len(a.gitLog.commits) != 2 {
		t.Fatalf("default list = %v, want main's 2", logSubjects(a.gitLog.commits))
	}

	a.setGitLogScope(gitLogScopeAll)
	if len(a.gitLog.commits) != 3 {
		t.Errorf("after All = %v, want 3", logSubjects(a.gitLog.commits))
	}
	a.gitLog.commits = nil // a no-op pick must not reload
	a.setGitLogScope(gitLogScopeAll)
	if a.gitLog.commits != nil {
		t.Error("re-picking the shown scope reloaded the list")
	}
	a.setGitLogScope(gitLogScopeHead)
	if len(a.gitLog.commits) != 2 {
		t.Errorf("after Current = %v, want 2", logSubjects(a.gitLog.commits))
	}
}

// TestRefreshGitLogCommits_GoneBranchFallsBack covers a scoped branch
// deleted under the panel: the refresh drops back to the current branch
// and flashes why, instead of leaving an empty list.
func TestRefreshGitLogCommits_GoneBranchFallsBack(t *testing.T) {
	repo := scopeRepo(t)
	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	a.gitLog.open = true
	a.setGitLogScope("refs/heads/side")
	if len(a.gitLog.commits) != 3 {
		t.Fatalf("side list = %v", logSubjects(a.gitLog.commits))
	}

	gitRun(t, repo, "branch", "-q", "-D", "side")
	a.refreshGitLogCommits()
	if a.gitLog.scope != gitLogScopeHead {
		t.Errorf("scope = %q, want the current branch after deletion", a.gitLog.scope)
	}
	if len(a.gitLog.commits) != 2 {
		t.Errorf("fallback list = %v, want main's 2", logSubjects(a.gitLog.commits))
	}
	if !strings.Contains(a.statusMsg, "side is gone") {
		t.Errorf("flash = %q, want it to name the vanished branch", a.statusMsg)
	}
}

// TestGitLogScopeCheckout_SwitchesBranch runs the picker's Check out row
// against real git: it switches to the viewed branch.
func TestGitLogScopeCheckout_SwitchesBranch(t *testing.T) {
	repo := scopeRepo(t)
	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	a.gitLog.scope = "refs/heads/side"

	items := a.gitLogScopeItems(loadGitLogRefs(repo))
	if items[0].label != "Check out side" {
		t.Fatalf("first row = %q, want Check out side", items[0].label)
	}
	a.statusMsg = ""
	items[0].run(a)
	pumpAppEvents(t, a, func() bool { return strings.HasSuffix(a.statusMsg, "— done") })
	if got := strings.TrimSpace(gitOut(t, repo, "branch", "--show-current")); got != "side" {
		t.Errorf("current branch = %q, want side", got)
	}
}

// TestGitLogPress_ScopeButtonOpensPicker checks the header button is
// drawn with the branch name and that a press on it opens the picker
// (one btnRect for both).
func TestGitLogPress_ScopeButtonOpensPicker(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.gitBranch = "main"
	a.gitLog.open = true
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()

	r := a.gitLogScopeRect()
	cells, w, _ := scr.GetContents()
	var b strings.Builder
	for x := r.x; x < r.x+r.w; x++ {
		if c := cells[r.y*w+x]; len(c.Runes) > 0 {
			b.WriteRune(c.Runes[0])
		}
	}
	if got := b.String(); got != " ⑂ main ▾ " {
		t.Errorf("scope button = %q, want \" ⑂ main ▾ \"", got)
	}

	if got := a.gitLogPress(r.x+1, r.y); got != "" {
		t.Errorf("scope press started a drag %q", got)
	}
	if m, ok := a.modal.(*paletteModal); !ok || m.title != "Show in git log" {
		t.Fatalf("scope press opened %T, want the scope picker", a.modal)
	}
}

// TestGitLogScopeLabel_ElidesLongNames keeps a ticket-length branch from
// shoving the title and toolbar off the header, and names All.
func TestGitLogScopeLabel_ElidesLongNames(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitLog.scope = "refs/heads/" + strings.Repeat("x", 60)
	if n := runeLen(a.gitLogScopeLabel()); n > gitLogScopeNameMax+6 {
		t.Errorf("label is %d cells, want at most %d", n, gitLogScopeNameMax+6)
	}
	a.gitLog.scope = gitLogScopeAll
	if got := a.gitLogScopeLabel(); got != " ⑂ all branches ▾ " {
		t.Errorf("all label = %q", got)
	}
}

// TestMenuGitLogScope_OpensTheLogFirst pins the ≡ door: from a shut
// panel it opens the log and then the picker; off a repo it does nothing.
func TestMenuGitLogScope_OpensTheLogFirst(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuGitLogScope()
	if a.gitLog.open || a.modal != nil {
		t.Fatal("non-repo opened something")
	}

	a.gitIsRepo = true
	a.menuGitLogScope()
	if !a.gitLog.open {
		t.Fatal("the log did not open")
	}
	if m, ok := a.modal.(*paletteModal); !ok || m.title != "Show in git log" {
		t.Fatalf("modal = %T, want the scope picker", a.modal)
	}
}
