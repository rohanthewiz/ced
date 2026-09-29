# Session: Search history dropdown on every find input

Session ID: 7961304d-ff1e-4027-90f4-60fdaef0465a
Date: 2026-09-29

## 1. The prompt

> Please add a history dropdown for all finds where possible

## 2. Surveying the find surfaces

Inputs that ask for a search:

- Find bar — query row (`findField`) and replace row (`replField`), App
  fields, the bar owns the keyboard but is not a modal.
- `promptModal` prompts — Find all in file (`openFindAll`), Find in
  project (`menuFindInProject`), Go to symbol in project
  (`menuGoToWorkspaceSymbol`).
- Find-all list's filter + replace boxes, git log filter, tree
  type-to-find, references filter — FILTERS over results in hand, not
  searches; left without a dropdown (the Find-all replace box records
  into the replacement list, but has no dropdown — N-031).

## 3. Persistence: `search_history` in `.ced/history.bytdb`

`internal/history/searches.go` + `db.go`:

- Lists per KIND, MRU, exact-match dedupe, `MaxSearches` 25:
  `find` (shared by find bar, Find all, Find in project — same question),
  `replace`, `symbol` (a fuzzy server-matched name, not text).
- Refused: empty, line breaks, over `MaxSearchBytes` (512).
- Table `search_history(k PK, last)`, `k = "<kind>:<text>"` (split at the
  FIRST `:`; kinds never contain one). Counter `search_seq` in
  `history_meta`.
- Same recipe as the recent-file ring: touched entries re-stamped from
  the stored counter, list order oldest first; `ForgetSearch` deletes;
  trim per kind after the merge (a full find list never evicts replace
  rows). Two instances ADD (`TestSearches_TwoInstancesAdd`).
- History owns the lists outright (unlike the recent-file ring, which the
  App owns). Compaction-ceiling comment updated (worst case ≈38KB more).

## 4. The dropdown (`internal/app/searchhistory.go`)

- `histDrop` state held BY VALUE by its owner (App.findHist for the bar,
  `promptModal.hist`); `histDropGeom` is the one geometry for draw, keys
  and mouse. Opens on the preferred side, flips when that side can't
  hold it and the other has more room, shrinks rows, never covers the
  status bar. Newest entry is always nearest the field.
- Keys: Up / ▾ click opens (Down stays "list all" in the bar); arrows
  move VISUALLY; past the newest closes; Enter/Tab/click FILLS the field,
  never submits (a recalled query is often the start of the next);
  Delete / row `×` forgets; Esc closes only the list; any other key
  closes and is typed. Empty history flashes "No recent … yet".
- Mouse: hover tracks, wheel walks, outside press closes and falls
  through (completion popup contract).
- Why not `openPicker`: the palette takes the modal slot, which closes the
  find bar / IS the prompt — it would destroy the field being filled.
  Recorded in CLAUDE.md as the second exception beside Find-all.

## 5. Wiring

- Find bar (`find.go`): labels `Find▾` / `Repl▾` are the buttons (same
  7-cell width, no input width lost); list anchored at the bar's TOP for
  both rows so the replacement list never covers the query; hints gain
  `↑: recent`. Records: Enter/Shift+Enter, `closeFind` and
  `closeAllModals` via `rememberFindBar`, `Close()` before `writeHistory`,
  successful replace/replace-all (`rememberReplace`: both halves).
  Router: `findHistMouse` right after the menu block (gated on
  `dragMode == ""`); `drawFindHist` in the overlay layer before
  menu/modals.
- Prompt (`modals.go`): `openSearchPrompt(title, hint, initial, kind, cb)`;
  ▾ at the field's right end on the field BG (2-cell hit), "↑ recent" on
  the hint row when it fits; dropdown asked BEFORE the outside-click-
  cancels rule (the list hangs past the box); drawn last.
- Recording lives where searches RUN: `showFindAll` (before the miss
  check — a miss here is often wanted elsewhere), `startProjectSearch`
  (after the index checks), `startWorkspaceSymbols`, find-all
  `confirmReplace` commit success.

## 6. Bug caught by the tests

A pick zeroes the `histDrop`, and `findHistPick` read the field from the
dropdown's kind AFTER that — a replacement pick landed in the Find row.
Fixed by passing the field in (`findHistPick(field, v)`);
`TestFindBar_LabelClickOpensTheRowsList` failed before the fix.

## 7. Verification

- `internal/history/searches_test.go` (8 tests), 
  `internal/app/searchhistory_test.go` (20 tests); `find_test.go`
  updated for the `Repl▾` label.
- `make test` (race) green; `go vet ./...` clean.
- Real binary via run-ced on a scratch copy of main.go: bar dropdown opens
  above `Find▾` with three recorded queries; a second run's Esc-P + Up
  showed them again (persisted), opening below the prompt field.
- README Find-in-file section: `Find▾` sample + a history bullet.
  CLAUDE.md: picker-rule exception + a "Search history" design block.

## Next

Closed: None. Declined: None. Raised: N-031, N-032.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
