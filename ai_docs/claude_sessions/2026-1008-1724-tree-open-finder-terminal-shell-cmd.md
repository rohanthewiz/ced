# 2026-10-08 17:24 — Tree: Open in Finder, Open in terminal, Shell command…

Session: `1954ae9b-9ebd-4133-acc8-45c835a68459`

## Request

From the cats-todo backlog: in the file tree's context menu add
- Open in Finder
- Open in terminal (configurable)
- Use `{{DIR_ENTRY}}` in a command, e.g. `ls {{DIR_ENTRY}}`

## What was built

Three new tree right-click rows directly under "Open in $EDITOR", on files,
folders and the root, each with a ≡ File twin acting on the active file
(else the project root — `openInEditorTarget`).

### Open in Finder — `internal/app/hostopen.go`
- macOS: file → `open -R <path>` (revealed, selected); folder → `open <dir>`.
- Elsewhere the label is "Open file manager" and it runs `xdg-open` on the
  folder (a file's parent). No `$DISPLAY`/`$WAYLAND_DISPLAY` → flashed reason.
- macOS over SSH still runs `open`, and the flash says the window landed on
  the Mac's own screen.
- Shared host-exec seam for the new verbs: `hostRun` (Setsid, stdin
  /dev/null, CombinedOutput), `hostEnv`, `hostGOOS`; `hostRunAsync` reads
  the seam on the main loop (race-safe vs test cleanup) and posts only a
  failure that comes within `hostFailWindow` (10s) — a terminal command
  that returns when its window closes must not flash an hour later.

### Open in terminal — `internal/app/openterminal.go`
- New config key `"terminal"` (`userconfig.Config.Terminal`, hand-edited, no
  ≡ row): `auto` (default) = cats pane → tmux `split-window -v -c <dir>` →
  ced's panel; `ced`; `cats`/`tmux` (fall back to the panel WITH a flash
  naming why); anything else is a command line run as `sh -c` from the
  folder with `{{DIR}}` expanded (e.g. `open -a Ghostty {{DIR}}`).
- File → its parent folder. In ced's panel the `cd` is SUBMITTED
  (`runInTermPanel`), staged only if the panel is busy; no cd at all when
  the shell is already there.

### Shell command… — `internal/app/entrycmd.go`
- History prompt (`openSearchPrompt`, new kind `history.EntryCommands` =
  `entrycmd`), seeded with the latest template or `ls -la {{DIR_ENTRY}}`.
- `{{DIR_ENTRY}}` = entry, `{{DIR}}` = its folder; absolute and
  `shellArg`-quoted (untrusted file names never reach the shell bare);
  `"{{…}}"`/`'{{…}}'` replaced whole; no placeholder → path appended.
- Runs (not stages) in ced's terminal panel at every tier; history stores the
  TEMPLATE.

### Fix: tree popup width
`contextModal` gained `w`, sized by `contextMenuWidthFor` (widest label + 6,
floor `contextMenuWidth`, ceiling screen width). Labels like "Add to
favorites…" previously ran past the 19-col frame.

### Other edits
- `app.go`: `terminalPref` field + load; three ≡ File rows (menu pins
  170→173 actions, 187→190 rows, height 193→196, dividers `[2,5,193]`).
- `newTestApp` pins `hostEnv` empty and `hostRun` refusing.
- README section "Open in Finder, Open in terminal, Shell command…";
  CLAUDE.md architecture map, menu pins, search-history kinds, and a rules
  bullet under Workspace.

## Verification
- `make test` (race) all green; new tests: `hostopen_test.go`,
  `openterminal_test.go`, `entrycmd_test.go`, `TestLoadTerminal`,
  `TestDefaults_TerminalIsAuto`.
- run-ced: popup draws all rows inside the border; Shell command… on `pkg/`
  ran `ls -la <abs>/pkg` in the panel; Open in terminal (cats env unset)
  submitted `cd …/pkg`. With the cats env inherited, it took the cats branch
  and split a REAL pane in the user's cats workspace (told the user).

## Next

Closed: None. Declined: None. Raised: N-057, N-058.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
