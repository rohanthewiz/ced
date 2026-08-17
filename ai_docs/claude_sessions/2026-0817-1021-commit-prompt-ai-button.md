# Session: the AI button was there all along — nobody could see it

- Date: 2026-08-17
- Branch: `main`
- Repo: ced (`~/projs/go/ced`)
- Session id: `51f2a3c3-7b59-4a89-9839-199153ae888a`
- Predecessor: `2026-0814-1422-undo-survives-autosave.md`

## What was asked

> When committing, right there at the commit dialog, give me an option
> to gen the commit msg with AI

…with a screenshot of the commit prompt showing `[ Cancel ]  [  OK  ]`
and nothing else.

## The feature already existed

`gitcommitmsg.go` has shipped the `[ ✦ AI ]` button on the commit prompt
since `5db3c91` (2026-08-12), and the installed binary (Aug 14 14:26)
post-dated it. So the question was never "build this" — it was "why is
it invisible on this machine".

Answer, found in the user's config:

```json
{ "chatagent": "claude", "chatmodel": "claude-sonnet-4.6", "copilot": "on" }
```

`claude-code-acp` is not on PATH here (`copilot-language-server` is).
`chatEnsureStarted` looks the binary up, fails, sets `chat.dead = true`,
and the button's gate was:

```go
func (a *App) canSuggestCommitMsg() bool {
	return a.gitIsRepo && a.chatAgentEnabled() && !a.chat.dead
}
```

So the affordance disappeared — no button, no row, no explanation, on
exactly the machine that most needed to be told what to install. Same
gate hid the git-panel Actions row and the ≡ "Suggest commit message"
row, so there was no surviving surface to learn from either.

## The rule that was violated

CLAUDE.md already states it, for the Copilot auth rows:

> **Menu rows stay clickable when unavailable** — unlike the dimming LSP
> rows, `menuCopilotAuth` flashes WHY (disabled / not installed). Sign in
> is a new user's first touch; a dimmed row is a dead end.

Three things make it bind harder here than in the menu:

1. **The verdict is DISCOVERED by trying.** "Not installed" only exists
   after something attempts a start, so a pre-emptive hide can hide the
   affordance from a machine that has never even looked.
2. **The reason names the binary; the absence names nothing.** A missing
   button cannot say `install claude-code-acp on PATH`.
3. **A prompt cannot borrow the ≡ menu.** The modal owns the keyboard,
   so there is no second surface to fall back to.

## What changed

**Availability is a reason, not a gate.**

- `canSuggestCommitMsg()` is down to `a.gitIsRepo` — the one question
  that makes the action *meaningless* rather than merely unavailable.
- New `commitDraftBlockedReason()` (gitcommitmsg.go) is consulted when
  the button is USED: it calls `chatEnsureStarted` (so the answer is
  honest about a binary nobody has looked for), then reports the kill
  switch / dead-agent / mid-turn reason.
- **Checked BEFORE `closeModal`** — a refusal must not cost the user the
  message they had already typed. That ordering is the load-bearing part.
- New `chatUnavailableReason()` (copilot_chat.go) is the ONE spelling of
  that sentence, shared with `chatOpenPanel`, which now asks it twice:
  once before starting, once after (the discovery point).
- `hasSuggestableCommit()` and the panel's Actions row keep only the
  "is there a change to describe" half.

**Keyboard twins for the prompt's buttons.**

`promptExtra` grew a `key rune`; `promptModal.handleKey` fires the match
on Alt+rune before the field sees it (`fireExtraKey`). `alt+a` drafts,
`alt+t` flips the trailer chip. Safe for the find bar's exact reason —
the surface consumes the keystroke, so handleKey's Alt+rune leader
branch never sees it, tmux's folded "Esc a" included.

Two deliberate details:

- The chord fires even when `extraRects` SHED the button for width. A
  truncated button lies about its target; a key cannot.
- `commitPromptHint` (`"message   ·   alt+a = ✦ AI draft"`) advertises
  it in the subtitle, which is the prompt's only discovery surface —
  the ≡ hint column, where every other twin is learned, is unreachable
  from a modal.

## Files

```
internal/app/gitcommitmsg.go        gate → reason, the chords, the hint
internal/app/copilot_chat.go        chatUnavailableReason (one spelling)
internal/app/modals.go              promptExtra.key + fireExtraKey
internal/app/gitcommitmsg_test.go   4 tests added, 2 rewritten
internal/app/gitpanelwalk_test.go   "no agent, no button" → "no agent, a reason"
CLAUDE.md                           both rules written down
```

Two existing tests pinned the OLD behavior (`"no agent, no button"`,
`"suggest row offered with a dead agent"`) and were rewritten with the
reasoning, not deleted — the behavior change is the point of the session.

## Verification

`go test ./...` green, `-race` green on `internal/app`.

Live, through the `run-ced` capture tool, on a scratch repo with the
user's own config shape (`chatagent: claude`, binary absent):

```
┌────────────────────────────────────────────────────┐
│ Commit staged changes                          esc │
├────────────────────────────────────────────────────┤
│ message   ·   alt+a = ✦ AI draft                   │
│  fix the thing                                     │
│             [ Cancel ]      [  OK  ]      [ ✦ AI ] │
└────────────────────────────────────────────────────┘
 Claude Code chat unavailable — install claude-code-acp on PATH, then re-pick it via ≡ → …
```

The `{esc}a` payload really does arrive as Alt+a through a live pty, the
typed message survives the refusal, and the status bar names the binary.

## Note for the owner

Drafts work today by either installing `claude-code-acp`, or switching
back to Copilot under ≡ → Copilot → Chat agent (that binary IS installed
here). The flashed reason is slightly longer than a 120-column status
bar — `chatAgentRetryHint` is shared and pinned by other tests, so it was
left alone; worth shortening if it bothers anyone.
