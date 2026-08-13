# ced × cats: Vision and Phased Roadmap

*The editor and the multiplexer as one IDE.*

Successor to [mini-ide-plan.md](mini-ide-plan.md) (fully executed 2026-07).
North star: **intuitiveness** — click on things for actions more than remember
key chords; chords remain as accelerators. Written 2026-08-12.

---

## 0. The headline finding

ced has **zero** integration with cats today. It is "Cats Editor" by branding
only — it targets generic tmux/SSH terminals, and nearly every UX compromise a
GoLand user feels is scar tissue from that hostile channel, a channel cats
specifically removes:

| ced compromise (today) | Terminal constraint | What cats offers instead |
|---|---|---|
| Esc-leader, "no Ctrl chords" rule | Ctrl-S/Q flow control, Ctrl-B tmux prefix | Full kitty keyboard protocol, server-encoded; **⌘-chords reach the pane** (⌘C/⌘Z already fall through by design) |
| ~90-row ≡ menu as fallback for every action | macOS Terminal+tmux swallows right-click | With mouse tracking on, cats hands over **everything incl. right-click**, clean cell coords |
| OSC 52 write-only clipboard; "compare vs pasted text" | No clipboard read in terminals | catapp bridges the real pasteboard (write works remotely today; read is a small upstream ask) |
| Display-only status bar, menu redundancy | Untrustworthy mouse pipeline | Guaranteed SGR mouse; clickable everything is safe |
| Embedded grsh pseudo-terminal (no PTY) | Can't spawn real panes | `tab.create` / `pane.split` via control socket spawns real sibling PTY panes |
| No "editor needs you" signal | No notification path | Hook API: pane badge, sidebar row, toast, **native notification + phone push** |
| Split panes deferred (layout-tree refactor) | Would need in-process layout engine | cats's BSP tree IS the layout engine |

## 1. Vision

**ced-on-cats is the first terminal editor whose IDE is the multiplexer.**
Every terminal editor before it fights its host. ced stops fighting and
*federates*:

- **⌘S saves. ⌘P opens a file. Right-click always works.** GoLand muscle
  memory just works, because cats delivers what a raw terminal cannot.
- **cats panes are ced's split panes.** "Open in split" is `pane.split` + a
  sibling ced — the mux's layout engine, zoom, and swap gestures come free.
  Exactly the refactor mini-ide-plan deferred, obtained without building it.
- **Agents in sibling panes are collaborators, not neighbors.** ced sees
  `pane_agent` events, can `pane.send_input` a selection to Claude next door,
  `read` its output back, `chat.send` to the cats ACP panel. "Ask the agent
  about this function" is a right-click verb.
- **The editor can call your phone.** Via the hook socket, ced reports
  `blocked` when a modal question waits (clobber conflict, cherry-pick
  conflict) — badge, toast, native notification, ntfy push. Walk away during
  a long operation; your phone tells you the editor needs you.
- **Navigation knows where you've been.** `path.list` exposes cdx frecency;
  the fuzzy finder ranks by your whole-terminal-life history.
- **One theme, one clipboard, one input language.** `config.get` gives ced the
  host's exact colors; kitty input gives real ⌘ chords; `clipboard.read`
  (upstream ask) finally closes the OSC 52 read gap.

**Fallback discipline:** capability ladder. Tier 1 adds; it never moves. The
Esc-leader never changes meaning; tmux users lose nothing.

## 2. Capability ladder

### Detection
`CATS_ENV=1` + `CATS_PANE_ID` + dialable `CATS_CONTROL_SOCKET` ⇒ Tier 1. Any
failure ⇒ Tier 0, silently (same philosophy as missing gopls). Probe cached on
App at startup; a posted `catsStateEvent` if the socket dies later. **No
feature may exist only at Tier 1 without a Tier-0 fallback path.**

| | Tier 0 — any terminal | Tier 1 — inside cats |
|---|---|---|
| Chords | Esc leader + opportunistic ModMeta (⌘C/V/Z, app.go ~2067) | + full ⌘ accelerator layer via kitty protocol |
| Right-click | Best-effort; ≡ menu fallback | Guaranteed context menus |
| Clipboard | OSC 52 write-only; compare-with-paste | + real read once `clipboard.read` lands upstream |
| Splits | None (tabs only) | cats panes via control API |
| Terminal | Embedded grsh | + real PTY sibling panes |
| Attention | `flash()` in status bar | + hook reporter → badge/toast/phone push |
| Theme | ced's own 10 + user JSON | + "Cats (host)" theme from `config.get` |
| Recents | ced session recents | + cdx frecency via `path.list` |
| Agents | In-process ACP chat panel | + sibling-pane agents (events + send_input/read) |

### New package: `internal/cats`
Mirrors the shape of `internal/remote`/`internal/lsp` (unix-socket JSON
peers), obeying the iron events-only rule (goroutines never touch App state;
they PostEvent; only the main loop mutates):

- `detect.go` — env sniff + socket probe → `Caps` struct, parsed pane ID.
- `client.go` — newline-JSON control-socket client; typed wrappers for the
  verbs ced uses: `PaneSplit`, `TabCreate`, `PaneSendInput`, `Read`,
  `ConfigGet`, `PathList`, `ChatSend`, `PaneList`, `PaneFocus`. Wire structs
  are hand-copied minimal mirrors of `cats/internal/app/command_vocab.go` —
  do **NOT** import the cats module; ced stays buildable standalone.
