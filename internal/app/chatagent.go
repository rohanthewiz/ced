// =============================================================================
// File: internal/app/chatagent.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-29
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// chatagent.go generalizes the chat panel's backend. The panel speaks
// ACP (Agent Client Protocol — see copilot_chat.go), and ACP is an open
// protocol: Copilot is just one agent that happens to speak it. This
// file is the registry of agents ced knows how to spawn, plus the ≡
// picker that switches between them.
//
// Design constraints, in order:
//
//   - ONE panel, switchable backend — not a panel per agent. The left
//     edge is single-occupancy by design (see copilot_chat.go's layout
//     notes), and two chat strips would fight over it.
//   - Same silent-degradation contract as every integration: the
//     agent's binary being on PATH is the opt-in; a missing binary
//     marks the chat dead without a modal. Re-picking an agent from the
//     ≡ list is the deliberate retry gesture (it clears the dead
//     verdict), the same role the Copilot off/on toggle plays for the
//     sidecar.
//   - Only the Copilot backend is gated by the "copilot" config key —
//     that key means "may ced run GitHub's binary", and it must not
//     grow into a kill switch for unrelated agents (chatAgentEnabled).
//   - Everything downstream of the spawn is already agent-agnostic:
//     session/prompt turns, streaming, the model roster picker, and
//     context attachments (embedded-resource capability is probed per
//     agent in chatInitialize, with the fenced-text fallback). File and
//     selection context therefore work identically on every backend.
package app

import (
	"os/exec"

	"github.com/rohanthewiz/ced/internal/userconfig"
)

// chatAgentCopilotID is the registry id of the default (Copilot)
// backend — the one agent whose lifecycle is additionally tied to the
// "copilot" config key and the phase-1 sign-in flow.
const chatAgentCopilotID = "copilot"

// chatAgentDef describes one ACP-speaking chat backend: how to spawn
// it, how to name it in the UI, and what to tell the user when its
// handshake fails (which is almost always "not authenticated").
type chatAgentDef struct {
	id     string   // stable config value ("chatagent" key)
	name   string   // display name for the header, menu, transcript
	binary string   // executable resolved on PATH — presence is the opt-in
	args   []string // argv after the binary

	// authHint is appended to the transcript after a failed handshake.
	// Each agent authenticates its own way (device flow, CLI login, an
	// env var), and the raw protocol error never says which — this line
	// does.
	authHint string

	// coAuthorEmail is the address in the Co-Authored-By trailer of a
	// commit message this agent DRAFTED (gitcommitmsg.go); the name half
	// of the trailer is `name` above, so the log credits the agent by
	// exactly the string the UI calls it. Empty = no trailer for this
	// backend, whatever the preference says.
	//
	// Deliberately a non-routing noreply@ address at the vendor's own
	// domain, not the account address a forge resolves to an avatar: the
	// trailer is a RECORD that a machine drafted the sentence, and an
	// address that resolves would turn that record into a mention of a
	// specific account ced has no way to verify was involved.
	coAuthorEmail string
}

// chatAgents returns the registry in display order. The first entry is
// the default backend, which keeps zero-value App structs (tests build
// them by hand) behaving exactly as before this file existed.
func chatAgents() []chatAgentDef {
	return []chatAgentDef{
		{
			id: chatAgentCopilotID, name: "Copilot",
			binary: copilotServerBinary, args: []string{"--acp"},
			authHint:      "Sign in first: ≡ → Copilot → Sign in to GitHub",
			coAuthorEmail: "noreply@github.com",
		},
		{
			id: "claude", name: "Claude Code",
			binary:        "claude-code-acp",
			authHint:      "Authenticate first: run `claude` in a terminal and sign in, or set ANTHROPIC_API_KEY",
			coAuthorEmail: "noreply@anthropic.com",
		},
		{
			id: "gemini", name: "Gemini",
			binary: "gemini", args: []string{"--experimental-acp"},
			authHint:      "Authenticate first: run `gemini` in a terminal and sign in",
			coAuthorEmail: "noreply@google.com",
		},
	}
}

// chatAgentByID resolves a persisted agent id, falling back to the
// default backend for "" or an id this build doesn't know — the
// stale-chatmodel rule: a saved preference from another version must
// degrade quietly, never break startup.
func chatAgentByID(id string) chatAgentDef {
	agents := chatAgents()
	for _, def := range agents {
		if def.id == id {
			return def
		}
	}
	return agents[0]
}

