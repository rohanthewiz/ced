# Session: cats-native plan, Phase 3.4 — the ring whose head is the screen

- Session id: `8b0709a8-049e-4e95-8fe1-c248da7c396e`
- Date: 2026-08-13
- Branch: `main`
- Worked from the cats checkout (`~/projs/go/cats`) against the ced repo
  (`~/projs/go/ced`), which is where every change landed.
- Plan: `ai_docs/cats-native-plan.md` §Phase 3 — **3.4 now ✅, so Phase 3
  has only 3.3 left.**
- Predecessor: `2026-0813-1254-cats-native-phase5-2.md`

## What was asked

Load the last session, see what remains in the cats-ced integration plan,
then do 3.4 — the recent-files picker, which was the only thing standing
between ⌘E and its row in `metakeys.go`.

## What landed

```
internal/session/session.go     + test   Entry.Recent, TouchRecent, MaxRecentFiles
internal/app/recentfiles.go     + test   the ring, the picker, the label   (new)
internal/app/app.go                      App.recentFiles, 2 openFile touches, ≡ row
internal/app/folder.go                   seed at load, capture at record, restore touch
internal/app/tabbar.go                   the switchToTab touch
internal/app/leader.go                   Esc B
internal/app/metakeys.go        + test   ⌘E bound; the file's cats note un-staled
ai_docs/cats-native-plan.md              3.4 done + notes; §7 and §5 item 2 updated
```

`make test` (race) green; `go vet` clean; `gofmt` clean apart from the four
pre-existing offenders (`hostident_test.go`, `reconcile.go`, `tabbar.go`,
`editor/syntax.go` — tabbar's is the ASCII diagram in its header, which
gofmt wants to re-indent into nonsense).

## The design, and the one decision that would have been invisible

### (a) The ring persists per folder — it is not derived from the tabs

`session.Entry` gains `Recent []string`, stored beside that folder's tab
list in state.json, seeded at load and written back at Close. The spec
said "MRU ring over `internal/session` data", which could have been read
as "build it from `Entry.Tabs`" — and that reading produces a picker with
no reason to exist. `Esc b` already lists the OPEN files. The rows worth
having are the ones the switcher cannot show: the file closed twenty
minutes ago, and the file you were in before this root was reopened. A
ring derived from open tabs has none of them.

It is per folder for the same reason the tab list is: "the last file I had
open" is a question about a PROJECT, and a global list answers it with
somebody else's project.

Not gated on the session-restore preference. That toggle governs whether a
folder reopens its tabs; a user who wants a blank editor has not asked the
editor to forget where they have been — the line folder-recency already
drew.

### (b) The order is the feature

The head of the ring is the file ON SCREEN, so the picker's first row is
the file before it: ⌘E, Enter is a flip back to where you just were, which
is what the chord means to a hand trained in VS Code or GoLand. Everything
else in the file is bookkeeping in service of that one row.

That is why the touch lives in `switchToTab` — the single funnel every
tab-switching surface already goes through (click, `Esc .`, the switcher)
— rather than in each surface. Same argument the nav history already made
there, one comment further down.

### (c) `closeTab` deliberately does NOT touch it

The failure this avoids only appears on the NEXT launch, which is why it
is a test and not a comment.

Closing a tab makes a neighbour active without anyone having navigated to
it, and quitting closes every tab in turn. A `closeTab` hook would
therefore rewrite the ring in reverse close order at the exact moment
`recordSession` writes it to disk — so the list restored on the next start
would be one nobody visited, in an order nobody chose. The picker itself
stays correct without the hook because it excludes the current file
explicitly rather than trusting the head.

`TestRecentFiles_ClosingTabsDoesNotReorderTheRing` closes every tab and
asserts the ring is byte-for-byte what it was.

## Surfaces

- `Esc B` — the shifted twin of `Esc b`: b lists the files that are OPEN,
  B lists the ones you have BEEN in. Follows the f/F and p/P convention
  where the capital is the same verb at a wider scope.
