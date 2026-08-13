// =============================================================================
// File: internal/app/copilot_chat_perm.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-29
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// copilot_chat_perm.go is phase 4 of the AI chat integration: the
// permission UI and the client-side filesystem the Agent Client
// Protocol expects. Phase 3 kept the panel chat-only by declaring no fs
// capabilities and auto-declining every session/request_permission;
// this file replaces that scope guard with real answers:
//
//   - session/request_permission surfaces as a picker modal (the house
//     rule for every choose-one-from-a-list UI) offering the agent's
//     own options — Allow / Always Allow / Reject… — and the user's
//     pick goes back as the JSON-RPC response.
//   - fs/read_text_file serves the OPEN TAB'S BUFFER when the file is
//     open (unsaved edits included — the same rationale as context
//     attachments: the on-disk copy of the file you just changed is the
//     one answer that's quietly wrong), falling back to disk.
//   - fs/write_text_file writes the file and immediately runs the same
//     reconciliation pipeline the 10s tick uses, so a clean open tab
//     reloads the agent's edit and a dirty one gets the overwrite
//     warning — agent edits are just external changes.
//
// The threading dance: agent requests arrive on goroutines the client
// spawns per request (see lsp.Client.onRequest — it is explicitly NOT
// the read loop, so blocking here is safe). The chatServe* helpers run
// there: they post an event carrying a reply channel, block until the
// main loop answers, and hand the result back to the transport. Only
// the main-loop handlers touch App — the same events-only contract as
// every other chat surface.
//
// Read-only chat (config "chatwrite", ≡ toggle, default on) is the
// coarse switch above all of that: with it off, ced declares no write
// capability at the handshake, refuses fs/write_text_file outright, and
// auto-rejects any permission request the agent labelled as a mutation
// (chatMutatingKinds) instead of prompting. The per-request prompt is
// the primary guard — this is the posture for when you want to ask
// questions with no possibility of an edit landing regardless of how a
// prompt gets answered.
//
// Confinement: every fs path is resolved against the project root and
// anything outside it is refused with an error the agent can read. The
// check is lexical (Clean + Rel), the same trust level as the rest of
// the editor — ced is not a sandbox, but an agent must not wander into
// $HOME through a chat panel.
//
// Permission lifecycle rules:
//
//   - Requests QUEUE (chat.permQueue); one picker at a time. The
//     dispatch-tail hook (chatPermAfterEvent) reopens the prompt when
//     the modal slot frees up, so a request that arrived behind the
//     finder or the menu resurfaces instead of rotting.
//   - Every request is answered EXACTLY ONCE (the answered flag):
//     picking answers with the pick, dismissing (Esc / click outside)
//     answers with the agent's own reject option, teardown and turn
//     cancellation answer with the cancelled outcome — the spec's
//     required response to permissions pending when a turn dies. The
//     serve goroutine's timeout is the backstop for a user who walks
//     away; a late UI answer then lands in a buffered channel nobody
//     reads, which is harmless.
//   - The decision is echoed into the transcript ("✓ allowed" /
//     "⊘ rejected") — the agent's next answer often references what it
//     was or wasn't allowed to do, and that line is its referent.

package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/userconfig"
)

// chatFSTimeout bounds how long a serve goroutine waits for the main
// loop to answer an fs request. The handler is a map lookup plus a file
// read/write — seconds of silence mean the editor is wedged, and the
// agent deserves an error over a hang.
const chatFSTimeout = 10 * time.Second

// chatPermOption is one choice the agent offered with a permission
// request: the wire id to answer with, the display name for the picker
// row, and the kind ("allow_once", "reject_always", …) that styles the
// transcript note.
type chatPermOption struct {
	id, name, kind string
}

// chatPermRequest is one pending session/request_permission: what the
// agent wants to do, the choices it offered, and the channel the
// blocked serve goroutine is waiting on. answered is main-loop-only
// state guaranteeing exactly one reply ever goes out. kind is the tool
// call's ACP kind ("read", "edit", "execute", …) — the only signal we
// get about whether saying yes would change something.
type chatPermRequest struct {
	title    string
	kind     string
	options  []chatPermOption
	reply    chan any // buffered(1): a send never blocks the main loop
	answered bool
}

// chatPermRequestEvent carries a parsed permission request from the
// serve goroutine to the main loop. seq/sessionID guard against a
// request straggling in from a torn-down connection.
type chatPermRequestEvent struct {
	when      time.Time
	seq       int
	sessionID string
	req       *chatPermRequest
}

