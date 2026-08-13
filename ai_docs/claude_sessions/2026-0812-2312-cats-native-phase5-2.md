# Session: cats-native plan, Phase 5.4 / 5.5 / 5.7 / 5.8 — four consumers of one client

- Session id: `bae506a2-92b7-442e-b7e1-c70cb768a1e4`
- Date: 2026-08-12
- Branch: `main`, feature commit `282fe51` (+ this doc)
- Worked from the cats checkout (`~/projs/go/cats`) against the ced repo
  (`~/projs/go/ced`), which is where every change landed.
- Plan: `ai_docs/cats-native-plan.md` §Phase 5 — **5.4, 5.5, 5.7 now ✅**,
  **5.8 three-quarters ✅**. Two new upstream asks written into §5.
- Predecessor: `2026-0812-2241-cats-native-phase5-1.md`

## What was asked

Load the last session, continue with any remaining Phase-5-or-lower items.

## What landed

One feature commit. Four new files + their tests (~1,900 LOC incl. tests),
small edits to eight existing ones. `make test` (race) green; `go vet`
clean; `gofmt` clean apart from the same three pre-existing offenders
(`hostident_test.go`, `reconcile.go`, `tabbar.go`).

```
internal/app/catstheme.go    + test   config.get palette → a real ced theme
internal/app/catssplit.go    + test   pane.split → an independent sibling ced
internal/app/catsfrecency.go + test   path.list recents → the folder picker
internal/app/catsagents.go   + test   pane.list → see / quote / ask
main.go                              a new --root flag (see 5.5)
```

### The shape every one of these ended up with

Poll on a goroutine → cache on the App → read the cache from the loop.

It is not a style preference. The theme is applied from an event, the
frecency list is read by a picker, and the agent segment is built **inside
a draw call** — none of those may dial a socket. So each feature has a
`catsPollX` that is Tier-1 gated and rate-limited, posts a `catsEvent`
back, and a main-loop reader that only ever touches the cache. Refresh
points are the stream's own events. Worth keeping for 5.6 and anything
after it.

`catsPostNotice(screen, msg)` was added for the other half of the same
rule: a background goroutine's only permitted output is one status phrase,
posted, never flashed directly.

### (a) 5.4 — the theme bridge is ten keys wide, and that is the finding

cats and ced were built by the same author on the same idea: **eight
required color keys** (`bg fg muted line accent ok warn err`), everything
else derived. The names are identical on both sides. So the synthesis is
those eight, plus `panel`→`sidebar-bg` and `panel2`→`line-hl`, and ced's
own derivation table (palette.go) invents the other twenty-seven —
including the entire syntax palette, which cats has no opinion about
because it is a multiplexer.

Two rules earn their keep:

- **Hex only.** cats accepts any CSS color and its own derivations emit
  `rgba()` for translucent keys. Those are dropped. If dropping one costs
  a CORE key, the synthesis is abandoned entirely — a theme built from
  five of eight keys is a stranger wearing the host's name.
- **`chrome` is deliberately NOT taken for `status-bg`.** ced paints its
  status bar as background-colored text on `status-bg`, so it needs a LOUD
  color; cats' chrome is a quiet dark surface, and importing it would give
  a dark-on-dark bar. The derived `status-bg = accent` is what carries the
  resemblance.

Auto-select is for the unpinned only (`App.themePinned`, set from
config.json at startup and by any picker choice), and it is applied with
`persist=false` — so a ced started in a plain terminal tomorrow is still
the shipped default rather than a frozen copy of one night's cats palette.
The one pinned case that still follows is being pinned TO the host theme.

Refresh rides `focus_changed` (rate-limited to 3s) until `theme_changed`
exists upstream — the roadmap's §5 item 3.

**Known cosmetic wart, not introduced here:** with a green accent and a red
`err`, ced's `accent-soft` derivation (`lighten(mix(accent, err, .45))`)
lands on olive — live, cats-green produced `syn-keyword = #979b81`. That
rule is hue-naive and every hand-written green theme gets the same;
fixing it is a change to `derivations()` that touches every theme, so it
was left alone.

### (b) 5.5 — the split is a diff, and that is upstream's fault (kindly)

```
pane.list   → the ids that exist now
pane.split  → the host opens a shell beside us … and answers `ok`, no data
pane.list   → the ids again; the one that appeared is ours
pane.send_input → type `exec ced --root <root> <file>` into it
```

`tab.create` returns `{num, pane}`; `pane.split` has the id in hand
(`np` in `CmdPaneSplit`) and drops it. Hence the diff — and hence
**`catsNewPane` insists the answer be UNIQUE**: zero new panes means the
split did nothing, two means somebody raced us, and both refuse. Typing a
command into the wrong pane is far worse than not typing it. Written up as
§5 item 7 (return the pane) and item 8 (accept an argv).

