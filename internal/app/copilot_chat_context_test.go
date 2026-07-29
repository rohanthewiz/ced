// =============================================================================
// File: internal/app/copilot_chat_context_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for chat context attachments (copilot_chat_context.go). No real
// agent is ever spawned — newTestApp marks the chat dead and these tests
// inject the shared fakeCopilotConn via wireChat. Anything that persists
// a preference redirects XDG_CONFIG_HOME at a temp dir first, so no test
// writes the dev machine's config.

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// seedChatFile writes a fixture into the app's project root and opens
// it, returning its absolute path — the setup every attachment test
// starts from.
func seedChatFile(t *testing.T, a *App, name, body string) string {
	t.Helper()
	p := filepath.Join(a.rootDir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
	a.openFile(p)
	return p
}

// promptBlocks decodes the prompt array from the params the fake conn
// recorded for the most recent session/prompt call.
func promptBlocks(t *testing.T, fake *fakeCopilotConn) []map[string]any {
	t.Helper()
	raw := fake.paramsFor("session/prompt")
	if len(raw) == 0 {
		t.Fatal("no session/prompt call recorded")
	}
	var p struct {
		Prompt []map[string]any `json:"prompt"`
	}
	if err := json.Unmarshal(raw[len(raw)-1], &p); err != nil {
		t.Fatalf("decode prompt params: %v", err)
	}
	return p.Prompt
}

// TestChatAutoAttachment_SelectionBeatsFile pins the auto-context rule:
// off yields nothing, on yields the whole active file, and a selection
// narrows it to those lines — a highlighted region is a narrower
// question than the file it sits in.
func TestChatAutoAttachment_SelectionBeatsFile(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedChatFile(t, a, "main.go", "package main\n\nfunc main() {}\n")

	a.chat.autoContext = false
	if _, ok := a.chatAutoAttachment(); ok {
		t.Fatal("auto-context off should attach nothing")
	}

	a.chat.autoContext = true
	at, ok := a.chatAutoAttachment()
	if !ok || at.ranged() || !at.auto {
		t.Fatalf("whole-file auto attachment = %+v (ok=%v)", at, ok)
	}
	if got := a.chatAttachLabel(at); got != "main.go" {
		t.Errorf("label = %q, want main.go", got)
	}

	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 2, Col: 3}
	at, ok = a.chatAutoAttachment()
	if !ok || at.lineFrom != 1 || at.lineTo != 3 {
		t.Fatalf("selection attachment = %+v (ok=%v)", at, ok)
	}
	if got := a.chatAttachLabel(at); got != "main.go:1-3" {
		t.Errorf("ranged label = %q", got)
	}
}

// TestChatSelectionLines_TrimsTrailingColumnZero pins the line-wise
// trim: a selection dragged to the START of a line stops short of it,
// the same convention every editor's line-wise operations use.
func TestChatSelectionLines_TrimsTrailingColumnZero(t *testing.T) {
	tab := &editor.Tab{Buffer: editor.NewBuffer("a\nb\nc\nd\n")}
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 2, Col: 0}
	if from, to := chatSelectionLines(tab); from != 1 || to != 2 {
		t.Errorf("trim: got %d-%d, want 1-2", from, to)
	}
	// A reversed drag normalizes; a single-line selection keeps its line.
	tab.Anchor = editor.Position{Line: 3, Col: 1}
	tab.Cursor = editor.Position{Line: 1, Col: 0}
	if from, to := chatSelectionLines(tab); from != 2 || to != 4 {
		t.Errorf("reversed: got %d-%d, want 2-4", from, to)
	}
}

