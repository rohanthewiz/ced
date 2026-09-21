// =============================================================================
// File: internal/app/lspprogress_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/lsp"
)

// note builds a progress event for the Go server.
func progressNote(token, kind, title, msg string, pct int) *lspServerNoteEvent {
	return &lspServerNoteEvent{server: lspGoServerID, progress: &lsp.Progress{
		Token: token, Kind: kind, Title: title, Message: msg, Percent: pct,
	}}
}

// TestLSPProgress_SegmentLifecycle pins the segment: begin shows the
// title, a report updates message and percent while KEEPING the title it
// never resent, and end takes the segment away.
func TestLSPProgress_SegmentLifecycle(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	a.openFile(goPath)
	if s := a.lspProgressSuffix(); s != "" {
		t.Fatalf("idle server shows %q", s)
	}

	a.handleLSPServerNote(progressNote("1", lsp.ProgressBegin, "Loading packages", "", -1))
	if s := a.lspProgressSuffix(); s != " · gopls: Loading packages" {
		t.Errorf("after begin = %q", s)
	}
	a.handleLSPServerNote(progressNote("1", lsp.ProgressReport, "", "12/40", 30))
	if s := a.lspProgressSuffix(); s != " · gopls: Loading packages 12/40 30%" {
		t.Errorf("after report = %q", s)
	}
	a.handleLSPServerNote(progressNote("1", lsp.ProgressEnd, "", "", -1))
	if s := a.lspProgressSuffix(); s != "" {
		t.Errorf("after end = %q, want the segment gone", s)
	}
}

// TestLSPProgress_TokensAreIndependent pins the set: ending one piece of
// work must not blank another still running.
func TestLSPProgress_TokensAreIndependent(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	a.openFile(goPath)
	a.handleLSPServerNote(progressNote("1", lsp.ProgressBegin, "Loading", "", -1))
	a.handleLSPServerNote(progressNote("2", lsp.ProgressBegin, "Checking", "", -1))
	a.handleLSPServerNote(progressNote("2", lsp.ProgressEnd, "", "", -1))
	if s := a.lspProgressSuffix(); !strings.Contains(s, "Loading") {
		t.Errorf("suffix = %q, want the surviving work", s)
	}
}

// TestLSPProgress_OnlyTheActiveFilesServer pins the scope: another
// language's indexing is not news to someone reading a Go file.
func TestLSPProgress_OnlyTheActiveFilesServer(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	a.openFile(goPath)
	a.lspInstall("rust-analyzer", &fakeLSPConn{})
	a.handleLSPServerNote(&lspServerNoteEvent{server: "rust-analyzer",
		progress: &lsp.Progress{Token: "r", Kind: lsp.ProgressBegin, Title: "Indexing", Percent: -1}})
	if s := a.lspProgressSuffix(); s != "" {
		t.Errorf("a Go tab shows rust-analyzer's progress: %q", s)
	}
}

// TestLSPLoadingNote pins the hint on empty answers: present while the
// server is busy, absent once it is idle — so "No definition found"
// stops reading as "this key is broken" during a cold start.
func TestLSPLoadingNote(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	if got := a.lspLoadingNote(goPath); got != "" {
		t.Errorf("idle note = %q", got)
	}
	a.handleLSPServerNote(progressNote("1", lsp.ProgressBegin, "Loading", "", -1))
	if got := a.lspLoadingNote(goPath); !strings.Contains(got, "gopls is still loading") {
		t.Errorf("busy note = %q", got)
	}
	a.handleLSPDefinition(&lspDefinitionEvent{fromPath: goPath})
	if !strings.Contains(a.statusMsg, "still loading") {
		t.Errorf("empty definition flash = %q", a.statusMsg)
	}
}

// TestLSPShowMessage_OnlyProblemsFlash pins the filter: a server's info
// chatter stays off the flash line, an error reaches it, first line only.
func TestLSPShowMessage_OnlyProblemsFlash(t *testing.T) {
	a, _, _ := newLSPTestApp(t)
	a.handleLSPServerNote(&lspServerNoteEvent{server: lspGoServerID, msgType: lsp.MessageInfo, msgText: "Finished loading"})
	if a.statusMsg != "" {
		t.Errorf("info flashed: %q", a.statusMsg)
	}
	a.handleLSPServerNote(&lspServerNoteEvent{server: lspGoServerID, msgType: lsp.MessageError, msgText: "go.mod not found\ndetails"})
	if a.statusMsg != "gopls: go.mod not found" {
		t.Errorf("error flash = %q", a.statusMsg)
	}
}

// TestHandleLSPExit_ClearsProgress pins that a crashed server stops
// claiming to be busy — nothing would ever send its `end`.
func TestHandleLSPExit_ClearsProgress(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	a.handleLSPServerNote(progressNote("1", lsp.ProgressBegin, "Loading", "", -1))
	a.handleLSPExit(&lspExitEvent{server: lspGoServerID})
	if _, busy := a.lspBusy(goPath); busy {
		t.Error("a dead server must not read as busy")
	}
}
