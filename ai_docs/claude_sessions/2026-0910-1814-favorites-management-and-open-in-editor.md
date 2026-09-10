# Session: Favorites management, and Open in $EDITOR

- Date: 2026-09-10
- Branch: `main`
- Repo: ced (`~/projs/go/ced`)
- Session id: `01EZ7QsbPQUKFLDxTwEVibjJ`
- Predecessor: `2026-0910-1708-favorite-locations-cli.md`
- Commits: `ad95671`, `939d4c9`, `c6f952d` (continuing `51b132a`)

## What was asked

Three prompts, in sequence.

**1.** > add the menu row and picker for revealing favorites mid-session

**2.** > Add Favorites management under "File | Manage Favorites" -> Menu
> should have the first item be "Global" followed by local options - with
> the ability to add a new favorite. An "Add to favorites" option should
> also be given in the context menu of the Filetree when the item is a
> directory. And a separate request but also in the context menu of the
> filetree, for an editable file item I would like a context menu item to
> open in the current $EDITOR

Amended mid-turn: > for an editable file item **or directory**

**3.** > Can we add the Open in $EDITOR to the filetree context menu also?

The third one is the interesting one — see "The bug that prompt found".

## `ad95671` — Go to favorite (≡ Navigation)

The mid-session twin of `ced fav <name>`. Placed in **Navigation**, not
Search or View: Go back / Go forward walk the trail you made, this jumps
to the places you named in advance. A browser's pairing — history beside
bookmarks.

**The one real design point: it resolves STRICTLY in the open root.** The
CLI walks up because it is still *choosing* a project; a running editor
already has one. A walk here could resolve a favorite in the workspace's
**parent** — a path outside the file tree, which the tree would then
refuse, having been handed somewhere the user cannot see.

That forced a refactor: `favorites.ResolveIn` (no walk) became the half
that knows how to turn one (root, name) into a path, and `Resolve` became
the loop around it. One implementation of "resolve here".

Two smaller rules:

| surface | shows entries that don't resolve? | why |
|---|---|---|
| `ced fav list` | yes, marked `missing here` | it is a REPORT |
| ≡ Go to favorite | no, dropped | it is a list of VERBS — the palette has no disabled state, and a row answering Enter with "that isn't here" is worse than one never offered |

The row is never dimmed. A dead end that cannot explain itself is worse
than a flash naming the fix, and an honest predicate would put a file
read inside `menuLayout`, which runs every frame the menu is open.

### A bug found while testing this

Project override keys are written symlink-normalized by `Add`, but
favorites.json is hand-editable and a person types the spelling they use.
On macOS every path under `/var` and `/tmp` differs from its resolved
form — so a hand-written block would parse, list, and **silently never
apply**. Nothing on screen would explain it.

`Lookup` / `List` / `Remove` now all read through one tolerant helper
(`projectOverrides`, normalized first, lexical as fallback). All three,
because a key that resolves but cannot be deleted would be its own bug.
Regression test skips on platforms where the temp dir needs no
resolution — there is nothing to pin there.

## `939d4c9` — Manage favorites, Add to favorites, Open in $EDITOR

### Two questions asked up front

Both had genuine forks, so they were put to the user rather than guessed.

**"Global" as the first item** — three readings: a scope label on a flat
list, a drill-in row, or a scope picker. Answered: **drill-in**.

**Scope for the tree's Add** — answered: **prompt with a `[scope]` chip**,
the commit prompt's `[trailer: on]` pattern.

### The Manage layout, and why the asymmetry is the design

```
Manage favorites — proj2          Global favorites
┌────────────────────────┐        ┌──────────────────────────────┐
│ Global favorites…  (3) │  ───▶  │ clsess  ai_docs/claude_sess… │
│ local   docs/plans     │        │ cosess  ai_docs/copilot_ses… │
│ Add a favorite…        │        │ plans   ai_docs/plans        │
└────────────────────────┘        │ Add a global favorite…       │
                                  └──────────────────────────────┘
```

The global map is written once and shared by every project, so editing it
*from inside one of them* is the rarer act and belongs a gesture deeper.
The override list for the repo in front of you is what you maintain, so
that is what the row opens on.

It **lists what doesn't resolve** — the report/verb split one floor down
from the Go-to picker. You cannot go to a folder that isn't there, but a
broken entry is exactly the one you came here to fix. The `missing here`
marker is on **project rows only**: a global default this project doesn't
follow is the normal case, and marking those would put a warning on
nearly every row and teach the user to ignore it.

Per-entry actions: Go to (only when the path resolves), Rename…, Change
path…, Override in this project… (global entries with no override yet),
Remove.

### Details worth keeping

- **Path is asked BEFORE name**, because the name's default is derived
  from it. Answering the other way means typing a name and then
  discovering what it should have been. The tree's right-click skips the
  path prompt entirely — the click *was* the path answer.
- **Every list's add row is seeded to that list's scope**, so where you
  asked decides what you get rather than a flag you have to remember. The
  chip still overrides, so the seed is a default and never a trap.
- **The chip is a closure, not an App field** — the commit prompt's rule.
  The value belongs to one invocation.
