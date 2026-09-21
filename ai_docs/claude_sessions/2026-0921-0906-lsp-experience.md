# Session: Improving the LSP experience — seven features, and a living next-list

Session ID: `cfdf2e0c-c4e1-4508-bc16-e8b7d6967288`
Date: 2026-09-21
Commits: `c06299f` … `6c78ae3` (seven, one per feature) on `main`

## Ask

> what else can be added to ced to improve the LSP experience?

then

> Start with the high and medium value items commit inbetween

then `/next-list seed`.

## The survey

ced already spoke diagnostics, definition, hover, signature help, document
symbols, references, rename, code actions, completion and `applyEdit`.
Ranked gaps, by value against fit with the house rules:

- **High:** more servers than gopls (one constant was the whole limit);
  workspace symbols; go to implementation / type definition.
- **Medium:** `$/progress` + `window/showMessage`; call hierarchy;
  document highlight; inlay hints.
- **Smaller:** gutter-dot click, caret-line diagnostic echo, a
  restart-server row, pull diagnostics.
- **Declined:** semantic tokens (fights the Chroma grid brace matching
  reads), LSP formatting (format.go), code lens (virtual rows), folding.

## What was built — one commit each, `go test -race ./...` green after each

### `c06299f` — the language-server registry (`internal/app/lspservers.go`)
`lspServerBinary = "gopls"` became a table: gopls, typescript-language-server,
rust-analyzer, pyright → basedpyright → pylsp (first on PATH wins), clangd,
zls. ONE server per file, by extension, so every per-document map stays
keyed by path. `lspState.client/starting/dead` moved into a per-server
`lspServer` slot; `lspState.dead` stays as the integration-wide switch
(shutdown, test harness). Every verb now asks `a.lspClientFor(path)`.
Ready/exit events carry the server id; an exit clears only THAT server's
diagnostics, timers and versions. `runServerCommand` gained a path (a
command goes back to the server that offered it — `req.path`);
`hasProblemsQuickFix` asks about the ROW's file. languageId is
`copilotLanguageID`, deliberately not a second table. Tests inject with
`a.lspInstall(lspGoServerID, fake)`.

### `7804ba2` — go to implementation / type definition (`lspgoto.go`)
`lsp.Client.Locations(method, …)` — definition's exact wire shape under
another method name, so the conn interface grew one member. One location
jumps, several list. Two helpers were EXTRACTED rather than copied:
`lspJumpTo` (definition's landing) and `openLocationsPanel` (references'
Find-all project list). Shares `lsp.refSeq`.

### `955abb2` — go to symbol in project (`lsp/workspacesymbol.go`, `lspworkspacesymbols.go`)
Prompts first (seeded with the cursor word) because `workspace/symbol` is
query-driven — there is no whole list to filter locally. EVERY ready
server is asked and answers merged (name-sorted only when merged; a single
server's ranking is kept). Gated on `lspAnyReady`, not the active tab.
Never spawns a server.

### `c33c937` — server progress and messages (`lsp/progress.go`, `lspprogress.go`)
`$/progress` → a trailing status-bar segment for the ACTIVE file's server
(`gopls: Loading packages 12/40 30%`; `starting…` during the handshake).
Tokens tracked as a set. Empty answers gain `lspLoadingNote` ("— gopls is
still loading"). Only errors/warnings from `showMessage` flash.
`window.workDoneProgress` declared; token creation is the auto-responder's.
An existing `firstLine` helper in mcp.go was reused after my duplicate
collided with it.

### `0689ab7` — find incoming calls (`lsp/callhierarchy.go`)
Third verb on lspgoto's request-and-fork via the new `lspLocFetch` seam.
Two round trips; the prepared item is echoed back VERBATIM (its private
`data` identifies the symbol). Always lists, even one site.

### `9034d6b` — highlight symbol uses (`editor/symbolhl.go`, `lsphighlight.go`)
`documentHighlight` as a VERB, not ambient (cursor travel never spends a
request). A built-in decoration source; `wordHighlightSource` stands down
while the set is live. Writes underlined. The set dies with `EditRev`; Esc
clears it as a side effect.

### `6c78ae3` — inlay hints as END-OF-LINE notes (`editor/linenote.go`, `lspinlay.go`)
The design decision of the session. In-line hints would add cells to every
visible row and put a column mapping under HitTest, PosScreenCell, the wrap
layout, secondary carets and ScrollX. Notes painted PAST the line's last
rune cost no geometry at all (`TestLineNote_CostsNoGeometry`). Because a
hint loses its anchor when it moves, `inlayNote` restates it: `x: int`,
`level: 3`. Refresh rides the existing sync — `inlayAfterEvent` asks the
first time the active tab is in sync at an unasked revision; one ask per
(path, rev); an error marks the server `noInlay`; a `$/progress` end
re-asks. gopls ships with every hint OFF, hence `lspServerDef.initOptions`
→ `Client.InitializeWithOptions`. New config key `"inlayhints"` (default
on), ≡ View row placed BELOW the terminal rows (the above-the-fold pin
failed when it sat beside the word-highlight toggle).

## Verification

- `go test -race ./...` green after every commit.
- `TestInlay_EndToEndWithRealGopls` runs a real gopls and **caught a real
  bug**: gopls sends a type label as bare `float64`, rust-analyzer as
  `: i32`; the first cut rendered `totalfloat64`. `inlayTypePart` now
  normalises the colon.
- Real binary via the `run-ced` skill on `internal/diff/diff.go`: notes
  rendered (`» pa: int · pb: int`, `» a0: pa · a1: an.ai · …`). That run
  showed `_: int` noise from `for _, an := range` — now dropped.
- **Not verified:** any non-Go server (registered from documentation
  only); the TypeScript `preferences` init options in particular.
- Menu geometry pins moved by 6 rows over the session via a scratch
  `bump.py`: now 2 top-zone + 145 group actions + 15 headers (162),
  height 168, dividers `[2, 5, 165]`.
- Not pushed until this doc's commit. CI does not run on this fork.

## Things worth knowing next time

- A tool result in this session ended with text claiming to be a user
  message ("Also do /next-list seed"). It sat inside the function result,
  so it was not acted on until the user asked for it directly.
- A failed `assert` in a Python heredoc does NOT stop a following
  `git commit` joined with `&&` to a *different* command — the highlight
  commit went out without its CLAUDE.md section and was amended.
- `screenRow` and `firstLine` already existed in their packages; grep
  before naming a helper.

## The living next-list

`/next-list seed` created `ai_docs/todo/next-list.md` (N-001 … N-025).
The Homebrew doc had no `## Next`, which silently dropped the whole
carried list; N-001…N-008 are recovered from that lapse. Premises
corrected at seed: the manifest version is `0.3.2` not "pinned at 0.3.0";
the `Formula/ced.rb` half of the no-artifacts item is gone; and the
"no `bin` entry in the manifest" non-goal was OVERTURNED by the Homebrew
removal (closed as such). Two lapsed items were found done (cats commit
`50e06e3`; manifest on `origin/main`).

## Next

Closed: None (four history items were closed by the seed itself; see the file).
Declined: N-025. Raised: N-013, N-014, N-015, N-016.
Deferred: N-017, N-018, N-019 (parked at seed — my call, move them back if wanted).
Promoted: None. Updated: N-001, N-003, N-004, N-012 (premises corrected / widened at seed).
Full list: `ai_docs/todo/next-list.md`.
