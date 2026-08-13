# Session: cats-native plan, Phase 5.1 + 5.3 — the editor learns to speak to its host

- Session id: `59fed02d-f953-47f6-9d28-a54687740853`
- Date: 2026-08-12
- Branch: `main`, feature commit `459271f` (+ this doc)
- Worked from the cats checkout (`~/projs/go/cats`) against the ced repo
  (`~/projs/go/ced`), which is where every change landed.
- Plan: `ai_docs/cats-native-plan.md` §Phase 5 — **5.1 and 5.3 now ✅**,
  and §5's item 5 (an upstream *verify*, not build) is answered.
- Predecessor: `2026-0812-2204-cats-native-phase4-5.md`

## What was asked

Load the last session, continue with Phase 5.

## What was already done, and what that left

§7 said to pull 5.4's OSC 7 / title emission to week one. It turned out to
be **already landed** — `internal/app/hostident.go`, commit `d2bf955`. So
Phase 5 genuinely started at **5.1** (the `internal/cats` package +
`cats_glue.go`), and 5.3 (the hook reporter) came with it, because
`hooks.go` is part of 5.1's specified package and a package with no
consumer is dead weight.

## What landed

One feature commit. Five new files (~1,050 LOC incl. tests) plus small
edits to six existing ones. `make test` (race) green; `go vet` clean;
`gofmt` clean apart from the same four pre-existing offenders
(`hostident_test.go`, `reconcile.go`, `tabbar.go`, `internal/editor/syntax.go`).

```
internal/cats/detect.go   + test   env sniff + control-socket ping → Caps
internal/cats/client.go   + test   one call per connection, typed §7 verbs
internal/cats/events.go   + test   events.subscribe, reconnects forever
internal/cats/hooks.go    + test   idle/working/blocked → badge/toast/push
internal/app/cats_glue.go + test   tier, reporter policy, sibling notifications
```

### (a) Detection is two halves because one of them is IO

`DetectEnv()` reads four env vars — free, so `New()` calls it inline.
`Caps.Probe()` dials and pings — milliseconds when the socket is live or
absent, but a *wedged* listener could hold it, so it runs on a goroutine
and posts one `catsEvent` back. A host that is hung must never cost the
editor its first frame.

The probe insists on a **reply**, not a dial. A socket file proves nothing
(a crashed server leaves one behind) and a successful dial only proves
something is listening. The service NAME is recorded but never matched
against a constant — catway answers `"catway"` and the Rust cats answers
its own; a client that rejected an unfamiliar one would break on the
sibling implementation it exists to work with.

### (b) "Blocked" is not "a modal is open"

This is the whole judgment call of 5.3. The user who just pressed Rename
knows there is a prompt on screen; paging them about it is the noise that
gets a notification channel muted for good. Blocked means the editor
raised a question **the user did not ask for**, and exactly four sites say
so by calling `catsAsking(phrase)`:

| site | phrase |
|---|---|
| `openConflictModal` (reconcile.go) | `file changed on disk` |
| `chatMaybeOpenPermission` | `<agent> needs an answer` |
| `openFormatTrustPrompt` | `formatter trust` |
| `openGitConflictPicker` | `cherry-pick stopped on conflicts` |

The phrase is what shows up on a phone, so it is written as one. Marked
**after** `openModal` (which clears the mark on its way in), and cleared
in `catsAfterEvent` whenever the modal slot is empty — so no site has to
remember the other half, and a stale "blocked" (which would train the user
to ignore the real one) cannot get stuck on.

Deliberately NOT marked: the formatter **install** prompt sitting twenty
lines below the trust one. It is an offer, and declining it costs nothing;
a push notification for it would be advertising.

`catsAfterEvent` runs **last** in the dispatch tail — after
`conflictAfterEvent` and `chatPermAfterEvent`, which can raise the very
question that blocks us. Taken earlier it would report idle in the same
pass that blocked the editor.

### (c) Two protocol traps, both caught before production

**The hook seq must be seeded from the clock.** The server keeps a
per-source high-water mark and drops anything at or below it — and that
mark lives on the PANE, so it outlives the ced process. A counter starting
at 1 means a restarted ced has *every* report dropped as stale, and a
folder switch alone rebuilds the App. Every shipped cats hook asset uses
`time.time_ns()` for precisely this reason (each hook invocation is a
fresh process); `NewReporter` now does the same. The live test proved it
by accident and then on purpose: the claim and the release below ran as
two separate processes, and under a plain counter the release would have
been dropped and the pane left badged.

**One `json.Decoder` must span the subscribe ack and the events.** A
second decoder for the pump strands anything the server wrote in the same
breath as the ack — invisibly, forever. Caught while writing the file (the
comment said one thing and the code did another); `TestStreamEventBundledWithAck`
pins it.

### (d) The stream reconnects forever, and that is not paranoia

A unary call that fails is a feature that didn't happen. A subscription
that stays dead is a feature that **stopped working and never said so**.
Capped backoff (500ms → 30s), indefinitely, until Close. No jitter — one
subscriber per editor, no herd, and a deterministic sequence is one a test
can assert on. `Close` must interrupt a blocked read, which is why the
live connection is held under a mutex, and it waits for the goroutine so a
closed Stream can never deliver a callback onto a screen being finalized.

