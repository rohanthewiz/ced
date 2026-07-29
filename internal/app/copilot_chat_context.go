// =============================================================================
// File: internal/app/copilot_chat_context.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// copilot_chat_context.go gives the ACP chat panel something to talk
// about: the file you're looking at, the lines you highlighted, and any
// other project file you name.
//
// Why it has to be PUSHED, not fetched: the phase-3 handshake declares
// fs.readTextFile FALSE and auto-declines every session/request_permission
// (see copilot_chat.go's scope guard), so the agent genuinely cannot read
// a path we merely point at. ACP's prompt is a ContentBlock ARRAY, and
// the block type that carries bytes is the embedded `resource` — so r-ed
// ships the text itself and the scope guard stays exactly as tight as it
// was. A `resource_link` block would be a lie here; don't add one.
//
// House rules for this layer:
//
//   - Attachments are PER TURN. chatSendPrompt consumes them, echoes
//     what it sent into the transcript, and clears the list. An ACP
//     session keeps its own history server-side, so a sticky attachment
//     would re-send the whole file on every prompt for the rest of the
//     session — the user pays for that twice over (tokens and premium
//     multiplier) with nothing on screen to explain why.
//   - Auto-context is the exception, and it is a TOGGLE, not a sticky
//     attachment: while on, every turn carries the active tab, or just
//     its selected lines when there is a selection (selection beats
//     file — a highlighted region is a narrower question). It is
//     synthesized at send time, so it always reflects where the caret
//     actually is.
//   - Content comes from the open Tab's BUFFER when the file is open,
//     falling back to disk. You attach what you are looking at,
//     including unsaved edits — attaching the stale on-disk copy of the
//     file you just changed is the one failure mode that would make the
//     agent's answers quietly wrong.
//   - Attachments are VISIBLE: chip rows sit between the transcript and
//     the composer, and every dispatch writes a 📎 note into the
//     transcript. Context you can't see is context you forget you're
//     paying for.
//   - embeddedContext gates the wire format. The agent advertises it in
//     initialize's agentCapabilities.promptCapabilities; when it is
//     false we fold the same text into the prompt as a fenced block,
//     which every agent understands. Silent degradation, same contract
//     as the rest of the integration.

package app

import (
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/r-ed/internal/editor"
	"github.com/rohanthewiz/r-ed/internal/lsp"
	"github.com/rohanthewiz/r-ed/internal/userconfig"
)

const (
	// chatAttachMaxBytes caps ONE attachment's payload. A prompt turn is
	// billed by what it carries, and a 20k-line generated file attached
	// by a stray click would spend the user's premium requests without
	// ever being read. The cut is announced (in the chip and in the
	// transcript note), never silent.
	chatAttachMaxBytes = 64 << 10

	// chatAttachRowsMax bounds the chip block so a long attachment list
	// can't crowd the transcript out of a narrow strip; the last row
	// becomes a "+N more" summary instead.
	chatAttachRowsMax = 3

	// chatAttachPickLimit bounds the fuzzy picker's inventory. The
	// palette scores every item on every keystroke, and a monorepo index
	// can run to six figures. Truncation is stated in the picker title.
	chatAttachPickLimit = 2000

	// chatAttachRemoveLabel is the chip's remove affordance — same
	// glyph and spacing as the panel's ✕, so it reads as the same verb.
	chatAttachRemoveLabel = " ✕ "

	// chatAttachGlyph marks an attachment in chips and transcript notes.
	// Deliberately NOT a paperclip emoji: every marker in this editor
	// (⏹ ✕ ❯ ⧉ ⚙ ⊘) is single-width, and an emoji would take two cells
	// while runeLen counts one — the label would overrun the ✕ button.
	chatAttachGlyph = "▤ "
)

// chatAttach is one piece of context queued for the next prompt turn.
// The content is deliberately NOT stored: it is resolved at send time
// from the live buffer (or disk), so a file edited between attaching and
// sending goes out in its current state.
type chatAttach struct {
	// path is absolute — the same contract the LSP layer keeps, and what
	// tab lookup and file:// URI construction both need.
	path string

	// lineFrom / lineTo are 1-based inclusive; both zero means the whole
	// file. Selections snap to whole lines on purpose: half a line of
	// code is not something a model can reason about, and the column
	// offsets would be noise in the URI fragment.
	lineFrom, lineTo int

	// auto marks the synthesized active-tab attachment. It never lives
	// in chat.attach — chatPendingAttachments derives it per call — but
	// the flag rides along so the chip can label it and its ✕ can mean
	// "turn the toggle off" rather than "drop this entry".
	auto bool
}