// TestChatAttachContent_PrefersBuffer pins the rule that makes the
// feature trustworthy: an open file is read from its BUFFER, so unsaved
// edits go out with the prompt. Attaching the stale on-disk copy of the
// file you just changed would make the agent's answer quietly wrong.
func TestChatAttachContent_PrefersBuffer(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	p := seedChatFile(t, a, "notes.txt", "on disk\n")
	a.activeTabPtr().Buffer = editor.NewBuffer("edited but unsaved\n")

	body, truncated, err := a.chatAttachContent(chatAttach{path: p})
	if err != nil || truncated {
		t.Fatalf("content: err=%v truncated=%v", err, truncated)
	}
	if !strings.Contains(body, "edited but unsaved") {
		t.Errorf("buffer text not used: %q", body)
	}

	// A file with no tab falls back to disk.
	q := filepath.Join(a.rootDir, "other.txt")
	if err := os.WriteFile(q, []byte("from disk\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body, _, err = a.chatAttachContent(chatAttach{path: q})
	if err != nil || !strings.Contains(body, "from disk") {
		t.Fatalf("disk fallback: %q err=%v", body, err)
	}
}

// TestChatAttachContent_RangeAndErrors covers the line-slice path plus
// the two refusals a prompt must never carry silently: an out-of-range
// span and a binary file.
func TestChatAttachContent_RangeAndErrors(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	p := seedChatFile(t, a, "lines.txt", "one\ntwo\nthree\nfour\n")

	body, _, err := a.chatAttachContent(chatAttach{path: p, lineFrom: 2, lineTo: 3})
	if err != nil {
		t.Fatalf("range: %v", err)
	}
	if body != "two\nthree" {
		t.Errorf("range body = %q", body)
	}

	// A span past EOF clamps; a span entirely past it is an error.
	if body, _, err = a.chatAttachContent(chatAttach{path: p, lineFrom: 3, lineTo: 99}); err != nil {
		t.Fatalf("clamped range: %v", err)
	} else if !strings.HasPrefix(body, "three") {
		t.Errorf("clamped body = %q", body)
	}
	if _, _, err = a.chatAttachContent(chatAttach{path: p, lineFrom: 90, lineTo: 99}); err == nil {
		t.Error("a span past EOF should error, not send an empty block")
	}

	bin := filepath.Join(a.rootDir, "blob.bin")
	if err := os.WriteFile(bin, []byte{'P', 'K', 0x03, 0x00, 0x04}, 0o644); err != nil {
		t.Fatalf("seed binary: %v", err)
	}
	if _, _, err = a.chatAttachContent(chatAttach{path: bin}); err == nil {
		t.Error("binary content should be refused")
	}
}

// TestChatAttachContent_TruncatesAtLineBoundary pins the per-attachment
// cap: the payload is cut to at most chatAttachMaxBytes, and the cut
// lands on a newline so the model never sees half a token.
func TestChatAttachContent_TruncatesAtLineBoundary(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	line := strings.Repeat("x", 79) + "\n"
	big := strings.Repeat(line, (chatAttachMaxBytes/len(line))+50)
	p := filepath.Join(a.rootDir, "big.txt")
	if err := os.WriteFile(p, []byte(big), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	body, truncated, err := a.chatAttachContent(chatAttach{path: p})
	if err != nil || !truncated {
		t.Fatalf("expected truncation: err=%v truncated=%v", err, truncated)
	}
	if len(body) > chatAttachMaxBytes {
		t.Errorf("body = %d bytes, over the %d cap", len(body), chatAttachMaxBytes)
	}
	if strings.HasSuffix(body, "\n") || !strings.HasSuffix(body, "x") {
		t.Errorf("cut should land on a line boundary, tail = %q", body[len(body)-5:])
	}
}

// TestChatPromptBlocks_EmbeddedResource pins the wire shape when the
// agent accepts embedded context: one resource block per attachment
// (URI carrying the #L range, text carrying the bytes) with the user's
// question LAST, so it's the final thing the model reads.
func TestChatPromptBlocks_EmbeddedResource(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	p := seedChatFile(t, a, "main.go", "package main\n\nfunc main() {}\n")
	a.chat.embeddedContext = true
	a.chat.autoContext = false
	a.chat.attach = []chatAttach{{path: p, lineFrom: 1, lineTo: 2}}

	blocks, notes := a.chatPromptBlocks("what does this do?")
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}
	if blocks[0]["type"] != "resource" {
		t.Fatalf("first block = %+v", blocks[0])
	}
	res, _ := blocks[0]["resource"].(map[string]any)
	uri, _ := res["uri"].(string)
	if !strings.HasSuffix(uri, "/main.go#L1-2") {
		t.Errorf("uri = %q, want the #L range fragment", uri)
	}
	if body, _ := res["text"].(string); body != "package main\n" {
		t.Errorf("resource text = %q", body)
	}
	if blocks[1]["type"] != "text" || blocks[1]["text"] != "what does this do?" {
		t.Errorf("text block should carry the bare question: %+v", blocks[1])
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "main.go:1-2") {
		t.Errorf("notes = %v", notes)
	}
}

// TestChatPromptBlocks_InlineFallback pins the degradation path: an
// agent that doesn't advertise embeddedContext gets ONE text block with
// the same content folded in as a labelled fenced block.
func TestChatPromptBlocks_InlineFallback(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	p := seedChatFile(t, a, "main.go", "package main\n")
	a.chat.embeddedContext = false
	a.chat.autoContext = false
	a.chat.attach = []chatAttach{{path: p}}

	blocks, _ := a.chatPromptBlocks("explain")
	if len(blocks) != 1 || blocks[0]["type"] != "text" {
		t.Fatalf("fallback should be a single text block: %+v", blocks)
	}
	text, _ := blocks[0]["text"].(string)
	for _, want := range []string{"Context:", "main.go", "```go", "package main", "explain"} {
		if !strings.Contains(text, want) {
			t.Errorf("folded prompt missing %q:\n%s", want, text)
		}
	}
	if !strings.HasSuffix(text, "explain") {
		t.Error("the user's question must come last")
	}
}

// TestChatPromptBlocks_UnreadableIsReported pins that a failed
// attachment is announced rather than silently dropped — a prompt the
// user thinks carries a file must not go out pretending it did.
func TestChatPromptBlocks_UnreadableIsReported(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.chat.autoContext = false
	a.chat.attach = []chatAttach{{path: filepath.Join(a.rootDir, "ghost.go")}}

	blocks, notes := a.chatPromptBlocks("hi")
	if len(blocks) != 1 {
		t.Errorf("a failed attachment should add no block: %+v", blocks)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "NOT attached") {
		t.Fatalf("notes = %v", notes)
	}
}

// TestChatSendPrompt_EchoesAndConsumes pins the per-turn contract: the
// dispatch carries the attachment, writes a 📎 note into the transcript
// (so the answer has a visible referent), and clears the pending list so
// the next turn doesn't silently re-send the file.
func TestChatSendPrompt_EchoesAndConsumes(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := wireChat(a)
	p := seedChatFile(t, a, "main.go", "package main\n")
	a.chat.embeddedContext = true
	a.chat.autoContext = false
	a.chat.attach = []chatAttach{{path: p}}

	a.chatSendPrompt("review this")
	waitForCopilot(t, "prompt sent", func() bool { return fake.called("session/prompt") })

	if len(a.chat.attach) != 0 {
		t.Errorf("attachments should be consumed by the turn: %+v", a.chat.attach)
	}
	if len(a.chat.msgs) != 1 || a.chat.msgs[0].role != chatRoleTool ||
		!strings.Contains(a.chat.msgs[0].text, chatAttachGlyph+"main.go") {
		t.Fatalf("transcript note: %+v", a.chat.msgs)
	}
	blocks := promptBlocks(t, fake)
	if len(blocks) != 2 || blocks[0]["type"] != "resource" {
		t.Fatalf("wire blocks = %+v", blocks)
	}

	// A second turn with nothing attached sends the bare question.
	a.chat.turnActive = false
	a.chatSendPrompt("and now?")
	waitForCopilot(t, "second prompt", func() bool { return len(fake.paramsFor("session/prompt")) == 2 })
	if blocks = promptBlocks(t, fake); len(blocks) != 1 {
		t.Errorf("second turn re-sent context: %+v", blocks)
	}
}

// TestChatAddAttachment_DedupesAndOpens pins two behaviours of the add
// path: the same range is never queued twice (it would be sent twice),
// and attaching opens the panel — context you can't see is context you
// can't trust.
func TestChatAddAttachment_DedupesAndOpens(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	p := seedChatFile(t, a, "main.go", "package main\n")
	a.chat.autoContext = false

	a.chatAddAttachment(chatAttach{path: p})
	if len(a.chat.attach) != 1 || !a.chat.open {
		t.Fatalf("first add: attach=%+v open=%v", a.chat.attach, a.chat.open)
	}
	a.chatAddAttachment(chatAttach{path: p})
	if len(a.chat.attach) != 1 || !strings.Contains(a.statusMsg, "already attached") {
		t.Fatalf("duplicate add: attach=%+v flash=%q", a.chat.attach, a.statusMsg)
	}
	// A different range of the same file is a different attachment.
	a.chatAddAttachment(chatAttach{path: p, lineFrom: 1, lineTo: 1})
	if len(a.chat.attach) != 2 {
		t.Errorf("ranged add should not dedupe against the whole file: %+v", a.chat.attach)
	}

	// With Copilot off the panel can't open — the attachment is still
	// queued, but the flash must keep explaining WHY nothing appeared
	// rather than claiming success over it.
	b := newTestApp(t, t.TempDir())
	b.chat.autoContext = false
	b.copilot.enabled = false
	b.chatAddAttachment(chatAttach{path: seedChatFile(t, b, "main.go", "package main\n")})
	if len(b.chat.attach) != 1 || b.chat.open {
		t.Fatalf("disabled add: attach=%+v open=%v", b.chat.attach, b.chat.open)
	}
	if !strings.Contains(b.statusMsg, "Copilot is disabled") {
		t.Errorf("flash = %q, want the reason the panel stayed shut", b.statusMsg)
	}
}

// TestMenuChatAttachCurrent pins the ≡ row: it attaches the selection
// when there is one and the whole file otherwise, matching the label it
// advertises, and refuses an image tab rather than stuffing binary into
// a text block.
func TestMenuChatAttachCurrent(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.chat.autoContext = false
	seedChatFile(t, a, "main.go", "package main\n\nfunc main() {}\n")

	if got := a.chatAttachActionLabel(); got != "Attach current file to chat" {
		t.Errorf("label with no selection = %q", got)
	}
	a.menuChatAttachCurrent()
	if len(a.chat.attach) != 1 || a.chat.attach[0].ranged() {
		t.Fatalf("whole-file attach = %+v", a.chat.attach)
	}

	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 2, Col: 0}
	tab.Cursor = editor.Position{Line: 2, Col: 5}
	if got := a.chatAttachActionLabel(); got != "Attach selection to chat" {
		t.Errorf("label with a selection = %q", got)
	}
	a.menuChatAttachCurrent()
	if len(a.chat.attach) != 2 || a.chat.attach[1].lineFrom != 3 || a.chat.attach[1].lineTo != 3 {
		t.Fatalf("selection attach = %+v", a.chat.attach)
	}

	tab.Mode = "image"
	a.menuChatAttachCurrent()
	if len(a.chat.attach) != 2 || !strings.Contains(a.statusMsg, "text files only") {
		t.Fatalf("image tab: attach=%d flash=%q", len(a.chat.attach), a.statusMsg)
	}
}

// TestMenuChatClearAttachments pins the clear row and its count label.
// Clearing touches only explicit attachments — auto-context is a toggle,
// not an entry in the list.
func TestMenuChatClearAttachments(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	p := seedChatFile(t, a, "main.go", "package main\n")
	a.chat.autoContext = true
	a.chat.attach = []chatAttach{{path: p, lineFrom: 1, lineTo: 1}}

	if got := a.chatClearAttachLabel(); got != "Clear chat attachments (1)" {
		t.Errorf("label = %q", got)
	}
	a.menuChatClearAttachments()
	if len(a.chat.attach) != 0 || a.hasChatAttachments() {
		t.Fatalf("clear left %+v", a.chat.attach)
	}
	if !a.chat.autoContext {
		t.Error("clearing attachments must not flip the auto-context toggle")
	}
}

// TestChatAttachChips_Geometry pins the chip block's effect on layout:
// each chip costs the transcript a row, the block sits directly above
// the composer, and it never eats the transcript entirely.
func TestChatAttachChips_Geometry(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.chat.open = true
	a.chat.autoContext = false
	p := seedChatFile(t, a, "main.go", "package main\n")

	bare := a.chatVisibleRows()
	a.chat.attach = []chatAttach{{path: p}}
	if got := a.chatAttachRows(); got != 1 {
		t.Fatalf("one attachment = %d chip rows", got)
	}
	if got := a.chatVisibleRows(); got != bare-1 {
		t.Errorf("visible rows = %d, want %d", got, bare-1)
	}
	_, py, _, ph := a.chatPanelRect()
	if got, want := a.chatAttachTop(), py+ph-2; got != want {
		t.Errorf("chip top = %d, want %d (directly above the composer)", got, want)
	}
	if iy, _, _ := a.chatInputSpan(); a.chatAttachTop() >= iy {
		t.Error("chips must sit above the input row, not on it")
	}
}

// TestChatAttachRowsView_OverflowSummary pins the cap: past
// chatAttachRowsMax the last row summarises the remainder instead of
// hiding it, and the summary row carries no remove button.
func TestChatAttachRowsView_OverflowSummary(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.chat.open = true
	a.chat.autoContext = false
	for i := 0; i < 6; i++ {
		a.chat.attach = append(a.chat.attach, chatAttach{path: filepath.Join(a.rootDir, "f.go"), lineFrom: i + 1, lineTo: i + 1})
	}
	rows := a.chatAttachRowsView()
	if len(rows) != chatAttachRowsMax {
		t.Fatalf("rows = %d, want %d", len(rows), chatAttachRowsMax)
	}
	last := rows[len(rows)-1]
	if last.idx != -1 || !strings.Contains(last.label, "+4 more") {
		t.Errorf("summary row = %+v", last)
	}
}

// TestChatAttachPress_RemovesAndOwnsItsRows pins the chip mouse
// contract: the ✕ drops that attachment, a press elsewhere on a chip is
// still consumed (so a drag there can't select transcript prose), and
// the auto chip's ✕ flips the toggle instead — the only thing removing
// a synthesized entry can mean.
func TestChatAttachPress_RemovesAndOwnsItsRows(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.chat.open = true
	a.chat.autoContext = false
	p := seedChatFile(t, a, "main.go", "package main\n")
	a.chat.attach = []chatAttach{{path: p}}

	btn := a.chatAttachRemoveRect(0)
	if !a.chatAttachPress(btn.x-1, btn.y) {
		t.Fatal("a press on the chip row should be consumed")
	}
	if len(a.chat.attach) != 1 {
		t.Fatalf("a press off the ✕ must not remove: %+v", a.chat.attach)
	}
	if !a.chatAttachPress(btn.x+1, btn.y) || len(a.chat.attach) != 0 {
		t.Fatalf("✕ press left %+v", a.chat.attach)
	}

	// The auto chip's ✕ is the toggle, persisted like the ≡ row.
	a.chat.autoContext = true
	btn = a.chatAttachRemoveRect(0)
	a.chatAttachPress(btn.x+1, btn.y)
	if a.chat.autoContext {
		t.Fatal("the auto chip's ✕ should turn auto-context off")
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "ced", "config.json"))
	if err != nil || !strings.Contains(string(data), `"chatcontext": "off"`) {
		t.Fatalf("preference not persisted: %v %s", err, data)
	}
}

