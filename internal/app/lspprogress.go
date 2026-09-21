// =============================================================================
// File: internal/app/lspprogress.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// lspprogress.go surfaces what a language server says about ITSELF: its
// work-done progress ("Loading packages 12/40") and the messages it
// addresses to the user.
//
// The problem it solves is a cold start that reads as a broken feature.
// gopls needs several seconds to load a large module, rust-analyzer can
// need a minute, and for that whole window every verb answers "no
// definition found" — which is true, and indistinguishable from the
// verb not working. The server was saying why all along; ced was
// dropping the notification.
//
//	server ──$/progress──► onNotify ──► lspServerNoteEvent ──► lspServer.progress
//	                                                                │
//	                  status bar " · gopls: Loading packages 30%" ◄─┘
//	                  empty-answer flashes gain "— gopls is still loading"
//
// The decisions worth spelling out:
//
//   - IT IS A STATUS-BAR SEGMENT, NOT A FLASH. Progress arrives in bursts
//     of dozens of reports; flashing them would bury every other message
//     for the length of the load. A segment updates in place, trails the
//     bar (so it is the first thing clipped on a narrow terminal, never
//     Ln/Col), and disappears on its own.
//   - IT SHOWS THE ACTIVE FILE'S SERVER ONLY. rust-analyzer indexing is
//     not news to someone reading a Go file.
//   - ONLY ERRORS AND WARNINGS FROM showMessage ARE FLASHED. Servers are
//     chatty at info level ("Finished loading packages"), and that story
//     is already told by the segment going away.
//   - Tokens are tracked as a SET, because a server runs several pieces
//     of work at once and an `end` for one must not blank the others. The
//     segment shows the most recently updated one.

package app

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/lsp"
)

// lspServerNoteEvent carries one $/progress or window/showMessage from a
// server's read loop to the main loop.
type lspServerNoteEvent struct {
	when   time.Time
	server string

	// Exactly one of the two is set.
	progress *lsp.Progress
	msgType  int
	msgText  string
}

// When satisfies the tcell.Event interface.
func (e *lspServerNoteEvent) When() time.Time { return e.when }

// lspProgressItem is one piece of in-flight work as the bar will show it.
type lspProgressItem struct {
	title   string
	message string
	percent int // -1 when the server gave none
}

// lspPostServerNote decodes a server note off-loop and posts it. It
// reports whether the method was one of its two, so the caller's
// dispatch stays a chain of cheap checks. Runs on the client's read
// loop: post, never touch the App.
func lspPostServerNote(scr tcell.Screen, server, method string, params json.RawMessage) bool {
	switch method {
	case "$/progress":
		if p, ok := lsp.ParseProgress(params); ok {
			_ = scr.PostEvent(&lspServerNoteEvent{when: time.Now(), server: server, progress: &p})
		}
		return true
	case "window/showMessage":
		if typ, text, ok := lsp.ParseShowMessage(params); ok {
			_ = scr.PostEvent(&lspServerNoteEvent{when: time.Now(), server: server, msgType: typ, msgText: text})
		}
		return true
	}
	return false
}

// handleLSPServerNote applies one note on the main loop.
func (a *App) handleLSPServerNote(e *lspServerNoteEvent) {
	if a.lsp.dead {
		return
	}
	if e.progress == nil {
		// Info and log are the server narrating; only a problem is worth
		// the flash line.
		if e.msgType == lsp.MessageError || e.msgType == lsp.MessageWarning {
			a.flash(e.server + ": " + firstLine(e.msgText))
		}
		return
	}
	sv := a.lsp.server(e.server)
	p := e.progress
	switch p.Kind {
	case lsp.ProgressEnd:
		delete(sv.progress, p.Token)
		// A server that was loading gave its hint requests an empty
		// answer that was not its real one; ask again now it is done.
		a.inlayReask()
		if sv.progressLast == p.Token {
			sv.progressLast = ""
		}
	default:
		if sv.progress == nil {
			sv.progress = map[string]*lspProgressItem{}
		}
		item := sv.progress[p.Token]
		if item == nil {
			item = &lspProgressItem{percent: -1}
			sv.progress[p.Token] = item
		}
		// A report carries only what CHANGED: the title comes once, on
		// begin, and a report with no message keeps the previous one.
		if p.Title != "" {
			item.title = p.Title
		}
		if p.Message != "" || p.Kind == lsp.ProgressBegin {
			item.message = p.Message
		}
		if p.Percent >= 0 {
			item.percent = p.Percent
		}
		sv.progressLast = p.Token
	}
}

// lspBusy reports whether path's server has work in flight, and its id.
func (a *App) lspBusy(path string) (id string, busy bool) {
	def := lspServerFor(path)
	if def == nil || a.lsp.dead {
		return "", false
	}
	sv := a.lsp.servers[def.id]
	if sv == nil {
		return def.id, false
	}
	// A handshake in flight is the earliest form of "still loading".
	return def.id, sv.starting || len(sv.progress) > 0
}

// lspLoadingNote is appended to an EMPTY answer's flash: while the server
// is still loading, "nothing found" usually means "not loaded yet", and
// saying so is the difference between a user retrying in ten seconds and
// concluding the verb is broken.
func (a *App) lspLoadingNote(path string) string {
	if id, busy := a.lspBusy(path); busy {
		return " — " + id + " is still loading"
	}
	return ""
}

// lspProgressSuffix renders the status-bar segment for the ACTIVE file's
// server, or "" when it is idle.
func (a *App) lspProgressSuffix() string {
	t := a.activeTabPtr()
	if t == nil || a.lsp.dead {
		return ""
	}
	def := lspServerFor(t.Path)
	if def == nil {
		return ""
	}
	sv := a.lsp.servers[def.id]
	if sv == nil {
		return ""
	}
	if sv.starting {
		return " · " + def.id + ": starting…"
	}
	item := sv.progress[sv.progressLast]
	if item == nil {
		// The most recent piece of work ended; show any that remains.
		for _, it := range sv.progress {
			item = it
			break
		}
	}
	if item == nil {
		return ""
	}
	text := item.title
	if item.message != "" {
		if text != "" {
			text += " "
		}
		text += item.message
	}
	if item.percent >= 0 {
		text += fmt.Sprintf(" %d%%", item.percent)
	}
	const maxProgressText = 48
	if r := []rune(text); len(r) > maxProgressText {
		text = string(r[:maxProgressText-1]) + "…"
	}
	return " · " + def.id + ": " + text
}
