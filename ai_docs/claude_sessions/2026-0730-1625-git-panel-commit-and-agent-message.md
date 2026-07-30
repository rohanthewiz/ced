# Session: Git panel commit rows + agent-drafted commit messages

Session ID: e7ca998b-8c72-4ae2-8d1d-94e74e6674e7
Date: 2026-07-30

### Ask

> "In the git panel, give me the option to commit a message. Allow the
> current chat agent to suggest a commit message based on the selected
> files"

### What shipped

Two related verbs in the git panel's `Actions ▾` picker (and their ≡
Git twins), plus one new file:

- `internal/app/gitcommitmsg.go` — the whole feature
  (+ `gitcommitmsg_test.go`, 9 tests)

**Commit the selection.** New `Commit <target>…` row. Targets are the
ticked files, falling back to the highlighted row (`gitPanelTargets`,
unchanged). It stages first and then commits with a pathspec:

```go
a.runGitCmdSeq("Commit "+what, [][]string{
    append([]string{"add", "--"}, paths...),
    append([]string{"commit", "-m", msg, "--"}, paths...),
})
```

The panel's tick is a *work-tree* statement, so committing only what
happened to be in the index would silently commit a stale version of
the file you ticked. The pathspec-limited commit leaves anything else
already staged untouched — that's what makes the row safe on a
half-staged tree. `Commit staged…` survives unchanged as the plain
index commit; `menuGitCommit` now routes through the shared
`openCommitPrompt(nil, "")` so both surfaces phrase it identically.

**Ask the agent for the message.** New row
`Suggest commit message for <target> (<agent>)`, gated on
`canSuggestCommitMsg()` — the agent-agnostic `chatAgentEnabled()` +
`!chat.dead`, so it works with Copilot, Claude Code, or Gemini equally.
Flow:

1. `gitPanelSuggestCommit` opens the chat panel FIRST (a request
   streaming into a hidden panel reads as a hang), flashes "Asking
   <agent>…", then forks git off-loop.
2. `loadCommitDiff` builds the payload: `git diff --stat --patch HEAD
   -- <paths>` so a half-staged file arrives as ONE change (a message
   drafted from half of it describes half the commit), retrying without
   `HEAD` on an unborn branch. Untracked targets have no diff at all,
   so their contents are appended under a `--- new file: rel ---`
   marker — otherwise a commit of only-new-files looks empty to the
   agent. Capped via the existing `truncateAtLine` (32KB total, 4KB per
   new file).
3. The result arrives as a `gitCommitDiffEvent`; `handleGitCommitDiff`
   echoes a short human-readable ask into the transcript (not the
   patch — the panel is a narrow strip) and sends the real prompt
   through `chatSendPrompt`, the single dispatch point. Mid-handshake
   it queues like the composer does.
4. `handleChatTurnDone` calls `chatCommitSuggestDone`, which lifts the
   draft out and opens the commit prompt **pre-filled**.

### Design decisions worth keeping

- **A suggestion is a normal, visible chat turn** — not a hidden second
  ACP session. Considered bypassing the panel with a private
  `session/prompt`, but `handleChatUpdate` filters updates by
  `sessionID`, so a private session's answer would be silently dropped;
  and a visible turn is stoppable, inspectable, and leaves the record
  of what the message was drafted from.
- **The answer is claimed by generation + transcript mark**
  (`commitSuggestReq{files, seq, mark}`), never "the last agent
  message". `chatAgentTextSince(mark)` collects agent prose written
  after the send point, and a transcript trim (the 500-message cap)
  makes the clamp return nothing rather than someone else's text. A
  stale generation is left alone; a cancelled/errored turn consumes the
  request and drafts nothing; `chatDisconnect` clears it.
- **Nothing commits without an Enter.** The draft only pre-fills.
- **`commitSubject` parses defensively** — fences, `Commit message:`
  labels, bullets, backticks and surrounding quotes come off and one
  line survives, clipped at 120 runes. The prompt field is single-line
  (a multi-line composer is still the known follow-up), so an unparsed
  answer would put a markdown fence in a commit.
- **Never steal the modal slot from a pending permission prompt** — the
  agent is blocked on that one. The draft stays readable in the
  transcript and a flash says so.

### Supporting changes

- `gitcmd.go`: new `runGitCmdSeq(label, cmds)` — several git commands
  in order on ONE goroutine, stopping at the first failure, posting a
  single `gitCmdDoneEvent`. Two `runGitCmd` calls would race, and two
  flashes for one gesture reads as a bug.
- `copilot_chat.go`: `chatState.commitSuggest` field, cleared in
  `chatDisconnect`, claimed at the tail of `handleChatTurnDone`.
- `app.go`: `gitCommitDiffEvent` in the dispatch switch; new ≡ Git row
  "Suggest commit message" (`hasSuggestableCommit` gate).
- `app_test.go`: menu geometry pins bumped for the added row — 74 → 75
  rows (2 top-zone + 63 group actions + 10 headers), height 80 → 81,
  dividers `[2, 5, 78]`, Git section 13 → 14 rows.
- `CLAUDE.md`: architecture map entry + a "Commit rows + agent-drafted
  messages" house-rules section.

### Tests

`gitcommitmsg_test.go` (9): subject parsing incl. clipping, wire-prompt
contents, the two new picker rows (present/absent by agent liveness),
a real-repo commit that proves only the target is committed and an
unrelated staged file stays staged, `loadCommitDiff` over a
staged+unstaged+untracked mix, the send path's `session/prompt` shape
(via `wireChat` + `waitForCopilot` — the turn call is async), the empty
-diff refusal, prefilled-prompt claim, stale/cancelled drops, and the
disconnect clear. Plus `TestRunGitCmdSeq_StopsAtFirstFailure` in
`gitcmd_test.go`.

`make test` (go test -race ./...) green.

### Not done

- Multi-line commit bodies — blocked on the single-line prompt modal /
  composer, which is already a tracked follow-up.
- No config key: the row is gated on the agent being available, so
  there is nothing to opt out of beyond the existing chat switches.
