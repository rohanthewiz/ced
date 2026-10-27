# AI drafts: claim the answer when the ask queues behind the agent start

Session: `ea2f71c9-fa1e-4c50-adc0-55c7780ed286`

## Ask

1. From the cats-todo backlog: "The AI commit message button in the
   commit popup is not working."
2. "fix the GoNotes one too, then commit"
3. "commit and push GoNotes, then do /sess-wrap on ced"

## Diagnosis

The ✦ AI button (`openCommitPromptWith`, gitcommitmsg.go) was wired
correctly: click and Alt+a both reach `run`, `commitDraftBlockedReason`
starts the agent, the prompt closes, and `gitPanelSuggestCommit` forks
`git diff` off-loop. The break was in `handleGitCommitDiff`'s
`a.chat.starting` branch. It queued the prompt (`queuedPrompt`) but armed
NO `commitSuggestReq`, so when `handleChatReady` flushed the turn and it
finished, `chatCommitSuggestDone` had nothing to claim. The draft landed
only in the chat transcript and the commit prompt never came back.

That branch is the COMMON first-use path, not a corner. ✦ starts the
agent and forks the diff in the same gesture. The diff is back in
milliseconds while the ACP handshake takes seconds. A second press
worked only because the agent was ready by then.

The comment justifying it ("the queued send happens under a generation
we can't name yet") was wrong. `connSeq` moves only in `chatDisconnect`.
`chatEnsureStarted` launches the start under the current value and
`handleChatReady` drops a mismatch, so the queued send runs under the
`connSeq` that is current while `starting`.

GoNotes' ✦ title (`noteSuggestTitle`, gonotes.go) had the identical
branch and comment. There it was not a race but a certainty:
`chatOpenPanel` starts the agent synchronously right before the switch,
so the first ask after launch ALWAYS hit `starting`.

Separate, machine-local cause: `~/.config/ced/config.json` has
`"chatagent": "claude"` and `claude-code-acp` is not on PATH (only
`copilot-language-server` is installed). On this machine ✦ therefore
flashes "Claude Code chat unavailable — install claude-code-acp on PATH…",
which is the intended behaviour, not a bug.

## What shipped

Commit `9cba6c0`, pushed to `origin/main`.

- `handleGitCommitDiff` (gitcommitmsg.go): the `starting` branch now
  arms `commitSuggestReq{files, seq: a.chat.connSeq, mark, trailer}` like
  the ready branch. The mark is taken AFTER the "starting … will send
  shortly" info line, so only agent prose is read back. A failed start
  goes through `chatDisconnect`, which already clears the claim. The
  comment explains the generation reasoning, with a small flow diagram.
- `noteSuggestTitle` (gonotes.go): same change, arming
  `noteTitleReq{at, seq, mark, private}`.

## Tests

- `TestHandleGitCommitDiff_QueuedBehindStartReopensPrompt`: queued while
  starting → `handleChatReady` sends it → turn done → the commit prompt
  reopens pre-filled with only the agent's line and the target named.
- `TestHandleGitCommitDiff_QueuedAskDiesWithFailedStart`: a start exit
  clears both the claim and the queued prompt.
- `TestNoteSuggestTitle_QueuedBehindStartReopensPrompt`: the same
  round trip for GoNotes, with the `[private: on]` chip carried through.

## Verification

- Both `…QueuedBehindStartReopensPrompt` tests were run against the HEAD
  version of the source file and FAILED ("the queued ask has no pending
  request"), then passed with the fix.
- `go test -race ./...`: 25 packages ok, no failures.
- Not seen in the real binary: no ACP agent the config selects is
  installed here (N-096).

## Next

Closed: None. Declined: None. Raised: N-096, N-097.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
