# Session: Phase 6 — the upstream asks, and their consumers, in one day

- Date: 2026-08-13
- Branch: `main` (both repos)
- Worked from the cats checkout (`~/projs/go/cats`); changes landed in
  **both** cats and ced (`~/projs/go/ced`).
- Plan: `ai_docs/cats-native-plan.md` — **Phase 6 ✅. Phases 1–6 are now
  closed.**
- Predecessor: `2026-0813-1744-cats-native-4.6-blame-and-phase2-marker.md`
- Commits: cats `ff8c89a` (pushed), ced `d5cd5be` (pushed)

## What was asked

Load the last session, then do Phase 6. Phase 6 is "each landed ask gets
its consumer", and at the start of the session **no ask had landed except
the ⌘ passthrough** — so the phase was two halves: build the cats side of
§5's items 1, 3, 7 and 8, then build ced's side of all four. Both are done.

## What landed

**cats (`ff8c89a`)** — 705 insertions:

```
internal/clipboard/           + test   NEW: pbpaste/wl-paste/xclip/xsel, the rune-safe cap
internal/ctlproto/proto.go    + test   MethodClipboardRead, ClipboardData, TransportMethods()
cmd/catway/clipboard.go       + test   NEW: the handler, answered before the dispatcher
cmd/catway/control.go                  the intercept (now a switch, two methods)
cmd/catway/settings.go        + test   theme_changed out of broadcastTheme, deduped
cmd/catway/catway.go                   lastTheme + the seed
internal/app/events.go                 EventThemeChanged, ThemeChangedEvent
internal/app/command_vocab.go + test   SplitParams' spawn trio, SplitResult, shared validators
internal/app/commands.go               the split case: validate, lock, stage, answer
cmd/catctl/*                           the `clipboard` verb (raw stdout), IsTransportMethod
cmd/catgen-dart/testdata/golden        regenerated
docs/protocols/control-api.md          all three, with the security argument
```

**ced (`d5cd5be`)**:

```
internal/cats/client.go       + test   ClipboardRead, SplitParams/SplitResult
internal/cats/events.go                EventThemeChanged + payload alias
internal/app/catsclip.go      + test   NEW: both clipboard verbs (11 tests)
internal/app/catssplit.go     + test   one round trip, legacy path as fallback
internal/app/catsrun.go                cwd rides the split now
internal/app/catstheme.go     + test   the poll retires once the host pushes
internal/app/cats_glue.go              subscribe, dispatch, catsKindClip, themeEvented
internal/app/app.go           + test   two menu rows, one leader key
ai_docs/cats-native-plan.md            §4, §5 ×4, Phase 6, §7, critical files
```

Both suites green (`make test-ghostty` in cats, `make test -race` in ced);
`go vet` clean in both; gofmt unchanged apart from the four pre-existing
offenders on the ced side.

## The three asks, and what decided each

### 1. `clipboard.read` — the permission stance is structure, not a flag

The plan asked for "socket exposure + a permission stance (a config flag
and/or local-session-only)". What it became: **not a §7 command at all.**
It is a control-TRANSPORT method (`ctlproto.MethodClipboardRead`), answered
in `controlDispatch` before `app.Dispatcher` sees the name — the exact
boundary `pair` is already drawn against, applied to the user's clipboard
instead of to credentials. The §7 table is shared with the network-facing
browser by construction, and the clipboard holds whatever they last copied
in ANY application.

**No config flag on top of that, deliberately.** A caller holding the
owner-only socket can already `pane.send_input` `pbpaste` into a shell pane
and `capture` the answer. A switch would gate nothing it does not already
have, and would make the honest path look more privileged than the
dishonest one.

Read-only, and off the terminal stream: OSC 52 is write-only by design,
because a terminal that answered clipboard reads would let anything that
can print bytes exfiltrate it.

### 2. `theme_changed` — emitted from the funnel, deduped on the resolved look

`broadcastTheme` is the one place `config.set` / `theme.save` /
`theme.delete` all reach, so the event is emitted there rather than from
three commands. The dedupe is the part worth keeping: that funnel runs
after **every** `config.set`, including one that only rebound a copy-mode
key, and a subscriber ACTS on an event where the browser's push is merely
idempotent restyling. Compared against the RESOLVED appearance, not the
config, because that is what a subscriber renders.

