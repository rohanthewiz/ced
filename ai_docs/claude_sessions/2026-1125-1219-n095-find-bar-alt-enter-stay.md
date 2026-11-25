# N-095 — Alt+Enter in the find bar closes it and stays on the hit

Session: `76e7e593-cda5-4a15-8ad1-97b012b9265a`

## Ask

Pick up next-list item N-095: the find bar's Esc goes back to where the
bar opened, so there was no KEYBOARD way to close it and stay on a hit
(only a click on the hit's line, or `↓` into the Find-all list and
Enter). Add a bar chord (Alt+Enter) that closes and keeps the caret,
plus its hint word.

## What landed (`abf6d6c`)

- **find.go** — `stayFind`: the bar's Alt+Enter. It is `closeFind`
  minus `escapeFind`'s restore (closeFind already zeroes `findFrom`),
  kept as its own verb so the key case names the intent. The caret stays
  where the search put it (hit start, no selection — `FocusCurrentMatch`).
  `handleFindKey`'s Enter case reads Alt FIRST, so on the replace row it
  closes without rewriting the hit. `escapeFind`'s doc and the key table
  in `handleFindKey`'s doc name the new door.
- **Hint** — both rows end with `alt+Enter: stay`. The hint is now
  segments (`findBarHintSegs`) joined by `fitHint`, which keeps the
  longest run of LEADING segments that fits, so a mid-width window sheds
  the new word before losing the whole hint. Measured beside the default
  tree: a 140-column window shows the old hint minus the new word; at
  100–120 columns (bar 70–90 cells), where the old all-or-nothing hint
  showed nothing, the first words now show.
- **The legacy Alt+Enter spelling (bug found on the way)** — on tmux / a
  terminal without CSI-u, Alt+Enter is ESC CR, which tcell 2.13.9's
  ESC + control-char fold reports as KeyRune 'm' with ModAlt|ModCtrl
  ('j' for LF). handleKey already rewrote that to KeyEnter+ModAlt, but
  only just above its leader branches — AFTER the modal and find-bar
  dispatch — so in the bar it reached `textField` and typed an "m" into
  the query. The rewrite is now `foldAltEnter` (altchords.go), called by
  handleKey in the same place and at the top of `handleFindKey`.
  Deliberately NOT hoisted above the modal dispatch: inside tmux a quick
  "Esc, Enter" arrives as the same ESC CR, and folding it for a dialog
  would turn "cancel, then a newline" into a SUBMIT of the dialog's
  default. In the bar the same accident only means "close and stay".
  Modals still see Alt+Ctrl+'m': probed after the commit, the Go to line
  prompt types an "m" (not submitted) — raised as N-152.
- **Docs** — README Find in file: both diagrams (the replace one also
  gained the `.*` it was missing and its `Replace   All` re-aligned),
  the Esc bullet names `Alt+Enter`, and the "Alt keys that aren't button
  twins" note under `#### Alt chords inside dialogs` lists it (it is not
  a rune chord, so it stays out of the pinned table). CLAUDE.md's find
  rule records `stayFind` and why the bar calls `foldAltEnter` itself.

## Tests

- `TestFindBar_AltEnterStaysOnTheHit`, `TestFindBar_AltEnterOnReplaceRowDoesNotReplace`.
- `TestFindBar_LegacyAltEnterStaysAndTypesNothing` — through `handleKey`
  with Alt+Ctrl+'m'; confirmed it FAILS with the bar's fold commented out
  (query "needlem").
- `TestFoldAltEnter_RewritesOnlyTheLegacySpelling`,
  `TestFitHint_ShedsSegmentsFromTheEnd`,
  `TestDrawFindBar_HintNamesTheStayChord` (220 cols: the word on both
  rows; 140 cols: the old hint intact, only the new word shed). A first
  draft guarded the narrow case with `t.Skip`; replaced by the measured
  width per the no-Skip rule.
- `make test` (race) green.

## Real binary inside tmux

Through the run-ced docker harness (tmux 3.5a): open main.go, `Esc f`,
type `Println`, send `{esc}{enter}` as one write, then type `Z`.
Alt+Enter → `fmt.ZPrintln` (stayed on the hit); the same script with a
plain `{esc}` → `Zpackage main` (went back). main.go left clean. Added
as the `N-095 find bar alt+Enter through tmux stays on the hit` check
in `.claude/skills/run-ced/tmux/checks.sh` (undoes its Z before quit);
the whole suite: 10 passed, 0 failed.

## Next

Closed: N-095. Declined: None. Raised: N-152.
Deferred: None. Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
