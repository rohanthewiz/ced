# Session: Named Themes — Ten Palettes, an 8-Key Derivation Table, and Live Switching

- **Session ID:** `dff866a9-150a-461e-b1e0-65256b993c64`
- **Date:** 2026-07-31
- **Branch:** main
- **Repo:** `ced`

## Request

> Do the same kind of extensive theming on CEd as you have done on
> `~/projs/go/cats/ai_docs/claude_sessions/2026-0731-0838-theming-system-named-themes.md`

That cats session built a named-theme system (ten palettes, a sparse-theme
derivation table, user theme files, a live broadcast to every browser). This
session ports the same shape onto ced's very different substrate: a single-
process TUI where "broadcast" is just reassigning a struct field, and where
the customization UI has to be the editor itself, because CLAUDE.md forbids
adding a settings dialog.

## What shipped

### 1. `internal/theme` — one hardcoded palette became three layers

- **`palette.go` (new)** — the color model.
  - `Palette` is `map[string]string` over **35 canonical keys**. A map, not
    a struct of `tcell.Color`: "was this key stated?" has to be answerable
    and zero is a real color.
  - **Only 8 keys are required** — `bg fg muted line accent ok warn err`.
    An ordered `derivations()` table fills the other 27: `sidebar-bg ←
    shadePanel(bg)`, `selection ← 32% accent over bg`, `syn-string ← ok`,
    `git-deleted ← err`, `syn-type ← mix(accent, ok, .45)`, …
  - A stated key always wins, and **later rules see the stated value**
    (`syn-operator` lightens whatever `syn-type` ended up being) — so the
    table is ordered by dependency, not alphabetically.
  - `Normalize` never mutates its input: sparse themes stay sparse on disk.
  - Color math on canonical `#rrggbb` strings (`mix`, `lighten`, `darken`,
    `shadePanel`, `Luminance`, `IsDark`); conversion to `tcell.Color`
    happens once, in `ToTheme`, which is written longhand so adding a
    `Theme` field forces a deliberate key decision.
- **`builtin.go` (new)** — `Spec{Name, Label, Dark, Colors, Source, Path}`
  and **ten themes** in presentation order (darks first, lights last —
  the audience skews dark and a mis-click into a light theme in a dim room
  is the one switch that actually hurts):
  `tokyo-night` (default), `darcula`, `gruvbox-dark`, `solarized-dark`,
  `cool-blue` (Nord), `super-warm`, `dark-game`, `dark-city`,
  `solarized-light`, `corporate`.
  Only tokyo-night is authored in full (all 35 keys) so it reproduces
  `theme.Default()` byte-for-byte; the other nine state their core eight
  plus the keys that give the scheme its character — which doubles as the
  working proof that the derivation table produces a usable editor.
- **`load.go` (new)** — the registry.
  - `~/.config/ced/themes/*.json`, in the shape
    `{"name","label","dark","colors":{…}}`. `name` defaults to the filename
    stem, `label` to the name, `dark` to a Rec. 601 luminance guess.
  - **A user theme shadows a built-in IN PLACE** (same name → same list
    position). Two identically-named picker rows is a bug report.
  - **A broken file is a warning, never a failure**: `Registry` returns the
    specs it could read alongside the errors it hit.
  - `Encode`/`Save` write in canonical key order (core eight at the top),
    atomically, with a `full` flag for expanded vs. sparse output.
  - `Resolve` falls back to `Default()` + a reason for unknown/broken names.
- **`theme.go`** — package doc rewritten to describe the layering;
  `Default()` kept as a hand-written literal *on purpose* (it's the floor
  when a file is broken or a preference is stale, so it must not be able to
  fail) with a test pinning it against the built-in of the same name.

### 2. `internal/app/theme.go` (new) — the editor's side

- `App.themeName` + `App.themeSpecs`; `loadThemes` / `applyThemeName` /
  `setTheme` / `restyleTabs` / `activeThemeSpec`.
- **A switch is a live restyle, never a restart.** `setTheme` assigns
  `App.theme`, repaints the screen default style, and marks every tab
  `StyleStale`. `Tab.Styles` is the *only* cache of theme-derived colors in
  the whole editor — every other surface builds its styles inside its own
  draw call — so that one line is the entire invalidation story.
- Three ≡ **View**-group rows (above the fold, same rationale as the
  terminal rows): `Theme: <name>…`, `Customize theme…`, `Reload themes`.
- The picker is `openPicker` (house rule) and **keeps** the current theme
  in the list, annotated `— current`, unlike the chat-model picker:
  re-picking is how a user reverts after previewing. `(light)` and
  `(custom)` suffixes are searchable by the fuzzy scorer for free.
- **The editing loop is the customization UI.** "Customize theme…" writes
  the active palette out *fully expanded* under a `-custom` name (so the
  original stays reachable), switches to it, and opens it in a tab;
  `themeAfterSave` (hooked into `saveTabAt`) re-reads the registry on any
  save under the themes dir. Change a hex, Save, watch it repaint. Running
  it while already on a user theme just opens their file — never rewrites a
  hand-kept sparse palette into 35 expanded lines.
- Same silent-degradation contract as LSP/formatters: unknown name, broken
  file, unwritable config → one flash, editor keeps running on the default.
