# Session: cats-native plan, Phase 4.4 — the one-gesture pre-commit survey

- Session id: `ea4fccac-d2e2-4031-97ef-96082f165782`
- Date: 2026-08-12
- Branch: `main`, feature commit `582fcf1` (+ this doc)
- Worked from the cats checkout (`~/projs/go/cats`) against the ced repo
  (`~/projs/go/ced`), which is where every change landed.
- Plan: `ai_docs/cats-native-plan.md` §Phase 4.4 — now marked ✅ done
- Predecessor: `2026-0812-2058-cats-native-phase4-3.md`

## What was asked

Load the last session, then continue with Phase 4. §7 and the 4.3 doc
both name the same next item: **4.4, the one-gesture pre-commit survey**
— walk mode, reviewed checkmarks, hunk verbs, ending on the commit
message field.

## What landed

One feature commit plus the plan update. Two new files
(`gitpanelwalk.go`, `gitpanelhunks.go`) with test siblings, ~1300 LOC
incl. tests. `make test` (race) green; `go vet` clean; `gofmt` clean
apart from the three pre-existing offenders (`hostident_test.go`,
`reconcile.go`, `tabbar.go` — plus `internal/editor/syntax.go`, also
pre-existing).

### (a) Walk mode — `gitpanelwalk.go`

**Walk mode steps the panel's EXISTING selection.** There is no second
index: "the file being reviewed" and "the file whose diff is on screen"
are one variable and so cannot disagree. What the mode adds is the
keyboard and the rule that LEAVING a file marks it read.

Keys, a pager's set: `n`/`p` step, space pages then steps at the bottom
(one gesture that reads a long diff and a short one the same way), Enter
finishes, `h` opens hunk actions, `a` the Actions list, `r` toggles the
mark by hand, `q` stops. Everything else is swallowed — a keystroke
aimed at a panel that owns the keyboard must never reach the buffer.

**Placement in `handleKey` is the design.** The branch sits with
`term.focused` / `chat.focused` / `treeFocus`, AFTER the Esc / leader /
menu blocks, so every global gesture keeps working from inside the
survey. That block returns before the branch, so the walk never sees an
Esc — hence `stopGitPanelWalk()` is a **side effect in the Esc block**,
beside `clearCarets()` and `chatClearSelection()`. Esc is the universal
"drop that", and a mode holding `n`/`p` is what people press Esc to
escape.

**The wheel reads the change set as one document**: at the end of a
file's diff, one more notch steps to the next file. The detent is free —
the notch that REACHES the edge clamps and does nothing else, so
changing file always costs a deliberate extra push. It never ENDS the
survey: walking off the last file would put a commit modal on screen in
answer to a scroll, which nobody aims that precisely. `n` and Enter do
that.

### (b) Reviewed marks — a second set, deliberately

```
[ ] / [x]   selection — what Actions operates on
✓           reviewed  — what you have actually read
```

Collapsing them would mean reading a file implied acting on it. The mark
lives in the list's **leftmost cell**, which was blank padding before, so
it costs the path nothing: `▸` where the survey is standing, `✓` for a
file already read, blank otherwise. Clicking that column toggles the
mark without moving the highlight (the checkbox's own rule).

Marks are keyed by absolute path, pruned on refresh like `checked` — and
more urgently, because the walk's terminal commit targets them. They
**outlive the walk**, which is what makes the survey survive the thing it
exists for: click into the editor, fix what you spotted (a press outside
the panel ends the walk, mouse-first focus), come back to
`Resume 3/7 ▶`.

`gitPanelReviewNudge` goes quiet at 0 reviewed AND at all-reviewed, so
the one state it describes still registers. It never gates a commit.

### (c) Hunk verbs — and what they forced

Chips at the right edge of the hunk header: `[+]` stage, `[−]` unstage,
`[↺]` revert (revert confirms). `gitPanelHunkChipsAt` is called by the
drawer AND the click router, so a chip can't be painted where it can't
be clicked; it withholds the strip entirely on a pane too narrow to
carry it. `Hunk actions…` is the Tier-0 twin (macOS Terminal swallows
clicks), and also where the verbs are spelled out in words.

**Five decisions worth remembering**

1. **The diff pane stopped showing the union `git diff HEAD`.** A hunk
   of the union belongs to neither the index nor the work tree once a
   file is half-staged — and staging one hunk is precisely what MAKES it
   half-staged. Any design reading only the union supports exactly ONE
   hunk-stage per file. So `loadGitPanelDiff` now asks git's two
   questions separately. For a one-sided file the bytes are IDENTICAL to
   the union's (a file whose index matches HEAD has no staged half, and
   vice versa), so nothing changed for the common case; a mixed file
   gets two labelled sections, which is also the honest picture of a
   state the union merged into a diff of something that exists nowhere.
   The union survives as the FALLBACK, and it plus the untracked
   synthesis report `sideNone` — readable, not applicable.
2. **`side` travels WITH the lines on the event**, never re-derived on
   arrival: an unmixed display carries no marker to derive it from, and
   a guess is the difference between staging a hunk and "patch does not
   apply".
3. **Verbs are gated by what git actually checks.** Unstaged hunks can
   always be staged (`apply --cached`) and reverted (`apply -R`) — their
   old side IS the index and their new side IS the work tree, mixed file
   or not. Staged hunks can always be unstaged (`apply --cached -R`).
   Reverting a staged hunk needs `apply --index -R`, which requires
   index and work tree to agree — so on a mixed file that row is
   WITHHELD rather than offered and rejected.
