# Session: Favorite relative locations + the CLI on urfave/cli

- Date: 2026-09-10
- Branch: `main`
- Repo: ced (`~/projs/go/ced`)
- Session id: `01EZ7QsbPQUKFLDxTwEVibjJ`
- Predecessor: `2026-0910-1403-splitter-ergonomics.md`
- Commit at start: `a95b960`

## What was asked

> Create the concept of favorite relative locations in a project.
> For example:
>
> - plans -> ai_docs/plans
> - clsess -> ai_docs/claude_sessions
> - cosess -> ai_docs/copilot_sessions
>
> We could invoke these (have CEd go to this location after startup) with
> a command like: `ced fav plans`.
> Let's use this cmdline options package if we are not already:
> github.com/urfave/cli/v2

Two features in one prompt, and the second is load-bearing for the
first: `ced fav <name>` is a subcommand, and the hand-rolled arg walker
in main.go had no notion of one.

## The two decisions that were the user's to make

Both were asked up front, because either answer produced materially
different work.

**1. What does "go to this location" mean?** Re-root the editor at the
favorite, or reveal it in the tree?

Answered: **reveal, keep the project root.**

That is the whole justification for the feature existing. `ced
ai_docs/plans` *already* re-roots — and pays for it by discarding the
project, since git status, gopls's `rootUri`, the finder index, the ACP
session cwd and every plugin's working directory are all derived from
`rootDir`. A root of `ai_docs/plans` is a workspace where none of those
describe the code. So the verb keeps the project and moves the **view**,
which is the one thing the plain path spelling structurally cannot do.

**2. Where does the name → path map live?** Global, per-project, or both?

Answered: **global defaults + per-project overrides**, one file.

## What was built

| file | what it owns |
|---|---|
| `internal/favorites/favorites.go` | the file format, both scopes, `Clean`/`CleanName`, the walk-up `Resolve` |
| `internal/filetree/filetree.go` | `Tree.Reveal` — expand/load down to a path, select it |
| `internal/app/favorites.go` | `App.RevealPath` — the editor-side verb |
| `main.go` | the whole CLI on urfave/cli, plus the `fav` subtree |
| `internal/userconfig/userconfig.go` | `FavoritesPath()` |

Plus a `_test.go` beside each, per the project convention.

## The resolver: walking up is the feature, not a fallback

`ced fav plans` is typed from wherever the user is standing, which for a
project of any size is not the project root. A version that only worked
from the top would be half a feature. So `Resolve` climbs from the start
directory, and **the directory that owns the favorite becomes the root.**

The subtlety that makes two scopes work: each candidate level is asked in
**full** — its own project override first, then the global default —
rather than looking the name up once against the start directory. The
override is keyed by the root that owns it, and that root is one of the
directories being climbed. A single up-front lookup would make every
per-project override invisible from every subdirectory of its own
project. `TestResolve_ProjectOverrideIsReachableFromASubdirectory` pins
exactly that.

## Confinement, checked twice

A favorite is a project-**relative** location. The one thing this must
not become is a way for a config file to point the editor at `/etc`.

- `Clean` refuses empty, `.`, absolute, and anything climbing out with
  `..` — at **write** time, so an entry that could never resolve is never
  written. It also runs on **read**, because a text editor will happily
  write what `fav add` refused.
- `Resolve` re-checks after the join, because a lexical test alone is
  escapable through a symlink living inside the root (the workspace-edit
  rule). The **root** is resolved too, or a project under `/tmp` reads as
  outside itself on macOS.

## Two failures that must not collapse into one

They have different fixes, so they get different messages:

| case | message | why |
|---|---|---|
| name bound nowhere | `no such favorite "plsn" — known: clsess, cosess, plans` | it's a typo; a bare "no such favorite" sends you off to read a config file |
| bound, directory missing | `favorite "cosess": /…/ai_docs/copilot_sessions does not exist` | it's a project that doesn't follow the convention; name the path so there's something to check |

Same split in `fav list`, where a global default this project doesn't
follow is marked `missing here` rather than hidden.

## `Tree.Reveal` — ancestors, not the target

The tree is lazy, so a folder three levels down has **no Node at all**
until something walks the path and loads each directory on the way. That
walk *is* the reveal.

Deliberate asymmetry: ancestors are expanded because they must be (or
the target's row doesn't exist in the flattening), but the **target is
left as it was found**. A future scroll-to-a-path must not spring open a
folder somebody deliberately collapsed. Arriving *by name* is the
opposite case, so `App.RevealPath` expands it — that's the caller's
policy, not the primitive's.