- `themeDirFn` / `themeConfigPathFn` are package vars so `newTestApp` can
  point the whole feature at `t.TempDir()`.

### 3. `internal/userconfig` — `"theme"` key + themes dir

`Config.Theme`, the `theme` JSON key (trimmed + lowercased, **not**
validated — the registry includes files the user adds and removes between
runs), `SaveTheme`, and `ThemesDir()` beside `MCPPath()`/`RcPath()` so the
config locations can't drift.

### 4. Docs

- **README**: a "Themes" section — the ten-theme table, the eight-key
  minimal example, the roll-your-own rules, and the save-to-preview loop.
- **CLAUDE.md**: architecture-map entries for the four new files, a "Named
  themes" design-pattern block with the house rules, and a note in "What
  NOT to add" that a theme file is DATA (a fixed key list), not a plugin.

## Judgment calls worth remembering

- **`accent-soft` is pulled toward `err`, not merely lightened.** The first
  cut (`lighten(accent, .20)`) landed within a few percent of
  `syn-function` — which *is* the accent — so every sparse user theme would
  have painted keywords and calls in nearly the same color and looked
  cheap. `lighten(mix(accent, err, .45), .05)` lands in the purple-mauve
  range most schemes use for keywords. Pinned by
  `TestNormalize_KeywordsAndFunctionsDiffer`.
- **`shadePanel` branches on light/dark.** Dark themes step the sidebar
  down 18%, light themes only 6%: the same *relative* change on a
  near-white background is far more visible, and a heavily darkened light
  sidebar reads as an error state.
- **Status-bar contrast is a real constraint.** `drawStatusBar` paints
  `StatusBG` as the background and `theme.BG` as the *text*, so `accent`
  has to be bright enough on a dark theme for the background color to read
  on top of it. That's an assertion in `TestBuiltins_Legible`, not a
  comment.
- **Customize appends `-custom`.** A copy that kept the built-in's name
  would shadow it in place and make the original unreachable from the
  picker — surprising, and hard to undo without knowing where the file is.

## Gotchas encountered

- `TestShadePanel`'s first version compared *absolute* luminance drops and
  failed: 6% of a white background removes more light than 18% of a
  near-black one. The claim only holds in relative terms (Weber's law),
  which is also the term that matters perceptually.
- `App.status` doesn't exist — the flash field is `App.statusMsg`.
- `editor.Buffer` has no `SetText`; replacing a buffer's contents in a test
  is `DeleteRange(Position{}, EndPos())` + `InsertString`.
- `SimulationScreen` serves `GetContents` from the **front** buffer, so a
  draw test needs `scr.Show()` before reading cells (the existing
  `app_test.go` tests already carried that note).
- BSD `sed` has no `\b`; the `a.status` → `a.statusMsg` sweep needed
  `perl -pe 's/a\.status(?![A-Za-z])/a.statusMsg/g'`.
- Menu geometry pins move whenever a row is added: 79 → **82 rows**,
  height 85 → **88**, dividers `[2,5,82]` → `[2,5,85]`, and
  `TestMenuLayout_WithCustomActions`'s 88 → **91**.

## Files

- **New:** `internal/theme/{palette,builtin,load}.go` + tests,
  `internal/app/theme.go` + test (~2,340 lines including tests).
- **Touched:** `internal/theme/theme.go` (package doc, `Default()` doc),
  `internal/app/app.go` (App fields, `loadUserConfig`, three menu rows,
  `saveTabAt` hook), `internal/app/app_test.go` (`newTestApp` stubs,
  geometry pins), `internal/userconfig/userconfig.go` + test,
  `README.md`, `CLAUDE.md`.

## Verification

- `make test` (`go test -race ./...`) green across all 14 packages;
  `gofmt -l` and `go vet ./...` clean.
- New coverage: derivation completeness + input immutability + stated-wins,
  typo/missing-core rejection, hex canonicalisation, `mix`/luminance/
  `shadePanel`, registry shadowing / append / broken-file isolation,
  encode round-trip + canonical key order, the live-switch cache
  invalidation, unknown-name fallback, the save-to-preview loop, the
  no-rewrite rule, and picker row construction.
- **`TestBuiltins_Legible`** walks all ten themes asserting text /
  status-bar / muted / comment contrast gaps — a theme that fails is
  unusable, not merely ugly.
- **`TestDraw_UnderEveryBuiltinTheme`** paints a real frame under each
  theme through the simulation screen and checks the editor body actually
  wears the theme's background.
- A throwaway palette dump confirmed the derived values for a sparse
  8-key theme by eye before it was deleted.
- Not yet done: a visual pass in a real terminal. Try
  `≡ → Theme → Solarized Light` against a live tree.

## Follow-up ideas

- The picker could preview on highlight (apply as the selection moves,
  revert on Esc) — `openPickerWithCancel` already has the cancel hook the
  revert would need.
- Chroma's own token → color mapping (`internal/editor/highlight.go`) still
  matches by category; a theme can't reach token types the `syn-*` keys
  don't name.
- Image tabs (`internal/editor/image.go`) render against the theme, but
  nothing checks a light theme doesn't wash out the checkerboard.
- No `Esc-` leader for the theme picker — deliberate for now (the command
  palette finds it), but it's the obvious next binding if it gets used a lot.
