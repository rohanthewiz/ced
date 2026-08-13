# Session: cats-native plan, Phase 4.3 — cherry-pick / revert polish

- Session id: `aabc4747-f375-489e-b072-a03fffa8338a`
- Date: 2026-08-12
- Branch: `main`, base commit `39ab1b1` (1 feature commit + this doc)
- Worked from the cats checkout (`~/projs/go/cats`) against the ced
  repo (`~/projs/go/ced`), which is where every change landed.
- Plan: `ai_docs/cats-native-plan.md` §Phase 4.3 — now marked ✅ done
- Predecessor: `2026-0812-2012-cats-native-phase4-2.md`

## What was asked

Load the last session, then continue with Phase 4. §7 and the 4.2 doc
both name the same next item: **4.3, cherry-pick / revert polish** — the
pointer-anchored log actions, the confirms, and the conflict picker.

## What landed

One feature commit plus the plan update. `make test` (race) green;
`gofmt`/`go vet` clean apart from the three pre-existing offenders
(`hostident_test.go`, `reconcile.go`, `tabbar.go`).

### (a) Right-click a commit row → the verbs at the pointer

`tryGitLogContextClick` in `gitlogactions.go`, routed from app.go's
Button3 branch right after the problems panel's.

**It reuses `editorContextModal`, not the fuzzy picker.** A right-click
has already named its subject by where it landed, so the picker's query
field would be a text box asking a question the gesture just answered —
the same call the problems panel made. Rows come from
`gitLogActionItems` through a new `contextItemsFromPalette` adapter, so
the anchored menu and the Tier-0 `Actions ▾` picker are one list and
cannot drift.

Boundaries: a commit row SELECTS first (right-clicking row three and
acting on row one is the bug every context menu must not have); the
detail pane opens against the standing selection (nothing there to
re-aim at); the header and search bar SWALLOW the gesture rather than
escalate, because falling through would answer a click on the log with a
menu about the editor.

### (b) Confirms — and the modal defect they exposed

Cherry-pick and revert now gate behind `confirmGitLogApply`, naming the
commit's **subject** (a seven-char hash is not recognisable) and the
**branch the new commit lands on** — the log lists `--all`, so the row
under the pointer routinely belongs to a branch you are not standing on.
Both labels gained the house `…` that promises a row opens something.

This needed `openConfirmLines`: `confirmModal`'s single centered line is
50 cells and **byte-sliced** anything longer. Which surfaced the real
find — the pre-existing `reset --hard` confirm, the most destructive verb
in the panel, was an 80-character sentence being cut mid-warning. Fixed
to two lines. The truncation itself is now `elide` (rune-safe, marks the
cut) because commit subjects reach this dialog and this repo's subjects
are full of em dashes.

`confirmBodyRows` is the single source `rect()` and `buttons()` both
derive from, so the button row rides down under a longer body instead of
being painted over; a one-line body reproduces the historical `my+5`
exactly, which is why no existing test moved.

### (c) The conflict picker — `gitconflict.go`

Rows are a **safety gradient**: open (bulk row, then one row per file so
the picker also answers "which files?"), then resolve, then abort last
behind its own confirm. The default row can only ever open a file.

**Four decisions worth remembering**

1. **"Resolved" means the INDEX says so, not that the markers are gone.**
   git refuses `--continue` while any path is unmerged, so a marker-free
   but unstaged file is still a conflict. The marker scan only decides
   what is SAFE to `git add` — staging a file that still holds `<<<<<<<`
   is how conflict markers get committed. `fileHasConflictMarkers`
   therefore answers **true** for anything it cannot read: "I could not
   check" must never come out as "go ahead". When every file is
   marker-free, staging and continuing collapse into one row via
   `runGitCmdSeq`.
2. **The operation is re-derived from the repo on every open**, never
   remembered from the command that failed. The picker outlives that
   command (the ≡ row, a second right-click), and a remembered verb would
   eventually offer `cherry-pick --abort` for a rebase started in a
   terminal. The table is most-specific-first because an interactive
   rebase paused on a cherry-pick leaves BOTH markers — pinned by test.
   Merge and rebase come along for free, so a `stash pop` conflict (op
   `""`, no continue/abort rows) gets the same surface.
3. **`--continue` needs `GIT_EDITOR=true` in the ENVIRONMENT.** Both
   nearer-looking spellings were tested live and fail: git rejects
   `--continue --no-edit` as conflicting options, and `-c
   core.editor=true` **loses to an inherited `GIT_EDITOR`** — so it works
   on this machine and fails for anyone who set one in their profile.
   Cost a new `runGitCmdEnv` / `runGitCmdSeqEnv` pair; note that setting
   `Cmd.Env` at all REPLACES the environment, hence `os.Environ()` first.