// ranged reports whether the attachment covers a line range rather than
// the whole file.
func (at chatAttach) ranged() bool { return at.lineFrom > 0 && at.lineTo >= at.lineFrom }

// same reports whether two attachments cover identical ground — the
// dedupe key for chatAddAttachment.
func (at chatAttach) same(other chatAttach) bool {
	return at.path == other.path && at.lineFrom == other.lineFrom && at.lineTo == other.lineTo
}

// -----------------------------------------------------------------------------
// The pending set
// -----------------------------------------------------------------------------

// chatPendingAttachments is what the NEXT turn will carry: the
// synthesized auto-context entry first (it's the implicit subject of the
// question), then the explicitly attached files in the order they were
// added. Derived on every call so the auto entry always reflects the
// current tab and selection.
func (a *App) chatPendingAttachments() []chatAttach {
	var out []chatAttach
	if at, ok := a.chatAutoAttachment(); ok {
		out = append(out, at)
	}
	return append(out, a.chat.attach...)
}

// chatAutoAttachment builds the active tab's attachment for the current
// moment: the selected lines when there is a selection, otherwise the
// whole file. Reports false when auto-context is off, there is no saved
// file in the active tab, or the tab isn't text (an image can't ride in
// a text resource block).
func (a *App) chatAutoAttachment() (chatAttach, bool) {
	if !a.chat.autoContext {
		return chatAttach{}, false
	}
	t := a.activeTabPtr()
	if t == nil || t.Path == "" || t.IsImage() {
		return chatAttach{}, false
	}
	at := chatAttach{path: absolutePathFor(t.Path), auto: true}
	if t.HasSelection() {
		at.lineFrom, at.lineTo = chatSelectionLines(t)
	}
	return at, true
}

// chatSelectionLines returns the 1-based inclusive line span a tab's
// selection covers. A selection ending in column 0 stops short of the
// line it touches — the user dragged to the start of it, not into it —
// which is the same trim every editor applies to line-wise operations.
func chatSelectionLines(t *editor.Tab) (from, to int) {
	start, end := t.Anchor, t.Cursor
	if end.Line < start.Line || (end.Line == start.Line && end.Col < start.Col) {
		start, end = end, start
	}
	last := end.Line
	if end.Col == 0 && end.Line > start.Line {
		last--
	}
	return start.Line + 1, last + 1
}

// chatAddAttachment queues one attachment, opening the panel so the user
// can see what they just did. Duplicates are refused rather than stacked
// — attaching the same file twice sends it twice.
func (a *App) chatAddAttachment(at chatAttach) {
	for _, have := range a.chatPendingAttachments() {
		if have.same(at) {
			a.flash(a.chatAttachLabel(at) + " is already attached")
			return
		}
	}
	a.chat.attach = append(a.chat.attach, at)
	a.chatOpenPanel()
	if !a.chat.open {
		// The panel refused to open and already flashed why (disabled, no
		// binary). Leave that message standing — it's the one the user
		// needs; the attachment is queued for whenever chat comes back.
		return
	}
	a.flash("Attached " + a.chatAttachLabel(at) + " to the next chat message")
}

// chatRemoveAttachment drops the entry at index i of the PENDING list
// (what the chips show). Removing the synthesized auto entry can only
// mean one thing — the user doesn't want the active file riding along —
// so it flips the toggle, persisted like the ≡ row does it.
func (a *App) chatRemoveAttachment(i int) {
	pending := a.chatPendingAttachments()
	if i < 0 || i >= len(pending) {
		return
	}
	if pending[i].auto {
		a.setChatContext(false)
		return
	}
	idx := i
	if len(pending) > len(a.chat.attach) {
		idx-- // the auto entry occupies slot 0 but isn't in chat.attach
	}
	if idx < 0 || idx >= len(a.chat.attach) {
		return
	}
	a.chat.attach = append(a.chat.attach[:idx], a.chat.attach[idx+1:]...)
}

// chatAttachLabel names an attachment for chips, flashes and transcript
// notes: project-relative path plus the line span when it has one.
func (a *App) chatAttachLabel(at chatAttach) string {
	label := a.relativePathFor(at.path)
	if at.ranged() {
		label += fmt.Sprintf(":%d-%d", at.lineFrom, at.lineTo)
	}
	return label
}

// -----------------------------------------------------------------------------
// Content resolution
// -----------------------------------------------------------------------------

// chatAttachTab finds the open tab for an absolute path, nil when the
// file isn't open. The buffer is preferred over disk so unsaved edits go
// out with the prompt.
func (a *App) chatAttachTab(path string) *editor.Tab {
	for _, t := range a.tabs {
		if t.Path == path && !t.IsImage() && t.Buffer != nil {
			return t
		}
	}
	return nil
}

