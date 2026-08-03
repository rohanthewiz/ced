# Session: open folder, recent folders, and per-folder session restore

Session ID: `e7bb0077-dd6e-4fa9-8d78-7c7eb38576e0`
Date: 2026-08-03
Branch: `main`
Commit: `73123d8` (1 commit, +2225 / −29 across 14 files)

---

## How this started

Stages 7 and 8 of [`ai_docs/opus-improvements-analysis.md`](../opus-improvements-analysis.md),
taken together because the second is what makes the first worth having:

| # | Stage | Items |
|---|---|---|
| 7 | Open folder | recent folders, bare-`ced` restore |
| 8 | Session restore | open tabs + cursors per root |

One design call went to the owner before any code was written, and it
changed what shipped — see below.

---

## The question that was asked first

The plan sketched **"bare `ced` opening the last folder"**. That reads
fine on paper and breaks the gesture the editor is actually launched
with:

```
cd myproj && ced        ← would silently land you somewhere else
```

Offered three readings; the owner picked **keep cwd, add `--last`**.
Session restore then covers the half that was actually valuable: `cd
myproj && ced` gives you the folder's *tabs* back without hijacking where
"here" means. `ced --last` is the explicit form, and ≡ → File → Recent
folders… is the in-editor twin.

Second question, same message: tab restore **on by default with a ≡
toggle** (the auto-save shape), which is what shipped.

---

## Stage 7 — open folder

`internal/app/folder.go`, `main.go`

### A root switch is a restart, and it lives in `main`

The plan already said teardown → `New(newRoot)` rather than a field
reassignment, and the reason holds up: `rootDir` is cheap to change, but
everything *derived* from it is not — the tree, the finder index, git
status and both git panels, gopls's `rootUri` (fixed at initialize), the
ACP session cwd, MCP's `roots/list`, plugin working directories, the
compare panel's two sides.

What the plan didn't say is that **the App cannot rebuild itself**. So:

```
requestOpenFolder → App.nextRoot = root; App.quit = true
                         │
                    Run() returns
                         │
main:  next := a.NextRoot();  a.Close();  New(next)  ─┐
        └────────────────── loop ──────────────────────┘
```

One code path builds a workspace, and it's the one that runs on every
single launch. A second re-derivation path would be exercised by nobody.

Two details in that loop are load-bearing:

- **`Close` is explicit, not deferred.** A deferred `Close` fires when
  `main` returns, so a folder switch would leave the old screen, its
  goroutines and its language servers alive underneath the new App.
- The `openFile` seed is consumed on the first iteration only
  (`for openFile := res.OpenFile; ; openFile = ""`), so switching folders
  doesn't try to reopen a file from the old project.

The screen blinks once on the way through. That is the whole price.

### Resolving what the user typed

`resolveFolder` expands a leading `~`, and resolves a **relative path
against `rootDir` — not the process working directory**. That looks
pedantic until you remember the embedded grsh terminal's `cd` chdirs the
whole editor process by design, so "the current directory" is a value the
user cannot see, while the project root is on screen.

A file path is refused by name ("… is a file, not a folder") rather than
quietly rooting at its parent.

### The recent picker

`openPicker` (house rule), and it **excludes** the current root rather
than annotating it — the inverse of the theme picker, where re-picking is
how you revert a preview. Re-picking your own folder would tear down a
workspace and rebuild an identical one.

Folders deleted since they were recorded are **pruned during the walk**,
not dimmed: a row you can't open is worse than a shorter list, and the
walk is the only moment ced is in a position to notice.

---

## Stage 8 — session restore

`internal/session/session.go`, `internal/app/folder.go`

### A file of its own, for the inverse of mcp.json's reason

`~/.config/ced/state.json`. mcp.json is a separate file because the *user*
writes it; this one is separate because **ced rewrites it on every folder
switch and every exit**. Machine churn has no business landing in a file
somebody hand-edits, and a corrupt state file should cost a tab list
rather than a settings file. `userconfig` owns only the path
(`StatePath()`); the schema lives in `internal/session`.

**Order is the recency.** The entry list is stored most-recent-first
rather than carrying timestamps that would have to be sorted on load.
Nothing in the UI says "opened 2 hours ago", so a timestamp would be a
field with no reader and one more thing to get wrong across clock skew.

### Two write points, deliberately

```
New()    → Touch(root), save          ← "I was here"     (survives a crash)
Close()  → Record(tabs, active), save ← "this is what I had open"
```

A run that dies unexpectedly costs its tab list — which was never
written — but not the fact that the folder was opened at all, which is
what keeps `--last` and the recent list honest.