- `events.go` — long-lived `events.subscribe` stream goroutine, reconnect
  with backoff; must survive cats restarts and never block the main loop.
- `hooks.go` — hook-socket reporter (`CATS_SOCKET_PATH`):
  `ReportState(state, customStatus)` as `source:"ced"`, seq-numbered,
  fire-and-forget; `pane.release_agent` on shutdown.

Glue in a new `internal/app/cats_glue.go`: one new tcell event type
`catsEvent{kind, payload}`; the main event switch gains one case dispatching
on `kind`. App holds `catsClient *cats.Client`, nil at Tier 0; every call
site is `if a.catsClient != nil { … } else { fallback }`. Request/reply calls
run request-on-goroutine → result-as-posted-event (the custom-actions
pattern).

### ⌘ accelerator layer
Extend the existing ModMeta branch (app.go ~2067, today ⌘C/V/Z only) into a
table **gated on Tier 1 / kitty detection** (so Alt-as-Meta terminals can't
misfire): ⌘S save, ⌘P fuzzy-open, ⌘⇧P palette, ⌘F find, ⌘⇧F project search,
⌘W close tab, ⌘D duplicate, ⌘/ comment, ⌘G go-to-line, ⌘E recent files,
⌘click go-to-definition (mouse router: ModMeta+Button1). Pure accelerators —
every one keeps its Esc-leader/menu path. Never claim cats-reserved keys
(⌘K ⌘B ⌘V ⌘± ⌘0 F12). **Mac-app caveat:** Cocoa eats ⌘ before the WebView;
each pass-through needs native menu routing upstream (mapped in cats
`ai_docs/claude_sessions/2026-0727-1618-…`); browser-cats works day one.

---

## 3. Phased roadmap

Ordering: intuitiveness quick wins → safety → completion → git suite → deep
cats. Phases 1–3 are pure-ced and fully functional at Tier 0. Each phase is
shippable alone; every new file gets a `_test.go` sibling.

### Phase 1 — Click-first discoverability (~1–2 weeks) — ✅ done 2026-08-12
*Goal: a GoLand user drives ced for a day without learning one chord.*

*All six items landed (statusbar.go, contextmenu.go, whichkey.go,
searchable ≡ menu, treenav.go, interactive find-all incl. pin/filter/
dismiss/replace-in-results/re-run). Notes vs. spec: the pin glyph is
◇/◆ (single-width house rule, not 📌); the diag status segment is
stamped but inert until Phase 3's Problems panel claims its click;
"Compare selection with…" landed as compare-selection-with-paste via
an armed snapshot (compare.go selPending).*

1. **Clickable status bar** — extract `drawStatusBar` (app.go ~3719) into new
   `statusbar.go` as a slice of `statusSegment{text, onClick, rect}` (the
   `btnRect` one-rect-for-draw-and-hit idiom). Segments: filename → tab
   picker · dirty ● → save · Ln,Col → go-to-line · diag counts `E:2 W:5` →
   Problems panel (Phase 3) · branch → branch switcher · Copilot/LSP → their
   menus · ≡ glyph far right → the menu (a mouse path to the menu at last).
   One new branch in the mouse router (app.go ~2252). ~300 LOC.
2. **Editor right-click context menu** — new `contextmenu.go` on the
   `contextModal` chassis, anchored at the click point. Click sets caret
   first (unless inside current selection), then context-sensitive rows:
   Go to Definition / Find Usages / Rename… / Hover / Code Actions… (all
   already-wired LSP verbs) · Copy/Cut/Paste · Compare selection with… ·
   Search project for ⟨word⟩ · (Tier 1 later) Send selection to agent pane.
   `enabled:` predicates in the existing menu style. ~250 LOC.
3. **Which-key overlay** — new `whichkey.go`. Add a `label` field to
   `leaderBinding` (leader.go; labels cribbed from menu rows sharing the
   shortcut). When Esc arms the leader and ~350ms pass (the `lastEscape`
   timestamp exists), draw a bottom-anchored overlay of `key → label`
   columns; **rows are clickable** (click = fire). Prefix namespaces
   (`Esc a`, `Esc x`) re-render the overlay. Passive — does not claim the
   modal slot; any key dismisses. The leader becomes self-documenting.
   ~300 LOC + label backfill.
4. **Searchable ≡ menu** — typing while the menu is open switches it to
   fuzzy-filtered mode across all groups (bypassing collapsed state),
   reusing the palette matcher over flattened `menuItemDef`s. ~150 LOC.
5. **File-tree keyboard navigation** — focus toggle, arrows move selection,
   →/← expand/collapse, Enter opens, typeahead; `n`/`d`/`r` map to existing
   context-menu verbs. Closes the mouse-only-tree gap; tree becomes
   symmetric: mouse-first, keys as accelerators. ~250 LOC.