// When satisfies the tcell.Event interface.
func (e *chatPermRequestEvent) When() time.Time { return e.when }

// chatFSResult is the main loop's answer to one fs request: the JSON
// result on success, or the error the transport should surface.
type chatFSResult struct {
	result any
	err    error
}

// chatFSRequestEvent carries one fs/read_text_file or
// fs/write_text_file request to the main loop, reply-channel pattern
// as the permission event.
type chatFSRequestEvent struct {
	when      time.Time
	seq       int
	sessionID string
	write     bool
	path      string
	content   string
	line      int // 1-based first line to read; 0 = start of file
	limit     int // max lines to read; 0 = to the end
	reply     chan chatFSResult
}

// When satisfies the tcell.Event interface.
func (e *chatFSRequestEvent) When() time.Time { return e.when }

// -----------------------------------------------------------------------------
// Serve side — runs on the client's per-request goroutines, never the
// main loop. Post an event, block on the reply, hand back the result.
// -----------------------------------------------------------------------------

// chatParsePermission decodes a session/request_permission's params
// into a fresh request with its reply channel armed. Pure, so tests
// can pin the wire shapes without a connection.
func chatParsePermission(params json.RawMessage) (sessionID string, req *chatPermRequest) {
	var p struct {
		SessionID string `json:"sessionId"`
		ToolCall  struct {
			Title string `json:"title"`
			Kind  string `json:"kind"`
		} `json:"toolCall"`
		Options []struct {
			OptionID string `json:"optionId"`
			Name     string `json:"name"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(params, &p)
	req = &chatPermRequest{title: p.ToolCall.Title,
		kind:  strings.ToLower(strings.TrimSpace(p.ToolCall.Kind)),
		reply: make(chan any, 1)}
	for _, o := range p.Options {
		if o.OptionID == "" {
			continue // an unanswerable option is worse than none
		}
		req.options = append(req.options, chatPermOption{id: o.OptionID, name: o.Name, kind: o.Kind})
	}
	return p.SessionID, req
}

// chatServePermission answers one session/request_permission: hand the
// request to the main loop and block until the user (or a teardown
// path) settles it. The timeout matches the turn's own budget — if the
// user walks away, the turn dies at the same moment this does, and the
// reject is the honest last word.
func chatServePermission(scr tcell.Screen, seq int, params json.RawMessage) (any, error) {
	sessionID, req := chatParsePermission(params)
	_ = scr.PostEvent(&chatPermRequestEvent{when: time.Now(), seq: seq, sessionID: sessionID, req: req})
	select {
	case res := <-req.reply:
		return res, nil
	case <-time.After(chatTurnTimeout):
		return chatPermRejectResult(req.options), nil
	}
}

// chatServeFS answers one fs/read_text_file or fs/write_text_file,
// same bridge: post, block briefly, relay.
func chatServeFS(scr tcell.Screen, seq int, write bool, params json.RawMessage) (any, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Path      string `json:"path"`
		Content   string `json:"content"`
		Line      int    `json:"line"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("bad fs params: %v", err)
	}
	ev := &chatFSRequestEvent{when: time.Now(), seq: seq, sessionID: p.SessionID,
		write: write, path: p.Path, content: p.Content, line: p.Line, limit: p.Limit,
		reply: make(chan chatFSResult, 1)}
	_ = scr.PostEvent(ev)
	select {
	case r := <-ev.reply:
		return r.result, r.err
	case <-time.After(chatFSTimeout):
		return nil, fmt.Errorf("editor did not answer in time")
	}
}

// -----------------------------------------------------------------------------
// Outcome builders — the session/request_permission response shapes.
// -----------------------------------------------------------------------------

// chatPermSelectResult is the "the user picked this option" response.
func chatPermSelectResult(optionID string) any {
	return map[string]any{
		"outcome": map[string]any{"outcome": "selected", "optionId": optionID},
	}
}

// chatPermCancelResult is the cancelled outcome — the spec's required
// answer for permissions pending when the turn is cancelled or the
// connection dies.
func chatPermCancelResult() any {
	return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
}

// chatPermRejectResult declines with the agent's own reject option
// (once-scoped preferred, so nothing is remembered against a future
// ask), falling back to the cancelled outcome when the agent offered no
// reject at all.
func chatPermRejectResult(options []chatPermOption) any {
	for _, kind := range []string{"reject_once", "reject_always"} {
		for _, o := range options {
			if o.kind == kind {
				return chatPermSelectResult(o.id)
			}
		}
	}
	return chatPermCancelResult()
}

// -----------------------------------------------------------------------------
// Read-only chat — the "the agent may not change anything" switch
// -----------------------------------------------------------------------------

// chatMutatingKinds is the set of ACP tool-call kinds that can change
// something on disk. "execute" is in the list because a shell command is
// a write with extra steps — a read-only mode that let one through would
// be a read-only mode in name only. Kinds that only look ("read",
// "search", "fetch", "think") and kinds we don't recognise are NOT here:
// an unrecognised kind still gets the normal prompt, because
// auto-rejecting everything an agent forgot to label would make the mode
// useless rather than safe. ced's own write path is refused outright
// regardless, which is the guarantee this mode actually rests on.
var chatMutatingKinds = map[string]bool{
	"edit": true, "delete": true, "move": true, "execute": true,
}

// chatWriteEnabled reports whether the agent may change files. Reading
// through a method (not the field) keeps the policy in one place for the
// three enforcement points: the declared capability, the fs write
// handler, and the permission gate.
func (a *App) chatWriteEnabled() bool { return a.chat.writeEnabled }

// chatWriteBlocked reports whether this permission request must be
// refused without asking: a mutating tool call while the panel is in
// read-only mode. Prompting for something the user has already said no
// to is a question with one honest answer — better to answer it and say
// why in the transcript.
func (a *App) chatWriteBlocked(req *chatPermRequest) bool {
	return !a.chatWriteEnabled() && chatMutatingKinds[req.kind]
}

// chatWriteToggleLabel names the ≡ row for its current direction, the
// auto-save toggle's flip-in-place style.
func (a *App) chatWriteToggleLabel() string {
	if a.chat.writeEnabled {
		return "Block agent file changes (read-only chat)"
	}
	return "Allow agent file changes"
}

// menuToggleChatWrite flips read-only chat from the ≡ menu.
func (a *App) menuToggleChatWrite() {
	a.closeMenu()
	a.setChatWrite(!a.chat.writeEnabled)
}

// setChatWrite applies and persists the agent-may-write preference. The
// enforcement paths read the flag live, so switching to read-only takes
// hold on the very next request — including one already queued. The
// DECLARED capability is a handshake artifact, though, so an attached
// agent keeps whatever it was told at connect time; the transcript note
// says so rather than letting the user assume a restart happened.
func (a *App) setChatWrite(on bool) {
	if a.chat.writeEnabled == on {
		return
	}
	a.chat.writeEnabled = on
	note := "Read-only chat: " + a.chatAgent().name + " may not change files"
	if on {
		note = a.chatAgent().name + " may change files (each change asks first)"
	}
	if a.chatReady() {
		note += " — restart the agent to update what it was told it can do"
	}
	a.chatAppendMsg(chatMsg{role: chatRoleInfo, text: note})
	a.flash(note)
	if err := userconfig.SaveChatWrite(userconfig.DefaultPath(), on); err != nil {
		a.flash("chatwrite: " + err.Error())
	}
}

// -----------------------------------------------------------------------------
// Permission UI — main loop only
// -----------------------------------------------------------------------------

// handleChatPermRequest lands one permission request: queue it and show
// the prompt if the modal slot is free. A stale generation or session
// gets the reject straight back — that request belongs to a connection
// the user already tore down.
func (a *App) handleChatPermRequest(e *chatPermRequestEvent) {
	if e.seq != a.chat.connSeq || (e.sessionID != "" && e.sessionID != a.chat.sessionID) {
		e.req.reply <- chatPermRejectResult(e.req.options)
		return
	}
	if len(e.req.options) == 0 {
		// Nothing to offer the user — answer cancelled rather than
		// showing an empty picker with no legal outcome.
		e.req.reply <- chatPermCancelResult()
		return
	}
	if a.chatWriteBlocked(e.req) {
		// Read-only chat: refuse without asking, but say so in the
		// transcript — a silent refusal reads as the agent stalling.
		title := e.req.title
		if title == "" {
			title = "a tool call"
		}
		e.req.reply <- chatPermRejectResult(e.req.options)
		e.req.answered = true
		a.chatAppendMsg(chatMsg{role: chatRoleTool,
			text: "⊘ rejected (read-only chat): " + title})
		return
	}
	a.chat.permQueue = append(a.chat.permQueue, e.req)
	a.chatMaybeOpenPermission()
}

// chatMaybeOpenPermission shows the prompt for the queue's head when
// nothing else owns the modal slot. Deliberately polite: it never
// steals the slot from an open modal or the menu — the dispatch-tail
// hook retries once they close.
func (a *App) chatMaybeOpenPermission() {
	if len(a.chat.permQueue) == 0 || a.modal != nil || a.menuOpen {
		return
	}
	req := a.chat.permQueue[0]
	items := make([]paletteItem, 0, len(req.options))
	for _, opt := range req.options {
		opt := opt // capture per-iteration for the closure
		label := opt.name
		if label == "" {
			label = opt.kind // an unnamed option still needs a row
		}
		items = append(items, paletteItem{
			label: label,
			run:   func(app *App) { app.chatAnswerPermission(req, opt) },
		})
	}
	title := req.title
	if title == "" {
		title = "a tool call"
	}
	a.chat.permModal = a.openPickerWithCancel(
		a.chatAgent().name+" asks: "+title, items,
		func(app *App) { app.chatDismissPermission(req) })
	// The agent is stopped until this is answered, and the user may well
	// have walked away expecting it to run — the case the host's
	// notification story exists for. See cats_glue.go.
	a.catsAsking(a.chatAgent().name + " needs an answer")
}

// chatAnswerPermission settles a request with the user's pick.
func (a *App) chatAnswerPermission(req *chatPermRequest, opt chatPermOption) {
	a.chatSettlePermission(req, chatPermSelectResult(opt.id), chatPermNote(req.title, opt))
}

// chatDismissPermission settles a request the user waved away (Esc,
// click outside): the agent's own reject option, once-scoped. A
// dismissal must still answer — the agent is blocked on it.
func (a *App) chatDismissPermission(req *chatPermRequest) {
	title := req.title
	if title == "" {
		title = "a tool call"
	}
	a.chatSettlePermission(req, chatPermRejectResult(req.options), "⊘ rejected: "+title)
}

// chatSettlePermission is the single answer path: send the reply once,
// drop the request from the queue, close its picker if it is still up,
// note the decision, and surface the next queued request.
func (a *App) chatSettlePermission(req *chatPermRequest, result any, note string) {
	if req.answered {
		return
	}
	req.answered = true
	req.reply <- result
	for i, q := range a.chat.permQueue {
		if q == req {
			a.chat.permQueue = append(a.chat.permQueue[:i], a.chat.permQueue[i+1:]...)
			break
		}
	}
	if a.chat.permModal != nil && a.modal == a.chat.permModal {
		a.closeModal()
	}
	a.chat.permModal = nil
	a.chatAppendMsg(chatMsg{role: chatRoleTool, text: note})
	a.chatMaybeOpenPermission()
}

// chatPermNote renders a decision for the transcript, keyed on the
// option's kind so "Always Allow" and "Allow" both read as ✓. Unknown
// kinds fall back to the option's own name — never a silent line.
func chatPermNote(title string, opt chatPermOption) string {
	if title == "" {
		title = "a tool call"
	}
	switch {
	case strings.HasPrefix(opt.kind, "allow"):
		return "✓ allowed: " + title
	case strings.HasPrefix(opt.kind, "reject"):
		return "⊘ rejected: " + title
	}
	name := opt.name
	if name == "" {
		name = opt.kind
	}
	return "· " + name + ": " + title
}

// chatFlushPermissions answers every pending request with the
// cancelled outcome and closes the prompt — the spec-required cleanup
// when the turn is cancelled or the connection goes away. Quiet on
// purpose: the teardown that called it already narrates itself.
func (a *App) chatFlushPermissions() {
	for _, req := range a.chat.permQueue {
		if req.answered {
			continue
		}
		req.answered = true
		req.reply <- chatPermCancelResult()
	}
	a.chat.permQueue = nil
	if a.chat.permModal != nil && a.modal == a.chat.permModal {
		a.closeModal()
	}
	a.chat.permModal = nil
}

// chatPermAfterEvent runs at the tail of every dispatch (the
// lspAfterEvent pattern): if a permission request is waiting and the
// modal slot just freed up — the user closed the finder, the menu, a
// prompt — the request resurfaces instead of silently timing out. A
// couple of comparisons when idle.
func (a *App) chatPermAfterEvent() {
	a.chatMaybeOpenPermission()
}

// -----------------------------------------------------------------------------
// Client-side filesystem — main loop only
// -----------------------------------------------------------------------------

// handleChatFSRequest lands one fs request: resolve and confine the
// path, then serve the read from the freshest text we have or apply the
// write and reconcile. The reply channel is buffered, so answering a
// request whose serve goroutine already timed out is harmless.
func (a *App) handleChatFSRequest(e *chatFSRequestEvent) {
	if e.seq != a.chat.connSeq || (e.sessionID != "" && e.sessionID != a.chat.sessionID) {
		e.reply <- chatFSResult{err: fmt.Errorf("session is gone")}
		return
	}
	if e.write && !a.chatWriteEnabled() {
		// Refused even though the handshake declared no write
		// capability: capabilities are advisory, and an agent that asks
		// anyway deserves a reason it can read and relay.
		e.reply <- chatFSResult{err: fmt.Errorf(
			"ced is in read-only chat mode — file changes are disabled")}
		return
	}
	path, err := a.chatFSResolve(e.path)
	if err != nil {
		e.reply <- chatFSResult{err: err}
		return
	}
	if e.write {
		e.reply <- chatFSResult{err: a.chatFSWrite(path, e.content)}
		return
	}
	content, err := a.chatFSRead(path, e.line, e.limit)
	if err != nil {
		e.reply <- chatFSResult{err: err}
		return
	}
	e.reply <- chatFSResult{result: map[string]any{"content": content}}
}

// chatFSResolve absolutizes an agent-supplied path and confines it to
// the project root. Lexical containment (Clean + Rel) — the same trust
// model as the rest of the editor, aimed at keeping a confused agent
// inside the project, not at defeating a hostile one.
func (a *App) chatFSResolve(path string) (string, error) {
	return a.resolveInRoot(path)
}

// resolveInRoot absolutises path against the project root and refuses
// anything that escapes it. One implementation, because every surface that
// takes a path from something other than the user — the chat agent, a
// language server's workspace edit, a git-log jump, terminal output — owes
// the same answer, and a second copy would eventually disagree.
//
// The check is LEXICAL, then repeated after resolving symlinks. Lexical
// alone is escapable here in a way it isn't for a pure read: writeFileAtomic
// resolves symlinks before writing (fileio.go), so a link inside the root
// pointing outside it would pass a textual test and then be written through.
// A link that can't be resolved (it doesn't exist yet) keeps the lexical
// answer — that's the ordinary "save creates the file" case.
func (a *App) resolveInRoot(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("empty path")
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(a.rootDir, abs)
	}
	abs = filepath.Clean(abs)
	if !a.insideRoot(abs) {
		return "", fmt.Errorf("%s is outside the project root", path)
	}
	// The resolved path is compared against the resolved ROOT, not the raw
	// one. The root itself routinely sits behind a link — /tmp is /private/tmp
	// on macOS, and /home is often a link on Linux — so resolving only one
	// side would put every file in such a project "outside" its own root.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		root := a.rootDir
		if rr, err := filepath.EvalSymlinks(root); err == nil {
			root = rr
		}
		if !pathInside(resolved, root) {
			return "", fmt.Errorf("%s resolves outside the project root", path)
		}
	}
	return abs, nil
}

