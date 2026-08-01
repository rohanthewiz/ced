# Session: Seeing the Themes — a PTY Capture Driver, and the run-ced Skill

- **Session ID:** `dff866a9-150a-461e-b1e0-65256b993c64`
- **Date:** 2026-07-31
- **Branch:** main
- **Repo:** `ced`
- **Continues:** `2026-0731-1829-theming-named-themes.md` (same session — that
  doc covers the theming feature; this one covers proving it visually and
  capturing the method as a skill)

## Request

> run it and let me see the themes

…then, after the visual pass:

> yes, keep the driver as a project skill

## The problem

ced is a tcell TUI. `./bin/ced . < /dev/null` exits immediately, and the
test suite's `SimulationScreen` proves the draw functions run but not that
the real binary starts, sizes itself, and paints. **There is no tmux on
this machine**, so the usual `send-keys` / `capture-pane` route was out.
And even with a captured screen, terminal text in a chat reply loses every
color — which is exactly what a theming change is about.

## What shipped

### 1. A PTY capture driver (first as a scratch tool, then as a skill)

- Allocates a real pseudo-terminal at 150×44 — on darwin that's the
  `TIOCPTYGRANT` / `TIOCPTYUNLK` / `TIOCPTYGNAME` ioctl dance, not
  `grantpt(3)` — and sets the winsize on **both** ends (a zero-sized pty
  makes tcell draw nothing).
- Spawns `bin/ced` with `Setsid` + `Setctty`, feeds it scripted
  keystrokes, and collects everything it writes.
- Replays that ANSI stream into a virtual screen grid: absolute cursor
  moves, erase, SGR (truecolor / 256 / basic 16), text with
  `runewidth`-correct advance. Enough to reconstruct a full-screen TUI
  exactly; not a general terminal (no scrollback, private modes ignored).

### 2. The artifact

Ten themes captured one run each (`XDG_CONFIG_HOME` pointed at a config
presetting `"theme"`), assembled into a comparison page and published:

**https://claude.ai/code/artifact/c0a1b7a7-af86-4455-b75c-b6189d8dc763**

Each card shows the theme's eight core colors plus a dimmer row of derived
ones, with `~` marking derivation. The authored-vs-derived counts landed
as a better argument for the derivation table than the commit message was:
`tokyo-night` 35 authored / 0 derived (the byte-for-byte reference), and
everything else **21–24 authored, 11–14 derived**.

Page design notes: the chrome is a deliberately cool-biased neutral,
because any accent would fight at least one of ten palettes — all
saturated color comes from the themes themselves, each card borrowing its
own accent as a local `--tint`. Mono-first type (the subject is a terminal
editor) with system sans for prose.

### 3. `.claude/skills/run-ced/` — the driver kept as a project skill

```
SKILL.md            recipes, gotchas, caveats
capture/main.go     PTY driver + ANSI→grid emulator
capture/go.mod      separate module (go-runewidth, x/sys)
capture/go.sum
build-page.py       captures all ten themes → one comparison page
```

Generalized well past the throwaway version:

- **A script language** — `[delayMs]@[payload]` steps separated by `;`,
  with `{esc} {enter} {tab} {up} {down} … {down x12}` tokens — so any
  surface is reachable, not just themes: the ≡ menu, the command palette,
  the theme picker, git panel, git log, terminal.
- **`-text` output.** The scratch version only emitted HTML, which is
  useless to an agent — you can't grep a color. `-text` prints the screen
  as plain text, turning "did the file open / did the panel appear / what
  does the status bar say" into a one-liner. `-out` (with `-fragment`) is
  for palettes and for showing a human.
- **`-theme` / `-config`**, `-cols`/`-rows`, `-help-script`.

## The three gotchas (each cost a run to find)

1. **`SNAP` before you quit.** Quitting restores the terminal and clears
   it, so a capture taken after `{esc}q` is a blank screen. The first pass
   produced ten identical 7,670-byte files before this was spotted — the
   symptom is *every output the same suspiciously small size*.
2. **Two Escs must be SEPARATE writes.** `{esc}{esc}` in one payload does
   not open the ≡ menu: it arrives as a single `\x1b\x1b` and folds into
   one Alt+Esc event. `400@{esc};120@{esc}` works. This is the same
   folding CLAUDE.md documents for tmux, showing up here for a different
   reason. Leaders (`{esc}p`) are fine in one payload — only the bare
   double-Esc folds.
3. **Wait ~1500ms for boot**, or you photograph a half-drawn screen (ced
   builds the file index and starts integrations at launch).

## Isolation decisions

- **`capture/` is a separate Go module on purpose**, so its two deps never
  enter ced's own `go.mod` (the "one static binary" rule). Verified
  excluded from `go build ./...`, `go vet ./...`, and `make test`. Both
  deps resolve from the local module cache with `GOPROXY=off`.
- **Every run gets a throwaway `XDG_CONFIG_HOME`**, so a capture can never
  read or write the real `~/.config/ced`. Default config is
  `{"copilot":"off","autosave":"off"}` — no sidecar, and no background
  writes into the project being photographed.
- **`build-page.py` reads palettes by asking the theme package**: it
  writes a temporary probe test into `internal/theme/`, runs it, and
  deletes it in a `finally`, so the swatches come from `Normalize` rather
  than a hand-copied table. Authored-key counts are parsed out of
  `builtin.go` the same way. Nothing on the page is transcribed by hand.

## Gotchas encountered (tooling)

- `go mod tidy` in a directory with no `.go` files silently empties the
  `require` block ("`all` matched no packages"), which then fails the
  build with "module lookup disabled by GOPROXY=off". Write the source
  first, or restore go.mod afterwards.
- Building offline needs `GOPROXY=file://$(go env GOMODCACHE)/cache/download`
  for the initial `tidy`; after go.sum exists, plain `GOPROXY=off` works.
- ced's fuzzy finder matches **paths**, not just filenames — capturing the
  ced repo itself with the default script opened
  `.claude/skills/run-ced/capture/main.go`, not the root `main.go`.

## Verification

- End-to-end cold run: deleted both binaries, ran
  `python3 .claude/skills/run-ced/build-page.py /tmp/themes-verify.html`
  — it rebuilt `bin/ced` and the capture tool, captured all ten themes on
  fresh PTYs, and produced a structurally identical page (10 sections, 10
  frames, balanced spans).
- The dominant background per capture came back `#1a1b26`, `#2b2b2b`,
  `#282828`, `#002b36`, `#2e3440`, `#2a1f1a`, `#12101c`, `#1b1f23`,
  `#fdf6e3`, `#ffffff` — exactly the ten authored palettes, so the
  captures are genuinely distinct and correctly themed.
- Probe test cleanup confirmed; `make test` green; `gofmt -l` clean.

## Commits

- `6f6a2ed` — Add named themes: ten palettes, sparse theme files, live switching
- `6da777c` — Add run-ced skill: drive the real binary on a PTY and capture the screen

## Follow-up ideas

- The driver is **timing-based, not event-based** — steps are sleeps, not
  "wait until drawn". A loaded machine can outrun a delay. A `-wait-for
  <regex>` flag that polls the grid would make long scripts robust.
- No golden-frame regression test yet. `-text` output is stable enough to
  diff, so a handful of committed reference screens would catch layout
  regressions that the unit tests can't see.
- macOS-only PTY setup. Linux would need `grantpt`/`ptsname` instead of
  the `TIOCPTY*` ioctls — worth doing if CI ever runs visual checks.
