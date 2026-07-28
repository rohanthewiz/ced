# Session: Copilot connect fixes + chat model picker

Session ID: 28e97b1e-fd19-4dd0-9ff6-bd7842e8ef8c
Date: 2026-07-27

### Ask

> "Help me get connected to github copilot using device auth" → then
> "How can I select the copilot model? Add the necessary UI elements"
> and "add the Show Chat under Copilot in the editor menu".

First real-binary shakedown of the phase 1–3 Copilot integration (all
three phases were built against fakes — `copilot-language-server` was
never installed on this machine until this session). Two real bugs
fell out, then the model-picker feature went in.

### Environment setup (this machine)

- Installed `@github/copilot-language-server` (v1.526.0) via npm; npm's
  global bin (`~/node/bin/bin/`) isn't on PATH, so symlinked the binary
  into `~/bin/`.
- No device flow was actually needed: `~/.config/github-copilot/apps.json`
  already held OAuth records for `Rohan-Allison_cbre` (enterprise account
  on github.com, not a GHE host) under the exact app id the server uses
  (`Iv1.b507a08c87ecfe98`). The server picked them up on first start.

### Bug 1 — sidecar spawned without `--stdio` (commit 07263e9)

The npm/native server REQUIRES `--stdio` and exits instantly with a
usage error without it. r-ed spawned it with nil args → instant
`copilotExitEvent` → dead verdict → the misleading "install
copilot-language-server on PATH" flash even though LookPath passed.
Fix: `copilotServerArgs = []string{"--stdio"}` (extracted var, used at
the spawn) + `TestCopilotServerArgsIncludeStdio` pinning the flag —
every fake-conn test stays green if the flag is dropped, hence the pin.
The chat agent already passed `--acp`, unaffected.

### Bug 2 — 5s handshake timeout vs the macOS Keychain prompt (commit b37bf50)

With the server actually starting, its cold start blocks on
credential-store resolution — on macOS a Keychain password prompt the
user has to answer. The `initialize` call ran on the 5s `Call` default,
so the handshake timed out while the dialog was up, closed the healthy
server, and re-cached the dead verdict. Fix: `copilotInitTimeout = 2 *
time.Minute` applied to `initialize` in BOTH handshakes (LSP sidecar in
copilot.go, ACP chat in copilot_chat.go — same binary, same cold
start). Everything after initialize keeps the 5s budget. Pinned by
`TestCopilotInitTimeoutOutlivesKeychainPrompt`. Advise "Always Allow"
on the Keychain dialog. This unblocked the user — Copilot connected
with the existing enterprise credentials, no device flow.

### Feature — chat model selection (uncommitted at doc time, committed by this wrap)

Probed the real ACP agent first: `session/new` returns
`models.availableModels` (`modelId`, `name`, `_meta.copilotUsage`
multiplier) + `currentModelId`; `session/set_model {sessionId,
modelId}` switches the live session (verified by hand over ndjson).
Roster today: Auto (default claude-sonnet-4.6), Claude Sonnet 4.5/4.6/5,
Haiku 4.5, GPT-5.x family, Gemini 3.x, MAI-Code-1-Flash.

Implementation (all in house patterns):

- `chatModel{id, name, usage}`; roster + `modelID` on `chatState`,
  carried by `chatReadyEvent`, cleared in `chatDisconnect`.
  `chatInitialize` now returns a `chatSession` struct and decodes the
  models block.
- ≡ Copilot row "Chat model: <Name>" (`menuChatModel` /
  `chatModelLabel`) → `openPicker` fuzzy list (branch-switcher rules:
  current model excluded, named in the title; rows show the spend
  multiplier, e.g. "GPT-5.5  (7.5x)"). Pick → async
  `session/set_model` → `chatModelSetEvent` → transcript note + flash.
- Click before the agent is up queues via `modelPickWanted`
  (queuedPrompt pattern); handleChatReady opens the picker on arrival.
  Disabled/dead states flash why (menuCopilotAuth rule).
- **Persistence**: new `"chatmodel"` config key (`userconfig.ChatModel`,
  `SaveChatModel`, un-validated — roster is server-defined).
  `chat.modelPref` is loaded at startup, applied during every handshake
  via set_model; a stale id or failed set is silently skipped (must
  never break the handshake). Pref survives disconnects.
- User's machine set to `"chatmodel": "claude-sonnet-5"` by hand-edit
  of `~/.config/r-ed/config.json` (their requested default).

### Menu move — Show Chat under Copilot (owner decision)

The chat show/hide toggle moved from the View group to the Copilot
group; Copilot section order is now: Sign in → Show/Hide chat → Chat
model → suggestions toggle → Enable/Disable Copilot. This deliberately
overrode the old "chat toggle stays above the fold in View" pin
(`TestMenuLayout_TerminalRowsAboveTheFold` no longer lists it) — with
the collapse-by-default menu the section header keeps it one click
away. Geometry pins updated: 2 top-zone + 52 actions + 10 headers = 64
rows, height 70, dividers [2, 5, 67] (CLAUDE.md numbers updated too).

### Tests added

- `TestChatInitialize_ModelRoster` — a `fakeACPAgent` speaking real
  ndjson over `io.Pipe` + `lsp.NewClientACP` exercises roster decode,
  pref-apply, stale-pref skip, and failed-set swallow. New pattern:
  first test of `chatInitialize` against real framing.
- Label states, unavailability flashes, queued picker, picker
  row/title rules, `session/set_model` wire shape, set-handler
  success/failure (+ config write via `t.Setenv XDG_CONFIG_HOME`),
  disconnect scope, userconfig round-trip.

### State / follow-ups

- Suite green under race detector; `./r-ed` and `bin/r-ed` rebuilt.
- Follow-up candidates: a picker entry surfacing `modes` /
  `configOptions` (session/new returns those too, unused); showing the
  model in the chat panel header; multi-line composer (known).