`RevealPath` also takes the **keyboard** for a folder, because the
selection highlight only renders while the tree is `Focused` — a cursor
nobody can see is worse than none. A **file** favorite opens a tab
instead and leaves the keyboard in the editor.

## The CLI rewrite: one parser, no shadow copy

The temptation was to keep the old pure `resolveArgs` for testability and
bolt urfave on beside it. That is exactly the drift the codebase's house
rules exist to prevent. Instead:

- `parseArgs` runs the **real** `cli.App` and fills a `cliResult` rather
  than acting. Tests drive actual flag definitions, actual subcommand
  resolution, actual error strings.
- `resolveArgs` in `main_test.go` is a **one-line alias** onto it, so
  every pre-existing arg test survived unchanged and now exercises the
  real parser.

Four urfave details that mattered:

- **`actionDone` is the default result.** urfave serves `--help` and
  `help` itself without ever calling an Action, so the zero-ish value has
  to mean "handled, nothing left to do". Were it `actionEdit`, asking for
  help would start an editor.
- **`helpText` is the app's help *template***, not a function beside it,
  so `--help`, `help` and a usage error reach the same words. It's a raw
  string — which cost one compile error, because the draft contained a
  backtick.
- `HideVersion` + an explicit `--version/-v/-V` flag, since main owns the
  exact output (`ced 0.2.0`) and urfave spells it differently.
- `OnUsageError` returns the message alone (urfave's default buries it
  under the whole help block); `ExitErrHandler` is a no-op, so a test
  driving the parser can't kill the test binary.

`resolveTarget` stayed a plain function — it's the one piece of the CLI
that is about the filesystem (dir → root, file → root+tab, missing → the
vim-style new-file intent) rather than about flags.

## Two traps found by using it

**A path in the name slot.** `ced fav /home/me/proj` would have failed as
`no favorite named "/home/me/proj"` — an error about the wrong thing
entirely. Caught by a test that assumed the bare form took a directory;
fixed by validating with `CleanName` and pointing at `ced fav list <dir>`.

**A favorite named after a subcommand.** urfave resolves a subcommand
*before* falling through to the open action, so a favorite called `list`
would be written happily and then be permanently unreachable. `fav add`
now refuses its own words (`add`, `rm`, `list`, `path`, …) — write time
is the only moment the user can still pick another one.

## Verified in the real editor

Not just in tests. `run-ced`'s capture tool hardcodes `exec.Command(bin,
".")`, so the run went through a wrapper script (`exec ced fav plans`)
with `-seed` supplying a favorites.json and `-dir` set to
`proj2/internal/app` — two levels down, to prove the walk.

```
 EXPLORER                    │  ≡
 proj2                       │        ← root is the PROJECT, not plans
 ▾ ai_docs/                  │
   ▸ claude_sessions/        │        ← untouched, still collapsed
   ▾ plans/                  │        ← expanded, selected
       caret-blink.md        │
       splitters.md          │
 ▸ internal/                 │
 Revealed ai_docs/plans                                          ced +1  ≡
```

The HTML capture confirmed the `plans/` row carries a distinct
background (`#2e523f` against the sidebar's `#242a25`), i.e. the tree
really did take focus and the highlight really is rendering.

## Behavior change worth knowing

**A filename starting with `-` is now a parse error** rather than a
new-file intent — urfave rejects unknown flags where the old walker fell
through to `os.Stat`. `ced ./-foo.go` works. Judged a net win, but it is
a change.

## Deliberately not done

**No ≡ menu row and no leader key.** This is a *startup* verb.
Re-revealing a favorite mid-session is a different feature that wants a
picker, and `App.RevealPath` is already the primitive it would be built
on. Adding a row would also mean updating the `menuLayout` geometry pins
(`TestMenuLayout_NoCustomActions` and friends) — real cost, for scope the
prompt didn't ask for. Offered to the user instead.

## Docs

- `CLAUDE.md`: three architecture-map entries, a **Favorite locations**
  house-rules section, a **The command line is urfave/cli/v2** section,
  and `favorites.json` added to the config-file exception list in
  *What NOT to add*.
- `README.md`: a **Favorite locations** subsection under Usage, plus the
  `ced fav plans` line in the usage block.

## State at the end

`make test` green under `-race`, `go vet` and `gofmt` clean.
