# N-016: Python inlay hints over workspace/configuration (premise checked)

Session: `6cb07b3d-005c-4ecb-a775-59de60105a26`

## Ask

1. Next list item N-016, pasted from the cats-todo backlog: "Inlay hints
   for servers that take their hint settings through
   `workspace/configuration` rather than `initializationOptions`
   (pyright). The auto-responder answers every configuration request
   with `{}`, so those servers never switch hints on. Contingent on
   N-013 showing it matters."
2. `/sw`

## Findings

The servers were installed into the session scratchpad (npm for pyright,
basedpyright and typescript-language-server; `uvx` for pylsp, with
`UV_CACHE_DIR` in the scratchpad). A throwaway Go probe drove each one
over stdio. It logged every server→client request and asked for
`textDocument/inlayHint` over the whole file. Two switches: declare
`workspace.configuration` or not, and which settings tree to answer
from.

| Server | ced's handshake | `workspace.configuration` declared, `{}` answered |
| ------ | --------------- | -------------------------------------------- |
| pyright 1.1.414 | no config request; inlayHint "Unhandled method" | asks `python`, `pyright`; still "Unhandled method" |
| basedpyright 1.40.2 | no config request; **6 hints** | asks `python`, `basedpyright`; same 6 hints |
| pylsp 1.15.0 | inlayHint "Method Not Found" | never asks; same |
| typescript-language-server 5.3.0 / TS 5.9.3 | 5 hints with ced's `initOptions`, 0 without | asks `formattingOptions`; 0 without initOptions |
| gopls | no config request; 5 hints from initOptions | asks `gopls`; `{}` keeps the initOptions hints |

- **The premise is false on both counts.** pyright has no inlay hints to
  switch on (they are Pylance-only), and pylsp has none either.
  basedpyright ships them ON. Answering its `basedpyright` section with
  `analysis.inlayHints.* = false` dropped it to 0 hints, so the route is
  real, but nothing needs it. The `{}` answer never comes into play: ced's
  `initialize` has never declared `workspace.configuration` (`git log -S`
  back to the first LSP commit, 1a4d1ff). Servers that read settings this
  way only ask once a client declares it.
- **The "gopls BLOCKS on workspace/configuration" comments were
  defensive assertions**, never observed under ced's handshake. gopls
  does ask once the capability is declared. The branch stays: a server
  that asks regardless would block on an unanswered request.
- **basedpyright's npm package installs a `pyright-langserver` alias**
  (its `package.json` `bin` maps both names). An npm basedpyright
  install therefore satisfies ced's FIRST Python command. A machine
  with real pyright AND basedpyright gets pyright first, and so no hints
  (N-103).
- **Real binary, basedpyright on PATH** (run-ced, `pyproj/demo.py`):
  `» x: LiteralString · name= "hi" · times= 3`, `» items: list[int]`,
  `» -> Literal[42]`. The line-1 return hint gave way to the caret-line
  diagnostic note (`⚠ Return type is unknown (+4 more)`), as designed.
  The `name= "hi"` spacing was the one defect.
- **TypeScript, the N-013 doubt:** `initOptions.preferences` IS the
  route (5 hints vs 0). In the real binary over TS 5.9: a `✗ Type
  'string' is not assignable to type 'number'.` note and inlay notes.
  The return-type hint reads `» : string` with no subject (N-102).
- **TypeScript 7 breaks typescript-language-server.** npm `latest` is
  `typescript@7.0.2`, with no `lib/tsserver.js`. The server reports
  "1.0.0, bundled" and sends an error showMessage on inlay requests. In
  ced the same file got no diagnostic, no hints and no flash for 9s+.
  Silent, but no TypeScript features at all (N-101).
- `~/.cargo/bin/rust-analyzer` is the rustup stub again ("Unknown binary
  'rust-analyzer' in official toolchain"), so rust-analyzer stays unrun.

## What shipped

Commit `bd9c399`:

- **`internal/app/lspinlay.go`**: `inlayParamPart` joins a parameter
  label and its argument on the label's own idiom. `name=` is a keyword
  argument and joins with no space (`name="hi"`). `level:` keeps it
  (`level: 3`). Pinned by `TestInlayNote_KeywordArgumentLabelsTakeNoSpace`
  using the labels basedpyright actually sent.
- **`internal/app/lspservers.go`**: the Python entry records the table
  above, which explains why it has no `initOptions`. The TypeScript entry
  records that its preferences were verified.
- **`internal/lsp/client.go`**: `autoRespond` explains that the
  `workspace/configuration` branch is defensive and which servers ask
  when. The `onRequest` and `ErrRequestUnhandled` notes are qualified to
  match. No code change.
- **`CLAUDE.md`**: the `onRequest` rule's parenthetical is corrected the
  same way.
- **`README.md`**: the inlay-hints bullet says Python hints come from
  basedpyright and that pyright and pylsp have none.

Verified: `go test -race ./internal/app/ ./internal/lsp/` passes. In the
real binary after the fix: `» x: LiteralString · name="hi" · times=3`.

## Next

Closed: N-016. Declined: None. Raised: N-101, N-102, N-103.
Deferred: None. Promoted: None. Moved: None.
Updated: N-013. Full list: `ai_docs/todo/next-list.md`.
