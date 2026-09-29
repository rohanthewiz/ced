# Session: README find section, blame name disambiguation, Find-all replace history

Session ID: 950051ed-c588-4cb2-9269-d807a032e394
Date: 2026-09-29

Three next-list items from the cats-todo backlog, one after another:
N-032, N-033, N-031. Commits: `4c158d9`, `46cde57`, `b131bc0`.

## 1. N-032 — README `### Find in file` was stale

> README `### Find in file` is stale beyond this session's bullet: it
> still says there is no whole-word or case-sensitive toggle (the bar has
> `Aa` / `|W|`) and never mentions the replace row, ↓ list-all, or Find
> in project. An instance of N-003.

Rewrote the section against the code (find.go, findall.go,
projectsearch.go, goto.go, leader.go, app.go menu rows, metakeys.go):

- Bar mockup in its real order (`Find▾`, hint, counter, `Aa |W|`).
- `Aa` / `|W|`: click, `Alt+c` / `Alt+w`, the ≡ Find rows; session-only.
- Selection seeding, red counter on no match.
- Replace row: `Esc e` / ≡ / `Tab`; `Enter` or Replace = this hit and
  advance; `Alt+a` or All = every hit; one undo step. Second mockup.
- Find-all list (`Esc F`, ↓ in the bar): peek, Esc restores, `d` dock,
  bottom-border resize, `p` pin → filter `/`, dismiss, `⟳`, replace.
- Find in project (`Esc P` / `⌘⇧F`): finder index so `.gitignore`
  applies; keyboard walk opens nothing; a click opens and KEEPS the list,
  Enter opens and closes it; 10,000 cap, title "first 10000".
- Go to line (`Esc j` / `⌘G`): `line:col`, and a pasted `app.go:314:22`
  (filename ignored — `parseLineSpec` resets on a non-numeric field).
- Hotkey table gains `Esc F`, `Esc e`, `Esc P`, `Esc j`.

Two claims were corrected by reading the code before committing: project
search's single click does NOT close the list, and go-to-line doesn't
open the named file.

Found while verifying: the bar's case/word options stop at the bar —
`showFindAll` calls `editor.FindAll` (zero options) and `search.Options`
has no case/word fields. README says so; raised as **N-035**.

## 2. N-033 — blame showed given names only

> Blame shows the author's GIVEN name (`blameGivenName`) in both styles;
> the IDE look the bands style copies shows the surname ("Allison").
> Switch, or make it a choice, if the first names prove ambiguous on a
> team.

Asked the user (switch / choice / disambiguate / close). Chose
**disambiguate**: keep the given name, add surname letters only where two
DIFFERENT authors in the same file share it. No config key, no ≡ row.

- `disambiguateBlameAuthors` runs at the end of `parseBlamePorcelain`
  over the whole file (decision 2 of gitblame.go: labels and the column
  width, measured afterwards in `newFileBlame`, never depend on what is
  scrolled into view). Groups distinct `Full` names by label, first-seen
  order.
- `blameSurnamedLabels` grows the surname prefix until the group is
  distinct: "Rohan A." / "Rohan B."; "Rohan Al." / "Rohan Ad."; a whole
  surname drops the dot ("Ada Ng" / "Ada Ngu." — compared dot-stripped
  so "Ng" vs "Ng." doesn't count); one-word names keep their label;
  surname = LAST word (middle names skipped). The given name is elided to
  keep labels within `blameAuthorMax` (10); the prefix stops growing
  before the given name would drop under two cells.
- Uncommitted "you" lines never collide; identical spellings stay alike.
- Tests: `TestDisambiguateBlameAuthors_*` (4) and
  `TestParseBlamePorcelain_DisambiguatesSharedGivenNames` (fails with
  the call removed). CLAUDE.md blame bullet states the rule.
- README has no blame section at all — left for N-003.

## 3. N-031 — Find-all replace box had no history dropdown

> The Find-all list's replace box RECORDS into the replacement history
> but has no dropdown of its own (it sits in a narrow shared row, pinned
> or not). The list's filter box, the git log filter and the references
> filter have no history either — filters, not searches. Add if asked.

Did the replace box; left the filters without history (the item's own
reasoning).

- `findAllModal.replHist histDrop`. `Up` in the replace box
  (`handleFieldKey`, which both routes reach — modal slot and the pinned
  panel's router branch) or a click on the `⇄▾` label
  (`replHistBtnRect`, was `⇄ `) opens `history.SearchReplace`, below
  the box (`replHistGeom`, preferUp=false). A pick fills and focuses the
  box, never runs "Replace in N". Up in the filter box stays a no-op.
- Unpinned: asked first in `handleMouse` (after a live resize), drawn
  last in `draw`. Pinned: the router only hands the panel clicks inside
  its frame, so `findAllPinHistMouse` is asked right after
  `findHistMouse`; `drawFindAllPinHist` paints from the overlay pass
  after `drawFindHist`. `closeAllModals` closes it (the panel stays).
- **Bug found on the way**: a dropdown's own button could never close
  it. `histDropMouse` treated the press on the button as an outside
  press — closed the list, handed the press on — and the button then
  reopened it. That broke the find bar's promised "second click puts the
  list away" and the prompt `▾`. `histDropMouse` now takes the owner's
  `toggle btnRect` and consumes a press on it; the find bar's own toggle
  branch was dropped (a click reaching it means the list is shut or on
  the other row).
- Tests: 7 in findall_test.go (`TestFindAll_Replace*`,
  `TestFindAll_FilterBoxHasNoHistory`,
  `TestFindAll_PinnedReplaceHistHangsOverTheEditor` — `findAllRows = 3`
  so the list really hangs past the panel,
  `TestCloseAllModals_ClosesThePinnedReplaceHist`) and 2 in
  searchhistory_test.go (`TestFindBar_SecondLabelClickClosesTheList`,
  `TestSearchPrompt_SecondButtonClickClosesTheList`). The three toggle
  tests fail with the fix disabled.
- CLAUDE.md search-history bullet, README find section, searchhistory.go
  header updated.

`make test` (race) green after each item. `internal/app/hovermodal.go`
still not gofmt-clean (N-034, untouched).

## Next

Closed: N-031, N-032, N-033. Declined: None. Raised: N-035.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
