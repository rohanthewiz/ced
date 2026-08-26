# Session: the chat composer grows a second dimension

- Date: 2026-08-26
- Branch: `main`
- Repo: ced (`~/projs/go/ced`)
- Session id: `3c440efb-1797-4e9a-a23b-0c532ca1c7a7`
- Predecessor: `2026-0826-1044-scrollbar-rail-marks.md`

## What was asked

> On the Mac what would it take to handle voice dictation in the prompt
> body?

Answered as an assessment (see below), whose conclusion was that macOS
dictation already works — it arrives as plain keystrokes — and the one
real work item was the multi-line composer CLAUDE.md had been calling "a
known follow-up". Then:

> Do the multi-line composer

## The dictation assessment (kept, it's the design context)

macOS dictation is handled entirely by the OS and the terminal emulator:
ced receives ordinary rune key events, so whatever surface owns the
keyboard (composer, prompt modal, find bar, editor) already takes
dictated text. The genuine gaps found:

1. **Spoken "new line" submits** — the composer was a single-line
   `textField` where Enter sends. The real fix is the multi-line
   composer (a bare Return still submits; that part is inherent).
2. Smart punctuation is a System Settings problem; ced should not
   silently normalize typed input.
3. Live-correction backspaces arrive as normal Backspace events —
   already handled.
4. ced cannot start/stop dictation; there is no escape sequence for it.

## What shipped

### New widget: `internal/app/chatcomposer.go` (+ `_test.go`)

`chatComposer` — textField grown a second dimension, used ONLY by the
chat prompt. Every other single-line input stays on `textField`: a
widget that can hold a newline must not be reachable from surfaces that
would send its value somewhere expecting one line.

- **One rune slice with `\n` separators**, not a line list — no
  per-line state (no styles, no undo) to justify a Buffer.
- **Display rows HARD-wrap** at the field width (the find-bar /
  signature-label argument): only rune==column lets a caret index become
  a (row, col) by arithmetic, and a click become a caret index the same
  way. The word-wrapped transcript is prose being READ; the composer is
  text being EDITED.
- Wrapping derived on demand from (value, width), never cached — the
  chatRows rule. Enter is NOT handled by the widget (textField's
  contract): send-vs-newline is the caller's policy; the caller routes
  Enter and calls `insertNewline`.
- Home/End are LOGICAL-line verbs (`lineBounds`); Backspace/Delete join
  lines when they consume a `\n`; `clickAt` clamps rows/cols;
  `adjustScroll`/`draw` keep the caret row visible when the value
  outgrows the band.
- **Caret boundary rules** (`caretRowCol`): a caret exactly on a wrap
  boundary belongs to the FOLLOWING row at col 0 (where the next typed
  rune lands); a caret at a logical line's end stays on that line's
  last row (col may equal the width, the textField caret convention).
- `composerSanitize` is the surviving half of flattenPaste's old job:
  CRLF/CR → `\n`, tab → one space (caret math needs rune==column),
  other control runes dropped.

### Key routing (`handleChatKey` + one `handleKey` fix)

- **Enter sends; Alt+Enter breaks the line** (Shift+Enter too, where
  the terminal can tell — kitty/Ghostty/WezTerm via CSI-u).
- **Up/Down move the caret while it has somewhere to go**
  (`moveVertical` reports false at the edge rows) **and fall back to
  prompt history at the composer's edges** — exactly what they meant
  when the composer was one row tall.
- **The legacy Alt+Enter fold — the load-bearing find of the session.**
  A terminal without CSI-u (tmux included) sends ESC CR for Alt+Enter,
  and tcell's fold for ESC + control-char reports it as the RUNE `'m'`
  carrying `ModAlt|ModCtrl` (input.go does `r += 0x60`; `'j'` for the
  rare LF-sending emulator). `'m'` is a bound leader rune, so the chord
  was ALREADY misfiring multicaret on the buffer behind the panel.
  `handleKey` now rewrites that spelling into `KeyEnter+ModAlt` BEFORE
  the leader branches, so every terminal delivers one spelling of the
  gesture and every consumer downstream sees it.

### Geometry (`copilot_chat.go`, `copilot_chat_context.go`)

- The composer band grows a row per wrapped line and DISPLACES the
  transcript (the Find-all rule: nothing floats over what it serves),
  capped at `chatComposerMaxRows` (6), past which the widget scrolls
  internally. Panel floor: header + ≥1 transcript row survive any
  prompt.
- **Chips clamp against what the composer left, never the other way
  around** — that one-way dependency is what keeps the two clamps from
  chasing each other. `chatAttachTop` = `chatComposerTop() - chips`.
- `chatInputSpan` now means the band's FIRST row (prompt gutter drawn
  there only); `chatInputCols` split out because the width is needed BY
  the row-count math that places the y. `chatComposerWidth` = the text
  area the rows()/caret math and the drawn span must agree on.
- `chatComposerEdit` wraps every composer mutation (typing, newline,
  paste): a band that grows or shrinks moves the transcript's bottom
  edge, so a bottom-pinned view is re-pinned after the edit (the
  chatAppendMsg atBottom rule, triggered by layout instead of content).
  Deliberately NOT wrapped around PgUp/PgDn — those scroll away on
  purpose.
- Clicks anywhere in the band position the caret (`clickAt`), replacing
  the old single-row `y == iy` check.

### Paste

`chatInsertPaste` keeps line breaks now — `flattenPaste` is no longer in
the chat path (its doc updated; the terminal panel remains its only
caller, per line, because there a break means Enter). The composer's own
sanitize carries the control-noise contract. Cmd+V and bracketed paste
still funnel through the one entry point.

## Files touched

- `internal/app/chatcomposer.go`, `chatcomposer_test.go` — NEW
- `internal/app/copilot_chat.go` — chatState.input type, send/history,
  handleChatKey, chatComposerEdit, geometry helpers, press + draw
- `internal/app/copilot_chat_context.go` — chip clamp + chatAttachTop
- `internal/app/app.go` — the legacy Alt+Enter fold rewrite in handleKey
- `internal/app/textpaste.go` — flattenPaste doc (chat no longer flattens)
- `internal/app/copilot_chat_test.go`, `textpaste_test.go` — updated to
  the breaks-kept contract + 4 new app-level tests (Alt/Shift+Enter,
  the 'm' fold end-to-end, band grow/cap, caret-before-history)
- `CLAUDE.md` — chat-panel section rewritten for the multi-line
  composer; architecture map gained chatcomposer.go

## Verification

`gofmt` clean; `go build ./...`; `go test -race ./...` all green
(app package ~20s under race).

## Known follow-ups / non-goals

- Spoken "new line" during dictation still submits — a bare Return is
  indistinguishable from the send gesture; inherent to Enter-sends.
- No goal column in `moveVertical` (the composer is a few short lines,
  not a code buffer) — deliberate.
- No on-screen hint for the Alt+Enter chord yet; discovery is docs-only.
- Other ESC+control folds (`'a'` Alt|Ctrl etc.) can still reach the
  leader branch; only the Enter folds are rewritten — scoped on purpose.
