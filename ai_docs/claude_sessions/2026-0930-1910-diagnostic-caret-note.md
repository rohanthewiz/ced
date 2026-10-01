# Session: diagnostic message on the caret line; JSON errors explained

Session ID: d935bafc-2250-498f-b3e8-b031227330f4
Date: 2026-09-30

## 1. The ask

From the cats-todo backlog: "If there is say a JSON validation error at a
particular line, I see a red underscore, but I have no way of knowing
what it's telling me, or even if it is an inaccurate indication."

## 2. Diagnosis

The message WAS reachable. Reproduced in the real binary (`run-ced`, a
scratch `broken.json` with `"tags": ["a", "b",],`): hovering the
underline (SGR motion `{esc}[<35{semi}X{semi}YM`), clicking the gutter
`◇`, and Esc-i all opened the tooltip. Two real problems instead:

1. **Discoverability.** macOS Terminal + tmux report no mouse motion, so
   the hover never fires; the gutter click and Esc-i are invisible
   unless known. The natural gesture — clicking the red underline — only
   moved the caret (a code click is the caret's, by design).
2. **The mark looked wrong.** encoding/json rejects the `]` after a
   trailing comma, so the underline sat on a blameless `]` with "invalid
   character ']' looking for beginning of value" — reads as the editor
   being confused.

## 3. What landed

### Caret-line diagnostic note
- **`internal/app/diagnote.go`** (new): `stampDiagCaretNote` runs before
  every `tab.Render` (app.go draw). Picks diagnostics via `diagsAtCaret`
  (covering the caret, else starting on its line — now shared with
  Esc-i's `diagLinesAtCaret`), renders the WORST one as
  `✗ message  (+N more)` (`diagCaretNoteText`), colour from
  `diagSeverityColor`. Caret line only, every producer (LSP, plugins,
  ced's validator).
- **`internal/editor/linenote.go` / `tab.go`**: `Tab.SetCaretNote(line,
  text, fg)` — a separate slot from the inlay-hint map, guarded by
  EditRev AND the caret being on that line at paint time. Painted in the
  end-of-line slot, upright, no `»`; replaces that line's inlay note.
  `paintNoteText` is the shared placement for both note kinds.

### JSON errors explained
- **`internal/format/jsonexplain.go`** (new): `explainJSONError` keys on
  the parser's error class + rejected byte + previous non-blank byte and
  MOVES the mark onto the mistake: trailing comma → the comma; missing
  comma → end of the previous value; EOF → the unclosed opener / string
  quote (`jsonOpeners`, string- and escape-aware); mismatched closer →
  names the opener's line. Also comments, single quotes, unquoted
  keys/words, missing `:`, double commas, text after the document, raw
  line breaks/tabs in strings, bad escapes. Unrecognised shapes keep the
  parser's wording and position.
- `validate.go` calls it; the old Offset-1 comment rewritten.

### Tests
- New: `jsonexplain_test.go` (16 shapes, EOF openers, braces in strings,
  fallback, `prevNonBlank`), `diagnote_test.go` (caret-line note on/off,
  JSON trailing comma end to end, note text, `diagsAtCaret`), two caret
  note tests in `linenote_test.go`.
- Updated (deliberately — they pinned the old `}` position / raw
  wording): `TestValidate_LocatesTheBrokenCharacter` (now a mismatched
  closer), `TestValidate_OpeningABrokenFileMarksIt`,
  `TestValidate_ReparseRestoresTheFinding`,
  `TestDiagTooltip_ReadsEveryProducer`,
  `TestMenuHoverInfo_AnswersWithDiagnosticsWithoutAServer`.
- `make test` (race) green; gofmt clean.

### Verified in the real binary
```
3 ◇  "tags": ["a", "b",],  ✗ trailing comma: JSON allows no ',' before ']'
2 ◇  "a": 1  ✗ missing ',' after this value
```
Note in the theme's DiagError colour; comma underlined, `]` plain.

### Docs
- README: Diagnostics bullet leads with "put the caret on the marked
  line"; JSON bullet describes as-you-type marks in plain words.
- CLAUDE.md: Diagnostics + Data formats rules; architecture map gains
  `diagnote.go`, `jsonexplain.go`.

## Next

Closed: N-012. Declined: None. Raised: N-037.
Deferred: None. Promoted: None.
Updated: N-010. Full list: `ai_docs/todo/next-list.md`.
