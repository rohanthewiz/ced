# N-107, N-109, N-097: Subtle floor, one Rel-escape test, draft-queue refusals

Session: `5448be56-e10b-47e5-87c5-e082f76486a3`

## Ask

`/work-next-list`, second batch (the triage is in
`2026-1104-1308-n080-n098-n089-label-plural-cmd-o`).

## What changed

### A red build fixed first (`b8dd7ea`)

N-080's `0a0b92f` was committed after running only the Summarize/README
tests; `TestEditorContextSelectionHandOffRows` looked the right-click row
up by the OLD label ("(1 lines)") and went red. Fixed and pushed on its
own. Every commit after it ran the full `go test ./...` first.

### N-107 — Subtle floored against LineHL (`fa465bf`)

cool-blue states `line` == `line-hl` (#3b4252), so every modal border and
title rule (Subtle on LineHL: modals.go `chrome()`, the ≡ menu's
`borderStyle`) drew at 1.00:1. Measured all ten built-ins first:
Subtle/LineHL runs 1.13 (dark-city) … 1.49 (gruvbox), cool-blue 1.00.

- **Decision (minor):** of the item's two options, a derivation floor
  rather than editing cool-blue's `line`. The floor guards user themes
  with the same collapse (common in Nord-style palettes, where both are
  "the elevated grey"), and it changes only Subtle, where a new `line`
  would also move `md-code-bg` and the other derivations that read it.
- `ToTheme` maps `Subtle: subtleInk(line, line-hl)` (palette.go): the
  shared `LiftToContrast`, hue kept, to `minSubtleContrast` = 1.12 —
  just under the faintest built-in that reads, so the other nine keep
  their stated `line` exactly.
- Real binary (run-ced, cool-blue, palette open): the frame is `#4f5563`
  on `#3b4252`, close to Nord's own nord3 (#4c566a).
- Tests: every-builtin floor (fails without the fix), "readable themes
  untouched", and a light/dark collapsed-line lift for user themes.
- CLAUDE.md Themes gains the rule; the menu-split comment and CLAUDE.md
  line that said "Subtle is 1:1 on cool-blue" are updated.

### N-109 — `relEscapes` (`8d733c4`)

`pathInside` tested `HasPrefix(rel, "..")`, which calls a child named
`..x` outside. The new `relEscapes(rel)` (gitstatus.go) checks the whole
leading segment (`..` or `..` + separator — the only form
`filepath.Rel` emits). Nine more call sites asked the same question the
same wrong way (runexec, zipops, gitopstate, tabgroups, projectsearch
×2, recentlocations, tabbar, finder; finder and one projectsearch site
had the correct form spelled out longhand). All go through the helper
now. `TestPathInside` gained `/foo/..x` (inside) and `/..foo` (not);
`TestRelEscapes` is a table.

### N-097 — draft asks never overwrite the queue (`b0c15b9`)

The start-up queue (`chat.queuedPrompt`) is ONE prompt flushed as ONE
turn. ✦ commit message, ✦ note title — and Summarize, which the item
didn't name but had the same assignment — overwrote it.

- **Decision (the item's own default):** refuse with a flash
  (`chatQueueBusyReason`: "a chat message is waiting for <agent> to
  start — try again once it has sent"). Checked at every door: the ✦
  buttons' shared pre-check `commitDraftBlockedReason` (BEFORE the
  prompt closes, so its typed text survives), `gitPanelSuggestCommit`,
  `handleGitCommitDiff` (re-checked: the diff returns from a fork),
  `noteSuggestTitle`, `menuSummarize` (before `chatAttachOnce`, so no
  orphan attachment).
- **Converse, added on the same reasoning:** the composer's Enter
  APPENDS to the queue, which against a queued CLAIMED draft would make
  the agent answer both and pre-fill the mix as the draft.
  `chatQueueHoldsDraft` makes chatSend refuse there and keep the text.
  An unclaimed queue (a plain message, a Summarize) still takes appends.
- Five tests (`…RefusesOverAQueuedMessage` ×3,
  `TestCommitDraftBlockedReason_NamesAQueuedMessage`,
  `TestChatSend_RefusesToJoinAQueuedDraft`), all seen failing on the old
  code. CLAUDE.md's agent-drafted-messages rule records it.

## Tests

`go test ./...` and `go vet` before each commit; run-ced for N-107.
`gofmt -l` lists `findalllive_test.go` and `openfile_test.go` — drift
from before this session, untouched.

## Next

Closed: N-107, N-109, N-097. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
