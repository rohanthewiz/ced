# Session: Recent locations — per-repo folder + file history in bytdb

Session ID: ff37bed7-e149-46cd-9e01-6aa30cd885af
Date: 2026-09-28

## 1. The request (cats-todo backlog)

> Track the top 5 most recently used folders followed by a thin spacer then
> the top 10 most frequently used folders. Once I select a folder, show me
> the top 3 recently used followed by a spacer then the top 8 most
> frequently used folders.

Clarified over three rounds with the owner:

1. "Once I select a folder" = **drill-in**: pick a folder, see its
   subfolders. Keep it simple — **the same 5 + 10 at every level**. Come up
   with an efficient algorithm.
2. "Use bytdb over state.json."
3. "No to the DB location. I want each repo to have its own recent
   locations / files, not a central location." Then, by question:
   the file lives at `<repo>/.ced/` (gitignored); the recent-FILES ring
   moves into the same per-repo db; the picker's top level is this repo's
   folders (project switching stays in ≡ File → Recent folders…).

## 2. Evolution (what was built and thrown away)

- **v1**: usage index in `state.json` (`Store.Usage`), top level =
  workspaces, drill-in = subfolders, subfolders of OTHER projects opened
  via `App.nextReveal` through main's restart loop. Rejected: storage.
- **v2**: `internal/folderuse` + `~/.config/ced/folders.bytdb`. Rejected:
  central location.
- **v3 (shipped)**: `internal/history` + `<repo>/.ced/history.bytdb`. The
  workspace kind (`UseRoot`), `Owner`, `nextReveal`/`NextReveal` and the
  main.go loop change were all removed as dead with it; ≡ File → Recent
  folders… is byte-for-byte back to HEAD.

## 3. What shipped

**≡ Nav → "Recent locations…"** (`internal/app/recentlocations.go`), under
Recent files. This repo's folders: 5 recent · `┄┄┄` spacer · 10 frequent
(not repeating the recent ones), labelled relative. `›` rows drill into the
same picker one level down ("Folders in internal", first row "Reveal
internal in tree"); a leaf is REVEALED in the tree — never re-rooted (the
favorites rule). A use = a file opened in a NEW tab (openFile's new-tab
branch only; tab switches don't count) or a folder picked here; only
folders strictly inside the root.

**Recent files (Esc-B)** now load from / save to the same per-repo db.
`state.json`'s `Entry.Recent` is read once to migrate a repo with no ring
(entries marked touched so Close writes them), and `recordSession` no
longer writes it.

**The algorithm** (`internal/history/index.go`): a path trie; each node
keeps its own `(hits, last)` and the componentwise MAX over its subtree.
Record = one O(depth) walk raising bounds. Top-k = best-first search on a
max-heap of subtree bounds / self entries — the first k self entries
popped are the top k, so cost ≈ k·depth·fanout·log, independent of index
size; "subfolders of X" is the same search started at X. Frequency ties
break on recency (the lexicographic bound stays valid). Recency is a
sequence number, not a clock. Eviction with hysteresis at 400 folders →
90%, protecting the most recent quarter. `TestIndex_SearchMatchesBruteForce`
checks the search against a full sort on 3000 random uses.

**Persistence** (`internal/history/db.go`, bytdb v0.18.0 via its
`database/sql` driver): tables `folder_use`, `recent_files`,
`history_meta`. bytdb locks its file per engine and two ced windows on one
repo are normal, so the db is **opened briefly** — load on first use,
write on Close — with 10×25ms retry on `ErrLocked`, pending changes kept
on failure. **Writes add deltas**: per-node `delta` hits, a dirty set,
pending deletes (subtree delete = range `(p/, p0)` so `proj2` survives
pruning `proj`); locally minted sequence numbers are re-issued from the
stored counters inside the transaction, so two windows merge rather than
the last to close winning (`TestWrite_TwoInstancesAdd`). Paths are stored
RELATIVE (a moved checkout keeps its history; test pins it). Loading
creates nothing; the first real write creates `.ced/` and appends
`history.bytdb*` to `.ced/.gitignore`.

**Palette** (`palette.go`): `paletteItem.spacer` (never selected, run,
counted or clicked; dropped while filtering), the list now SCROLLS to the
selection (it used to draw only the first 10 rows), and
`openPickerRows` asks for a taller frame.

Tests: `internal/history/{index,db}_test.go`, `recentlocations_test.go`,
palette spacer/scroll tests, recent-files tests rewritten for the repo db
+ migration; `newTestApp` pins `historyPathFn`. Menu pins → 164 rows /
height 170 / dividers `[2, 5, 167]`. Verified in the real binary with
`run-ced` on a scratch project (picker from the db; open a file + quit →
folder and file written back). `make test` green; `CGO_ENABLED=0` builds.

CLAUDE.md: architecture map lines, a new "Recent locations + per-repo
history" section, the `.ced/history.bytdb` note in What NOT to add, and
the menu pins.

## Next

Closed: None. Declined: None. Raised: N-027, N-028, N-029.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
