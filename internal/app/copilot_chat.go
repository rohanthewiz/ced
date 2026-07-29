// =============================================================================
// File: internal/app/copilot_chat.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-22
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// copilot_chat.go is phase 3 of the GitHub Copilot integration: a chat
// panel backed by the Agent Client Protocol (ACP). It runs the SAME
// copilot-language-server binary as phases 1–2, but as a second process
// in --acp mode over internal/lsp's client with ndjson framing (see
// lsp/acp.go) — chat and completions are separate protocols by GitHub's
// design, so they get separate processes but share every house rule:
//
//   - Silent degradation: no binary → dead, no nagging; the "copilot"
//     config key is the shared opt-out. Auth is phase 1's device flow —
//     the ACP agent reads the same stored credentials, so there is no
//     second sign-in here.
//   - Events only: spawn/handshake, prompt turns, and streamed updates
//     all run off-loop and post chat*Events; only the main loop touches
//     App.chat. No auto-restart after a crash — toggling Copilot
//     off/on is the deliberate retry path.
//   - Menu-first: the show/hide toggle and the model picker live in
//     the ≡ Copilot group (owner preference — all Copilot surfaces in
//     one place).
//
// Layout: the panel docks as a full-height strip on the LEFT edge and
// the file tree flips to the RIGHT — the owner's explicit preference,
// same flip the left-docked terminal performs. The left edge is
// single-occupancy (chat and a left-docked terminal swap, never stack),
// mirroring the bottom strip's terminal/git-panel exclusivity and for
// the same reason: two independently resizable strips on one edge need
// circular clamp math on small windows.
//
// The turn pipeline:
//
//	Enter ──► session/prompt (blocks until the turn ends) ──► chatTurnDoneEvent
//	              │
//	              └──◄ session/update notifications stream agent_message_chunks
//	                   into the transcript while the call is in flight
//
// Scope guard: this phase is CHAT ONLY. The handshake declares no fs
// capabilities and session/request_permission is auto-declined (the
// agent then answers in prose instead of editing files). A permission
// UI is a later phase, not a TODO to sneak in here.

package app

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/lsp"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

const (
	// chatPanelMinWidth keeps the strip usable — enough columns for a
	// short wrapped sentence plus the prompt. Same spirit as
	// termPanelMinWidth, a touch wider because prose wraps worse than
	// shell commands.
	chatPanelMinWidth = 28

	// chatSessionTimeout bounds the session/new handshake call. Longer
	// than the 5s default because the agent may be loading model state
	// on first run, but bounded so a wedged agent settles into the dead
	// state instead of hanging the start goroutine forever.
	chatSessionTimeout = 30 * time.Second

	// chatTurnTimeout bounds one session/prompt turn. Turns block
	// server-side for the whole answer (minutes when the model reasons
	// or calls tools), which is exactly why lsp.Client grew
	// CallWithTimeout for the sign-in flow — same escape hatch here.
	chatTurnTimeout = 15 * time.Minute

	// chatTranscriptMax bounds the transcript's message count. Beyond
	// it the oldest messages fall off — the termScrollbackMax rule
	// applied to chat.
	chatTranscriptMax = 500
)

// chatMsgRole styles a transcript message: who (or what) produced it.
type chatMsgRole int

const (
	chatRoleUser  chatMsgRole = iota // prompt the user sent
	chatRoleAgent                    // streamed agent prose (markdown-ish)
	chatRoleTool                     // one-line tool-call/permission notes
	chatRoleInfo                     // editor-side status: errors, hints
)

// chatMsg is one logical transcript entry. Display rows are derived
// from these at render time (chatRows) so a panel resize re-wraps the
// whole history for free.
type chatMsg struct {
	role chatMsgRole
	text string
}

// chatRowAction marks the derived rows that are affordances rather
// than transcript text: the per-response and whole-conversation copy
// buttons. The zero value is "ordinary text row", so every existing
// chatRow literal keeps meaning what it meant.
type chatRowAction int

const (
	chatRowNone    chatRowAction = iota // ordinary transcript text
	chatRowCopyMsg                      // ⧉ for the message at msgIdx
	chatRowCopyAll                      // ⧉ for the whole conversation
)

// chatRow is one wrapped display row plus the styling facts the paint
// loop needs: the source role, whether the row sits inside a fenced
// code block, and (for the ⧉ affordances) which message it copies.
type chatRow struct {
	text   string
	role   chatMsgRole
	code   bool
	action chatRowAction
	msgIdx int // index into chat.msgs; only meaningful for chatRowCopyMsg
}

// chatPos addresses a character in the DERIVED row space (wrapped row
// index + rune column), not the transcript's logical text. Selection
// lives here because that's what the user actually points at, and it
// survives scrolling for free — row indices count from the top of the
// transcript, not the viewport.
type chatPos struct {
	row, col int
}

// before reports whether p sorts ahead of q in reading order.
func (p chatPos) before(q chatPos) bool {
	if p.row != q.row {
		return p.row < q.row
	}
	return p.col < q.col
}

// chatModel is one entry of the agent's model roster: the wire id
// session/set_model wants, the display name for picker rows, and the
// premium-request multiplier GitHub attaches in _meta ("1x", "7.5x").
// The multiplier is kept because a model pick is a spend decision —
// hiding it would invite expensive surprises.
type chatModel struct {
	id    string
	name  string
	usage string
}

// chatState is everything the chat integration remembers, owned by App
// and mutated only on the main loop.
type chatState struct {
	open    bool
	focused bool // keyboard routes to the input line while true

	// width is the user-chosen strip width from a splitter drag, 0 for
	// "auto" (a third of the screen). Session-only, like the terminal
	// strip's width.
	width int

	client   copilotConn // same generic call surface as the sidecar
	starting bool        // async spawn + handshake in flight
	dead     bool        // unavailable: no binary, crashed, failed start

	// sessionID is the ACP conversation this panel feeds. One session
	// per connection — history lives server-side for the turn context
	// and client-side in msgs for display.
	sessionID string

	// turnActive is true while a session/prompt call is in flight;
	// cancelSent remembers that ⏹ was already pressed for this turn.
	turnActive bool
	cancelSent bool

	// queuedPrompt holds text submitted while the async start was still
	// in flight; handleChatReady sends it. The signInWanted pattern —
	// without it the very first Enter would vanish into "not ready yet".
	queuedPrompt string

	// models is the agent's roster from session/new and modelID the
	// session's current pick — both only meaningful while attached
	// (cleared by chatDisconnect). modelPickWanted queues a picker
	// open requested mid-handshake, the queuedPrompt pattern again.
	// modelPref is the user's persisted choice (config "chatmodel"),
	// applied during the handshake; it survives disconnects so a
	// restart re-applies it.
	models          []chatModel
	modelID         string
	modelPickWanted bool
	modelPref       string

	// autoContext is the "every turn carries the active file" toggle
	// (config "chatcontext"); attach is the explicitly attached context
	// for the NEXT turn only, consumed and cleared by chatSendPrompt.
	// embeddedContext is the agent's advertised willingness to accept
	// resource blocks — false means fold the same text into the prompt.
	// See copilot_chat_context.go for the whole layer.
	autoContext     bool
	attach          []chatAttach
	embeddedContext bool

	msgs   []chatMsg
	scroll int // first visible wrapped row

	// Transcript selection, in derived-row coordinates. selActive is set
	// by a press in the transcript body and stays set (so the highlight
	// persists after the mouse comes up) until the next press, Esc, or a
	// copy that consumes it. anchor is where the drag started, head
	// where it is now — either may sort first.
	selActive bool
	selAnchor chatPos
	selHead   chatPos

	input     textField
	history   []string
	histIdx   int    // == len(history) while editing a fresh line
	histDraft string // in-progress input stashed while browsing history
}

