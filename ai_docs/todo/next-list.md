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
- **Validate** sits directly below Open: items whose remaining work is purely
  testing (a hand check in a real terminal or app, a run on real data or
  hardware, or a test to write or repair), with no product change planned. A
  check that finds a defect raises it as a new Open item; an Open item whose
  fix has landed but is unchecked moves to Validate.
- **Nothing leaves Open, Validate or Roadmap without a line in another
  section.** Done → Closed. Declined → Non-goals. Merged → Closed as
  `merged into N-xxx`. Moving among Open, Validate and Roadmap is fine.
- Open, Validate and Roadmap stay in **ID order**. Never renumber, never delete.

**Next ID: N-140**

## Open

- **N-069** · raised `2026-1016-1651-postgres-sql-formatting` · value low
  ced's in-process SQL formatter (sqlfmt.go) deliberately does not wrap
  long lines and does not look inside `$$` PL/pgSQL bodies (a second
  language, a string to the SQL grammar). A long DDL line or a messy
  function body stays as written. Revisit only if someone hits it; any
  change must keep the re-lex token check (`sameSQLTokens`).

- **N-081** · raised `2026-1021-2049-editor-context-rows-change-view` · value low
  The editor right-click menu carries six LSP verbs; "Go to
  implementation", "Go to type definition" and "Highlight symbol uses"
  aim at the symbol under the caret just the same but are ≡ Code rows
  only. Suggested this session alongside the rows that shipped; not
  picked. If they go in, fold the git rows (Show change, Blame, Git
  status, New branch) into one "Git…" picker row to keep the popup
  short.

- **N-095** · raised `2026-1026-1545-find-click-to-line-esc-back` · value low
  The find bar's Esc now goes back to where the bar opened, so there is
  no KEYBOARD way to close the bar and stay on a hit: the doors are a
  click on the hit's line (an editor press while the bar is up forgets
  the origin, `App.findFrom`) or `↓` into the Find-all list and Enter
  there. ced is mouse-first, so this may never bite; if it does, add a
  bar chord (Alt+Enter, say) that closes and keeps the caret, plus its
  hint word.

