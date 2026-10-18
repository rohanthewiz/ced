# Conflict side names

Session: `c93bad9c-d2c5-4441-b472-8ccc0e76d7b4`

## Ask

From the cats-todo backlog: "When resolving git conflicts the language of
what is local vs incoming is not clear to me. What can be done there?"

## Diagnosis

- "Current" / "incoming" say where a side sits, not whose it is.
- The one line that mapped them to the operation (the Conflicts panel's
  sides line) was in the panel, away from where the decision happens.
  The lens, right-click rows, ≡ pickers, `[ All current ]`, whole-file rows
  and presence labels all used the bare words.
- git's marker says `<<<<<<< HEAD`, which is jargon.
- In a rebase your own commit is the INCOMING side, so "local" points the
  wrong way at exactly that moment.

Options offered: (A) name each side by its role + source everywhere,
(B) label the `=======` line, (C) git's ours/theirs (rejected: flips in a
rebase). The user chose **A + B with "yours"**.

## What was built

**`internal/app/conflictsides.go`** (new, + `_test.go`): one namer for
every door. The model stays positional; only the words change.

| op | above (current) | below (incoming) |
|---|---|---|
| merge | yours (branch) | theirs (branch from MERGE_MSG) |
| cherry-pick | yours (branch) | picked (short hash) |
| revert | yours (branch) | revert (short hash) |
| rebase | upstream (onto) | **yours** (short hash) |
| none | current | incoming (exactly the old labels) |

- Names come from `a.conflictPanel.info`. Gaps are filled from the block's
  marker label, and "HEAD" is never shown. A record describing a different
  op than `a.gitOp` lends no names.
- `refreshGitStatus` now keeps the info fresh while the panel is shut.
  `reloadGitOpInfo` reuses the commit summary when the stopped commit hasn't
  moved, so it costs file reads only and no `git log` fork per tick.
- In a rebase past step 1, the sides line adds "+ your commits replayed so far".
- Used by: lens labels (short form = role, e.g. `Yours · Picked`),
  right-click rows, the ≡ resolve and compare pickers (titles too), the
  panel's sides line, `[ All yours ] [ All picked ]`, the panel row menu
  ("Accept all yours (main)", "Use your side for the whole file…") and its
  confirm dialog, compare headers, and `conflictKindLabel(xy, sides)`
  ("deleted on your side").

**Separator label**: `editor.NoteSource` (linenote.go) is a third optional
DecorationSource extension: end-of-line notes made of coloured runs,
upright. Note-slot precedence: lens > source note > caret diagnostic note >
inlay. `conflictSource.Notes` labels `=======` with
`▲ yours (main)  ▼ picked (2a0370b)`. A diff3 block labels both
`|||||||` and `=======` (base in between). Labels show only while an op is known.

**Contrast**: blame's lift moved into `liftToContrast(fg, bg)` (gitblame.go).
The label runs are lifted to 4.5:1 on the separator's grey. Before the lift,
Darcula's green was about 2.5:1. Pinned across all 10 built-in themes.

Docs: README conflict section (table of sides, new mock-up), CLAUDE.md
conflict rule + architecture map + decoration-layer paragraph.

Tests: `make test` (race) all green. New: `conflictsides_test.go`
(table, merge-msg parse, phrases, positional fallback, diff3 notes,
contrast in every theme, stale record, marker fill, on-screen lens +
label, and a REAL `git rebase` stop asserting "yours" is the lower side),
`TestReloadGitOpInfo_ReusesTheSummary`, `TestConflictKindLabel_FollowsTheOperation`,
editor `TestSourceNote_*`. Updated old-wording pins in conflictpanel /
gitopstate / gitconflict tests.

## Real-binary check (run-ced)

After the commit, the real `bin/ced` was driven through the run-ced PTY
capture on a throwaway repo stopped mid-rebase: `feature` had two
commits, the first (README only) replayed cleanly, and the second
("Reject empty input", `return nil, errEmpty`) conflicted with `main`'s
`return []string{}, nil`. That stopped the rebase at step 2 of 2. Script:
open `parse.go` through the finder, then palette → "Show conflicts panel".
At 150 columns it drew:

```
  10 ●<<<<<<< HEAD  Accept upstream (9a589ef) · Accept yours (97440ec) · Accept both · Compare sides
  11          return []string{}, nil
  12 ●=======  ▲ upstream (9a589ef)  ▼ yours (97440ec)
  13          return nil, errEmpty
  14 ●>>>>>>> 97440ec (Reject empty input)
 ⚠ Rebase 2 of 2 · 97440ec “Reject empty input”        [ Continue ] [ Skip ] [ Abort ] ⟳
   ▲ upstream (9a589ef) + your commits replayed so far · ▼ yours (97440ec)
 ● parse.go  1 conflict · both modified                  [ All upstream ] [ All yours ]
```

- "yours" is on the lower side, which holds the feature commit's line;
  upstream is `main`'s tip (`onto`).
- Past step 1 the panel adds "+ your commits replayed so far".
- At 150 columns the full lens labels fit. In the 120-column simulation
  test they fall back to `Yours · Picked · …` (N-075).
- gopls puts error dots (●) on all three marker lines. The `=======` label
  still shows there, so a diagnostic doesn't push it off the line.
- The status bar reads `⚠ rebase: 1 conflict`.

Colour captures in Tokyo Night, Darcula and Solarized Light (the two
themes where the label colours get boosted for contrast) were published as a private
artifact: https://claude.ai/artifact/Vg9ffabHD7PJj7XBxKPfue. This is
the capture tool's terminal emulator, not tmux, cats or Terminal.app; that
hand check stays in N-047.

## Also committed

Uncommitted work that was already in the tree at session start (untouched
for an hour, green under the full suite) was committed separately as
`682ca3d Recent folders: Local and Global as two picker panes`
(palette.go `openPickerPanes`, folder.go, recentlocations.go + their tests,
and its own CLAUDE.md / README hunks, staged per hunk).

## Next

Closed: None. Declined: None. Raised: N-075.
Deferred: None. Promoted: None. Moved: None.
Updated: N-047. Full list: `ai_docs/todo/next-list.md`.
