# Session: Esc-a — a leader namespace for the whole AI surface

Session ID: 32f5c26b-db23-4fcb-8166-09ade9167408
Date: 2026-07-31

Continues [2026-0731-1932-skills-support.md](2026-0731-1932-skills-support.md)
(same session). That doc ends with skills bound to a shifted `Esc S`;
this one is why that binding lasted about an hour.

### Ask

> "add a leader key for the skills picker"
> then: "What if we used Esc a as leader for all things AI?"

### Why the flat table had to give

Binding skills took the last mnemonic letter. `s` is Save, `k` is the
palette, `a` was the palette's "actions" alias — skills ended up on a
shifted `S`, justified in the commit by "the price of the only mnemonic
letter left". That's a table at capacity announcing itself.

Meanwhile the AI surface is **fifteen menu rows with one binding between
them**, and the chat panel — the most-reached-for of the lot — had no key
at all. A namespace was the structural answer, and the owner proposed it
one turn later.

### The decision that needed the owner

`Esc a` was the palette's alias, so the prefix couldn't be free. Three
options went up:

- **`Esc A`** (shifted) — nothing breaks, palette keeps both bindings.
- **`Esc a`** — best mnemonic, no shift, but months of palette muscle
  memory now arms a chord instead.
- Stay flat.

**Owner chose `Esc a`.** The palette keeps `Esc k` (the Cmd+K muscle
memory every editor teaches) plus its pinned ≡ headline row.

### What shipped

```
Esc a c   chat panel (focus, else toggle — the Esc-` gesture)
Esc a s   skills picker
Esc a a   attach current file / selection
Esc a f   attach file…
Esc a m   chat model
Esc a b   chat backend (agent)
Esc a t   tools — MCP servers
```

A `leaderBinding` carrying a `sub` table is a **prefix**: `fireLeader`
runs no action, stores the sub-table on `App.leaderChord`, stamps
`leaderChordAt`, and flashes the binding's `hint`. `handleChordKey` — the
first thing `handleKey` calls — resolves the next rune against it.

Four things worth remembering:

- **tmux came free.** Both leader entry paths (bare `Esc`+rune, and the
  folded `Alt`+rune tmux delivers because it buffers ESC for its
  escape-time) already funnel through `fireLeader`, so the prefix arms
  identically either way and the second rune arrives bare. Pinned by
  `TestLeaderChord_TmuxAltPath`.
- **A miss inside a live chord is SWALLOWED with a flash** — deliberately
  unlike the flat table's fall-through. A lone Esc can be a stray tap,
  which is exactly why the flat table stays harmless to mash; `Esc a` is
  two deliberate keys, so falling through would answer a mistyped chord
  by dropping a character into the user's code.
- **2s window (`leaderChordFor`), not `doubleEscMs`.** A chord is
  composed, not reflexive — 500ms isn't long enough to recall which
  letter the model picker is. Esc drops a pending chord (handleChordKey
  disarms on any non-rune and returns false, so normal Esc handling still
  runs and `Esc a Esc s` saves).
- **The flashed hint is the namespace's only keyboard discovery
  surface** — the flat table gets that from the ≡ hint column — so a test
  asserts every sub-binding appears in it.

Sub-bindings collide with the top-level table on purpose (`a`, `f`, `m`,
`t` all mean something else after a bare Esc). That's the point of a
namespace: the prefix already said which world you're in.

### Menu hints, kept honest

Seven ≡ rows gained accelerators (`esc a c/b/m/a/f`, `esc a t`,
`esc a s`) and the palette row dropped to `esc k`. The house rule is that
dispatch and the hint column must move together or the menu lies —
`TestLeaderSkills_OpensThePicker` now reads both, and the row is found
through `labelFor` rather than the static label (which is empty for
toggle rows).

### Verified in the real binary

Via the `run-ced` capture tool: the hint flash renders in the status bar,
`Esc a s` opens the skill picker, `Esc a c` opens the chat panel, and the
expanded ≡ Copilot group shows the new hint column.

### Tests

Six new in `leader_test.go` — arming without acting, the tmux Alt path,
the swallowed miss (plus the next keystroke typing normally), expiry,
Esc-cancel, and the palette keeping `Esc k`. Three existing tests pinned
the old `Esc a` → palette behavior and were rewritten, one of them
inverted to assert `Esc a` must NOT open the palette: the chord would be
unreachable otherwise.

The binding-table walk now also checks the namespace's invariants — a
prefix carries a sub-table and a hint but no action, and no sub-binding
nests another namespace or is marked repeatable.

### Not done (deliberate)

- **One namespace, one level.** The bar for a second is what justified
  this one: an action surface deep enough that the flat table can't
  mnemonically hold it. A chord is a real cost, paid by everyone who has
  to remember which letters are prefixes.
- **No which-key modal.** The status-line hint is the discovery surface;
  a popup would need the modal slot the pickers already own.
- **Auth, the toggles, clear-attachments, copy-transcript stay
  menu-only** — rare or destructive-ish enough that the ≡ row is the
  right speed.