- ≡ → **Tab** → "Recent files…", directly under "Switch tab…" — same
  question, wider set. In the Tab group rather than File because File is
  far enough down the menu to be below the fold.
- **⌘E** at Tier 1 / kitty hosts (`metakeys.go`), which is what 3.4 was
  blocking. `TestMetaAccelsAreNeverTheOnlyPath` is satisfied by the two
  rows above — the ⌘ layer may never be an action's only door.

Rows render through the tab switcher's own `tabPickerLabel`, so a file
that is open looks identical in both lists (two pickers disagreeing about
how a file looks would read as two different lists of files). A file
OUTSIDE the root gets its directory spelled out — the switcher can afford
to leave that blank for a file you deliberately opened, but this list is
walked rather than read, and the entries arriving from outside the project
(a go-to-definition jump into a dependency) are exactly the ones whose
basename is `client.go` for the fourth time.

Deleted files are pruned when the picker opens — up to 50 stats on a
keystroke, in memory only, with the write riding the session at Close. The
≡ predicate deliberately does NOT stat: menu predicates run on every draw
of an open menu, and fifty stats per frame to grey out one row is not a
trade worth making. The cost is a row that can be enabled when every entry
has since been deleted, in which case the picker flashes — which is where
that sentence belongs anyway.

## Two deviations from the spec, both deliberate

- **`openPicker`, not `openPickerWithCancel`.** The latter exists for
  callers that must hear about a dismissal (the chat permission prompt has
  an agent blocked on the answer), and it is literally the former with a
  nil cancel. Nothing here needs the callback.
- **⌘E's cats half.** The chord is NOT on cats' `CMD_TO_PANE` allowlist,
  which predates it, so in browser-cats the browser still keeps ⌘E and the
  row is live in kitty/Ghostty/WezTerm only. Same armed-but-host-gated
  state every chord in that table started in — and per 5.2's third shape,
  a feature's status has to name the host's half.

## Verification

- `make test` (race) green, `go vet` clean.
- New tests: 10 in `recentfiles_test.go` (the two-file flip and that
  picking it lands there; switch reorders; closed files kept and
  reopenable; close does not reorder; vanished files pruned; a lone file
  offers nothing; the session round trip AND that the seed is a copy, not
  an alias of the store's slice; a restored session keeps its remembered
  order rather than tab order; outside-root labels; untitled buffers
  ignored), 4 in `session_test.go` (move-not-append, tail cap, the
  state-file round trip collapsing hand-edited duplicates, Touch keeping
  the ring), 1 in `metakeys_test.go` (⌘E reaches THIS picker and not one
  of the editor's several others).
- One unrelated test needed its constants bumped: `TestMenuLayout_*` pins
  the ≡ menu's exact geometry, so the new row moved it 138→139 rows. That
  is the test working as designed.

## State / next steps

- **Phase 3 has only 3.3 left** (hover on mouse dwell, Tier-1 only). 4.6
  (blame) stays optional and unclaimed; Phase 2's `⚠` tab marker should
  still re-raise a deferred conflict prompt. Phases 1, 2, 4 and 5 closed.
- **Phase 6's critical path is now §5 item 1, `clipboard.read`** — the one
  remaining upstream ask with a whole feature behind it (§4 Tier 1 plus
  native paste anywhere). The plan's §7 still called item 2 the critical
  path after it shipped yesterday; corrected.
- **New one-line cats ask:** add `KeyE` to `CMD_TO_PANE`. No browser
  claims ⌘E, so it is the same one-entry change `KeyS` was. Recorded in
  the plan's §5 item 2.
- Still owed from yesterday, neither of them code: the ⌘ chords end to end
  in a browser (the running catway is the old binary), and the mac app,
  which analysis says is free of collisions.
- Fourth shape, named in the plan for whoever builds the next list: **an
  MRU list is only useful if its head is what is on screen** — so the
  touch belongs at the one funnel, and the events that look like visits
  but are not (a close making a neighbour active, a quit closing every tab
  in turn) must not touch it at all.