// TestChatPanelPress_ChipsBeatSelection pins the routing order: a press
// inside the chip block never starts a transcript drag-select.
func TestChatPanelPress_ChipsBeatSelection(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.chat.open = true
	a.chat.autoContext = false
	p := seedChatFile(t, a, "main.go", "package main\n")
	a.chat.attach = []chatAttach{{path: p}}
	a.chatAppendMsg(chatMsg{role: chatRoleAgent, text: "some prose to select"})

	if mode := a.chatPanelPress(2, a.chatAttachTop()); mode != "" {
		t.Errorf("chip press started drag mode %q", mode)
	}
	if a.chat.selActive {
		t.Error("chip press armed a transcript selection")
	}
}

// TestChatContextToggle pins the ≡ toggle: it flips, flashes, and
// persists the preference under the chatcontext key.
func TestChatContextToggle(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	a.chat.autoContext = true

	if got := a.chatContextToggleLabel(); got != "Disable auto-attach current file" {
		t.Errorf("on label = %q", got)
	}
	a.menuToggleChatContext()
	if a.chat.autoContext {
		t.Fatal("toggle did not turn auto-context off")
	}
	if got := a.chatContextToggleLabel(); got != "Enable auto-attach current file" {
		t.Errorf("off label = %q", got)
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "ced", "config.json"))
	if err != nil || !strings.Contains(string(data), `"chatcontext": "off"`) {
		t.Fatalf("preference not persisted: %v %s", err, data)
	}
}

