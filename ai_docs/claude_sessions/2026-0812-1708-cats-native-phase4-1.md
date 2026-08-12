# Session: cats-native plan, Phase 4.1 — the push dialog

- Session id: `81e7dd28-db17-464f-aee0-23bc222093fe`
- Date: 2026-08-12
- Branch: `main`, commit `85a004e` (1 feature commit + this doc)
- Worked from the cats checkout (`~/projs/go/cats`) against the ced
  repo (`~/projs/go/ced`), which is where every change landed.
- Plan: `ai_docs/cats-native-plan.md` §Phase 4.1 — now marked ✅ done
- Predecessor: `2026-0812-1557-cats-native-phase3-2.md`

## What was asked

Load the last session, then continue with Phase 4 of the ced × cats
plan. §7's execution order and both prior session docs name the same
next item: **4.1, the push dialog** — "never type the current branch".

## What landed

One feature commit plus the plan update. `make test` (race) green.
Verified against a real scratch repo with a local bare remote, which
confirmed three things and changed nothing (the assumptions held).

### New: `internal/app/gitpush.go` + `gitpush_test.go`

The hard requirement is the whole feature: **option 0 in the
remote-branch dropdown is ALWAYS the current local branch**, inserted
unconditionally rather than sorted out of `ls-remote`. Every other push
UI asks "to which branch?" and hands you an empty box or a fetched list
— which, for a branch the remote has never heard of, does not contain
the answer. Enter-Enter-Enter pushes it.

**The four decisions worth remembering**

1. **Its own modal, NOT `formModal`** — a deliberate deviation from the
   plan's "on formModal". formModal's rows *are*
   `customactions.Prompt` values — a CONFIG type describing what a TOML
   action author may declare. Checkboxes, a live header, option lists
   refilled from a goroutine, and a button whose label changes with the
   form's state would all push editor UI into a package that exists to
   parse config. So it mirrors formModal's SHAPE and shares only the
   primitives (`centeredRect`, `drawFrame`, `btnRect`, `drawButton`,
   `textField`) — the same problems.go-vs-gitlog.go call as last
   session, for the same reason: surfaces that will evolve apart share
   the house patterns, not the code.
2. **Locals inline, network async.** `git remote` + `symbolic-ref` are
   two forks the dialog cannot open without — exactly
   `menuGitSwitchBranch`'s stated budget. Only `ls-remote` goes to a
   goroutine (`gitPushRefsEvent`), and **its failure posts an EMPTY
   list**, not an error: the dropdown's load-bearing rows are
   synthesized locally, so an offline push still works.
3. **The key axes are split.** Up/Down move BETWEEN rows; Left/Right
   change the focused row's value. formModal cycles selects with both
   axes, which it can afford because every row is one kind — here the
   branch row becomes a text field that owns Left/Right for its caret.
4. **Force is never bare.** `--force-with-lease`, a red
   `⚠ Overwrites origin/x unless it moved since your last fetch.` line,
   and a button relabeled "Force Push". No second confirm modal: the
   deliberate tick plus a button that says Force IS the confirmation
   (the plan specified this shape). The submit button is
   **right-anchored** so relabeling grows it leftward instead of sliding
   it out from under a pointer already on its way to click it.

### What the render pass caught

Dumping the modal to a `SimulationScreen` before trusting it (a
throwaway `zz_pushdump_test.go`, deleted after) found two real bugs that
no unit test would have:

- **Long branch names tail-truncated the ahead/behind counts away** —
  the one fact on that line no other row restates (the target ref is
  echoed by the dropdown AND the upstream row; the local name is echoed
  by the status bar, tab strip, and log panel). `headerFit` now drops
  the LOCAL half first, keeping the arrow so the line still reads as a
  push.
- **"other…" mode was a one-way door.** Up/Down moved rows, Left/Right
  went to the caret, and `cycleBranch` was unreachable — a user who hit
  the escape hatch by accident was stuck until they cancelled the whole
  dialog. Now arrowing off either END of the field steps the option
  (the arrow is a dead key there anyway), and the chevrons stay drawn so
  the mouse has the same exit.

### Smaller pieces

- **`gitStatus` grew the tracking half**: `Upstream`, `Ahead`, `Behind`,
  `HasRemote`, via `loadGitTracking`. Two forks, not one: the one-fork
  alternative (`for-each-ref %(upstream:track)`) answers in English
  (`[ahead 3, behind 1]`), which is a parser waiting to break. The
  second fork is skipped when there's no upstream, and the extra
  `git remote` fork for `HasRemote` is paid ONLY by an untracked branch
  — a tracked branch is itself proof a remote exists.