### Symlink normalisation is not cosmetic

```
ced /tmp/proj          → rootDir = /tmp/proj
cd /tmp/proj && ced    → rootDir = /private/tmp/proj    (macOS Getwd)
```

Without `session.Normalize` (Abs + `EvalSymlinks`, best-effort) that one
directory keeps **two half-sessions that overwrite each other in turn**.
It's exported because the app layer asks the same question — "is this
entry the folder I'm in?" — and two normalisers would answer differently.
Resolution is best-effort: a path that no longer exists keeps its
absolute form, or `Remove` could never prune it.

This surfaced through the real-binary capture, not a unit test: the first
seeded `state.json` restored nothing, because the capture runs `ced .`
with `cmd.Dir` set and the seed said `/tmp/cedproj`.

### Restore rules

- **Existence is checked here, not left to `editor.NewTab`**, which
  deliberately succeeds on a missing path — that's the `ced foo.go`
  new-file intent, right for an explicit open and wrong for a restore.
  Nobody asked to resurrect a file they deleted, and an empty buffer
  wearing its name is the worst possible way to say it's gone. (The unit
  test caught this: 2 tabs restored where 1 was expected.)
- **`Tab.RestoreView`, not `MoveCursorTo`** — the stored *scroll* is part
  of what's being put back, and every other cursor write sets
  `cursorMoved` so the next Render would scroll it away. Same argument
  the Find-all popup's Esc path makes.
- **The active index is captured after the append**, against the tab list
  rather than the stored one: files that failed to reopen leave gaps. If
  the active file itself vanished, the last tab that *did* open wins.
- Everything degrades in **silence** — too big, binary, unreadable, gone.
  The user asked to open a folder; a wall of "could not reopen" messages
  about files they may not remember having open is noise. One quiet
  "Restored 2 tabs" and no advice about turning it off (it fires on every
  launch, and a self-explaining flash every time is nagging).

### `wireTab` / `announceTab`

Restore is the **second** way a tab is born, so `openFile`'s wiring was
split rather than copied:

| | |
|---|---|
| `wireTab` | before the append — DecoSources (git < plugin < LSP), `WordHighlight`, first diff |
| `announceTab` | after — `lspOpenDoc`, `copilotOpenDoc`, plugin open hook |

A second copy would drift, and the failure mode is invisible: a restored
tab quietly without a git gutter, or without a plugin's marks.

---

## Verification

`make test` (race detector) green, plus real-binary checks through the
`run-ced` PTY capture skill — which earned its keep again, since the
symlink split above is invisible to unit tests:

- two tabs restored from a seeded `state.json`, the recorded one active,
  status reading "Restored 2 tabs"
- the recent-folders picker with the current root correctly absent
- a switch through the picker → editor restarts rooted at the new folder
- **switch away and back inside one run**: closed a tab with `Esc w`
  first, and only the surviving tab came back — proving `Close` records
  the *current* set, not the one it started with
- `Open folder…` with `../otherproj` typed into it → resolved against the
  project root and switched
- the prompt's hint overflowing the 54-column modal (caught and shortened)
- `--last` with and without a state file

New tests: `session/session_test.go` (17), `app/folder_test.go` (18),
plus 2 in `userconfig_test.go` and 3 in `main_test.go`.

Also fixed a **pre-existing flake** in `internal/mcp`'s handshake test:
`notifications/initialized` is fire-and-forget, so reading the fake
server's method list straight after `Connect` raced its reader goroutine.
It failed roughly 1 run in 10 before this change too.

---

## What's queued

Stages 9–12, untouched:

| # | Stage |
|---|---|
| 9 | LSP verbs — document symbols first, then references/rename/actions |
| 10 | Terminal diagnostics — scrollback → `diag.go` → clickable jumps |
| 11 | `--wait` / `--remote` — `$EDITOR` integration, single-instance open |
| 12 | Undo memory cap — byte-budget the snapshot stack |

Three notes for whoever picks these up:

- **Stage 11 now has a partner.** `--last` is the first flag ced has
  taken beyond print-and-exit, and `main`'s new restart loop is the
  natural place `--wait` would hook in — it already distinguishes "the
  editor exited" from "the editor wants to come back".
- **`internal/session` is a general per-folder store**, not a tab list
  with extra steps. Anything that wants to remember something per project
  — a last search, a panel layout, a branch — can add a field to `Entry`
  and inherit the recency queue, the cap and the atomic write.
- **`wireTab` / `announceTab` are now the tab-creation contract.** Any
  third way a tab is born (a preview tab for project-search, say — see
  stage 4's note) goes through them, not through a third copy.