4. **`git apply` runs at the repo TOPLEVEL.** A patch's `a/… b/…` paths
   are work-tree-root-relative; `git apply` resolves them from the
   CURRENT DIRECTORY in work-tree mode (root-relative only under
   `--cached`/`--index`) — and ced is routinely rooted in a
   subdirectory. Same lesson as 4.3's absolute paths, from the other
   end. Pinned by a subdirectory-root test. `gitToplevel` (already in
   gitlog.go) is now the shared answer; `loadGitStatusFiles` uses it too.
5. **A one-hunk patch takes ITS OWN section's file header**, found by
   walking BACK from the `@@` to the section boundary — a mixed diff
   carries two headers, and taking the top one would hand git the wrong
   old-side content.

### (d) The terminal state

The walk ends on the commit prompt over what it read (explicit ticks
still outrank the inferred set), with a `[ ✦ AI ]` button beside OK.
`promptModal` grew an OPTIONAL third button rather than becoming its own
modal — it is a generic house primitive and an extra button is a general
extension of it, unlike formModal, whose rows are a config type (4.1's
call). The button fills the field; it does not submit.

## What the render pass caught

Dumping the panel and the prompt to a `SimulationScreen` before trusting
them (throwaway `zz_walkdump_test.go`, deleted after) found the header
title being drawn from the end of `Actions ▾` — i.e. **straight over the
new survey button**. Labels now yield to controls (and both drop out
rather than overlapping on a narrow panel). Pinned by
`TestGitPanelHeader_TitleYieldsToTheButton`.

The prompt dump also caught the glyph: the roadmap says ✨, but `drawAt`
advances one CELL per rune and U+2728 is East-Asian-Wide — it would
render two cells and shift the button row through the modal's border.
Every glyph ced draws is narrow; the button is `✦` (U+2726).

## Conventions / gotchas worth remembering

- **Menu row counts are pinned in `app_test.go`**, again — ONE new ≡ Git
  row moved the same six numbers (136→137, 130→131 + its comment,
  `dividers[2]` 133→134, Git section 17→18 ×2 + its comment, 139→140).
  The "126 total" in the header comment was already stale; corrected to
  131 while there.
- **`git apply` rewrites a file by replacing it**, so a tight poll can
  catch the moment it doesn't exist. A test that reads the file back in
  a `pumpAppEvents` condition must treat a missing read as "not yet".
- `newTestApp` leaves `copilot.enabled` false and `chat.dead` true —
  both must be set for `canSuggestCommitMsg` (and so the ✦ button).
- A test that types after an Esc is asserting on the LEADER table, not
  on focus: Esc-n is New file. Clear `a.lastEscape` first.
- Section markers start with `─`, which no line inside a real unified
  diff can (git's are ' ', '+', '-', '\', '@', or an ASCII header word).
  `diffTargetLine` resets its new-side counter on one, or a jump from
  the second section would use the first section's numbering.
- gopls still flags `../ced` files as "not in workspace" from a cats
  session — noise; `go build ./...` + `make test` in ced are the real
  checks.

## Live verification

Real-repo tests (they run on every `make test`, so the behaviour git
owns is re-checked continuously):

- Stage one hunk of a two-hunk file → only that hunk lands in the index,
  and the file becomes mixed; the second hunk is then still stageable
  from the mixed view, and the work tree ends clean.
- Unstage one hunk back out → the index loses that hunk, keeps the
  other, and the work tree is untouched (`--cached -R`, not `--index`).
- Revert one hunk → gone from the work tree, the other hunk intact.
- ced rooted in a SUBDIRECTORY of the repo → stage still lands.
- Scratch repo by hand for the odd shapes: a work-tree deletion and a
  staged rename both produce a one-sided diff, so both stay addressable.

## State / next steps

- Phases 1, 2, 3.1, 3.2, 4.1, 4.2, 4.3 and 4.4 all done (all
  2026-08-12).
- Next per §7: **Phase 4.5** — `commitMsgTrailer` config key
  (`internal/userconfig`, `"on"|"off"` house string-enum) honored by
  gitcommitmsg.go, plus a clickable `[trailer: on]` chip beside the new
  ✦ AI button for a per-invocation override. ~60 LOC. That closes Phase
  4; 4.6 (blame) is explicitly optional.
- Then Phase 5 (deep cats integration), with 5.4's OSC 7 / title
  emission pulled to the front.
- Still on the table from Phase 2: clicking a tab's `⚠` marker should
  re-raise a deferred conflict prompt.
- Unclaimed Phase-3 leftovers: 3.3 (hover on dwell) is Tier-1 and
  belongs with Phase 5; 3.4 (recent-files picker) is an hour's work.
- Untouched follow-ups for 4.4: a review mark does not clear when the
  file changes AFTER being read (it records that you looked, not what
  you looked at); stepping BACK lands at the top of the previous file's
  diff rather than its bottom (the diff arrives async, and a
  "land at the end" flag was not worth it); reverting a staged hunk on a
  mixed file is withheld with no on-screen explanation of why (the
  sequence is unstage, then revert); and the hunk chips have no
  keyboard-only equivalent beyond the `h` picker.
- Untouched from 4.3: no `--skip` row on a stopped cherry-pick;
  conflicted files are unmarked in the tree and tab bar.
