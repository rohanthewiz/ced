# Find: a click on code goes there; Esc goes back (find bar too)

Session: `7946257e-90b4-4f6b-a1b7-0bf9d7358e6c`

## Ask

1. "If the find all dlg or it's original interactive search input is
   dismissed with a click elsewhere and the click is on a line in an edit
   window, then go to that line."
2. "If any find dlg or input is dismissed with ESC then revert to the
   previous location. Try to maintain the other find all dialog rules
   where they make sense."

First pass made Esc ALWAYS go back everywhere, retiring the Find-all
list's pick rule (N-090/N-092). Asked about it, the user chose: "Keep Esc
after a pick as an exception" and "go with the new code for the Find bar".

## What shipped

### Click on a line of code (findall.go, findalllive.go, modals.go)

- `App.editorLineAt(x, y)`: the buffer position a press lands on in the
  active editor, against the layout on screen NOW. Refuses off the text
  area, past the last line, images and markdown previews.
- Unpinned Find-all list: an outside press goes through `settleAt` first.
  On a line of code it hands back the tint, closes, and moves the caret to
  the clicked position. The position is resolved BEFORE the close because
  the list takes rows (or columns) out of the editor band. No drag is
  started, for the same reason. Any other outside press still accepts, as
  before. Works in project mode (it lands in whatever file is active and
  clears the project tints).
- Live prompt: new `promptModal.onClickAway` hook, asked before the
  outside-click cancel. `findAllLive.clickAway` restores the find state
  only, KEEPS the view the user clicked in (the preview's scroll), and
  moves the caret. Any other outside press is the ordinary cancel.
  `restore` was split into `restoreView` + `restoreFind`.

### Esc goes back

- Find-all list: unchanged. Esc goes back until a row is PICKED (click or
  keyboard walk), then stays. The pick is documented as the one exception
  to "Esc goes back" across the find surfaces.
- Live prompt: unchanged (already went back).
- Find bar (find.go, app.go): new `viewMark` (tab by pointer, caret,
  anchor, scroll) captured as `App.findFrom` in `openFind`. Esc runs
  `escapeFind`: close, then `restore`, which only acts on the same,
  still-active tab and only when the caret moved (a wheel-only scroll is
  not yanked back). An editor press while the bar is up zeroes `findFrom`,
  so click-then-Esc stays. `closeFind` and `closeAllModals` zero it. Hint
  "Esc: close" → "Esc: back".
- Bar → Find all inherits the bar's origin. `openFindAllFromBar` (↓)
  overwrites the list's origin with `findFrom` (the list still opens on
  the bar's hit). `openFindAll` hands it to the live prompt (`inherit`).
  Esc then goes back to before the search, not to the bar's last hit.

### Docs

README (find bar Esc bullet and hint rows, Find all paragraph, live
prompt sentence) and CLAUDE.md (the Find-all PEEK bullet, the live-prompt
exit note).

## Tests

- findall_test.go: `TestFindAll_ClickOnCodeGoesToThatLine` (top and right
  dock), `…ClickOnCodeInProjectModeGoesThere`, `…ClickPastTheLastLineAccepts`,
  `TestEditorLineAt_RefusesOffTheCode`, plus a `codeClickT` helper. The
  original pick tests are kept unchanged.
- findalllive_test.go: `…ClickOnCodeGoesThere` (view kept, find state
  restored), `…ClickOffTheCodeCancels`, `…InheritsTheFindBarOrigin`.
- find_test.go: `TestFindBar_EscGoesBackToWhereItOpened`,
  `…EscRestoresTheScroll`, `…EditorClickThenEscStays`,
  `…EscAfterATabSwitchLeavesBothAlone`, `…DownListEscGoesBackToTheBarOrigin`,
  `TestViewMark_RestoreOnlyWhenTheCaretMoved`.
- modals_test.go: `TestPromptClickAway_HookAnswersBeforeTheCancel`.

## Verification

- `make test` (race) green after the final change.
- run-ced PTY (120×40, goto.go, `strconv`):
  - list + click on row 37 → list gone, Ln 39 (the line drawn there)
  - live prompt + click on row 5 → prompt gone, Ln 14, view kept
  - list ↓ then Esc → stays at Ln 83 (pick exception)
  - list plain Esc → Ln 1
  - footer reads "esc back"
- `gofmt -l` still lists findalllive_test.go (predates this work).

## Next

Closed: None. Declined: None. Raised: N-095.
Deferred: None. Promoted: None. Moved: None.
Updated: N-094. Full list: `ai_docs/todo/next-list.md`.
