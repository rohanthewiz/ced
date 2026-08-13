# Session: cats-native plan, Phase 5.2 — the ⌘ table, and what the wire would not carry

- Session id: `ed2482f7-91e8-4d6d-a0aa-1600345c3302`
- Date: 2026-08-13
- Branch: `main`
- Worked from the cats checkout (`~/projs/go/cats`) against the ced repo
  (`~/projs/go/ced`), which is where every change landed.
- Plan: `ai_docs/cats-native-plan.md` §Phase 5 — **5.2 now ✅, so Phase 5
  is closed.**
- Predecessor: `2026-0813-1218-cats-native-phase5-6.md`

## What was asked

Load the last session, continue with the remaining Phase 5 items. Exactly
one was left: 5.2, the ⌘ accelerator table.

## What landed

```
internal/app/metakeys.go       + test   the ⌘ table, its gate, the fold
internal/app/app.go                     one dispatch line + one swallow
ai_docs/cats-native-plan.md             5.2 done; two corrections; §5 item 2 rewritten
```

`make test` (race) green; `go vet` clean; `gofmt` clean apart from the four
pre-existing offenders (`hostident_test.go`, `reconcile.go`, `tabbar.go`,
`editor/syntax.go`).

Nine chords: ⌘S save, ⌘P find file, ⌘⇧P palette, ⌘F find, ⌘⇧F find in
project, ⌘W close tab, ⌘D duplicate line, ⌘/ comment, ⌘G go to line.

## The two things the plan had wrong, and how they were found

Both by **reading the wire at both ends before writing the feature** —
cats' front end and encoder, tcell's parser — rather than by binding keys
and pressing them. Neither would have shown up as a failing test; both
would have shown up as a chord that silently does nothing.

### (a) ⌘click go-to-definition cannot be built. Not "not yet" — cannot.

SGR mouse reports carry exactly three modifier bits: shift 4, meta/alt 8,
ctrl 16. There is no super bit. cats' encoder maps Command to ghostty's
`ModSuper` and then hands the event to `MouseFormatSGR`, which has nowhere
to put it (its test matrix has shift and ctrl+alt cases and no super case,
which is the tell); tcell's own SGR decoder likewise decodes only those
three, so it has no ModMeta to produce. **A ⌘+click is byte-identical to a
plain click.** The ask is retired in the plan rather than deferred.

The irony worth keeping: bit 8 — the one ⌘ would have had to borrow — is
already spent on Alt, which is ced's multicaret click.

### (b) "browser-cats works day one" was wrong; the browser is a gate too.

`cmd/catway/web/index.html`:

```js
if (e.metaKey && !e.ctrlKey && e.code !== "KeyC" && e.code !== "KeyZ") return;  // leave other Cmd shortcuts to the browser
```

Only ⌘C and ⌘Z reach a pane. So at Tier 1 **nothing in this table can
arrive today**, in the browser as much as in the mac app (where Cocoa
resolves ⌘ menu equivalents before the WebView is offered them). §5 item 2
was written as a Mac-app ask; it is a two-front-end ask, and the browser
half is the small one — everything below that gate already works (`mods()`
sends meta as bit 8 → `inputenc` → `ModSuper` → `\x1b[<cp>;9u`, and a
legacy pane gets nothing at all, so widening cannot break a shell).

The curation is the real work and it is a cats-side product call: ⌘W, ⌘T
and ⌘L belong to the BROWSER, and swallowing them to hand a pane a
shortcut is a bad trade. The plan now suggests a first allowlist of the
chords no browser needs and ced already binds: ⌘S ⌘P ⌘⇧P ⌘F ⌘⇧F ⌘D ⌘/ ⌘G.
**Not done — it is a change to the other repo and it is the user's call.**

## The design

**The rune arrives UNSHIFTED.** Command reaches a terminal program only
through the kitty keyboard protocol (tcell asks for it at startup:
`CSI > 1 u`), and the CSI-u parameter is kitty's *unicode-key-code* — the
lowercase codepoint — with Shift in the modifier bits. So ⌘⇧P is
`('p', Meta|Shift)`, not `('P', Meta)`. Other emitters send the shifted
rune instead. `metaChord` folds both spellings into one `(rune, shift)`
pair so the table says each chord once; the existing Cmd+Z branch had been
handling this informally with two switch cases.

