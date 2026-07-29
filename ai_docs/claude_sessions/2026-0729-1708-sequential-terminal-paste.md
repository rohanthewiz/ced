# Session: Sequential execution for multi-line terminal pastes

Session ID: 7771ada1-03f6-42aa-8e5d-91582ce9d329
Date: 2026-07-29

Direct follow-up to `2026-0729-1648-chat-and-terminal-paste.md`, and it
**reverses that doc's terminal policy**. The routing work there stands;
"a paste never runs anything" does not. Read this one for the terminal.

### Ask

> "Do sequential paste for multi-line terminal pastes"

The prior session shipped flattening for the terminal panel and flagged
running the lines sequentially as the owner's call. This is that call,
exercised.

### What changed

A line break in a paste now means what pressing Enter means. Pasting
`cd /tmp\nls -la\necho done` runs `cd /tmp`, then `ls -la`, and parks
`echo done` at the prompt — no break after it, so it waits for a real
Enter. With a trailing newline it runs too.

The paste's first line joins whatever is already on the input line, at
the caret: `echo hello ` + a pasted `world\n` submits
`echo hello world`, exactly as typing it would.

The chat composer still flattens. That divergence is now documented on
both sides as deliberate rather than an oversight: a break in a PROMPT
implies no submit, so flattening loses nothing and keeps the text
editable before it's sent.

### Why it needs a queue

`submitTermCommand` runs `Eval` on a goroutine and refuses while
`term.running` — so the lines cannot simply be looped over. A loop would
either interleave commands or hit the busy guard and drop them on the
floor.

`term.pasteQueue` holds the tail; `termRunPasteQueue` submits the next
line and stops the moment one starts an Eval; `handleTermDone` calls back
in, so the batch walks itself forward one command at a time. Lines that
need no Eval (blank ones, continuation lines grsh is still accumulating)
don't stop the walk — which is exactly what lets a pasted multi-line
block arrive as ONE unit.

Each line goes through `submitTermCommand` rather than around it, so
pasted input feeds grsh's `NeedsMore` continuation like typed input and
every line is echoed into the scrollback with its prompt. A batch reads
back as the commands it ran.

### Stop has to mean stop

The queue's abort paths matter more than its happy path:

- **⏹ drops the queue** *before* anything else, whether or not there is a
  process to signal. Without that, interrupting line 1 of a five-line
  paste would just hand the shell line 2 — the opposite of the button's
  promise. It also fires when nothing is running at all, which a paste
  whose first line needed no Eval can produce.
- **`exit` drops it** in `handleTermDone`'s exit branch: the remaining
  lines were meant for a shell that no longer exists.
- **Hiding the panel does NOT** drop it — a running command already
  survives hide/show, and a batch is the same promise. Left alone
  deliberately; the three "panel yields the strip" call sites (git panel,
  chat dock, toggle) are layout events, not abort gestures.
- **A failed line does not abort** the rest. A shell without `set -e`
  keeps going, and a paste is a sequence of commands, not a script.

The interrupt flashes were restructured so each branch reports the drop
itself. Appending a second `a.flash` would have overwritten — and
contradicted — "nothing to interrupt (builtin or Go code running)", since
the last flash wins.

### Files touched

- `internal/app/terminal.go` — `term.pasteQueue`; `termInsertPaste`
  rewritten (normalize CRLF/CR, flatten per LINE, first line to the
  caret, rest queued); `termRunPasteQueue`; `termDropPasteQueue`; the
  `exit` and ⏹ drops; resume call at the end of `handleTermDone`.
- `internal/app/textpaste.go` — `pasteLineCount` deleted (dead once the
  flash changed); `flattenPaste`'s doc now explains the two callers use
  it at different granularity, which IS the difference between the
  panels.
- `internal/app/copilot_chat.go` — comment reframed: the composer's
  flattening is a deliberate divergence from the terminal, and why.
- `CLAUDE.md` — the terminal panel's paste rule rewritten from "a paste
  never runs anything" to the real-shell semantics plus its three
  invariants; the stale `chatFlattenPaste` reference in the chat section
  fixed.
- Tests: `textpaste_test.go`, `terminal_test.go`.

### Tests

New or rewritten (9): sequential ordering with the unterminated tail
parked and exactly the expected number of Evals; trailing-newline-runs
(also the one-line case); break-free paste runs nothing; first line joins
the typed input; pasted block feeds `NeedsMore` as one unit; ⏹ drops the
queue and a later `handleTermDone` doesn't resurrect it; ⏹ with nothing
running drops it without claiming an interrupt; `exit` drops it;
CRLF-is-one-break and tab-survives-as-space.

Three older pins had to change because they asserted the behavior this
session replaced — `TestPaste_IntoFocusedTerminalPrompt`,
`TestPaste_TerminalSingleLineNoFlash` (became
`TestPaste_TerminalTrailingNewlineRuns`), and `TestTermPasteClip` /
`TestTermPasteClip_CmdV`. The terminal paste tests now need a fake
session (`openTestTerm`), since without one `submitTermCommand` no-ops
and the queue is dropped.

`make test` (`-race`), vet, gofmt all clean.

### Notes for next time

- **Cmd+V executes too.** Both gestures share `termInsertPaste` by
  design, so Cmd+V of `cd foo\nls` runs `cd foo`. If that should differ,
  the split belongs at the two call sites, not inside the queue.
- The queue is UI-driven, main-loop only. Nothing in it is safe to touch
  from a goroutine, and `termRunPasteQueue` must stay re-entrant from
  `handleTermDone`.
- Typeahead while a batch runs behaves like a real terminal: the parked
  line is editable, and Enter during a running command is refused by the
  existing busy guard.
- Pasted lines land in `term.history` like typed ones — a shell does the
  same, and it's what makes ↑ useful after a batch.