6. **Fully interactive find-all** (req: "Findall should be fully
   interactive") — findall.go graduates toward a results *panel*:
   - **Pin** 📌 next to the dock toggle: pinned survives editor clicks and
     edits (docked-panel mode); unpinned keeps today's PEEK contract exactly.
   - **Click really jumps**: single click = preview (PEEK), double-click /
     Enter = commit jump leaving the panel open.
   - **Filter box**: second `textField` narrowing displayed *results*
     (distinct from the query).
   - **Dismiss rows**: `✕` per row (and Delete key) — results become a
     worklist you burn down.
   - **Replace in results**: Replace field + "Replace in N results" applying
     only to *surviving* rows via `workspaceedit.go` (one undo gesture);
     confirm modal states file/match counts.
   - **Re-run** ↻; stale rows (line text no longer matches) render dimmed.
   ~400 LOC delta.

### Phase 2 — Never clobber, never be clobbered (~1 week) — ✅ done 2026-08-12
Req in the user's words. Extract `reconcileOpenTabsWithDisk` (app.go ~1572)
into new `reconcile.go` and extend. Two interception points:

*Landed as specified (reconcile.go + reconcile_test.go, one commit).
Notes vs. spec: the "undoable reload" needed a new editor primitive —
`Tab.ReloadUndoable` (reload, then re-file the pre-reload buffer onto
the fresh history), used by both the clean-tab reload and Take disk.
Markers are `⚠` (conflict) and `⊘` (deleted) sharing the tab strip's
dirty-dot slot, worst news winning. Two save paths not in the spec's
list also got the guard: cross-file edits refuse non-interactively
(a detached participant that moved aborts the whole group — half a
rename is worse than none), and format-on-save now adopts its own
write's mtime when it keeps the user's edits, so ced never blocks a
save behind a question about a write it made itself. A conflict raised
during save-all-then-quit blocks the quit (nothing is lost; the user
re-quits after answering). The Tier-1 `blocked` hook report is the
Phase 5.3 consumer of the conflict record; nothing here needs to
change for it.*

**A. Watcher tick (disk changed under an open tab):**

| Disk | Buffer | Today | New behavior |
|---|---|---|---|
| newer | clean | silent reload + flash | keep (correct); flash "reloaded ↺", undoable |
| newer | dirty | flash warning only | **conflict prompt** (below) |
| deleted | any | flash + mark dirty | keep + tab-strip `⊘` marker; save recreates |

The conflict prompt: persistent `⚠ disk changed` marker + a modal (raised
immediately if the tab is frontmost, else deferred to next activation) with
four clickable choices on the confirm-modal chassis:
- **Compare…** → compare panel as buffer-vs-current-disk (look before you
  choose; other choices stay available from the compare action row)
- **Keep mine** (disk mtime adopted so the warning clears; next save
  overwrites knowingly)
- **Take disk** (buffer state pushed as an undo snapshot first — reversible)
- **Decide later** (marker stays; save is guarded by B)

**B. Save guard (never clobber):** every save path (menu save, autosave,
save-and-close, format-on-save) stats first; if disk mtime > `tab.Mtime` the
write **aborts** and raises the same prompt, re-running the save on
resolution. Today ced warns but writes anyway — this is the fix.

**Autosave interplay:** autosave never raises a modal from a background
tick — on conflict it silently *suspends autosave for that tab*, sets the
marker; the modal appears on next interaction. Adopt write-then-stat so
ced's own saves never trip the watcher. At Tier 1, a suspended conflict
reports `blocked` via the hook — the flagship phone-push scenario. ~400 LOC.

**Follow-up worth doing (not blocking):** after "Decide later" the only
route back to the prompt is an explicit save. Clicking the tab's `⚠`
marker should re-raise it — a small tab-bar hit-test in the Phase-1
stamped-rect idiom.

### Phase 3 — IDE smarts (~2–3 weeks; completion is the big rock)

1. **LSP completion popup** — ✅ done 2026-08-12.

   *Landed as specified: `internal/lsp/completion.go` (wire layer) +
   `internal/app/completion.go` (the popup), ~1100 LOC with tests. Notes
   vs. spec:*

   - *`completionItem/resolve` is wired but **dormant against gopls**,
     and that is the correct outcome. The client deliberately does NOT
     declare `resolveSupport`, so gopls computes `additionalTextEdits`
     (the auto-import) up front — verified against a real server: every
     item comes back with `addl=1`. gopls then answers
     `resolveProvider:false`, so the resolve call is gated off. The
     trade is the same one codeAction already makes: correctness never
     waits on a second round trip, and resolve exists only to enrich the
     detail pane for a server that offers it.*
   - *`snippetSupport:false` is declared (ced has no tab-stop engine);
     an item that arrives as a snippet anyway is **refused with a flash**
     rather than writing `${1:}` into the buffer.*
   - *gopls answers `isIncomplete:true`, so the re-request path — not
     local filtering — is the one that actually runs for Go. Both exist:
     a complete list filters in memory with the palette's fzy scorer.*
   - *Auto-trigger is server trigger characters ONLY (`.` for Go), never
     every letter — Copilot ghost text already occupies "guess what I'm
     typing", and two overlays racing per keystroke is noise. Esc-Space
     is the deliberate invocation (⌘Space is the OS's, Ctrl is banned),
     which needed a `keyLabel` field on `leaderBinding` so the which-key
     overlay can print `spc` instead of a blank cell.*
   - *New editor primitive: `Tab.PosScreenCell` — `CursorScreenCell` for
     an arbitrary position, because the popup anchors at the START OF
     THE TOKEN, not the caret.*
   - *Accept goes through `ApplyMultiEdit`, so the item's edit and its
     auto-import are ONE undo step; the caret lands via the returned
     result, since an import inserted above shifts every line after it.
     The server's edit end is extended to the caret when the user typed
     on while the request was in flight (else `fmt.Pr` + "in" accepts as
     `fmt.Printlnin`).*
   - *Staleness is deliberately looser than hover's: a response is kept
     when the caret is still on its line with only identifier runes typed
     since. At a 150ms debounce the strict rule would have shown the
     popup only to slow typists.*
   - *Kind glyphs live in `internal/icons` as `CompletionKind` (Codicon
     block, spelled as escapes — pasted PUA glyphs silently became empty
     strings that still compiled).*

   The original spec follows.

   The single biggest GoLand gap;
   `textDocument/completion` is simply unwired today (only Copilot ghost
   text exists). Wire request + `completionItem/resolve` in `internal/lsp`
   (the definition/hover plumbing pattern). New `completion.go`: an anchored
   popup at the caret — **NOT** a modal-slot occupant (like ghost text, it
   coexists with typing); draw model borrows `hovermodal.go` + palette row
   rendering (kind icons via `internal/icons`), fuzzy-filters as you type.
   Trigger: server trigger characters + manual (`Esc Space`; ⌘Space is
   OS-reserved, Ctrl is banned). Debounce ~150ms on the `lspAfterEvent`
   chain; results via PostEvent. Accept: Tab/Enter/**click** applies
   `textEdit` + `additionalTextEdits` (auto-import!) through
   `workspaceedit.go`. Ghost-text interplay: popup wins while open (one
   boolean gate in copilot_ghost.go). ~800–1000 LOC — the centerpiece.
2. **Problems panel** — ✅ done 2026-08-12.

   *Landed as `internal/app/problems.go` + tests (~1000 LOC with the
   suite). The Phase-1 diag status segment is inert no longer: it
   toggles the panel and lands the highlight on the active file's
   first problem. Notes vs. spec:*

   - *It docks at the BOTTOM, not in find-all's top/right band. Both
     of find-all's docks exist to keep a list near the code it points
     at inside ONE file — the top strip is short, the right column is
     62 cells. A problem row is `path:line │ message`: wide, prose,
     and belonging to no particular file. So it takes the bottom
     strip and mirrors `gitlog.go`'s shape (which itself mirrors
     gitpanel rather than sharing its code, for the same reason).
     Single occupancy with the git panels, compare, and a
     bottom-docked terminal, both directions.*
   - *It is never a modal, not even briefly. Diagnostics are ambient —
     you asked no question — so nothing here may own the keyboard.
     That is the one real difference from find-all, whose list starts
     as a peek popup and only becomes furniture when pinned.*
   - *Filters landed as header CHIPS — `✗ 3 ⚠ 5 ℹ 1` plus a scope
     toggle (`all files` ⇄ `this file`). The scope chip is not in the
     spec and closes a real gap: the status segment counts the ACTIVE
     FILE while the panel lists the project, so one click reconciles
     the number you pressed with the list you got. Counts honor scope
     but never severity — a hidden bucket's count is the argument for
     unhiding it. Selection survives every filter toggle and every
     publish by IDENTITY (path+line+message), not index.*
   - *Right-click reuses `editorContextModal` rather than growing a
     third anchored-menu chassis — its rows are already plain
     `func(*App)` with predicates. Rows: Quick fix… / Go to problem /
     Copy message. **Quick fix jumps first, and the jump is the
     mechanism, not a courtesy**: `codeAction` is asked about a range
     in the ACTIVE document and its handler drops answers for a file
     the user has left, and moving the caret onto the diagnostic is
     also what makes `diagsForRange` echo that exact diagnostic back
     as the request context (which is how a fix finds its problem).*
   - *Keyboard twins beyond the ≡ toggle: **Next / Previous problem**,
     which walk the panel's list from wherever the CARET is, treating
     the whole project as one document (the row order makes each
     file's problems contiguous). They work with the panel closed,
     honor the filters, and refuse at the ends with a flash rather
     than clamping — a key that silently lands you where you already
     were reads as broken. Without them the panel would be a surface
     a click-eating terminal could only look at.*
   - *No filter TEXT box (find-all has one): it would need a focus
     model in a panel that deliberately has none. The severity chips
     are the filter the spec asked for; a text filter is cheap to add
     later if the list ever gets long enough to need it.*
   - *Leader: `Esc !` — the letters this table would want (`p`, `P`)
     are long gone, and a bang is what a list of errors looks like.*

   The original spec follows.

   New `problems.go`, docked panel styled after
   find-all's dock machinery: all diagnostics, click row = PEEK-style jump,
   severity filter buttons, right-click → quick-fix (code actions already
   wired). Entry: the Phase-1 diag status segment. ~500 LOC.
3. **Hover on mouse dwell** — Tier 1 (motion reporting reliable there).
4. **Recent files picker** — MRU ring over `internal/session` data via
   `openPickerWithCancel`; ⌘E at Tier 1.

### Phase 4 — The git suite (~2 weeks)
ced's git layer is already deep — `gitlogactions.go` **already implements
cherry-pick, revert, reset (hard-mode confirm), checkout, branch, tag, copy
SHA**. This phase closes the specific named gaps:

1. **Push dialog — never type the current branch.** — ✅ done 2026-08-12
   New `gitpush.go` + `gitpush_test.go`. All four rows as specified, the
   hard requirement honored (option 0 is always the current branch, present
   before any network call), header `main → origin/main (ahead 3, behind 1)`,
   push via `runGitCmd`. Entry points all four: ≡ Git group ("Push…"),
   gitpanel Actions, a new `↑ push` gitlog header button, and the status bar.
   Deviations, all deliberate:
   - **Its own modal, not `formModal`.** formModal's rows ARE
     `customactions.Prompt` values — a config type describing what a TOML
     action author can declare. Teaching it checkboxes, a live header,
     goroutine-refilled option lists, and a state-dependent button label
     would push editor UI into a package that exists to parse config. It
     mirrors formModal's shape and shares its primitives (`centeredRect`,
     `drawFrame`, `btnRect`, `drawButton`, `textField`) — the same
     problems.go-vs-gitlog.go call, for the same reason.
   - **Locals inline, network async.** `git remote` + `symbolic-ref` are two
     forks the dialog can't open without — `menuGitSwitchBranch`'s budget.
     Only `ls-remote` goes to a goroutine (`gitPushRefsEvent`), and its
     failure posts an EMPTY list so an offline push still works.
   - **Key axes split**: Up/Down move between rows, Left/Right change the
     row's value. formModal cycles selects with both, which it can afford
     because every row is one kind; here the branch row becomes a text
     field that owns Left/Right. Arrowing off either END of that field is
     the way back out of "other…" (the chevrons stay drawn for the mouse).
   - **The force confirm is in-dialog, not a second modal** — the tick, the
     red `⚠ Overwrites origin/x unless it moved since your last fetch.`
     line, and a button relabeled "Force Push" (right-anchored so it can't
     slide out from under the pointer) are the confirmation.
   - **Status-bar entry is a NEW `↑3↓2` segment**, not the branch segment:
     the branch keeps switch-branch, and the count is the fact whose verb
     is push — the ● → Save pairing. A bare `↑` marks a never-pushed
     branch. Fed by new `gitStatus.Upstream/Ahead/Behind/HasRemote`
     (`loadGitTracking`), where the extra `git remote` fork is paid only by
     an untracked branch.
   - **Detached HEAD is refused with a flash** rather than offered a
     `HEAD:refs/heads/x` refspec — this dialog's premise is that the
     current branch is the default answer.
   - Tier 1 `working` reporting is left to Phase 5.3, which builds the
     reporter.
   ~640 LOC incl. tests. Verified against a scratch repo with a local bare
   remote: set-upstream, the rename refspec `main:feature-x` (and that `-u`
   tracks the right-hand side), force-with-lease overwriting, and
   force-with-lease correctly REFUSING with "stale info" once the remote
   moved.
2. **Git history search.** — ✅ done 2026-08-12
   New `gitlogfilter.go` + `gitlogfilter_test.go` (~650 LOC incl. tests).
   All four modes as specified (`--grep -i` default · `a:` author · `p:` path
   with `--follow` · `s:` pickaxe), 250ms debounce, results on a goroutine,
   the prior list held until they land, and filtered rows keep every verb
   (click = diff, Actions ▾ = cherry-pick et al.), so "search history, then
   cherry-pick the hit" is one flow. Deviations, all deliberate:
   - **Its own file, not a bar bolted into gitlog.go** — same file-per-
     feature discipline as the rest of the roadmap; gitlog.go gains only
     geometry (`gitLogBodyRows`/`gitLogBodyTop`), the ⌕ button, and one
     press case.
   - **The query TEXT is the only state.** The mode is parsed out of it on
     demand and the chips REWRITE the prefix rather than setting a flag
     beside it — a chip reading "author" over text reading `p:foo` is a bug
     this design cannot have. Chips therefore double as the syntax legend,
     and the empty-field placeholder spells all four prefixes.
   - **The periodic refresh yields to an applied filter.** The 10s tick
     exists to keep a live view of HEAD honest; a search result is not that
     view, and re-forking a multi-second pickaxe every ten seconds (0.6s on
     ced's own history, measured) would shuffle rows under the pointer.
     `gitLogRefreshNow` (⟳, re-opening the panel) re-runs it explicitly;
     `refreshGitLogCommits` stands down. `applyGitLogCommits` is the shared
     tail both paths install results through.
   - **Sequence-numbered results, not just a debounce**: a slow pickaxe
     landing after a newer fast `--grep` is dropped. Same for the debounce
     tick itself.
   - **No spinner animation** (§6's "don't add animation"): a ` searching… `
     word in the bar's status slot, replaced by ` N matches ` / a red
     ` no matches `. The title switches "commits" → "matches" so a filtered
     panel never misreports the repository, and the empty list says "(no
     matching commits)".
   - **Entry points**: the ⌕ header button (a toggle, lit while open), a
     new ≡ Git row "Search history…", and **Esc-S** — `/` was unavailable
     (Esc-/ is toggle-comment, and the panel owns no keys), and the leader
     is the house's keyboard idiom anyway. Esc-S opens the panel too: it is
     one thought, not two.
   - **The field owns the keyboard only while focused**, and gives it back
     on Esc or on a click outside the panel — stricter than the find bar,
     because a stale focus over a docked panel would type the user's code
     into a search box. Up/Down from inside the field walk the commit list
     (new `gitLogSelect`/`gitLogMoveSelection`), which is the panel's first
     keyboard route into its own rows; Tab cycles the mode.
   - **Pickaxe pre-seed from the selection** as specified, for a single-line
     selection only — the field is one line and could only show a mangled
     version of a multi-line one.
   Verified against a real repo (two authors, two files, a string added then
   removed): each mode selects exactly its commits, the pickaxe finds BOTH
   the appearance and the disappearance, and a term starting with `-` stays
   a term.

   The original spec follows.

   gitlog.go gains a filter bar (`textField`
   primitive, like find.go). `/` or a 🔍 header button focuses it. Modes via
   clickable chip or prefix syntax: default `--grep -i` (message) ·
   `a:` author · `p:` path (follow file history) · `s:` **pickaxe `-S`**
   ("when did this string appear/disappear"), pre-seeded from the current
   selection when invoked with one. 250ms debounce, re-run on goroutine →
   PostEvent (pickaxe is slow — spinner row, keep prior list until results
   land). Filtered rows keep full behavior — click = per-commit diff,
   right-click = actions — so "search history, then cherry-pick the hit" is
   one flow. ~300 LOC.
3. **Cherry-pick / revert polish.** — ✅ done 2026-08-12
   New `gitconflict.go` + `gitconflict_test.go`, plus the right-click
   surface and the confirms in `gitlogactions.go` (~1000 LOC incl. tests).
   All three parts as specified. Deviations and findings, all deliberate:
   - **(a) The anchored menu reuses `editorContextModal`**, the chassis
     the problems panel already borrowed, rather than teaching the fuzzy
     picker to anchor: a right-click has named its subject by where it
     landed, so a query field would ask a question the gesture just
     answered. Rows come from `gitLogActionItems` through a small
     `contextItemsFromPalette` adapter, so the two doors cannot drift
     apart. Right-clicking a commit row SELECTS it first; the detail pane
     opens the menu against the standing selection (it has no row to
     re-aim at); the header and search bar swallow the gesture rather
     than escalating to the ≡ menu.
   - **(b) The confirms needed a multi-line body.** `confirmModal`'s
     single centered line is 50 cells and silently byte-sliced anything
     longer — so the new `openConfirmLines` grows the modal a row per
     line and rides the button row down with it. Found on the way: the
     PRE-EXISTING `reset --hard` confirm, the most destructive verb in
     the panel, was an 80-character sentence being cut mid-warning. Fixed
     to two lines. The truncation is now `elide` (rune-safe, and marks
     the cut) because commit subjects reach this dialog.
   - **(c) The conflict picker's rows are a safety gradient**: open (and
     one row per file, so the picker also answers "which files?"),
     then resolve, then abort last behind its own confirm. The default
     row can therefore only ever open a file.
   - **"Resolved" means the INDEX says so, not that the markers are
     gone.** git refuses `--continue` while any path is unmerged, so the
     marker scan only decides which files are safe to `git add` — staging
     a file that still contains `<<<<<<<` is how conflict markers get
     committed. When every file is marker-free, staging and continuing
     collapse into one row via `runGitCmdSeq`.
   - **The operation is re-derived from the repo on every open**, never
     remembered from the command that failed — the picker is reachable
     long afterwards, and a remembered verb would offer
     `cherry-pick --abort` for a rebase started in a terminal. The
     detection table covers merge and rebase for free, so a `stash pop`
     conflict or a terminal rebase gets the same surface.
   - **`--continue` needs `GIT_EDITOR=true` in the ENVIRONMENT**, which
     cost a new `runGitCmdEnv` / `runGitCmdSeqEnv` pair. Both
     nearer-looking spellings were tested and fail: git rejects
     `--continue --no-edit` as conflicting options, and `-c
     core.editor=true` loses to an inherited `GIT_EDITOR` — so it would
     work on the developer's machine and fail on a user's.
   - **`runGitCmdHook`** is how a conflict is caught: the failure hook
     rides on the done-event (so two commands in flight can't claim each
     other's) and claims the failure ONLY when the repo is left
     mid-operation — "bad revision" still gets the error modal with git's
     own words. It also refreshes the tree, which the failure path never
     did, so the gutters stop describing the pre-conflict work tree.
   - **`git add` gets ABSOLUTE paths.** git reports unmerged paths
     relative to the work-tree root while ced runs git with `-C rootDir`,
     and rootDir is routinely a subdirectory of the repo — the relative
     spelling would silently stage the wrong thing. Pinned by a test that
     opens the repo from a subdirectory.
   - **Entry points**: the right-click, and a new ≡ Git row "Resolve
     conflicts…" gated on the status snapshot's new `ConflictedFiles`
     set (read out of porcelain the snapshot already had, so the
     predicate stays a fork-free field read).
   - Tier 1 `blocked` reporting is left to Phase 5.3, which builds the
     reporter; the hook point is marked in `gitConflictFailHook`.
   Verified against real repos: a cherry-pick and a revert both stopped
   on conflict, the marker scan flipping as the file is fixed, and
   `--continue` actually finishing the cherry-pick — plus a merge whose
   `--continue` proved that git really does launch the editor there.

   The original spec follows.

   (a) Right-click a log row opens the
   existing `gitLogActionItems` picker **at the pointer** (Tier 1;
   `Actions ▾` fallback Tier 0). (b) Confirm modals for cherry-pick/revert
   naming the commit subject + target branch (they currently run
   immediately; house rule: destructive verbs confirm). (c) **Conflict
   handling**: on nonzero exit with conflicts, open a picker — Open
   conflicted files (as tabs, conflicts visible via git gutter) / Abort
   (`--abort`) / Continue (`--continue`, enabled once markers are gone).
   Tier 1 reports `blocked` — a merge conflict is exactly the phone-push
   moment. ~200 LOC.
4. **One-gesture pre-commit survey.** gitpanel already has diff-vs-HEAD +
   per-file checkboxes + Actions. Add:
   - **Walk mode**: "Review all ▶" header button (+ `n`/`p`, wheel) stepping
     file-to-file through every changed file's diff, `3/7 files` indicator.
   - **Reviewed checkmarks** distinct from the stage checkbox; commit button
     shows `Commit (5/7 reviewed)` — a nudge, not a gate.
   - **Hunk verbs in the survey**: per-hunk stage/unstage/revert-hunk click
     targets in the diff gutter (revert-hunk confirms).
   - **Terminal state**: the walk ends on the commit-message field with the
     ✨ AI button adjacent — survey flows straight into commit.
   ~250 LOC.
5. **AI commit message — no-trailer option.** `commitMsgTrailer` config key
   (`internal/userconfig`, string-enum `"on"|"off"` house style) honored by
   gitcommitmsg.go, plus a clickable `[trailer: on]` toggle chip next to the
   AI button for per-invocation override — visible state beats a buried
   setting. ~60 LOC.
6. *(Optional, cut freely)* **Blame layer** via the decoration layer
   (`internal/editor/decoration.go`); click an annotation → that commit's
   diff in gitlog.

### Phase 5 — Deep cats integration (~2–3 weeks, ced side)

1. `internal/cats` package + `cats_glue.go` as specified. ~600 LOC.
2. **⌘ accelerator table** behind the cap (browser-cats first; Mac app after
   upstream routing). ~200 LOC.
3. **Hook reporter — the editor can page you.** `blocked` + custom_status
   when a modal question waits (clobber conflict, cherry-pick conflict,
   trust prompt); `working` during long ops (project search, workspace
   rename, agent turn); `idle` otherwise; release on exit. cats does
   badge/toast/native/ntfy with zero further ced work. ~150 LOC.
4. **Theme unity + identity.** Synthesize a "Cats (host)" theme from
   `config.get` colors onto `internal/theme` slots, auto-selected unless the
   user pinned one; re-poll on `focus_changed` until a `theme_changed` event
   exists upstream. **Emit OSC 7 (cwd) + OSC 0/2 (title) — pull this to week
   one**: two escape sequences, instant payoff (cats chrome, tab names,
   notifications name the file being edited). ~200 LOC.
5. **Splits via the mux.** "Open in split →/↓" (context menu, tab-bar
   right-click, leader) ⇒ `pane.split` spawning an **independent sibling
   ced** on the same file — Phase 2's conflict matrix + autosave already
   make two-ced-one-file safe, so v1 is near-zero work beyond the spawn
   call. Shared-buffer over `internal/remote` is a stretch goal. ~150 LOC.
6. **Real terminal panes.** Tier 1 "Terminal" spawns a real PTY sibling via
   `pane.split`/`tab.create` (grsh remains Tier 0). "Run file" uses
   `pane.wait_for_output` to badge success/failure via the hook.
7. **Frecency navigation.** Merge `path.list` recents (cdx-ranked) into the
   fuzzy finder with a distinct icon; "Recent projects" picker → open tree
   there or `tab.create` a new ced. ~200 LOC.
8. **Agents as collaborators.** Status-bar segment for sibling agent state
   ("claude: working", click → `pane.focus`); context-menu "Send selection
   to ⟨agent pane⟩" → `pane.send_input` (staged, not auto-submitted); "Ask
   cats chat about selection" → `chat.send` with file/line prefix; `capture`
   a sibling pane into a read-only compare tab (diff an agent's proposal
   against your buffer without leaving the editor). ~300 LOC.

### Phase 6 — Upstream-gated polish
Each landed ask gets its consumer: `clipboard.read` → native
compare-with-clipboard + paste-from-system anywhere; `theme_changed` → drop
the poll; `ui.notify` → host-drawn toasts; ⌘ routing → full Mac-app chords.

---

## 4. Compare with clipboard (both tiers)

- **Tier 0, polished now (~80 LOC):** OSC 52 is write-only, so paste stays
  the transport — but package it: a "Compare with clipboard…" context-menu /
  menu row opens the compare panel *pre-armed* in paste-capture mode with a
  one-line instruction ("paste now"); `comparePasteTarget` /
  `comparePasteClip` (compare.go) already do the mechanics.
- **Tier 1 (upstream-gated):** `clipboard.read` control command → ced calls
  it via `internal/cats`, result → PostEvent → compare panel opens fully
  populated. Zero gestures. Gate on capability probe.

## 5. Upstream asks (cats-side — same author; separate track, all optional to Phases 1–4)

1. **`clipboard.read` control command.** catapp already bridges `pbpaste`
   into the page; needs socket exposure + a permission stance (suggest a
   config flag and/or local-session-only) since remote panes could read the
   host clipboard. Unlocks §4 Tier 1 + native paste.
2. **⌘ passthrough policy in the Mac app.** Rather than one-by-one native
   menu items, a "pane speaks kitty" policy: forward all non-reserved ⌘
   chords to kitty-protocol panes. Biggest ask; Esc-leader carries
   everything meanwhile.
3. **`theme_changed` event** in the `events.subscribe` stream, payload = the
   theme section of `ConfigGetResult`. Small. Until then: poll on
   `focus_changed`.
4. **`ui.notify {title, body, actions}`** host-drawn toast. Nice-to-have;
   the hook's `custom_status` covers the attention story. Defer.
5. *(Verify, not build)* the hook server grants full authority to arbitrary
   sources — confirm `source:"ced"` gets badge/sidebar/toast/push per the
   documented custom-source rules.
6. *(Later, if splits feel good)* `pane.open_file` convention — cats asks
   the focused editor pane to open a path (inverse of `ced --remote`):
   "click a file anywhere in cats → opens in ced".

## 6. Risks and tradeoffs

- **Esc-leader vs ⌘:** the leader is the identity (works everywhere,
  tmux-safe) and stays canonical — which-key, menu shortcut column, docs all
  speak Esc-first; ⌘ is a silent bonus layer. Never bind anything ⌘-only.
- **⌘ misfire in non-kitty terminals:** Alt-as-Meta terminals could catch
  chords meant for multicaret. Gate the extended table on Tier 1/kitty
  detection; only C/V/Z stay universal.
- **God-package growth:** `internal/app` is ~30k LOC source and this adds
  ~5k. Discipline: protocol/transport → `internal/cats`; app-side additions
  are new single-responsibility files (statusbar.go, contextmenu.go,
  whichkey.go, completion.go, problems.go, gitpush.go, reconcile.go,
  cats_glue.go) per the file-per-feature + `_test.go` convention; nothing
  new in app.go beyond dispatch lines. A later `internal/git` extraction is
  its own project — don't relocate mid-roadmap.
- **Single modal slot:** completion popup, which-key overlay, pinned
  find-all and the Problems panel all deliberately avoid the modal slot
  (popup / passive overlay / docked panel) so they compose with modals.
  The clobber prompt IS modal — correct, it must block the save.
- **Two-ced-one-file** (splits) leans hard on the clobber matrix — Phase 2
  strictly before Phase 5.
- **Redraw cost:** full-redraw is fine — cats diffs cell-level server-side;
  Tier 0 stays tcell-diffed. Just don't add animation.
- **Event-stream lifecycle:** reconnect with backoff; all failure modes
  degrade to Tier 0 silently. Test with a killed socket.
- **Control-socket security:** filesystem perms are the only auth; treat the
  socket as trusted-local and never proxy arbitrary commands from
  plugins/agents through it.

## 7. Execution order

Phase 1 → 2 → 3.1 (completion) → 4 → 5 → 6, with **Phase 5.4's OSC 7/title
emission pulled forward to week one** (two escape sequences, instant
cats-integration payoff), and Phase 3.2–3.4 slotting in wherever a breather
is needed.

**Progress:** Phases 1, 2, 3.1, 3.2, 4.1, 4.2 and 4.3 are done (all
2026-08-12). Next is **Phase 4.4 — the one-gesture pre-commit survey**
(walk mode through every changed file's diff, reviewed checkmarks, hunk
verbs, ending on the commit-message field), then 4.5 (`commitMsgTrailer`).
The two remaining Phase-3 items are small and unclaimed: 3.3 (hover on
mouse dwell) is Tier-1 only and so really belongs with Phase 5, and 3.4
(recent-files picker) is an hour's work whenever a breather is wanted.

## 8. Verification

- House rules: `_test.go` sibling per file; UI assertions via
  `tcell.NewSimulationScreen` (existing pattern); `make test` green per
  phase.
- Tier detection: run inside a cats pane and in plain Terminal/tmux; confirm
  silent Tier 0 with the socket absent/dead/killed mid-session.
- cats side: `catctl` to inspect pane title/cwd after OSC emission; hook
  state visible as badge/toast; `catctl probe` for headless protocol tests.
- Git suite: scratch repo with a local bare remote — commit, push (incl.
  set-upstream + force-with-lease), cherry-pick/revert incl. the conflict
  path into the conflict picker.
- Clobber matrix: scripted external edits (append/touch while buffer
  dirty/clean; delete file; save-over-newer) → verify every cell of the
  matrix + autosave suspension + write-then-stat.

## Critical files

- `internal/app/app.go` — event loop ~1289, key router ~1935 + ModMeta
  ~2067, mouse router ~2252, reconcile ~1572, status bar ~3719, menu groups
  ~202
- `internal/app/completion.go` + `internal/lsp/completion.go` — the
  non-modal popup and its wire layer. The pattern any future
  live-while-typing surface should copy: keys read off the top of the
  router (not the modal slot), state on App, draw above the panels,
  stamped rects for clicks.
- `internal/app/leader.go` — leaderBinding table (which-key labels, new
  bindings)
- `internal/app/palette.go` + `formmodal.go` — the generic picker and form
  primitives every new surface reuses
- `internal/app/findall.go`, `gitlog.go`, `gitlogfilter.go`,
  `gitlogactions.go`, `gitpanel.go`, `gitcommitmsg.go`, `compare.go` — git
  suite + interactive find-all + compare targets. `gitlogfilter.go` is the
  pattern for any future async-query surface: parsed-from-the-text state
  (no second source for the mode), argv built by a pure function the tests
  pin without forking, sequence-numbered results, and a pipeline refresh
  that stands down while a user-owned query is applied
- `internal/app/problems.go` — the bottom-strip worklist. The pattern any
  future docked list should copy: rows/view indirection with the selection
  preserved by identity, header chips as the only filter UI, one geometry
  source per control, and keyboard twins (next/previous) that walk the same
  list without the panel being open
- `cats/internal/app/command_vocab.go` + `cats/internal/ctlproto/` +
  `cats/internal/app/events.go` — the wire vocabulary `internal/cats`
  mirrors (hand-copied, never imported)