// TestDrawChatAttachments_Smoke renders the open panel with an
// attachment and reads the chip row back off the simulation screen: the
// label lands, the ✕ lands where chatAttachRemoveRect says it does, and
// the composer below it is untouched.
func TestDrawChatAttachments_Smoke(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.menuToggleChat()
	a.chat.autoContext = false
	p := seedChatFile(t, a, "main.go", "package main\n")
	a.chat.attach = []chatAttach{{path: p, lineFrom: 1, lineTo: 1}}
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()

	cells, w, _ := scr.GetContents()
	readRow := func(y int) string {
		var sb strings.Builder
		for x := 0; x < a.chatStripW(); x++ {
			if c := cells[y*w+x]; len(c.Runes) > 0 {
				sb.WriteRune(c.Runes[0])
			}
		}
		return sb.String()
	}
	chip := readRow(a.chatAttachTop())
	if !strings.Contains(chip, "main.go:1-1") || !strings.Contains(chip, "✕") {
		t.Errorf("chip row = %q, want the label and its ✕", chip)
	}
	btn := a.chatAttachRemoveRect(0)
	if c := cells[btn.y*w+btn.x+1]; len(c.Runes) == 0 || c.Runes[0] != '✕' {
		t.Errorf("✕ is not where chatAttachRemoveRect points (x=%d)", btn.x+1)
	}
	if iy, _, _ := a.chatInputSpan(); !strings.Contains(readRow(iy), "❯") {
		t.Errorf("composer row = %q, want the prompt gutter", readRow(iy))
	}
}

