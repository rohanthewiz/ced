# Session: AI docs rewrite + read-only chat switch

Session ID: 6e7409d1-886e-49aa-afb0-d09e9b0aefb7
Date: 2026-07-29

### Ask

> "It is unclear how to use the Copilot agent"
> → "yes, rewrite the AI section to match phases 3-4"
> → "verify those npm package names"
> → "I do want the option to disable agent writing as a whole. We
>    already have a switch for reading (including context), so do
>    similarly for writing, so we can operate in a safe chat mode"

Two deliverables: bring the README's AI documentation current with
phases 3–4, then add a read-only-chat switch.

### Part 1 — why it read as unclear

The complaint was well-founded. `README.md`'s AI section still described
phase 2:

- It pointed at **≡ → View → Show Copilot chat**; the toggle had moved
  into the ≡ Copilot group (owner preference, one block).
- It claimed chat was **"read-only by design — requests to touch your
  files are automatically declined"**, which phase 4 replaced with real
  permission prompts and root-confined fs read/write.
- It never mentioned the **agent registry** at all — Claude Code and
  Gemini were invisible — nor context attachments, the model picker, or
  transcript copy.

Plus a discoverability issue independent of the docs: `seedMenuFoldDefault`
collapses every ≡ section on first run, so the Copilot group is a single
folded header.

### Part 2 — the rewritten section

`## AI features (chat agents + Copilot completions)` — renamed away from
"GitHub Copilot" since the panel is agent-agnostic. Seven steps:

1. **Install an agent binary** — table of all three backends. Copilot's
   native-binary path preserved for Node-free installs.
2. **Pick your chat backend** — the picker, `"chatagent"` persistence,
   and re-picking-the-current-agent as the crash/retry gesture. Carries
   the callout that the menu opens folded.
3. **Authenticate** — Copilot's device flow verbatim from the old text,
   plus Claude Code's and Gemini's CLI/env-var paths, and what a failed
   handshake writes into the transcript.
4. **Inline suggestions** — unchanged behavior, now explicitly scoped as
   Copilot-sidecar-only and independent of the chat backend.
5. **Chat panel** — correct menu location, model picker, the ⧉ copy
   affordances, drag-select + Cmd+C, left-edge terminal exclusivity.
6. **Context attachments** — pushed-not-fetched, selection-beats-file,
   buffer-not-disk, per-turn clearing, chip ✕ semantics, 64 KB cap.
7. **Read/write permissions** — replaces the stale read-only paragraph.

Also updated the features bullet (no longer Copilot-only) and its anchor
link, and the "Turning it off" block now lists every chat config key,
spelling out that `"copilot": "off"` gates GitHub's binary only.

**npm package names were verified against the registry** (`npm view … bin`),
not asserted from memory — each `bin` name matches what `chatagent.go`
resolves on PATH:

| Package | Version at check | `bin` |
| --- | --- | --- |
| `@zed-industries/claude-code-acp` | 0.16.2 | `claude-code-acp` |
| `@google/gemini-cli` | 0.53.0 | `gemini` |
| `@github/copilot-language-server` | 1.526.0 | `copilot-language-server` |

### Part 3 — read-only chat (`"chatwrite"`)

The write-side twin of the `"chatcontext"` read switch. **Default on** —
this is an opt-in posture, not a behavior change.

**Three enforcement points, because capabilities are advisory:**

1. **Handshake** — `chatInitialize` took a new `allowWrite` param and
   declares `fs.writeTextFile: allowWrite`. The honest signal: an agent
   that knows it cannot write plans differently instead of proposing
   edits that would only be refused.
2. **fs handler** — `handleChatFSRequest` refuses `fs/write_text_file`
   before the path even resolves, with an error the agent can read and
   relay.
3. **Permission gate** — `handleChatPermRequest` auto-rejects any request
   whose `toolCall.kind` is in `chatMutatingKinds`, using the agent's own
   reject option, with a `⊘ rejected (read-only chat): <title>` line in
   the transcript. Never queued, never shown — prompting for something
   the user already said no to is a question with one honest answer.

Point 3 is the one that matters for non-Copilot backends: Claude Code and
Gemini write through their OWN tools, not ACP's fs, so the permission
request is the only chokepoint ced sees. This is also why
`chatParsePermission` now decodes `toolCall.kind` (normalised) — it is
the only signal available about whether saying yes would change anything.

**Two deliberate judgment calls, both flagged to the owner:**

- **`execute` is in `chatMutatingKinds`** (with edit/delete/move). A
  shell command is a write with extra steps; letting one through would
  make the mode read-only in name only.
- **Unlabelled / unrecognised kinds still prompt.** Auto-rejecting
  everything an agent forgot to label would make the mode useless rather
  than safe, and ced's own write path is refused regardless — that
  refusal is the guarantee the mode actually rests on.

**Live vs handshake semantics.** The enforcement paths read the flag
live, so blocking takes hold on the very next request including one
already queued. The declared capability is a handshake artifact, so
`setChatWrite` appends "restart the agent to update what it was told it
can do" to its transcript note when a session is attached — rather than
letting the user assume a reconnect happened.

### Files touched

- `internal/userconfig/userconfig.go` (+test) — `ChatWrite` field,
  `chatwrite` key, parse switch, `SaveChatWrite`, doc block.
- `internal/app/copilot_chat.go` — `chat.writeEnabled`, `allowWrite`
  threaded through `chatEnsureStarted` → `chatInitialize`, capability
  declaration, header-comment scope note.
- `internal/app/copilot_chat_perm.go` (+test) — `chatMutatingKinds`,
  `chatWriteEnabled` / `chatWriteBlocked`, the toggle + label +
  `setChatWrite`, both enforcement hooks, `kind` on `chatPermRequest`.
- `internal/app/app.go` — ≡ Copilot menu row, `cfg.ChatWrite` seeding.
- `internal/app/app_test.go` — `newTestApp` seeds `writeEnabled = true`
  (hand-built Apps would otherwise silently run every test in read-only
  mode); menu geometry pins 71→72 rows, height 77→78, dividers
  `[2,5,74]`→`[2,5,75]`.
- `README.md`, `CLAUDE.md` — the rewrite above, plus the phase-4 house
  rule for the switch and the refreshed geometry pins.

### Tests

New coverage: the gate matrix over every kind in both modes,
auto-reject path (reply + no modal + transcript), reads-still-prompt,
fs write refused with nothing on disk while reads keep working, toggle
persistence + no-op silence, both handshake capability directions (the
fake ACP agent now records the declared `writeTextFile`), and the
`chatwrite` config key (values, case tolerance, invalid, round-trip
preserving unknown keys).

`make test` green with `-race`; gofmt and vet clean.

### Notes for next time

- The ≡ Copilot group is now 11 rows. Menu geometry pins live in
  `TestMenuLayout_NoCustomActions` **and** `TestMenuLayout_WithCustomActions`
  (baseline + 3) — both need updating when a row is added, and CLAUDE.md
  repeats the numbers.
- If read-only chat should stop blocking `execute`, it is one entry in
  `chatMutatingKinds`.
