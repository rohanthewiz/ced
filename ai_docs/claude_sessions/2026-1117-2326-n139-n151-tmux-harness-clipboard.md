# N-139, N-151 — the tmux hand checks scripted in docker, and copy inside an untouched tmux

Session: `bf5fffcd-9ebf-4213-a664-3cfa9fde36c5`

## Ask

Fourth batch of the `/work-next-list` run (earlier:
`2026-1117-2243-…`, `2026-1117-2301-…`, `2026-1117-2313-…`). N-139 was the
highest-value item left; docker turned out to be up and
`apt-get install tmux` inside the local `golang:1.26.1` image was allowed
(host `npm install` had been refused).

## N-139 — tmux harness (`a2ff639`)

`.claude/skills/run-ced/`:

- `capture/` (the driver): `openPTY` split into `pty_darwin.go` (the old
  TIOCPTYGRANT code) and `pty_linux.go` (TIOCSPTLCK / TIOCGPTN); new
  `-raw FILE` (every byte the terminal received, whole run) and
  `-snapcmd CMD` (run at SNAP; with it `-text` prints the command's
  output instead of the built-in emulator's).
- `tmux/`: `Dockerfile` (golang:1.26.1 + tmux 3.5a, image
  `ced-tmux-harness`), `tmux.conf` (mouse, focus-events, set-clipboard
  on, allow-passthrough on, no status line) and `tmux-default.conf`
  (only mouse on + no status), `ced-in-tmux` (the driver's `-bin`: the PTY
  becomes a tmux CLIENT whose pane runs ced), `run.sh` (cross-compiles
  ced and the driver for linux, builds the image once, mounts binaries at
  `/hbin` — a nested mount under the read-only `/harness` failed — and
  passes `-snapcmd 'tmux -L ced capture-pane -p' -raw /out/raw.bin`),
  `checks.sh` (nine checks, PASS/FAIL, one shared build via HARNESS_BIN).
- SKILL.md gained "Through tmux (Linux container)"; CLAUDE.md's Build
  section points at `checks.sh`.

Geometry measured once from a capture: ✕ quit at column 32, ≡ at 36 of
row 1, the tree seam at column 30 (100×30).

First run: 8 PASS (Alt+r and Alt+x through Esc folding — screens read to
make sure the "lacks" checks weren't vacuous; clicks on ✕ / ≡; the ≡
hover label from a motion report; a seam drag 30 → ≥40; OSC 52 out of
tmux with the recommended config, payload decoded as `/work/main.go:1`)
and 1 FAIL: an untouched tmux put NO OSC 52 on its terminal. Results
recorded on N-070, N-073, N-078, N-082, N-086, N-090, N-094 (each still
has hand-only parts).

## N-151 — copy inside an untouched tmux (`84d6837`)

Raised from that FAIL. clipboard.go sent only the DCS passthrough form
inside tmux; tmux 3.3+ defaults `allow-passthrough off`, and the
package comment's claim that set-clipboard on is the default was wrong
(it is `external`, which also ignores a plain OSC 52 from a pane). A
probe in the container showed `printf … | tmux load-buffer -w -` DOES
reach the outer terminal under the defaults (tmux emits `52;;<b64>`
itself). Fix: with `$TMUX` set, `CopyToSystem` also runs
`clipboard.TmuxLoadBuffer` (`tmux load-buffer -w -`, detached through
internal/child, 5s cap, on a goroutine with `crash.Recover`); the
passthrough sequence still goes out. newTestApp pins the seam to a
no-op. `TestCopyToSystem_InsideTmuxAlsoAsksTmux`. The check's pattern
was widened from `]52;c;` to `]52;` (the load-buffer form has an empty
selection parameter) — the untouched-tmux row had NO OSC 52 at all
before the fix, so it still discriminates. `checks.sh`: 9/9. README's two
clipboard lines updated.

## Next

Closed: N-139, N-151. Declined: None. Raised: N-151.
Deferred: None. Promoted: None. Moved: None.
Updated: N-070, N-073, N-078, N-082, N-086, N-090, N-094 (harness
results recorded). Full list: `ai_docs/todo/next-list.md`.
