# 2026-10-14 14:47 — N-042: tab group colour picker + coloured member underline

Session: `937b4687-e294-48bf-89ef-ad315761e142` (dropped from the
cats-todo backlog as pasted text)

## Request

Next-list item N-042, the tab group extras left out of the first cut:

- choosing a group's colour (groups get distinct ones automatically)
- a COLOURED member underline, if a capability check can tell which
  terminals take the colon-form SGR 58

## Research

- **tcell v2.13.9** writes underline colour as `58:5:n` / `58:2::r:g:b`
  (colon form), and only when it treats TERM as xterm-like: any name
  starting `xterm`, plus a few named entries (tmux, alacritty, konsole,
  gnome, kitty, ghostty …). `screen-256color` never gets SGR 58. Apple
  Terminal reports `xterm-256color`, so it WOULD get it. That's why the
  gate matters.
- The hazard: a terminal that splits colons like semicolons reads
  `58;5;n`, so 58 is ignored, 5 = blink and n = a stray SGR.
- A runtime query (XTGETTCAP) is out: the reply arrives on stdin, which
  tcell owns and would misparse as keys.
- A September 2026 vim review of terminal SGR parsers (PR #21412) lists
  iTerm2, Konsole, xterm.js, Contour, kitty, VTE, WezTerm, Alacritty,
  Ghostty, foot, Rio, Zellij, tmux, mintty and Windows Terminal as
  accepting both colon forms. It gives no version floors for iTerm2 or
  VTE.
- **cats** panes are parsed by libghostty-vt and get `CATS_ENV=1`,
  `TERM=xterm-256color`, `COLORTERM=truecolor`.

## What changed

- **`internal/app/undercolor.go`** (new): `underlineColorTerminal(env)`
  is an ALLOWLIST sniff, positive evidence only. The innermost
  multiplexer decides first, because outer variables leak into its panes:
  - `STY` (GNU screen) → no.
  - `TMUX` → yes only with `TERM_PROGRAM=tmux`.
  - `ZELLIJ` → yes.
  - `CATS_ENV=1` → yes.

  After that come `TERM_PROGRAM` (iTerm.app, WezTerm, ghostty, vscode,
  mintty, rio), terminal-specific TERMs (kitty, ghostty, foot, wezterm,
  alacritty, rio, contour), then `KITTY_WINDOW_ID`, `WT_SESSION`,
  `KONSOLE_VERSION`, and `VTE_VERSION` ≥ 5200 (a conservative floor; not
  verified). `resolveUnderlineColor(mode, env)` applies the override.
- **userconfig**: `"underlinecolor"` auto/on/off
  (`UnderlineColorMode`). It errors on a typo, like `"icons"`, and is
  hand-edited only.
- **app.go**: `App.underlineColor`, resolved in `loadUserConfig` through
  the `hostEnv` seam. It's false in a directly built App and in
  newTestApp.
- **tabbar.go**: a member's underline is
  `Underline(UnderlineStyleSolid, groupColour)` when vouched for, plain
  otherwise. The icon glyph's style is now derived from the tab's style
  (`st.Foreground(gfg)`), which fixes the underline breaking under the
  file icon. A test fails without that fix.
- **tabgroups.go**:
  - New "Change colour…" row in `tabGroupContextItems`, so the
    ≡ *Tab groups…* picker shows it too.
  - `pickTabGroupColor` lists six rows named by syntax role, each with a
    swatch, "current" and "used by <group>". Sharing a colour is
    allowed. The pick is stored as the INDEX.
  - New `tabGroupColorIndex` fold, used by `tabGroupColor` and
    `nextTabGroupColor`, and `tabGroupColorNames`.
  - The header comment gained two bullets.
- **palette.go**: `paletteItem.swatch` (a two-cell background block in
  front of the label; the label isn't changed, so matching is
  unaffected) and `paletteSwatchW`.
- **Docs**:
  - README tab-groups paragraph covers Change colour and the underline
    rule, with the config key.
  - CLAUDE.md tab-groups rule is rewritten (the underline used to be
    "never coloured") and `undercolor.go` is added to the architecture
    map.
- **Tests**:
  - `undercolor_test.go` (allowlist table incl. leak cases, resolve
    override).
  - In `tabgroups_test.go`: underline colour on/off, the picker, a stale
    group, the index fold.
  - Icon underline in `tabbar_test.go`, swatch in `palette_test.go`, and
    `TestLoadUnderlineColorValues`.
  - The pinned group menu rows in
    `TestChipRightClick_OpensTheGroupMenu` are updated.
- `make test` (race) green.

## Real-binary check

The run-ced capture tool was copied to the scratchpad with a `CAP_RAW`
raw-byte dump; the repo's tool is unchanged. A folder group was made
through the palette (Add tab to group → New folder group). Results:

| Mode | Environment | SGR 58 in the output |
|---|---|---|
| `on` | — | `ESC[58:2::77:179:128m`, equal to the chip background |
| `off` | — | none |
| `auto` | inside cats | yes |
| `auto` | `CATS_ENV` unset | none |

Picking "keyword colour" in the real picker flashed "Group api: keyword
colour". The underline moved to the new hue, which matched the new chip
background. Nobody looked at it by eye in kitty, iTerm2 or the other
listed terminals.

## Next

Closed: N-042. Declined: None. Raised: None. Deferred: None.
Promoted: None. Moved: None. Updated: None. Full list:
`ai_docs/todo/next-list.md`.
