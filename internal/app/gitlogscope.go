// =============================================================================
// File: internal/app/gitlogscope.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-04
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitlogscope.go decides WHICH history the git log panel lists: the
// current branch (the default), every ref at once, or one named branch,
// local or remote. The header's "⑂ main ▾" button and its ≡ Git twin
// ("Git log branch…") open one picker over those choices; the picker
// also carries "Check out <branch>" while the log is showing a local
// branch you are not on — the log is where you go looking at a branch,
// and switching to the one you're reading is the natural next step.
//
// Why the current branch by default: the panel used to walk `--all`,
// which on any repo with a few feature branches interleaves their
// commits with yours by date, so "what happened on my branch" — the
// question the panel is opened for most — had to be read around other
// people's work. `--all` is still one pick away, and it is what a
// cross-branch tick set wants.
//
// House patterns in play:
//
//   - The scope is a REVISION ARGUMENT, nothing more (gitLogScopeRevs).
//     Every log command the panel runs — the plain list and every search
//     mode but `c:` — is built by gitLogFilterArgs, which splices it in,
//     so the list and a search over it can never disagree about which
//     history they cover. `c:` names its own starting commit and
//     ignores the scope on purpose (a commit off this branch must still
//     be findable — it's what a blame click lands in).
//   - A named branch is held by its FULL refname (refs/heads/x,
//     refs/remotes/origin/x). That spelling can't start with "-", so it
//     is never read as an option, and can't collide with a tag or a file
//     of the same short name. The label shows the short name.
//   - Choose-one-from-a-list = openPicker, like every other list.
//   - Session-only, like the list width: a fresh session opens on the
//     current branch, which is the answer the panel should start from.
//   - A scope that stops existing (the branch was deleted, a remote
//     pruned) falls back to the current branch and says so once, rather
//     than leaving an inexplicably empty list (gitLogScopeGone).

package app

import (
	"os/exec"
	"strings"
)

const (
	// gitLogScopeHead is the default scope: whatever HEAD is. It is the
	// zero value so a directly-built App (and every test) gets it free,
	// and it follows a branch switch with no bookkeeping — `git log`
	// with no revision walks HEAD, whatever HEAD has become.
	gitLogScopeHead = ""
	// gitLogScopeAll walks every ref (`--all`). "*" is a safe sentinel:
	// check-ref-format forbids "*" in a ref name, so no branch can ever
	// be spelled like it.
	gitLogScopeAll = "*"

	// gitLogScopeNameMax caps the branch name in the header button. The
	// left-hand chain (Actions, push, search, this) must leave room for
	// the title and diff toolbar; a 60-character ticket branch would
	// otherwise shove both off the header. elide marks the cut.
	gitLogScopeNameMax = 24

	// gitLogBranchGlyph is the branch mark on the header button — the
	// fork the cherry-pick dialog already uses for merges, single width
	// (the runeLen rule).
	gitLogBranchGlyph = "⑂"
)

// gitLogRef is one branch the scope picker offers: the full refname
// (what git is handed) and the short name (what the user reads).
type gitLogRef struct {
	full   string
	short  string
	remote bool
}

// gitLogScopeRevs is the revision argument a scope contributes to a log
// command: nothing for HEAD (git's default), `--all`, or the full
// refname. Split out so the argv rule is pinned without forking git.
func gitLogScopeRevs(scope string) []string {
	switch scope {
	case gitLogScopeHead:
		return nil
	case gitLogScopeAll:
		return []string{"--all"}
	default:
		return []string{scope}
	}
}

// gitLogRefShort turns a full refname into the name a user types:
// refs/heads/feat/x → feat/x, refs/remotes/origin/main → origin/main.
// Anything else is returned unchanged — it is already the best label
// available.
func gitLogRefShort(full string) string {
	for _, p := range []string{"refs/heads/", "refs/remotes/"} {
		if strings.HasPrefix(full, p) {
			return full[len(p):]
		}
	}
	return full
}

// gitLogScopeName is the scope spelled for a human, unelided: the
// current branch's name (HEAD when detached or unborn), "all branches",
// or the named branch's short name.
func (a *App) gitLogScopeName() string {
	switch a.gitLog.scope {
	case gitLogScopeHead:
		return a.gitLogBranchLabel()
	case gitLogScopeAll:
		return "all branches"
	default:
		return gitLogRefShort(a.gitLog.scope)
	}
}

// gitLogScopeLabel is the header button's text: " ⑂ main ▾ ". ▾ says a
// list opens, the house convention for the Actions button beside it.
func (a *App) gitLogScopeLabel() string {
	return " " + gitLogBranchGlyph + " " + elide(a.gitLogScopeName(), gitLogScopeNameMax) + " ▾ "
}

// gitLogScopeRect is the scope button's rectangle — next along the
// header's LEFT chain after ⌕ search, for the reason push and search
// joined it there: the right-hand ⧉/⟳/✕ trio keeps its geometry, and
// the title (which already drops out on a narrow panel) absorbs the
// width. One source for draw, hit-test and the hover label.
func (a *App) gitLogScopeRect() btnRect {
	s := a.gitLogSearchRect()
	return btnRect{x: s.x + s.w, y: s.y, w: runeLen(a.gitLogScopeLabel())}
}

// loadGitLogRefs lists local branches, then remote-tracking branches,
// each group in git's (alphabetical) order. Symbolic refs are dropped —
// origin/HEAD is an alias for a branch already in the list, and offering
// it twice would make the picker look like it lists two histories.
// Best-effort like every git read: any failure is an empty list.
func loadGitLogRefs(rootDir string) []gitLogRef {
	if rootDir == "" {
		return nil
	}
	out, err := exec.Command("git", "-C", rootDir, "for-each-ref",
		"--format=%(refname)%09%(symref)", "refs/heads", "refs/remotes").Output()
	if err != nil {
		return nil
	}
	return parseGitLogRefs(out)
}

