// =============================================================================
// File: internal/app/copilot_chat_perm_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-29
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the chat permission UI and client-side fs handlers
// (copilot_chat_perm.go). Everything is driven through the main-loop
// handlers with hand-built events — the serve goroutines are thin
// post-and-wait shims over the same channels these tests read directly,
// so no connection (and no timer wait) is ever needed.

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// permOutcomeJSON marshals a permission reply for substring asserts —
// the reply values are nested map[string]any, and their wire form is
// what actually matters.
func permOutcomeJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal outcome: %v", err)
	}
	return string(b)
}

// permEvent builds a valid-by-default permission event against the
// wired chat session, letting each test break exactly one thing.
func permEvent(title string, options ...chatPermOption) *chatPermRequestEvent {
	return &chatPermRequestEvent{
		when:      time.Now(),
		sessionID: "sess-1",
		req: &chatPermRequest{
			title:   title,
			options: options,
			reply:   make(chan any, 1),
		},
	}
}

// TestChatParsePermission pins the wire decode: title and options come
// through, and an option without an optionId is dropped — a row the
// client could never answer with.
func TestChatParsePermission(t *testing.T) {
	sid, req := chatParsePermission(json.RawMessage(`{
		"sessionId": "s-9",
		"toolCall": {"title": "Edit main.go"},
		"options": [
			{"optionId": "ok", "name": "Allow", "kind": "allow_once"},
			{"optionId": "", "name": "ghost", "kind": "allow_always"},
			{"optionId": "no", "name": "Reject", "kind": "reject_once"}
		]}`))
	if sid != "s-9" || req.title != "Edit main.go" {
		t.Errorf("sid=%q title=%q", sid, req.title)
	}
	if len(req.options) != 2 || req.options[0].id != "ok" || req.options[1].id != "no" {
		t.Errorf("options = %+v, want the two answerable ones", req.options)
	}
}

// TestChatPermRejectResult pins the decline preference order: the
// agent's reject_once beats reject_always, and no reject option at all
// falls back to the cancelled outcome.
func TestChatPermRejectResult(t *testing.T) {
	opts := []chatPermOption{
		{id: "always-no", kind: "reject_always"},
		{id: "no", kind: "reject_once"},
		{id: "ok", kind: "allow_once"},
	}
	if got := permOutcomeJSON(t, chatPermRejectResult(opts)); !strings.Contains(got, `"optionId":"no"`) {
		t.Errorf("reject = %s, want the reject_once option", got)
	}
	only := []chatPermOption{{id: "ok", kind: "allow_once"}}
	if got := permOutcomeJSON(t, chatPermRejectResult(only)); !strings.Contains(got, `"cancelled"`) {
		t.Errorf("no-reject fallback = %s, want cancelled", got)
	}
}

// TestHandleChatPermRequest_QueuesAndPrompts pins the happy path: a
// live-session request queues and its picker takes the modal slot,
// titled with the agent's ask.
func TestHandleChatPermRequest_QueuesAndPrompts(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)

	a.handleChatPermRequest(permEvent("Edit main.go",
		chatPermOption{id: "ok", name: "Allow", kind: "allow_once"},
		chatPermOption{id: "no", name: "Reject", kind: "reject_once"}))
	if len(a.chat.permQueue) != 1 {
		t.Fatalf("queue = %d, want 1", len(a.chat.permQueue))
	}
	m, ok := a.modal.(*paletteModal)
	if !ok || m != a.chat.permModal {
		t.Fatalf("modal = %T, want the tracked permission picker", a.modal)
	}
	if !strings.Contains(m.title, "Edit main.go") {
		t.Errorf("picker title = %q, want the tool title", m.title)
	}
	if len(m.items) != 2 || m.items[0].label != "Allow" {
		t.Errorf("picker items = %+v, want the agent's options", m.items)
	}
}

// TestHandleChatPermRequest_StaleRejects pins the connSeq guard: a
// request from a torn-down connection is answered (rejected) at once,
// never queued or shown — its agent is gone, but its goroutine still
// blocks on the reply.
func TestHandleChatPermRequest_StaleRejects(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	e := permEvent("Edit main.go", chatPermOption{id: "no", kind: "reject_once"})
	e.seq = a.chat.connSeq + 1
	a.handleChatPermRequest(e)
	if len(a.chat.permQueue) != 0 || a.modal != nil {
		t.Fatalf("stale request queued: queue=%d modal=%v", len(a.chat.permQueue), a.modal)
	}
	select {
	case res := <-e.req.reply:
		if got := permOutcomeJSON(t, res); !strings.Contains(got, `"optionId":"no"`) {
			t.Errorf("stale reply = %s, want the reject option", got)
		}
	default:
		t.Fatal("stale request never answered — its goroutine would block to timeout")
	}
}