// chatAttachContent resolves an attachment to the text that will be
// sent, reporting whether the cap trimmed it. Errors (unreadable,
// binary) are the caller's to surface — a failed attachment must be
// visible, not silently dropped from a prompt the user thinks carries it.
func (a *App) chatAttachContent(at chatAttach) (body string, truncated bool, err error) {
	var lines []string
	if t := a.chatAttachTab(at.path); t != nil {
		lines = t.Buffer.Lines
	} else {
		data, readErr := os.ReadFile(at.path)
		if readErr != nil {
			return "", false, readErr
		}
		if isBinaryContent(data) {
			return "", false, fmt.Errorf("binary file")
		}
		lines = strings.Split(string(data), "\n")
	}
	if at.ranged() {
		from, to := at.lineFrom-1, at.lineTo
		if from < 0 {
			from = 0
		}
		if to > len(lines) {
			to = len(lines)
		}
		if from >= to {
			return "", false, fmt.Errorf("lines %d-%d are past the end of the file", at.lineFrom, at.lineTo)
		}
		lines = lines[from:to]
	}
	body = strings.Join(lines, "\n")
	if len(body) > chatAttachMaxBytes {
		return truncateAtLine(body, chatAttachMaxBytes), true, nil
	}
	return body, false, nil
}

// isBinaryContent is the "don't paste a JPEG into a chat prompt" guard:
// a NUL byte in the leading window is the same cheap heuristic git uses.
func isBinaryContent(data []byte) bool {
	window := data
	if len(window) > 8000 {
		window = window[:8000]
	}
	for _, b := range window {
		if b == 0 {
			return true
		}
	}
	return false
}

// truncateAtLine cuts s to at most limit bytes, backing up to the last
// newline so the payload never ends mid-token. A single line longer than
// the limit is cut where it falls — nothing better is available.
func truncateAtLine(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := s[:limit]
	if nl := strings.LastIndexByte(cut, '\n'); nl > 0 {
		cut = cut[:nl]
	}
	return cut
}

// -----------------------------------------------------------------------------
// Wire format
// -----------------------------------------------------------------------------

// chatPromptBlocks builds one turn's ACP content blocks plus the
// transcript notes describing what rode along. The text block always
// comes last so the user's actual question is the final thing the model
// reads.
//
// Two shapes, chosen by the agent's advertised promptCapabilities:
// embedded `resource` blocks when it accepts them (the file keeps its
// own identity and URI), or the same text folded into the prompt as a
// labelled fenced block when it doesn't. Never a resource_link — see the
// file header for why the agent can't follow one.
func (a *App) chatPromptBlocks(text string) (blocks []map[string]any, notes []string) {
	pending := a.chatPendingAttachments()
	if len(pending) == 0 {
		return []map[string]any{{"type": "text", "text": text}}, nil
	}
	var inline strings.Builder
	for _, at := range pending {
		label := a.chatAttachLabel(at)
		body, truncated, err := a.chatAttachContent(at)
		if err != nil {
			notes = append(notes, chatAttachGlyph+label+" NOT attached: "+err.Error())
			continue
		}
		note := chatAttachGlyph + label + " · " + chatByteLabel(len(body))
		if truncated {
			note += " (truncated at " + chatByteLabel(chatAttachMaxBytes) + ")"
		}
		notes = append(notes, note)

		if a.chat.embeddedContext {
			blocks = append(blocks, map[string]any{
				"type": "resource",
				"resource": map[string]any{
					"uri":      chatAttachURI(at),
					"mimeType": chatAttachMime(at.path),
					"text":     body,
				},
			})
			continue
		}
		inline.WriteString(label)
		inline.WriteString(":\n```")
		inline.WriteString(copilotLanguageID(at.path))
		inline.WriteString("\n")
		inline.WriteString(body)
		inline.WriteString("\n```\n\n")
	}
	prompt := text
	if inline.Len() > 0 {
		prompt = "Context:\n\n" + inline.String() + text
	}
	return append(blocks, map[string]any{"type": "text", "text": prompt}), notes
}

// chatAttachURI is the attachment's file:// URI, carrying the line span
// as a #L<from>-<to> fragment — the convention every code host and
// editor uses, so an agent that surfaces the URI shows something the
// user recognises.
func chatAttachURI(at chatAttach) string {
	uri := lsp.PathToURI(at.path)
	if at.ranged() {
		uri += fmt.Sprintf("#L%d-%d", at.lineFrom, at.lineTo)
	}
	return uri
}