The subscription is filtered server-side to `pane_notify` alone — the one
event ced acts on today. An idle editor is not woken by every title change
in the session.

### (e) The reciprocal: the host can page the editor too

`pane_notify` from a *sibling* pane reaches ced's status bar ("claude
needs attention"). Events naming our own pane are dropped — without the
self-check, ced blocking on a prompt would notify ced about ced, which is
also why `catsInit` resolves this pane's internal id (`ResolvePane`) while
it is already off the main loop.

## Conventions / gotchas worth remembering

- **Never import the cats module.** The wire structs are hand-copied
  minimal mirrors. ced must stay buildable on a machine that has never
  heard of cats.
- **Source is `"ced"`, never `"cats:ced"`.** The `cats:` prefix marks a
  reserved native source, whose state is detection-driven and whose hook
  state reports are deliberately downgraded.
- **The hook socket is a different address from the control socket**
  (`CATS_SOCKET_PATH` vs `CATS_CONTROL_SOCKET`), so the reporter is armed
  independently of Tier 1 — the attention story is the half that matters
  when you are not looking at the screen.
- **Unix socket paths are capped at ~104 bytes**, and macOS `t.TempDir()`
  eats most of it. Every socket test in this package builds its own short
  base under `/tmp`, same as `internal/remote`'s tests.
- **Custom status is capped at 32 BYTES**, because that is the unit cats
  truncates in — but the cut backs off to a rune boundary, which cats does
  not do. Doing it correctly here is what stops the server's own
  truncation from ever firing and emitting invalid UTF-8 into its UI.
- `reconnectMin`/`reconnectMax` are package vars for the one reason
  `SyntaxSettle` is: a test collapses the window instead of sleeping.
  They are not settings.
- **No new ≡ rows this session** — deliberately, since the menu row counts
  are pinned in `app_test.go` and every Phase-5 feature after this one
  brings rows of its own. The visible surface for now is the pane badge
  itself.

## Live verification

This session was itself running inside a cats pane (`wF:p26`, catway
35827), so the whole client was exercised against the real server via a
throwaway `zz_live_test.go` (deleted after):

```
InCats=true Tier1=true Hooks=true handle="wF:p26" service="catway" proto=1
ResolvePane("wF:p26") = 289          (matches catctl panes)
PaneList: 16 panes; agent pane 289: claude / working
ConfigGet: theme="cats-green" colors=33      ← 5.4's data source, confirmed
PathList: recents=34                          ← 5.7's data source, confirmed
link up=true
```

Then the hook round trip, against a plain shell pane (`wF:pN`) rather than
this agent's own, so nothing stole a badge from a live agent:

```
$ … CED_CATS_LIVE_HOOK=claim
$ catctl panes → { "pane": 129, "handle": "wF:pN", "agent": "ced",
                   "agent_state": "blocked", … }
$ … CED_CATS_LIVE_HOOK=release
$ catctl panes → { "pane": 129, "handle": "wF:pN", … }   (clean)
```

That settles the roadmap's §5 item 5: **a custom hook source does get real
state authority for its pane**, and `pane.release_agent` hands it back.
Toast/push policy beyond the sidebar was not exercised — it is cats-side
configuration, not something ced can assert.

## State / next steps

- Phases 1, 2, 3.1, 3.2, 4.1–4.5 and now **5.1, 5.3, and 5.4's OSC half**
  are done. §5's item 5 is verified.
- **The foundation is in place, so every remaining Phase-5 item is a
  consumer rather than new plumbing.** Suggested order: **5.4 theme unity**
  (`config.get` verified; wants a `focus_changed` subscription the stream
  can carry today) → **5.5 splits** (near-zero: `pane.split` + spawn a
  sibling ced) → **5.7 frecency** (`path.list` returns 34 recents here;
  its natural home is 3.4's recent-files picker) → **5.8 agents as
  collaborators** (`pane.list` already reports sibling agent/state, and
  `pane_notify` is already subscribed). **5.2, the ⌘ table**, is
  independent of all of it.
- Untouched follow-ups for this work: long ops that are still invisible to
  the reporter (workspace rename, format-on-save, git pushes) — only a
  chat turn and a project search set `working` today; no ≡ surface for the
  tier or the host identity; `Release`'s synchronous-ness is by
  construction, not pinned by a test (a test process does not exit the way
  the editor does).
- Unclaimed and optional: 4.6 (blame layer via
  `internal/editor/decoration.go`).
- Still on the table from Phase 2: clicking a tab's `⚠` marker should
  re-raise a deferred conflict prompt.
- Phase-3 leftovers: 3.3 (hover on dwell) can now be built on this client;
  3.4 (recent-files picker) is an hour's work.
- Untouched from 4.4: a review mark does not clear when the file changes
  after being read; stepping BACK lands at the top of the previous file's
  diff; reverting a staged hunk on a mixed file is withheld with no
  on-screen explanation; the hunk chips have no keyboard-only equivalent
  beyond the `h` picker.
- Untouched from 4.5: no keyboard route to the `[trailer: on]` chip, and
  it is dropped below ~65 columns.
- Untouched from 4.3: no `--skip` row on a stopped cherry-pick;
  conflicted files are unmarked in the tree and tab bar.