// insideRoot reports whether abs (already absolute and cleaned) sits within
// the project root. Delegates to gitstatus.go's pathInside — one containment
// rule for the whole app, so a path the git panel considers inside the
// project can't be one a workspace edit considers outside it.
func (a *App) insideRoot(abs string) bool {
	return pathInside(abs, a.rootDir)
}

// chatFSRead serves a read from the open tab's buffer when the file is
// open — unsaved edits are the truth the agent should reason about —
// falling back to disk. line (1-based) and limit slice the result the
// way the ACP read request asks for it.
func (a *App) chatFSRead(path string, line, limit int) (string, error) {
	var lines []string
	if t := a.chatAttachTab(path); t != nil {
		lines = t.Buffer.Lines
	} else {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		lines = strings.Split(string(data), "\n")
	}
	if line > 0 {
		if line > len(lines) {
			lines = nil
		} else {
			lines = lines[line-1:]
		}
	}
	if limit > 0 && limit < len(lines) {
		lines = lines[:limit]
	}
	return strings.Join(lines, "\n"), nil
}

// chatFSWrite applies an agent write and immediately reconciles: the
// same pipeline the 10s tick runs, so a clean open tab reloads the new
// content and a dirty one gets the overwrite warning — an agent edit is
// just an external change with a name. The transcript note is the
// visible receipt; parent directories are created because "write a new
// file" legitimately includes new folders.
func (a *App) chatFSWrite(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	a.chatAppendMsg(chatMsg{role: chatRoleTool, text: "✎ wrote " + a.relativePathFor(path)})
	a.refreshTreeNow()
	return nil
}
