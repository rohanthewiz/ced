# Session: MX Master wheel freeze at end of file → one frame per burst

Session ID: c6cd6be5-f98f-4421-ace8-534a9c8c5a6c
Date: 2026-09-22

## 1. The report

> The editor locked up when I scrolled to the bottom of the 4000+ line
> CLAUDE.md … I had scrolled pretty quickly to the bottom with a Logitech
> MX Master mouse.

Inside cats the WHOLE workspace hung; closing ced's pane freed it. Later
reproduced in macOS Terminal.app too, where the editor "unfroze itself"
after a while. `ced-hang.log` (stderr capture) was blank; CPU ~0%
afterwards.

## 2. Investigation (what was ruled out)

- **Editor-level render** at top vs bottom, source / soft wrap / markdown
  preview: flat and cheap (throwaway test in `internal/editor`).
- **App-level wheel + draw** over the real CLAUDE.md: ~0.6ms/step, no
  growth toward EOF (throwaway test in `internal/app`).
- **Real binary** via the run-ced capture harness: PgDn, paced wheel,
  1500-notch bursts, bursts interleaved with motion reports, the user's
  own config.json (copilot on) — all stayed responsive.
- **tcell `PostEvent`** is non-blocking (`ErrEventQFull`), so no
  self-deadlock on the event queue. ced sends no terminal queries after
  startup; ced's cats control-socket calls all carry a 3s timeout and none
  run on the wheel path.
- **cats side** (Explore agent over `~/projs/go/cats`): cathost writes
  every pane's input with a blocking `ptmx.Write` on ONE dispatch
  goroutine shared by all panes (`writePTY`, `internal/orchestration/
  host.go:147`), wheel reports are one SGR per line with no coalescing
  (`inputenc/encoder.go:220`), and emulator query replies are written
  back from inside the parse holding `emuMu`, which the daemon-wide
  flusher also takes. So one pane that falls behind on reading stdin
  freezes input/frames for every pane — why the freeze spread across the
  workspace. Not fixed (separate repo); flagged to the owner.

## 3. Root cause (in ced)

Once the user said the MX Master "sends scrolls even at the end": measured
the real binary with 3000 notches fired AFTER jumping to EOF — ~4s of CPU,
~1–1.7ms per event, the same mid-file. Run's loop did a full
`draw()` + `Show()` per event, so a free-spin's thousands of no-op scrolls
queued seconds of painting. Freeze = backlog; thaw = backlog drained.

## 4. Fix (committed e3a9a2a)

`internal/app/inputburst.go` (+ `inputburst_test.go`):

- `isBurstMouseEvent` — wheel on any axis, or motion with no button.
  Drags, presses, keys are NOT burst events.
- `(*App).deferFrame(ev, now)` — skip the frame when it's a burst event,
  another event is ALREADY pending (so Run never blocks over an unpainted
  screen), and the last paint was < `burstFrameMax` (33ms, ~30fps — a
  sustained spin still scrolls visibly).
- `App.lastFrameAt` stamped after every paint in `Run`.
- CLAUDE.md: architecture-map line + "One frame per input burst" house
  rules section.

Result: 3000 notches at EOF 3.96s CPU → 0.63s; the finder opened 400ms
after the burst. `go test -race ./...` green.

## 5. Other commits

- **ab8d3f9** — the owner's in-progress ≡ menu work, committed separately:
  late-arriving sections (Cats after the probe, plugin commands after a
  reload) inherit `menuFoldDefault`, the last bulk fold choice;
  `toggleMenuSection` flips the effective state.
- Two stray CLAUDE.md edits that landed while the editor was frozen
  ("A config Pfile / dotfile", a deleted blank line before `## Releases`)
  were reverted before committing.
- **2e1ab71** — Release ced 0.3.4 (`version.go` + `cats-plugin.toml`),
  tag `v0.3.4`.

## 6. Follow-ups

- cats: give each pane its own input writer with a bounded queue
  (coalesce/drop wheel reports under backpressure) and route emulator
  query replies through it instead of writing under `emuMu`, so one slow
  pane can never stall the workspace.
