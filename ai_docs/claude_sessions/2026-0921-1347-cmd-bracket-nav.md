# Session: ⌘[ / ⌘] walk ced's own nav history (and why the mouse can't)

Session ID: `49bcf521-e022-445e-a3a5-b0e65a3305dc`
Date: 2026-09-21
Branch: `main`

## Ask

> In the Cats orchestrator which is the native env for cats-todo the keys
> CMD+] and CMD+[ and the mouse back and fwd buttons navigate across Cats.
> However, when we are in CEd I want those keys to navigate among file
> positions within CEd itself

## What shipped

Two rows on the ⌘ accelerator layer (`internal/app/metakeys.go`), plus
their tests:

```go
{key: '[', action: (*App).menuNavBack, label: "Go back"},
{key: ']', action: (*App).menuNavForward, label: "Go forward"},
```

They fire the same file-navigation history (nav.go) that Alt+←/→ and
Esc-o/O already reach, so `TestMetaAccelsAreNeverTheOnlyPath` is
satisfied without argument — both verbs have leader keys AND ≡
Navigation rows.

New tests in `metakeys_test.go`:

- `TestMetaAccelNavigatesHistory` — open two files, ⌘[ lands on the
  first, ⌘] returns to the second.
- `TestMetaAccelNavIsGated` — the same chord on a disarmed host does
  nothing, the standing defence against a terminal folding Option into
  Meta.

`go test ./...` green.

### Why this pair belongs on the ⌘ layer more than any other row

Every other entry there is a *second* door onto a keycap the Esc table
already offers. This one is the only pair whose keycaps the Esc leader
structurally **cannot** offer: `\x1b[` is the CSI introducer and `\x1b]`
is OSC, so `Esc [` / `Esc ]` are eaten by the terminal before tcell sees
them (the trap CLAUDE.md's tab-strip section already records — it is why
tab switching is `Esc ,` / `Esc .`). So the letters Esc-o / Esc-O stay
the guaranteed path and ⌘[ / ⌘] add the keycaps the rest of the
ecosystem trained the user's hands on. The never-⌘-only rule holds; the
keycap-only-here fact is new and is written into the code comment.

## What did NOT ship, and why — the cats side

Investigated `/Users/RAllison3/projs/go/cats` read-only. Both halves of
the ask are blocked there, not here.

### ⌘[ / ⌘] are claimed by cats before the pane sees them

`cmd/catway/web/js/20-keys.js:111` intercepts BracketLeft/BracketRight
with Meta (or Ctrl+Alt off-mac), `preventDefault`s, and sends
`nav.back` / `nav.forward` to the server — cats' own cross-pane,
cross-tab, cross-workspace focus history. The allowlist that would
forward a chord to the pane is:

```js
const CMD_TO_PANE = new Set(["KeyS","KeyP","KeyE","KeyF","KeyD","KeyG","Slash"]);  // :56
```

gated on the focused pane having asked for the kitty protocol (`:66`).
Brackets are not on it. `internal/inputenc/inputenc.go:124` *can* encode
them; the front end simply never sends the ⌘-modified form.

So inside cats the new binding is **armed and dark** — exactly the state
⌘E was in before cats `ed4962c` added `KeyE` to that set. It is live
today in kitty, Ghostty, WezTerm and the mac app.

### The mouse thumb buttons cannot reach ced at all, at two layers

1. **cats never forwards them.** The front end grabs buttons 3/4
   window-wide in the capture phase and routes them to
   `nav.back`/`nav.forward` (`20-keys.js:183`); the pane mouse handler
   drops `ev.button > 2` (`24-upload.js:150`); and the wire has no
   encoding for them at all — `wire/up.go:88` is left/middle/right/none,
   and `internal/inputenc/encoder.go:294` rejects anything else. The only
   buttons above 2 cats emits are the wheel pseudo-buttons 4–7.

2. **tcell could not decode them even if it did.** `handleMouse`
   (tcell v2.13.9 `input.go:669`) switches on `btn & 0x43`, so X11
   buttons 8/9 — SGR 128/129 — collapse to `Button1` and `Button3`
   respectively. A thumb press would arrive indistinguishable from a
   plain left click, and a forward press from a middle click. Nothing
   ced can do while tcell owns the tty.

This is the mouse-report ceiling metakeys.go's header already documents
for ⌘+click, hit from the other direction: that gesture had no super bit
to carry, this one has no button code.

## Proposed cats change (asked, not done)

Left as a question for the owner, since it is a different repo and it
trades a cats-level gesture away:

- add `BracketLeft`/`BracketRight` to `CMD_TO_PANE`, gated as the rest
  are on the pane's kitty flags — ced is already listening;
- for the thumb buttons, have cats **translate** them into that same
  ⌘[ / ⌘] key sequence for a kitty-capable focused pane, rather than
  teaching the wire a new button. That sidesteps the tcell hole
  entirely and mirrors the trick cats already uses spelling ⌘+click as
  ctrl+alt.

Open question put to the owner: unconditional for kitty panes, or
something narrower? The cost either way is losing cats-level cross-pane
back/forward on those gestures inside such a pane.

## Files touched

- `internal/app/metakeys.go` — the two rows + the rationale comment
- `internal/app/metakeys_test.go` — two tests
