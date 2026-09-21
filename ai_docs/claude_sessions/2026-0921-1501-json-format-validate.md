# Session: JSON formatting + validation, and the diagnostic split it exposed

Session ID: `4d3d7cb2-61bf-4b53-b293-db32e08db92f`
Date: 2026-09-21
Branch: `main`

## Ask

> Apart from .go, add formatters and validators for common types like
> JSON, YAML, TOML

## Three decisions taken before writing anything

The ask has real forks in it, and two of them change the architecture,
so they went to the owner rather than being guessed:

1. **Engine** → *hybrid*: an installed external tool if there is one,
   ced's own in-process pass otherwise.
2. **Validators** → *diagnostics in the gutter*, not a save-time flash.
3. **Scope** → *JSON first, end to end*, with YAML/TOML slotting into
   the proven seam afterwards.

A fourth question came up mid-build and is the reason this session is
bigger than "add a formatter" — see the merge seam below.

## What shipped

### The formatting ladder (`runFormatOnSave`)

```
project .ced/format.json     the repo's own answer, trust-gated
an installed external tool   the ecosystem's answer (builtin.go)
ced's own in-process pass    always available (inprocess.go)
global-defaults install offer
```

`BuiltinCommandsFor` grew a `rootDir` parameter so a tool pinned in the
repo's own `node_modules/.bin` outranks one that merely happens to be on
the developer's `$PATH`. A prettier the repo pinned is a statement by
*that repo*; one on `$PATH` is a statement about a laptop.

The in-process rung is what stops this being a feature that is inert on
most machines it ships to. Go got built-in formatting free because gofmt
comes with the toolchain; JSON has no equivalent, and an
external-tool-only implementation would have been the
theoretical-feature trap `plugins/diag.go` already warns about.

### The validator

A `DecorationSource` (`app/validate.go`) at **git < validate < plugin <
LSP**, glyph `◇` — distinct from the LSP's `●` and the plugin layer's
`◆` on purpose. Debounced 400ms, armed only while the active tab is a
kind ced validates, parsed once at `wireTab` so an already-broken file
shows its mark on open.

### The merge seam (the unplanned half)

Fanning out a mapping agent over the diagnostic consumers turned up a
split nobody designed:

| surface | read | plugin findings visible? |
|---|---|---|
| gutter mark + underline | own source | yes |
| overflow markers | both caches | yes |
| diag tooltip | `a.lsp.diags` | **no** |
| Problems panel | `a.lsp.diags` | **no** |
| next/prev problem | `a.lsp.diags` | **no** |
| status-bar counts | `a.lsp.diags` | **no** |

Those four were typed to `[]lsp.Diagnostic`, so a non-LSP finding was
excluded at the *type* level. A validator built on the plugin template
would therefore have produced **a red underline whose message could not
be read anywhere** — half the feature, and the half that matters.

So `app/diagmerge.go`: `a.diagsFor(path)` merges LSP + plugin +
validator, and the four surfaces read it. Closes the plugin gap on the
way past.

## The things that would have been bugs

Recorded because each was a real fork, not a style point.

**`encoding/json` reports the byte AFTER the offending one.** Probed it
rather than guessing — `src[off-1]` is the bad character in every error
shape, the unterminated-document case included. The first draft used
`Offset` as given and would have underlined one cell right, every time.

**Columns are runes, not bytes.** The parser counts bytes; editor `Span`
columns are rune indices. Any non-ASCII above the error pushes the
underline right one cell per extra byte. `café` in a fixture pins it.

**Synthetic diagnostics must be re-encoded to UTF-16.** Every consumer
runs `editorPosFor` on the way out, which decodes `Character` as UTF-16
code units. A fabricated diagnostic carrying a raw rune column is
silently shifted on any line holding an astral-plane rune — an emoji,
which a JSON string may perfectly well contain. The test uses 😀
specifically so the two encodings differ, and asserts they differ, so a
future fixture change can't quietly stop exercising the conversion.

**`diagsForRange` must NOT use the seam.** Code actions echo diagnostics
back to gopls *verbatim* — their server-private `data`/`code` fields are
how a quick fix finds the problem it fixes. A synthetic one has no `Raw`
and means nothing to the server. Left on `a.lsp.diags` and pinned by
`TestDiagsForRange_StaysOnLSPOnly`.

