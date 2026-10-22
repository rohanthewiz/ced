# Find prompts: match case + whole word chips (N-083)

Session: `b714bbef-8783-4d36-a256-6a420acb5775`

## Ask

"do N-083" — the three text-find prompts (Find all in file, Find in
project, Find all in folder) carried only a `[regex: …]` chip; match case
and whole word were reachable only from the find bar and ≡ Find, which a
keyboard-owning prompt can't reach. Then `/sw`.

## What shipped

### Chips (internal/app/find.go)

`openFindPrompt` now builds three `promptExtra`s, keyed by the find bar's
own chords so there is one set to learn:

```
[ Cancel ]   [  OK  ]   [case: off] [word: off] [regex: off]
                           alt+c       alt+w       alt+r
extras order (right→left):   [2]         [1]         [0]
```

- Each flips the App's toggle (`toggleFindCase` / `toggleFindWord` /
  `toggleFindRegex`), not a prompt-local copy — the callbacks read
  `app.findOptions()` when the search runs, and the next bar agrees.
- Regex is `extras[0]`: keeps the right edge (where the bar puts `.*`)
  and is the LAST chip dropped on a narrow terminal — it changes what the
  typed text means; case/word only narrow. `fireExtraKey` isn't gated on
  `extraRects`, so dropped chips' chords still work.
- Widths are EXACT (`findPromptRegexChipWidth` 12, case 11, word 11 —
  the regex chip lost its old spare cell). Arithmetic in
  `promptModal.width`: 43 + 13 + 12 + 12 − 1 = 79, which fits an 80-col
  terminal. One spare per chip would be 82 → the modal refuses to grow
  and two chips vanish on the most common size.
- Hint: `<hint> · alt+c case · alt+w word · alt+r regex`; at 79 cols the
  right-aligned "↑ recent" still fits.

### Hint clipping (internal/app/modals.go)

The prompt's hint row is drawn through `clipRunes(m.hint, mw-4)`. Needed
for the longer hint on a non-grown (54-col) prompt, and fixes a latent
bug: a deep folder in "Find all in folder"'s hint painted across the
frame and the screen beside it.

### Tests

- find_test.go: `TestFindPrompt_CaseAndWordChips` (alt+c/alt+w flip, no
  field leak, labels report state), `TestFindPrompt_ChipWidthsCoverEveryLabel`,
  `TestFindPrompt_AllChipsFitEightyColumns` (3 rects, case < word < regex,
  drawn side by side, click flips in place),
  `TestFindPrompt_NarrowKeepsRegexAndEveryChord` (60 cols → regex only,
  alt+w still works).
- modals_test.go: `TestPromptDraw_ClipsALongHint`.
- projectsearch_test.go: the regex-chip test now expects three extras.
- `make test` green.

### Real binary

`run-ced` at 80×24, Esc-F on find.go: the prompt grows to 79 columns,
shows `[case: off] [word: off] [regex: off]` and the full hint with
"↑ recent"; alt+c → `[case: on]`.

### Docs

README find section names the three chips + keys; CLAUDE.md Find section
gains the chip rule (exact widths, don't pad —
`TestFindPrompt_AllChipsFitEightyColumns`).

## Next

Closed: N-083. Declined: None. Raised: None. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