**The gate is Tier 1 OR a self-identified kitty-protocol emulator** (kitty,
Ghostty, WezTerm, by TERM / TERM_PROGRAM / marker env). iTerm2 is
deliberately excluded: it speaks the protocol in recent versions but also
ships the Option-as-Meta setting the gate exists to defend against, and no
env var separates the two configurations. Losing an accelerator there costs
nothing (Esc still works); a phantom save costs a lot.

Arming it at **Tier 1** is deliberate even though the chords cannot arrive:
the encoder underneath already speaks super, so the day cats widens its
gate these light up **with no ced release**. A gate that waited for that
day would have to be re-opened by hand.

**Two house rules became tests instead of discipline:**

- `TestMetaAccelsAreNeverTheOnlyPath` — every action in the table must
  also be reachable from the Esc-leader table or a ≡ row. Function values
  aren't comparable in Go, so identity goes through `reflect…Pointer()`,
  which is exactly the question being asked: is this the same function the
  other surface calls? A ⌘-only binding now fails the build.
- `TestMetaAccelsAvoidReserved` — ⌘K ⌘B ⌘V and the font chords are cats';
  ⌘C ⌘V ⌘Z are handleKey's own, and stay there because they are
  context-sensitive (compare panel, chat composer, terminal each claim
  their own paste) in a way a flat table cannot express.

**⌘E is not bound.** Its picker doesn't exist yet (3.4). A chord that
flashes nothing reads as a broken editor, not an unbuilt feature.

**One adjacent bug fixed on the way past.** An unclaimed ⌘ chord used to
fall through to the editing switch, where `KeyRune` inserts the rune — so
in any host that forwards Command, ⌘S typed an `s` into the user's code.
The ModMeta branch now returns rather than falling through: a Command
chord is never text. ModAlt deliberately keeps its fall-through, because
Option-as-Meta typists insert real characters with it.

**Precedence.** The table dispatches at the TOP of the ModMeta branch, on
the same principle as the leader table above it: ⌘S must save whether the
keyboard currently belongs to the editor, the chat composer or the
terminal panel (all of which return early below). It cannot shadow c/v/z —
they are on its reserved list and a test says so. An open ≡ menu is the one
exception: it owns the keyboard, so the layer stands down (the chord is
still swallowed, so the menu's fuzzy search doesn't gain a stray letter).

## Verification

- `make test` (race) green, `go vet` clean.
- Read-only reading of the live cats checkout for the front-end gate and
  the encoder path; `catctl pane` confirms the control API exposes no
  kitty-flag field, so the pane's keyboard mode can't be asserted live
  from here.
- **The wire encoding is NOT asserted by a test, and cannot be with this
  tcell.** The only way to feed raw bytes to its parser from a test is
  `SimulationScreen.InjectKeyBytes`, which in v2.13.9 does not run the CSI
  parser at all — it maps bytes one at a time, and its control-byte branch
  never advances the slice, so an ESC spins forever posting `KeyESC` and
  blocks the caller on a full channel (cost me one hung test run; the
  attempt is recorded in metakeys.go's header so nobody repeats it). The
  encoding claim rests on reading both ends instead: tcell's
  `calcModifier` (bit 8, "kitty calls this Super" → ModMeta) and cats'
  encoder test pinning `\x1b[97;9u` for ⌘A.

## State / next steps

- **Phase 5 is closed.** Phase 4 was already. What is left anywhere is
  small and unclaimed: 3.3 (hover on dwell), 3.4 (recent-files picker —
  the only thing between ⌘E and its row), 4.6 (blame, optional), and
  Phase 2's `⚠` tab marker re-raising a deferred conflict prompt.
- **Phase 6 is entirely upstream-gated, and §5 item 2 is now its critical
  path.** Its browser half is one gate and an allowlist; ced is ready for
  it today. Awaiting the user's call on whether to make that change in the
  cats repo.
- Other upstream asks unchanged: `clipboard.read` (item 1), `theme_changed`
  (item 3), `pane.split` returning its pane (item 7) and taking an argv
  (item 8), plus 5.6's addition — nothing reports a pane child's exit.
  Item 6 (⌘click) is now retired as impossible.
- Third shape, named in the plan for the next integration: **the ced side
  of an integration can be finished while the feature is still dark** —
  and a feature's status has to name the host's half. "Done" for 5.2 means
  done in kitty/Ghostty/WezTerm and ready in cats.