- **The status-bar entry is a NEW `↑3↓2` segment**, not the branch
  segment the plan named. The branch keeps switch-branch; the count is
  the fact and push is the verb that clears it — the ● → Save pairing
  that statusbar.go's own header already argues for. A bare `↑` marks a
  never-pushed branch (no count exists, and it's the moment the dialog
  exists for).
- **`↑ push` gitlog header button** rides the LEFT-anchored chain (right
  after `Actions ▾`) so the ⧉/⟳/✕ trio's geometry was untouched.
- The refspec is **always explicit** (`<local>:<remote>`) even when the
  names match, so the command doesn't depend on the repo's
  `push.default`. `-u` with an explicit refspec still tracks the
  right-hand side — confirmed live.
- **Detached HEAD is refused with a flash.** Pushing one is a
  `HEAD:refs/heads/x` refspec typed by someone who already knows they
  want it; this dialog's premise is that the current branch is the
  default answer.
- Deliberately NOT built: Tier-1 `working` reporting during the push —
  it needs the hook reporter Phase 5.3 builds.

### Wiring (all one-liners in existing seams)

Four App fields + their reset in `refreshGitStatus` · one event-switch
case · one ≡ Git row ("Push…", after the commit rows) · one gitpanel
Actions row · `gitLogPushRect` + one press case + one draw call (and the
title now measures from the push button) · one status-bar segment.

## Conventions / gotchas worth remembering

- **Draw the surface before you trust it.** Both real bugs this session
  were layout/interaction facts invisible to unit tests. A throwaway
  dump test that prints the modal's cells row by row cost five minutes.
- **`pushArgs` is split out from `submit` on purpose** so the argv —
  the only thing here with consequences — is testable without any test
  ever running `git push`. Worth copying for any future verb that
  shells out destructively.
- **Menu row counts are pinned in `app_test.go`**, again: ONE new ≡ row
  meant six numbers (`TestMenuLayout_NoCustomActions` ×2 + its comment +
  `dividers[2]`, `TestMenuLayout_CollapseHidesSectionRows` ×2 + its
  comment, `TestMenuLayout_WithCustomActions` ×1). The two
  `a.height = 140` modal-rect pins had headroom and needed nothing.
- **`gofmt -l internal/app/` reports three pre-existing offenders**
  (`hostident_test.go`, `reconcile.go`, `tabbar.go`) — unrelated to this
  work, confirmed via `git status`. Don't "fix" them mid-feature.
- Test helpers already exist and should be reused, not rewritten:
  `initRepo` / `gitRun` / `writeFileT` (gitstatus_test.go),
  `gitAvailable` / `initTestRepo` (gitdiff_test.go). This session added
  `initRepoWithRemote` on top of them.
- **A local bare repo exercises every path a network remote would** —
  ls-remote, upstream resolution, ahead/behind, force-with-lease's
  refusal — with none of the flakiness. It's what the plan's §8 asks
  for and it's the right default for git tests here.
- gopls still flags `../ced` files as "not in workspace" from a cats
  session — noise; `go build ./...` + `make test` in ced are the real
  checks.

## Live verification (scratch repo + local bare remote)

Confirmed by running the exact argv the dialog emits:

- `push --set-upstream origin main:main` → tracking set to `origin/main`.
- `rev-list --count --left-right @{upstream}...HEAD` → `0	1` after one
  local commit, pinning the column order (**left is BEHIND**).
- `push --set-upstream origin main:feature-x` → upstream becomes
  `origin/feature-x`, i.e. `-u` follows the RIGHT-hand side.
- `push --force-with-lease origin main:feature-x` overwrites after an
  amend, and **correctly refuses with "stale info"** once the remote was
  moved out from under it — so the warning sentence is accurate, not
  aspirational.

## State / next steps

- Phases 1, 2, 3.1, 3.2 and 4.1 all done (all 2026-08-12).
- Next per §7: **Phase 4.2 — git history search**, the gitlog filter bar
  (`--grep` default · `a:` author · `p:` path · `s:` pickaxe), 250ms
  debounce, results on a goroutine. ~300 LOC.
- Then 4.3 (cherry-pick/revert polish + the conflict picker), 4.4 (the
  pre-commit survey walk mode), 4.5 (`commitMsgTrailer` config + chip).
- Still on the table from Phase 2: clicking a tab's `⚠` marker should
  re-raise a deferred conflict prompt.
- Unclaimed Phase-3 leftovers: 3.3 (hover on dwell) is Tier-1 and
  really belongs with Phase 5; 3.4 (recent-files picker) is an hour's
  work whenever a breather is wanted.
- Untouched follow-ups for 4.1: no `--tags` / `--follow-tags` option, no
  "push all branches", and no post-push offer to open the remote's
  compare/PR URL — none were asked for, and the last one is the most
  obviously useful if the dialog gets a second pass.
