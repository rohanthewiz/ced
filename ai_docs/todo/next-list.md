# ced — next list

The living list of open follow-ups. Sessions edit this file IN PLACE
(`/sess-save`, `/next-list`); nothing is copied forward from one session
doc to the next. A session doc's `## Next` records only what that session
changed here, as `Closed: … Raised: …`.

Seeded 2026-09-21 by `/next-list seed` from the last 15 session docs
(`2026-0828-1713-chat-archive` … `2026-0921-0801-lsp-diagnostic-messages`)
plus the LSP work done in the seeding session itself
(`2026-0921-0906-lsp-experience`).

## Conventions

- **IDs are permanent and never reused.** `N-001`… Refer to items by ID.
- **`raised`** is the stem of the session doc the item FIRST appeared in,
  even when that is older than any window a rebuild looked through.
- **Age is computed, never stored**: the number of session docs since
  `raised`.
- **Value** is the payoff of doing it, not the effort:
  - `high` — something is being worked around today, or a second
    independent consumer has arrived
  - `medium` — it blocks one named thing, or it is a visible defect nobody
    has to route around yet
  - `low` — a gap nobody has bumped into, or contingent on something that
    does not exist
- **Open** is what we intend to pick up next. **Roadmap** is wanted but not
  soon — parked, not declined. **Non-goals** is what we are likely never to
  do, kept so it stays visibly declined.
- **Nothing leaves Open or Roadmap without a line in another section.**
  Done → Closed. Declined → Non-goals. Merged → Closed as `merged into N-xxx`.
- Open and Roadmap stay in **ID order**. Never renumber, never delete.

**Next ID: N-026**

## Open

- **N-001** · raised `2026-0910-2016-tool-windows` · value medium
  **Releases stop at v0.2.0.** Tags `v0.3.0` and `v0.3.2` exist but neither
  has a GitHub Release or artifacts, so `install.sh` still installs 0.2.0.
  `origin/release` is at `0409317 Release ced 0.2.0`, 184 commits behind
  `main`. The fork suppresses push triggers: fast-forward `release`, then
  `gh workflow run release.yml --repo rohanthewiz/ced --ref release`. It
  will be the first run without the `brews:` step. (Originally "v0.3.0 has
  no artifacts"; the `Formula/ced.rb` half died with the Homebrew removal,
  and the release-branch note from `2026-0914-1114-homebrew-removed` is
  folded in here.)

- **N-002** · raised `2026-0910-2016-tool-windows` · value low
  `toolLayoutSummary()` (toolwindow.go) has no reader outside tests.
  Verified still true 2026-09-21. Either give it one (a status-bar or ≡
  label saying where everything is) or delete it.

- **N-003** · raised `2026-0910-2016-tool-windows` · value medium
  **The README goes stale and no test catches it.** Known spots for
  tool-window changes: `### Tool windows`, the Features list, the chat
  section, the hotkey table. As of 2026-09-21 it is also silent on
  everything LSP added since: Select all, the multi-server registry (it
  still says only "gopls"), go to implementation / type definition,
  symbol-in-project, incoming calls, symbol highlight, inlay hints, the
  `"inlayhints"` key, the diagnostic tooltip, and the ≡ Code "Restart
  language server" row.

- **N-004** · raised `2026-0913-1919-cats-plugin` · value low
  `cats-plugin.toml`'s `version` is hand-maintained. It currently matches
  (`0.3.2`) and the host does not compare it, but release CI will not bump
  it, so it drifts on the next auto-bumped patch. Teach `release.yml` to
  rewrite it, or accept it as informational. (Premise corrected at seed:
  the item said "pinned at 0.3.0".)

- **N-005** · raised `2026-0913-1919-cats-plugin` · value low
  cats-side decision: should two ced launch paths share ONE sidebar group
  (map the shell-launched fallback id to the configured plugin id)? Rare in
  practice; left as-is.

- **N-006** · raised `2026-0913-1919-cats-plugin` · value low
  cats-side decision: should a BLOCKED editor count toward the AGENTS
  attention tally? Currently it does not.

- **N-007** · raised `2026-0913-2142-select-all` · value low
  **⌘A as a Cmd accelerator for Select all.** Legitimate under the
  metakeys.go rule now the verb has a ≡ Edit row to be a second door onto.
  Needs the rune table, `metaReserved()` and a pin test. Verified not done
  2026-09-21.

- **N-008** · raised `2026-0913-2142-select-all` · value low
  Optional: a "Select all" row in ≡ **File** too, if "File | Edit" meant
  both groups. Today it is in Edit only. One line plus the menu pins.

- **N-009** · raised `2026-0914-1114-homebrew-removed` · value low
  `.claude/commands/summary-of-downloads.md` says download counts include
  Homebrew installs. True up to v0.3.0, false for anything released after
  the tap was removed; tweak when N-001 ships a release.

- **N-010** · raised `2026-0921-0801-lsp-diagnostic-messages` · value medium
  Try the diagnostic pointer tooltip in a REAL terminal (plain tmux, cats,
  macOS Terminal.app): do motion events reach it, does 250ms feel right.
  Only exercised on the simulation screen so far. `run-ced` cannot send
  mouse motion; this needs a person or a capture-tool extension.