- **N-101** · raised `2026-1028-1614-n016-python-inlay-hints-premise` · value medium
  **typescript-language-server is dead over TypeScript 7, silently.**
  npm's `typescript@latest` is 7.0.2 (the native port), which ships no
  `lib/tsserver.js`. typescript-language-server 5.3.0 then reports
  TypeScript "1.0.0, bundled" and answers an inlay request with no hints
  plus an error `window/showMessage` ("Inlay Hints request failed.
  Requires TypeScript 4.4+."). In the real binary, a `.ts` file with a
  type error got NO diagnostic, no hints and no flash, where the same
  file over TS 5.9 shows both. That's silent degradation working, but it
  means a fresh `npm i -g typescript-language-server typescript` install
  gives no TypeScript features. Decide: register TypeScript 7's own
  language server, document "needs TS ≤ 5.x", or both.
  2026-11-04 (`2026-1104-1442-n102-n093-return-hints-modal-replaced`):
  the command line is found. TypeScript 7.0.2's own `tsc` IS a language
  server: `tsc --lsp --stdio` (npm `typescript@7`, a native
  `typescript-darwin-arm64` binary behind `bin/tsc`) answers
  `initialize`, and validates strictly — it refuses a request missing
  `processId` / `rootUri` / `capabilities`, which ced sends. TS 5.x's
  `tsc` has no `--lsp`, so registering it means telling the two apart
  (a `tsc --version` probe, or trying it after typescript-language-server
  reports TypeScript "1.0.0, bundled") before spawning. Its inlay-hint
  and diagnostics support is unchecked.
  2026-11-06 (`2026-1106-1114-n110-n101-alt-chords-tsc-lsp`): **checked,
  and the two routes are now concrete.** Probed with ced's own handshake
  (a scratchpad script: ced's capability set, null
  `workspace/configuration` answers, full-text didChange).
  `tsc --lsp --stdio` (typescript-go 7.0.2) initializes and answers
  hover in markdown, but **its diagnostics are PULL-only**: one EMPTY
  publishDiagnostics on open, nothing after an edit or a save, while
  `textDocument/diagnostic` answers (3 items, 5 after an edit). ced is
  push-only, so it would show no diagnostics. **Its inlay hints ignore
  `initializationOptions.preferences`** (0 hints). VS Code-shaped
  settings (`typescript.inlayHints.parameterNames.enabled: "literals"`,
  `variableTypes.enabled`, `functionLikeReturnTypes.enabled`) give 6,
  pushed by `workspace/didChangeConfiguration` (which it registers for)
  or answered to `workspace/configuration` (sections js/ts, typescript,
  javascript, editor). Separately, **typescript-language-server 6.0.3**
  (2026-11-02) now refuses TS7 at `initialize` with its own reason ("The
  TypeScript of the workspace (TypeScript 7.0.2 …) provides no
  tsserver.js. No other valid TypeScript installation was found"). Since
  N-112 the ≡ Code "Restart language server" row shows that reason in
  full; opening a file still fails silently. **tsls 6.0.3
  over `typescript@6` (6.0.3) works with ced unchanged**: in the probe
  and in the real binary (run-ced) it gave `✗ Type 'string' is not
  assignable…`, the gutter ●, and `» name: "hi" · times: 3` notes. The
  choice is now:
  (1) document `npm i -g typescript-language-server typescript@6` in
  the README's server table (cheap, works today; `typescript@latest` is
  7);
  (2) register `tsc --lsp --stdio`, which needs three pieces: pull
  diagnostics (ask `textDocument/diagnostic` after each sync and feed
  the same diags path), a per-server settings push after `initialized`,
  and a `tsc --version` check (5.x/6.x `tsc` has no `--lsp`), ordered
  ahead of tsls for TS7;
  (3) both, with (1) now and (2) later.
  2026-11-06 (`2026-1106-1133-n101-typescript6-docs`): **the user chose
  (3).** (1) landed: the README's server table is followed by "install
  it with TypeScript 6" (`npm install -g typescript-language-server
  typescript@6`), why (no `tsserver.js` in 7), and the fallback. tsls
  6.0.3 uses a TypeScript 6 installed BESIDE it when the project's own
  `node_modules` holds 7 ("Using Typescript version (bundled) 6.0.3"),
  seen in the probe and in the real binary (✗ diagnostic, `» name: "hi"
  · times: 3`). lspservers.go's TypeScript entry carries the same note.
  **What remains is (2), decided and ready to build:** register `tsc
  --lsp --stdio` ahead of tsls for TypeScript 7, with pull diagnostics
  (`textDocument/diagnostic` after each sync, into the same diags path),
  a per-server VS Code-shaped settings push after `initialized`
  (`typescript.inlayHints.*` via `workspace/didChangeConfiguration`), and
  a `tsc --version` check so 5.x/6.x `tsc` (no `--lsp`) is never spawned
  as a server. When it lands, the README's TypeScript note changes with
  it.

- **N-103** · raised `2026-1028-1614-n016-python-inlay-hints-premise` · value low
  **pyright is tried before basedpyright, so a machine with both gets no
  Python hints.** `lspservers.go` orders the Python commands "most
  capable first". basedpyright is the more capable one: inlay hints,
  which pyright lacks. But its default `recommended` mode is stricter
  (`⚠ Return type is unknown (+4 more)` on a 12-line file with no
  config). Both read `[tool.pyright]` / `pyrightconfig.json`, so a
  configured project behaves the same either way. Note basedpyright's
  npm package also installs a `pyright-langserver` alias, so an
  npm-only machine already runs basedpyright first. Decide whether to
  swap the order.

- **N-113** · raised `2026-1110-1630-reliability-performance-usability-audit` · value high
  **Undo can leave a tab "clean" while the disk holds different text.**
  Undo, Redo and Revert set `t.Dirty = t.CanRevert()` (undo.go:335,
  :357, :375), and `CanRevert` compares against `undoOriginal`, the
  ON-OPEN snapshot, which `Save` never updates. Type, let auto-save
  write (or save), undo back to the opened text: the tab reads clean,
  the disk keeps the undone text, and `menuQuit` quits with no prompt.
  ≡ "Revert file" does the same. The reverse also holds: undo back to
  the SAVED text reads dirty. Reproduced by a test on a scratch copy
  (after auto-save + undo: buffer `original`, disk `original
  EXPERIMENT`, dirty=false). Fix: keep a last-saved baseline (a header
  copy set in NewTab / Reload / Save / ReloadAsEdit) and set Dirty from
  buffer != baseline in all three.

- **N-114** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **Editing a non-UTF-8 file corrupts it silently.** `internal/editor`
  never asks `utf8.Valid`. The buffer primitives go through `[]rune`
  (buffer.go:108-114, :148-151), so any edit to a line rewrites every
  invalid byte on THAT line as U+FFFD, and auto-save persists it 5s
  later. Reproduced: Latin-1 `caf\xe9 one` + one typed rune saves as
  `caf\xef\xbf\xbd oneX`; Shift-JIS and BOM-less UTF-16 without a NUL in
  the first 8KB the same. Untouched lines and a no-edit save round-trip
  byte for byte. Decide the policy: (1) stopgap, open such a file with
  auto-save off and a held warning (or read-only); (2) decode Latin-1 /
  CP1252 on open and re-encode on save. Suggested: (1) first.

- **N-115** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **A full disk empties the file being saved.** `WriteFileAtomic` falls
  back to a truncating `os.WriteFile` on ANY `CreateTemp` error
  (fileio.go:209-212), against its own header comment ("a crash or a
  full disk can never leave a half-written file"). Reproduced on a 1MB
  volume filled to 100%: 28000 bytes before, 0 after; with auto-save it
  hits every dirty tab. The buffer survives and the tab stays dirty.
  The temp-file path itself is solid (original intact, temp removed).
  Fix: fall back only on EACCES / EPERM / EROFS, and in the fallback
  write without O_TRUNC, truncating to length after a good write.

- **N-116** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **Format-on-save race: a stale formatter result replaces newer saved
  text.** `handleFormatDone` (format.go:666-692) matches by path and
  calls `ReloadAsEdit` whenever the tab is clean, with no generation or
  `EditRev` check, and `execFormatterChain` starts a second run while
  one is in flight. Save, keep typing, save again before formatter #1
  finishes: #1's output lands on disk and in the buffer and the text
  typed between the saves is gone (still in undo, but the swap is
  silent). Reproduced with a 400ms read-then-write stand-in formatter.
  Breaks the "generation-check async results" rule. Fix: carry the
  tab's `EditRev` at save on `formatDoneEvent` and skip adoption if it
  moved; one run per path, with one queued rerun.

- **N-117** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **An external change whose mtime is not NEWER is invisible, then
  overwritten.** Reconcile (reconcile.go:152), `diskNewerThanTab`
  (:464) and the auto-save guard (autosave.go:282) only test
  `ModTime().After(tab.Mtime)`; no size, inode or hash. `cp -p`,
  `rsync -t`, `tar x`, a restored backup, or a write inside the same
  timestamp granule (NFS, some sshfs) is never seen. Reproduced: same
  mtime, external content ignored by reconcile, next save `ok=true`
  with no modal; an mtime 48h older, auto-save wrote over it. Also
  `NewTab` stats AFTER the read (tab.go:296-305). Fix: record (mtime,
  size, inode) at load and save, treat ANY difference as changed, stat
  before the read; hash-compare on mismatch to avoid false conflicts.

- **N-118** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **No crash safety.** No `recover()` and no `os/signal` anywhere in
  non-test code; `main.go` calls `a.Close()` only after `Run` returns.
  A panic (main loop or any of ~74 `go func` sites), SIGTERM, or SIGHUP
  from a killed tmux pane skips `Close`: unsaved edits, the tab list
  (`recordSession`) and history (`writeHistory`) are lost, and a panic
  or SIGTERM leaves the terminal in raw mode / alt screen / mouse 1003
  / kitty keyboard with the trace printed into the alt screen.
  Auto-save is a pure debounce (autosave.go:107-127 re-arms on every
  edit, :147-150 while a modal or menu is up), so steady typing is
  never saved. Read, not run. Four parts: (a) recover in `Run` plus a
  `safeGo` wrapper for goroutines: write dirty buffers
  (`EncodedBytes`) to a recovery folder, `screen.Fini()`, re-panic;
  (b) `signal.Notify(SIGHUP, SIGTERM)` posting a quit event through the
  normal `Close` path; (c) a max wait on auto-save (force a save after
  ~30s of continuous dirtiness); (d) `recordSession` on tab open and
  close. Decide where recovery files live: `<repo>/.ced/recover/` is
  inside an existing dotdir; outside a repository it needs a home
  under `~/.config/ced/`, which the "no new dotfiles" rule must allow.

- **N-119** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **state.json is overwritten wholesale.** `recordSession`
  (folder.go:146-147) and the startup touch (`loadSessionStore`,
  folder.go:72-85) dump the store loaded at STARTUP. Two instances: the
  last to quit erases the other's entry (reproduced: after B quits its
  entry is there with 1 tab; after A quits it is gone). A parse error
  flashes and is then saved over, so one stray comma wipes every
  recent folder and layout (reproduced: three folders → one). The temp
  name is a fixed `path + ".tmp"` shared by concurrent writers
  (session.go:238-242). `saveToolLayout` already re-reads before
  writing; config.json's `saveKey` is fine. Fix: re-`Load` and upsert
  only this root's entry, rename an unparseable file to
  `state.json.bad` rather than saving over it, `CreateTemp` for the
  temp name.

- **N-120** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **One bad value in config.json resets EVERY setting to defaults.**
  `Load` returns `Defaults()` on a parse error and on any single bad
  value (userconfig.go:666-669, :680-684, :695-699); the app only
  flashes. Reproduced: `{"autosave": false, "chatwrite": "off"}` loads
  as AutoSave=true ChatWrite=true (a JSON boolean is "bad"), and so
  does a valid file with one unrelated typo'd value. Auto-save, agent
  file writes and plugins silently come back on. Fix: apply keys one
  by one and keep the valid ones, accept JSON booleans, hold the error
  for a ≡ label per the startup-errors rule.

- **N-121** · raised `2026-1110-1630-reliability-performance-usability-audit` · value low
  **Smaller data-safety fixes, each contained.** Close a part by
  striking it here; close the item when all are done. All reproduced
  on a scratch copy unless marked.
  (a) Every save replaces the inode: group, xattrs, ACLs, hard links
  and setgid are lost, a 0444 file in a writable dir is overwritten
  silently (auto-save too), and the directory is not fsynced
  (fileio.go:203-236). Fix: `fchown` the temp and copy the full mode;
  write in place when nlink > 1 or ownership can't be kept; refuse or
  confirm when the target lacks owner write.
  (b) Undo onto a state gofmt would change, then one idle auto-save:
  `ReloadAsEdit`'s `pushUndo` clears the redo stack (tab.go:586,
  undo.go:244). Fix: skip auto-save (or the quiet formatter) while
  `CanRedo()`.
  (c) A clean tab whose file vanishes and returns (branch switch away
  and back) was marked Dirty by DiskGone (reconcile.go:128-136), so it
  comes back as a false "unsaved edits" conflict holding stale text,
  and "Keep mine" overwrites the new content. Fix: remember whether it
  was dirty before DiskGone; if clean, `ReloadUndoable`.
  (d) The chat agent's fs write is a plain `os.WriteFile(…, 0o644)`
  and an open CRLF / BOM tab is served to it as LF without the BOM
  (copilot_chat_perm.go:584-587, :614-620), so the write-back converts
  the file. Fix: re-apply the tab's LineEnding / BOM and write through
  `editor.WriteFileAtomic`.
  (e) A failed paste runs `os.RemoveAll(p.dest)` even when the failure
  was "dest already exists" (copypaste.go:522-531). Fix: remove only
  what ced's own Mkdir / O_EXCL create made.
  (f) The dirty-close dialog captures a tab INDEX (app.go:5013-5023);
  a tab closing to its left under the dialog (the cats Move-to-split
  answer) makes Discard close a different tab. Fix: capture
  `*editor.Tab` and resolve with `tabIndexOf`, as tabcontext.go does.
  (g) Deleting a folder closes open dirty tabs inside it without a
  prompt, and the confirm does not mention them (fileops.go:218-230;
  read, not run).
  (h) A minority-CRLF file keeps `\r` in the buffer as a character;
  typing at end of line writes text after the CR (fileio.go:108-115).

- **N-122** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **Git children share ced's controlling terminal.** `runGitCmdFull`
  (gitcmd.go:136-138) is `exec.Command` + `CombinedOutput` with the
  inherited env: no Setsid, no `GIT_TERMINAL_PROMPT`, no timeout, no
  handle kept. Only `ls-remote` sets `GIT_TERMINAL_PROMPT=0`
  (gitpush.go:432; its comment "nothing is attached to a terminal it
  could prompt on" is wrong). Reproduced under a pty against a local
  401 server: `Username for …:` is written to the tty over the frame,
  git waits forever, keys are split between tcell and git, and after
  killing git its `git remote-http` helper survives holding the pipe,
  so `CombinedOutput` never returns. No cancel path. Suspected the
  same way, not run: an ssh passphrase / unknown host key, a signed
  commit with pinentry-curses, custom actions running `scp` / `ssh`
  (app.go:5427). Also: no read-only git call sets
  `GIT_OPTIONAL_LOCKS=0`, so the 10s `git status` can take
  `index.lock` under a git command the user runs in another pane
  (absence verified by grep, the collision not reproduced). Fix: ONE
  constructor for every launcher (`runGitCmdFull`, `runGitCmdSeqHook`,
  `runGitApplyPatch`, `fetchGitPushHeads`, `execCustomAction`):
  `Setsid` (as `hostRun` does), `GIT_TERMINAL_PROMPT=0`, `GPG_TTY`
  dropped, `CommandContext` with a process-group kill and `WaitDelay`,
  `GIT_OPTIONAL_LOCKS=0` on reads. A refusal then lands in the
  existing "Git failed" modal.

- **N-123** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **A wedged language server freezes the editor.** `lspFlushChange`
  sends the whole document on the MAIN LOOP (lsp.go:580
  `client.DidChange(…, t.Buffer.String())`, didOpen at :471), and
  `Client.send` is a blocking pipe write with no deadline
  (client.go:332-349). A server that is alive but not reading (a
  deadlock, swap, SIGSTOP) fills the pipe within a few edits and the
  loop never returns; `Close()` writes `exit` through the same `send`
  before its 2s Kill fallback, so quit and ≡ "Restart language server"
  block too. Reproduced with `sleep 8` as the server: `DidChange(300kB)`
  and `Close()` both still blocked after 2s. Fix: a writer goroutine
  per client fed by a bounded queue; `Notify` enqueues without
  blocking; overflow or a write past N seconds marks the server dead
  and kills it; `Close` kills after its grace period without writing
  first.

- **N-124** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **Dropped `PostEvent`s leave permanent stuck state.** tcell's
  `PostEvent` is non-blocking and DROPS when its 256-slot queue is
  full, while tcell's own input enqueue blocks, so input arriving
  faster than the loop drains pins the queue. 107 of 110 call sites
  write `_ = scr.PostEvent(…)` (handled: terminal.go:223,
  lspcodeaction.go:372, remote.go:169). Drops that stick:
  `formatDoneEvent` and Restore's `gitCmdDoneEvent` leave
  `formatRuns[path]` up (reconcile off for that path until restart);
  `termDoneEvent` leaves `term.running` (terminal "busy" for the
  session); `chatTurnDoneEvent` leaves `chat.turnActive`;
  `chatPermRequestEvent` leaves its goroutine waiting up to 15 min;
  the four `…ReadyEvent`s leave `starting` set and leak the child;
  `autoSaveEvent`, `validateEvent`, `pluginEditEvent` leave their
  one-shot timers unarmed until the next edit. The mechanism and the
  per-event cost are verified; no collision was reproduced end to end.
  What fills the queue: a paste (N-125) or a main-loop stall (N-127)
  while the pointer moves (all-motion mouse is on). Fix: a
  `postResult` helper for COMPLETION events that retries until
  delivered or the App is closed (every such site is on a goroutine or
  an AfterFunc); tickers may keep dropping.

- **N-125** · raised `2026-1110-1630-reliability-performance-usability-audit` · value high
  **A paste draws one full frame per pasted character.** A bracketed
  paste arrives as one EventKey per rune; `handleKey` only appends to
  `pasteBuf`, but `Run` still runs `draw()` + `Show()` for each
  (`deferFrame` defers only wheel / motion events). Measured by
  replaying Run's loop body on a simulation screen at 200×60: 5250
  runes (125 lines) took 9.0s, 1.72 ms per rune, every frame
  identical. It is also the easiest way to pin the event queue
  (N-124). Fix: skip the frame while `a.pasting`.

- **N-126** · raised `2026-1110-1630-reliability-performance-usability-audit` · value low
  **Missing bounds on children and wire input.** Close a part by
  striking it here. Read, not run, unless marked.
  (a) Formatters run `exec.Command` + `CombinedOutput` with no timeout
  (format.go:508-510); a hang leaves `formatRuns[path]` up for good.
  Fix: `CommandContext` with a 30–60s cap and `WaitDelay`.
  (b) A plugin's 30s timeout kills only `sh` (plugincmd.go:120-128: no
  Setpgid, no WaitDelay, and a grandchild holding the pipe keeps
  `Run()` waiting); edit-triggered runs have no in-flight check
  (plugindeco.go:134, :157). Fix: Setpgid + group kill + WaitDelay,
  skip a provider already running.
  (c) `Close` kills no in-flight git / formatter / plugin /
  custom-action child and not the terminal's foreground job; a server
  still handshaking at a folder switch leaks, since its client exists
  only inside the ready event (lsp.go:328, :341); `Client.Close`
  returns early without `Wait` when the read loop already closed
  (client.go:296), one zombie per server that exits on its own.
  (d) An uncapped `Content-Length` reaches `make([]byte, contentLen)`
  (client.go:532-535) and panics the read goroutine. REPRODUCED
  (`makeslice: len out of range`). Fix: reject above a cap (64MB).
  (e) One non-JSON stdout line ends an ACP / MCP connection
  (acp.go:90-91), a common MCP stdio wrapper bug; `ReadBytes('\n')`
  is unbounded. Fix: skip unparsable lines, cap the line length.
  (f) `handleEvent` has no case for `*tcell.EventError`, so a dead
  tty that does not SIGHUP ced leaves it polling git forever
  (suspected). Fix: treat it as quit.

- **N-127** · raised `2026-1110-1630-reliability-performance-usability-audit` · value high
  **The git status snapshot runs synchronously on the main loop, every
  10 seconds and after every save.** `refreshGitStatus` →
  `loadGitStatus` is six forks in a row (gitstatus.go:113, :122, :197,
  :206, :315, :333), called from the tree tick (`refreshTreeNow`),
  `handleAutoSave` and ~33 sites in all. Measured on this repo, this
  machine: 190–265 ms per call, so the UI stalls that long every 10s;
  `app.New` takes 230–360 ms, mostly this (the rest of startup is
  ~30–50 ms). The same tick also forks one `git diff` per open tab
  (`requestOpenTabDiffs`, ~38 ms each, off-loop) and a `git ls-files`
  (the finder rebuild), every 10s forever, focused or not, against
  the "standing timers only while something needs them" rule. Fix:
  (a) run the snapshot on one coalesced goroutine (in-flight flag +
  seq) and post a `gitStatusEvent`; (b) fold the six forks into one
  `git status --porcelain=v2 --branch --show-stash`; (c) reconcile on
  focus-IN and stop polling while unfocused (terminals that report no
  focus keep the tick); (d) skip the per-tab diff when neither the
  file's mtime nor HEAD / index moved. Lands well with N-122's
  constructor (timeout, `GIT_OPTIONAL_LOCKS=0`).

- **N-128** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **Every structural edit re-lexes the whole file on the main loop.**
  Enter, a multi-line paste, undo and line ops call
  `InvalidateStyles`, and the next Render runs Chroma over the whole
  buffer. Measured: ~0.5 ms per KB, so on `internal/app/app.go` (261KB)
  Enter + Render is 129 ms and Undo + Render 151 ms, with 59MB of
  garbage each; typing (the deferred path) is 1.2 ms. Fix: (a) patch
  the grid for line inserts / deletes (splice rows) and DEFER like an
  intra-line edit; (b) run the settle re-lex on a goroutine keyed by
  `EditRev`, posting the grid back, so the loop never blocks on
  Chroma. With N-129, `MaxHighlightBytes` (512KB) can be revisited.

- **N-129** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **The style grid stores a 72-byte `tcell.Style` per character.**
  Measured: `app.go`'s 261KB of text keeps a 19.1MB grid resident (75×
  the text) holding 9 distinct styles; each lex allocates 59MB. Twenty
  ordinary open tabs hold tens of MB of grid. Fix: a one-byte style
  CLASS per rune plus a palette table resolved at paint time. A theme
  switch then swaps the palette without a re-lex (`restyleTabs`), and
  brace matching can ask "is this class string / comment" instead of
  comparing colours (the degradation when a theme's syn-string == fg
  goes away).

- **N-130** · raised `2026-1110-1630-reliability-performance-usability-audit` · value low
  **Render converts whole lines whatever the viewport shows.**
  `runes := []rune(line)` and `rowStyles := make([]tcell.Style,
  len(runes))` per visible line, per frame (tab.go:1196-1213).
  Measured at 200×60 on a single-line file: 1MB → 14 ms and 84MB of
  garbage per frame; 5MB → 190 ms and 420MB per frame (every frame,
  mouse motion included), so a minified bundle under the 32MB open cap
  is unusable. Ordinary files are fine (1.2 ms a frame). Fix: walk to
  `ScrollX` without allocating (tab stops still anchor at column 0)
  and build runes / styles for the visible window only; the wrapped
  layout needs the same treatment.

- **N-131** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **Undo depth collapses on big files and depends on WHERE you edit.**
  Each step is a whole-buffer header copy (16 bytes × lines) charged
  to the 32MB budget, and `snapshotCost` compares index by index, so a
  line insert or delete near the top charges every shifted line's
  bytes. Measured, structural edits: 50k lines → 41 steps survive,
  200k → 10, 400k → 5; on a 6,000-line, 437KB file of distinct lines
  (the size of app.go) 61 steps survive near the top against 330 near
  the end. The trim is silent. Fix: delta undo records (line range,
  removed lines, inserted lines), so memory and accounting are both
  O(change); `pushUndoEntry` stays the single write path.

- **N-132** · raised `2026-1110-1630-reliability-performance-usability-audit` · value low
  **Find allocates per line per keystroke.** `SetFindQuery("gamma")`
  measured 6 ms at 10k lines, 49 ms / 79MB at 100k, 181 ms / 317MB at
  400k, and the find bar and the live Find-all prompt re-scan on every
  keystroke. Fix: an ASCII fast path on bytes (`strings.Index`),
  converting to rune columns only on a hit. Only matters on very
  large files.

- **N-133** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **`ced path/to/file.go` roots the editor at the file's folder.**
  `resolveTarget` (main.go:139) returns the parent dir as the root.
  Inside a repository that then (a) writes `<subfolder>/.ced/`
  (history.bytdb, its lock, a .gitignore covering only the database),
  which `git status` shows as `?? internal/app/.ced/` (seen in a
  scratch clone); (b) adds the subfolder to Recent folders in
  state.json; (c) narrows the tree, finder, project search, git and
  the LSP rootUri to that folder. Fix: root at the nearest ancestor
  holding `.git` (`projectRootFor` already exists) and reveal + open
  the file; no repository → the parent dir as today. A change to
  documented behaviour (README Usage, `helpText`): decide.

- **N-134** · raised `2026-1110-1630-reliability-performance-usability-audit` · value low
  **Flashed messages vanish.** `flash` is one slot shown for 3s
  (`statusFlashFor`), overwritten by the next of 647 call sites, and
  many carry the only explanation of a refusal ("an unavailable row
  explains itself"). There is no history. Add a small message log
  (the last N flashes with their time) behind a ≡ View row, opened
  through `openPicker`; consider holding error-class flashes longer.

- **N-135** · raised `2026-1110-1630-reliability-performance-usability-audit` · value low
  **No in-app door to config.json or a key reference.** config.json
  has 25 hand-edited keys and no ≡ row opens it; the which-key band
  exists only behind `Esc ?`; the README (2958 lines) is the only
  reference. Add ≡ rows "Open config.json" (create it with `{}` when
  missing, then plain `openFile`) and "Keyboard reference" (the
  which-key band). Each new row moves the README and menu-layout pins.

- **N-136** · raised `2026-1110-1630-reliability-performance-usability-audit` · value low
  **Limits that bite are silent.** A non-UTF-8 file (N-114) and a
  trimmed undo history (N-131) change what the editor will do for you
  and say nothing. Give each a status-bar segment with a `tip`, and
  flash once when an undo trim first drops a step. Waits on the
  decisions in N-114 and N-131.

## Validate

Items whose remaining work is purely testing: hand checks in a real terminal
or app, runs on real data or hardware, and tests to write or repair. No
product change is planned unless a check finds a defect, which is then raised
as a new Open item. Split out of Open on 2026-10-07; each item kept its ID
and `raised`.

- **N-010** · raised `2026-0921-0801-lsp-diagnostic-messages` · value low
  Try the diagnostic pointer tooltip in a REAL terminal (plain tmux, cats,
  macOS Terminal.app): do motion events reach it, does 250ms feel right.
  `run-ced` CAN send motion (SGR button 35, `{esc}[<35{semi}X{semi}YM`)
  and the tooltip opens on the underline in the real binary
  (2026-09-30); what stays is a person's hand check. Value lowered from
  medium: the caret-line note (`diagnote.go`) now answers in a terminal
  with no motion at all, so the tooltip is no longer the only mouse door.

- **N-013** · raised `2026-0921-0906-lsp-experience` · value medium
  **Most non-Go language servers have never been run.** clangd WAS, on
  2026-09-21 through the real binary (`run-ced`, a three-file C project):
  diagnostics, go to definition (into the header), hover, completion and
  inlay notes (`» n: 3 · by: 4`, on by default) all work. Still
  registered from documentation only: typescript-language-server,
  rust-analyzer, pyright/basedpyright/pylsp and zls — none RUNS on the
  dev machine. (2026-10-08: `~/.cargo/bin/rust-analyzer` exists but is
  rustup's proxy with the component missing — it exits "Unknown binary
  'rust-analyzer' in official toolchain". `rustup component add
  rust-analyzer` would make it the cheapest server to check next. Until
  then ced's look-up finds it and the server dies on start, which should
  degrade silently; worth confirming on the way.) 2026-10-11
  (`2026-1011-2210-validate-n059-n056-n060-n061-n013-real-binary`): the proxy is GONE from the dev machine (no
  `~/.cargo/bin/rust-analyzer`; `rust-analyzer` not on PATH), so the
  dying-server case was run with a stand-in on PATH that prints rustup's
  "Unknown binary" error and exits 1: opening a .rs file in the real
  binary shows no modal, no flash, nothing in the status bar; ≡ Code
  offers "Restart language server (rust-analyzer)" as the retry; `Esc d`
  does nothing at all (menuGoToDefinition returns early on
  `!hasLSPActions()` — the same as with no server, by the
  silent-degradation rule). 2026-10-28 (`2026-1028-1614-n016-python-inlay-hints-premise`):
  pyright 1.1.414, basedpyright 1.40.2, pylsp 1.15.0 and
  typescript-language-server 5.3.0 (npm/uv installs in a scratch dir)
  RAN, through a protocol probe and in the real binary. basedpyright in
  ced: inlay notes (`» x: LiteralString · name="hi" · times=3`) and
  diagnostics. pyright and pylsp have no inlay hints at all. TypeScript
  over TS 5.9 in ced: diagnostics (`✗ Type 'string' is not assignable…`)
  and inlay notes. The `initOptions.preferences` doubt is settled: they
  ARE the route (5 hints with, 0 without; no didChangeConfiguration
  needed). Over TypeScript 7 it is dead (N-101). The rustup stub is BACK
  at `~/.cargo/bin/rust-analyzer`, still without the component, so
  rust-analyzer and zls remain unrun.

- **N-026** · raised `2026-0921-0932-next-list-run` · value low
  cats-side: `"KeyA"` is now in `CMD_TO_PANE` (cats `bd18905`,
  `cmd/catway/web/js/20-keys.js`; the cost to a shell was checked first
  and is nil: a legacy pane fails the kitty gate). What's left is a HAND
  CHECK that ⌘A actually reaches a ced pane in Chrome and in the cats mac
  app, the lesson ⌘E taught (`ed4962c`; Chrome resolved its ⌘E as a menu
  item and never dispatched it). Both are expected to deliver it; close
  on confirmation.

- **N-038** · raised `2026-0930-2002-tab-menu-file-verbs` · value low
  Tab menu "Move to split →" / "Copy to split →" have only run against
  the test's fake control socket (`withCtlSpy`). The real binary drew the
  rows inside cats, but no hand check has clicked one and watched the
  pane appear and the tab close. Also open: whether a `↓` pair is
  wanted. It was left out to keep the menu at 15 rows (≡ Cats still has
  "Open in split ↓").

- **N-040** · raised `2026-0930-2025-tab-groups` · value low
  Tab groups have never been checked by hand in a real terminal. The
  run-ced emulator draws no underlines, so the member underline (plain,
  `tabbar.go`) has only been seen in tests. Look at it in tmux, cats and
  macOS Terminal.app, and check that the chip reads well on a light
  theme (`tabGroupChipFG` picks BG or Text by contrast).

- **N-047** · raised `2026-1003-2042-cherry-pick-and-conflicts-ui` · value low
  The conflict UI has only been driven through `run-ced`'s emulator. Hand
  check in tmux, cats and macOS Terminal.app: the side washes' contrast
  (light themes especially), a real mouse click on the `<<<<<<<` lens,
  and the cats "blocked" badge firing on a stop (`conflictPanel.unseen`).
  Since `2026-1017-2208-conflict-side-names` also: the side names
  (conflictsides.go) on a real `git rebase` stop — "yours" on the lower
  side, the `=======` label's ▲/▼ colours readable on the grey wash
  (pinned at 4.5:1 in tests; drawn correctly by the run-ced PTY capture
  on a real rebase stop, but never seen in tmux, cats or Terminal.app),
  and a conflicted `stash pop` keeping the plain current / incoming words.

- **N-057** · raised `2026-1008-1724-tree-open-finder-terminal-shell-cmd` · value low
  The new tree rows' host branches have only run behind the `hostRun`
  seam. Hand check: "Open in Finder" really reveals a file (`open -R`)
  and opens a folder; "Open in terminal" with `"terminal": "tmux"` (or
  auto) inside real tmux splits a pane in the folder (no tmux on the dev
  machine); a custom command (`open -a Ghostty {{DIR}}`) opens there.
  The cats branch and ced's panel branch DID run in the real binary.

- **N-065** · raised `2026-1014-1518-host-theme-tree-colors` · value low
  The host theme's tree corrections (`catsHostTreeColors`: a panel lighter
  than bg is dropped for the derived darker sidebar, folders = fg) have
  only run in tests. run-ced has no cats host, so the synthesized theme
  never appears there. Hand check inside cats on cats-green and one theme
  that keeps its panel (Tokyo Night): does the darker tree/tab strip sit
  well against cats' own sidebar, and do the folder/file tiers read.
  Since `2026-1028-1801-tab-bar-red-tint` the tab strip is no longer the
  tree's surface: it wears `tabbar-bg` (sidebar-bg + 8% err), derived
  from the host's panel and err too. Check that red cast as well.

- **N-066** · raised `2026-1015-1640-tree-recent-folders-star` · value low
  The tree's recent-folders star has only been seen through run-ced's
  emulator, which doesn't measure glyph width. It went `★` (muted, too
  faint) → emoji `⭐` (two cells, too big) → `★` bold gold (a little
  small) → `⭑` (U+2B51, NEUTRAL width) bold gold, one cell. Size seen
  and approved in cats on 2026-10-16. In SF Mono's fallback ⭑ is a few
  pixels wider than its cell. Still to hand check in macOS Terminal.app,
  tmux and cats: the overhang doesn't clip or smear into the splitter
  when a narrow panel pins it to the last column, and a real click on it
  opens Recent folders.

- **N-067** · raised `2026-1016-1651-postgres-sql-formatting` · value low
  PostgreSQL formatting's external rung has only run with stubbed
  binaries. 2026-11-04 (`2026-1104-1610-n067-sql-formatter-real-run`):
  **sql-formatter 15.9.0 and the in-process rung are checked.**
  sql-formatter (npm, scratchpad) takes ced's argv as built: `--fix -c
  '<inline JSON>'` rewrites in place and keeps lower case with tabs
  (`useTabs`) or 4 spaces (`tabWidth`); a root `.sql-formatter.json`
  handed over with `-c <path>` wins (upper case, 3 spaces, on a
  lower-case file). Through the real binary (run-ced, sql-formatter on
  PATH, ≡ File "Format file" via the palette) a lower-case 4-space file
  came back clause-per-line, lower case, 4 spaces, on disk and in the
  tab. With nothing on PATH the in-process rung formatted the same way,
  and ONE `Esc u` put the original line back. **Still unchecked:
  pg_format** — fetching pgFormatter's release tarball was refused by
  the session's auto-mode check, so it needs installing by hand
  (`brew install pgformatter`), then: ≡ File → Format file on a tab- and
  a four-space-indented lower-case .sql file (argv `pg_format -i --tabs
  | --spaces N --keyword-case 1|2`), and a root `.pg_format` (`-c`)
  winning.

- **N-070** · raised `2026-1016-1849-tree-star-find-files-quit-button` · value low
  The ✕ quit button left of ≡ (quitbutton.go) has only run in tests and
  run-ced's emulator. Hand check in cats and tmux: a real click on any
  of its three cells opens "Close the editor?" (dirty tabs → Save /
  Discard / Cancel), the red ✕ reads as distinct from the tree header's
  muted ✕ a few cells left, and the ≡ beside it still opens the menu.
  Since `2026-1018-2005-quit-button-tweaks-0.6.1` (ced 0.6.1) the ✕ is a
  DESATURATED red and the clean-tabs confirm opens on Yes: also check the
  dusty red still reads as "close the editor" (not as muted chrome) in a
  couple of themes, and that click + Enter quits.

- **N-071** · raised `2026-1016-2111-recent-folders-local-and-workspace-routing` · value medium
  Recent folders' Tier-1 routing (catsworkspace.go) has only run against
  the control-socket spy. Hand check in a real cats, rebuilt with the
  `workspace.list` `cwd` field (cats 2026-10-16): clicking a project in
  the tree's ⭑ (1) with a ced open on it in another workspace brings
  that workspace, tab and pane into view; (2) with only shells in its
  workspace opens a new ced tab THERE and follows it; (3) with its
  workspace asleep, or none, opens the tab in the current workspace.
  Also with an OLDER cats (no `cwd`): the pane-evidence fallback still
  finds the workspace. And the `./` local half at the bottom reveals,
  never re-roots.

- **N-073** · raised `2026-1017-2044-button-hover-labels` · value medium
  Button hover labels have run in unit tests and in the run-ced PTY
  driver (✕ label drawn over a bare PTY), never through a multiplexer.
  Hand check in plain tmux (`mouse on`), cats, and macOS Terminal + tmux:
  resting on ✕ / ≡ / a tab's × / status ⧉ / a panel ⟳ shows the label
  after ~½s, moving across a multi-cell button (`Actions ▾`) doesn't
  flicker, a click or key dismisses it without being eaten, and the
  unpinned Find-all list's ⟳ ◇ ◨ labels draw ABOVE the list. Where no
  motion is reported, nothing appears and nothing breaks.

- **N-078** · raised `2026-1020-1857-push-receipt-pr-link` · value medium
  The push receipt rework (ced 0.6.5) has run only in unit tests on a
  simulated screen. Hand check a REAL first push of a new branch to
  GitHub from the Push dialog, in tmux and in cats: the whole output is
  shown, the `pull/new/<branch>` URL sits on one row (terminal
  Cmd/Shift-click opens it), `[ Copy link ]` lands it on the Mac
  clipboard over SSH (OSC 52 through tmux), `[ Open link ]` opens the
  browser locally, the box stays ~20s / while hovered, and ≡ Git "Show
  last push output" brings it back. Also try ≡ Git "New branch…" (bad
  name → git's refusal modal; dirty tree → the "changes come along"
  hint).

- **N-079** · raised `2026-1021-2049-editor-context-rows-change-view` · value low
  The change view (hunkview.go) ran in the real binary through run-ced
  (≡ Git "Show change at caret…" drew the diff + original; `{esc}r` as
  alt+r reverted the buffer, the bar went, disk untouched). Hand check in
  tmux and cats: a real click INSIDE the change-bar popup opens it (the
  popup's "click: revert…" hint reads), alt+r arrives through tmux's Esc
  folding, the editor right-click rows added the same session (Show
  change…, Blame this line…, Git status…, New branch…, Toggle line
  comment, Summarize selection…, Send selection to GoNotes…) appear
  where expected, and a removal revert after a save lands in place.

- **N-082** · raised `2026-1021-2124-regex-find` · value low
  Regex find ran only under the simulation screen. Hand check in tmux
  and cats: the bar's `.*` button draws right of `|W|` and toggles on a
  click, alt+r in the bar arrives through tmux's Esc folding (and does
  not fire the `Esc r` leader), a half-typed `(foo` shows "regex:
  missing closing )" in red, `$1` replace (bar Replace / All and the
  list's "Replace in N") expands, and the `[regex: off]` chip on the
  find prompts flips with a click and alt+r.

- **N-084** · raised `2026-1022-1932-diff-views-split-ws-steppers` · value low
  The diff views (diffview.go) ran in the real binary through run-ced
  only via the command palette (git panel side by side, ws hiding a
  re-indent, "Change 1 of 2", the compare panel's `[↺]`). Hand check in
  tmux and cats: CLICKS on the header toolbar (▲ ▼ split ws) in the
  compare panel, git panel and git log, their hover labels, the toolbar
  shedding on a narrow header, a compare `[↺]` click reverting the
  buffer, the git walk's `]`/`[`, the cherry-pick preview's
  alt+n/p/v and the change view's alt+p/n/s through tmux's Esc folding,
  and the change view's ◂ Prev / Next ▸ / Side by side buttons.

- **N-086** · raised `2026-1023-1213-gitlog-detail-select-copy` · value medium
  Git log detail drag-select (diffselect.go) ran in the real binary only
  through run-ced's PTY (scripted SGR press/motion/release → "4 lines
  copied", highlight painted). Hand check in tmux and cats: the drag
  highlights live, the release lands on the SYSTEM clipboard via OSC 52
  through tmux passthrough (paste it outside ced), split mode stays on
  the pressed side, dragging onto a clipped row's … takes the line's
  tail, and Esc / a click elsewhere drops the highlight. Since
  `2026-1104-1610-n067-sql-formatter-real-run` (N-087) the compare panel,
  the git panel's diff and the cherry-pick preview select the same way
  (run-ced PTY: a scripted drag across the git panel diff flashed "3
  lines copied"): check those three too, and that the git panel's hunk
  chips and the compare `[↺]` still answer a real click.

- **N-088** · raised `2026-1025-2017-cmd-up-down-l-g-chords` · value medium
  ⌘↑ / ⌘↓ (top / end of file, ⌘⇧ selecting; the markdown preview's top /
  bottom), ⌘L (Go to line, moved from ⌘G) and ⌘G (git panel toggle) ran
  only under the simulation screen. cats `9409f6c` added ArrowUp,
  ArrowDown and KeyL to `CMD_TO_PANE`; the user first saw ⌘↑ do nothing
  in cats before that change. Hand check after `make macapp` and a
  relaunch, in the cats MAC APP and in CHROME: all four chords reach a
  ced pane, ⌘⇧↑ selects, and a shell pane still gets the browser's ⌘L.
  ⌘L in Chrome is the doubtful one: Open Location is a menu item, and
  Chrome may resolve it natively the way it does ⌘E (`ed4962c`). If it
  does, note it in metakeys.go / 20-keys.js like ⌘E; Esc j stays the way
  in.

- **N-090** · raised `2026-1025-2232-find-all-esc-revert-and-seam` · value medium
  Find-all: Esc after a row CLICK keeps the row, `↺ revert loc` (title,
  key `r`) returns to the origin tab and view, and the right dock's new
  ┃ seam resizes the column. Verified by simulation tests and run-ced's
  PTY (a scripted SGR press/motion/release on the seam widened the list
  by 20 and it stayed open). Hand check in tmux and in cats with a real
  mouse: click a row then Esc stays there and the hint reads "esc stay";
  arrows then Esc still restores; ↺ from Find in project after a click
  goes back to the starting tab; the seam's grip is visible against the
  theme, the drag tracks from where it was seized, and a pinned panel's
  seam drags too.

- **N-094** · raised `2026-1026-1159-find-all-live-prompt` · value medium
  Live Find-all prompt (findalllive.go): seen in run-ced's PTY (the hit
  centered above the bottom-anchored box, "1 of 1 · enter lists them").
  Hand check in tmux and cats: typing feels immediate on a large file
  (it re-scans per keystroke like the find bar), a paste into the prompt
  lands sensibly, Alt+c/w/r re-ask, Esc puts the view and the find bar's
  old tint back, and the box sits right on a short window. Since
  `2026-1026-1447-find-all-no-caret-seed-clear-button` the prompt opens
  BLANK on an unselected caret word (find bar's text only) and the find
  prompts carry a `×` / Alt+x clear (seen in run-ced's PTY: × drawn left
  of ▾, Alt+x empties it). Also check: a real click on the ×, and Alt+x
  arriving through tmux's Esc folding without firing the `Esc x`
  plugins leader. Since `2026-1026-1545-find-click-to-line-esc-back`: a click on a line of code
  outside the unpinned list or the live prompt closes it and lands the
  caret there (seen in run-ced's PTY: list → Ln 39, prompt → Ln 14 with
  the view kept), and the find bar's Esc goes back to where it opened
  (an editor click while it is up makes Esc stay). Hand check in tmux
  and cats: the click lands on the line under the pointer in both docks,
  and the bar's Esc / click-then-Esc feel right in daily use.

- **N-096** · raised `2026-1027-1312-ai-draft-queued-claim` · value medium
  **First-use ✦ drafts with a real agent.** Fixed in `2026-1027-1312-ai-draft-queued-claim`: the commit
  prompt's ✦ AI and the GoNotes ✦ title asks now claim their answer when
  they queue behind the agent's start, so the FIRST press after launch
  reopens the prompt pre-filled instead of leaving the answer only in the
  chat transcript. Pinned by
  `TestHandleGitCommitDiff_QueuedBehindStartReopensPrompt` and
  `TestNoteSuggestTitle_QueuedBehindStartReopensPrompt` (fake connection).
  Unseen in the real binary: this machine's configured agent
  (`"chatagent": "claude"`) has no `claude-code-acp` on PATH, so ✦ only
  flashes the install hint here. Check with an installed agent: launch
  ced, press ✦ in the commit prompt before the chat has ever started,
  and confirm the prompt comes back with the draft; same for GoNotes.

- **N-100** · raised `2026-1028-1242-open-file-anywhere-cmd-o` · value low
  **⌘O / Open file… by hand.** The ≡ row, the prompt and Tab completion
  were seen in run-ced's PTY (Tab listed the root, descended
  `internal/` → `editor/`, listed `buffer.go  buffer_test.go`; `~/.zsh`
  Tab + Enter opened `.zshrc`). The ⌘O chord itself ran only under the
  simulation screen (`TestMetaAccelOpensFileByPath`). Check in a real
  kitty / Ghostty / WezTerm that ⌘O opens the prompt and that Tab works
  through tmux. N-098 landed (cats `3f77564`, KeyO in `CMD_TO_PANE`,
  ⌘O in the help row): check the cats mac app (after `make macapp` and a
  relaunch) and Chrome too. Chrome's own File > Open is the doubtful
  one; if Chrome keeps the chord, give ⌘O the help row's `†` and note it
  in metakeys.go / 20-keys.js as ⌘E is.

- **N-104** · raised `2026-1028-1801-tab-bar-red-tint` · value low
  **The red-tinted tab strip by eye.** `tabbar-bg` (sidebar-bg + 8% err)
  was seen only through run-ced's PTY capture on Darcula (`#352929`), and
  on the other themes only as numbers in tests. Look at it in a real
  terminal on a dark theme and on both light themes, where it comes out
  pinkish beige (`#edd9c8` solarized-light, `#efe5e7` corporate): does it
  read as a tint rather than a stain, and does the active tab still lift
  out of the strip? If it is wrong, the knob is `tabBarTint` in
  theme/palette.go.

- **N-105** · raised `2026-1030-1649-markdown-preview-contrast` · value low
  **The lifted markdown preview colours by eye.** Body text is now lifted
  to 9:1 and the other text roles to 7:1 (`mdBodyContrast`, `mdInkContrast`
  → `inkFloor`), and prose takes no syntax hue: inline code is the slab's
  neutral ink, headings / rules / bullets the one accent
  (`2026-1108-2028-markdown-preview-contrast`). Fenced syntax colours get
  back what the code slab took (`slabInk`). Seen only through run-ced's
  PTY capture on Darcula (inline code `#cbd4dd`, 7.24:1) and a seeded
  Cats green palette, and as numbers for the other built-ins. Look at a
  real document in a real terminal on Darcula, Cats green, a Solarized
  and a light theme: does inline code still read as code with only the
  slab marking it, do h2 and h3 stay distinct with one accent, is
  Solarized's lifted body text acceptable, and do fenced comments stay
  quieter than the code? The two constants, then lift step granularity
  (tenths, up to one step of overshoot), are the knobs if a colour lands
  too pale.

- **N-106** · raised `2026-1031-0200-menu-search-box-splitters` · value low
  **The ≡ menu's search box and splits by eye.** Seen only through
  run-ced's PTY capture (Tokyo Night and the cats host theme). In a real
  terminal, inside tmux and in cats, check three things. First, the
  terminal caret really shows in the box while the menu is up (`drawMenu`
  ends with `ShowCursor` now, not `HideCursor`). Second, the field reads
  as a box: its fill (BG) stands off the menu (LineHL) by only 1.06:1 on
  Tokyo Night, the same as the palette's input always has. Third, typing
  turns the menu into the palette without a visible jump. Also look at
  the dotted splits on both light themes. If the box is too faint, the
  fill is in `drawMenuSearchBox`; if the splits are too loud or too
  faint, the knob is `menuSplitContrast`.

- **N-137** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **CI has never run on this fork.** `.github/workflows/test.yml`
  already runs `go test ./... -race` on ubuntu-latest and macos-latest,
  but Actions is not enabled in the fork's Actions tab. Linux is the
  platform ced is written for (SSH into a box) and the suite has only
  ever run on the macOS dev machine. Enable Actions, push, and read
  the first run: a Linux-only failure is raised as a new Open item.
  For scale, the suite takes 2m40s locally WITHOUT -race, 153s of it
  in `internal/app`.

- **N-138** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **There are no benchmarks and no fuzz tests.** Zero `func Benchmark`
  and zero `func Fuzz` in the repo, so the numbers in N-125 and
  N-127 … N-132 were taken with throwaway tests and nothing guards
  them. Write: (a) benchmarks for `Highlight`, a clean `Tab.Render`
  frame, Enter + Render, `App.draw`, `SetFindQuery`, an undo push on a
  large buffer, and a paste replayed through Run's loop body, with a
  loose budget test on the ones a fix is meant to move; (b) fuzz
  targets for the code that parses what ced did not write: the LSP
  frame reader (`Content-Length`, N-126d), the ndjson reader,
  `plugins.ParseDiagnostic`, the conflict-marker scanner, `lexSQL`,
  the JSON explainer, and a `readTextFile` → `EncodedBytes` round trip
  that must be byte-identical (N-114).

- **N-139** · raised `2026-1110-1630-reliability-performance-usability-audit` · value medium
  **The hand checks have no harness.** About 25 Validate items above
  wait on a real tmux, and there is no tmux on the dev machine;
  run-ced's capture driver speaks to a bare PTY. Run that driver
  THROUGH tmux in a Linux container (mouse on, `focus-events on`), so
  the checks that are about tmux itself become scripted: Esc folded
  into Alt (the alt+r / alt+x chords of N-082, N-084, N-094), OSC 52
  passthrough (N-086, N-078), and clicks on chrome buttons (N-070,
  N-073, N-090). What only an eye can judge (colours, glyph width)
  and the cats / Terminal.app checks stay by hand. Each item this
  covers gets its result recorded in place.

## Roadmap

Wanted, but not next. Parked, not declined.

- **N-005** · raised `2026-0913-1919-cats-plugin` · value low
  cats-side decision: should two ced launch paths share ONE sidebar group
  (map the shell-launched fallback id to the configured plugin id)? Rare in
  practice; left as-is.

- **N-017** · raised `2026-0909-1845-tree-multi-select` · value low
  Tree marks: a cross-folder "select all matching" (tick every
  `*_test.go`). The finder answers the question differently today.

- **N-019** · raised `2026-0828-1713-chat-archive` · value low
  Real chat RESUME via ACP `session/load`. Contingent on an agent
  advertising `loadSession` and on solving the replay-doubles-the-transcript
  problem; the archive deliberately does not store the agent session id.

- **N-043** · raised `2026-0930-2056-bookmarks` · value medium
  Bookmarks have no keyboard door: the leader table is out of letters,
  so Toggle / Next / Previous are ≡ Nav rows reached by the palette
  (`Esc k` + typing). A function key (F2 / F11-style, nothing in ced
  binds F-keys yet) or a ⌘ chord (must be pressed in a real browser
  first, per the ⌘ allowlist rule) would make stepping through them
  practical. Decide which, then add it as a second door.

- **N-074** · raised `2026-1017-2044-button-hover-labels` · value low
  Button hover labels (buttontip.go) skip three spots: buttons inside
  ordinary modals (Yes/No etc. — they already carry words, and the layer
  is silent under every modal but the unpinned Find-all list), the
  commit receipt's `[ Push <branch>… ]`, and the status bar's
  ` · preview` / ` · wrap` pieces, which get no key line because their
  onClick (`toggleMarkdownView`/`toggleSoftWrap`) is not the verb the
  leader binds. Add only if someone misses them.

- **N-075** · raised `2026-1017-2208-conflict-side-names` · value low
  The conflict lens's full labels now carry names ("Accept yours (main) ·
  Accept picked (2a0370b) · Accept both · Compare sides") and no longer
  fit at ~120 columns, so a typical window shows the short roles
  (`Yours · Picked · Both · Compare`); the full names live on the
  `=======` label. If the short form reads as too terse in practice,
  try a middle tier (role + name without "Accept") in `layoutLens`.

- **N-077** · raised `2026-1020-1857-push-receipt-pr-link` · value low
  The push receipt's `[ Open link ]` / `[ Copy link ]` are click-only
  (the receipt lets keys pass through). The keyboard path today is ≡ Git
  "Show last push output" + selecting the URL, which is now on one row.
  If that proves clumsy, add a ≡ Git "Copy last push link" row (dim
  until a push printed a link).

## Non-goals

- **N-006** · declined 2026-11-03 · raised `2026-0913-1919-cats-plugin`
  — Editor should not count as an agent
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
  IN-LINE inlay hints (linenote.go's header has the bill). Since
  `2026-1003-2042-cherry-pick-and-conflicts-ui`, editor/lens.go offers a
  clickable END-OF-LINE lens (`LensSource`), so an LSP code lens would no
  longer need virtual rows — still declined, but that reason is gone.

- **N-001** · declined `2026-1013-1525-retire-release-pipeline` — reviving the GitHub Release pipeline
  (`release` branch, auto-bump, goreleaser). The owner retired it on
  2026-10-13 (cats N-019) instead of reviving it: `release.yml`,
  `.goreleaser.yml` and `install.sh` are deleted. ced ships as the cats
  plugin, or builds from source. The Releases page stops at v0.2.0, and
  later versions exist only as tags.

## Closed

Newest first. Closures before 2026-09-21 live in the session docs.

- closed 2026-11-06, `2026-1106-1153-n111-git-log-remote-checkout` —
  **N-111** the git log's scope picker now checks out a viewed
  remote-tracking branch (`gitLogScopeRemoteCheckout`, gitlogscope.go).
  `loadGitLogRefs` adds `%(upstream)`, so the picker's single fork tells
  it which local branch already tracks the ref. Rows: "Check out <local>
  (tracks origin/x)" when one does (preferring the same name; no row
  when you're on it), "Check out as local x" (`switch --create=x --track
  refs/remotes/origin/x`) when the name is free, and "Check out as local
  branch…" (a prompt seeded `origin-x`) when an unrelated local `x`
  exists. Every variant puts the scope back on HEAD, because the remote
  ref never gets your next commits. Decided: the scope stays
  session-only, not in `session.Entry`, because it is a view rather than
  a setting (reason in the file header). Checked:
  `TestGitLogScopeRemoteCheckout_Cases`,
  `…_CreatesTrackingBranch`, `…_CollisionPrompts` (real git),
  `TestParseGitLogRefs_ReadsUpstream`, `TestGitLogTrackArgs`,
  `TestGitLogRemoteLocalName`.
- closed 2026-11-06, `2026-1106-1114-n110-n101-alt-chords-tsc-lsp` —
  **N-112** a failed restart now explains itself. ≡ Code "Restart
  language server" arms `lspServer.explainStart` (lsprestart.go) before
  `lspEnsureStarted`. The two start-failure posts in `lspEnsureStarted`
  put the error on `lspExitEvent.err`. `handleLSPExit` answers the
  err-carrying report (the read loop's bare report, which can land
  first, neither flashes nor uses up the flag) through
  `explainLSPStartFailure`: a flash plus an info modal with the whole
  reason, wrapped to the modal; it declines an open modal or ≡ menu.
  `lspStartReason` peels `lsp: initialize: … (code N)` and
  vscode-languageserver's "Request initialize failed with message: ",
  and spells paths under the root relative. A start from opening a file
  stays silent. Decided: the modal on top of the flash, because tsls's
  sentence is ~170 cells with the key words at the end (cut at 160
  columns in the real binary), the failed-git-command rule. Checked:
  `TestMenuRestartLSP_FailedHandshakeExplainsItself`,
  `TestLSPStartFailure_SilentWithoutTheRow`, `TestLSPStartReason`,
  `TestExplainLSPStartFailure_DeclinesABusyScreen`; full suite with
  -race; in the real binary (run-ced, tsls 6.0.3 over a TypeScript 7
  workspace) opening a.ts showed nothing, and the row brought up
  "typescript-language-server failed to start" with "…provides no
  tsserver.js. No other valid TypeScript installation was found.
  Exiting." in full.

- closed 2026-11-06, `2026-1106-1114-n110-n101-alt-chords-tsc-lsp` —
  **N-110** the in-surface Alt chords are pinned. Every keyboard-owning
  surface that answers Alt+<letter> now has ONE table, `altChords`
  (altchords.go: `altChord{key, run}`, `altChordRune`, `fireAltChord`,
  first match wins), and its handleKey dispatches THROUGH it, so the list
  a test reads is the list a key reaches: the prompt (extras, then the
  `×` clear chord; `fireExtraKey` folded in), the find bar
  (`findBarAltChords`), the cherry-pick dialog, the change view and a
  picker's ⧉ headings (`runHeadingCopy` folded in). No behaviour changed:
  unbound Alt runes are swallowed or typed exactly where they were.
  README gained `#### Alt chords inside dialogs` under Hotkeys, pinned
  both ways by `TestREADME_AltChordTableListsEveryChord` over the
  `altChordSurfaces` fixture (find bar, find prompts, commit message,
  GoNotes title, label bookmark, add to favorites, cherry-pick, change
  view, Recent folders / locations), plus
  `TestAltChords_NoSurfaceBindsAKeyTwice`. Checked: a dropped row and a
  bogus row both fail the pin; full suite with -race green; in the real
  binary (run-ced) Alt+c took the bar from "1 of 4" to "1 of 2" and
  Alt+x emptied the Find all prompt.

- closed 2026-11-04, `2026-1104-1610-n067-sql-formatter-real-run` —
  **N-087** drag-select now works in every diff pane. The compare panel
  and the git panel's diff join through a small registry,
  `diffSelSurfaces` (diffselect.go: drag mode, pane, lines, rows, paint),
  so app.go's drag / release / Esc / outside press / ⌘C are one loop
  each (`diffSelDrag`, `diffSelRelease`, `diffSelClearAll`,
  `diffSelClearOutside`, `diffSelLive`); the git log keeps its older
  wiring. Each pane's paint geometry is ONE method its draw uses too
  (`comparePaint`, `gitPanelDiffPaint`). The cherry-pick preview runs the
  drag inside its modal (`selecting`, `diffPaint`, ⌘C in its handleKey),
  since a modal owns its mouse. Chips keep winning: the compare `[↺]` is
  checked before arming, and the git panel asks `gitPanelHunkChipHit`
  before its click. Real binary: a scripted SGR drag across the git
  panel diff flashed "3 lines copied". Tests:
  `TestCompareSel_DragCopiesOnRelease`,
  `TestGitPanelSel_DragCopiesAndChipsStillWin`,
  `TestDiffSel_PressElsewhereAndCmdC`,
  `TestCherryPickDiffSel_DragCopiesInsideTheDialog`. The real-mouse /
  OSC 52 hand check joined N-086.

- closed 2026-11-04, `2026-1104-1530-n108-n110-ctx-splits-readme-pins` —
  **N-108** the tree, tab and editor right-click menus carry dotted
  splits between their runs. A ROW KIND (`split: true` on
  `editorContextItem` / `contextItem`), not the ≡ menu's `splitAbove`
  flag: it keeps `ctxScroll`'s one-screen-row-per-item mapping, so
  pointer, reveal, offset and every opener's height are untouched. Split
  rows are never hovered, the arrows skip them (the tree's arrows gained
  `step`), a click on one leaves the popup up; `drawCtxSplit` paints the
  ≡ menu's `┄` in `menuSplitColor`. Builders put a split at every run
  boundary and finish with `trimCtxSplits` (no leading / trailing /
  doubled split). Runs: tree — make/find here · change the entry · copy
  · copy its name · how it's drawn · collect it (marks, favorites, tab
  group) · take it outside; tab — look · change · place · copy · close;
  editor — conflict · symbol · text · splits · hand the selection off ·
  drawing · places · repo. Seen in the real binary (run-ced, a right-click
  on a tree folder). Tests: `TestTrimCtxSplits`,
  `TestEditorContext_SplitRowsAreInert`, `TestTreeContext_SplitRowsAreInert`;
  the exact-list pins gained `""` split cells.

- closed 2026-11-04, `2026-1104-1442-n102-n093-return-hints-modal-replaced` —
  **N-093** a modal-level "being replaced" hook: `replacedModal`
  (modal.go), called by `closeAllModals` with the slot ALREADY cleared,
  so it runs once and never for a modal's own exits (each clears the
  slot first). The live Find-all prompt treats it as a cancel
  (`promptModal.replaced` → `onCancel` → view + find state back, as Esc);
  the unpinned Find-all list treats it as an accept (caret stays on the
  preview, tint handed back via `restoreFind`). Pinned by
  `TestFindAllLive_ReplacedRestoresViewAndFindState`,
  `TestFindAll_ReplacedHandsBackTheTint` and the contract test
  `TestCloseAllModals_TellsADisplacedModal` — all three seen failing
  without the hook. CLAUDE.md's single-modal rule records it.

- closed 2026-11-04, `2026-1104-1442-n102-n093-return-hints-modal-replaced` —
  **N-102** a type hint anchored just past `)` (spaces skipped,
  `inlayAfterCloseParen`) is a RETURN type and reads `-> T`
  (`inlayReturnPart`, lspinlay.go); labels carrying their own arrow
  (basedpyright, clangd) pass through. Arrow over naming the function:
  arrow functions have no name beside their `(`, and it is the form two
  servers already send. Real binary with typescript-language-server
  5.3.0 + TS 5.9 (scratchpad install): `function greet(name: string,
  times: number) {  » -> string`, and the arrow function's line ends
  `· -> string` (was `» : string`). Pinned by
  `TestInlayNote_ReturnTypeAfterParenReadsAsAnArrow` and
  `TestInlayAfterCloseParen`.

- closed 2026-11-04, `2026-1104-1349-n107-n109-n097-subtle-floor-pathinside-draft-queue` —
  **N-097** a draft ask no longer overwrites a queued composer message:
  ✦ commit message, ✦ note title and Summarize (whose queued branch
  assigned too) REFUSE with "a chat message is waiting for <agent> to
  start — try again once it has sent" (`chatQueueBusyReason`,
  copilot_chat.go). Checked at every door: the ✦ buttons' pre-check
  (`commitDraftBlockedReason`, before the prompt closes, so its text
  survives), `gitPanelSuggestCommit`, `handleGitCommitDiff` again (the
  diff returns from a fork), `noteSuggestTitle`, `menuSummarize` (before
  the attach). The converse is closed too: Enter refuses to APPEND to a
  queued CLAIMED draft (`chatQueueHoldsDraft`) and keeps the composer
  text, since the mixed answer would be pre-filled as the draft. Five
  tests, each seen failing on the old code.

- closed 2026-11-04, `2026-1104-1349-n107-n109-n097-subtle-floor-pathinside-draft-queue` —
  **N-109** `relEscapes(rel)` (gitstatus.go, beside `pathInside`) is
  now the one "does this Rel answer leave its base?" test: exactly `..`
  or a `..` + separator prefix, so a child named `..x` is inside.
  `pathInside` and the nine other sites asking the same question of a
  Rel answer (runexec, zipops, gitopstate, tabgroups, projectsearch ×2,
  recentlocations, tabbar, finder) go through it. Cases added to
  `TestPathInside` (`/foo/..x` inside, `/..foo` not) plus a
  `TestRelEscapes` table.

- closed 2026-11-04, `2026-1104-1349-n107-n109-n097-subtle-floor-pathinside-draft-queue` —
  **N-107** fixed in the theme, not per surface: `ToTheme` floors
  `Theme.Subtle` against LineHL (`subtleInk`, palette.go) at
  `minSubtleContrast` 1.12 — just under the faintest built-in frame that
  reads (dark-city, 1.13), so only a collapsed `line` == `line-hl`
  moves; user themes get the same guard. cool-blue's frames go from
  `#3b4252` on `#3b4252` (1.00:1) to `#4f5563` (seen in run-ced's HTML
  capture of the palette on cool-blue). Pinned by
  `TestToTheme_SubtleStandsOffLineHLOnEveryBuiltin` (fails without the
  floor), `…FloorLeavesReadableThemesAlone` (the other nine keep their
  stated `line` exactly) and `TestSubtleInk_LiftsACollapsedLineOnLightAndDark`.

- closed 2026-11-04, `2026-1104-1308-n080-n098-n089-label-plural-cmd-o` —
  **N-098** cats `3f77564`: `"KeyO"` is in `CMD_TO_PANE`, so ⌘O (Open
  file…) reaches a kitty-protocol pane in browser-cats and the mac app; a
  shell pane fails the gate and keeps the browser's chord. The cost was
  weighed in 20-keys.js on ⌘L's precedent: File ▸ Open File, only while a
  kitty pane has focus; the mac app loses nothing (catapp's menus claim
  no ⌘O). The help row names ⌘O (cmdhelp.test.mjs, N-089, enforces it);
  cmdarrows.test.mjs pins both halves of the gate, the encoder test the
  bytes (CSI 111;9u). metakeys.go and CLAUDE.md no longer call ⌘O dark
  in cats. Whether Chrome hands the keydown over is N-100's hand check.

- closed 2026-11-04, `2026-1104-1308-n080-n098-n089-label-plural-cmd-o` —
  **N-089** cats `abacc56`: the help overlay's "sent to an editor pane,
  not the browser" row (`32-help.js`) now lists ⌘A and ⌘X, and
  `cmdhelp.test.mjs` pins it to `CMD_TO_PANE` BOTH ways (every forwarded
  chord named; every named chord forwarded, ⌘C / ⌘Z excepted as the
  fall-through pair). The set is lifted with its value, so a new entry
  fails `make jstest` until the row lists it — the drift can't recur.
  Seen failing on the old row (⌘A, ⌘X missing), passing on the new.

- closed 2026-11-04, `2026-1104-1308-n080-n098-n089-label-plural-cmd-o` —
  **N-080** `summarizeLabel` counts through `plural`, so a one-line
  selection reads "Summarize selection (1 line)…" on the ≡ AI row and
  the editor right-click row alike (one label function). Pinned by a
  one-line case in `TestSummarizeLabel_NamesWhatItCovers`.

- closed 2026-11-03, `2026-1103-1952-n068-pgsql-live-validation` —
  **N-068** `.pgsql` / `.psql` files are now validated live (◇ + caret
  note); `.sql` stays formatted-only. Still ONE kind (`kindSQL`): the
  dialect is asked only by the validator, via `sqlDialectKnown`
  (kinds.go). The check (format/sqlvalidate.go) is formatSQL's front half
  — `lexSQL` + `matchSQLParens` — so the underline and Format's refusal
  give the same message at the same place
  (`TestValidateSQL_AgreesWithTheFormatter`); a lex error hides the paren
  check (one problem). ≡ "Validate file" on a .sql file now flashes the
  dialect reason and names .pgsql (`format.NotValidatedReason`) instead
  of "ced checks JSON and CSV". Checked in the real binary with run-ced:
  the same text is marked in fn.pgsql, quiet in dump.sql.

- closed 2026-11-03, `2026-1103-1906-n003-readme-pins` —
  **N-003** the README is now pinned to the code, both ways (a row
  missing, or a row naming something gone), through one parser,
  `internal/readme` (sections, pipe tables, code spans; fences opaque).
  New README surfaces, each with its test: `### The ≡ menu, row by row`
  (`TestREADME_MenuReferenceListsEveryRow`, app_test.go — every
  builtinMenuGroups row in its section's `####` table, dynamic labels
  resolved on a test App and matched against `<…>` forms, Key = the
  menu's hint); the `Tool window` table in `### Tool windows`
  (`TestREADME_ToolWindowTableListsEveryTool` — title + default edge);
  `## Configuration (config.json)` (`TestREADME_ConfigTableListsEveryKey`,
  userconfig_test.go — every `fileFormat` tag). 94 of ~190 ≡ rows and 9
  of 25 config keys had been documented nowhere. Fixed on the way: the
  Themes section named the palette `Esc-a` (it is `Esc k`). Context
  menus and ⌘ chords raised as **N-110**.

- closed 2026-10-31, `2026-1031-1737-n099-outside-root-active-folder` —
  **N-099** the guard moved INTO `openFile`: `adoptFileFolder` (app.go)
  is the one gate for "a file's folder becomes active", and session
  restore goes through it too (a session ending on a GOROOT tab armed
  the folder verbs the same way). Inside = lexically under the root
  first (an in-tree symlinked folder still counts), else EvalSymlinks on
  both sides with the answer RE-SPELLED under the root
  (`projectFolderFor`), so a resolved spelling of the root never reads
  as a subfolder. `openFileAnywhere` deleted; Open file… calls plain
  `openFile`. Pinned by `TestOpenFile_OutsideRootKeepsActiveFolder`,
  `TestLSPJumpTo_OutsideRootLeavesFolderVerbsDark`,
  `TestOpenFile_ResolvedSpellingAdoptsTheTreesSpelling`,
  `TestProjectFolderFor_Cases`,
  `TestRestoreSession_OutsideActiveTabKeepsTheRootFolder`.

- closed 2026-10-28, `2026-1028-1614-n016-python-inlay-hints-premise` —
  **N-016** premise false, nothing to switch on. Run against the real
  servers: pyright 1.1.414 has no `textDocument/inlayHint` at all
  (Pylance-only), pylsp 1.15.0 neither, and basedpyright 1.40.2 ships
  hints ON and answers them through ced's handshake unchanged. The `{}`
  auto-responder never even comes into play: ced declares no
  `workspace.configuration`, so none of gopls, pyright, basedpyright,
  pylsp or typescript-language-server sends that request. The comments
  claiming "gopls blocks on it" now say the branch is defensive. The run
  surfaced one defect, fixed: basedpyright's keyword-argument labels
  rendered as `name= "hi"`, now `name="hi"` (`inlayParamPart`,
  `TestInlayNote_KeywordArgumentLabelsTakeNoSpace`).

- closed 2026-10-25, `2026-1025-2252-n092-n091-find-all-picks-and-doors` —
  **N-092** keyboard walking now counts as choosing a row, the same as a
  click: ↑↓ / PgUp / PgDn / Home / End set `findAllModal.picked` (renamed
  from `clicked`) at the gestures, not in `selectRow`, so the open's
  nearest-hit preview, ⟳ and dismissals don't. Esc after a pick stays;
  `↺ revert loc` / `r` is the way back. Pinned by
  `TestFindAll_ArrowWalkPicksTheRow`, `TestFindAll_EscWithoutAPickRestores`,
  `TestFindAll_RerunAndDismissDoNotPick`.
- closed 2026-10-25, `2026-1025-2252-n092-n091-find-all-picks-and-doors` —
  **N-091** ⟳ got the in-list key `a` ("again"; `r` is revert), named in
  the widest footer hint and its button tip. The pinned panel's ↺ got a
  ≡ Find row, "Find all: revert location" (`menuFindAllRevert`: restores,
  leaves the panel up; with no list it flashes why), which the pinned
  ↺'s tip names. Menu pins moved to 206 rows / height 212 / dividers
  `[2, 5, 209]`. Pinned ⟳ ◆ ✕ stay mouse-only by design (the pinned
  panel is mouse-driven).

- closed 2026-10-22, `2026-1022-1954-n085-word-diff-emphasis` —
  **N-085** side-by-side diffs now emphasise the changed words of each
  paired row on both sides. `diff.Words` (internal/diff/words.go) runs
  the package's own patience differ over word tokens — no second
  algorithm; pairs sharing less than half the longer line are left
  plain, spans separated only by whitespace merge, and under ignore
  whitespace a spacing-only token is never lit. Lazy per row, cached on
  the `diffPane`; fill = the side's colour at 35% over the background,
  text lifted to 4.5:1, checked on every builtin. The change view
  (hunkview.go) has its own painter and is not covered.

- closed 2026-10-22, `2026-1022-1207-n083-find-prompt-chips` —
  **N-083** the three text-find prompts now carry `[case: …]` (alt+c)
  and `[word: …]` (alt+w) beside `[regex: …]` (alt+r) — the bar's own
  chords, flipping the App's toggles. Regex stays rightmost and is the
  last chip dropped on a narrow terminal; chip widths are exact so all
  three fit 80 columns (the prompt grows to 79,
  `TestFindPrompt_AllChipsFitEightyColumns`). The hint names all three
  keys; the prompt's hint row is now clipped at the border
  (`TestPromptDraw_ClipsALongHint`), which also fixes a deep folder path
  in "Find all in folder" painting across the frame.

- closed 2026-10-20, `2026-1020-1913-n076-new-branch-row` —
  **N-076** the Switch branch picker (≡ Git row and the status bar's
  branch label) now leads with "+ New branch…" (`newBranchRowLabel`,
  runs `menuGitNewBranch`). The picker always opens, so a one-branch
  repo gets that row instead of the "No other branches" flash
  (`TestMenuGitSwitchBranch_OneBranchOffersNewBranch`). The row is the
  initial highlight, so Enter on an unfiltered list now creates rather
  than switches.

- closed 2026-10-18, `2026-1018-2021-n072-palette-hides-scattered` —
  **N-072** palette yes, completion no. Measured over the ≡ menu's 178
  labels: under a real word the scattered tail was unrelated rows ("ref"
  → 1 hit + 20 others, "git" 7 + 16, "line" 7 + 16), so the palette
  (every `openPicker` list) now drops scattered rows when a contiguous
  one exists (`dropScatteredMatches`), and `paneCount` counts the same
  way. New `finder.IsContiguous` is the shared tier check. Completion
  keeps them: servers return camel-hump matches on purpose ("nt" →
  NewTab) that a stray contiguous hit would hide.

- closed 2026-10-14, `2026-1014-1447-n042-tab-group-colours` —
  **N-042** both extras. Group menu (and the ≡
  *Tab groups…* picker, which shows the same rows) gains *Change colour…*
  (`pickTabGroupColor`): the six syntax hues, each row with a swatch
  (new `paletteItem.swatch`), "current" and "used by <group>" marked,
  stored as the index so it follows theme switches and the session.
  Members' underline takes the group colour (SGR 58) only when
  `undercolor.go` vouches for the terminal: an env ALLOWLIST where the
  innermost multiplexer decides (screen → no; tmux only with
  TERM_PROGRAM=tmux; Zellij; cats by CATS_ENV), then TERM_PROGRAM /
  TERM / terminal-specific vars, VTE ≥ 0.52. List from a Sept 2026
  review of terminals' SGR parsers (vim PR #21412). `"underlinecolor"`
  auto/on/off overrides (ssh hides most evidence). Also fixed: the
  member underline broke under the file icon. Real binary checked via
  a raw byte dump of the capture tool: on/auto-in-cats emit
  `58:2::r:g:b` matching the chip, off/auto-without-evidence emit none;
  not checked by eye in other terminals.

- closed 2026-10-13, `2026-1013-1525-retire-release-pipeline` — **N-009** moot. The
  download-summary note about Homebrew installs is true for every GitHub
  Release that exists (all are v0.2.0 or older), and no more will ship
  now that the pipeline is retired (N-001 → Non-goals).

- closed 2026-10-13, `2026-1013-1326-n064-git-log-merge-picks` —
  **N-064** the log rows now carry `Merge` (parent count from `%P` in
  `gitLogFormat`), and both single-commit verbs plus the tick set add
  `-m 1` only when a merge is in the command (cherry-pick through
  `cherryPickArgs`' `mainline`, revert through `gitLogRevertArgs`). The
  confirm gains a line naming the choice (`gitLogMergeNote`: "replays" /
  "undoes what it merged in (-m 1)"). Pinned against real git by
  `TestGitLogActions_MergeEndToEnd` (pick onto main, revert on topic) and
  `TestGitLogTicks_SetWithAMergeEndToEnd`; not driven in the real binary.

- closed 2026-10-13, `2026-1013-1242-n051-cherry-pick-merges-diff-preview` —
  **N-051** both remaining extras. MERGES are
  listed (`⑂`) and picked against their first parent: `-m 1` is always
  the answer (the merge's own contribution to the source branch; `-m 2`
  replays the branch against what it merged and is left to the terminal),
  and it rides in the SAME `git cherry-pick` as non-merge picks — git
  ≥ 2.21 picks a non-merge under `-m 1` unchanged (checked against git
  2.54 by hand and by `TestCherryPick_MergeEndToEnd`). A merge that only
  brings in what HEAD has ("merge main into feature") is shown `[=]` and
  refused — git would stop the sequence on it as an empty pick. Git <
  2.31 falls back to the merge-free listing with "merges hidden" in the
  summary. DIFF PREVIEW: `alt+d` / the detail row's `▸ diff` toggle opens
  a pane inside the dialog (cherrypickdiff.go) that follows the
  highlight — message, stat, patch (first-parent for a merge), cached by
  hash, Alt/Shift+arrows and the wheel scroll it. Checked in the real
  binary (`run-ced`). Raised N-064 (the git log panel's merge picks).

- closed 2026-10-11, `2026-1011-2210-validate-n059-n056-n060-n061-n013-real-binary` —
  **N-061** checked in the real binary (`run-ced`, SGR motion reports):
  with no pointer the receipt is gone by 8.5s; with the pointer resting
  inside it is still up at 9.5s; after leaving at 3s it is still up 5s
  later and gone 8.5s later (a full 7s re-arm on leave).

- closed 2026-10-11, `2026-1011-2210-validate-n059-n056-n060-n061-n013-real-binary` —
  **N-060** checked in the real binary (`run-ced`, a repo with a bare
  remote and upstream): a commit from the git panel's Actions ▾ shows
  the receipt with `[ Push main… ]` under the message; a click on it
  opens the Push dialog for `main → origin/main`; with the receipt up,
  `Esc p` dismisses it AND opens the finder (the key is not eaten).

- closed 2026-10-11, `2026-1011-2210-validate-n059-n056-n060-n061-n013-real-binary` —
  **N-063** (raised and fixed in the same session, found by N-056's
  check) the search-history dropdown clipped Copy to… destinations at
  the END, so two folders under one long parent drew as identical rows.
  `histDropLabel` (searchhistory.go) clips destinations from the front
  (`chatFitPath`), searches and command templates still at the end.
  Pinned by `TestHistDropLabel_DestinationsKeepTheirTail`; re-checked in
  the real binary (`…/v56dest/two`, `…/v56dest/one`).

- closed 2026-10-11, `2026-1011-2210-validate-n059-n056-n060-n061-n013-real-binary` —
  **N-056** checked in the real binary in a git repo (`run-ced`, four
  restarts): a copy records its destination in `.ced/history.bytdb`
  (the database ignored by the `.ced/.gitignore` it writes; only that
  file shows as untracked, by design); the next process seeds the field
  with it; Up opens the dropdown listing both destinations, newest
  first; Delete forgets the highlighted one and the forgetting survives
  a restart (field then seeded with the survivor). The check found
  N-063, fixed alongside.

- closed 2026-10-11, `2026-1011-2210-validate-n059-n056-n060-n061-n013-real-binary` —
  **N-062** (raised and fixed in the same session, found by N-059's
  check) a COMBINED diff — the git panel's view of an unmerged file
  (`diff --cc`), and `git show` of a merge commit in the git log —
  expanded tabs one cell short: its two prefix columns were counted as
  one and code. `diffDisplayLineCols` + `diffPrefixColsAt` (gitpanel.go)
  read the column count off the hunk header's `@` run; both painters use
  them for rows holding a tab. Pinned by
  `TestDiffDisplayLineCols_CombinedDiff`; re-checked in the real binary.

- closed 2026-10-11, `2026-1011-2210-validate-n059-n056-n060-n061-n013-real-binary` —
  **N-059** hand check run in the real binary (`run-ced`, a scratch repo
  with tab-indented Go and a parked cherry-pick): the git panel diff of
  a modified Go file, a git log commit detail, and a Go conflict's
  Compare sides all draw one tab as 4 cells and two as 8. The check found
  N-062 (combined diffs one cell short), fixed alongside.

- closed 2026-10-11, `2026-1011-2152-n050-n053-n048-marker-size-scan-cache-log-ticks` —
  **N-048** the git log panel ticks several commits (leading-cell click,
  or Actions ▾ "Tick …") and cherry-picks them together: "Cherry-pick N
  ticked commits onto <branch>…" leads the Actions picker, confirms with
  the list, runs one `git cherry-pick` oldest first (order from a
  streamed `rev-list --topo-order` — fixed in `ea34914` after the
  end-to-end test caught a plain walk applying a same-second parent
  late). New gitlogticks.go; pinned by `TestGitLogTicks_CherryPickEndToEnd`
  (real repo, ticked newest first, lands oldest first), the parked-op
  refusal, the draw (✓ + "N ticked" title) and the row order tests.

- closed 2026-10-11, `2026-1011-2152-n050-n053-n048-marker-size-scan-cache-log-ticks` —
  **N-053** the Conflicts panel's refresh reuses a file's last disk scan
  while its size, nanosecond mtime and marker length are unchanged
  (`conflictCountCached`, `conflictPanelState.diskScans`, rebuilt from
  the unmerged set each refresh). The verbs that gate `git add` (Mark
  resolved, Stage all & continue) still read fresh via
  `conflictCountFor`. Pinned by `TestConflictPanel_DiskScanCachedByStat`
  (same-stat rewrite stays cached, the gate reads fresh, an mtime move
  rescans).

- closed 2026-10-11, `2026-1011-2152-n050-n053-n048-marker-size-scan-cache-log-ticks` —
  **N-050** the conflict grammar takes a marker length
  (`editor.ParseConflictsSized`, `Tab.SetConflictMarkerSize`; memo keyed
  on revision AND size). The git snapshot asks `git check-attr --stdin -z
  conflict-marker-size` for UNMERGED files only (no fork when nothing is
  conflicted), pushes the sizes onto open tabs and wireTab; the disk
  count and the picker's stage-row probe use the same size (the probe
  asks git live). Pinned by `TestConflictMarkerSize_RealRepo` (a real
  repo with `conflict-marker-size=10`), `TestParseConflictMarkerSizes`,
  and editor tests for sized parsing and the memo.

- closed 2026-10-11, `2026-1011-2142-n054-n036-n052-theme-counts-readme-leaders-conflicted-tabs` —
  **N-052** a conflicted (unmerged) file's TAB label is drawn in
  `theme.Error`, the tree's rule for the same file, active or not
  (`tabGitConflicted`, tabbar.go, reading `a.gitConflicted`). The colour
  sits on the label, not the marker slot, which keeps the disk state
  (⊘ / ⚠ / ●). Pinned by `TestDrawTabBar_ConflictedTabLabelIsError`.

- closed 2026-10-11, `2026-1011-2142-n054-n036-n052-theme-counts-readme-leaders-conflicted-tabs` —
  **N-036** the README hotkey table now lists all 20 missing top-level
  leaders (`Esc z/Z T , . b o O ~ v h H A g L S`, backtick, plus rows for the
  `Esc a` / `Esc x` / `Esc C` prefixes). `TestREADME_LeaderTableListsEveryBinding`
  (leader_test.go) fails when a binding in `leaderBindings()` or the
  static AI table has no row — it reported exactly those 20 before the
  README change.

- closed 2026-10-11, `2026-1011-2142-n054-n036-n052-theme-counts-readme-leaders-conflicted-tabs` —
  **N-054** the theme package's comments no longer state key counts
  ("the other twenty-nine", "thirty-five" — really 33 derived, 41 in
  all, counted from `derivations()`). Reworded count-free ("every other
  key", "the full palette", "forty-odd") in theme.go, palette.go,
  builtin.go, palette_test.go and load_test.go so the next derived key
  can't make them drift again. Theme tests pass.

- closed 2026-10-08, `2026-1008-1837-conflict-compare-sides` —
  **N-049** a conflict block's sides diff in the
  compare panel ("Compare sides": a lens button, a right-click row, ≡ Git
  "Compare conflict sides…"). The panel takes two arbitrary texts now
  (`compareTexts`); a diff3 block offers current ↔ incoming, base ↔
  current and base ↔ incoming. Not a 3-way merge editor — a unified diff
  of one pair at a time, with ⟳ re-reading the block and double-click
  landing on the buffer line.

- closed 2026-10-08, `2026-1008-1825-run-ced-palette-leader` —
  **N-041** the run-ced recipes open the palette with `{esc}k`, in
  SKILL.md and in the capture tool's `-help-script` text. "Leaders worth
  knowing" now says `k` palette, adds `?`, and warns that `a` is the AI
  namespace. Found while checking with the real binary: the theme-picker
  recipe's filter `theme` ranks "Reload themes" first, so its `{enter}`
  reloaded themes — now `theme:`. The `-help-script` ≡ menu example sent
  `{esc}{esc}` in one write (folds into Alt+Esc, opens nothing); it now
  splits the two Escs like SKILL.md's recipe. All three recipes re-run
  against `bin/ced`.

- closed 2026-10-08, `2026-1008-1815-context-menus-scroll` —
  **N-039** anchored context menus scroll when taller than the window.
  `editorContextModal` (editor, tab, tab-group, problems, git log,
  conflicts) and the tree's `contextModal` share `ctxScroll`
  (contextmenu.go): `placeContextSized` places by the CLIPPED height,
  ▲/▼ on the borders, wheel scrolls, arrows reveal, a press on a marked
  border pages. Premise corrected: ced draws nothing below `minHeight`
  (24), so the tab/tree menus fit today; the editor menu (conflict + cats
  + bookmark + search rows) is what actually overflowed.

- closed 2026-10-08, `2026-1008-1803-diag-note-off-switch` —
  **N-037** the caret-line diagnostic note has an off switch: ≡ View
  "Hide / Show diagnostic note" beside the inlay-hints row, persisted as
  `"diagnote"` (default on; `userconfig.SaveDiagNote`). Held inverted as
  `App.diagNoteOff` so tests that build App directly keep the note. The
  nit is fixed too: while the note is on, a press stamps the diag-tip
  pointer cell, so the click's release (a same-cell motion report) no
  longer arms a tooltip saying the same thing; while off, it still arms
  (the click's only answer). Menu pins re-pinned (174 actions, 197).
  Pinned by `TestDiagNote_OffSwitch`,
  `TestDiagNote_ClickReleaseArmsNoTooltipWhileOn`, `TestDiagNoteKey`.

- closed 2026-10-08, `2026-1008-1750-tab-menu-copy-to` —
  **N-055** the tab right-click menu has a "Copy to…" row, after Zip
  file (copy-it-out group). It acts IN PLACE on the clicked tab
  (`promptCopyTo` with that tab's path), so a background tab is copied
  without coming forward; an unsaved buffer is copied as shown, never
  saved. ≡ File "Copy file to…" is its twin. Order pin updated
  (`TestTabContext_RightClickOpensTheTabMenu`, CLAUDE.md); pinned by
  `TestTabContext_CopyToCopiesTheClickedTabInPlace`.

- closed 2026-10-08, `2026-1008-1737-popup-width-rule` —
  **N-058** checked: nothing overflowed. problems.go, gitlogactions.go,
  conflictpanel.go (and tabcontext.go, tabgroups.go, the editor menu)
  already grew to their widest label — `contextMenuWidth` was only the
  loop's floor; the one truly fixed popup, the preview's "Stop Preview"
  (contextmenu.go), fits it (12 + 6 ≤ 19). The seven hand copies of the
  sizing loop now share `contextMenuWidthForLabel` (modals.go) via
  `contextMenuWidthFor` / `editorContextMenuWidth`, the preview menu
  included, so they can't drift.

- closed 2026-10-03, `2026-1003-1533-change-bar-popup` —
  **N-046** CLAUDE.md's Commits rule now allows `Co-Authored-By: Claude`
  trailers (user's call); the "Generated with Claude Code" footer stays
  banned.

- closed 2026-10-03, `2026-1003-1222-wrapped-scroll-ceiling` —
  **N-045** wrapped scroll ceiling in rows: `MaxScroll` hands a wrapped
  tab to `maxScrollWrapped`, which walks up from EOF until
  `viewH - overscroll` display rows are counted (one row per line gives
  the old formula). Pinned by
  `TestSoftWrap_CaretBelowLineCeilingStaysOnScreen`.

- closed 2026-10-01, `2026-1001-0055-bookmark-labels` —
  **N-044** the four bookmark extras:
  - labels: `editor.Bookmark.Label` rides remap / restore / merge; ≡ Nav
    "Label bookmark…" (+ editor right-click twin on a bookmarked line),
    `[Clear name]` alt+c because a prompt drops an empty submit; shown in
    brackets in the picker and in Next/Prev's flash; stored as an
    omitempty `label` key (`history.MaxBookmarkLabel` 60 runes).
  - overflow: `offscreen.bookmarks`, POPUP ONLY — no colour (Accent
    already means the caret; any rank demotes a diagnostic).
  - "tree cut/paste MOVE": premise lapsed — ced has no move verb; the
    file clipboard only COPIES (`startPaste` → `copyTree`), and a copy
    rightly gets no bookmarks. Renames are the only in-editor move and
    were already re-keyed. A move made outside ced (shell `mv`,
    `git mv`) still leaves "(missing)" rows — unchanged by design.
  - `⚑` width: U+2691 is East-Asian-Width Neutral, not
    Emoji_Presentation; macOS libc `wcwidth` = 1, uniseg = 1 (2 only
    with VS16, which ced never emits). The real binary in a PTY (run-ced
    capture) lines `⚑   7` up with `    8`. Not checked: a GUI terminal
    whose font fallback draws the glyph wider than its cell — tmux and
    tcell lay out by wcwidth, so that would be a font overdraw, not a
    column shift.

- closed 2026-09-30, `2026-0930-1910-diagnostic-caret-note` —
  **N-012** the caret line's diagnostic is echoed as the caret moves,
  but END-OF-LINE (the inlay-note slot, in severity colour) rather than
  in the width-budgeted status bar: `diagnote.go`, `Tab.SetCaretNote`.
  Worst diagnostic + `(+N more)`, caret line only, shares
  `diagsAtCaret` with Esc-i.

- closed 2026-09-29, `2026-0929-1929-readme-recent-files-and-locations` —
  **N-029** README gains `### Recent files and recent locations`: both
  ≡ Nav pickers (the 5 + 10 lists, the `›` drill-in, reveal not re-root,
  what counts as a use), the "No file open" links, and where the history
  lives (`.ced/history.bytdb`, gitignored, repositories only, "(not
  saved)"). Plus a Features bullet and the `Esc B` hotkey row.
- closed 2026-09-29, no session doc (commit "Hover asks for markdown first (N-030)") —
  **N-030** hover now asks for MARKDOWN first. Checked against real
  gopls v0.21 on ced's own sources before deciding: the plaintext form
  was worse than the heuristic's known costs. A type's method list
  arrives as unindented `func (c *Client) …` lines, which `hoverReflow`
  could only read as prose and joined into one paragraph. Every doc link
  also trails a `[Name]: file:///…` definition line, visible in any
  short tooltip. Markdown fences the signature and the method list, splits sections
  with `---`, sends each paragraph on one line, and resolves doc links.
  `lsp.Hover.Markup` keeps the kind (bare MarkedString = markdown,
  `{language, value}` fenced, array elements blank-separated); it rides
  `lspHoverEvent.markdown` for both the Esc-i modal and the dwell
  tooltip. In markdown, non-code lines go through the preview's own
  inline scanner (`editor.MarkdownInlineText`, plus `MarkdownLinkOnly`
  for the pkg.go.dev footer, shown as its URL). The plaintext guesses
  stay for servers that ignore the preference. Signature help and
  completion docs stay plaintext first. Known wart, gopls-side: its
  markdown linkifies the `[string]` in `map[string]any`, so that one
  reads "mapstringany". Pinned by
  `TestHoverLines_MarkdownReadsGoplsStructure`,
  `TestHoverLines_MarkdownDropsThePlaintextGuesses`,
  `TestHandleLSPHover_RendersMarkdown`, `TestHoverMarkup`,
  `TestInitialize_PrefersMarkdownHover`,
  `TestMarkdownInlineText_ReadsLikeThePreview`, `TestMarkdownLinkOnly`.
- closed 2026-09-29, no session doc (commit "gofmt drift (N-034)") —
  **N-034** `gofmt -w` on `internal/app/hovermodal.go` (the
  `tooltipPlace` diagram's leading indent), `internal/editor/tab.go`
  (`mdView`/`MDScroll` alignment) and `internal/lsp/inlayhint_test.go`
  (a long one-line goroutine split). Formatting only; `gofmt -l` is now
  empty.
- closed 2026-09-29, `2026-0929-1855-find-options-reach-the-lists` — **N-035** the bar's `Aa` / `|W|` now reach both
  lists. `App.findOptions()` is the one conversion; `showFindAll`
  snapshots it onto `findAllModal.opts` (⟳, the stale check and the
  replace plan keep asking that question if a toggle flips under a pinned
  list), and borrows/returns the tab's `FindOpts` with its query.
  `search.Options` embeds `editor.FindOptions`; the event carries them;
  `startProjectSearch(query, opts)`; project tints install the list's
  options and restore the tab's. Titles and "no occurrences" flashes name
  non-default options ("(match case, whole word)", `findOptionsNote`).
  Found on the way: `rowStale` / `buildReplacePlan` compared the row to
  the query byte-for-byte, so a case-insensitive list dimmed its
  "Count" hit for "count" as stale and Replace skipped it — now
  `editor.MatchesAt`, which shares `matchCols`' fold and boundary rule.
  Pinned by `TestFindAll_HonoursTheBarsOptions`,
  `TestFindAll_CaseVariantRowsAreNotStale`,
  `TestProjectSearch_CarriesTheOptionsThrough`,
  `TestProject_HonoursMatchCaseAndWholeWord`,
  `TestMatchesAt_AgreesWithTheScanner`.
- closed 2026-09-29, `2026-0929-1808-find-docs-blame-names-replace-history` — **N-031** the Find-all list's replace
  box has its own dropdown on the replacement list it already recorded
  into: `Up` in the box or a click on its `⇄▾` label, opens below, fills
  and never runs "Replace in N". Unpinned it rides the modal slot;
  pinned, `findAllPinHistMouse` asks it before the router's panel
  dispatch (rows hang over the editor) and `drawFindAllPinHist` paints it
  from the overlay pass; `closeAllModals` shuts it. The filter boxes
  (Find-all, git log, references) stay history-less — filters, not
  searches. Found on the way: a dropdown's own button could never close
  it (the outside-press rule closed it and handed the press to the button,
  which reopened it) — the find bar's promised second-click close and the
  prompt `▾` both; `histDropMouse` now takes the owner's `toggle` rect and
  consumes a press on it. Pinned by `TestFindAll_Replace*`,
  `TestFindAll_FilterBoxHasNoHistory`,
  `TestFindAll_PinnedReplaceHistHangsOverTheEditor`,
  `TestCloseAllModals_ClosesThePinnedReplaceHist`,
  `TestFindBar_SecondLabelClickClosesTheList`,
  `TestSearchPrompt_SecondButtonClickClosesTheList`.
- closed 2026-09-29, `2026-0929-1808-find-docs-blame-names-replace-history` — **N-033** neither switch nor choice:
  blame keeps the given name and adds surname letters only where two
  different authors in the same file share it —
  `disambiguateBlameAuthors`, run at the end of `parseBlamePorcelain`
  over the whole file so labels (and the column width) never depend on
  what is scrolled into view. "Rohan A." / "Rohan B.", the prefix grows
  on a shared initial ("Rohan Al." / "Rohan Ad."), a whole surname drops
  the dot, the given name is what gets elided to fit `blameAuthorMax`.
  No config key, no ≡ row. Pinned by `TestDisambiguateBlameAuthors_*`
  and `TestParseBlamePorcelain_DisambiguatesSharedGivenNames`.
- closed 2026-09-29, `2026-0929-1808-find-docs-blame-names-replace-history` — **N-032** README `### Find in file`
  rewritten against the code: `Aa` / `|W|` (click, `Alt+c` / `Alt+w`, the
  ≡ Find rows), selection seeding, the replace row (`Esc e`, Tab, `Enter`
  / `Alt+a`, one undo step), the Find-all list (`Esc F` / ↓: preview,
  Esc restores, `d` dock, `p` pin with filter / dismiss / ⟳ / replace),
  Find in project (`Esc P`, click keeps the list, 10,000 cap) and Go to
  line. The hotkey table gains `Esc F` / `Esc e` / `Esc P` / `Esc j`.
  Raised N-035 for the toggles not reaching the lists.
- closed 2026-09-29, `2026-0929-1722-history-not-saved-notice` — **N-028** neither option as written: no
  fallback location (a new dotfile, which CLAUDE.md rules out) and no
  startup flash (startup errors are HELD for a ≡ label). Instead
  `history.WriteProblem` probes by doing — temp dir beside a missing
  `.ced/`, temp file in an existing one, write-open of the database, each
  undone — on the first menu/palette ask, and the answer is held: ≡ Nav
  "Recent files…" / "Recent locations…" read "(not saved)", stay
  clickable, and flash "History won't be saved this session: can't create
  .ced (permission denied)". A non-repository root is memory-only by
  design and never labelled. Pinned by `TestWriteProblem_*` and
  `TestHistoryNoSave_*`.
- closed 2026-09-29, `2026-0929-1710-select-all-file-row` — **N-008** ≡ **File** now carries a "Select all"
  row too (under the Copy-path rows), same action and predicate as the
  Edit row. It is flagged `paletteTwin` so the command palette still
  lists the verb once — an explicit flag, not dedupe by label + func
  pointer, because plugin/custom-action rows are closures from one func
  literal and would merge. Pinned by `TestMenuSelectAll_InFileAndEdit`
  and `TestPalette_TwinRowListedOnce`; menu pins now 150 actions / 167
  rows / height 173.
- closed 2026-09-29, `2026-0929-1617-history-only-in-repos` — **N-027** `.ced/history.bytdb` is written only when
  `history.Persists(root)`: the root is inside a git work tree (walks up,
  so a repo subfolder counts; a `.git` FILE counts, for worktrees and
  submodules) or already holds a `.ced/` (earlier history, format.json).
  Anywhere else the history is memory-only for the session — `ced ~` no
  longer leaves `~/.ced/`. Checked at write time, so a mid-session
  `git init` persists. Filesystem walk, not `git rev-parse`, so Close
  never waits on a process. Pinned by `TestPersists_*` and
  `TestWriteHistory_OnlyInARepository`.
- closed 2026-09-29, `2026-0929-1552-overflow-markers-panels` — **N-018** overflow markers on the compare panel
  (lines), Problems (problems, over the filtered view), chat (wrapped
  transcript rows, band stops above chips/composer) and terminal
  (scrollback lines). All sit in each panel's blank right margin. Problems
  is the only list COLORED by what's hidden (severity, since rows sort by
  path); the terminal stays plain over stderr on purpose. The click routes
  through `scrollAt` unchanged. Seen in the real binary (terminal ▴/▾).
- closed 2026-09-21, `2026-0921-0932-next-list-run` — **N-004** `release.yml`'s auto-bump now rewrites
  `cats-plugin.toml`'s version in the same commit, and
  `TestVersion_MatchesCatsManifest` catches a manual bump that forgot it.
  The workflow edit is UNRUN — first exercised by N-001's release.
- closed 2026-09-21, `2026-0921-0932-next-list-run` — **N-002** `toolLayoutSummary()` has a reader: the
  ≡ "Reset tool window layout" flash now carries it, since most of what
  a reset changes is off screen. Kept rather than deleted.
- closed 2026-09-21, `2026-0921-0932-next-list-run` — **N-007** ⌘A selects all (`metaAccels`, pinned by
  `TestMetaAccelSelectsAll`; the never-the-only-path test passes via the
  ≡ Edit row). Not live in browser-cats until N-026.
- closed 2026-09-21, `2026-0921-0932-next-list-run` — **N-015** `lspLookPath` pinned at "never found" in
  `newTestApp`; the two real-gopls tests opt back in with
  `useRealLSPBinaries` (confirmed they still RUN, not skip).
- closed 2026-09-21, `2026-0921-0932-next-list-run` — **N-011** gutter click opens the diagnostic tooltip
  (`diagGutterPress`, diagtip.go). Diagnosed lines only; a second click
  closes it; no caret move, no drag. Like N-010, only exercised on the
  simulation screen — `run-ced` cannot send mouse events.
- closed 2026-09-21, `2026-0921-0932-next-list-run` — **N-014** "Restart language server" ≡ Code row.
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
