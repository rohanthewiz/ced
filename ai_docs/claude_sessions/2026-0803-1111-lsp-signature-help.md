# Session: LSP signature help

Session ID: `9789547f-5a05-4315-9860-ce1688753ce9`
Date: 2026-08-03
Branch: `main`
Parent commit: `70f12b8`

---

## How this started

Same session as the references work, one request later. The queue left
by that commit named signature help as the next cheapest of stage 9's
remaining verbs — "hover's shape with a different trigger and a different
modal" — and that estimate held, with one exception the estimate didn't
see: **the trigger is the design problem, not a detail.**

---

## What was built

| File | What |
|---|---|
| `internal/lsp/types.go` | wire structs, `Signature` normal form, `ParseSignatureHelp`, `paramRange`, `markupText` |
| `internal/lsp/client.go` | `SignatureHelpAt()` + the capability |
| `internal/app/lspsignature.go` | the verb: request, render, land |
| `internal/app/hovermodal.go` | one new field — `emph []hoverEmph` |
| `internal/app/lsp.go` | `lspConn.SignatureHelpAt` |
| `internal/app/app.go` | dispatch case + the ≡ Code row |
| `internal/app/leader.go` | `Esc I` |

---

## It is MANUAL, and that is structural

Every other editor pops signature help automatically on `(` and `,`.
ced cannot, and the reason is a load-bearing property of its own design
rather than a shortcut:

**A modal here owns the keyboard.** That is the single-slot modal
interface working as intended — `App.modal` non-nil routes every key
through the modal, which is exactly what makes prompts, pickers and
confirmations reliable. A tooltip that appeared while the user typed
would therefore swallow the very next keystroke it exists to help them
write. Not "would feel janky" — would eat the character.

So the honest reading is that the automatic version of this feature is a
**different layer**, and ced already has exactly one non-modal overlay:
ghost text (`editor/ghost.go`), which paints into the render row without
touching input at all. If signature help ever goes live-while-typing,
that is the layer it goes through. That is now written into CLAUDE.md as
a rule, because the alternative is a future session reading "manual
trigger" as an omission and bolting an auto-trigger onto the modal.

---

## The emphasis IS the feature

Without the active-parameter highlight this verb is hover on the
enclosing function, which the user could already get by moving the
cursor onto it. Everything else in the tooltip — the signature text, the
docs — hover already does.

So `hoverModal` grew exactly one field:

```go
// hoverEmph marks a run of one line to paint with emphasis.
type hoverEmph struct{ line, start, end int }
```

Same arrangement as `findAllModal.heading` from the references commit,
one floor down: two verbs share a modal because the geometry, the
trigger-happy dismissal and the "this is a glance, not a workspace"
framing are identical, and the ONE thing that differs gets one field.

Emphasis is repainted **over** the finished line rather than the line
being drawn in three segments. The width cap can cut a span short or
away entirely, and clamping one range is simpler to get right than three
interlocking substrings.

---

## Hard wrap for code, word wrap for prose

The signature label is hard-wrapped; the documentation is word-wrapped
through the chat panel's `wrapChatText`. That is the same split the chat
transcript already makes between fenced code and text, and here it is
load-bearing **twice**:

1. A signature is code. Collapsing its runs of whitespace (which
   `strings.Fields` does) misrepresents it.
2. Only an exact rune-per-column mapping lets a parameter's offset
   become a `(row, column)` by division:

   ```
   row, col := off/width, off%width
   ```

   A word wrapper drops and merges spaces, so every offset past the
   first break would be a guess. A parameter straddling a break gets one
   emphasis run per row — both halves lit, which is what the user needs.

---

## The protocol's optionals collapse in internal/lsp

The ParseDocumentSymbols rule, applied again. What the app layer never
learns:

- **`activeSignature` / `activeParameter` are POINTERS on the wire.**
  Absent has to be distinguishable from zero, and zero is *the first
  signature* and *the first parameter* — the overwhelmingly common
  answer. A plain int would make a server that omitted the field
  indistinguishable from one saying "the first parameter", which is the
  difference between an accurate hint and a confidently wrong one.
- **A signature's own `activeParameter` overrides the help's** (spec
  precedence). It is the only way a server can say different things
  about different overloads in one response.
- **`paramRange` resolves the label's two shapes, offsets FIRST.** The
  `[start, end)` pair is exact (UTF-16, converted to runes via the
  existing `RuneCol`). The string form is the protocol's older, looser
  option and resolving it means a substring search, which can land on
  the wrong occurrence in something like `f(int, int)`. Cost of being
  wrong there is a few columns of misplaced emphasis, not a wrong
  tooltip — worth taking, worth documenting.