- **Rename is remove-then-ADD-FIRST.** `Add` is the only path that
  validates a name; doing the remove first would lose the entry when the
  new name is refused. Pinned by a test that submits `a/b`.
- **A malformed file refuses to be written.** `loadFavoriteSet` flashes
  and returns failure rather than an empty set — reading a syntax error
  as "you have no favorites" on a surface about to write would save over
  whatever the file held.
- Nothing confirms. A favorite is a name, not data.
- Added `favorites.ResolveRel` so the management list can resolve an
  entry's **own** path rather than the name it is filed under — a global
  entry shadowed by a project override still has a path, and "Go to" must
  offer the row the user is looking at.

### Open in $EDITOR

`$VISUAL` beats `$EDITOR` — the convention's own answer: $VISUAL is what
you set when a full-screen program is welcome.

**Tier 1 runs it, Tier 0 stages it**, and that is structural rather than
careful: a cats sibling pane is a real pty so vim/emacs/helix work, while
ced's terminal panel is a REPL strip and explicitly *not* a pty. Side by
side, not stacked — `menuCatsTerminal` splits vertically because a
terminal is a strip under your work; this is a peer editor, and a
half-height vim is worse than a half-width one.

The path is written **relative to where the command will run**: relative
in the Tier-1 pane that starts at the root, absolute in a staged line
whose cwd grsh may have moved.

`editorEnv` is a package var pinned **empty** by newTestApp. Not
tidiness: the tree row was conditional, so without the seam the
context-menu row counts would pass or fail depending on what the
developer exported.

### A side effect I caused

Verifying the Tier-1 path ran it *for real*. The capture harness inherits
`CATS_CONTROL_SOCKET`, so ced split an actual pane and started `nvim` in
the scratch project — in the user's live cats session. No `cats` CLI on
PATH to list or close it, and closing someone's pane is not mine to do.
Reported to the user.

**Lesson for next time:** before exercising a cats Tier-1 verb through
`/tmp/capture`, clear `CATS_ENV` / `CATS_CONTROL_SOCKET` from the child's
environment, or the "test" is a real gesture in the user's session.

## `c6f952d` — the bug the third prompt found

> Can we add the Open in $EDITOR to the filetree context menu also?

It was already there. The row existed, wired to `ctxOpenInEditor` — but
gated on `editorCommand() != ""`, and **neither `$VISUAL` nor `$EDITOR`
is exported on this machine**, login shell included. So it never
rendered. From the user's side that is indistinguishable from the feature
not existing.

Checking the facts before answering was what turned "no, it's already
there" into a real fix:

```
EDITOR=[<unset>]  VISUAL=[<unset>]
```

### The wrong precedent, and the right one

The gate followed the **Paste row's** argument: a popup's fixed
vocabulary is something users learn positions in, so a permanently dimmed
row is worse than its absence.

But the two are not alike:

| | Paste | Open in $EDITOR |
|---|---|---|
| precondition | something the user just did | a variable they may never have set |
| can they see why it's absent? | yes — they didn't copy anything | no |

The right precedent was already in this codebase, and had even been cited
one commit earlier for the favorites picker:

> `menuCopilotAuth` flashes WHY — a dimmed row is a dead end.

So both rows are now unconditional and the refusal teaches, naming both
variables and an export line:

```
Neither $VISUAL nor $EDITOR is set — try:  export EDITOR=vim
```

**A row nobody can find is worse than a row that explains itself.**

### One ordering consequence

Being unconditional, the row joins the FIXED vocabulary — so it belongs
*above* `Run in terminal…`, which is still the conditional row and keeps
the last slot its own rule gives it. Appending the editor row had
displaced it, caught by `TestTreeContextRunRowOnlyForExecutables`.

The three context tests now pin the exact ordered label list:

```
folder → … Select, Add to favorites…, Open in $EDITOR
file   → … Select, Open in $EDITOR, [Run in terminal…]
root   → … Copy abs path, Add to favorites…, Open in $EDITOR
```

`hasOpenInEditor` was deleted rather than left dead.

## Menu geometry pins, twice

`TestMenuLayout_NoCustomActions` and `TestMenuLayout_WithCustomActions`
moved in both feature commits:

| | rows | height | dividers |
|---|---|---|---|
| before | 148 | 154 | 2, 5, 151 |
| +Go to favorite | 149 | 155 | 2, 5, 152 |
| +Manage favorites, +Open in $EDITOR | 151 | 157 | 2, 5, 154 |

The numbers written in CLAUDE.md's menu section were already stale
against the code — the code is the truth, the test is where it's pinned.

## What is NOT verified by a real run

The **tree context menu itself**. `/tmp/capture` sends keystrokes, not
mouse events, and treenav has no context-menu key — so there is no way to
drive a right-click from here. Covered by unit tests asserting exact
ordered labels for file / folder / root, which is strong, but a
right-click in a real terminal is the last confirmation.

Everything else was captured live: the Manage picker, the global
drill-in, the per-entry action list, the scope chip, the Go-to picker
dropping an unresolvable favorite, and the `$EDITOR` refusal.

## State at the end

`make test` green under `-race`, `go vet` and `gofmt` clean.
