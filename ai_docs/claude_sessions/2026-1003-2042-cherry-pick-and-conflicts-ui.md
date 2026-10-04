# Session: Cherry-pick dialog + merge-conflict UI (in-editor lens, Conflicts panel)

Session ID: `0c276b15-ca67-49f3-af1c-110de5091063`
Date: 2026-10-03

## Request

From the cats-todo backlog: "I need an advanced, intuitive UI for
cherry-picking and handling merge conflicts."

Starting point: the 4.3 conflict PICKER (`gitconflict.go`: open files,
stage, continue, abort) and the git log's single-commit cherry-pick. Known
4.3 follow-ups: no `--skip`, nothing on screen saying the repo is parked,
conflicted files unmarked.

## What was built

Three surfaces, plus the plumbing they share.

### 1. In-editor resolution (editor/conflict.go, editor/lens.go, app/conflictview.go)

```
  10 <<<<<<< HEAD  Accept current · Accept incoming · Accept both
  11         return []string{}, nil              ← current wash (green)
  12 =======                                     ← neutral grey
  13         return nil, errEmpty                ← incoming wash
  14 >>>>>>> 683e1dc (Reject empty input)
```

- **editor/conflict.go** — `ParseConflicts` (strict grammar: exactly 7
  marker chars + space/EOL, `=======` alone; unclosed openers are text;
  diff3 `|||||||` base supported), `ConflictBlock` (Start/Base/Mid/End,
  labels, `RegionOf`, `Resolution`), `ConflictChoice` (current, incoming,
  both, both-incoming-first, base, neither) with shared `Label()`.
  `Tab.Conflicts()` memoized per EditRev; `ResolveConflict` /
  `ResolveAllConflicts` are ONE structural undo step (bottom-up splice);
  `NextConflict`, `ConflictAt`.
- **Two new decoration primitives** (decoration.go, lens.go):
  `LineWashSource` (whole-row bg incl. gutter and past-EOL; replaces the
  caret-line highlight) and `LensSource` (clickable end-of-line buttons:
  full → short labels → shed from the right; outranks both notes; Render
  STAMPS `lensHits`, `Tab.LensAt` hit-tests them).
- **app/conflictview.go** — `conflictSource` (wired in `wireTab`): marker
  rows bold, washes by region, lens on each opener. GATED on
  `a.gitConflicted[path]` so marker fixtures stay quiet. Lens IDs carry
  `conflictLensTag`; the block is re-identified by its opener line at
  click. Verbs: `resolveConflictBlock`, `resolveAllConflictsIn`,
  `stepConflict` (crosses into the next unmerged file, buffer-first
  counts), `menuResolveConflictAtCaret` (picker), editor right-click rows
  (prepended in contextmenu.go). Resolving never stages.
- Theme: `conflict-current` (22% ok) and `conflict-incoming` (CHOSEN by
  `conflictIncomingHue`: first of accent-soft/warn/err/accent whose wash
  clears both the current wash and the separator grey).

### 2. Conflicts tool window (app/conflictpanel.go, app/gitopstate.go)

```
─ Conflicts · 1 left ──────────────────────────────────────────── ✕ ─
 ⚠ Cherry-pick 1 of 3 · 683e1dc “Reject empty input”   [ Continue ] [ Skip ] [ Abort ] ⟳
   current = main (HEAD) · incoming = 683e1dc
 ● parse.go  1 conflict · both modified          [ All current ] [ All incoming ]
```

- `toolConflicts` ("conflicts"), generic header (title count "N left" /
  "ready to continue"), bottom by default.
- gitopstate.go reads the parked op from the git dir: CHERRY_PICK_HEAD /
  REVERT_HEAD / MERGE_HEAD+MERGE_MSG / rebase-merge counters; multi-pick
  position = `rev-list --count sequencer/head..HEAD` + 1, total = that +
  todo lines (verified: todo lists the STOPPED pick first).
  `loadConflictFiles` = `status --porcelain -z` with XY codes;
  presence conflicts (DU/UD/AU/UA/DD) get Keep/Delete, never "ready".
- Op row: Continue (dimmed + explains itself; becomes "Resolve all &
  continue" when only marker-free files remain), Skip (confirm; not for
  merge), Abort, ⟳ — right-aligned via `layoutButtonsRight`.
- Rows: All current / All incoming → Mark resolved; right-click adds
  whole-file `checkout --ours/--theirs` + add (confirmed).
- "Marked resolved since this stop" is the ONE remembered thing (keyed by
  op + stopped commit); everything else re-derived per refresh.