**`ced --root <dir>` is new.** `ced internal/app/x.go` roots at
`internal/app` — the right guess for a human, the wrong one for a program
reproducing a workspace. Without the flag, a split of a nested file would
give the sibling a one-package file tree.

`exec` prefixes the line so quitting the sibling closes the pane instead
of leaving a shell prompt. Everything is single-quoted;
`catsShellQuote` is the file's safety boundary — it is the one place ced
hands a path it did not author to a shell.

### (c) 5.7 — two lists, not one

ced's recents are places you **edited** (recorded by this program, carrying
tab counts). The host's are places you have **been** (every `cd` in every
pane, cdx-ranked). They are not interleaved: ced's first, the host's after,
suffixed `· cats` — which, in a fuzzy picker, is also a search term that
narrows to the host's half for free.

Deduped against ced's own set (and the current root), pruned to what still
exists, capped at 24. `hasRecentFolders` now passes on the host list alone,
so the picker is worth opening on a ced that has never been anywhere.

"Open project in new cats tab…" is a separate row, not a modifier on that
picker, because the two answer different questions: one MOVES this editor
(and takes its unsaved work along), the other leaves it and starts a second
one elsewhere. It uses `tab.create` with an argv — no shell, nothing to
quote.

### (d) 5.8 — the self-check is the whole trick

`pane.list` reports agent identity and state per pane, so the status bar
can name the sibling that most wants a human (`blocked > working > idle`,
ties by pane id so the segment cannot flicker), and clicking it focuses
that pane.

**Our own pane must be excluded, and not for tidiness:** ced reports itself
as the agent `"ced"` through the hook socket (5.3), so without the check
the editor would offer to send the user's selection to itself. The live
run proved this from the other direction — this very session's pane
(`wF:p26`, claude, working) is what the self-check removed, and turning
the check off brought `"claude: working"` straight back.

Sending is `submit: false`, permanently. An editor that could silently
prompt an agent is an editor that can spend somebody's tokens and edit
their files with no keystroke. Multi-line selections are safe because cats
**paste-encodes** `send_input` against the pane's live mode state, so a TUI
with bracketed paste on receives twenty lines as one block instead of
executing them one at a time — that fact is what makes the verb sane.

The quote carries `path:12-40`, project-relative, with the end line backed
off by one when the selection ends at column 0 (the shape every drag has).

## Surfaces added

- **≡ "Cats" group**, spliced by `visibleMenuGroups` only when `InCats` —
  conditional rather than dimmed, because every row addresses a program
  that is not there in a plain terminal. Six rows. This is also why
  `TestMenuLayout_NoCustomActions`'s 132 rows did NOT move: `newTestApp` is
  not in cats.
- **`Esc C` leader namespace** (`r` `d` split, `p` project tab, `a` focus
  agent, `s` send selection, `k` ask chat) — the third dynamic (`subFor`)
  prefix, so outside cats it arms nothing and the hint says why.
- **Editor right-click**: the two split rows, plus the two send rows when
  there is a selection.
- **Status bar**: the agent segment, first in the right-hand run and so the
  first dropped on a narrow window.

## Live verification

Run against the real catway this session lives in (`wF:p26`, self = pane
289), via a throwaway `zz_live_test.go`, deleted after. Read-only — no
split was performed, nothing was typed into anyone's pane:

```
host theme "cats-green", 33 colors → synthesized, label "Cats (host: cats-green)", dark
mapped: bg fg muted line accent ok warn err sidebar-bg line-hl   (10 of 33)
resolved: bg=#1F2420 accent=#4DB380 synKeyword=#979B81 synString=#6AC47A
host recents=34 → 24 rows; "~/projs/go/cats  · cats", "~  · cats", …
panes=16, agent panes=0            ← correct: the only agent IS us
  with the self-check off: "claude in wF:p26 — working" → segment "claude: working"
```

## State / next steps

- **Phase 5 remaining: 5.2 (⌘ table)**, independent of everything and gated
  on Mac-app routing; **5.6 (real terminal panes)**, which wants
  `pane.wait_for_output` plus the spawn plumbing 5.5 just built; **5.8's
  last quarter** — `capture` a sibling pane into a read-only compare tab.
- New upstream asks, both small, both in §5: **pane.split should return its
  new pane** (kills the diff), and **pane.split should take an argv** (kills
  the shell).
- Untouched follow-ups from this work: the split's sibling opens at the top
  of the file rather than at your cursor (ced has no `+line` CLI form); the
  agent status segment shows one agent and a count, with no way to see the
  full list except the picker; `catsRecentFolders` stats every candidate on
  every picker open (24 stats, fine today, not free on a network mount);
  the host theme's `syn-keyword` olive noted in (a).
- Unchanged from before: 4.6 (blame layer) unclaimed; Phase-2's `⚠` tab
  marker should re-raise a deferred conflict prompt; 3.3 (hover on dwell)
  and 3.4 (recent-files picker) small and unclaimed; the 4.3/4.4/4.5
  leftovers listed in the previous session doc still stand.
