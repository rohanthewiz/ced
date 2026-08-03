# Session: remote open ($EDITOR handoff) and the undo byte cap

Session ID: `07421e84-052a-435e-9e73-07bba58aea14`
Date: 2026-08-03
Branch: `main`
Parent commit: `6c3125b`

---

## How this started

Stages 11 and 12 of [`ai_docs/opus-improvements-analysis.md`](../opus-improvements-analysis.md),
asked for together — the last two entries in the work plan:

| # | Stage | Items |
|---|---|---|
| 11 | `--wait` / `--remote` | `$EDITOR` integration, single-instance open |
| 12 | Undo memory cap | byte-budget the snapshot stack |

They share nothing but a commit. Stage 12 is one file and a cost model;
stage 11 is a new package, a new listening socket, and a CLI surface.

---

## Stage 12 — the undo byte cap

`internal/editor/undo.go`, `internal/editor/tab.go`

### The entry count was the wrong unit

`maxUndoEntries = 500` bounded the stack by *steps*, which says nothing
about memory. A snapshot of a 25k-line file costs 400KB in slice headers
alone — 500 of them is ~200MB **per tab**, paid again for every large
file open. On a 400-line source file the same 500 entries are under 4MB.

So the entry cap stays as a backstop and `maxUndoBytes` (32MB, shared by
the undo AND redo stacks because entries move between them) becomes the
limit that actually binds on the files where it matters.

### What the cost model counts, and what it refuses to

This was the whole design decision. Two terms, and dropping either one
breaks a real case:

- **The `[]string` header array** (`len * 16`). Unshared by construction
  — `captureSnapshot` allocates a fresh slice every time — so it is real,
  per-entry, and dominant on a large file. It is the 200MB above.
- **Only the lines that DIFFER from the entry below.** Copying a
  `[]string` copies headers, not characters. Every untouched line is the
  same string the live buffer and the neighbouring snapshots already
  hold, so charging each entry for whole-file content over-counts by the
  *depth of the stack*: a 1MB file's 500-step history would measure as
  500MB and get amputated for no reason.

Counting headers alone was tempting and is wrong: editing a file of very
long lines (minified JS, a data blob) strands a multi-megabyte string per
step that nothing else references, and the header term is 16 bytes.

Each entry is measured against **the snapshot it will sit on**
(`undoTop`, or the popped entry in `Undo` — popping first is what makes
it available). Those two are always exactly one edit apart, which is the
comparison the estimate is built for. The cost is stamped once and
stored, so eviction is O(1); re-measuring the stack bottom on every push
would make a capped stack quadratic in the file's line count.

### Two invariants worth keeping

- **Trimming never empties the stack.** A single step big enough to blow
  the whole budget (a multi-megabyte paste) still has to be undoable, and
  a history of zero is indistinguishable from undo being broken.
- **`pushUndoEntry` is the single write path** for the undo stack, and
  the running sums are maintained on *every* path — push, `Undo`,
  `Redo`, redo invalidation, `initUndo`. A sum that drifts upward
  silently shrinks the history until the budget falsely binds, which is
  the kind of bug nobody reports because it looks like a preference.

---

## Stage 11 — `ced --remote` / `ced --wait`

`internal/remote/remote.go`, `internal/app/remote.go`, `main.go`

### Why it exists

The editor's premise is one instance per project inside tmux. Without a
handoff, `EDITOR=ced` in another pane starts a **second full-screen TUI**
— and if it lands inside the first instance's terminal panel, that panel
is a REPL strip, not a PTY, so it can't host it at all. With it,
`git commit` in pane 2 opens its message as a tab in pane 1 and blocks
until you close that tab.

### Discovery is by PROJECT ROOT, not "the" instance

Everything in ced is rooted: the tree, the finder index, gopls's
`rootUri`, the terminal's cwd. A file delivered to an instance rooted
elsewhere lands in a workspace where none of that applies.

So a client probes every socket in the runtime directory, asks each one
which root it serves, and picks the instance whose root **contains** the
file — longest root wins, so a repo opened alongside a subdirectory of
itself resolves to the inner one. `contains` requires a separator at the
boundary, or `/a/proj` claims files in `/a/project-notes`.

The consequence is that `kubectl edit`'s `/tmp/…` scratch file belongs to
nobody and correctly gets its own local editor. That is the feature
working, not a gap.

### ErrNoInstance is a fallback; a refusal is not

Both flags start a normal local editor when nothing is listening — a
`$EDITOR` that errors out is worse than one that opens the wrong window,
and a bare `--wait` then blocks the way any terminal editor does, so git
behaves identically either way.

A handler **refusal** is the opposite and must never collapse into it:
that returns a real error, because falling back there would silently
start a second editor on a file the first one already declined. There is
a test asserting the two don't read alike.

### Sockets are named per PROCESS, not per root

`<root-hash>-<pid>.sock`. A deterministic per-root name forces every
second instance on one project to decide whether to take the socket
over, and the honest answer is that it can't — the first one is still
running and still wants it. Per-process names let both listen; the client
unlinks the sockets nobody answers, so a crashed instance needs no
reaper.