// TestChatAnswerPermission pins the pick path: the chosen option goes
// back on the reply channel, the decision lands in the transcript, and
// the request leaves the queue.
func TestChatAnswerPermission(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	e := permEvent("Edit main.go", chatPermOption{id: "ok", name: "Allow", kind: "allow_once"})
	a.handleChatPermRequest(e)

	a.chatAnswerPermission(e.req, e.req.options[0])
	res := <-e.req.reply
	if got := permOutcomeJSON(t, res); !strings.Contains(got, `"optionId":"ok"`) {
		t.Errorf("reply = %s, want the picked option", got)
	}
	if len(a.chat.permQueue) != 0 || a.modal != nil {
		t.Errorf("after answer: queue=%d modal=%v, want empty/closed", len(a.chat.permQueue), a.modal)
	}
	last := a.chat.msgs[len(a.chat.msgs)-1]
	if last.role != chatRoleTool || !strings.Contains(last.text, "✓ allowed: Edit main.go") {
		t.Errorf("transcript = %+v, want the ✓ allowed note", last)
	}
}

// TestChatDismissPermission_ViaEsc pins the cancel hook end to end: Esc
// on the picker still answers the agent — with its own reject option —
// because a dismissal the agent never hears about blocks the turn until
// timeout.
func TestChatDismissPermission_ViaEsc(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	e := permEvent("Run go test",
		chatPermOption{id: "ok", name: "Allow", kind: "allow_once"},
		chatPermOption{id: "no", name: "Reject", kind: "reject_once"})
	a.handleChatPermRequest(e)

	a.modal.handleKey(a, tcell.NewEventKey(tcell.KeyEsc, 0, 0))
	select {
	case res := <-e.req.reply:
		if got := permOutcomeJSON(t, res); !strings.Contains(got, `"optionId":"no"`) {
			t.Errorf("Esc reply = %s, want the reject option", got)
		}
	default:
		t.Fatal("Esc never answered the agent")
	}
	last := a.chat.msgs[len(a.chat.msgs)-1]
	if !strings.Contains(last.text, "⊘ rejected: Run go test") {
		t.Errorf("transcript = %q, want the ⊘ rejected note", last.text)
	}
}

// TestChatPermQueue_NextPromptAfterAnswer pins the queue drain: with
// two requests pending, settling the first immediately surfaces the
// second's picker.
func TestChatPermQueue_NextPromptAfterAnswer(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	first := permEvent("Edit a.go", chatPermOption{id: "ok", name: "Allow", kind: "allow_once"})
	second := permEvent("Edit b.go", chatPermOption{id: "ok2", name: "Allow", kind: "allow_once"})
	a.handleChatPermRequest(first)
	a.handleChatPermRequest(second)
	if len(a.chat.permQueue) != 2 {
		t.Fatalf("queue = %d, want 2", len(a.chat.permQueue))
	}

	a.chatAnswerPermission(first.req, first.req.options[0])
	m, ok := a.modal.(*paletteModal)
	if !ok || !strings.Contains(m.title, "Edit b.go") {
		t.Fatalf("next prompt = %T %v, want the second request's picker", a.modal, a.modal)
	}
}

// TestChatPermAfterEvent_ReopensWhenSlotFrees pins the dispatch-tail
// hook: a request that arrived while another modal held the slot
// resurfaces once that modal closes.
func TestChatPermAfterEvent_ReopensWhenSlotFrees(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.openPicker("something else", []paletteItem{{label: "row"}})
	other := a.modal

	a.handleChatPermRequest(permEvent("Edit main.go",
		chatPermOption{id: "ok", name: "Allow", kind: "allow_once"}))
	if a.modal != other {
		t.Fatal("permission prompt stole the modal slot")
	}

	a.closeModal()
	a.chatPermAfterEvent()
	if m, ok := a.modal.(*paletteModal); !ok || m != a.chat.permModal {
		t.Fatalf("modal = %T, want the queued permission picker", a.modal)
	}
}

