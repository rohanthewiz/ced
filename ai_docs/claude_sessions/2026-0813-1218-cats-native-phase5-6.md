# Session: cats-native plan, Phase 5.6 + 5.8's last quarter — spawn, then watch

- Session id: `b7b4d230-fe2f-43c8-9c7a-5b341b8b4e33`
- Date: 2026-08-13
- Branch: `main`, feature commit `f2c725d` (+ this doc)
- Worked from the cats checkout (`~/projs/go/cats`) against the ced repo
  (`~/projs/go/ced`), which is where every change landed.
- Plan: `ai_docs/cats-native-plan.md` §Phase 5 — **5.6 and 5.8 now ✅**.
  Only **5.2 (the ⌘ table)** remains, and it is gated on the Mac app.
- Predecessor: `2026-0812-2312-cats-native-phase5-2.md`

## What was asked

Load the last session, continue with any remaining items of the plan.

## What landed

One feature commit. Two new files + their tests (~1,100 LOC incl. tests),
two new client verbs, small edits to four existing files. `make test`
(race) green; `go vet` clean; `gofmt` clean apart from the four
pre-existing offenders (`hostident_test.go`, `reconcile.go`, `tabbar.go`,
`editor/syntax.go`).

```
internal/cats/client.go       + test   Capture, WaitForOutput
internal/app/catsrun.go       + test   terminal pane + run file  (5.6)
internal/app/catscapture.go   + test   compare with a pane       (5.8)
internal/app/catssplit.go            catsSpawnSibling now RETURNS its pane
internal/app/cats_glue.go            two event kinds, run state, 3 new rows
```

### The second shape: spawn, then watch

Phase 5's earlier consumers all had one shape (poll → cache → read the
cache from the loop). The pane verbs needed a different one, worth
naming because it is what every future "make cats do something" will use:

**a control call that CREATES something answers before the thing inside
it is ready, and nothing in the API reports a child process's exit.**
`pane.split` returns as soon as the pane exists — its shell is a process
that still has to start (hence `catsRunScrollGuard`, a 250ms settle that
is a package var so tests can zero it) — and no verb will ever say "the
command you typed exited 2". So a pane is driven by typing a
self-describing command at it and waiting for the marker it prints.

### (a) 5.6 — the marker is the whole design

```
sh -c 'cd <root> && ( <cmd> ); printf "\n[ced run 41.3] exit:%s\n" "$?"'
pane.wait_for_output  regex \[ced run 41\.3\] exit:[0-9]+
```

Three details, each of which is a way this would otherwise be quietly
wrong rather than obviously broken:

- **The echo must not match.** `wait_for_output` is seeded with what is
  already on the pane's screen, and a shell ECHOES the command it was
  given. A naive marker satisfies its own wait the instant it is typed —
  reporting "finished, exit 0" for every command, always. The format
  string carries `%s` where the pattern needs `[0-9]+`, so only the
  printed result can match. That is `TestCatsRunMarkerCannotMatchItsOwnEcho`
  and it is the test of the file.
- **The marker is pid+sequence unique**, so a marker left in the
  scrollback by an earlier run cannot satisfy this one either.
- **The command runs in a SUBSHELL.** Found by running the script
  through a real `/bin/sh`: `exit 3` as the command exits the reporting
  shell before the printf, so the run looks like it never finished
  rather than like it failed. `( … )` fixes it, and the real-shell test
  now pins `exit 3`, a not-found `127`, and a failed `cd` taking its
  command with it.

`sh -c`, not the user's login shell: `$?` is not fish's spelling of the
exit status, and the exit-code protocol must not depend on the shell
somebody happens to like. The outer quoting goes through
`catsShellQuote`, so the test drives BOTH layers.

**The hook is the point of the wait.** While a run is in flight ced
reports `working` for its own pane; cats turns the working→idle edge into
its own "finished" notification (`notifyKind` in cmd/catway/notify.go —
verified by reading it, not assumed), which is the toast, badge, or phone
push for the run the user walked away from. The exit code itself goes to
ced's status bar, because the hook's vocabulary is state + a 32-char
custom status and the notification body is composed cats-side as
`agent + " finished"`.