// TestChatAttachMimeAndByteLabel smoke-tests the two formatting helpers:
// source files with no registered MIME type fall back to text/plain, and
// sizes read as bytes while small, KB past that.
func TestChatAttachMimeAndByteLabel(t *testing.T) {
	if got := chatAttachMime("/x/main.go"); got != "text/plain" {
		t.Errorf("go mime = %q", got)
	}
	if got := chatAttachMime("/x/README.md"); !strings.HasPrefix(got, "text/") {
		t.Errorf("markdown mime = %q, want a text/* type", got)
	}
	if got := chatAttachMime("/x/photo.png"); got != "text/plain" {
		t.Errorf("non-text mime should degrade to text/plain, got %q", got)
	}
	if got := chatByteLabel(512); got != "512 B" {
		t.Errorf("small size = %q", got)
	}
	if got := chatByteLabel(2048); got != "2.0 KB" {
		t.Errorf("large size = %q", got)
	}
}

// TestChatFitPath pins the path clip direction: the TAIL survives,
// because a path's basename and line span are what identify it.
func TestChatFitPath(t *testing.T) {
	got := chatFitPath("internal/app/copilot_chat.go:10-40", 12)
	if runeLen(got) != 12 || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, ":10-40") {
		t.Errorf("clip = %q, want a 12-cell clip keeping the tail", got)
	}
	if got := chatFitPath("short.go", 40); got != "short.go" {
		t.Errorf("no clip needed: %q", got)
	}
	if got := chatFitPath("short.go", 1); got != "" {
		t.Errorf("unusable width should yield nothing, got %q", got)
	}
}
