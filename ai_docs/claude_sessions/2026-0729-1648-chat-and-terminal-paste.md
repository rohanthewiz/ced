# Session: Paste into the chat prompt + the terminal command line

Session ID: 7771ada1-03f6-42aa-8e5d-91582ce9d329
Date: 2026-07-29

> **Superseded in part:** the routing and the chat behavior below still
> hold, but the TERMINAL policy here ("a paste never runs anything",
> flattening, the rejected `; ` join) was reversed the same day —
> multi-line terminal pastes now run their lines in order. See
> `2026-0729-1708-sequential-terminal-paste.md`.

### Ask

> "I need the ability to paste into the Copilot Chat prompt of ced"
> → "yes, do the terminal panel too"

Two panels, one root cause. Neither bug was in the panel code — both
lived in the bracketed-paste **router**.

### The bug (chat)

`editorPasteTarget()` in `textpaste.go` is the single source of truth for
"who owns a bracketed paste". It suppressed the editor for a modal, the
find bar, the menu, and a focused **terminal** — but never for a focused
**chat** panel. So with the composer focused and a file open, a terminal
paste (Cmd+V / right-click / middle-click) armed accumulation against the
active tab and spliced the text into the **file behind the panel**. The
user saw nothing land in the prompt and a silent edit in their buffer.

With no tab open the same paste fell through to raw key events instead,
where the first `Enter` inside a multi-line paste submitted the prompt
mid-paste. Two different wrong answers, one missing gate.

`chatPasteClip` (Cmd+V from ced's own clipboard) worked the whole time —
which is why the panel *looked* like it supported pasting.

### The bug (terminal)

Worse, and the reason the follow-up was worth doing. `termPasteClip` was
the panel's only paste path; a real terminal paste was never gated at
all, so it arrived as raw keys and every `Enter` it carried reached
`submitTermCommand`. **Pasting a three-line snippet executed the first
two lines as commands nobody typed.**

Cmd+V had its own defect: it *dropped* `\n`/`\r` rather than replacing
them, gluing the tail of one line onto the head of the next —
`"cd foo\nls"` arrived as `cd fools`. `TestTermPasteClip` pinned exactly
that string, so the mangling was load-bearing in the suite.

### The fix

One router, three mutually exclusive targets:

- **`chatPasteTarget()`** / **`termPasteTarget()`** — new predicates
  beside `editorPasteTarget()`, same shape and same suppressions (modal /
  find bar / menu outrank every panel). `editorPasteTarget()` now also
  returns nil while chat has focus, so each predicate is independently
  correct rather than relying on switch order.
- **`handlePaste`** arms on any of the three and routes the flush
  chat → terminal → editor, matching `handleKey`'s focus tiebreak. The
  click handlers keep the two `focused` flags exclusive; `termPasteTarget`
  yields to chat anyway so the tiebreak can never produce two targets.
- **`textField.insertString`** (modal.go) — splices a whole paste in at
  the caret in one step, instead of replaying it a rune at a time through
  `handleKey`. Same reason the editor path uses one `InsertString`.
- **`flattenPaste` / `pasteLineCount`** (textpaste.go) — the shared
  single-line policy: breaks and tabs become one space each, a CRLF pair
  counts once, other control runes are dropped.
- **`chatInsertPaste`** / **`termInsertPaste`** — one entry point per
  panel, used by *both* gestures (Cmd+V and bracketed paste), so the two
  can't drift apart. `chatPasteClip` and `termPasteClip` are now
  one-liners over them.

Pastes splice at the caret, not append — type `explain: `, paste, keep
typing.

### The judgment call: what a multi-line paste means

Flattening, plus a flash on the terminal side
(`Pasted 3 lines as one command — review before Enter`). Two tempting
alternatives were rejected and the reasons are recorded in
`termInsertPaste`'s comment and CLAUDE.md so they don't get "improved"
back in:

- **Joining with `; `** would make a pasted script into a valid-looking
  compound command the user never typed — and a pasted `#` comment
  silently swallows the rest of the line, so what runs isn't what's on
  screen.
- **Replaying as keystrokes** runs every line but the last. That is the
  bug being removed.

Flattening keeps the whole text visible and editable and executes
nothing; `Enter` stays the only thing that submits. Chat gets no flash —
flattened prose still reads as what was pasted, and an agent doesn't care
about line breaks. A flattened *command* is a different animal.

The alternative — running pasted lines sequentially like a real shell —
was flagged to the owner as a one-line change in `termInsertPaste`, held
as their call rather than assumed.

### Files touched

- `internal/app/textpaste.go` — `chatPasteTarget`, `termPasteTarget`,
  three-way `handlePaste` routing, `flattenPaste`, `pasteLineCount`,
  chat suppression in `editorPasteTarget`, `strings` import.
- `internal/app/modal.go` — `textField.insertString`.
- `internal/app/copilot_chat.go` — `chatInsertPaste`; `chatPasteClip`
  reduced to a call into it (its private flatten helper moved to the
  shared one).
- `internal/app/terminal.go` — `termInsertPaste`; `termPasteClip`
  reduced likewise; `fmt` import for the flash.
- `internal/app/textpaste_test.go`, `copilot_chat_test.go`,
  `terminal_test.go` — below.
- `CLAUDE.md` — the paste-ownership house rule under both panels' house
  rules, including the two rejected alternatives.
- `README.md` — chat-panel bullet now covers both paste gestures and says
  a multi-line paste is flattened. (No user-facing terminal-panel section
  exists to update.)

### Tests

New, all through the real event path (`feedPaste` → `handleKey`, so the
`pasting` gate is exercised rather than bypassed):

- Chat: multi-line paste lands flattened in the composer and the file
  behind the panel stays empty; caret-splice; focus gating across
  closed / open-unfocused / focused plus modal, find bar, menu; paste
  with **no tab open** (the second way the old code lost the text);
  Cmd+V through `handleKey`; empty-and-control-only paste is a no-op that
  doesn't disturb the caret.
- Terminal: multi-line paste doesn't submit — asserts empty history,
  `!running`, clean editor buffer — and flashes its line count;
  single-line paste (with trailing newline) stays quiet; three-way target
  gating including both-panels-focused; Cmd+V no longer glues tokens.
- Shared: `flattenPaste` and `pasteLineCount` tables.

`TestTermPasteClip` was **updated, not deleted** — it pinned the old
`cd fools` gluing; it now pins breaks becoming spaces.

Full suite green across all 13 packages; gofmt and vet clean.

### Notes for next time

- The three paste predicates must stay mutually exclusive. Adding a
  fourth keyboard-owning surface means a fourth predicate plus a branch
  in `handlePaste` — not a special case inside one of the existing ones.
- Both composers are single-line `textField`s; that's the only reason
  flattening exists. A multi-line composer (still a known follow-up)
  would make `flattenPaste` the wrong default for chat, and the flash the
  wrong default for the terminal.
- Terminals that don't understand the bracketed-paste enable sequence
  still deliver pastes as raw keys, so the old per-line behavior survives
  there. Same silent-degradation contract as the formatter and LSP
  layers.
- grsh has its own multi-line continuation (`term.pending`); a flattened
  paste bypasses it. If that ever matters, it's a policy question about
  `termInsertPaste`, not about the router.
