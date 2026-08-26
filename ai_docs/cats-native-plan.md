# ced × cats: Vision and Phased Roadmap

*The editor and the multiplexer as one IDE.*

Successor to `mini-ide-plan.md` (fully executed 2026-07, then deleted — it
is in git history).
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
  `Capture`, `WaitForOutput`, `ConfigGet`, `PathList`, `ChatSend`,
  `PaneList`, `PaneFocus`. `WaitForOutput` dials through a COPY of the
  client with a wider timeout — it rides the unary envelope but resolves
  only on a match, and widening the shared client would leave every
  unrelated call waiting minutes on a dead socket. Wire structs
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

**Built 2026-08-13 (`metakeys.go`), with two corrections to the paragraph
above, both found by reading the wire rather than by trying it:**

- **⌘E and ⌘click are not in the table.** ⌘E's picker does not exist yet
  (3.4) — ***added 2026-08-13 once 3.4 built it***, so the table is nine
  chords plus ⌘E; its Esc twin is `Esc B`. It was off cats'
  `CMD_TO_PANE` allowlist at first — the same armed-but-host-gated state
  every row here started in — ***added there the same afternoon, cats
  `ed4962c`*** (see §5's follow-up note), so browser-cats forwards it now
  and the chord is live everywhere the rest of the table is.
  ⌘click *cannot* exist: SGR mouse reports carry three modifier
  bits — shift 4, meta/alt 8, ctrl 16 — and none of them is super, so
  cats' encoder drops the Command modifier before writing the report and
  tcell's SGR decoder has no ModMeta path to decode into. A ⌘+click is
  byte-identical to a plain click. Go-to-definition stays Esc-d, and
  ced's one modified click stays Alt+click (multicaret), which owns the
  very bit ⌘ would have had to borrow. Retire this ask — it needs a
  mouse protocol nobody implements.
- **"browser-cats works day one" was wrong.** cats' front end forwards
  only ⌘C and ⌘Z to the pane and returns early on every other Command
  chord (`cmd/catway/web/index.html`: *"leave other Cmd shortcuts to the
  browser"*), so at Tier 1 the table is armed but nothing reaches it —
  in the browser as much as in the mac app. That makes §5 item 2 a
  **two-front-end** ask rather than a Mac-app one, and the browser half
  is the small half: widen one gate to a curated allowlist, since the
  encoder below it already emits `\x1b[<cp>;9u` for super (pinned by
  cats' own encoder tests). **Closed the same day** — cats `77285f9`
  forwards a curated set to kitty-protocol panes only (§5 item 2), so
  browser-cats now carries ⌘S ⌘P ⌘⇧P ⌘F ⌘⇧F ⌘D ⌘G ⌘/ after all, with no
  ced change. Where the layer is live: kitty, Ghostty and WezTerm talking
  to ced directly, and browser-cats from that commit on.

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

**Follow-up — ✅ done 2026-08-13.** After "Decide later" the only route
back to the prompt was an explicit save. The tab's `⚠` now re-raises it.
Landed as specified (a hit-test in the Phase-1 stamped-rect idiom:
`tabRect` grew a `MarkerX`, which the painter and the router now share
so the cell that shows the marker is the cell that answers for it).
Notes vs. spec:

- ***Only `⚠` is a button.*** The slot's other two tenants stay inert:
  `⊘` is answered by saving and `●` by the save verb itself, and a
  marker that WROTE on click would be the tab strip's one destructive
  cell — sitting one column from the filename. They fall through to the
  tab switch a click on a tab has always meant.
- *The raise goes through a shared `raiseConflictNow`, which the save
  guard now calls too: both are the ON-DEMAND path (the user just
  performed a gesture about this file) as against `conflictAfterEvent`,
  which waits until it is sure it is not interrupting. It focuses the
  tab first, because every resolution acts on the ACTIVE one.*

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
3. **Hover on mouse dwell** — ✅ done 2026-08-13.

   *Landed as `internal/app/hoverdwell.go` + tests: rest the pointer on an
   identifier and the LSP's answer appears under it. Notes vs. spec:*

   - ***Ambient, not modal.** The keyboard verb (`menuHoverInfo`) keeps
     the modal slot because someone ASKED; nobody asked for this one, so
     it is a passive popup on the completion popup's layer — it never
     owns the keyboard and never blocks the dialog the user was about to
     open. Same argument the Problems panel makes about diagnostics.*
   - ***The tooltip belongs to the cell it describes.** It anchors under
     the pointer's cell and closes the instant the pointer leaves it — a
     tooltip that outlives what it points at is an obstruction, and this
     one is drawn over the user's code. Typing dismisses it too (and
     cancels the request behind it): a box that pops up mid-keystroke
     because the mouse happened to rest somewhere is the behaviour that
     makes people switch hover off in other editors.*
   - ***Silence is the failure mode.** A server with nothing to say
     produces nothing — no flash, no empty box. `menuHoverInfo` flashes
     "No hover info" because a person asked and deserves an answer.*
   - ***`hoverDwellPos` refuses far more than it accepts.** The pointer
     must be ON an identifier rune: gutter, whitespace, punctuation and
     the space past end-of-line are all cells where `HitTest`'s
     "nearest column" answer — exactly right for a click — would invent
     a symbol nobody is pointing at. "On it" is decided by round-tripping
     through the renderer's own `PosScreenCell` rather than re-deriving
     the gutter offset here, which is also what keeps tabs and
     double-width runes honest.*
   - *Timing/staleness is which-key's shape: a one-shot `AfterFunc` per
     motion posts a tick, and one generation counter invalidates BOTH a
     stale tick and an answer that arrives after the pointer moved.*
   - *Tier-1 gated per the spec, and it survives contact: motion
     reporting is precise and local inside cats. Tier 0 keeps the
     explicit verb, so this is a degraded capability rather than a
     missing one. `hoverDwellArmed` is the single line to widen if it
     should ever light up in bare kitty/Ghostty.*
   - *`hoverModal`'s geometry and painter were split into
     `tooltipSize` / `tooltipPlace` / `drawTooltipBox` so both hover
     surfaces are the same box measured the same way.*

   The original spec follows.

   Tier 1 (motion reporting reliable there).
4. **Recent files picker** — ✅ done 2026-08-13.

   *Landed as `internal/app/recentfiles.go` + tests, with a `Recent []string`
   field on `session.Entry` and a pure `session.TouchRecent` for the ring.
   Notes vs. spec:*

   - *The ring persists **per folder** in state.json beside that folder's
     tab list, rather than being an in-memory ring seeded from the tab
     list. That is the whole difference between this picker and the tab
     switcher (`Esc b`): the rows worth having are the CLOSED files, and a
     ring derived from open tabs would have none of them.*
   - ***The order is the feature.** The ring's head is the file on screen,
     so the picker's first row is the file before it — ⌘E, Enter is the
     two-file flip, which is what the chord means to a hand trained in VS
     Code or GoLand. That is why every activation touches the ring, and
     why the touch lives in `switchToTab` (the one funnel every switching
     surface already goes through) rather than in each surface.*
   - ***`closeTab` deliberately does NOT touch it.** Closing makes a
     neighbour active with nobody having navigated there, and quitting
     closes every tab in turn — hooking it would persist the reverse
     close order at the exact moment `recordSession` writes the file, so
     the list would come back as the one nobody visited. Pinned by a
     test, because the bug only shows up on the NEXT launch.*
   - *Not gated on the session-restore preference: that toggle governs
     reopening tabs, not remembering where the user has been — the line
     folder-recency already drew.*
   - *`openPicker`, not the spec's `openPickerWithCancel` — the latter
     exists for callers that must hear about a dismissal (an agent
     blocked on an answer), and the former is literally that call with a
     nil cancel. Nothing here needs the callback.*
   - *Surfaces: `Esc B` (the shifted twin of `Esc b` — open tabs vs. every
     file you have been in), ≡ → Tab → "Recent files…" directly under
     "Switch tab…", and ⌘E at Tier 1 / kitty hosts. Rows render through
     the tab switcher's own `tabPickerLabel`, so a file that is open looks
     the same in both lists; a file outside the root gets its directory
     spelled out, which the switcher can afford to omit and this list
     cannot.*
   - *Deleted files are pruned when the picker opens (stat per entry, cap
     50), in memory only — the write rides the session at Close like
     every other change. The ≡ predicate deliberately does not stat: menu
     predicates run every draw.*

   The original spec follows.

   MRU ring over `internal/session` data via
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
4. **One-gesture pre-commit survey.** — ✅ done 2026-08-12
   New `gitpanelwalk.go` + `gitpanelhunks.go` (+ tests, ~1300 LOC incl.
   tests), plus the header button, the review column, and the commit
   prompt's new ✦ AI button. All four parts as specified. Deviations and
   findings, all deliberate:
   - **Walk mode steps the panel's EXISTING selection**, rather than
     carrying a second index: "the file being reviewed" and "the file
     whose diff is on screen" are then the same variable and cannot
     disagree. What walk mode adds is the keyboard (`n`/`p`/space/
     Enter/`q`/`h`/`a`/`r`) and the rule that LEAVING a file marks it
     read.
   - **Reviewed marks outlive the walk** and are keyed by path, pruned
     on refresh like `checked` — so the survey survives the thing it
     exists for (click into the editor, fix what you spotted) and the
     button offers `Resume 3/7 ▶`. A press outside the panel ends the
     walk, the same mouse-first focus rule the terminal and chat
     composer follow; Esc ends it as a side effect in the Esc block,
     where the walk's own handler (below the leader block) never sees
     one.
   - **The wheel reads the change set as one document**: at the end of a
     file's diff, one more notch steps to the next file. The detent is
     free — the notch that REACHES the edge clamps and does nothing
     else. It never ENDS the survey, because a commit modal must not
     appear in answer to a scroll; `n` and Enter do that.
   - **The diff pane now fetches git's TWO diffs, not the union.** This
     is what the hunk verbs required: a hunk of `git diff HEAD` belongs
     to neither the index nor the work tree once a file is half-staged
     — and staging one hunk is precisely what MAKES it half-staged, so
     the union supports exactly one hunk-stage per file. `git diff` and
     `git diff --cached` are asked separately; for the (overwhelmingly
     common) one-sided file the bytes are IDENTICAL to the union's and
     nothing changed, and a mixed file gets two labelled sections. The
     union survives as the fallback for shapes neither question claimed
     (it, and the untracked synthesis, report `sideNone` — readable,
     not applicable).
   - **Verbs are gated by what git actually checks.** Unstaged hunks can
     always be staged (`apply --cached`) and reverted (`apply -R`);
     staged hunks can always be unstaged (`apply --cached -R`); reverting
     a staged hunk needs `apply --index -R`, which requires index and
     work tree to agree — so on a mixed file that row is withheld rather
     than offered and rejected.
   - **`git apply` runs at the repo TOPLEVEL.** A patch's `a/… b/…`
     paths are work-tree-root-relative, and `git apply` resolves them
     from the CURRENT DIRECTORY in work-tree mode — while ced routinely
     opens a subdirectory of a repo. Same lesson as 4.3's absolute-path
     finding, from the other end; pinned by a subdirectory-root test.
   - **Chips ride at the right edge of the hunk HEADER row**, not in the
     one-cell gutter the spec imagined — three verbs don't fit one
     column, and body rows would put click targets over the code being
     read. `gitPanelHunkChipsAt` is called by the drawer AND the click
     router (the btnRect rule), and withholds the strip entirely on a
     pane too narrow to carry it; `Hunk actions…` is the Tier-0 twin.
   - **The commit prompt grew an optional third button** rather than
     becoming its own modal: `promptModal` is a generic house primitive
     and an extra button is a general extension of it — unlike
     formModal, whose rows are a config type (4.1's call). It fills the
     field, it does not submit. The glyph is ✦ (U+2726), not ✨:
     `drawAt` advances one CELL per rune and U+2728 is East-Asian-Wide,
     so it would shift the button row through the modal's border.
   - **The nudge goes quiet at 0 reviewed and at all-reviewed**, so the
     one state it describes still registers; it never gates the commit.
   - The walk's terminal commit targets the REVIEWED set, unless ticks
     exist — an explicit selection outranks an inferred one.
   Found on the way: the header title was drawn from the end of
   `Actions ▾`, i.e. straight over any button added beside it. Labels
   now yield to controls, pinned by a test.
   Verified against real repos: staging one hunk of a two-hunk file
   leaves the other alone and turns the file mixed, the second hunk is
   still stageable from the mixed view, unstage takes one hunk back out
   without touching the work tree, revert removes one hunk from the work
   tree only, and the whole thing works with ced rooted in a
   subdirectory. Panels and dialogs were dumped to a `SimulationScreen`
   and read before being trusted.

   The original spec follows.

   gitpanel already has diff-vs-HEAD +
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
5. **AI commit message — no-trailer option.** — ✅ done 2026-08-12
   `commitmsgtrailer` in internal/userconfig (default on), the trailer and
   the chip in gitcommitmsg.go, `coAuthorEmail` on chatAgentDef, and a
   generalized button row in modals.go (~450 LOC incl. tests). Deviations
   and findings, all deliberate:
   - **The trailer had to be BUILT, not just suppressed.** The spec reads
     as an opt-out, but ced never emitted one: the wire prompt asks for a
     bare subject line and `commitSubject` keeps exactly one. So this is
     both halves — `Co-Authored-By: <agent name> <address>` appended to a
     drafted message, and the key and chip that turn it off.
   - **Only a DRAFTED message is attributed**, never one the user typed —
     hence two entry points (`openCommitPrompt` /
     `openCommitPromptDraft`) where there was one. It follows that the
     chip appears only on the drafted prompt: a `[trailer: on]` chip over
     hand-written work would state something untrue that the user cannot
     act on.
   - **The address is deliberately non-routing** (`noreply@` at the
     vendor's own domain, `chatAgentDef.coAuthorEmail`), not the account
     address a forge resolves to an avatar. The trailer is a RECORD that
     a machine wrote the sentence; an address that resolves turns it into
     a mention of an account ced never verified was involved.
   - **The key is lowercase `commitmsgtrailer`**, like every other key in
     the file — and encoding/json matches tags case-insensitively, so the
     camelCase spelling this plan used loads the same key. Pinned by a
     test, because a user copying a key out of a document should get the
     setting they meant.
   - **The chip got a ≡ Git twin** (`AI commit trailer: on/off`), which
     is what PERSISTS. The chip is the per-invocation override the spec
     asked for and writes nothing back — so the preference needed a
     surface of its own, and every other on/off key already has one.
     (One new ≡ row moved the pinned menu counts again: 137→138, 131→132
     + its comment, `dividers[2]` 134→135, Git 18→19 ×2 + its comment,
     140→141.)
   - **The chip's answer travels with the request** (`commitSuggestReq`),
     so asking the agent for another draft does not quietly re-arm a
     trailer the user just switched off.
   - **`promptModal` grew a generic extras ROW**, not a second ad-hoc
     button: `extras[0]` holds the right edge (so the ✦ button does not
     move when the chip joins it), each extra reserves the width of its
     WIDEST label (a toggle whose target resizes slides out from under
     the pointer), and the modal WIDENS to carry them — refusing to on a
     terminal too narrow, where extras shed from the left rather than
     paint a truncated click target. What that costs: at 56 columns the
     chip is gone, so the per-commit override is unavailable there (the
     setting still is, in ≡).
   Verified against a real repository: a drafted message commits with the
   trailer, the same message commits without it after the chip is clicked
   through `handleMouse`, and the button row was dumped to a
   `SimulationScreen` and read before being trusted.
6. **Blame layer.** — ✅ done 2026-08-13 *(the "optional, cut freely"
   item, taken because it was the last unclaimed one)*

   *Landed as `internal/app/gitblame.go` + tests, on a NEW decoration
   primitive it needed built first. Notes vs. spec:*

   - ***The decoration layer could not carry it as it stood.** Blame is
     TEXT the file does not contain: a `Span` can only restyle the
     user's own characters and a `GutterMark` is one cell. So
     `decoration.go` gained a third primitive — `LineAnnotation` +
     `AnnotationSource` — and, unlike the other two, it MAKES ROOM: the
     column opens between the line numbers and the mark cell, and
     `gutterCols()` replaces the `gutterWidth` constant everywhere the
     renderer converts between buffer columns and screen cells. That is
     the whole risk of this item, and it is why `EnsureVisible`,
     `PosScreenCell` and `HitTest` read a width CACHED at render time:
     a hit-test that used a width the screen doesn't have would put the
     caret several characters from the click.*
   - ***It blames the BUFFER, not the file on disk*** (`git blame
     --porcelain --contents -`, text on stdin). Blaming the saved file
     would misattribute every line below an unsaved insertion —
     annotations that are confidently wrong, which is worse than none.
     Lines the user has just typed come back under the all-zero hash
     and are marked `— you now` in the diff gutter's added color.
   - ***The column's width is a property of the file, not the window.***
     Measured once per result over every line. A width taken from the
     visible rows would slide the code sideways as it scrolled.
   - ***One heading per run, re-drawn at the top of the screen.*** The
     first rule was "annotate only where authorship changes", and
     looking at it killed half of that: scrolled into the middle of a
     file written in one commit, the column was eighteen cells of
     nothing. The run's stamp is now re-drawn on the first VISIBLE row,
     so the top row is a heading and the rows below it are boundaries.
     Found by dumping a real blame of a real file to a
     `SimulationScreen` and reading it — the same pass that caught git
     labelling uncommitted lines "External file (--contents)", the
     mechanism leaking into the margin. Who an uncommitted line belongs
     to is decided by the HASH, never by the name git puts on it.
   - ***Staleness is a settle timer, and correctness depends on it***:
     with `--contents`, every answer describes one exact revision of the
     buffer, so results are dropped by a per-path sequence number and
     re-asked (900ms) once typing stops. EditRev is the signature, the
     same one auto-save uses. The previous column stays up meanwhile.
   - ***The click needed a way to name a commit the panel never
     loaded.*** The log holds the newest 400 across `--all`, and the
     commit that wrote the line you are pointing at routinely is not
     among them — so the search bar gained a fifth mode, `c:<rev>`
     ("this commit and the history behind it"). It is the one mode that
     must NOT carry `--all`, which would union the whole repository
     back in and bury the commit asked for. Spelled `c:` and not `#`
     because `#42` is how people search a message for an issue number.
     A commit already in the list is just a selection change.
   - ***A click on the column is not a click on the code***: it reveals
     the commit, moves no caret and starts no drag (the Alt+click
     rule). Every row of a run answers, including the ones whose
     annotation is drawn further up — the column belongs to the line,
     only the ink is shared.
   - *Surfaces: `Esc A` (for git's own other name, `annotate` — NOT a
     shifted twin of `a`, which is the AI namespace and a prefix rather
     than a verb), ≡ Git → "Show/Hide blame", and ≡ Git → "Blame this
     line…", which is the keyboard twin of the click. That row
     deliberately does not switch the column on: it parks the question
     and answers it when git does, because a row about ONE line that
     turned on a whole layer would be the menu answering a question of
     its own.*
   - *Session-only, not persisted: it costs a `git blame` per file
     opened, and a preference that silently forks on every open is not
     one to restore without being asked.*

   The original spec follows.

   *(Optional, cut freely)* **Blame layer** via the decoration layer
   (`internal/editor/decoration.go`); click an annotation → that commit's
   diff in gitlog.

### Phase 5 — Deep cats integration (~2–3 weeks, ced side)

1. ✅ **done 2026-08-12** — `internal/cats` package + `cats_glue.go` as
   specified (detect / client / events / hooks + the app-side seam). Tier
   detection is split: a free env sniff inline at startup, the socket ping on
   a goroutine posting one `catsEvent` back. Verified live against a running
   catway: Tier 1, `ResolvePane("wF:p26") = 289`, `pane.list`, `config.get`
   (theme `cats-green`, 33 colors), `path.list` (34 recents), and the event
   stream connecting.
2. ✅ **done 2026-08-13** — **⌘ accelerator table** (`metakeys.go`): nine
   chords (⌘S, ⌘P, ⌘⇧P, ⌘F, ⌘⇧F, ⌘W, ⌘D, ⌘/, ⌘G) behind a gate that is
   Tier 1 **or** a self-identified kitty-protocol emulator (kitty, Ghostty,
   WezTerm — iTerm2 deliberately excluded: it speaks the protocol but also
   ships the Option-as-Meta setting this gate defends against, and no env
   var separates the two). Command arrives as kitty's super bit, which
   means the rune is UNSHIFTED with Shift in the modifiers (⌘⇧P is
   `'p'+Shift+Meta`, not `'P'`), so `metaChord` folds both spellings into
   one (rune, shift) pair. Two house rules are now enforced by test rather
   than by discipline: nothing may be ⌘-only (every entry's action must
   also be reachable from the Esc table or a ≡ row, checked by reflect
   code pointer), and nothing may claim a cats-reserved chord. One
   adjacent fix rode along: an unclaimed ⌘ chord is now SWALLOWED instead
   of falling through to the editing switch, where it typed its letter
   into the buffer — "⌘S didn't work" must never mean "⌘S typed an s".
   ***⌘← / ⌘→ (line start / end) added 2026-08-25*** — the pair the plan
   never asked for and the first addition that could not be a table row.
   The table is keyed by RUNE and returns early on anything else, while
   these are arrow keys carrying ModMeta, so they dispatch from the
   editing switch beside Alt+Up/Down — where the tab is already in hand
   and a motion belongs. They share the table's gate (`metaLineMotion`
   → `metaAccelArmed`) for the reason the gate exists: Alt+Left is nav
   history here, so an Option-as-Meta terminal would otherwise turn a
   readline reflex into a jump to column 0. `extend` is already read off
   ModShift, so ⌘⇧← / ⌘⇧→ select to the line edge for free, and because
   both motions route through `applyAtCarets` the chord fans out in
   multi-caret mode. Their Tier-0 twins are the End / Home keys, which
   is what makes this legal under the nothing-may-be-⌘-only rule.
   Whether cats' `CMD_TO_PANE` allowlist forwards them is the host's
   call, exactly as ⌘E's was — armed here regardless.
   See the corrections under §2's ⌘ layer for what was dropped (⌘E,
   ⌘click) and why the Tier-1 half is armed but dark.
3. ✅ **done 2026-08-12** — **Hook reporter — the editor can page you.**
   `blocked` + custom_status for a question the user did NOT ask for (disk
   conflict, cherry-pick conflict, formatter trust, agent permission);
   `working` for a chat turn or a project search; `idle` otherwise; release
   on exit. Reported on transitions only. The reciprocal direction landed
   with it: a `pane_notify` from a sibling pane reaches ced's status bar.
   *Not yet marked as blocked* — a workspace rename, format-on-save, and the
   other long ops that are currently invisible to the reporter.
4. ✅ **done 2026-08-12** — **Theme unity + identity.** The OSC half landed
   earlier (`hostident.go`); `catstheme.go` now synthesizes a "Cats (host:
   ⟨name⟩)" theme from `config.get`. Both palettes are built on the SAME
   eight required keys (bg fg muted line accent ok warn err), so the bridge
   is those eight plus `panel`→`sidebar-bg` and `panel2`→`line-hl`, with
   ced's derivation table inventing the other twenty-seven (cats has no
   syntax colors). Non-hex values (cats' rgba() keys) are dropped; a missing
   or non-hex CORE key abandons the synthesis rather than half-translating
   it. Auto-selected only when the user has never pinned a theme, and never
   persisted. Re-polled on `focus_changed` (rate-limited, 3s) until
   `theme_changed` exists upstream. *Verified live: 33 host colors →
   10 mapped → a full resolved palette.*
5. ✅ **done 2026-08-12** — **Splits via the mux.** "Open in split →/↓" (≡
   Cats group, editor right-click, `Esc C r`/`Esc C d`) ⇒ `pane.list` /
   `pane.split` / `pane.list` / `pane.send_input`, spawning an independent
   sibling ced on the same file. `pane.split` returns no data, so the new
   pane is recovered by DIFFING the two listings — an ambiguous diff types
   nothing (see §5 item 7). The sibling is started with `exec ced --root
   <root> <file>`: `--root` is a new ced flag, because a file path alone
   would root the sibling at the file's parent instead of this project.
   Shared-buffer over `internal/remote` remains a stretch goal.
6. ✅ **done 2026-08-13** — **Real terminal panes** (`catsrun.go`). "Terminal
   in a cats pane" splits a real pty running the user's own shell, sent a `cd`
   only when the inherited cwd is not the project root; grsh remains the
   Tier-0 terminal and the row falls back to opening it. "Run file in a cats
   pane…" prompts with a guessed command (per-file memory of the last one),
   runs it as `sh -c 'cd <root> && ( <cmd> ); printf "\n<marker>%s\n" "$?"'`
   and watches for the marker with `pane.wait_for_output` — the exit code
   comes back in ced's status bar, while the in-flight `working` state makes
   cats raise its own "finished" notification on the working→idle edge (a
   failed run is NOT `blocked`: blocked means a question, and a channel that
   pages for answers too gets muted). Three details carry the design: the
   marker's format string (`%s` where the pattern needs `[0-9]+`) is what
   stops the shell's ECHO of the command from satisfying the wait it just
   started; the marker is pid+sequence unique so an old one in the scrollback
   cannot; and the command runs in a subshell so an `exit`/`exec` inside it
   cannot take the reporting line with it. The run pane is remembered and
   reused — unless an agent has claimed it since. Tier-0 fallback: the
   command is staged, unsubmitted, on ced's own terminal input line.
   *Verified live: `capture` and `pane.wait_for_output` against the running
   catway; the exit-code protocol against a real `/bin/sh`.*
7. ✅ **done 2026-08-12** — **Frecency navigation.** `path.list` recents are
   merged into ≡ → File → **Recent folders…** (`catsfrecency.go`): ced's own
   places first (they carry tab counts and were recorded by this program),
   the host's after, deduped, pruned, capped at 24, and marked "· cats" —
   which doubles as a fuzzy search term. The picker now opens on the host's
   list ALONE, so a ced that has never been anywhere still knows your
   projects. "Open project in new cats tab…" spawns an independent ced
   elsewhere via `tab.create` (an argv, so no shell and nothing to quote).
   *Verified live: 34 host recents → 24 rows.*
8. ✅ **done 2026-08-13** — **Agents as collaborators.** *Three quarters
   2026-08-12* (`catsagents.go`): ✅ status-bar segment naming the sibling that most
   wants attention ("claude: blocked +1", ranked blocked > working > idle,
   click → `pane.focus`, our own pane excluded because the hook reporter
   makes cats call it the agent "ced"); ✅ "Send selection to agent…" →
   `pane.send_input` with a `path:12-40` reference and **submit false**,
   permanently (cats paste-encodes, so a multi-line selection lands intact);
   ✅ "Ask cats chat about selection" → `chat.send` with the same quote.
   ✅ *2026-08-13* — **"Compare buffer with agent pane…"** (`catscapture.go`):
   `capture` (scope recent, 2000 lines, unwrapped, no ansi) feeds the compare
   panel as the OLD side, so an agent's proposal is diffed against the buffer
   without leaving the editor. Agent panes are the offer when there are any,
   every other pane when there are none, our own in neither. Only the capture's
   trailing blank screen rows are trimmed — trimming its interior would report
   changes the user never made.

### Phase 6 — Upstream-gated polish — ✅ done 2026-08-13 (for every ask that has landed)
Each landed ask gets its consumer.

**Landed, with consumers built the same day (cats `ff8c89a`):**

- **`clipboard.read` → `internal/app/catsclip.go`.** "Compare with clipboard"
  (≡ Compare) opens the panel already populated — the §4 Tier-1 zero-gesture
  version, falling through to ced's own clipboard and then to the armed paste
  when there is no host. "Paste from host clipboard" (≡ Cats, `Esc C v`) puts
  it at the caret.
  **The finding that shaped the file: the host clipboard is not always the
  user's clipboard.** ced's copy goes out over OSC 52, which cats hands to the
  BROWSER's clipboard; `clipboard.read` reads the machine catway RUNS on. Same
  thing in the mac app and any local session, two different machines the moment
  someone drives a remote catway from a laptop. So nothing reads it ambiently —
  no background refresh of `clipBuf`, and ⌘V is left alone. An ambient sync
  would be delightful nine times in ten and, the tenth, would replace what the
  user copied in their browser with whatever sits on a headless build server.
- **`theme_changed` → the poll is retired, but only once the host proves it
  pushes.** A `theme_changed` frame carries the resolved palette straight into
  `catsThemeArrived`; the first one sets `themeEvented`, which stands the
  focus_changed poll down for the session. It cannot be dropped outright: the
  event vocabulary is not enumerable over the wire, so a host too old to send
  it is indistinguishable from one that simply has not changed theme yet, and
  faith would leave that user's editor permanently out of step.
- **`pane.split` returning its pane + taking an argv → `catssplit.go` is one
  round trip.** The argv is exec'd AS the pane's process, so there is no shell,
  no quoting, no bracketed-paste assumption and no race against a prompt; the
  returned id makes the `pane.list` diff unnecessary. The legacy
  list/split/list/type path stays as the fallback and is told apart with
  nothing to probe: a zero pane means an old host, which means the argv was
  ignored too, because both shipped in the same commit.

**Still outstanding, because the ask has not landed:** `ui.notify` →
host-drawn toasts (§5 item 4, deferred by design — the hook's `custom_status`
covers the attention story); ⌘ routing → the Mac-app chords, which is a
hand-check rather than code (§5 item 2).

---

## 4. Compare with clipboard (both tiers)

- **Tier 0, polished now (~80 LOC):** OSC 52 is write-only, so paste stays
  the transport — but package it: a "Compare with clipboard…" context-menu /
  menu row opens the compare panel *pre-armed* in paste-capture mode with a
  one-line instruction ("paste now"); `comparePasteTarget` /
  `comparePasteClip` (compare.go) already do the mechanics.
- ✅ **Tier 1 (shipped 2026-08-13, both sides):** `clipboard.read` control
  command → ced calls it via `internal/cats`, result → PostEvent → compare
  panel opens fully populated. Zero gestures. One menu row serves both tiers
  (`menuCatsCompareClipboard`) rather than two: "compare with what I copied" is
  one question, and a second row would make the user choose between two
  clipboards they have no way to look at.

## 5. Upstream asks (cats-side — same author; separate track, all optional to Phases 1–4)

1. ✅ **shipped cats-side 2026-08-13 (cats `ff8c89a`)** — **`clipboard.read`
   control command.** `internal/clipboard` reads the host clipboard with
   pbpaste / wl-paste / xclip / xsel; the method is answered by
   `orch.handleClipboardRead` before `app.Dispatcher` sees the name.

   The permission stance landed as **structure, not a flag**: it is a control
   TRANSPORT method (`ctlproto.MethodClipboardRead`), not a §7 command, so the
   browser front end cannot reach it at all — the same boundary `pair` is drawn
   against, applied to the user's clipboard rather than to credentials. No
   config switch on top of that, deliberately: a caller already holding the
   owner-only socket can `pane.send_input` `pbpaste` into a shell pane and
   `capture` the answer, so a flag would gate nothing it does not already have
   and would only make the honest path look more privileged than the dishonest
   one. `catctl clipboard` prints the text raw so it pipes.
2. ✅ **shipped cats-side 2026-08-13 (cats `77285f9`)** — **⌘ passthrough
   policy.** Found while building ced's 5.2: the browser gate
   (`cmd/catway/web/index.html`, `if (e.metaKey && !e.ctrlKey && e.code
   !== "KeyC" && e.code !== "KeyZ") return;`) meant *no* ⌘ chord but C and
   Z reached a pane, so this was never a Mac-app-only gap. Everything
   below that gate already worked — `mods()` sends meta as bit 8,
   `inputenc` maps it to ghostty's `ModSuper`, and the encoder emits
   `\x1b[<cp>;9u` for a kitty-protocol pane and nothing at all for a
   legacy one.

   Shipped shape: a curated `CMD_TO_PANE` set (`KeyS KeyP KeyF KeyD KeyG
   Slash`, matched on `e.code` so Shift rides along for the pane to
   interpret and a non-QWERTY layout still works) forwarded **only to a
   pane whose kitty flags are non-zero**. That second half is the part
   that keeps it from being a regression: a legacy pane cannot receive a
   super chord, so forwarding one there would swallow the user's browser
   shortcut and send nothing. The browser could not tell the two apart, so
   `pane_modes` now carries `kitty` (omitempty — an old client's absent
   field reads as 0, i.e. today's behavior). ⌘W ⌘T ⌘L ⌘R ⌘N ⌘Q stay the
   browser's on purpose.

   ✅ **Follow-up shipped 2026-08-13 (cats `ed4962c`)** — **⌘E** added to
   `CMD_TO_PANE`. ced binds it since 3.4 (recent files) and the allowlist
   predated the binding. It passes the same test as the rest of the set:
   Safari and Chrome spend ⌘E on "use selection for find", which asks a
   CANVAS for a text selection it cannot have, so the browser loses
   nothing measurable. The kitty-flags gate is unchanged.

   ✅ **The mac app was free — confirmed by hand 2026-08-14.** Cocoa
   resolves ⌘ *menu key equivalents* before the WebView, but catapp's
   menus claim only ⌘H ⌥⌘H ⌘Q, Edit's ⌘Z ⇧⌘Z ⌘X ⌘C ⌘V ⌘A and View's
   ⌘+ ⌘= ⌘- ⌘0 — none of which collides with `CMD_TO_PANE` — so the
   chords fall through the responder chain to the WebView and hit the
   same handler. **The "⌘ passthrough needs native menu routing" ask is
   therefore withdrawn**, and shrinks to "only for a chord that collides
   with a menu item", which today is none of them.

   The same hand-check turned up one hole, and only one: **⌘E does not
   reach the page in Chrome**, even though `KeyE` is on the allowlist and
   the running bundle embeds it (verified against the installed binary,
   not the source). Chrome claims the chord for a menu item of its own
   and resolves it before the page, so cats never receives the keydown
   and cannot `preventDefault` what it is never offered — the same
   mechanism as Cocoa's, opposite outcome, because catapp has no ⌘E menu
   item and Chrome does.

   **Everything else arrives.** ⌘S ⌘P ⌘F ⌘D ⌘G were hand-checked in
   Chrome and the mac app on 2026-08-14 and reach the editor in both, so
   the ⌘ accelerator layer is fully live in every host that matters:
   kitty, Ghostty, WezTerm, catapp, and Chrome-but-for-⌘E. Note that
   those five are Chrome menu items too (Save Page As, Print, Find,
   Bookmark, Find Next) — **a menu binding is not the disqualifier**;
   Chrome dispatches the keydown to the page first and honours
   `preventDefault`, which is what lets any web editor claim ⌘S. ⌘E is
   the exception to that, not an instance of it, and `Esc B` is the way
   in when it bites.

   Standing rule for the next chord added: the allowlist was curated by
   *what the browser loses*, which is a proxy for the question that
   decides delivery — *will the host hand it over*. The proxy held for
   every chord but ⌘E. Press a new entry in a real browser rather than
   reasoning it in; the failure is benign (inert in that host, never
   wrong) but silent.
3. ✅ **shipped cats-side 2026-08-13 (cats `ff8c89a`)** — **`theme_changed`
   event** in the `events.subscribe` stream, payload = the theme section of
   `ConfigGetResult` (an alias, so the two can never drift). Emitted from
   `broadcastTheme`, the one funnel `config.set` / `theme.save` /
   `theme.delete` all reach, and deduped against the RESOLVED appearance —
   that funnel runs after every `config.set` including one that only rebound a
   copy-mode key, and a subscriber ACTS on an event where the browser's push is
   merely idempotent restyling. It is cats' first SESSION-scoped event: pane 0,
   so a pane-filtered subscription does not see it.
4. **`ui.notify {title, body, actions}`** host-drawn toast. Nice-to-have;
   the hook's `custom_status` covers the attention story. Defer.
5. ✅ *(Verified 2026-08-12, not built)* the hook server grants full authority
   to arbitrary sources. A `pane.report_agent` from `source:"ced"` against a
   live catway relabeled the target pane `agent: "ced", agent_state:
   "blocked"` in `catctl panes`, and `pane.release_agent` handed it back
   cleanly. The `cats:` prefix is what marks a source reserved (its state is
   detection-driven and its hook state reports are dropped), so an
   unprefixed name is both correct and necessary. Toast/push policy beyond
   the sidebar was not exercised — it is cats-side configuration.
6. *(Later, if splits feel good)* `pane.open_file` convention — cats asks
   the focused editor pane to open a path (inverse of `ced --remote`):
   "click a file anywhere in cats → opens in ced".
7. ✅ **shipped cats-side 2026-08-13 (cats `ff8c89a`)** — **`pane.split`
   returns its new pane.** `{pane}`, not `{num, pane}`: a split happens inside
   the tab the caller is already in, so naming the tab would report something
   the caller told us. Not reply-gated — like `tab.create` the split is worth
   performing for a caller that never listens (the browser's own split button
   sends no id), and the result is a handle rather than the point of the call.
8. ✅ **shipped cats-side 2026-08-13 (cats `ff8c89a`)** — **`cwd` / `command`
   / `env` on `pane.split`**, matching `TabCreateParams` field for field and
   sharing its validator and its workspace-lock rule (a bare split is still the
   user asking for a shell and goes through). "Open in split" is now ONE round
   trip with no shell in the middle.

   Shipping 7 and 8 together is what made the consumer's capability check free:
   a zero pane in the reply means an old host, which means the argv was ignored
   too, so one field answers both questions with nothing to probe at startup.

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

**Progress:** Phases 1, 2, 3.1, 3.2, 4.1–4.5 and **5.1, 5.3, 5.4, 5.5, 5.7**
are done (2026-08-12); **5.6, the last of 5.8, 5.2, 3.4, 3.3, 4.6 and
Phase 2's `⚠` follow-up** landed 2026-08-13, and **Phase 6** the same day.
**Every phase from 1 to 6 is now closed**, with the one caveat Phase 6 is
defined by: it can only consume asks that have landed, and `ui.notify` (§5
item 4) is still deferred by design. §5's items 1, 2, 3, 7 and 8 have all
shipped cats-side; item 5 is verified; item 6 stays retired as impossible.

Every Phase-5 consumer now shares one shape, and it is worth stating once
because the next one should follow it: **poll on a goroutine, cache on the
App, read the cache from the loop.** The theme, the frecency list and the
pane list are all read by main-loop code (a picker, a draw call), and none
of them may dial a socket there. Refresh points are the stream's own events
(`focus_changed`, `pane_notify`), each behind a rate limit.

5.6 added a second shape worth naming, because the pane verbs need it:
**spawn, then watch.** A control call that creates something (a split) is
answered before the thing inside it is ready, and nothing in the API reports
a child process's exit — so a pane is driven by typing a self-describing
command at it and waiting for the marker it prints. The wait is what turns
"the editor typed something somewhere" into a result the hook can page a
human about.

5.2 added a third, and it is the one to keep in mind before promising a
Tier-1 gesture: **the ced side of an integration can be finished while the
feature is still dark.** The ⌘ table is written, tested and armed inside
cats, and not one of its chords can arrive there until cats' own front end
stops holding them back. That is the correct place to stop — the
alternative is either waiting on another repo before writing anything, or
shipping a gate that has to be re-opened by a release. It is also the
reason a feature's status has to name the HOST's half: "done" here means
done in kitty/Ghostty/WezTerm and ready in cats.

3.4 added a fourth, small and easy to get backwards: **an MRU list is only
useful if its head is what is on screen**, which means the touch belongs at
the one funnel every surface already goes through — and means the events
that look like visits but aren't (a close making a neighbour active, a quit
closing every tab in turn) must NOT touch it. The failure mode is invisible
until the next launch, so it is a test rather than a comment.

3.3 added a fifth, and it is the one to weigh before any future surface
that appears without being asked for: **an unbidden surface is defined by
its refusals.** The dwell tooltip's code is mostly places it declines to
ask (gutter, whitespace, past end-of-line, mid-drag, under a menu) and
answers it declines to paint (superseded, wrong file, empty). The
feature is one call to a verb ced already had; everything else is the
work of not being in the way. Its sibling rule: the ambient flavour of a
verb must not inherit the modal flavour's failure behaviour — the
keyboard hover flashes "No hover info" because a person asked, and the
same flash on mouse-rest would be noise the user cannot switch off.

4.6 added a sixth, and it is about where a feature is allowed to push:
**an overlay that needs ROOM is a change to the renderer, not a
decoration.** Spans and gutter marks compose freely because they paint
over cells that already exist; the blame column moves the code, so
every conversion between buffer columns and screen cells has to know
about it, and the only safe way to keep the mouse honest is for the
geometry helpers to read a width the renderer CACHED — geometry
answering about the frame the user is looking at, never about the one
being computed. Its sibling rule, learned the same afternoon: **dump
the surface and read it.** Two of this feature's rules were wrong in a
way no amount of reasoning caught — an empty margin where a file has
one author, and git's own `--contents` mechanism ("External file")
leaking into the column — and both took one look at a real render to
see.

Phase 6 added a seventh, and it is the one that decides how a client
treats a server it did not ship with: **let the answer be the
capability probe.** `pane.split` returning a zero pane means both "this
host cannot name the pane" and "this host ignored your argv", because
the two landed in one commit — so the fallback is chosen by the reply
already in hand, with nothing asked at startup and no version number
anywhere. `theme_changed` is the same rule where no answer exists to
read: the event vocabulary is not enumerable over the wire, so the poll
it replaces stands down only once a frame has PROVED the host pushes,
and a host too old to send one keeps the old behaviour forever without
being asked about it. The failure this avoids is the quiet one — a
client that assumed the new shape does not error, it just silently
stops doing the thing.

Its sibling, from the clipboard: **name the place, do not name the
concept.** "The clipboard" is one word for two machines the moment a
browser and a catway are on different boxes, and the verb that reads
one of them is only safe as an explicitly-requested read of that named
place — never as an ambient answer to "what did the user copy?".

**Phases 1 through 6 are all closed.** What remains is not code: the ⌘
chords hand-checked end to end in browser-cats (the running catway is
an older binary) and in the mac app, where the analysis says there are
no menu collisions; and §5 item 4 (`ui.notify`), deferred on purpose.

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
- `internal/app/hovermodal.go` + `internal/app/hoverdwell.go` — one verb
  with a modal flavour and an ambient one, sharing `tooltipSize` /
  `tooltipPlace` / `drawTooltipBox`. The pattern for any future surface
  the user did not ask for: tie its lifetime to whatever summoned it,
  refuse loudly in code and silently on screen, and never let it take
  the modal slot
- `internal/editor/decoration.go` — the three overlay primitives.
  `Span` and `GutterMark` paint over cells that exist; `LineAnnotation`
  (with `AnnotationSource`) opens a column and moves the code, which is
  why it is the only one the renderer's geometry has to know about. Any
  future margin (a coverage strip, a review comment) belongs here, and
  should take the same all-or-nothing width and cached-geometry rules
- `internal/app/gitblame.go` — the annotation column's one consumer, and
  the pattern for anything that annotates a buffer from an expensive
  external command: blame the BUFFER (never the saved file), measure the
  column over the whole file so it cannot jitter, drop answers by
  per-path sequence, and re-ask on a settle timer keyed to `EditRev`
- `internal/app/problems.go` — the bottom-strip worklist. The pattern any
  future docked list should copy: rows/view indirection with the selection
  preserved by identity, header chips as the only filter UI, one geometry
  source per control, and keyboard twins (next/previous) that walk the same
  list without the panel being open
- `internal/app/catsclip.go` — the host clipboard, and the pattern for any
  future verb that reads something OUTSIDE the editor on the user's behalf:
  name the place rather than the concept, never read it ambiently, and
  snapshot the target (tab + `EditRev`) at the moment the user asked so a
  late answer declines instead of landing where the caret drifted to
- `internal/app/catssplit.go` — the two-vintage host. The pattern for
  consuming any upstream ask: send the new shape unconditionally, let the
  REPLY be the capability probe, and keep the old sequence as the branch it
  falls into — a client that assumes the new shape does not error, it
  silently stops working
- `cats/internal/app/command_vocab.go` + `cats/internal/ctlproto/` +
  `cats/internal/app/events.go` — the wire vocabulary `internal/cats`
  mirrors (hand-copied, never imported). `ctlproto` is also where the
  methods that are NOT §7 commands live (`pair`, `clipboard.read`): a
  transport method is answered before cats' dispatcher sees the name, which
  is what keeps it off the browser's reach