- Empty pick (already applied) detected: nothing unmerged + nothing
  staged → Continue dimmed with "Skip moves on".

### 3. Cherry-pick dialog (app/cherrypick.go)

≡ Git → "Cherry-pick from branch…" → branch picker (for-each-ref by
recency, symrefs and current dropped) → modal listing
`HEAD...src --right-only --cherry-mark --no-merges` (cap 300, announced),
loaded off-loop (seq-checked event). `[=]` already-applied rows unpickable;
`⚠` = touches a file HEAD changed since the merge base. Space ticks,
Enter picks (the highlighted one if none ticked), alt+a/x/s/b. Applied
OLDEST FIRST in one `git cherry-pick`; `-x` dropped with `--no-commit`.
Refused (panel shown) while an op is parked.

### Plumbing / behaviour changes

- **Stop hook** (`gitConflictFailHook`) now raises the panel and opens
  the first conflict instead of the picker modal; claims the event only
  when files are unmerged (else git's message shows over the panel).
- **Bug fixed**: continue / skip now carry the fail hook
  (`runGitCmdSeqHook` added to gitcmd.go), so a continue that stops on the
  NEXT commit's conflict returns to the panel instead of the error modal.
  `gitConflictAfterStep` closes the panel and flashes "<Op> finished".
- Picker (kept as the keyboard door) gained Skip, Keep/Delete, and a
  panel toggle row; stage-and-continue excluded while a presence conflict
  is pending.
- cats: `conflictPanel.unseen` reports `blocked` for a stop (the panel is
  not a modal), cleared by the next key/click in `handleEvent`.
- Status bar ⚠ segment (after the branch) and tree error colour for
  conflicted files/folders. `gitOp`/`gitDir` ride the existing rev-parse
  (`--show-toplevel --absolute-git-dir`, `splitRevParseDirs`).
- ≡ Git: 5 new rows (Resolve conflict at caret…, Next/Previous conflict,
  Show/Hide conflicts panel, Cherry-pick from branch…). Menu pins now
  168 actions / 185 rows / height 191 / dividers `[2, 5, 188]`.

## Found by driving the real binary (run-ced), all fixed + tested

1. **Stale row counts**: after `--continue` reloaded a tab with the next
   commit's markers, the row said "no markers left". Open files' counts
   are now read LIVE from the buffer (`conflictFileCount`).
2. **Format-on-save race**: staging a `.go` file through `saveTabAt`
   started the async formatter while `git add` → `--continue` ran; it
   also held off the reconcile. New `saveForStaging` writes the buffer
   verbatim (no format, no plugin hooks).
3. **Invisible incoming wash** in a green-accent theme (the cats host
   theme): accent-soft came out olive, washing to the separator's grey.
   Fixed by `conflictIncomingHue`; the new palette test then caught
   Darcula too. Tokyo Night unchanged.
4. Test-fixture lesson: cherry-picking onto a commit's own unchanged
   parent in the same second reproduces the IDENTICAL hash, so git omits
   it from `HEAD...topic` rather than marking it `=`.

## Verification

- `make test` (race) green; gofmt + vet clean.
- New tests: editor/conflict_test.go, lens_test.go, decoration_test.go
  (washes); app/conflictview_test.go, conflictpanel_test.go (incl. real-git
  end-to-end resolve → mark → continue, continue into the next conflict
  with the file open, presence conflict, empty pick, skip),
  gitopstate_test.go, cherrypick_test.go (real repo e2e with -x, conflict
  → panel); additions in gitconflict/gitstatus/gitcmd/cats_glue/
  toolheader/contextmenu tests, filetree, theme palette.
- Real binary via run-ced: dialog, stop (panel + lens), lens click, and
  "Resolve all & continue" into "Cherry-pick 3 of 3" all confirmed.
  Screenshots published as an artifact:
  https://claude.ai/artifact/AL5TQMYKpPtEWvzXbgz2AW

## Docs

- README: Features bullet, tool-window lists, new section
  "Cherry-picking and merge conflicts".
- CLAUDE.md: architecture map, menu pins, decoration-layer primitives,
  Conflicts + cherry-pick design rules under Git, the incoming-hue rule
  under Themes, conflicts in the tool-window list.

## Next

Closed: None. Declined: None.
Raised: N-047, N-048, N-049, N-050, N-051, N-052, N-053, N-054.
Deferred: None. Promoted: None.
Updated: N-025. Full list: `ai_docs/todo/next-list.md`.