- **N-011** · raised `2026-0921-0801-lsp-diagnostic-messages` · value medium
  A click on the gutter diagnostic dot opens the tooltip immediately (today
  it moves the caret to column 0). The mouse path for terminals with no
  motion reporting — macOS Terminal.app is one.

- **N-012** · raised `2026-0921-0801-lsp-diagnostic-messages` · value low
  Echo the caret line's diagnostic in the status bar as the caret moves
  (vim/ALE style). Not done because the bar is width-budgeted; Esc-i
  already covers the keyboard path. The `lspProgressSuffix` segment added
  2026-09-21 is the shape it would take.

- **N-013** · raised `2026-0921-0906-lsp-experience` · value medium
  **No non-Go language server has been run.** typescript-language-server,
  rust-analyzer, pyright/basedpyright/pylsp, clangd and zls are registered
  from their documentation only (lspservers.go). Open a real project in
  each that is installed and confirm diagnostics, definition and
  completion. The TypeScript inlay-hint `preferences` in `initOptions` are
  the least certain part — typescript-language-server may want them via
  `workspace/didChangeConfiguration` instead.

- **N-015** · raised `2026-0921-0906-lsp-experience` · value low
  `lspLookPath` is not pinned in `newTestApp` although its doc comment
  implies it (tests rely on `a.lsp.dead = true` instead). A test that sets
  `dead = false` and opens a non-Go file would spawn whatever server the
  machine has. Pin it, with the real-gopls tests restoring it.

- **N-016** · raised `2026-0921-0906-lsp-experience` · value low
  Inlay hints for servers that take their hint settings through
  `workspace/configuration` rather than `initializationOptions` (pyright).
  The auto-responder answers every configuration request with `{}`, so
  those servers never switch hints on. Contingent on N-013 showing it
  matters.

## Roadmap

Wanted, but not next. Parked, not declined.

- **N-017** · raised `2026-0909-1845-tree-multi-select` · value low
  Tree marks: a cross-folder "select all matching" (tick every
  `*_test.go`). The finder answers the question differently today.

- **N-018** · raised `2026-0910-1826-clickable-overflow-markers` · value low
  Overflow markers on the surfaces that have none — compare panel, problems
  panel, chat, terminal. The click comes along for free via `scrollAt`.

- **N-019** · raised `2026-0828-1713-chat-archive` · value low
  Real chat RESUME via ACP `session/load`. Contingent on an agent
  advertising `loadSession` and on solving the replay-doubles-the-transcript
  problem; the archive deliberately does not store the agent session id.

## Non-goals

- **N-020** · declined `2026-0910-2016-tool-windows` — polishing a bottom-docked
  Explorer / narrow-docked git panels. Usable but cramped on purpose; the
  internal seams are tuned for a wide strip and nobody has asked.
- **N-021** · declined `2026-0910-2016-tool-windows` — making the Find-all list a tool
  window. Live preview, an Esc that restores the view, and a TOP dock no
  tool window has. (Related, from `2026-0910-1826-clickable-overflow-markers`:
  the UNPINNED list's markers have no popup; pinning restores it.)
- **N-022** · declined `2026-0913-2142-select-all` — a leader key for Select all. The
  flat Esc table is out of mnemonic letters.
- **N-023** · declined `2026-0909-1845-tree-multi-select` — persisted tree marks, and
  Rename as a set verb (a bulk rename needs a pattern language).
- **N-024** · declined `2026-0828-1713-chat-archive` — a delete row in the Recent
  chats picker. The retention cap and `rm` cover it; `chatstore.Remove`
  exists if it is ever wanted.
- **N-025** · declined `2026-0921-0906-lsp-experience` — semantic tokens (would fight
  the Chroma grid brace matching reads), LSP formatting (format.go covers
  it), code lens (needs virtual rows), folding (no fold model), and
  IN-LINE inlay hints (linenote.go's header has the bill).

## Closed

Newest first. Closures before 2026-09-21 live in the session docs.

- closed 2026-09-21 — **N-014** "Restart language server" ≡ Code row.
  `internal/app/lsprestart.go`; acts on the active file's server, never
  dimmed, names the missing binaries. Needed a slot generation
  (`lspServer.gen`) so the replaced process's late exit event cannot kill
  its successor. The spawn path itself is untested (it would start a real
  server); try it once by hand: `kill` gopls, then use the row.
- closed 2026-09-21 — **"a `bin` entry in ced's manifest" (was a Non-goal
  in `2026-0913-1919-cats-plugin`) — OVERTURNED, not done.** The reason
  was that it would shadow the Homebrew `ced`; the Homebrew removal
  (`2026-0914-1114-homebrew-removed`) reversed that, and
  `cats-plugin.toml` now has `bin = ["./bin/ced"]` as the official install.
- closed 2026-09-21 — commit and push the cats changes (`catway.go`,
  `wire/down.go`, `pluginpane_test.go`). `~/projs/go/cats` is clean and
  `50e06e3 agents: an editor is a tool row, not an agent row` holds them.
- closed 2026-09-21 — rebuild and restart catway for the editor
  reclassification. Not verifiable from here (a running process); closed on
  the grounds that any catway built since `50e06e3` includes it. Reopen if
  ced still shows among the coding agents.
- closed 2026-09-21 — `catctl plugin install rohanthewiz/ced` from GitHub.
  Its only blocker was the manifest reaching `main`; `cats-plugin.toml` is
  on `origin/main`.