// TestChatFlushPermissions_OnDisconnect pins teardown: disconnecting
// answers every pending request with the cancelled outcome and closes
// the prompt, so no serve goroutine outlives its connection un-answered.
func TestChatFlushPermissions_OnDisconnect(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	e := permEvent("Edit main.go", chatPermOption{id: "ok", name: "Allow", kind: "allow_once"})
	a.handleChatPermRequest(e)

	a.chatDisconnect()
	select {
	case res := <-e.req.reply:
		if got := permOutcomeJSON(t, res); !strings.Contains(got, `"cancelled"`) {
			t.Errorf("disconnect reply = %s, want cancelled", got)
		}
	default:
		t.Fatal("disconnect left the request un-answered")
	}
	if len(a.chat.permQueue) != 0 || a.modal != nil {
		t.Errorf("after disconnect: queue=%d modal=%v", len(a.chat.permQueue), a.modal)
	}
}

// fsEvent builds a valid-by-default fs event against the wired chat
// session, mirroring permEvent.
func fsEvent(write bool, path string) *chatFSRequestEvent {
	return &chatFSRequestEvent{
		when:      time.Now(),
		sessionID: "sess-1",
		write:     write,
		path:      path,
		reply:     make(chan chatFSResult, 1),
	}
}

// TestChatFSResolve pins the root confinement: project paths resolve
// (relative ones against the root), and anything that escapes — or an
// empty path — is refused.
func TestChatFSResolve(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	abs, err := a.chatFSResolve("sub/x.go")
	if err != nil || abs != filepath.Join(a.rootDir, "sub", "x.go") {
		t.Errorf("relative: %q, %v", abs, err)
	}
	if _, err := a.chatFSResolve(filepath.Join(a.rootDir, "y.go")); err != nil {
		t.Errorf("absolute inside root: %v", err)
	}
	if _, err := a.chatFSResolve("../outside.go"); err == nil {
		t.Error("escape via .. should be refused")
	}
	if _, err := a.chatFSResolve("/etc/passwd"); err == nil {
		t.Error("absolute path outside root should be refused")
	}
	if _, err := a.chatFSResolve("  "); err == nil {
		t.Error("empty path should be refused")
	}
}