// chatAttachMime types a resource block by extension, defaulting to
// text/plain. Source files mostly have no registered MIME type and
// text/plain is the honest answer — the URI's extension is what actually
// tells the model what it's reading.
func chatAttachMime(path string) string {
	if mt := mime.TypeByExtension(filepath.Ext(path)); mt != "" {
		if semi := strings.IndexByte(mt, ';'); semi > 0 {
			mt = strings.TrimSpace(mt[:semi])
		}
		if strings.HasPrefix(mt, "text/") {
			return mt
		}
	}
	return "text/plain"
}

// chatByteLabel renders a payload size the way the chips and notes want
// it: whole bytes while small, one decimal of KB past that.
func chatByteLabel(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024)
}

// -----------------------------------------------------------------------------
// Menu actions
// -----------------------------------------------------------------------------

// chatContextToggleLabel names the ≡ Copilot row for its direction —
// the Enable/Disable phrasing every other toggle row uses.
func (a *App) chatContextToggleLabel() string {
	if a.chat.autoContext {
		return "Disable auto-attach current file"
	}
	return "Enable auto-attach current file"
}

// menuToggleChatContext flips the auto-context toggle from the ≡ menu.
func (a *App) menuToggleChatContext() {
	a.closeMenu()
	a.setChatContext(!a.chat.autoContext)
}

// setChatContext applies and persists the auto-context preference. Both
// entry points (the ≡ row and the auto chip's ✕) route through here, so
// the setting can't end up meaning different things depending on which
// surface changed it.
func (a *App) setChatContext(on bool) {
	a.chat.autoContext = on
	if on {
		a.flash("Chat auto-attach on — the active file rides with every message")
	} else {
		a.flash("Chat auto-attach off")
	}
	if err := userconfig.SaveChatContext(userconfig.DefaultPath(), on); err != nil {
		a.flash("chatcontext: " + err.Error())
	}
}

// chatAttachActionLabel names the attach row for what it would actually
// send — the selection-beats-file rule, stated in the label so the row
// never surprises.
func (a *App) chatAttachActionLabel() string {
	if a.hasSelection() {
		return "Attach selection to chat"
	}
	return "Attach current file to chat"
}

// menuChatAttachCurrent attaches the active tab: its selected lines when
// there is a selection, the whole file otherwise.
func (a *App) menuChatAttachCurrent() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil || t.Path == "" {
		a.flash("No saved file to attach")
		return
	}
	if t.IsImage() {
		a.flash("Copilot chat takes text files only")
		return
	}
	at := chatAttach{path: absolutePathFor(t.Path)}
	if t.HasSelection() {
		at.lineFrom, at.lineTo = chatSelectionLines(t)
	}
	a.chatAddAttachment(at)
}

// menuChatAttachFile picks any project file as context. Reuses the
// palette as a fuzzy picker (the house rule for every choose-one-from-a-
// list UI) over the finder's existing index.
func (a *App) menuChatAttachFile() {
	a.closeMenu()
	if a.finder == nil {
		a.flash("File index unavailable")
		return
	}
	paths := a.finder.Paths()
	if len(paths) == 0 {
		a.flash("File index is still building — try again in a moment")
		return
	}
	title := "Attach file to chat"
	if len(paths) > chatAttachPickLimit {
		title = fmt.Sprintf("Attach file to chat (first %d of %d)", chatAttachPickLimit, len(paths))
		paths = paths[:chatAttachPickLimit]
	}
	items := make([]paletteItem, 0, len(paths))
	for _, rel := range paths {
		rel := rel // capture per-iteration for the closure
		items = append(items, paletteItem{
			label: rel,
			run: func(app *App) {
				app.chatAddAttachment(chatAttach{path: absolutePathFor(filepath.Join(app.rootDir, rel))})
			},
		})
	}
	a.openPicker(title, items)
}

// hasChatAttachments gates the clear row — the auto entry isn't in the
// list, so this is only about explicitly attached files.
func (a *App) hasChatAttachments() bool { return len(a.chat.attach) > 0 }

// chatClearAttachLabel counts what the clear row would drop.
func (a *App) chatClearAttachLabel() string {
	if n := len(a.chat.attach); n > 0 {
		return fmt.Sprintf("Clear chat attachments (%d)", n)
	}
	return "Clear chat attachments"
}

// menuChatClearAttachments drops every explicitly attached file.
func (a *App) menuChatClearAttachments() {
	a.closeMenu()
	a.chat.attach = nil
	a.flash("Chat attachments cleared")
}

// -----------------------------------------------------------------------------
// Chip rows — geometry, hit-testing, drawing
// -----------------------------------------------------------------------------

