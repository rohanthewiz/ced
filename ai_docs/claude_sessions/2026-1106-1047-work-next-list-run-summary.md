# `/work-next-list` run — summary

Session: `5448be56-e10b-47e5-87c5-e082f76486a3`

## Ask

1. `/work-next-list` over `ai_docs/todo/next-list.md`: every Open /
   Validate item that doesn't need the user, commit and push after each,
   a session wrap every 2–3 items.
2. `/sw` (this doc).

The run's work is recorded batch by batch in five docs; this one ties
them together and keeps the hand-offs in one place.

| Doc | Items |
| --- | --- |
| `2026-1104-1308-n080-n098-n089-label-plural-cmd-o` | N-080, N-089 (cats), N-098 (cats) + the triage |
| `2026-1104-1349-n107-n109-n097-subtle-floor-pathinside-draft-queue` | N-107, N-109, N-097 |
| `2026-1104-1442-n102-n093-return-hints-modal-replaced` | N-102, N-093; N-101 research |
| `2026-1104-1530-n108-n110-ctx-splits-readme-pins` | N-108, N-110 (part) |
| `2026-1104-1610-n067-sql-formatter-real-run` | N-067 (part), N-087 |

## Outcome

**Closed (10):**

- N-080 — "1 line" label (`0a0b92f`, `b8dd7ea`)
- N-089 — the cats help row is pinned to `CMD_TO_PANE` (cats `abacc56`)
- N-098 — ⌘O is forwarded (cats `3f77564`, ced `4a5676f`)
- N-107 — Subtle floored against LineHL (`fa465bf`)
- N-109 — `relEscapes` (`8d733c4`)
- N-097 — draft asks never overwrite the queue (`b0c15b9`)
- N-102 — return types read `-> T` (`f42cb7f`)
- N-093 — `replacedModal` (`52ed6c5`)
- N-108 — right-click splits (`9c42605`)
- N-087 — drag-select in every diff pane (`b1d70d5`)

**Partly done, still listed:** N-110 (the ⌘ and right-click README pins
landed in `d5efa48`; the in-surface Alt chords remain, since they have no
registry). N-067 (sql-formatter and the in-process rung were checked for
real; pg_format remains).

**Updated:** N-100 (⌘O to check in the mac app and Chrome), N-101
(TypeScript 7's `tsc --lsp --stdio` found), N-086 (the new drag-select
panes added to its hand check).

## Lessons from the run

- **Run the full suite before every commit.** `0a0b92f` went out after
  running only the Summarize/README tests. `TestEditorContextSelectionHandOffRows`
  pinned the old "(1 lines)" label, and main was red until `b8dd7ea`.
  Every later commit ran `go test ./...` + `go vet` first.
- **Order items so the guard lands first.** N-089 (the cats help-row
  pin) went before N-098 (adding ⌘O), so the new chord was test-guarded
  the moment it joined the set.
- **External installs:** npm installs into the scratchpad were allowed
  (typescript-language-server, TypeScript 5.9/7, sql-formatter). A
  GitHub release tarball (pgFormatter) was refused by the auto-mode
  check as code from an external source. That is why N-067's pg_format
  half and N-013 (rust-analyzer via rustup, zls) are left for the user.
- **Pre-existing, not ours:** cats `make test-ghostty` fails
  `TestLongGraphemeCluster` (a libghostty crash), with or without these
  changes. cats' own list tracks it as its N-068. `gofmt -l` flags
  `findalllive_test.go` and `openfile_test.go`, also from before.

## Waiting on the user

- N-067: `brew install pgformatter`, then ≡ File → Format file on a
  tab-indented and a 4-space `.sql` file, and with a root `.pg_format`.
- N-013: `rustup component add rust-analyzer`, and a zls install, then
  open a `.rs` / `.zig` file.
- N-101: TypeScript 7 — register `tsc --lsp --stdio` (it must be told
  apart from 5.x's `tsc`), document "needs TS ≤ 5.x", or both.
- N-103: swap pyright / basedpyright order?
- N-081: put the extra LSP rows in the right-click menu (it now has
  splits, which softens the "keep it short" worry)?
- N-069, N-095: contingent; left as is.
- The Validate hand checks in tmux / cats / Terminal.app / Chrome
  (N-010 … N-106); N-096 needs an installed, signed-in ACP agent.

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None — this doc only summarises the run; every list change is
recorded in the five batch docs above. Full list:
`ai_docs/todo/next-list.md`.