// TestHandleChatFSRequest_ReadPrefersBuffer pins the freshest-text
// rule: an open tab's unsaved buffer beats the on-disk copy, exactly as
// context attachments do — the stale disk copy is the quietly-wrong
// answer.
func TestHandleChatFSRequest_ReadPrefersBuffer(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	wireChat(a)
	p := filepath.Join(root, "main.go")
	if err := os.WriteFile(p, []byte("on disk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.openFile(p)
	a.activeTabPtr().Buffer.Lines = []string{"in buffer", ""}

	e := fsEvent(false, p)
	a.handleChatFSRequest(e)
	r := <-e.reply
	if r.err != nil {
		t.Fatalf("read: %v", r.err)
	}
	content := r.result.(map[string]any)["content"].(string)
	if !strings.Contains(content, "in buffer") || strings.Contains(content, "on disk") {
		t.Errorf("content = %q, want the buffer text", content)
	}
}

// TestHandleChatFSRequest_ReadLineLimit pins the read window params:
// line is the 1-based start, limit caps the row count.
func TestHandleChatFSRequest_ReadLineLimit(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	wireChat(a)
	p := filepath.Join(root, "list.txt")
	if err := os.WriteFile(p, []byte("one\ntwo\nthree\nfour"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := fsEvent(false, p)
	e.line, e.limit = 2, 2
	a.handleChatFSRequest(e)
	r := <-e.reply
	if r.err != nil {
		t.Fatalf("read: %v", r.err)
	}
	if content := r.result.(map[string]any)["content"].(string); content != "two\nthree" {
		t.Errorf("windowed content = %q, want lines 2-3", content)
	}
}

// TestHandleChatFSRequest_WriteReloadsAndNotes pins the write path: the
// bytes land on disk, a CLEAN open tab reloads them immediately (an
// agent edit is an external change, reconciled through the same
// pipeline as any other), and the transcript carries the ✎ receipt.
func TestHandleChatFSRequest_WriteReloadsAndNotes(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	wireChat(a)
	p := filepath.Join(root, "main.go")
	if err := os.WriteFile(p, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.openFile(p)

	e := fsEvent(true, p)
	e.content = "new from agent\n"
	a.handleChatFSRequest(e)
	if r := <-e.reply; r.err != nil {
		t.Fatalf("write: %v", r.err)
	}
	data, err := os.ReadFile(p)
	if err != nil || string(data) != "new from agent\n" {
		t.Fatalf("disk = %q, %v", data, err)
	}
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "new from agent" {
		t.Errorf("clean tab not reloaded: line 0 = %q", got)
	}
	var found bool
	for _, m := range a.chat.msgs {
		if m.role == chatRoleTool && strings.Contains(m.text, "✎ wrote main.go") {
			found = true
		}
	}
	if !found {
		t.Errorf("transcript = %+v, want the ✎ wrote receipt", a.chat.msgs)
	}
}

// TestHandleChatFSRequest_WriteCreatesParents pins that an agent
// writing a brand-new file in a brand-new folder succeeds — "write a
// file" legitimately includes the directory it lives in.
func TestHandleChatFSRequest_WriteCreatesParents(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	wireChat(a)

	e := fsEvent(true, filepath.Join(root, "pkg", "util", "new.go"))
	e.content = "package util\n"
	a.handleChatFSRequest(e)
	if r := <-e.reply; r.err != nil {
		t.Fatalf("write: %v", r.err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "pkg", "util", "new.go")); err != nil || string(data) != "package util\n" {
		t.Errorf("new file = %q, %v", data, err)
	}
}

// TestHandleChatFSRequest_RefusesEscape pins the confinement on the
// full handler path: a path outside the root is answered with an error,
// and nothing is written.
func TestHandleChatFSRequest_RefusesEscape(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	wireChat(a)
	outside := filepath.Join(filepath.Dir(root), "escape.txt")

	e := fsEvent(true, outside)
	e.content = "nope"
	a.handleChatFSRequest(e)
	r := <-e.reply
	if r.err == nil || !strings.Contains(r.err.Error(), "outside the project root") {
		t.Fatalf("err = %v, want the confinement refusal", r.err)
	}
	if _, statErr := os.Stat(outside); !os.IsNotExist(statErr) {
		t.Error("escape write reached the disk")
	}
}

// TestHandleChatFSRequest_StaleSessionRefused pins the session guard: a
// request keyed to a session this panel no longer runs gets an error,
// not file access.
func TestHandleChatFSRequest_StaleSessionRefused(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	e := fsEvent(false, filepath.Join(a.rootDir, "x.txt"))
	e.sessionID = "sess-OLD"
	a.handleChatFSRequest(e)
	if r := <-e.reply; r.err == nil {
		t.Fatal("stale session should be refused")
	}
}

// -----------------------------------------------------------------------------
// Read-only chat (the "chatwrite" switch)
// -----------------------------------------------------------------------------

// TestChatWriteBlocked pins which tool calls read-only mode refuses
// without asking. Mutating kinds are blocked; kinds that only look, and
// kinds the agent never labelled, still reach the user — auto-rejecting
// an unlabelled call would make the mode useless rather than safe.
func TestChatWriteBlocked(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	req := &chatPermRequest{}

	// Writes allowed (the shipped default): nothing is pre-empted.
	for _, kind := range []string{"edit", "delete", "move", "execute", "read", ""} {
		req.kind = kind
		if a.chatWriteBlocked(req) {
			t.Errorf("kind %q blocked while writes are allowed", kind)
		}
	}

	a.chat.writeEnabled = false
	for _, kind := range []string{"edit", "delete", "move", "execute"} {
		req.kind = kind
		if !a.chatWriteBlocked(req) {
			t.Errorf("kind %q should be blocked in read-only chat", kind)
		}
	}
	for _, kind := range []string{"read", "search", "fetch", "think", "other", ""} {
		req.kind = kind
		if a.chatWriteBlocked(req) {
			t.Errorf("kind %q should still ask the user", kind)
		}
	}
}

// TestChatParsePermission_Kind pins that the tool kind survives the wire
// decode and is normalised — it is the only signal read-only mode has
// about whether saying yes would change something.
func TestChatParsePermission_Kind(t *testing.T) {
	_, req := chatParsePermission(json.RawMessage(`{
		"sessionId": "s-1",
		"toolCall": {"title": "Edit main.go", "kind": " Edit "},
		"options": [{"optionId": "no", "kind": "reject_once"}]}`))
	if req.kind != "edit" {
		t.Errorf("kind = %q, want the trimmed lowercase form", req.kind)
	}
}

// TestHandleChatPermRequest_ReadOnlyAutoRejects pins the gate: a
// mutating request in read-only mode is answered with the agent's own
// reject option immediately — never queued, never shown — and the
// transcript says why, because a silent refusal reads as the agent
// stalling.
func TestHandleChatPermRequest_ReadOnlyAutoRejects(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.chat.writeEnabled = false

	e := permEvent("Edit main.go",
		chatPermOption{id: "ok", name: "Allow", kind: "allow_once"},
		chatPermOption{id: "no", name: "Reject", kind: "reject_once"})
	e.req.kind = "edit"
	a.handleChatPermRequest(e)

	if len(a.chat.permQueue) != 0 || a.modal != nil {
		t.Fatalf("blocked request surfaced: queue=%d modal=%v", len(a.chat.permQueue), a.modal)
	}
	select {
	case res := <-e.req.reply:
		if got := permOutcomeJSON(t, res); !strings.Contains(got, `"optionId":"no"`) {
			t.Errorf("reply = %s, want the agent's reject option", got)
		}
	default:
		t.Fatal("blocked request never answered — its goroutine would block to timeout")
	}
	last := a.chat.msgs[len(a.chat.msgs)-1]
	if !strings.Contains(last.text, "read-only") || !strings.Contains(last.text, "Edit main.go") {
		t.Errorf("transcript note = %q, want the titled read-only refusal", last.text)
	}
}

// TestHandleChatPermRequest_ReadOnlyStillPromptsReads pins the other
// half: read-only mode is not "reject everything". A read still gets the
// normal picker.
func TestHandleChatPermRequest_ReadOnlyStillPromptsReads(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.chat.writeEnabled = false

	e := permEvent("Read main.go", chatPermOption{id: "ok", name: "Allow", kind: "allow_once"})
	e.req.kind = "read"
	a.handleChatPermRequest(e)
	if len(a.chat.permQueue) != 1 || a.modal == nil {
		t.Fatalf("read request should still prompt: queue=%d modal=%v", len(a.chat.permQueue), a.modal)
	}
}

// TestHandleChatFSRequest_ReadOnlyRefusesWrite pins the fs half of the
// switch: ced's own write path is refused with a readable reason and
// nothing reaches the disk, while reads keep working — the whole point
// of a read-only chat.
func TestHandleChatFSRequest_ReadOnlyRefusesWrite(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.chat.writeEnabled = false

	target := filepath.Join(a.rootDir, "new.txt")
	e := fsEvent(true, target)
	e.content = "written by the agent"
	a.handleChatFSRequest(e)

	r := <-e.reply
	if r.err == nil || !strings.Contains(r.err.Error(), "read-only") {
		t.Fatalf("err = %v, want the read-only refusal", r.err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Error("write landed on disk in read-only chat mode")
	}

	// Reads are unaffected.
	existing := filepath.Join(a.rootDir, "seen.txt")
	if err := os.WriteFile(existing, []byte("hello"), 0o644); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	re := fsEvent(false, existing)
	a.handleChatFSRequest(re)
	if rr := <-re.reply; rr.err != nil {
		t.Errorf("read refused in read-only mode: %v", rr.err)
	}
}

// TestSetChatWrite pins the toggle: the flag flips, the decision is
// persisted, and the transcript records it — the panel must never change
// what it will allow without saying so.
func TestSetChatWrite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())

	a.setChatWrite(false)
	if a.chat.writeEnabled {
		t.Fatal("setChatWrite(false) left writes enabled")
	}
	if len(a.chat.msgs) == 0 || !strings.Contains(a.chat.msgs[len(a.chat.msgs)-1].text, "Read-only") {
		t.Errorf("transcript = %+v, want the read-only note", a.chat.msgs)
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "ced", "config.json"))
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(data), `"chatwrite": "off"`) {
		t.Errorf("config = %s, want chatwrite off", data)
	}

	// A no-op set must not spam the transcript.
	before := len(a.chat.msgs)
	a.setChatWrite(false)
	if len(a.chat.msgs) != before {
		t.Error("re-setting the same value appended a note")
	}
}

// TestChatWriteToggleLabel pins the flip-in-place menu label.
func TestChatWriteToggleLabel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.chatWriteToggleLabel(); got != "Block agent file changes (read-only chat)" {
		t.Errorf("allowed label = %q", got)
	}
	a.chat.writeEnabled = false
	if got := a.chatWriteToggleLabel(); got != "Allow agent file changes" {
		t.Errorf("blocked label = %q", got)
	}
}
