# Session: Find options reach the Find-all list and project search (N-035)

Session ID: 97714993-a5cc-4008-bc8e-87f06eff06ab
Date: 2026-09-29

One next-list item from the cats-todo backlog: N-035.

## N-035 — `Aa` / `|W|` stopped at the bar

> The bar's `Aa` / `|W|` options stop at the bar: ↓ list-all
> (`showFindAll` calls `editor.FindAll`, zero options) and Find in project
> (`search.Options` has no case/word fields) always match a
> case-insensitive substring, so a `Aa`-on search turned into a list
> quietly widens.

### What changed

- **One conversion** — `App.findOptions()` (find.go) turns `findCase` /
  `findWord` into `editor.FindOptions`; `applyFindOptions` uses it too.
- **Find-all list** (findall.go) — `showFindAll` matches with
  `FindAllOpts` and SNAPSHOTS the options on `findAllModal.opts`. ⟳
  (`rerun`), the stale check and the replace plan all reuse the
  snapshot, so a toggle flipped under a pinned list doesn't change the
  question the list answers. The borrowed tint now goes on under the
  list's options (`tab.SetFindOptions(opts)` before `SetFindQuery`) —
  otherwise preview's `FindIndex = row` would point at the wrong match
  when the tab's pushed copy lagged the toggles. `priorOpts` is captured
  and restored verbatim with the rest of the tab's find state.
- **Project search** — `search.Options` embeds `editor.FindOptions`
  (so a future toggle reaches it without the package changing);
  `searchFile` calls `FindAllOpts`. `startProjectSearch(query, opts)`:
  the prompt callback and the editor right-click row pass
  `app.findOptions()` at run time, ⟳ passes the snapshot. The event
  carries the options onto the modal. `tintForProjectFind` installs the
  list's options on the opened tab and records `priorOpts`;
  `clearProjectFindTints` restores them before restoring the prior query.
- **Announced** — `findOptionsNote` gives " (match case, whole word)" or
  "" under defaults. Used in `titleText` (text searches only; heading
  producers like References never get it), the "Searching N files…"
  flash, and both "no occurrences" flashes. Words, not glyphs, because
  it sits in prose, and the lists are usually open with no bar on screen.

### Bug found on the way

`rowStale` and `buildReplacePlan` compared the row's text to the query
byte-for-byte. In the default case-insensitive list, a `Count` hit for
`count` was dimmed as stale and Replace silently skipped it (confirmed
with a probe test before touching it). Both now call the new
`editor.MatchesAt(line, col, query, opts)`, which folds and bounds
through the same helpers as `matchCols` (whole-word check factored out
as `wordBounded`). `TestMatchesAt_AgreesWithTheScanner` pins it against
`FindAllOpts` column by column under all four option combinations.

### Tests

- editor: `TestMatchesAt_AgreesWithTheScanner`,
  `TestMatchesAt_RejectsOutOfRangeAndEmpty`.
- search: `TestProject_HonoursMatchCaseAndWholeWord`.
- app: `TestFindAll_HonoursTheBarsOptions`,
  `TestFindAll_DefaultTitleHasNoOptionsNote`,
  `TestFindAll_MissNamesTheOptions`,
  `TestFindAll_CaseVariantRowsAreNotStale` (the bug),
  `TestFindAll_StaleCheckUsesTheListsOptions`,
  `TestFindAll_RerunKeepsTheSnapshotOptions`,
  `TestFindAll_RestoreGivesBackTheTabsOptions`,
  `TestProjectSearch_CarriesTheOptionsThrough`,
  `TestProjectSearch_ReferencesTitleHasNoOptionsNote`,
  `TestFindOptionsNote`, `TestFindOptions_MirrorsTheToggles`.

`make test` (race) green.

### Docs

- README: the "list and project search match case-insensitive
  substrings" line now says they follow the bar's toggles, name them in
  the title, and ⟳ keeps them.
- CLAUDE.md find rules: options snapshot on `findAllModal.opts`,
  `findOptionsNote`, staleness via `MatchesAt`; project matcher is
  `FindAllOpts` with `search.Options` embedding `editor.FindOptions`.

## Next

Closed: N-035. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