4. **`git add` gets ABSOLUTE paths.** git reports unmerged paths relative
   to the work-tree root, but ced runs git with `-C rootDir`, and rootDir
   is routinely a subdirectory of the repo — the relative spelling would
   resolve against the wrong base. Same rule `stageFilePath` already
   follows. Pinned by a test that opens the repo from a subdirectory.

### Plumbing

- **`runGitCmdHook`** — a failure hook riding on the done-event (so two
  commands in flight can't claim each other's), consulted before the
  error modal. `gitConflictFailHook` claims a failure ONLY when the repo
  is left mid-operation; "bad revision" and "your local changes would be
  overwritten" still get git's own words.
- The failure path now calls `refreshTreeNow()`, which it never did — the
  gutters and dirty colors were describing the pre-conflict work tree.
- **`gitStatus.ConflictedFiles`** read out of the porcelain output the
  snapshot already had (no extra fork), mirrored to `a.gitConflicted`, so
  the new ≡ Git row "Resolve conflicts…" stays a fork-free predicate.
  `isPorcelainConflict` covers all seven codes — testing only for `U`
  would miss `AA`, the common two-branches-created-the-same-file case.

## What the render pass caught

Dumping the dialogs to a `SimulationScreen` before trusting them (a
throwaway `zz_conflictdump_test.go`, deleted after) found the abort
confirm's second line reading `…are discar…` — 53 characters in a
48-cell slot. Reworded, and the test now asserts every confirm line fits
the body slot rather than just that the words are present. The dump also
confirmed the anchored menu flips left correctly against the panel's
right edge, and that the picker's row set reads exactly as designed.

## Conventions / gotchas worth remembering

- **Menu row counts are pinned in `app_test.go`**, again: ONE new ≡ Git
  row meant the same six numbers (`TestMenuLayout_NoCustomActions` ×2 +
  its comment + `dividers[2]`, `TestMenuLayout_CollapseHidesSectionRows`
  ×2 + its comment, `TestMenuLayout_WithCustomActions` ×1).
- `plural(n, one, many)` **includes the count** — it returns "2 files",
  not "files". Three call sites were doubled before the first build.
- The Yes/No confirm's buttons are at fixed `mx+14` / `mx+28`, so they
  sit left of centre in a 54-wide modal. Pre-existing, untouched.
- `git diff --name-only --diff-filter=U -z` is the direct question, where
  porcelain's XY table is an inference. `-z` also removes git's quoting
  of paths with spaces or non-ASCII.
- `git status --porcelain` and `git diff --name-only` both report
  work-tree-root-relative paths; `git ls-files` does not. Worth checking
  before joining anything to `rootDir`.
- gopls still flags `../ced` files as "not in workspace" from a cats
  session — noise; `go build ./...` + `make test` in ced are the real
  checks.

## Live verification

- Scratch repo, cherry-pick conflict: detection, unmerged list, marker
  scan flipping once the file is fixed, stage-then-`--continue` actually
  landing the commit.
- Revert conflict → `REVERT_HEAD`, `--abort` restoring a clean tree.
- A merge whose `--continue` **proved git really does launch the
  editor** — and that `-c core.editor=true` does not stop it while a
  hostile `GIT_EDITOR` is set, which is what forced the env approach.
- All of the above also live in `gitconflict_test.go` as real-repo tests,
  so the behaviour git owns is re-checked on every run.

## State / next steps

- Phases 1, 2, 3.1, 3.2, 4.1, 4.2 and 4.3 all done (all 2026-08-12).
- Next per §7: **Phase 4.4 — the one-gesture pre-commit survey**: walk
  mode ("Review all ▶" + `n`/`p`/wheel, `3/7 files`), reviewed checkmarks
  distinct from the stage checkbox, `Commit (5/7 reviewed)` as a nudge
  not a gate, per-hunk stage/unstage/revert verbs in the diff gutter, and
  the walk ending on the commit-message field beside the ✨ AI button.
  ~250 LOC.
- Then 4.5 (`commitMsgTrailer` config + chip, ~60 LOC).
- Still on the table from Phase 2: clicking a tab's `⚠` marker should
  re-raise a deferred conflict prompt.
- Unclaimed Phase-3 leftovers: 3.3 (hover on dwell) is Tier-1 and really
  belongs with Phase 5; 3.4 (recent-files picker) is an hour's work.
- Untouched follow-ups for 4.3: no `--skip` row (the third thing git
  offers on a stopped cherry-pick, and the one that quietly drops a
  commit — it wants its own confirm); the conflict picker has no
  keyboard-only entry point beyond the ≡ row / palette; conflicted files
  are not marked in the file tree or the tab bar, so a dismissed picker
  leaves nothing on screen saying the repo is parked; and the marker scan
  re-reads every conflicted file each time the picker opens, which is
  fine for the dozens but not for hundreds.