// chatAgent is the single read path for the active backend. It maps
// the zero value onto the default entry so App structs built without
// New (every test) keep today's Copilot behavior without seeding.
func (a *App) chatAgent() chatAgentDef {
	if a.chat.agent.id == "" {
		return chatAgents()[0]
	}
	return a.chat.agent
}

// chatLookPath resolves an agent binary on PATH. A package var so
// newTestApp can pin it to "never found" — belt and braces on top of
// chat.dead against a test ever spawning a real agent process.
var chatLookPath = exec.LookPath

// chatAgentEnabled reports whether the active backend is allowed to
// run. Only Copilot answers to the "copilot" kill switch; for every
// other agent the binary on PATH is the whole opt-in, so there is no
// config gate to consult.
func (a *App) chatAgentEnabled() bool {
	if a.chatAgent().id == chatAgentCopilotID {
		return a.copilot.enabled
	}
	return true
}

// chatAgentRetryHint names the retry gesture in unavailability
// messages. One consistent instruction for every backend — re-picking
// from the agent list clears the dead verdict and restarts.
const chatAgentRetryHint = "re-pick it via ≡ → Copilot → Chat agent"

// chatAgentLabel names the ≡ picker row for the current backend.
func (a *App) chatAgentLabel() string {
	return "Chat agent: " + a.chatAgent().name
}

// menuChatAgent opens the backend picker. Unlike the model picker the
// CURRENT agent stays in the list, annotated as a restart — for every
// non-Copilot backend this row is the deliberate retry path after a
// crash (Copilot alone has the off/on toggle as an alternative), so
// omitting it would leave a dead agent with no way back.
func (a *App) menuChatAgent() {
	a.closeMenu()
	cur := a.chatAgent()
	agents := chatAgents()
	items := make([]paletteItem, 0, len(agents))
	for _, def := range agents {
		def := def // capture per-iteration for the closure
		label := def.name
		if def.id == cur.id {
			label += "  (current — restart)"
		}
		items = append(items, paletteItem{
			label: label,
			run:   func(app *App) { app.chatSetAgent(def) },
		})
	}
	a.openPicker("Chat agent (current: "+cur.name+")", items)
}

// chatSetAgent switches (or restarts) the chat backend: tear down the
// live connection, clear the dead verdict — picking an agent IS the
// retry gesture — and start the new agent if the panel is open (a
// closed panel starts lazily on its next open, as always). The
// transcript survives on purpose, same as it survives any disconnect;
// the "Chat agent:" note marks where one backend's answers end and the
// next one's begin. Pending attachments survive too (chatDisconnect's
// contract), so file/selection context carries straight over.
func (a *App) chatSetAgent(def chatAgentDef) {
	restart := def.id == a.chatAgent().id
	a.chatDisconnect()
	a.chat.dead = false
	a.chat.agent = def
	if !a.chatAgentEnabled() {
		// Only reachable for the Copilot backend with the kill switch
		// off — the choice still persists so re-enabling Copilot lands
		// on the agent the user asked for.
		a.flash("Copilot is disabled — use ≡ → Enable Copilot first")
	}
	if restart {
		a.chatAppendMsg(chatMsg{role: chatRoleInfo, text: "Restarting " + def.name + " chat"})
	} else {
		a.chatAppendMsg(chatMsg{role: chatRoleInfo, text: "Chat agent: " + def.name})
		if err := userconfig.SaveChatAgent(userconfig.DefaultPath(), def.id); err != nil {
			a.flash("chatagent: " + err.Error())
		}
	}
	if a.chat.open && a.chatAgentEnabled() {
		a.chatEnsureStarted()
		if a.chat.dead {
			a.flash(def.name + " chat unavailable — install " + def.binary + " on PATH, then " + chatAgentRetryHint)
		}
	}
}

// chatAuthHint is the transcript line pointing at the active agent's
// auth flow after a failed handshake. Copilot's own auth state is
// visible to ced (the phase-1 sidecar tracks it), so its hint only
// shows when it would help; other agents authenticate entirely outside
// ced, so their hint always shows — auth is the overwhelmingly likely
// handshake failure, and a wrong hint costs one transcript line.
func (a *App) chatAuthHint() string {
	def := a.chatAgent()
	if def.id == chatAgentCopilotID && a.copilot.signedIn {
		return ""
	}
	return def.authHint
}