cats' first **session-scoped** event: it names no pane, so it goes out with
pane 0 and a pane-filtered subscription does not see it. That follows from
the filter's contract rather than working around it.

### 3 + 4. `pane.split` returns `{pane}` and takes an argv

Shipped together, which turned out to matter more than either alone (see
below). `{pane}` and not `{num, pane}`: a split happens inside the tab the
caller is already in. The spawn trio mirrors `TabCreateParams` field for
field and shares its validator and its workspace-lock rule — a `command` in
a locked workspace is refused, a bare split is still the user asking for a
shell.

## The ced side, and the two rules it added

### **Let the answer be the capability probe.**

`catsSpawnSibling` sends the argv unconditionally and reads the reply: a
zero pane means an old host, which means the argv was ignored too, because
both landed in one commit. One field answers both questions, with nothing
probed at startup and no version number anywhere. The legacy
list/split/list/type sequence stays as the branch it falls into.

The failure this avoids is the quiet one: a client that assumed the new
shape does not error — it leaves the user staring at a bare shell where
their editor should be.

`theme_changed` is the same rule where **no answer exists to read.** The
event vocabulary is not enumerable over the wire, so a host too old to send
one is indistinguishable from a host that has not changed theme yet. The
poll therefore stands down only once a frame has PROVED the host pushes
(`themeEvented`), and is set for any *decodable* frame — including one
whose palette ced declines, because the mark is a statement about the host,
not about the payload.

### **Name the place, not the concept.**

The finding that reshaped the clipboard work halfway through. ced's copy
goes out over OSC 52, which cats hands to the **browser's** clipboard;
`clipboard.read` reads the machine **catway runs on**. Those are the same
clipboard in the mac app and any local session, and two different machines
the moment someone drives a remote catway from a laptop.

The first design was an ambient refresh of `clipBuf` on `focus_changed`, so
that ⌘V would paste what you copied in Chrome. It would be delightful nine
times out of ten and, the tenth, would silently replace what the user
copied in their browser with whatever sits on a headless build server — a
clobber of state they cannot see, discovered only when they paste. It was
dropped whole. Nothing reads the host clipboard unless the user asked, in
that moment, for the host clipboard; ⌘V is untouched and stays synchronous.

The async paste's other rule: **snapshot the target at ASK time.** The tab
and its `EditRev` are recorded on the loop when the user picks the row, and
an answer arriving into a moved buffer declines — and hands the text to the
internal clipboard, which turns "gone" into "press ⌘V".

## Surfaces

- ≡ Compare → **"Compare with clipboard"** — one row for both tiers, because
  "compare with what I copied" is one question and a second row would make
  the user choose between two clipboards they cannot look at.
- ≡ Cats → **"Paste from host clipboard"**, `Esc C v`.
- `catctl clipboard` prints the text raw so it pipes.

## Verification

- cats: `make test-ghostty` green, `go vet` (both tag sets) clean.
- ced: `make test -race` green, `go vet` clean.
- **End to end against a live catway** on a scratch socket (built binaries,
  `/tmp/catsv6`, torn down after): the clipboard round-tripped a marker
  string; `pane.split` with `{"command":["/bin/sh","-c","echo MARKER; sleep"]}`
  answered `{"pane":3}` and `wait_for_output` saw the marker, proving the
  argv exec'd as the pane's process; `catctl events` carried exactly one
  `theme_changed` for a theme switch and **none** for a copy-mode-only save.
- **Not verified by machine:** any of the ced side in a live cats pane
  against the new catway. ced's half is exercised against a fake control
  socket that answers both host vintages.

## State / next steps

- **Phases 1–6 are closed.** What remains in the plan is not code:
  - the ⌘ chords hand-checked end to end in browser-cats (the running
    catway is still an older binary) and in the mac app, where the analysis
    says there are no menu collisions — now three days old;
  - §5 item 4 (`ui.notify`), deferred on purpose; item 6 (⌘click) retired
    as impossible; item 5 verified, not built.
- **Owed downstream: cats-mobile.** `pane.split`'s generated Dart method
  changed shape (`Future<void>` → `Future<SplitResult>`, plus the new params
  and `SplitResult` class). cats is pushed, so `tool/regen.sh` in
  `../cats-mobile` → `dart test` → commit the `CATS_REV` pin is the flow.
  Not run this session.
