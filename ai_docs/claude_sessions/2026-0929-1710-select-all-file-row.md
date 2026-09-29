# Session: "Select all" row in ≡ File too

Session ID: 63939c12-bf96-427d-852e-92c8113c07dc
Date: 2026-09-29

## 1. The prompt

From the cats-todo backlog — next-list item N-008:

> Optional: a "Select all" row in ≡ **File** too, if "File | Edit" meant
> both groups. Today it is in Edit only. One line plus the menu pins.

## 2. What already existed

- ≡ Edit row `{label: "Select all", action: menuSelectAll, enabled:
  hasSelectableTab}` (from `2026-0913-2142-select-all`), plus the editor
  right-click row and `⌘A`, all funnelling into `selectAllInFile`.
- `paletteActionItems` (palette.go) flattens `visibleMenuGroups()` with no
  dedupe — a second identical row would appear twice in the palette.

## 3. What changed

- **≡ File row** (app.go): "Select all" under "Copy relative/absolute
  path" — same action and predicate as the Edit row, so the two can't
  drift. Placed with the Copy-path rows because, like them, it feeds the
  clipboard.
- **`menuItemDef.paletteTwin`** (app.go): marks a row that deliberately
  repeats another ≡ row in a second section. The menu draws it; the
  palette skips it (palette.go), so Select all is listed once.
  - First attempt deduped the palette by (label, `reflect` code pointer)
    — dropped: plugin-command and custom-action rows are closures from
    ONE func literal, so they share a code pointer and two distinct
    commands with the same label would have merged. The explicit flag has
    no such failure mode.
- `selectAllInFile` doc comment names both ≡ rows.

## 4. Tests

- `TestMenuSelectAll_InFileAndEdit` (app_test.go) — the row is in both
  File and Edit, only the File copy is `paletteTwin`, it is dimmed with no
  tab and selects the whole buffer through the File row.
- `TestPalette_TwinRowListedOnce` (palette_test.go) — one "Select all" in
  the palette with a text tab open. Verified to FAIL with the flag off.
- Menu pins: `TestMenuLayout_NoCustomActions` now 150 group actions / 167
  rows / height 173 / dividers `[2, 5, 170]`;
  `TestMenuLayout_WithCustomActions` height 176. CLAUDE.md updated to match.
- `make test`: first full run green. A later full run failed once in
  `TestCommitPromptTrailer_CommitsWithAndWithoutIt` (git commit test,
  untouched by this change); it passed 5/5 alone under `-race` — looks
  load-flaky.

## 5. Docs

- README: the "some verbs have no Esc key" paragraph now says Select all
  is also under ≡ File.
- next-list: N-008 moved to Closed.

## Next

Closed: N-008. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