**A failed run is NOT `blocked`.** Blocked means the editor is asking a
question the user did not ask for (the rule cats_glue.go set in 5.3); a
build that failed is an answer. A channel that pages for both is a
channel that gets muted.

The run pane is remembered and reused — twelve runs must not end in
twelve panes — but only when it is still listed AND has acquired no
agent since. Typing a build command at a coding agent is the mistake the
self-check in catsagents.go exists to prevent, arriving by another door.

**Tier 0 is not a refusal.** "Run file" outside Tier 1 opens ced's own
grsh panel with the command typed in and NOT submitted (the same
division of labour as staging a selection in an agent's prompt);
"Terminal in a cats pane" opens that panel too, and says which terminal
it is giving you.

### (b) 5.8's last quarter — capture into the compare panel

`capture` (scope recent, 2000 lines, unwrapped, no ansi) becomes the OLD
side of the existing compare panel, so an agent's proposal is diffed
against the buffer without leaving the editor. compare.go's house rule —
the active buffer is always "new" — is what makes that the right way
round, and it comes with `diffTargetLine`'s double-click-to-jump for
free.

Only the capture's TRAILING blank rows are trimmed. Trimming its
interior blanks would report changes the user never made.

Agent panes are the offer when there are any (that is the question this
verb exists for); every other pane when there are none (a pane running a
test suite or a `git show` is just as comparable); our own in neither,
because ced reports itself to cats as the agent "ced".

### (c) The client's two new verbs

`WaitForOutput` dials through a **copy** of the Client with a wider
timeout. A Client is shared across goroutines by design, so widening the
shared one would leave every unrelated call waiting minutes on a dead
socket — pinned by a test. `matched=false` is an ANSWER, not an error:
"that never appeared" and "the host refused to look" are reported
differently, so they must not arrive looking the same.

`catsSpawnSibling` now returns the pane it created, and an empty command
line means "just make me a pane" (an unidentifiable pane stops being a
failure when nothing was going to be typed into it).

## Surfaces added

- ≡ **Cats** group: "Terminal in a cats pane", "Run file in a cats
  pane…", "Compare buffer with agent pane…" — 9 rows now.
- `Esc C` leader: `t` terminal, `x` run, `m` compare.
- The first two rows stay ENABLED below Tier 1, because there they open
  ced's own terminal. A dimmed row would hide a working feature behind
  an unexplained gap.

## Live verification

Read-only, against the real catway this session lives in (`wF:p26`,
self = pane 289), via a throwaway `zz_live_test.go`, deleted after.
Nothing was split and nothing was typed into any pane:

```
capture(289, recent, 200, unwrap) → 9757 bytes, 199 lines
wait "⏵⏵" (already on screen)     → matched=true in 1ms, with its line
wait "zzz-no-such-marker-wF:p26"  → matched=false in 2.001s, err=nil
```

The half that cannot be verified read-only — split, type, watch — is
covered instead by the real-`/bin/sh` test of the exit-code protocol plus
the socket-level assertions on what ced sends.

## State / next steps

- **Phase 5 has only 5.2 (the ⌘ table) left**, and it is gated on Mac-app
  routing (§5 item 2). Nothing else in the phase is unclaimed.
- Untouched follow-ups from this work: the run's 250ms settle is a guess
  and there is no "shell is ready" signal to replace it; a run cannot be
  cancelled from ced (the pane is right there, and Ctrl-C in it works);
  one run at a time, because the pane is reused; the guess table is
  deliberately short and does not look at build files beyond their names;
  the capture is not scoped to a code block inside the transcript.
- Upstream asks unchanged, both still open and both small: **pane.split
  should return its new pane** (§5 item 7 — 5.6 pays the diff cost a
  second time), and **pane.split should take an argv** (item 8). A third
  is now worth considering: nothing in the API reports a pane child's
  EXIT, which is why runs print their own marker.
- Unchanged from before: 4.6 (blame layer) unclaimed and optional;
  Phase-2's `⚠` tab marker should re-raise a deferred conflict prompt;
  3.3 (hover on dwell) and 3.4 (recent-files picker) small and unclaimed.
