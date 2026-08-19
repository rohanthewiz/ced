# Session: running an executable, and where "there" is

- Date: 2026-08-19
- Branch: `main`
- Repo: ced (`~/projs/go/ced`)
- Session id: `178baee5-538f-46a6-adeb-1c1dd6081afe`
- Predecessor: `2026-0817-1021-commit-prompt-ai-button.md`

## What was asked

> Give me the ability to run an executable file in a new terminal panel

…with a screenshot of the tree's right-click menu open on
`mac-install.sh*` — Rename, Delete, Copy, Zip, Copy rel path, Copy abs
path. The `*` in that screenshot is the whole premise: the tree already
knows which files are runnable (execmarks.go), and the editor already
owns a shell (terminal.go). Nothing joined them.

Then, after the first cut ran the file immediately:

> Stage before the run so we can add args, and possibly change directory
> just for the run

and, when asked which directory the staged `cd` should target:

> Ideally I would like the options of (the file's own dir, project root,
> or some other directory browsed through CDX like directory search

## What shipped

`internal/app/runexec.go` — one verb, two doors:

- tree right-click → **Run in terminal…** (appended, and only for a node
  the tree marked executable)
- ≡ **File** → **Run *name* in terminal…** (dynamic label), the keyboard
  twin per the right-click-swallowed rule. No leader key — the flat table
  is out of letters.

Both land in a directory picker, and the pick stages a line on the
terminal panel's input field:

```
Run build.sh from…
  ~/proj/scripts   · where the file lives
  ~/proj           · project root
  ~/projs/go       · cats          ← cdx frecency
  ~/Downloads      · cats
  …
```

```
❯ cd ~/proj/scripts && ./build.sh --verbose
hello, args: --verbose
```

## The three decisions worth keeping

**1. It stages, it does not submit.** The first cut ran the file on the
click, on the argument that an execute bit makes "run it" unambiguous
(unlike catsrun's `go run main.go` guess, which stages for exactly that
reason). Wrong: an execute bit says how to START a program and nothing
about what to pass it, so a row that fired immediately could never run a
script that takes a flag. Re-running is the panel's own history (Up),
which keeps the edited line — hence no per-file command memory was added.

**2. The `cd` is part of the staged line, because grsh has no subshell.**
Checked the language spec in the module cache before designing anything:

> **Builtins cannot be backgrounded** (`cd /tmp &` is an error) — there
> is no subshell to run them in.

So `(cd dir && cmd)` is not available, and grsh's `cd` chdirs the *whole
editor process* by design. Wrapping in `sh -c '…'` — what the cats pane
run does (catsRunScript) — would bury the command inside quotes where
arguments cannot be typed, which defeats decision 1. What is left is the
honest version: the `cd` leads the line, visible and editable, joined
with `&&` so a failed cd can't run the command somewhere unexpected, and
omitted entirely when the shell is already there (menuCatsTerminal's
pointless-cd rule, which also stops a second run stacking a redundant
one). Leading is also what makes typed arguments land after the command.

**3. "CDX like directory search" was already in the codebase.** `cdx` is
a frecency directory jumper on the owner's machine — and cats keeps a
cdx-ranked history that ced already consumes in `catsfrecency.go` for the
"Open project…" picker. So the third option is not a new mechanism: the
run-directory picker widens into `catsRecentFolders` plus ced's own
`sessionStore.Recent()`, deduped on `session.Normalize` (the folder
store's key) and pruned to what still exists. The two pickers can never
disagree about which directories exist. Verified live — 24 rows of the
owner's real cdx history came back through `path.list`.

Ordering is by specificity: the file's own directory, the project root,
wherever the shell currently is, then history. ONE candidate is not a
choice, so a workspace with nothing else to offer stages straight away.

## Smaller rules that fell out

- **`filetree.IsExecFile`** is now the single definition of "runnable",
  shared by the tree's `*` marker and the verb's live re-check. The node's
  bit is stamped at the last reload, so a `chmod -x` in the very panel
  this row feeds must refuse rather than stage something that only fails
  oddly.
- **`shellArg` quotes only what needs it**, unlike `catsShellQuote`'s
  unconditional single-quoting — that form is right for a line nobody
  sees, and wrong for one a person reads and edits.
- **The path is relative to the chosen directory when it sits inside it**,
  absolute otherwise (catsRelPath's rule). The `./` prefix is load-bearing:
  a bare `tool.sh` is a PATH lookup.
- **The busy-terminal guard was deliberately dropped** in the rewrite.
  Staging a line to press Enter on when the running command finishes is
  reasonable; refusing to submit is terminal.go's job, with its own
  message.

## Files

```
internal/app/runexec.go        new — the verb, the picker, the staged line
internal/app/runexec_test.go   new — 13 tests
internal/app/modals.go         tree context row (conditional, appended last)
internal/app/app.go            ≡ File row
internal/app/app_test.go       menu geometry pins: 142 rows, height 148,
                               dividers [2, 5, 145]
internal/filetree/filetree.go  IsExecFile extracted and exported
internal/filetree/filetree_test.go  TestIsExecFile
CLAUDE.md                      architecture map + a house-rules section
```

## Verification

`make test` (`-race`, all packages) green. `internal/app` re-run five
times while chasing a flake (below).

Live, through the `run-ced` capture tool, on a scratch project with
`scripts/build.sh` executable — the full gesture, picker → row 0 →
typed ` --verbose` → Enter:

```
│─ Terminal · /private/tmp/runexec-demo/scripts ──────────────── ✕ ─
│ ❯ cd /private/tmp/runexec-demo/scripts && ./build.sh --verbose
│ hello, args: --verbose
```

The panel header follows the cd, the arguments reach the script, and the
picker's frecency half is populated from the live cats socket.

## Loose end

One intermediate `go test ./internal/app/` run failed once with the
detail scrolled out of the captured tail. The code was byte-identical to
the run before it (only CLAUDE.md had changed between them), and it has
not recurred in five subsequent runs (3 plain, 2 `-race`). Looks like a
pre-existing timing flake; the failing test was never identified. Worth
a `-count=5` sweep if it shows up again.
