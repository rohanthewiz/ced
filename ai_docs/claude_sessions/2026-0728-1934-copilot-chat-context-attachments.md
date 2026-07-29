# Session: Copilot chat — file / selection context attachments

Session ID: 1b67a7ec-167b-4e2c-870c-f76da79c2ef1
Date: 2026-07-28

### Ask

> "In the Copilot chat what would it take to include the current file
> automatically or a selection within the current file, along with the
> ability to add other files as context?"

Answered as a scoping question first (the ask was "what would it take"),
then built it on the follow-up: *"build it with those defaults."*

### The defaults that were agreed

Per-turn attachments (not sticky), a chips row above the composer,
selection-beats-file for auto-context, 64KB cap per attachment.

### The key realization

The protocol already had this. `chatSendPrompt` was sending a
one-element array where ACP's `session/prompt` takes a ContentBlock
ARRAY. Two block types could carry a file:

- `resource_link` (uri only, agent fetches it) — **useless here.** The
  phase-3 handshake declares `fs.readTextFile: false` and auto-declines
  every `session/request_permission`, so the agent genuinely cannot read
  a path we merely point at. A resource_link would be a lie.
- `resource` (embedded: uri + mimeType + text) — **the path.** r-ed
  ships the bytes itself.

So attaching context required **zero loosening of the chat-only scope
guard**. That's the design's happy accident and the thing to preserve:
context is PUSHED, never fetched.

One gate: the agent advertises
`agentCapabilities.promptCapabilities.embeddedContext` in the
`initialize` result — which `chatInitialize` was discarding (passing
`nil` as the result). Captured now; when false, the same text folds into
the prompt as a labelled fenced block. An agent that advertises nothing
gets the text-only path (the safe direction).

### What shipped

```
─ Copilot - GPT-5.5 ─────────────── ✕ ─
 ❯ why does this leak?

 ▤ internal/app/x.go:12-40 · 1.2 KB

 The defer inside the loop…
                               ⧉ copy

 ▤ internal/app/x.go:12-40 (auto)  ✕
 ▤ internal/lsp/client.go         ✕
 ❯ ▏
```

- **Auto-attach the current file** — default ON (`"chatcontext"` config
  key). Synthesized at SEND time from the active tab, so it always
  reflects where the caret actually is. A selection narrows it to those
  lines.
- **Explicit attachments** — ≡ Copilot → attach current-file-or-selection
  (one row, `labelFor` follows what it would actually send) and "Attach
  file to chat…" (the palette as a fuzzy picker over the finder index,
  per the house rule against new list modals).
- **Chip rows** between transcript and composer, each with a ✕.
- **Transcript echo** on every dispatch: `▤ path:from-to · size`.

### Design decisions worth keeping

**Per-turn, not sticky.** `chatSendPrompt` — the SINGLE dispatch point,
so the queued-prompt path inherits all of this — resolves, echoes, and
clears. An ACP session keeps history server-side, so a sticky attachment
re-sends the whole file on every prompt for the rest of the session:
paid for twice (tokens + premium multiplier) with nothing on screen to
explain why.

**Auto-context is a TOGGLE, not a list entry.** `chatPendingAttachments`
derives it per call and prepends it. Its chip's ✕ can only mean one
thing — "stop sending the active file" — so it flips the toggle through
the same `setChatContext` the ≡ row uses. Two surfaces, one meaning, one
persisted value.

**Content comes from the open Tab's BUFFER**, falling back to disk. You
attach what you're looking at, unsaved edits included. Sending the stale
on-disk copy of the file you just changed is the one failure mode that
would make the agent's answers quietly wrong.

**Failures are announced, never silently dropped.** An unreadable or
binary file, or a line span past EOF, produces a `▤ … NOT attached: …`
note instead of a block. A prompt the user thinks carries a file must
never go out pretending it did. Same for the 64KB cap: cut on a line
boundary, and the cut is stated in the note.

**Chips are derived rows with one geometry source.**
`chatAttachRowsView()` is what draw AND hit-testing both consume; it
clamps itself (`chatAttachRowsMax = 3`, and never below one surviving
transcript row) and summarises the remainder as `+N more`.
`chatVisibleRows` subtracts it — that was the only ripple, since the
panel previously hardcoded "header + input = 2".

**`chatAttachPress` swallows the whole chip block**, not just the ✕ — a
drag started on a chip must not select transcript prose behind it. It
runs BEFORE `chatActionPress` in `chatPanelPress`.

**Markers stay single-width.** Used `▤`, not 📎. Every marker in this
editor (`⏹ ✕ ❯ ⧉ ⚙ ⊘`) is one cell and `runeLen` counts RUNES — a
double-width emoji would have overrun the ✕ button on a narrow strip.
Caught this before shipping; worth remembering for any future glyph.

**`chatOpenPanel` extracted from `menuToggleChat`** so attaching can open
the panel without duplicating the left-edge single-occupancy rules.
Attaching to a panel you can't see leaves no way to know it worked. When
the panel REFUSES to open (Copilot disabled, no binary), the "Attached…"
flash is suppressed so its explanation stands.

**Lifetime split at disconnect**: `embeddedContext` describes the AGENT,
so it dies with the connection; pending attachments are editor-side
context for an unsent message, so they survive (the `modelPref` rule).

### Files

- `internal/app/copilot_chat_context.go` — NEW (~570 lines): the model,
  content resolution, wire-format assembly, menu actions, chip geometry
  + draw
- `internal/app/copilot_chat_context_test.go` — NEW, 18 tests
- `internal/app/copilot_chat.go` — state fields, capability capture in
  `chatInitialize`, `chatSendPrompt` block assembly, chip-aware
  `chatVisibleRows`/`drawChatPanel`/`chatPanelPress`, `chatOpenPanel`
- `internal/app/copilot_chat_test.go` — `fakeACPAgent.embedded`, 3 tests
- `internal/app/app.go` — 4 ≡ Copilot rows, `cfg.ChatContext` wiring
- `internal/userconfig/` — `ChatContext` + `SaveChatContext` + 4 tests
- `internal/app/app_test.go` — menu geometry pins
- `CLAUDE.md` — new house-rules section + architecture map

### Tests

18 new in the context file: auto-context on/off/selection, the
column-zero line trim, buffer-beats-disk, range slicing + clamping,
binary refusal, truncation landing on a newline, both wire shapes
(embedded resource with a `#L1-2` URI fragment vs. the inline fold),
unreadable-is-reported, the echo-and-consume turn contract, dedupe, the
disabled-panel flash, both menu rows, chip geometry, overflow summary,
✕ removal + auto-chip-flips-toggle (with the persisted config asserted),
chips-beat-selection routing, a simulation-screen draw smoke test, and
the two formatting helpers. Plus 3 in the chat file for the capability
capture against real ndjson framing.

`make test` (race) green repo-wide.

### Gotcha for next time

`TestMenuLayout_NoCustomActions` and `TestMenuLayout_WithCustomActions`
both pin menu geometry, so any added ≡ row breaks them. Four rows added
→ 58 group actions / 70 rows / height 76 / dividers `[2, 5, 73]`, and
the custom-actions height 75 → 79. CLAUDE.md's copy of those numbers was
updated in the same pass — it goes stale easily.

### Not built (deliberate)

An `@`-style inline mention autocomplete in the composer. The ≡ rows are
the sanctioned surface; an inline trigger is a separate feature, not a
TODO to sneak in. Also untried: driving the real TUI to eyeball the chip
row — the draw test asserts against a simulation screen, but real
terminal glyph widths only show up in a real terminal.