// -----------------------------------------------------------------------------
// Custom tcell events — the goroutine → main-loop bridge
// -----------------------------------------------------------------------------

// chatReadyEvent is posted once the async spawn + ACP handshake +
// session/new completes; it carries the live connection, session, and
// the model roster the agent reported (with any persisted preference
// already applied — modelID is the session's actual current model).
type chatReadyEvent struct {
	when      time.Time
	client    copilotConn
	sessionID string
	models    []chatModel
	modelID   string
	embedded  bool // agent accepts embedded resource blocks in a prompt
}

// When satisfies the tcell.Event interface.
func (e *chatReadyEvent) When() time.Time { return e.when }

// chatExitEvent is posted when the agent dies or fails to start. err
// carries the handshake failure (auth, protocol) when there was one —
// unlike the completion sidecar, chat surfaces WHY in the transcript,
// because the panel is open and staring silence at the user.
type chatExitEvent struct {
	when time.Time
	err  error
}

// When satisfies the tcell.Event interface.
func (e *chatExitEvent) When() time.Time { return e.when }

// chatUpdateEvent carries one session/update notification from the
// read loop to the main loop, still raw — parsing happens on-loop.
type chatUpdateEvent struct {
	when      time.Time
	sessionID string
	update    json.RawMessage
}

// When satisfies the tcell.Event interface.
func (e *chatUpdateEvent) When() time.Time { return e.when }

// chatTurnDoneEvent lands one session/prompt call's completion.
type chatTurnDoneEvent struct {
	when       time.Time
	stopReason string
	err        error
}

// When satisfies the tcell.Event interface.
func (e *chatTurnDoneEvent) When() time.Time { return e.when }

// chatPermissionEvent reports an auto-declined permission request so
// the transcript can say what the agent wasn't allowed to do. Display
// only — the decline itself already happened on the read loop.
type chatPermissionEvent struct {
	when  time.Time
	title string
}

// When satisfies the tcell.Event interface.
func (e *chatPermissionEvent) When() time.Time { return e.when }

// chatModelSetEvent lands one session/set_model call's completion —
// the picker's async leg, same bridge pattern as every chat call.
type chatModelSetEvent struct {
	when  time.Time
	model chatModel
	err   error
}

// When satisfies the tcell.Event interface.
func (e *chatModelSetEvent) When() time.Time { return e.when }

// -----------------------------------------------------------------------------
// Lifecycle
// -----------------------------------------------------------------------------

// chatReady reports whether the ACP connection is up with a session.
func (a *App) chatReady() bool {
	return a.chat.client != nil && !a.chat.dead && a.chat.sessionID != ""
}

