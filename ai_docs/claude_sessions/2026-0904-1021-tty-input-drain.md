# Session: the stray `0;163;54m` after quitting

- Date: 2026-09-04
- Branch: `main`
- Repo: ced (`~/projs/go/ced`)
- Session id: `session_01UfhPQv7PvPJwaseAYW4TEz`
- Predecessor: `2026-0831-0130-brace-matching.md`

## What was asked

> When CEd exists there are sometimes some extraneous chars emitted onto
> the console, for example: `0;163;54m;`

A bug report with one piece of evidence in it, and that evidence turned
out to be the whole diagnosis.

## The diagnosis

`0;163;54m` is not a color and not a rendering artifact. It is the tail
of an **SGR mouse report**:

```
ESC [ < 0 ; 163 ; 54 m
        ^   ^     ^  ^
        |   col   row `m` = RELEASE (`M` would be a press)
        button 0 = left
```

The shell swallows `ESC [ <` as an unbound key sequence and echoes the
remainder at its prompt, which is exactly the string the user saw.

Three facts combine to strand it:

1. **ced asks for motion reporting.** `New` calls `EnableMouse` with
   button + drag + motion flags, which is DEC mode 1003 — the terminal
   reports every pointer movement, not just clicks.
2. **Menu rows fire on the PRESS.** Clicking ≡ → Quit tears the editor
   down while the button is still down, so the release is written a
   moment later, by which time nothing in ced is reading.
3. **tcell disables the mouse LAST and returns.** `Fini` writes
   `?1000l ?1002l ?1003l ?1006l` at the very end of its reset and exits
   immediately; anything already in the tty's input buffer is stranded
   by construction.

Hence "sometimes": it needs the pointer moving or a button lifting in
the moment around exit. A keyboard quit with a still mouse leaks
nothing.

### How it was confirmed

A raw-byte pty harness (scratchpad, not checked in — the `run-ced`
capture tool replays into a grid, and what was wanted here was the
bytes). Running `./bin/ced . ; echo SHELL-SEES: ; cat -v` and writing a
mouse report shortly after the quit key showed the report arriving in
the next reader verbatim:

```
SHELL-SEES:
^[[<0;163;54m
```

The same capture showed the shutdown order, mouse-off dead last:

```
^[]2;^G ^[[23;0t ... ^[[?1049l ^[[23;0;0t ^[[?1000l^[[?1002l^[[?1003l^[[?1006l
```

## What shipped

### `internal/app/ttydrain.go` — new

```go
func (a *App) drainPendingInput()
func mouseButtonHeld(btn tcell.ButtonMask) bool

var ttyDrainWindow        = 50 * time.Millisecond
var ttyDrainReleaseWindow = 150 * time.Millisecond
var ttyDrainPoll          = 2 * time.Millisecond
```

Called from `Close` between `hostIdentClose` and `screen.Fini()`. It
switches off all three reporting modes ced turned on — mouse, paste,
focus, mirroring `New`'s three Enable calls — and then consumes and
discards terminal events for a bounded window:

```
 press ──> ced quits ──> DisableMouse ──> [drain window] ──> Fini
                              ^                  ^
                   terminal stops reporting   release consumed here
```

**Before `Fini`, not after.** The screen is still in raw mode with
tcell's input goroutine running, so a partial escape sequence is
reassembled by the parser that already knows how and events are
discarded whole, rather than a byte count being guessed at on the raw
descriptor.

**The window is adaptive, and that is the design decision worth
keeping.** The baseline 50ms is sized against what can be *in flight*:
the terminal stops reporting as soon as it reads the disable, so the
only stranded bytes are ones written before that read — one link
latency's worth. But when a button is still DOWN at Close the
interesting byte has not been written yet; it appears when the finger
lifts, tens to a couple of hundred milliseconds later. So a held button
raises the budget to 150ms — as a **ceiling, not a delay**: the drain
stops the moment the release is seen, so an ordinary click pays its own
duration. The full 150ms is spent only when no release ever arrives,
which is the case where the terminal processed the disable first and
there was never anything to strand.

Wheel notches are excluded from "held" (no release to wait for), which
also matches how `handleMouse` already reads presses.

### `internal/app/app.go`

- New field `mouseHeld bool`, latched at the **top** of `handleMouse`,
  before any routing can return early — a press a surface claims and
  returns on must still be recorded. Deliberately not folded into
  `dragMode`, which only tracks gestures a surface claimed.
- `Close` calls `a.drainPendingInput()` before `a.screen.Fini()`.

### `internal/app/ttydrain_test.go` — new

Seven tests: queued input is consumed, `Close` drains before `Fini` (the
wiring is the part that can silently regress), a late release is waited
for, the wait stops as soon as it lands, both windows are bounded, a nil
screen is a no-op, and the latch tracks press / release / wheel.
`withShortDrain` collapses both windows the way the syntax tests
collapse `SyntaxSettle`.

Both bug tests were checked failing-first: with the drain loop removed
they report "left terminal input queued; it would reach the shell".

## Verified on a real pty

Driving the built binary, a report written shortly after the quit key:

| release arrives | before | after |
| --- | --- | --- |
| 20–60ms, no button held | leaks | swallowed |
| 100–140ms, button held | leaks | swallowed |
| 300ms, button held | leaks | leaks (past the ceiling) |

The last row is intended. That far out the terminal has long since
processed the disable and is no longer reporting, so there is nothing
real to strand.

Post-fix shutdown order, with the disables now ~50ms ahead of `Fini`'s
own copies:

```
^[]2;^G ^[[23;0t ^[[?1000l^[[?1002l^[[?1003l^[[?1006l^[[?2004l^[[?1004l ... ^[[?1049l
```

`make test` green across all 23 packages.

## Notes for next time

- The `-text` capture tool cannot answer questions about *bytes* — it
  replays into a grid. A raw-stream variant took about twenty lines on
  top of its `openPTY`, and running ced as `sh -c 'ced . ; cat -v'` is
  the trick that makes "what does the next reader see?" directly
  observable.
- First hypothesis was a truecolor SGR (`38;2;0;163;54m`) split by an
  interleaved `/dev/tty` write from `hostident`/`clipboard`. Ruled out
  by dumping every builtin theme's normalized palette: no theme derives
  rgb(0,163,54). The `<` and the lowercase `m` are what name it as a
  mouse release.
- This is not the last leak of its kind. Any future reporting mode ced
  enables belongs in `drainPendingInput`'s disable list beside the other
  three.
