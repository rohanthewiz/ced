# N-108, N-110: right-click menu splits; ⌘ and right-click README pins

Session: `5448be56-e10b-47e5-87c5-e082f76486a3`

## Ask

`/work-next-list`, fourth batch (triage in
`2026-1104-1308-n080-n098-n089-label-plural-cmd-o`). Done together
because both touch the three right-click menus.

## What changed

### N-108 — splits in the right-click menus (`9c42605`)

- **Decision (minor):** a split is a ROW KIND (`split: true` on
  `editorContextItem` and the tree's `contextItem`), not the ≡ menu's
  `splitAbove` flag. `ctxScroll` maps one screen row to one item, and
  CLAUDE.md makes `ctxScroll.pointer` the only row↔screen mapping; a
  flag would have meant a display-row index through pointer / reveal /
  offset and every opener's height. The item text itself suggested "a
  separator row kind that is never selected".
- Contract: never hovered (draw skips the highlight), the arrows skip it
  (`usable` on the editor chassis; the tree's Up/Down gained `step`), a
  click on one is inert and the popup stays (the tree's `activate`
  would otherwise have closed the menu on it). `drawCtxSplit` paints the
  ≡ menu's `┄` in `menuSplitColor`.
- `trimCtxSplits` (generic over both row types) drops leading, trailing
  and doubled splits, so builders place one at every boundary.
- Runs: tree — make/find here · change the entry · copy · copy its name
  · how it's drawn · collect (marks + favorites + tab group, merged into
  one run to avoid singletons) · take it outside (Refresh and Run stay
  with it, per their comments). Tab — the five its own comment already
  named: look · change · place · copy · close. Editor — conflict ·
  symbol · text · cats splits · hand the selection off (cats agents,
  cats chat, Summarize, GoNotes) · drawing · places (bookmarks, search)
  · repo.
- Real binary (run-ced): a right-click on a tree folder shows seven runs
  divided by `┄` rules.
- Tests: `TestTrimCtxSplits`, `TestEditorContext_SplitRowsAreInert`,
  `TestTreeContext_SplitRowsAreInert`; the exact-list pins
  (`TestOpenTreeContext_*`, `TestTabContext_RightClickOpensTheTabMenu`)
  gained `""` cells; `TestTabContext_RowsDimWithoutAPath` skips splits
  (no predicate).

### N-110 — README pins, partly (`d5efa48`)

- `#### Command-key accelerators` (under Hotkeys) replaces the prose
  chord list with a table: Chord · Action · Same as.
  `TestREADME_CommandKeyTableListsEveryChord` pins it to `metaAccels`
  (Action = label), with the chords dispatched elsewhere (⌘ arrows,
  ⌘C/⌘V/⌘Z) named in `metaChordsOutsideTable`; every row must name its
  other door.
- `### Right-click menus` with `#### File tree`, `#### Tab`,
  `#### Editor` tables (Row · Shown · What it does), pinned by
  `pinRightClickREADME`. The menus are state-built, so each test drives
  a fixture that reaches every row: tree — root, a ticked folder, a
  Markdown file, an executable, in a repo with a file clip; tab — plain,
  grouped and Markdown tabs inside cats; editor — a conflict with a hunk
  and a bookmark at the caret, a diff3 block (Accept base, `Compare
  sides…`), a selection at cats Tier 1 (`withCtlSpy`) with an agent
  pane, a Markdown file. Placeholder forms rank by literal length so
  `Accept both, <side> first` beats `Accept <side>`. Seen failing both
  ways on a renamed README row.
- Fixed on the way: metakeys.go still said cats lacked `KeyA` (it has
  had it since `bd18905`).
- **Not done, and why (N-110 stays Open, text updated):** the in-surface
  Alt chords of modals and the find bar. Each prompt builds its
  `promptExtra`s inline, and the bar's chords are a switch, so there is
  nothing to enumerate; a both-ways pin needs an enumerator or a shared
  table first.
- CLAUDE.md's README-pin rule lists the new pins and the
  fixture-must-reach-every-row rule.

## Tests

`go test ./...` + `go vet` before each commit; run-ced for N-108.

## Next

Closed: N-108. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: N-110. Full list: `ai_docs/todo/next-list.md`.
