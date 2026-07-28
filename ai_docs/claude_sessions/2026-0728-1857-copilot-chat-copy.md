# Session: Copilot chat — select, copy response, copy conversation

Session ID: 75017ebc-0ca9-4208-83ac-b36a4b6214e8
Date: 2026-07-28

### Ask

> "I need to be able to copy text in Copilot chat. Currently I cannot
> even highlight text in the Copilot chat. Select and copy with Cmd+C
> would be nice, but also give a typical double-rectangle copy icon for
> each Copilot response and give me another copy icon at the bottom of
> the conversation to copy the whole conversation"

Three deliverables, no ambiguity worth blocking on — built all three.

### Why highlighting was impossible

r-ed captures the mouse (tcell mouse mode), so the terminal's own
drag-to-select never sees a click inside the chat strip. Nothing was
broken; the editor simply has to provide selection itself, exactly as
it already does for the code pane. That framing set the whole design.

### What shipped

```
─ Copilot chat ──────────────────── ✕ ─
 ❯ how do I reverse a slice

 Use a loop:
 ```go
 for i := range s {}
 ```
 That's it.
                               ⧉ copy

                  ⧉ copy conversation
```

**1. Drag-select + Cmd+C.** A press in the transcript body starts a new
`"chatsel"` drag mode (`chatPanelPress` now RETURNS the mode, the
`gitPanelPress` convention; `handleMouse` grew the continuation branch
next to `chatsplit`). Motion extends, the highlight paints with
`theme.Selection`, and `Cmd+C` while the chat has focus lifts the text
onto both clipboards. Esc drops the highlight (added beside
`copilotClearGhost` in the Esc handler — same "purely a side effect"
spot).

**2. `⧉ copy` after each agent response.** **3. `⧉ copy conversation`**
at the end of the transcript. Both are DERIVED ROWS, not cells beside
prose.

**≡ Copilot → "Copy chat transcript"** — keyboard twin of the trailing
button, for the macOS-Terminal-swallows-clicks case (the
`Git panel actions` precedent).

### Design decisions worth keeping

**Selection lives in derived-row space, not logical text.**
`chatPos{row, col}` indexes the wrapped rows `chatRows(w)` produces —
which is what the user actually drags across. Row indices count from the
top of the transcript, so scrolling never shifts the highlight, and a
re-wrap just re-derives it. The one hazard: `chatAppendMsg` trimming at
`chatTranscriptMax` renumbers every row, so it clears the selection.

**Copy affordances are rows, not decorations.** A `chatRowAction` enum
(`chatRowNone` = zero value, so every existing `chatRow` literal kept
its meaning) plus `msgIdx`. `chatRows` appends one after each non-empty
agent message and a `chatRowCopyAll` after everything. Consequences that
made this the right call:

- Hit-testing stays "which row was clicked" — the transcript's existing
  vocabulary. No new coordinate math.
- Scroll/height/max-scroll accounting is free: they all go through
  `chatRows`.
- One `chatActionRect(row, x, ry, w)` right-aligns the label and BOTH
  draw and hit-test call it (btnRect rule).

**Two copy semantics, deliberately different.** The ⧉ button copies the
LOGICAL message — original line breaks, so a pasted answer isn't
shredded by a 38-column strip. A drag-selection copies the WRAPPED rows,
because that's the honest reading of a row-space selection: you get what
you saw. Action rows are skipped when building selection text, so a drag
past a button never pastes the word "copy".

**Transcript format is labelled, not glyph-prefixed.** `You:` /
`Copilot:` rather than `❯` / `⚙` / `⊘` — screen furniture doesn't
survive the trip to an issue or commit message.

**Copies route through `copilotCopyCode`**, the stubbable var phase 1
already introduced for the device-flow code copy — `newTestApp` neuters
it, so no test writes the dev machine's clipboard.

Only agent prose gets a ⧉: the user's own prompts are already in their
head, and tool/info notes are editor chatter.

### Files

- `internal/app/copilot_chat.go` — `chatRowAction`/`chatPos`, selection
  state on `chatState`, a new "Selection + copy" section
  (`chatRowWidth`, `chatSelRange`, `chatRowSelSpan`, `chatPosAt`,
  `chatSelectionText`, `chatTranscriptText`, `chatCopy*`), row emission,
  `chatActionPress`/`chatPanelDrag`, selection painting in `drawChatRow`
- `internal/app/app.go` — `chatsel` drag branch, `chatPanelPress` return
  wired to `dragMode`, Cmd+C in the chat-focused Meta branch, Esc clear,
  the ≡ Copilot row
- `internal/app/copilot_chat_test.go` — 7 new tests
- `internal/app/app_test.go` — menu geometry pins
- `CLAUDE.md` — chat house rules + corrected pin

### Tests

Seven new ones: row derivation (agent gets a ⧉, user doesn't, empty
transcript grows none), the full drag → `handleKey(Cmd+C)` path,
multi-row copy skipping button labels, Esc clearing, the per-response
button copying verbatim, the conversation button + its ≡ twin, and a
simulation-screen check that selected cells actually carry
`theme.Selection`.

`make test` green repo-wide.

### Gotcha for next time

`TestMenuLayout_NoCustomActions` pins row count / height / divider
offsets, so ANY added ≡ row breaks it — expected, update the three
numbers. CLAUDE.md's copy of those pins was already two rows stale
before this session; corrected to 54 actions / 66 rows / height 72 /
dividers `[2, 5, 69]`.
