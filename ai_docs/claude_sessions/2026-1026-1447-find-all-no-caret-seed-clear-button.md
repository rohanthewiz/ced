# Find all in file: no caret-word seed; a × / Alt+x clear on find prompts

Session: `79f46cd7-ea3f-4565-8d17-569d7164381d`

## Ask

1. "do not seed find all in file with the word under the cursor unless
   that word is actually selected."
2. "Also give me an "x" to clear the selection for find all just in
   case" (read as: a clear button on the prompt's field, for an unwanted
   pre-fill).

## What shipped

### No caret-word seed (findall.go, projectsearch.go)

- `findAllPromptSeed` now returns ONLY the find bar's query (when the bar
  is open with text). The caret-word lookup moved out into
  `caretWordSeed`.
- Why: the in-file prompt is LIVE (findalllive.go). It searches its
  pre-fill the moment it opens, tinting the file and moving the caret to
  a preview. A word the caret merely sits in was never pointed at. A
  single-line selection still runs silently (`findAllSelectionQuery`).
- Find in project KEEPS the caret word (`projectSearchSeed`: selection →
  bar → `caretWordSeed`). Its prompt is inert, so a guess costs nothing
  until Enter. The editor right-click search row (`contextSearchWord`)
  also keeps it: its label names the word, so clicking it is a choice.

### Clear button (modals.go, find.go)

- `promptModal.clearable`: a `×` in the field's last two cells, just
  left of the history `▾` (`clearBtnRect`). It is drawn and hit only
  while the field holds text. The text keeps out of those cells even when
  the × is hidden (`textSpan`), so nothing reflows when it appears.
- Keyboard twin is Alt+x (`promptClearKey`), checked after the extras and
  always consumed. Clearing is an EDIT (`onEdit` hears ""), so the live
  prompt settles back to the origin with no tint. An empty field is left
  alone (no re-ask).
- Set on every `openFindPrompt` prompt: Find all in file, Find in
  project, Find all in folder.
- The hint gains "· alt+x clear". The in-file hint went "lists every
  occurrence" → "lists every match" because the old one clipped by one
  cell (`alt+x cle…`) in the real binary.

### Docs

README (Find-all pre-fill sentence, prompt chips paragraph), CLAUDE.md
(the Find-all opening bullet, the find-prompts chips bullet), plus the
comments in leader.go, metakeys.go and the findalllive.go diagram.

## Tests

- findall_test.go: `TestFindAll_PromptSeedIsTheBarOnly` (renamed from
  `…PrefersBarThenWord`); `TestFindAll_AsksWhenNothingIsSelected` now
  wants a blank prompt, an unmoved caret and no tint (fails before the
  fix); new `TestFindAll_SelectedWordStillSearches`.
- findalllive_test.go: the seed test seeds from the find bar; new
  `TestFindAllLive_ClearPutsTheViewBack`.
- modals_test.go: `TestPromptClear_ButtonAndChordEmptyTheField` covers
  the × drawn at its rect, the click, Alt+x, the empty no-op, and a plain
  prompt having no ×.

## Verification

- `make test` (race) green; the find/prompt tests re-run after the hint
  edit.
- run-ced PTY (120×40, goto.go, Esc F): the prompt opened blank with the
  caret on a word; after typing `strconv` the field showed `× ▾` and the
  hint fit; `{esc}x` emptied the field and the × went.
- `gofmt -l` still lists findalllive_test.go and userconfig_test.go;
  both predate this session and were left alone.

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None. Updated: N-094.
Full list: `ai_docs/todo/next-list.md`.