- **Three spellings of "no parameter applies"** — absent, an explicit
  `-1` (LSP 3.17), and out of range — all fall out of one bounds check
  and leave the signature showable with nothing lit. Half an answer beats
  none.
- An out-of-range `activeSignature` **clamps to the first** rather than
  panicking on the slice.

`markupText` was extracted while doing this: the `string | MarkupContent`
union is used by hover, signature documentation and parameter
documentation alike, and `HoverText` now delegates to it and keeps only
the array shape that is unique to hover.

---

## Smaller decisions

- **"No signature help *here*."** The usual cause of an empty answer is
  the cursor being outside any call — a fact about the POSITION. A bare
  "no signature help" reads as a fact about the server, which sends
  people to check their gopls install over nothing.
- **The active parameter's own documentation comes BEFORE the
  signature's.** It answers the question that made the user press the
  key, and the tooltip is capped, so it must not be what gets cut.
- **`firstParagraph`** trims a Go doc comment to its opening statement
  rather than truncating mid-sentence at a line count — Go doc comments
  lead with the sentence that says what the function does.
- **The overload counter appears only when `Count > 1`.** Go has no
  overloads, so for the one language ced ships a server for this line is
  always absent; a "1 of 1" would be noise anywhere.

### The leader key

`Esc I` — and unlike last session's `Esc R`, this one is a **true**
shifted twin. Same tooltip, same glance, one question over: `i`
describes the symbol under the cursor, `I` describes the call the cursor
is standing inside. Which is the thing you want while your hands are
between the parentheses.

---

## Verification

`make test` (race detector) green.

**24 new tests.** Thirteen in `internal/app`: emphasis placement,
emphasis surviving a hard wrap (one run per row, whole parameter lit),
the no-active-parameter case, the overload counter's presence and
absence, parameter-doc-before-signature-doc ordering, the three helpers
(`hardWrap` preserving whitespace, `firstParagraph`, `capLines` marking
its cut), the pre-request flush, the three landing paths (opens with
emphasis / flashes on nil and on a blank label / drops another tab's
response), menu↔leader agreement, and two draw tests on the simulation
screen asserting the run really paints accent+bold and clamps under
truncation instead of indexing past the surviving runes.

Eleven in `internal/lsp`: both label shapes (the offset form exercised
with a non-BMP rune, where UTF-16 and rune offsets disagree),
signature-level precedence, the three "no active parameter" spellings,
index clamping, the empty answers, and `markupText`.

**Real binary**, through the `run-ced` PTY capture skill against real
gopls:

- `Esc I` with the cursor on `errs` in
  `fmt.Fprintf(&b, " ✗ %d", errs)` rendered
  `Fprintf(w io.Writer, format string, a ...any) (n int, err error)`
  hard-wrapped across two rows, with the doc paragraph beneath
- an HTML capture confirms `a ...any` painted `#7aa2f7` **bold** against
  `#c0caf5` body text — the emphasis lands on the right parameter, and
  stops at its end

One honest finding: with the cursor **inside a string-literal argument**,
gopls declines and the tooltip doesn't open. That is gopls's call, not a
gap in the plumbing — and it is the case that makes "here" the right word
in the flash.

Menu geometry pins updated — 119 rows / height 125 / dividers
`[2, 5, 122]` — in the tests and in CLAUDE.md.

`gofmt -l` reports the same three pre-existing files
(`internal/app/app.go`, `internal/app/tabbar.go`,
`internal/editor/syntax.go`); untouched, as the last two sessions did.

---

## What's queued

Stage 9 is down to the two verbs that share one blocker:

| Verb | Blocker |
|---|---|
| Code actions | applying a `WorkspaceEdit` |
| Rename | the same |

**The blocker is unchanged and is a BUFFER-layer problem.** A
`WorkspaceEdit` edits files the user never opened; every write path in
ced today is one buffer at a time, and a rename has to be ONE undo step
across all of them. Build that primitive first and put both verbs on top
— it pays for two, which is the argument for doing it properly rather
than special-casing rename.

Three notes for anyone working near this change:

- **Do not add an auto-trigger to `hoverModal`.** See the manual-trigger
  section — it is not a missing feature, it is the wrong layer. Ghost
  text is the right one.
- **`hoverModal` is now shared by two verbs and named for one of them.**
  That was deliberate (the `findAllModal` precedent: the type names the
  SHAPE, and a rename would churn tests for no behavioural gain), but a
  third consumer is the point at which "hover" stops describing it.
- **`hardWrap` looks like a naive helper and is not.** Anyone replacing
  it with `wrapChatText` "for consistency" will make the tooltip prettier
  and silently misplace every emphasis run past the first line break.