They live in `$XDG_RUNTIME_DIR/ced` (else a per-uid folder in the system
temp dir), mode 0700 — **not** `~/.config/ced`, because a socket is
runtime state that must not survive a reboot and has no business in a
directory the user hand-edits.

The names stay short because the kernel caps a unix socket path at ~104
bytes. That constraint reached the tests: on macOS `t.TempDir()` alone
burns most of the budget on `$TMPDIR` plus the test's own name, so the
transport tests use a short scratch base and say why in a comment. It is
a hard requirement of the transport, not a preference.

### The main-loop contract

`serveRemoteOpen` runs on the connection's own goroutine, posts an event
carrying a buffered reply channel, and blocks for the main loop's
answer — the ACP permission-request shape (`copilot_chat_perm.go`), for
the same reason: the handler has to block on a decision the loop makes.

**Every waiter is released exactly once.** `releaseRemote` is the single
write path and deletes the key as it closes, so a double release can't
panic. Exactly three things release one:

- the tab closing — **not** saving. `$EDITOR` callers expect the editor
  to be *finished*, not merely to have written once;
- `App.Close`, which runs on a quit **and** on a folder switch;
- the ≡ toggle going off.

A `--wait` client left hanging is a shell prompt in another pane that
never comes back, which is why the server also unblocks on its own
`Close` — the editor exiting means the same thing as the file closing.

### The root guard is re-checked on arrival

A client already refuses a mismatched instance, so `handleRemoteOpen`'s
check only ever fires for a request that didn't come from ced's own CLI.
The answer is the chat filesystem's answer: an error the caller can read,
never a file opened outside the workspace.

### Three states, not two

`"remote"` is the persisted key (default on) and the ≡ **File** row —
with the workspace rows, since what it scopes is which files this *root*
accepts. Its label has three states: `on` / `off` / **`unavailable`**.

A socket that won't bind costs the handoff, not the editor, and the
reason is *held on the state* for the label rather than flashed (a
startup flash scrolls past before anyone looks). Collapsing "off" and
"unavailable" would leave a user toggling a preference that was already
on. No leader key — a once-a-day action, and the flat table is out of
mnemonic letters.

### CLI

`--remote` / `--wait` **compose** rather than being two modes, and are
peeled off in a loop so order doesn't matter and repeats are harmless
(`core.editor` values get copied around and hand-edited). `-r`, `-w`, and
nvim's `--remote-wait` spelling all work. Both refuse a directory and a
missing argument: neither has anything to hand over or wait on.

---

## Verification

`make test` (race detector) green. 33 new tests — 13 in
`internal/remote` (real sockets, real clients, both legs of the wait
contract, stale-socket cleanup, the separator boundary), 12 in
`internal/app` (delivery, the root guard, the waiter registry, idempotent
release, the goroutine hop, the three labels), 3 in `main`, 1 in
`userconfig`, plus 7 in `internal/editor` for the cost model.

Real-binary checks through the `run-ced` PTY capture skill, with a second
ced driven from the shell against it:

- the running instance bound `…/ced-<uid>/da5810c0-<pid>.sock` at startup
- `ced --remote <file>` returned immediately; the editor's screen showed
  the new tab and the status bar read **“Opened COMMIT_EDITMSG (remote)”**
- `ced --wait <file>` was still blocked 3s later, and returned the moment
  the scripted `Esc w` closed the tab
- with nothing listening, `--wait` fell through to a local editor (it
  failed only on `/dev/tty`, which is the PTY-less shell, not the feature)
- the palette shows **“Remote open: on”**

Menu geometry pins updated for the new row — 117 rows / height 123 /
dividers `[2, 5, 120]` — in the tests and in CLAUDE.md.

`gofmt -l` reports `internal/app/app.go`, `internal/app/tabbar.go` and
`internal/editor/syntax.go`. Confirmed by stashing that all three predate
this work; left alone.

---

## What's queued

The work plan is now complete through stage 12. What remains from the
analysis document is stage 9's tail:

| # | Stage |
|---|---|
| 9 | LSP verbs — **references, rename, code actions, signature help** still open |

The note from last session still stands and is the reason rename is last:
it returns a `WorkspaceEdit` — edits to files the user never opened — and
ced has no primitive for that. Every write path today is one buffer at a
time, and a rename has to be ONE undo step across all of them. That is a
buffer-layer feature with an LSP verb on top. References can reuse the
Find-all panel wholesale (it already carries paths).

Two notes for whoever picks up something near this change:

- **`internal/remote` is the only place ced listens on anything.** If a
  second reason to accept a connection ever appears, it goes through the
  same socket and the same `Handler` shape — not a second listener. The
  per-process naming and the client's unlink-what-doesn't-answer sweep
  are what keep the runtime directory self-cleaning, and a second file
  naming scheme in there would break that.
- **`snapshotCost` is an estimate, deliberately.** It is not trying to
  measure the heap; it is trying to be monotone in the right direction
  and cheap enough to run on every push. Anything that makes it more
  accurate at the cost of a full pass over the buffer has traded away the
  reason it can run at all.
