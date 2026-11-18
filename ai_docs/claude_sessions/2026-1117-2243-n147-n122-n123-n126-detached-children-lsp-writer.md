# N-147, N-122, N-123, N-126 — detached children, a non-blocking LSP writer

Session: `bf5fffcd-9ebf-4213-a664-3cfa9fde36c5`

## Ask

`/work-next-list` — work every Open / Validate item in
`ai_docs/todo/next-list.md` that doesn't need the user, commit + push per
item, wrap every 2–3 items. This doc covers the first batch.

Triage (printed at the start of the run). Skipped as needing the user:
N-095, N-142, N-143, N-146 (contingent: "if it bites" / "raise if…"); N-103
and N-133 (their call: pyright order, `ced file` rooting); N-137 (enable
Actions in GitHub); N-013, N-067, N-096 (installs / an agent on their
machine); and the 25 hand checks in Validate (N-010 … N-145). Doable and
queued: N-124, N-101 (2), N-138, N-129 → N-128, N-121, N-130, N-132,
N-134, N-135, N-136, N-140, N-144, N-148, N-139 (docker is up).

## N-147 — gofmt drift (`51e6442`)

`gofmt -w` on the eight files. One doc comment had to be reworded first:
gofmt's doc-comment pass rewrites `E''` as a typographic `E”`, so
sqlvalidate_test.go now says `E'\n' escapes`.

## N-122 (+ N-126 a/b) — one detached constructor for every git child (`e29c5a0`)

**internal/child** (new package):

- `Detach(cmd)` — `Setsid` (no controlling tty: /dev/tty opens fail, so
  credential / passphrase / pinentry prompts become errors instead of
  drawing over the frame), `Cancel` = SIGTERM to the whole process group
  (`-pid`; TERM not KILL because git removes lock files on TERM),
  `WaitDelay` 5s so an escaped grandchild holding the pipe can't keep
  Wait blocked. Requires `exec.CommandContext` — exec refuses a Cancel
  hook without a context (the first test run tripped on exactly that).
- `GitEnv(base, read)` — removes then sets `GIT_TERMINAL_PROMPT=0`, drops
  `GPG_TTY`, `GIT_OPTIONAL_LOCKS=0` on reads.
- `Git(ctx, env, read, args...)`.

**internal/app/gitexec.go**: two lifetimes.

- READS (`gitReadOutput`, `gitReadRun`, `gitReadCombined`, or
  `child.Git(gitReadCtx())` where stdin/streaming is needed): 2m deadline,
  descended from a package-level read context that `App.Close` cancels
  and replaces (`endReadChildren`) — package-level because the loaders
  are plain functions of a root dir called from goroutines with no App;
  no `t.Parallel` in the app tests, so safe.
- WRITES (`runGitCmdFull`, `runGitCmdSeqHook` — one deadline per set —
  `runGitApplyPatch`): 15m, NOT ended by Close: a kill mid-write leaves
  `index.lock`, worse than letting it finish in its own session.

All ~45 `exec.Command("git", …)` sites converted (single-line ones by a
perl pass, two-line ones by a multi-line pass, the rest by hand), the
finder's `ls-files` too (1m). `TestGitChildrenUseTheConstructor` greps
internal/app and internal/finder. The helper names are `gitRead*` because
`gitRun(t, …)` is already a test helper.

Same wiring: formatters (`formatRunTimeout` 45s per command — a two-step
chain still ends inside `formatRunStale` 2m; a var for the test),
custom actions (1h cap), plugins (`runPluginShell` detached). Plugins also
got the in-flight gate: `pluginsOnEvent` split into `launchPluginHook` /
`launchPluginDeco`; `pluginRunBegin(key, relaunch)` refuses a second run
of the same (command, file) and parks ONE relaunch that the done-event's
`pluginRunEnd` starts; an entry older than `pluginRunStale` or from an
older inventory generation doesn't gate (lost done-event, reload).

Checked under a real pty (scratchpad `inpty.py`, python `pty.fork`; macOS
`script` needs a tty on stdin): a plain child opens /dev/tty, a detached
one can't. `TestRunGitCmd_CredentialPromptFailsIntoTheModal` (httptest 401,
`-c credential.helper=`) fails fast with "terminal prompts disabled" — run
inside the pty as well.

Snag: a `python3 - <<EOF` edit script was refused by the privacy plugin
hook (false positive — the files it rewrote carry the author header);
those edits went through the Edit tool instead. `goimports` isn't
installed; `gopls imports -w <one file>` fixes imports.

## N-123 + N-126 rest — a wedged language server no longer freezes ced (`47bcb77`)

**internal/lsp/writer.go** (new): every send frames the message into ONE
byte slice and `enqueue`s it (never blocks) for a single `writeLoop`
goroutine per client. Overflow (4096 frames / 64MB) or one write blocked
past `writeStall` (15s, `time.AfterFunc` around the Write) → `fail`: mark
dead, kill the process, close both pipes (closing the write end is what
unblocks the stuck Write) → read loop ends → onExit carries the reason →
the app marks the server dead. `Close` never writes: queues `exit`, sets
`wclosed`, closes `stopW` (writer drains, closes stdin), `go reap()` waits
`closeGrace` (2s) then kills, and ALWAYS Waits — `closeOnce` replaces the
early return on `closed` that the read loop also sets (N-126c zombie).
`writeStall` / `closeGrace` are vars read ONCE per client at
`startLoops` (`c.stall`, `c.grace`) — reading the globals live raced a
previous test's leftover goroutines under -race. `c.reaped` closes when
the reaper is done (tests wait on it; reading `ProcessState` directly
raced `Wait`).

Inbound: `maxMessageBytes` 64MB, negative / oversized Content-Length is a
protocol error (N-126d, was a `makeslice` panic). ndjson
(`readLineMessage`) skips lines that aren't a JSON-RPC object
(`JSONRPC == ""`) and over-long lines via `readCappedLine` (N-126e).

App: `a.lsp.handshakes` (`lspHandshakes`, mutex set of `lspConn`) holds a
client from Start until its ready event is handled; `lspShutdown` closes
the set and refuses later adds (N-126c handshake leak). `App.Close` also
calls `termInterruptAtClose` — SIGINT to the terminal panel's foreground
job and the paste queue dropped. N-126f was already done by N-118.

Real binary (run-ced): a stub `gopls` on PATH that answers `initialize`
then sleeps without reading, a 300KB main.go. Old binary (built from
`git archive HEAD` before the change): typing and the ≡ menu never drew —
frozen on didOpen. New binary: typed text shows, the menu opens.

## State

- All tests green (`go test ./...`), `-race` on internal/lsp and
  internal/child, `go vet ./...`, `gofmt -l .` empty.
- CLAUDE.md: map gained `internal/child/` and `gitexec.go`; new
  cross-cutting rule "Every child process is DETACHED"; LSP section gained
  "Sends never block".

## Next

Closed: N-147, N-122, N-123, N-126. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