// parseGitLogRefs decodes loadGitLogRefs' "refname<TAB>symref" lines.
// Split out so the symref and grouping rules are testable without a
// repo. Locals come first whatever order the input had — the branch
// you're likely to want is yours, not a remote's copy of it.
func parseGitLogRefs(out []byte) []gitLogRef {
	var locals, remotes []gitLogRef
	for _, ln := range strings.Split(string(out), "\n") {
		ln = strings.TrimRight(ln, "\r")
		full, sym, _ := strings.Cut(ln, "\t")
		if full == "" || sym != "" {
			continue
		}
		r := gitLogRef{full: full, short: gitLogRefShort(full)}
		switch {
		case strings.HasPrefix(full, "refs/heads/"):
			locals = append(locals, r)
		case strings.HasPrefix(full, "refs/remotes/"):
			r.remote = true
			remotes = append(remotes, r)
		}
	}
	return append(locals, remotes...)
}

// gitLogScopeItems builds the scope picker's rows. Order:
//
//	Check out feat/x            ← only while viewing a local branch you're not on
//	Current branch (main)
//	All branches
//	feat/x (viewing)            ← other local branches
//	origin/main                 ← then remote-tracking ones
//
// The row matching today's scope says "(viewing)" — the theme picker's
// "(current)" idea, worded for a view rather than a setting so it can't
// be misread as the checked-out branch. The checked-out branch is left
// out of the named list: "Current branch" already is it, and the
// difference between the two (one follows HEAD, one is pinned) is not a
// choice anyone should have to make from a list.
func (a *App) gitLogScopeItems(refs []gitLogRef) []paletteItem {
	viewing := func(scope, label string) string {
		if a.gitLog.scope == scope {
			return label + " (viewing)"
		}
		return label
	}
	var items []paletteItem
	if br, ok := a.gitLogScopeCheckoutTarget(); ok {
		items = append(items, paletteItem{
			label: "Check out " + br,
			run:   func(app *App) { app.runGitCmd("Switch to "+br, "switch", br) },
		})
	}
	items = append(items,
		paletteItem{
			label: viewing(gitLogScopeHead, "Current branch ("+a.gitLogBranchLabel()+")"),
			run:   func(app *App) { app.setGitLogScope(gitLogScopeHead) },
		},
		paletteItem{
			label: viewing(gitLogScopeAll, "All branches"),
			run:   func(app *App) { app.setGitLogScope(gitLogScopeAll) },
		},
	)
	for _, r := range refs {
		if !r.remote && r.short == a.gitBranch {
			continue
		}
		full := r.full // capture per iteration for the closure
		items = append(items, paletteItem{
			label: viewing(full, r.short),
			run:   func(app *App) { app.setGitLogScope(full) },
		})
	}
	return items
}

// gitLogScopeCheckoutTarget reports the local branch the log is showing
// when it is NOT the checked-out one — the branch "Check out" would
// switch to. Remote-tracking branches don't qualify: switching to one
// means creating a local branch, a different verb with its own naming
// question, which ≡ Git → "New branch…" / the terminal answer better.
func (a *App) gitLogScopeCheckoutTarget() (string, bool) {
	if !strings.HasPrefix(a.gitLog.scope, "refs/heads/") {
		return "", false
	}
	br := gitLogRefShort(a.gitLog.scope)
	if br == a.gitBranch {
		return "", false
	}
	return br, true
}

// openGitLogScopePicker lists the scopes. The refs are read inline (one
// fork, Switch branch's budget) because the picker needs them before it
// can open.
func (a *App) openGitLogScopePicker() {
	a.openPicker("Show in git log", a.gitLogScopeItems(loadGitLogRefs(a.rootDir)))
}

// menuGitLogScope is the ≡ Git / palette door to the header button —
// macOS Terminal can swallow clicks, so a header button needs a
// keyboard twin. It opens the log first when it's shut: choosing which
// history to read and reading it are one thought (Search history…'s
// argument).
func (a *App) menuGitLogScope() {
	a.closeMenu()
	if !a.gitIsRepo {
		return
	}
	if !a.gitLog.open {
		a.menuToggleGitLog()
	}
	if !a.gitLog.open {
		return // refused (tool layer) — nothing to scope
	}
	a.openGitLogScopePicker()
}

// setGitLogScope switches the panel to scope and reloads it through the
// user-initiated refresh, which re-runs an applied search under the new
// scope rather than dropping it — "this search, on that branch" is the
// likely intent of changing branch with results on screen. Picking the
// scope already shown is a no-op (no fork, no flicker).
func (a *App) setGitLogScope(scope string) {
	if scope == a.gitLog.scope {
		return
	}
	a.gitLog.scope = scope
	a.gitLog.listScroll = 0
	a.gitLogRefreshNow()
}

// gitLogScopeGone handles an empty plain-list load: when the scope names
// a branch that no longer resolves, it drops back to the current branch,
// flashes why, and reports true so the caller reloads. An empty list
// under a branch that still resolves (git itself failing) is left
// alone — the panel's "(no commits)" is the honest answer then. Only
// asked on an empty result, so a healthy panel never pays the fork.
func (a *App) gitLogScopeGone() bool {
	s := a.gitLog.scope
	if s == gitLogScopeHead || s == gitLogScopeAll {
		return false
	}
	err := exec.Command("git", "-C", a.rootDir, "rev-parse", "--verify", "--quiet", s).Run()
	if err == nil {
		return false
	}
	a.gitLog.scope = gitLogScopeHead
	a.flash(gitLogRefShort(s) + " is gone — showing the current branch")
	return true
}
