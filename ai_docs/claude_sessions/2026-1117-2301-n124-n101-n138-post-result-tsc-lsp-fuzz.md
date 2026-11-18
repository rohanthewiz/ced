# N-124, N-101, N-138 — reliable completion events, tsc --lsp, benchmarks + fuzz

Session: `bf5fffcd-9ebf-4213-a664-3cfa9fde36c5`

## Ask

Second batch of the same `/work-next-list` run (the first batch is
`2026-1117-2243-n147-n122-n123-n126-detached-children-lsp-writer`).

## N-124 — completion events are never dropped (`77493f4`)

tcell's `PostEvent` drops on a full 256-slot queue. `postResult`
(internal/app/postresult.go) posts and, while the queue is full, retries
with a 1ms→20ms backoff until it is taken or `postResultGiveUp` (1m)
passes — the backstop for a closed / switched-away App whose queue nobody
drains. Two dead ends first: tcell's `EventQ()` / `StopQ()` are on its
INTERNAL screen interface, not `tcell.Screen` (the first draft didn't
compile), and `PostEventWait` is deprecated with no deadline.

Conversion by script: every `_ = scr.PostEvent(&X…` whose event is not a
tick and that sits inside a `go func` / `AfterFunc` / client callback, or
in one of five helpers only goroutines call (`catsPostNotice`,
`catsPostRunDone`, `chatServePermission`, `chatServeFS`,
`lspPostServerNote`) — 94 sites. A second script checked each converted
line is LEXICALLY inside such a closure (indentation walk). The 15 ticks
(blink, dwell, tooltips, expiries, tree tick, auto-scroll) keep plain
PostEvent. `TestCompletionEventsArePostedReliably` greps for
`_ = x.PostEvent(&…(Done|Ready|Exit|Result|Failed)Event`.

## N-101 (2) — TypeScript 7's `tsc --lsp` (`ca6ca85`, `faa3ed7`)

`npm install typescript@7` into the scratchpad was REFUSED by the
auto-mode classifier, and no earlier scratchpad had it, so this was built
against the 2026-11-06 probe's recorded behaviour and checked against a
stub.

- lspservers.go: `lspCommandTraits` per alternate command (keyed by
  argv[0]): `probe` (runs on the START GOROUTINE — `lspPickCandidate`;
  the PATH lookups stay synchronous via `installedCommands`, so "nothing
  installed" is still a sync silent death), `pullDiagnostics`,
  `settings`. TypeScript: `{"tsc","--lsp","--stdio"}` first, probe =
  `lspTSCProbe` (`tsc --version`, detached, 10s) → `tscVersionHasLSP`
  (major ≥ 7); then typescript-language-server.
- lsp.go: the goroutine picks the candidate; a pull server's own
  publishes are IGNORED (tsc's one empty publish on open would race the
  first pull); `workspace/configuration` answered from settings
  (`lspConfigAnswer`, dotted sections, a whole key like `js/ts` first);
  settings pushed by `DidChangeConfiguration` right after `initialized`;
  ready event carries `pull` → `lspServer.pull`. `lspPullDiagsIfPulling`
  after every didOpen / didChange, `pullSeq` per path (never reset on
  close), newest wins in `handleLSPDiags`, closed docs dropped.
- internal/lsp/pulldiag.go: `PullDiagnostics` (`kind`/`items` report; null
  or empty → empty non-nil = retract), `DidChangeConfiguration`. The
  `textDocument.diagnostic` CLIENT capability is deliberately not
  declared (a dual-model server might stop pushing).
- README: the server table names `tsc --lsp` (TypeScript 7+) first; the
  TypeScript note now gives both install routes.

Real binary against a python stub `tsc` (version 7, one empty publish on
open, pull answers a type error): log showed initialize → initialized →
didChangeConfiguration (settings) → didOpen → diagnostic → inlayHint, and
the frame drew `● … ✗ Type 'string' is not assignable to type 'number'.`

Slip: the commit went in with `lsp.go` unformatted (`gofmt -l` prints but
exits 0, so `&&` didn't stop it) — fixed in `faa3ed7`; commits since are
gated on `test -z "$(gofmt -l .)"`.

## N-138 — benchmarks and fuzz targets (`cc82869`)

Benchmarks (synthetic input, `benchGoSource` / `benchApp`; numbers on the
M3): Highlight 256KB 232 ms / 114MB; clean frame 0.98 ms; 1MB line 22 ms /
85MB a frame; Enter+Render 113 ms / 58MB vs typing+Render 0.9 ms;
SetFindQuery 100k lines 13.6 ms / 29MB; undo push in 20k lines 40 µs;
App.draw 0.93 ms; a 125-line paste through Run's loop body 179 ms.
`newTestApp` now takes `testing.TB`.

Fuzz targets (each run 15–30s): `FuzzReadMessage`, `FuzzReadLineMessage`,
`FuzzParseDiagnostic`, `FuzzParseConflicts`, `FuzzReadEncodeRoundTrip`,
`FuzzLexSQL`, `FuzzValidateJSON`. Finds:

- **Real bug, fixed**: `"` + U+52EE9 + newline (an unterminated string
  ending in a 4-byte character) was marked at column 4 of a 2-character
  line — the explainer marked the byte before the newline, the last
  continuation byte, and `offsetToLineCol` counted each continuation byte
  as a rune. It now snaps an offset inside a rune back to the rune's
  start. `TestValidateJSON_MarkNeverSplitsARune` fails without it
  (checked by stashing the fix); the corpus entry is committed.
- By design: a file mixing CRLF and bare LF is normalised to its majority
  ending on save (N-121h); the round-trip property skips mixed files.
- My own wrong assumptions, corrected in the targets: `format.Problem` is
  ZERO-based; `plugins.Diagnostic.Col` is `-1` for "no column".

## State

- `go test ./...` green after each item; `go vet ./...`; gofmt clean.
- CLAUDE.md: Events-only rule gained the `postResult` clause; the map
  gained postresult.go; the LSP section gained the command-traits note;
  the Tests section gained the benchmark/fuzz conventions.

## Next

Closed: N-124, N-138. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: N-101 → Validate.
Updated: N-128, N-129, N-130, N-132 (the benchmark that measures each,
and the budget test owed with its fix). Full list: `ai_docs/todo/next-list.md`.