// chatEnsureStarted kicks off the async ACP agent start: spawn
// copilot-language-server --acp, run the ACP initialize handshake, and
// open a session. Missing binary marks the integration dead without a
// word — the shared silent-degradation contract. Idempotent.
func (a *App) chatEnsureStarted() {
	if !a.copilot.enabled || a.chat.client != nil || a.chat.starting || a.chat.dead || a.screen == nil {
		return
	}
	if _, err := exec.LookPath(copilotServerBinary); err != nil {
		a.chat.dead = true
		return
	}
	a.chat.starting = true
	scr := a.screen
	root := a.rootDir
	pref := a.chat.modelPref
	go func() {
		// Both callbacks run on the client's read loop — post, don't touch.
		onNotify := func(method string, params json.RawMessage) {
			if method != "session/update" {
				return
			}
			var p struct {
				SessionID string          `json:"sessionId"`
				Update    json.RawMessage `json:"update"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return
			}
			_ = scr.PostEvent(&chatUpdateEvent{when: time.Now(), sessionID: p.SessionID, update: p.Update})
		}
		onRequest := func(method string, params json.RawMessage) (any, error) {
			if method == "session/request_permission" {
				res, title := chatAutoRejectPermission(params)
				_ = scr.PostEvent(&chatPermissionEvent{when: time.Now(), title: title})
				return res, nil
			}
			// fs/* can't arrive (the handshake declares no fs
			// capabilities); anything else unknown gets an honest
			// method-not-found so the agent can fall back to prose.
			return nil, fmt.Errorf("ced does not handle %s", method)
		}
		onExit := func(error) {
			_ = scr.PostEvent(&chatExitEvent{when: time.Now()})
		}
		client, err := lsp.StartACP(root, copilotServerBinary, []string{"--acp"}, onNotify, onRequest, onExit)
		if err != nil {
			_ = scr.PostEvent(&chatExitEvent{when: time.Now(), err: err})
			return
		}
		sess, err := chatInitialize(client, root, pref)
		if err != nil {
			client.Close()
			// A failed handshake may leave the process alive with no
			// read-loop exit — post explicitly to settle the state
			// machine, same as the sidecar start path.
			_ = scr.PostEvent(&chatExitEvent{when: time.Now(), err: err})
			return
		}
		_ = scr.PostEvent(&chatReadyEvent{when: time.Now(), client: client,
			sessionID: sess.id, models: sess.models, modelID: sess.modelID,
			embedded: sess.embedded})
	}()
}

// chatSession is what a completed handshake yields: the ACP session id
// plus the model roster and current pick reported by session/new.
type chatSession struct {
	id       string
	models   []chatModel
	modelID  string
	embedded bool // promptCapabilities.embeddedContext from initialize
}

// chatInitialize runs the ACP handshake and opens the session; returns
// the session plus the agent's model roster. Runs on the start
// goroutine, never the main loop. The fs capabilities are declared
// FALSE on purpose — phase 3 is chat only, and not offering the
// capability is the protocol-honest way to keep the agent out of the
// user's files (see the header comment). A non-empty prefModel is
// applied via session/set_model; an id the roster no longer offers (or
// a failed set) keeps the agent's default silently — a stale saved
// preference must never break the handshake.
//
// The initialize RESULT is read for promptCapabilities.embeddedContext:
// context attachments ride as embedded resource blocks when the agent
// takes them, and fold into the prompt text when it doesn't (see
// copilot_chat_context.go). An agent that reports nothing gets the
// text-only treatment — the safe direction.
func chatInitialize(c *lsp.Client, root, prefModel string) (chatSession, error) {
	initParams := map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{"readTextFile": false, "writeTextFile": false},
		},
	}
	var caps struct {
		AgentCapabilities struct {
			PromptCapabilities struct {
				EmbeddedContext bool `json:"embeddedContext"`
			} `json:"promptCapabilities"`
		} `json:"agentCapabilities"`
	}
	// Same keychain-stall budget as the LSP sidecar handshake: the
	// agent's cold start can block on a macOS Keychain prompt.
	if err := c.CallWithTimeout("initialize", initParams, &caps, copilotInitTimeout); err != nil {
		return chatSession{}, err
	}
	var sess struct {
		SessionID string `json:"sessionId"`
		Models    struct {
			Available []struct {
				ModelID string `json:"modelId"`
				Name    string `json:"name"`
				Meta    struct {
					Usage string `json:"copilotUsage"`
				} `json:"_meta"`
			} `json:"availableModels"`
			CurrentModelID string `json:"currentModelId"`
		} `json:"models"`
	}
	// cwd must be absolute — root is absolutized by New, the same
	// contract that keeps LSP rootUris well-formed.
	err := c.CallWithTimeout("session/new",
		map[string]any{"cwd": root, "mcpServers": []any{}},
		&sess, chatSessionTimeout)
	if err != nil {
		return chatSession{}, err
	}
	if sess.SessionID == "" {
		return chatSession{}, fmt.Errorf("agent returned no session id")
	}
	out := chatSession{
		id:       sess.SessionID,
		modelID:  sess.Models.CurrentModelID,
		embedded: caps.AgentCapabilities.PromptCapabilities.EmbeddedContext,
	}
	for _, m := range sess.Models.Available {
		if m.ModelID == "" {
			continue
		}
		out.models = append(out.models, chatModel{id: m.ModelID, name: m.Name, usage: m.Meta.Usage})
	}
	if prefModel != "" && prefModel != out.modelID {
		for _, m := range out.models {
			if m.id != prefModel {
				continue
			}
			if err := c.CallWithTimeout("session/set_model",
				map[string]any{"sessionId": out.id, "modelId": prefModel},
				nil, chatSessionTimeout); err == nil {
				out.modelID = prefModel
			}
			break
		}
	}
	return out, nil
}

// handleChatReady installs the live connection and flushes a prompt
// the user submitted while the handshake was in flight.
func (a *App) handleChatReady(e *chatReadyEvent) {
	if a.chat.dead || !a.copilot.enabled {
		// Died or was disabled between the ready post and now.
		e.client.Close()
		return
	}
	a.chat.client = e.client
	a.chat.starting = false
	a.chat.sessionID = e.sessionID
	a.chat.models = e.models
	a.chat.modelID = e.modelID
	a.chat.embeddedContext = e.embedded
	if q := a.chat.queuedPrompt; q != "" {
		a.chat.queuedPrompt = ""
		a.chatSendPrompt(q)
	}
	if a.chat.modelPickWanted {
		a.chat.modelPickWanted = false
		a.openChatModelPicker()
	}
}

// handleChatExit marks the integration dead for this session — the
// shared no-flap, no-auto-restart policy. Unlike the completion
// sidecar it also writes the reason into the transcript: the panel is
// an open surface the user is looking at, and unexplained silence
// there reads as breakage, not degradation.
func (a *App) handleChatExit(e *chatExitEvent) {
	a.chatDisconnect()
	a.chat.dead = true
	if e.err != nil {
		a.chatAppendMsg(chatMsg{role: chatRoleInfo, text: "Copilot chat failed: " + e.err.Error()})
		if !a.copilot.signedIn {
			// The most common handshake failure is simply "not signed
			// in" — point at the phase-1 flow instead of leaving the
			// raw protocol error as the only clue.
			a.chatAppendMsg(chatMsg{role: chatRoleInfo, text: "Sign in first: ≡ → Copilot → Sign in to GitHub"})
		}
		return
	}
	a.chatAppendMsg(chatMsg{role: chatRoleInfo, text: "Copilot chat stopped — toggle Copilot off/on to restart"})
}

// chatShutdown tears the agent down without marking it dead — used by
// the Copilot disable toggle and editor exit, where a later re-enable
// should get a fresh start attempt.
func (a *App) chatShutdown() {
	a.chatDisconnect()
}

// chatDisconnect closes the connection and resets every field that
// only means something while an agent is attached. The transcript
// survives on purpose — same as terminal scrollback across a session
// end: the conversation's value doesn't die with the process.
func (a *App) chatDisconnect() {
	if a.chat.client != nil {
		a.chat.client.Close()
	}
	a.chat.client = nil
	a.chat.starting = false
	a.chat.sessionID = ""
	a.chat.turnActive = false
	a.chat.cancelSent = false
	a.chat.queuedPrompt = ""
	a.chat.models = nil
	a.chat.modelID = ""
	a.chat.modelPickWanted = false
	a.chat.embeddedContext = false
	// modelPref deliberately survives — it's the persisted preference,
	// re-applied by the next handshake. So do the pending attachments:
	// they're editor-side context for a message the user hasn't sent
	// yet, and a reconnect shouldn't quietly drop them.
}

// chatAutoRejectPermission builds the response for a
// session/request_permission request: pick the agent's own reject
// option (once-scoped preferred, so a future permission UI starts from
// a clean slate), or the cancelled outcome when no reject option
// exists. Pure — it runs on the read loop, so it must not touch App.
func chatAutoRejectPermission(params json.RawMessage) (result any, title string) {
	var p struct {
		ToolCall struct {
			Title string `json:"title"`
		} `json:"toolCall"`
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(params, &p)
	title = p.ToolCall.Title
	for _, kind := range []string{"reject_once", "reject_always"} {
		for _, o := range p.Options {
			if o.Kind == kind {
				return map[string]any{
					"outcome": map[string]any{"outcome": "selected", "optionId": o.OptionID},
				}, title
			}
		}
	}
	return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, title
}

// handleChatPermission notes an auto-declined permission request in
// the transcript so the agent's "I wasn't allowed to do that" answers
// have visible context.
func (a *App) handleChatPermission(e *chatPermissionEvent) {
	title := e.title
	if title == "" {
		title = "a tool call"
	}
	a.chatAppendMsg(chatMsg{role: chatRoleTool, text: "⊘ declined: " + title})
}

// -----------------------------------------------------------------------------
// Prompt turns
// -----------------------------------------------------------------------------

// chatSend submits the input line: echo it into the transcript, then
// either fire the turn, queue it behind a still-starting agent, or
// explain why nothing will answer. Bound to Enter while the panel has
// focus.
func (a *App) chatSend() {
	text := strings.TrimSpace(a.chat.input.String())
	if text == "" {
		return
	}
	if a.chat.turnActive {
		a.flash("Copilot is answering — ⏹ to stop it first")
		return
	}
	a.chat.input = newTextField("")
	a.chat.history = append(a.chat.history, text)
	a.chat.histIdx = len(a.chat.history)
	a.chat.histDraft = ""
	a.chatAppendMsg(chatMsg{role: chatRoleUser, text: text})
	switch {
	case a.chatReady():
		a.chatSendPrompt(text)
	case a.chat.starting:
		// Mirror signInWanted: the handshake races the user's first
		// Enter, and losing that prompt is the worst first impression.
		if a.chat.queuedPrompt != "" {
			a.chat.queuedPrompt += "\n" + text
		} else {
			a.chat.queuedPrompt = text
		}
		a.chatAppendMsg(chatMsg{role: chatRoleInfo, text: "starting Copilot chat — your message will send shortly"})
	default:
		a.chatAppendMsg(chatMsg{role: chatRoleInfo, text: "Copilot chat is not running — toggle Copilot off/on to restart"})
	}
}

// chatSendPrompt fires the async session/prompt turn for text. The
// call blocks server-side until the whole answer streamed, so it gets
// the long-turn timeout, never the 5s default.
//
// This is the single dispatch point, so it is also where context
// attachments are resolved, echoed, and consumed — the queued-prompt
// path flushes through here too, and attaching a file must work the same
// whether or not the handshake had finished when Enter was pressed.
func (a *App) chatSendPrompt(text string) {
	if !a.chatReady() {
		return
	}
	blocks, notes := a.chatPromptBlocks(text)
	if len(notes) > 0 {
		// What the model was handed is part of the conversation: without
		// this line an answer about "the file" has no visible referent.
		a.chatAppendMsg(chatMsg{role: chatRoleTool, text: strings.Join(notes, "\n")})
	}
	// Per-turn by design — see copilot_chat_context.go's house rules.
	a.chat.attach = nil
	a.chat.turnActive = true
	a.chat.cancelSent = false
	client := a.chat.client
	scr := a.screen
	sid := a.chat.sessionID
	params := map[string]any{
		"sessionId": sid,
		"prompt":    blocks,
	}
	go func() {
		var res struct {
			StopReason string `json:"stopReason"`
		}
		err := client.CallWithTimeout("session/prompt", params, &res, chatTurnTimeout)
		_ = scr.PostEvent(&chatTurnDoneEvent{when: time.Now(), stopReason: res.StopReason, err: err})
	}()
}

// handleChatUpdate lands one streamed session/update on the main loop:
// agent prose chunks append to the transcript, tool calls surface as
// one-liners, and everything else (thoughts, plans, status) is dropped
// — a narrow strip has no room for the agent's inner monologue.
func (a *App) handleChatUpdate(e *chatUpdateEvent) {
	if e.sessionID != a.chat.sessionID {
		return // an earlier connection's stragglers
	}
	var u struct {
		Kind    string          `json:"sessionUpdate"`
		Content json.RawMessage `json:"content"`
		Title   string          `json:"title"`
	}
	if err := json.Unmarshal(e.update, &u); err != nil {
		return
	}
	switch u.Kind {
	case "agent_message_chunk":
		if text := chatContentText(u.Content); text != "" {
			a.chatAppendAgentText(text)
		}
	case "tool_call":
		title := u.Title
		if title == "" {
			title = "tool call"
		}
		a.chatAppendMsg(chatMsg{role: chatRoleTool, text: "⚙ " + title})
	}
}

// chatContentText extracts the text of an ACP ContentBlock; non-text
// blocks (images, resources) yield "" and are skipped upstream.
func chatContentText(raw json.RawMessage) string {
	var c struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &c) != nil || c.Type != "text" {
		return ""
	}
	return c.Text
}

// handleChatTurnDone finishes one prompt turn. Errors and cancels get
// a transcript line; a clean end needs none — the answer already IS
// the feedback.
func (a *App) handleChatTurnDone(e *chatTurnDoneEvent) {
	a.chat.turnActive = false
	a.chat.cancelSent = false
	if e.err != nil {
		a.chatAppendMsg(chatMsg{role: chatRoleInfo, text: "Copilot chat: " + e.err.Error()})
		return
	}
	if e.stopReason == "cancelled" {
		a.chatAppendMsg(chatMsg{role: chatRoleInfo, text: "— stopped"})
	}
}

// chatInterrupt is the ⏹ button / the mouse-first stand-in for Ctrl+C:
// ask the agent to end the in-flight turn. The blocked session/prompt
// call then returns with stopReason "cancelled".
func (a *App) chatInterrupt() {
	if !a.chat.turnActive || !a.chatReady() {
		return
	}
	if a.chat.cancelSent {
		return // one cancel per turn; the agent is already unwinding
	}
	a.chat.cancelSent = true
	_ = a.chat.client.Notify("session/cancel", map[string]any{"sessionId": a.chat.sessionID})
	a.flash("stopping Copilot's answer")
}

// -----------------------------------------------------------------------------
// Transcript
// -----------------------------------------------------------------------------

// chatAppendMsg commits one transcript message, trimming history past
// the cap and keeping the view pinned to the newest row when it
// already was (the termAtBottom rule: reading old messages never gets
// yanked back down by new output).
func (a *App) chatAppendMsg(m chatMsg) {
	atBottom := a.chatAtBottom()
	a.chat.msgs = append(a.chat.msgs, m)
	if over := len(a.chat.msgs) - chatTranscriptMax; over > 0 {
		a.chat.msgs = a.chat.msgs[over:]
		// Trimming renumbers every derived row, so a selection anchored
		// in the old numbering would silently point at other text.
		a.chatClearSelection()
	}
	if atBottom {
		a.chat.scroll = a.chatMaxScroll()
	}
}

// chatAppendAgentText streams a prose chunk: append to the trailing
// agent message when one is open, otherwise start a new one. Chunks
// are partial words — a message per chunk would shred the answer.
func (a *App) chatAppendAgentText(text string) {
	atBottom := a.chatAtBottom()
	if n := len(a.chat.msgs); n > 0 && a.chat.msgs[n-1].role == chatRoleAgent {
		a.chat.msgs[n-1].text += text
	} else {
		a.chat.msgs = append(a.chat.msgs, chatMsg{role: chatRoleAgent, text: text})
	}
	if atBottom {
		a.chat.scroll = a.chatMaxScroll()
	}
}

// chatCopyMsgLabel / chatCopyAllLabel are the ⧉ affordances' text —
// the double-rectangle glyph plus a word, right-aligned on their own
// derived row. A whole row (rather than a cell squeezed beside prose)
// keeps hit-testing to "which row did they click", the same trick the
// transcript already uses for everything else.
const (
	chatCopyMsgLabel = "⧉ copy "
	chatCopyAllLabel = "⧉ copy conversation "
)

// chatRows derives the display rows for the current transcript at
// width w: messages word-wrapped, separated by blank rows, with fenced
// code blocks hard-wrapped (indentation is meaning there) and flagged
// for the code style. Each agent response is trailed by its own ⧉ copy
// row, and the whole transcript by a copy-conversation row. Recomputed
// on demand — the transcript is small (chatTranscriptMax) and deriving
// keeps resize/re-wrap free.
func (a *App) chatRows(w int) []chatRow {
	if w < 1 {
		w = 1
	}
	var rows []chatRow
	for i, m := range a.chat.msgs {
		if i > 0 {
			rows = append(rows, chatRow{}) // blank separator row
		}
		text := m.text
		if m.role == chatRoleUser {
			// The prompt gutter is part of the text so wrapping and
			// hit-free rendering stay trivial; continuation rows are
			// simply unindented.
			text = "❯ " + text
		}
		inFence := false
		for _, ln := range strings.Split(text, "\n") {
			fence := m.role == chatRoleAgent && strings.HasPrefix(strings.TrimSpace(ln), "```")
			if fence || inFence {
				if fence {
					inFence = !inFence
				}
				// Code (and the fence markers themselves): hard-wrap in
				// w-sized chunks, no word breaking — a reflowed code
				// line is worse than a truncated-looking one.
				r := []rune(ln)
				if len(r) == 0 {
					rows = append(rows, chatRow{role: m.role, code: true})
				}
				for len(r) > 0 {
					n := min(len(r), w)
					rows = append(rows, chatRow{text: string(r[:n]), role: m.role, code: true})
					r = r[n:]
				}
				continue
			}
			for _, seg := range wrapChatText(ln, w) {
				rows = append(rows, chatRow{text: seg, role: m.role})
			}
		}
		// Per-response copy affordance. Only agent prose gets one: the
		// user's own prompts are already in their head, and tool/info
		// notes are editor chatter, not content worth lifting out.
		if m.role == chatRoleAgent && strings.TrimSpace(m.text) != "" {
			rows = append(rows, chatRow{role: m.role, action: chatRowCopyMsg, msgIdx: i})
		}
	}
	if len(a.chat.msgs) > 0 {
		rows = append(rows, chatRow{}, chatRow{action: chatRowCopyAll})
	}
	return rows
}

// chatRowLabel is an action row's display text, "" for ordinary rows.
func chatRowLabel(row chatRow) string {
	switch row.action {
	case chatRowCopyMsg:
		return chatCopyMsgLabel
	case chatRowCopyAll:
		return chatCopyAllLabel
	}
	return ""
}

// chatActionRect places an action row's button flush with the panel's
// right edge — the single geometry source draw and hit-testing share
// (the btnRect rule). A row too narrow for the label yields a zero-width
// rect, which contains() can never match.
func chatActionRect(row chatRow, x, ry, w int) btnRect {
	lw := runeLen(chatRowLabel(row))
	if lw == 0 || lw > w {
		return btnRect{x: x, y: ry}
	}
	return btnRect{x: x + w - lw, y: ry, w: lw}
}

// wrapChatText greedy-word-wraps one logical line to width w. An empty
// line yields one empty row (paragraph breaks must survive); a word
// longer than w is hard-broken rather than overflowing the strip.
func wrapChatText(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	if strings.TrimSpace(s) == "" {
		return []string{""}
	}
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		for runeLen(word) > w {
			// Flush the pending line first so the hard break starts at
			// column 0, then split the oversized word into w-cell chunks.
			if line != "" {
				out = append(out, line)
				line = ""
			}
			r := []rune(word)
			out = append(out, string(r[:w]))
			word = string(r[w:])
		}
		switch {
		case line == "":
			line = word
		case runeLen(line)+1+runeLen(word) <= w:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// -----------------------------------------------------------------------------
// Selection + copy
// -----------------------------------------------------------------------------
//
// The panel captures the mouse, so the terminal's own drag-to-select
// never reaches the transcript — the editor has to provide selection
// itself, exactly as it does for the code pane. Selection lives in
// derived-row coordinates (see chatPos): what the user drags across is
// wrapped rows, and anchoring there means scrolling doesn't shift the
// highlight while re-wrapping simply re-derives it.

// chatRowWidth is the transcript's text width — the panel minus its
// one-column gutters. The single source for every rows() call that has
// to agree with what's on screen.
func (a *App) chatRowWidth() int {
	_, _, pw, _ := a.chatPanelRect()
	if w := pw - 2; w > 0 {
		return w
	}
	return 1
}

// chatClearSelection drops any transcript highlight. Called by Esc, by
// the next press, and after a copy consumes it.
func (a *App) chatClearSelection() {
	a.chat.selActive = false
	a.chat.selAnchor = chatPos{}
	a.chat.selHead = chatPos{}
}

// chatSelRange normalizes the anchor/head pair into reading order,
// reporting false when there is no selection or it is empty (a plain
// click, which arms an anchor but selects nothing).
func (a *App) chatSelRange() (start, end chatPos, ok bool) {
	if !a.chat.selActive {
		return chatPos{}, chatPos{}, false
	}
	start, end = a.chat.selAnchor, a.chat.selHead
	if end.before(start) {
		start, end = end, start
	}
	if start == end {
		return start, end, false
	}
	return start, end, true
}

// chatRowSelSpan returns the [from, to) rune columns of row that fall
// inside the selection, or (0, 0) when none do. Rune length is passed
// in because the caller already has the row's runes in hand.
func (a *App) chatRowSelSpan(row, runeCount int) (from, to int) {
	start, end, ok := a.chatSelRange()
	if !ok || row < start.row || row > end.row {
		return 0, 0
	}
	from, to = 0, runeCount
	if row == start.row {
		from = min(start.col, runeCount)
	}
	if row == end.row {
		to = min(end.col, runeCount)
	}
	if from > to {
		from = to
	}
	return from, to
}

// chatPosAt maps a screen cell to a transcript position, clamped into
// the derived rows so a drag that leaves the panel still lands
// somewhere sane (the editorDrag contract).
func (a *App) chatPosAt(x, y int) chatPos {
	px, py, _, _ := a.chatPanelRect()
	rows := a.chatRows(a.chatRowWidth())
	if len(rows) == 0 {
		return chatPos{}
	}
	row := a.chat.scroll + (y - py - 1)
	if row < 0 {
		row = 0
	}
	if last := len(rows) - 1; row > last {
		row = last
	}
	n := runeLen(rows[row].text)
	col := x - (px + 1)
	if col < 0 {
		col = 0
	}
	if col > n {
		col = n
	}
	return chatPos{row: row, col: col}
}

// chatSelectionText renders the highlighted rows back to text. Action
// rows are skipped — a selection dragged past a ⧉ affordance must not
// paste the button's label. Rows join with newlines, so a wrapped
// paragraph copies as the wrapped lines the user actually saw; that's
// the honest reading of a row-space selection.
func (a *App) chatSelectionText() string {
	start, end, ok := a.chatSelRange()
	if !ok {
		return ""
	}
	rows := a.chatRows(a.chatRowWidth())
	var parts []string
	for r := start.row; r <= end.row && r < len(rows); r++ {
		if rows[r].action != chatRowNone {
			continue
		}
		runes := []rune(rows[r].text)
		from, to := a.chatRowSelSpan(r, len(runes))
		parts = append(parts, string(runes[from:to]))
	}
	return strings.Join(parts, "\n")
}

// chatTranscriptText renders the whole conversation as plain text for
// the copy-conversation button. Roles become "You:" / "Copilot:"
// labels — the ❯ gutter and glyph prefixes are screen furniture, and
// what lands in the clipboard usually ends up in an issue or a commit
// message, where named speakers read better.
func (a *App) chatTranscriptText() string {
	var parts []string
	for _, m := range a.chat.msgs {
		switch m.role {
		case chatRoleUser:
			parts = append(parts, "You:\n"+m.text)
		case chatRoleAgent:
			parts = append(parts, "Copilot:\n"+m.text)
		default:
			parts = append(parts, m.text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// chatCopy pushes text onto both clipboards — the editor's internal
// buffer (so Paste works without a cooperating terminal) and the host's
// via OSC 52 — and flashes what happened. Routed through
// copilotCopyCode, the stubbable var the sign-in flow already uses, so
// no test writes the dev machine's clipboard.
func (a *App) chatCopy(text, what string) {
	if text == "" {
		return
	}
	a.clipBuf = text
	a.clipKind = clipText
	if err := copilotCopyCode(text); err != nil {
		a.flash(what + " copied (system clipboard unavailable)")
		return
	}
	a.flash(what + " copied")
}

// chatCopySelection is Cmd+C while the chat has focus: lift the
// highlighted text, then drop the highlight — the copy consumed it,
// and a lingering highlight over text you've already taken reads as
// stale. With nothing selected it says what to do instead of failing
// silently.
func (a *App) chatCopySelection() {
	text := a.chatSelectionText()
	if text == "" {
		a.flash("Drag to select chat text first — or click ⧉ to copy a response")
		return
	}
	a.chatCopy(text, "Selection")
	a.chatClearSelection()
}

// chatCopyMessage copies one transcript message whole — the ⧉ button
// beside a response. Copies the logical message, NOT the wrapped rows:
// a response lifted for pasting elsewhere should carry its original
// line breaks, not the strip's.
func (a *App) chatCopyMessage(idx int) {
	if idx < 0 || idx >= len(a.chat.msgs) {
		return
	}
	a.chatCopy(a.chat.msgs[idx].text, "Response")
}

// chatCopyTranscript copies the whole conversation.
func (a *App) chatCopyTranscript() {
	if len(a.chat.msgs) == 0 {
		a.flash("Nothing in the chat to copy yet")
		return
	}
	a.chatCopy(a.chatTranscriptText(), "Conversation")
}

// hasChatTranscript gates the ≡ row: something to copy exists.
func (a *App) hasChatTranscript() bool { return len(a.chat.msgs) > 0 }

// menuChatCopyAll is the keyboard twin of the transcript's trailing ⧉
// button — the git-panel-actions rule: a mouse-driven panel still needs
// a menu path, because macOS Terminal can swallow clicks.
func (a *App) menuChatCopyAll() {
	a.closeMenu()
	a.chatCopyTranscript()
}

// -----------------------------------------------------------------------------
// Scrolling
// -----------------------------------------------------------------------------

// chatVisibleRows is how many transcript rows the panel shows — the
// strip minus the header rule, the input row, and any attachment chips
// (chatAttachRowsView already clamps itself to leave a row here).
func (a *App) chatVisibleRows() int {
	_, _, _, ph := a.chatPanelRect()
	if v := ph - 2 - a.chatAttachRows(); v > 0 {
		return v
	}
	return 1
}

// chatContentRows counts the wrapped transcript rows at the current
// panel width.
func (a *App) chatContentRows() int {
	return len(a.chatRows(a.chatRowWidth()))
}

// chatMaxScroll is the scroll offset that pins the newest row to the
// bottom of the viewport. Hard clamp, no overscroll — a conversation
// reads bottom-up, same as the terminal.
func (a *App) chatMaxScroll() int {
	if max := a.chatContentRows() - a.chatVisibleRows(); max > 0 {
		return max
	}
	return 0
}

// chatAtBottom reports whether the view is pinned to the newest row.
// Sampled BEFORE appending, so streaming output follows the tail only
// when the user was already there.
func (a *App) chatAtBottom() bool {
	return a.chat.scroll >= a.chatMaxScroll()
}

// chatPanelScroll wheels the transcript by delta rows, hard-clamped.
func (a *App) chatPanelScroll(delta int) {
	a.chat.scroll += delta
	if max := a.chatMaxScroll(); a.chat.scroll > max {
		a.chat.scroll = max
	}
	if a.chat.scroll < 0 {
		a.chat.scroll = 0
	}
}

// -----------------------------------------------------------------------------
// History
// -----------------------------------------------------------------------------

// chatHistoryMove steps through past prompts with Up/Down, stashing
// the in-progress draft — readline behavior, same as the terminal.
func (a *App) chatHistoryMove(delta int) {
	c := &a.chat
	if len(c.history) == 0 {
		return
	}
	idx := c.histIdx + delta
	if idx < 0 || idx > len(c.history) {
		return
	}
	if c.histIdx == len(c.history) {
		c.histDraft = c.input.String()
	}
	c.histIdx = idx
	if idx == len(c.history) {
		c.input = newTextField(c.histDraft)
		return
	}
	c.input = newTextField(c.history[idx])
}

// -----------------------------------------------------------------------------
// Menu row + left-edge occupancy
// -----------------------------------------------------------------------------

// chatToggleLabel names the ≡ View row for its current direction.
func (a *App) chatToggleLabel() string {
	if a.chat.open {
		return "Hide Copilot chat"
	}
	return "Show Copilot chat"
}

// menuToggleChat shows or hides the chat panel. Opening explains
// unavailability with a flash instead of dimming (the menuCopilotAuth
// rule: a first-touch row must never be a silent dead end), claims the
// left edge from a left-docked terminal, and starts the agent.
func (a *App) menuToggleChat() {
	a.closeMenu()
	if a.chat.open {
		a.chat.open = false
		a.chat.focused = false
		return
	}
	a.chatOpenPanel()
}

// chatOpenPanel opens the chat strip and starts the agent, explaining
// unavailability with a flash instead of failing quietly. Shared by the
// ≡ toggle and by attaching context — attaching a file to a panel you
// can't see would leave the user with no way to know it worked.
// Idempotent: an already-open panel just takes focus back.
func (a *App) chatOpenPanel() {
	if !a.copilot.enabled {
		a.flash("Copilot is disabled — use ≡ → Enable Copilot first")
		return
	}
	a.chatEnsureStarted()
	if a.chat.dead {
		a.flash("Copilot chat unavailable — install copilot-language-server on PATH, then toggle Copilot off/on")
		return
	}
	a.chat.open = true
	a.chat.focused = true
	a.term.focused = false
	// Left-edge single occupancy: a left-docked terminal yields the
	// strip (its dock preference survives — reopening it evicts the
	// chat right back). A bottom-docked terminal coexists.
	if a.termDockLeft && a.term.open {
		a.term.open = false
	}
}

// chatModelLabel names the ≡ Copilot row for the model picker: the
// current model when a session knows it, a neutral opener otherwise.
func (a *App) chatModelLabel() string {
	if name := a.chatCurrentModelName(); name != "" {
		return "Chat model: " + name
	}
	return "Chat model…"
}

// chatCurrentModelName resolves the session's current model id to its
// display name, "" when no session is up or the id isn't in the roster.
func (a *App) chatCurrentModelName() string {
	for _, m := range a.chat.models {
		if m.id == a.chat.modelID {
			return m.name
		}
	}
	return ""
}

// menuChatModel is the ≡ row's dispatch: open the model picker, or
// explain why it can't (the menuCopilotAuth rule — always clickable,
// never a silent dead end). Picking a model doesn't require the panel
// to be open, but it does need a session — a click before the agent is
// up queues the picker behind the async start, the queuedPrompt
// pattern, so the first click never just vanishes.
func (a *App) menuChatModel() {
	a.closeMenu()
	switch {
	case !a.copilot.enabled:
		a.flash("Copilot is disabled — use ≡ → Enable Copilot first")
	case a.chat.dead:
		a.flash("Copilot chat unavailable — install copilot-language-server on PATH, then toggle Copilot off/on")
	case a.chatReady():
		a.openChatModelPicker()
	default:
		a.chat.modelPickWanted = true
		a.chatEnsureStarted()
		if a.chat.dead {
			// ensureStarted's LookPath just failed — correct the story.
			a.chat.modelPickWanted = false
			a.flash("Copilot chat unavailable — install copilot-language-server on PATH")
		} else {
			a.flash("Copilot chat is starting — the model list will open shortly")
		}
	}
}

// openChatModelPicker shows the roster as a fuzzy picker. The current
// model is excluded — offering a no-op row only clutters the list
// (menuGitSwitchBranch's rule); it's named in the title instead. Each
// row carries the premium-request multiplier so a pick is an informed
// spend.
func (a *App) openChatModelPicker() {
	items := make([]paletteItem, 0, len(a.chat.models))
	for _, m := range a.chat.models {
		if m.id == a.chat.modelID {
			continue
		}
		m := m // capture per-iteration for the closure
		label := m.name
		if m.usage != "" {
			label += "  (" + m.usage + ")"
		}
		items = append(items, paletteItem{
			label: label,
			run:   func(app *App) { app.chatSetModel(m) },
		})
	}
	if len(items) == 0 {
		a.flash("Copilot chat offered no other models")
		return
	}
	title := "Chat model"
	if name := a.chatCurrentModelName(); name != "" {
		title += " (current: " + name + ")"
	}
	a.openPicker(title, items)
}

// chatSetModel fires the async session/set_model call for a picked
// roster entry; the outcome lands as a chatModelSetEvent.
func (a *App) chatSetModel(m chatModel) {
	if !a.chatReady() {
		a.flash("Copilot chat is not connected")
		return
	}
	client := a.chat.client
	sid := a.chat.sessionID
	scr := a.screen
	go func() {
		err := client.CallWithTimeout("session/set_model",
			map[string]any{"sessionId": sid, "modelId": m.id},
			nil, chatSessionTimeout)
		_ = scr.PostEvent(&chatModelSetEvent{when: time.Now(), model: m, err: err})
	}()
}

// handleChatModelSet lands the set_model outcome: on success record
// the session's new model, note it in the transcript, and persist the
// choice so the next session starts there. Failure only flashes — the
// session keeps its previous model, so there's nothing to roll back.
func (a *App) handleChatModelSet(e *chatModelSetEvent) {
	if e.err != nil {
		a.flash("Copilot chat: switching model failed: " + e.err.Error())
		return
	}
	a.chat.modelID = e.model.id
	a.chat.modelPref = e.model.id
	a.chatAppendMsg(chatMsg{role: chatRoleInfo, text: "Chat model: " + e.model.name})
	a.flash("Chat model: " + e.model.name)
	if err := userconfig.SaveChatModel(userconfig.DefaultPath(), e.model.id); err != nil {
		a.flash("chatmodel: " + err.Error())
	}
}

// -----------------------------------------------------------------------------
// Geometry — one source for draw AND mouse routing
// -----------------------------------------------------------------------------

// chatStripW is the total column count the chat strip consumes (panel
// + its splitter column): zero when closed. The layout helpers pivot
// on this exactly like termStripW.
func (a *App) chatStripW() int {
	if !a.chat.open {
		return 0
	}
	return a.chatPanelWidth()
}

// chatSplitterX returns the strip's resize handle column (its
// rightmost cell), or -1 when the panel is closed — the same contract
// as termSplitterX.
func (a *App) chatSplitterX() int {
	if sw := a.chatStripW(); sw > 0 {
		return sw - 1
	}
	return -1
}

// chatPanelWidth returns the strip's column count for the current
// window: user width wins, auto mode takes a third of the screen, both
// re-clamped live so a window resize can't squeeze the editor out.
func (a *App) chatPanelWidth() int {
	w := a.chat.width
	if w == 0 {
		w = a.width / 3
	}
	if w < chatPanelMinWidth {
		w = chatPanelMinWidth
	}
	if max := a.maxChatPanelWidth(); w > max {
		w = max
	}
	return w
}

// maxChatPanelWidth is the widest the strip may grow while the editor
// keeps its minimum working columns next to the (right-docked) sidebar.
func (a *App) maxChatPanelWidth() int {
	max := a.width - a.sidebarW() - minEditorAfterDrag
	if max < chatPanelMinWidth {
		max = chatPanelMinWidth
	}
	return max
}

// resizeChatPanelWidth records a user-chosen strip width, clamped to
// the legal band, re-pinning the tail if the re-wrap moved it.
func (a *App) resizeChatPanelWidth(target int) {
	if target < chatPanelMinWidth {
		target = chatPanelMinWidth
	}
	if max := a.maxChatPanelWidth(); target > max {
		target = max
	}
	atBottom := a.chatAtBottom()
	a.chat.width = target
	a.chatPanelScroll(0) // re-clamp against the re-wrapped row count
	if atBottom {
		a.chat.scroll = a.chatMaxScroll()
	}
}

// chatPanelRect returns the panel's on-screen rectangle: a full-height
// strip on the left edge, one column narrower than the strip — the
// rightmost column belongs to the splitter, same convention as the
// left-docked terminal.
func (a *App) chatPanelRect() (x, y, w, h int) {
	return 0, 0, a.chatStripW() - 1, a.height - 1
}

// chatPanelContains reports whether (x, y) falls inside the open panel.
func (a *App) chatPanelContains(x, y int) bool {
	if !a.chat.open {
		return false
	}
	px, py, pw, ph := a.chatPanelRect()
	return x >= px && x < px+pw && y >= py && y < py+ph
}

// chatCloseRect is the ✕ button's rectangle in the header row —
// computed once so draw and hit-testing can't drift (btnRect rule).
func (a *App) chatCloseRect() btnRect {
	px, py, pw, _ := a.chatPanelRect()
	return btnRect{x: px + pw - 4, y: py, w: 3}
}

// chatStopRect is the ⏹ button's rectangle, left of the ✕. Only live
// while a turn runs; draw and hit-test both gate on turnActive.
func (a *App) chatStopRect() btnRect {
	c := a.chatCloseRect()
	return btnRect{x: c.x - 4, y: c.y, w: 3}
}

// chatInputSpan returns the input row's y and the field's [start, end)
// columns after the prompt — the one geometry source for drawing,
// caret placement, and click-to-position.
func (a *App) chatInputSpan() (y, start, end int) {
	px, py, pw, ph := a.chatPanelRect()
	y = py + ph - 1
	start = px + 1 + runeLen(a.chatPrompt())
	end = px + pw - 1
	if start > end {
		start = end
	}
	return
}

// chatPrompt is the input row's gutter text: a run indicator while a
// turn is in flight, a caret glyph otherwise.
func (a *App) chatPrompt() string {
	if a.chat.turnActive {
		return "⋯ "
	}
	return "❯ "
}

// -----------------------------------------------------------------------------
// Keyboard + mouse
// -----------------------------------------------------------------------------

// handleChatKey processes a keystroke while the panel has focus. Esc
// never reaches here (the global handler consumes it first), so
// leaders and the double-Esc menu keep working from inside the chat.
func (a *App) handleChatKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEnter:
		a.chatSend()
	case tcell.KeyUp:
		a.chatHistoryMove(-1)
	case tcell.KeyDown:
		a.chatHistoryMove(1)
	case tcell.KeyPgUp:
		a.chatPanelScroll(-a.chatVisibleRows())
	case tcell.KeyPgDn:
		a.chatPanelScroll(a.chatVisibleRows())
	default:
		a.chat.input.handleKey(ev)
	}
}

// chatPanelPress routes an initial left press inside the panel and
// names the drag mode it starts ("" for none, "chatsel" for a
// transcript drag-select — the gitPanelPress convention). Body clicks
// focus the input; a click on the input row also repositions the
// caret; a click on a ⧉ affordance copies instead of selecting.
func (a *App) chatPanelPress(x, y int) string {
	if a.chatCloseRect().contains(x, y) {
		a.chat.open = false
		a.chat.focused = false
		return ""
	}
	if a.chat.turnActive && a.chatStopRect().contains(x, y) {
		a.chatInterrupt()
		return ""
	}
	a.chat.focused = true
	a.term.focused = false
	iy, start, end := a.chatInputSpan()
	if y == iy {
		a.chat.input.clickAt(start, end, x)
		return ""
	}
	// The chip block owns its rows outright — a drag started on a chip
	// must not select transcript prose behind it.
	if a.chatAttachPress(x, y) {
		return ""
	}
	if a.chatActionPress(x, y) {
		return ""
	}
	// Anywhere else in the transcript body starts a fresh selection.
	// Empty until the drag moves, so a plain click just clears.
	_, py, _, ph := a.chatPanelRect()
	if y <= py || y >= py+ph-1 {
		return ""
	}
	pos := a.chatPosAt(x, y)
	a.chat.selActive = true
	a.chat.selAnchor = pos
	a.chat.selHead = pos
	return "chatsel"
}

// chatActionPress fires the ⧉ button under (x, y) if one is there,
// reporting whether it consumed the press. Rect math goes through
// chatActionRect so the click target can't drift from the glyph.
func (a *App) chatActionPress(x, y int) bool {
	px, py, _, _ := a.chatPanelRect()
	w := a.chatRowWidth()
	rows := a.chatRows(w)
	idx := a.chat.scroll + (y - py - 1)
	if idx < 0 || idx >= len(rows) {
		return false
	}
	row := rows[idx]
	if row.action == chatRowNone || !chatActionRect(row, px+1, y, w).contains(x, y) {
		return false
	}
	switch row.action {
	case chatRowCopyMsg:
		a.chatCopyMessage(row.msgIdx)
	case chatRowCopyAll:
		a.chatCopyTranscript()
	}
	return true
}

// chatPanelDrag extends the transcript selection to the mouse. Called
// from the "chatsel" drag continuation, so it also runs for events
// outside the panel — chatPosAt clamps them.
func (a *App) chatPanelDrag(x, y int) {
	if !a.chat.selActive {
		return
	}
	a.chat.selHead = a.chatPosAt(x, y)
}

// chatPasteClip inserts the text clipboard into the input line — the
// Cmd+V path while the chat has focus. Newlines become spaces: the
// composer is single-line (a multi-line composer is a known phase-3
// follow-up), and flattening a pasted snippet beats dropping most of
// it.
func (a *App) chatPasteClip() {
	if a.clipBuf == "" {
		return
	}
	for _, r := range a.clipBuf {
		if r == '\n' || r == '\r' {
			r = ' '
		}
		if r < 0x20 {
			continue
		}
		a.chat.input.handleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
}

// -----------------------------------------------------------------------------
// Drawing
// -----------------------------------------------------------------------------

// drawChatPanel paints the strip: header rule with title, status and
// buttons; the wrapped transcript; the attachment chips; and the prompt
// + input row. When the panel has focus the hardware cursor moves to the
// input caret.
func (a *App) drawChatPanel() {
	px, py, pw, _ := a.chatPanelRect()
	th := a.theme

	headerSt := tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Subtle)
	titleSt := tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Accent).Bold(true)
	statusSt := tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Muted)
	bodyBG := tcell.StyleDefault.Background(th.BG)

	// Header rule: title + a one-word status on the left, ⏹ (while a
	// turn runs) and ✕ on the right.
	for cx := px; cx < px+pw; cx++ {
		a.screen.SetContent(cx, py, '─', nil, headerSt)
	}
	closeBtn := a.chatCloseRect()
	// The header text may not run under the buttons on a narrow panel.
	avail := closeBtn.x - (px + 1)
	if a.chat.turnActive {
		avail = a.chatStopRect().x - (px + 1)
	}
	title := chatFitHeader(a.chatHeaderTitle(), avail)
	drawAt(a.screen, px+1, py, title, titleSt)
	if status := a.chatHeaderStatus(); status != "" {
		status = chatFitHeader("· "+status+" ", avail-runeLen(title))
		drawAt(a.screen, px+1+runeLen(title), py, status, statusSt)
	}
	drawAt(a.screen, closeBtn.x, closeBtn.y, " ✕ ", titleSt)
	if a.chat.turnActive {
		stopBtn := a.chatStopRect()
		drawAt(a.screen, stopBtn.x, stopBtn.y, " ⏹ ",
			tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Error).Bold(true))
	}

	// Transcript rows.
	rows := a.chatRows(pw - 2)
	for row, visible := 0, a.chatVisibleRows(); row < visible; row++ {
		ry := py + 1 + row
		for cx := px; cx < px+pw; cx++ {
			a.screen.SetContent(cx, ry, ' ', nil, bodyBG)
		}
		idx := a.chat.scroll + row
		if idx < 0 || idx >= len(rows) {
			continue
		}
		a.drawChatRow(rows[idx], px+1, ry, pw-2, idx)
	}

	// Attachment chips, between the transcript and the composer.
	a.drawChatAttachments()

	// Input row: prompt gutter + editable field.
	iy, start, end := a.chatInputSpan()
	for cx := px; cx < px+pw; cx++ {
		a.screen.SetContent(cx, iy, ' ', nil, bodyBG)
	}
	promptSt := tcell.StyleDefault.Background(th.BG).Foreground(th.Accent).Bold(true)
	if a.chat.turnActive {
		promptSt = tcell.StyleDefault.Background(th.BG).Foreground(th.Muted)
	}
	drawAt(a.screen, px+1, iy, a.chatPrompt(), promptSt)
	inputSt := tcell.StyleDefault.Background(th.BG).Foreground(th.Text)
	a.chat.input.draw(a.screen, iy, start, end, inputSt, a.chat.focused)
}

// chatHeaderTitle names the panel: "Copilot - <model>" once a session
// has told us which model answers, plain "Copilot chat" until then —
// the model is the one thing about a chat panel you can't infer from
// looking at it, and the ≡ row that switches it is two clicks away.
func (a *App) chatHeaderTitle() string {
	if name := a.chatCurrentModelName(); name != "" {
		return " Copilot - " + name + " "
	}
	return " Copilot chat "
}

// chatFitHeader clips a header segment to the columns left before the
// ⏹/✕ buttons, marking the cut with an ellipsis. A width that can't
// hold even that yields "" — better a missing segment than one running
// under a button.
func chatFitHeader(s string, width int) string {
	if width >= runeLen(s) {
		return s
	}
	if width < 2 {
		return ""
	}
	r := []rune(s)
	return string(r[:width-1]) + "…"
}

// chatHeaderStatus is the header's one-word state summary — empty in
// the steady signed-in state, where saying nothing is the status.
func (a *App) chatHeaderStatus() string {
	switch {
	case a.chat.dead:
		return "unavailable"
	case a.chat.starting:
		return "starting…"
	case a.chat.turnActive:
		return "thinking…"
	}
	return ""
}

// drawChatRow paints one wrapped transcript row in its role's style:
// user prompts in accent, code rows set off on the sidebar background,
// tool notes muted, editor info muted italic. Action rows paint their
// ⧉ affordance right-aligned instead. idx is the row's index in the
// derived row space — what the selection highlight is keyed on.
func (a *App) drawChatRow(row chatRow, x, ry, w int, idx int) {
	if w <= 0 {
		return
	}
	th := a.theme
	if row.action != chatRowNone {
		btn := chatActionRect(row, x, ry, w)
		if btn.w > 0 {
			drawAt(a.screen, btn.x, btn.y, chatRowLabel(row),
				tcell.StyleDefault.Background(th.BG).Foreground(th.Subtle))
		}
		return
	}
	if row.text == "" {
		return
	}
	st := tcell.StyleDefault.Background(th.BG).Foreground(th.Text)
	switch {
	case row.code:
		st = tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Text)
	case row.role == chatRoleUser:
		st = st.Foreground(th.Accent).Bold(true)
	case row.role == chatRoleTool:
		st = st.Foreground(th.Muted)
	case row.role == chatRoleInfo:
		st = st.Foreground(th.Muted).Italic(true)
	}
	text := row.text
	if runeLen(text) > w {
		text = string([]rune(text)[:w-1]) + "…"
	}
	drawAt(a.screen, x, ry, text, st)
	// Selection last, so the highlight wins over every role style —
	// same precedence the editor's decoration merge gives it. Spans are
	// measured against the FULL row (chatRowSelSpan's runeCount), then
	// clipped to what actually fits.
	from, to := a.chatRowSelSpan(idx, runeLen(row.text))
	if from == to {
		return
	}
	runes := []rune(text)
	selSt := st.Background(th.Selection).Foreground(th.Text)
	for c := from; c < to && c < len(runes); c++ {
		a.screen.SetContent(x+c, ry, runes[c], nil, selSt)
	}
}

// drawChatSplitter paints the strip's resize handle on its
// editor-facing (right) edge — the same visual language as the sidebar
// and terminal splitters.
func (a *App) drawChatSplitter() {
	x := a.chatSplitterX()
	if x < 0 {
		return
	}
	fg := a.theme.Subtle
	if a.dragMode == "chatsplit" {
		fg = a.theme.Accent
	}
	style := tcell.StyleDefault.Background(a.theme.SidebarBG).Foreground(fg)
	for y := 0; y < a.height-1; y++ {
		a.screen.SetContent(x, y, '│', nil, style)
	}
}