// chatAttachRow is one drawn chip row. idx indexes the pending list, or
// is -1 for the trailing "+N more" summary. The single source draw and
// hit-testing share, so a click can't land on a different chip than the
// one under the pointer.
type chatAttachRow struct {
	label string
	idx   int
}

// chatAttachRowsView derives the chip block for the current pending set,
// clamped to what the panel can spare. Beyond the cap the last row
// summarises the remainder rather than hiding it.
func (a *App) chatAttachRowsView() []chatAttachRow {
	// Chips are a panel affordance: with the panel closed there is no
	// geometry to reason about, and the row count must not perturb the
	// transcript's scroll math (chatAppendMsg runs either way).
	if !a.chat.open {
		return nil
	}
	pending := a.chatPendingAttachments()
	if len(pending) == 0 {
		return nil
	}
	n := len(pending)
	if n > chatAttachRowsMax {
		n = chatAttachRowsMax
	}
	// The transcript keeps at least one row: header + 1 row + input is
	// the floor below which the panel stops being a chat panel.
	_, _, _, ph := a.chatPanelRect()
	if room := ph - 3; n > room {
		n = room
	}
	if n <= 0 {
		return nil
	}
	out := make([]chatAttachRow, 0, n)
	for i := 0; i < n; i++ {
		if i == n-1 && len(pending) > n {
			out = append(out, chatAttachRow{label: fmt.Sprintf("+%d more attached", len(pending)-i), idx: -1})
			break
		}
		label := a.chatAttachLabel(pending[i])
		if pending[i].auto {
			label += " (auto)"
		}
		out = append(out, chatAttachRow{label: chatAttachGlyph + label, idx: i})
	}
	return out
}

// chatAttachRows is how many rows the chip block occupies — the number
// every other geometry helper subtracts.
func (a *App) chatAttachRows() int { return len(a.chatAttachRowsView()) }

// chatAttachTop is the first chip row's y: the block sits directly above
// the composer, where an attachment reads as part of the message you are
// about to send rather than part of the history.
func (a *App) chatAttachTop() int {
	_, py, _, ph := a.chatPanelRect()
	return py + ph - 1 - a.chatAttachRows()
}

// chatAttachRemoveRect is a chip's ✕ button, flush right on its row —
// the one geometry source draw and hit-testing share (the btnRect rule).
func (a *App) chatAttachRemoveRect(row int) btnRect {
	px, _, pw, _ := a.chatPanelRect()
	return btnRect{x: px + pw - 4, y: a.chatAttachTop() + row, w: 3}
}

// chatAttachPress routes a press inside the chip block, reporting
// whether the block owned it. The whole block swallows presses even when
// they miss a ✕ — a chip is not transcript text, so a drag started there
// must not select prose.
func (a *App) chatAttachPress(x, y int) bool {
	rows := a.chatAttachRowsView()
	if len(rows) == 0 {
		return false
	}
	i := y - a.chatAttachTop()
	if i < 0 || i >= len(rows) {
		return false
	}
	if rows[i].idx >= 0 && a.chatAttachRemoveRect(i).contains(x, y) {
		a.chatRemoveAttachment(rows[i].idx)
	}
	return true
}

// drawChatAttachments paints the chip block on the sidebar background,
// which sets it off from the transcript the same way the header rule is
// set off. Labels clip from the LEFT: the basename and line span are the
// identifying part of a path, so those are what survive a narrow strip.
func (a *App) drawChatAttachments() {
	rows := a.chatAttachRowsView()
	if len(rows) == 0 {
		return
	}
	px, _, pw, _ := a.chatPanelRect()
	th := a.theme
	st := tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Muted)
	btnSt := tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Subtle)
	top := a.chatAttachTop()
	for i, row := range rows {
		ry := top + i
		for cx := px; cx < px+pw; cx++ {
			a.screen.SetContent(cx, ry, ' ', nil, st)
		}
		btn := a.chatAttachRemoveRect(i)
		drawAt(a.screen, px+1, ry, chatFitPath(row.label, btn.x-(px+1)), st)
		if row.idx >= 0 {
			drawAt(a.screen, btn.x, btn.y, chatAttachRemoveLabel, btnSt)
		}
	}
}

// chatFitPath clips a path label to width from the LEFT, marking the cut
// with a leading ellipsis — the opposite end from chatFitHeader, because
// a path's tail is the part that identifies it.
func chatFitPath(s string, width int) string {
	if width >= runeLen(s) {
		return s
	}
	if width < 2 {
		return ""
	}
	r := []rune(s)
	return "…" + string(r[len(r)-(width-1):])
}
