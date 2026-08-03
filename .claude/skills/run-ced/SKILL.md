---
name: run-ced
description: Launch and drive the real ced binary to see what it actually draws — open files, menus, panels, pickers, themes — and capture the screen as greppable text or full-color HTML. Use whenever asked to run ced, screenshot it, show a theme, or confirm a UI change works in the real app rather than only in tests.
---

# Running ced

ced is a tcell TUI. It does nothing without a terminal on the other end
— `./bin/ced . < /dev/null` exits immediately, and the test suite's
`SimulationScreen` proves the draw functions run but not that the real
binary starts, sizes itself, and paints. **There is no tmux on this
machine.** The tool in `capture/` closes that gap: it allocates a real
pseudo-terminal, plays scripted keystrokes at ced, and replays the ANSI
byte stream it painted into a grid.

## Setup (both steps, every time)

```sh
make build                                                   # ./bin/ced
(cd .claude/skills/run-ced/capture && \
  GOFLAGS=-mod=mod GOPROXY=off go build -o /tmp/capture .)   # the driver
```

`capture/` is a **separate Go module** — deliberately, so its two
dependencies never enter ced's own `go.mod` (the "one static binary, no
surprises" rule). It's excluded from `go build ./...` and `make test` for
the same reason. `GOPROXY=off` works because both deps are already in the
module cache; drop it if that ever fails.

## Use it

```sh
/tmp/capture -bin ./bin/ced -dir . -text                     # read the screen
/tmp/capture -bin ./bin/ced -dir . -out /tmp/shot.html       # look at the colors
/tmp/capture -theme solarized-light -out /tmp/light.html     # a specific theme
/tmp/capture -help-script                                    # the script syntax
```

Two outputs, two readers, and picking the wrong one wastes a round trip:

- **`-text`** prints the screen as plain text. **This is the one you
  read.** It turns "did the file open / did the panel appear / what does
  the status bar say" into a `grep`. Always start here.
- **`-out`** writes HTML carrying every cell's real color. Use it for
  anything about the *palette*, and when showing the user. It's a
  standalone page by default; `-fragment` emits just the `<pre>` block
  for embedding in a larger page (see "Showing the user" below).

Other flags: `-dir` (project to open, default `.`), `-cols`/`-rows`
(default 150×44), `-config` (raw config.json contents), `-script`.

Every run gets a **throwaway `XDG_CONFIG_HOME`**, so a capture can never
read or write the developer's own `~/.config/ced`. The default config is
`{"copilot":"off","autosave":"off"}` — no sidecar, and no background
writes into the project being photographed. Pass `-config` to change it.

`-seed <dir>` copies a directory into that throwaway `<config>/ced`
before launch — the way to photograph anything that lives beside
config.json rather than inside it: a `plugins/` inventory, a `themes/`
folder, an `mcp.json`. It's a copy, not a symlink, so the run still
can't write back into your source.

```sh
mkdir -p /tmp/seed/plugins/demo && cp my-plugin.json /tmp/seed/plugins/demo/plugin.json
/tmp/capture -bin "$PWD/bin/ced" -dir /tmp/proj -seed /tmp/seed -text \
  -script '1800@;300@{esc}p;500@notes;700@{enter};1500@SNAP;300@{esc}q'
```

Note `-bin` there: `run` sets the child's working directory to `-dir`,
so a relative `./bin/ced` resolves against the project being opened, not
against ced's own checkout. Pass an absolute path whenever `-dir` isn't
the repo.

## Scripting keystrokes

Steps are `;`-separated; each is `[delayMs]@[payload]` (default 500ms),
where the delay is how long to wait **before** sending. Payload is
literal text plus `{esc} {enter} {tab} {up} {down} {left} {right} {home}
{end} {pgup} {pgdn} {bs} {space}`, with `{down x12}` to repeat.

The default script opens `main.go` through the fuzzy finder and scrolls
into it — tree, tab bar, line numbers, syntax colors, and status bar all
in one frame.

### Three things that will cost you a run if you forget them

1. **`SNAP` before you quit.** Quitting restores the terminal and clears
   it, so a capture taken after `{esc}q` is a blank screen. Put `SNAP` on
   the step *before* the quit. (Symptom: every output file identical and
   suspiciously small.)

2. **Two Escs must be separate writes.** `{esc}{esc}` in one payload does
   **not** open the ≡ menu — it arrives as a single `\x1b\x1b` and is
   folded into one Alt+Esc event. Split it:

   ```sh
   -script '1500@;400@{esc};120@{esc};800@SNAP;300@{esc}q'    # ≡ menu ✓
   -script '1500@;400@{esc}{esc};1200@SNAP;400@{esc}q'        # nothing ✗
   ```

   Leaders (`{esc}p`, `{esc}g`, …) are fine in one payload — it's only
   the bare double-Esc that folds.

3. **Give it time to boot.** Start with a `1500@` wait. ced builds the
   file index and starts integrations at launch; snapshotting at 300ms
   catches a half-drawn screen.

### Recipes

```sh
# open a file (the default script)
1500@;300@{esc}p;500@main.go;700@{enter};900@{down x12};900@SNAP;400@{esc}q

# the ≡ action menu
1500@;400@{esc};120@{esc};800@SNAP;300@{esc}q

# command palette, filtered
1500@;400@{esc}a;500@theme;800@SNAP;300@{esc};300@{esc}q

# theme picker, then pick the 3rd row and photograph the result
1500@;400@{esc}a;500@theme;800@{enter};700@{down x2};500@SNAP;400@{enter};900@SNAP

# git panel / git log / terminal
1500@;500@{esc}g;900@SNAP;400@{esc}q
1500@;500@{esc}L;1100@SNAP;400@{esc}q
1500@;600@{esc}`;900@SNAP;400@{esc}q
```

Leaders worth knowing: `p` find file, `a` palette, `t` sidebar, `f` find
in file, `g` git panel, `L` git log, `` ` `` terminal, `w` close tab,
`s` save, `q` quit. The menu's shortcut column is the source of truth.

## Showing the user

Terminal text in a reply loses every color, which is exactly what a
theme or styling change is about. For anything visual, capture with
`-out -fragment`, drop the `<pre>` blocks into a page, and publish it as
an artifact. Keep the page chrome neutral — a saturated accent will
fight whatever the editor is wearing.

`build-page.py` does this for the whole theme registry: it captures all
ten built-ins, pulls the resolved palettes out of the `theme` package,
and emits a comparison page.

```sh
python3 .claude/skills/run-ced/build-page.py /tmp/themes.html
```

## Caveats

- **macOS/POSIX only.** The PTY setup uses darwin's `TIOCPTYGRANT` /
  `TIOCPTYUNLK` / `TIOCPTYGNAME` ioctls rather than `grantpt(3)`. ced is
  already POSIX-only (the embedded grsh terminal), so this costs nothing.
- **Timing, not events.** Steps are sleeps, not "wait until drawn". A
  loaded machine can outrun a delay — if a capture looks half-painted,
  raise the waits before suspecting the editor.
- **The emulator is a subset.** It handles what tcell emits: absolute
  cursor moves, erase, SGR (truecolor, 256, and basic), and text. It has
  no scrollback and ignores private modes. Fine for photographing a
  full-screen TUI; not a general terminal.
- **The fuzzy finder matches paths, not just filenames.** `main.go` in a
  repo with several may open one you didn't mean. Type more of the path.