**`jq` is unusable here and that is structural.** `execFormatterChain`
runs an explicit argv with no shell — the reason a malicious format.json
cannot chain commands — so a stdout-only formatter has nowhere to put
its output, and `jq . f > f` truncates `f` before jq reads it. Every
external command in the table must rewrite in place. Documented as the
test any suggested addition has to pass.

**`json.Indent`, never a Marshal round trip.** Marshalling through a map
reorders every object (Go randomises map iteration — two saves could
differ), pushes numbers through a float64 (`1e3` → `1000`, precision
lost on big integers) and re-escapes strings. Indent is whitespace-only.
Fed *trimmed* bytes, or it indents one level deeper every save — a
formatter that is not idempotent fights the file.

**JSONC is carved out by name.** `tsconfig.json`, `jsconfig.json`,
`.eslintrc.json`, `.babelrc.json`, `devcontainer.json` and everything
under `.vscode/` are JSON *with comments*. Underlining their first `//`
would be flagging a file for doing exactly what its ecosystem intends.
The `.vscode` rule is per-folder because the convention is.

## Two gaps found by running the real binary

Both invisible to the test suite, both caught by `run-ced`.

**`Esc-i` was dead on JSON.** `menuHoverInfo` returned early whenever
`hasLSPActions` was false — correct while the LSP was the only producer
of diagnostics, wrong now. `.json` has no server ced ships a mapping
for, so the validator's message was reachable by mouse and from the
Problems panel but **by no key at all**. It now answers with the
diagnostics alone; still silent when there is genuinely nothing to say.

**The save flash read badly.** Label was `format json`, giving
"Formatted with format json". Now `json formatter`, which reads as the
name of a tool in all four templates the label is substituted into —
matching what the external rung's label already is.

Verified live: `◇` on the right line, tooltip reading `✗ invalid
character '}' looking for beginning of object key string (ced)`, status
bar `✗ 1`, Problems row `✗ broken.json:5 │ …`, a minified file formatted
on save, and a broken file left byte-for-byte untouched with the reason
flashed.

## One correctness gap closed in review

Edit a JSON file, switch tabs inside the 400ms window: the tick fires
against whatever tab is in front, and the edited one keeps findings
pinned to a revision it has moved past — marks *and* Problems rows gone
until it is typed in again.

Fix: `validateTab` records the revision for **every** validated buffer,
clean ones included, so "has this been checked?" is answerable;
`validateAfterEvent` re-arms on a stale active tab. A rev map holding
only broken files could not tell a clean file from an unexamined one and
would have re-armed forever on every clean file — the caret-blink
constraint. Both the recovery and that guard are pinned.

## Files touched

New:

- `internal/format/kinds.go` + test — one table decides what a file IS,
  so the three verbs can't disagree
- `internal/format/inprocess.go` + test — ced's own JSON pass
- `internal/format/validate.go` + test — the syntax check and `Problem`
- `internal/app/validate.go` + test — debounce, revision gate, source
- `internal/app/diagmerge.go` + test — `diagsFor`, every producer

Modified:

- `internal/format/builtin.go` — `rootDir` param, JSON table,
  `resolveTool`, `BuiltinHandles`
- `internal/app/format.go` — the in-process rung + `formatFileInPlace`
- `internal/app/app.go` — state field, event case, dispatch tail,
  `wireTab` registration + first parse, `closeTab`, `Close`
- `internal/app/diagtip.go`, `problems.go`, `lsp.go` — read the seam
- `internal/app/lspcodeaction.go` — comment pinning the exclusion
- `internal/app/lsp.go` — `Esc-i` fallback with no server
- `internal/editor/fileio.go`, `tab.go` — `writeFileAtomic` exported,
  rather than a second copy in the format path
- `CLAUDE.md` — two new sections, architecture map, ladder rewrite

`make test` green under `-race`.

## Follow-ups

- **YAML and TOML.** They slot into `format.kindFor` plus one case in
  each of the three verbs. Both need a pure-Go dep (`gopkg.in/yaml.v3`,
  `BurntSushi/toml`) — no CGO, so the single-static-binary promise
  holds, but it is the first new dependency this feature would add and
  the owner should call it.
- `gofmt -l` flags `internal/editor/tab.go` and
  `internal/lsp/inlayhint_test.go`. Both were already unformatted at
  HEAD; left alone deliberately.
