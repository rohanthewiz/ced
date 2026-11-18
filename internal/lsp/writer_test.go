// =============================================================================
// File: internal/lsp/writer_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package lsp

import (
	"bufio"
	"io"
	"strings"
	"testing"
	"time"
)

// deafClient wires a Client to a "server" that never reads its stdin —
// the wedge N-123 is about. io.Pipe has no buffer, so the first frame
// already blocks the writer, which is the full-pipe state reached
// within a few edits on a real 64KB pipe. exited receives onExit's
// error.
func deafClient(t *testing.T) (c *Client, exited chan error, srvOut *io.PipeWriter) {
	t.Helper()
	cliR, srvW := io.Pipe()
	_, cliW := io.Pipe() // nobody ever reads the client's writes
	exited = make(chan error, 1)
	c = NewClient(cliR, cliW, nil, func(err error) { exited <- err })
	t.Cleanup(func() { _ = srvW.Close(); _ = cliW.Close() })
	return c, exited, srvW
}

// TestNotify_NeverBlocksOnADeafServer pins N-123's first half: a
// whole-document didChange to a server that is not reading returns at
// once — it used to block the main loop forever.
func TestNotify_NeverBlocksOnADeafServer(t *testing.T) {
	c, _, _ := deafClient(t)
	doc := strings.Repeat("x", 300<<10) // the 300kB DidChange of the report
	start := time.Now()
	for v := 1; v <= 5; v++ {
		if err := c.DidChange("/a.go", v, doc); err != nil {
			t.Fatalf("DidChange %d: %v", v, err)
		}
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("five DidChanges to a deaf server took %s", d)
	}
}

// TestClose_NeverBlocksOnADeafServer pins the other half: Close (quit,
// "Restart language server") returns at once instead of writing `exit`
// into a full pipe.
func TestClose_NeverBlocksOnADeafServer(t *testing.T) {
	c, _, _ := deafClient(t)
	_ = c.DidChange("/a.go", 1, strings.Repeat("x", 1<<20))
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close blocked on a deaf server")
	}
}

// TestWriteStall_FailsTheConnection: a frame stuck past writeStall marks
// the server dead — onExit fires with the reason and a Call fails at
// once rather than after its own timeout.
func TestWriteStall_FailsTheConnection(t *testing.T) {
	old := writeStall
	writeStall = 100 * time.Millisecond
	t.Cleanup(func() { writeStall = old })

	c, exited, _ := deafClient(t)
	_ = c.Notify("test/stuck", nil)
	select {
	case err := <-exited:
		if err == nil || !strings.Contains(err.Error(), "stopped reading") {
			t.Errorf("onExit err = %v, want the stall reason", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a stalled write never failed the connection")
	}
	start := time.Now()
	if err := c.Call("test/after", nil, nil); err == nil {
		t.Error("a Call on a failed connection succeeded")
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Errorf("Call on a failed connection took %s", d)
	}
}

// TestEnqueue_OverflowFailsTheConnection: a server that stopped reading
// long enough to fill the queue is failed by the send that overflows it,
// not allowed to grow memory without bound.
func TestEnqueue_OverflowFailsTheConnection(t *testing.T) {
	c, exited, _ := deafClient(t)
	var err error
	for i := 0; i < maxQueuedFrames+2 && err == nil; i++ {
		err = c.Notify("test/flood", nil)
	}
	if err == nil || !strings.Contains(err.Error(), "not reading") {
		t.Fatalf("flooding a deaf server: err = %v", err)
	}
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("overflow did not end the connection")
	}
}

// TestClose_DrainsQueuedFramesThenExit pins the polite path on a healthy
// server: what was queued before Close still goes out, `exit` last, and
// then stdin closes (the EOF a server exits on).
func TestClose_DrainsQueuedFramesThenExit(t *testing.T) {
	c, srv, cleanup := pipeClient(t, nil, nil)
	defer cleanup()
	got := make(chan []string, 1)
	go func() {
		var methods []string
		for {
			m, err := readMessage(srv.in)
			if err != nil {
				got <- methods
				return
			}
			methods = append(methods, m.Method)
		}
	}()
	_ = c.Notify("one", nil)
	_ = c.Notify("two", nil)
	c.Close()
	select {
	case methods := <-got:
		if strings.Join(methods, ",") != "one,two,exit" {
			t.Errorf("server saw %v, want one, two, exit then EOF", methods)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stdin never closed after Close")
	}
	if err := c.Notify("late", nil); err == nil {
		t.Error("a send after Close was accepted")
	}
}

// TestClose_ReapsAServerThatExitedOnItsOwn pins N-126c's zombie: the read
// loop marks the connection closed when the server exits, and Close
// used to return early on that flag without ever waiting for the
// process.
func TestClose_ReapsAServerThatExitedOnItsOwn(t *testing.T) {
	exited := make(chan error, 1)
	c, err := Start(t.TempDir(), "sh", []string{"-c", "exit 0"}, nil, func(err error) { exited <- err })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("the server's exit was never reported")
	}
	c.Close()
	if !waitReaped(c) {
		t.Fatal("Close never waited for a server that had exited (zombie)")
	}
}

// TestClose_KillsAProcessThatIgnoresExit: a real process that never
// reads stdin nor exits is killed after closeGrace.
func TestClose_KillsAProcessThatIgnoresExit(t *testing.T) {
	old := closeGrace
	closeGrace = 100 * time.Millisecond
	t.Cleanup(func() { closeGrace = old })

	c, err := Start(t.TempDir(), "sleep", []string{"30"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if !waitReaped(c) {
		t.Fatal("a deaf process outlived Close's grace period")
	}
}

// TestReadMessage_RefusesAnOversizedContentLength pins N-126d: the
// header that used to reach make([]byte, n) — and panic the read
// goroutine — is a protocol error now. Negative lengths too.
func TestReadMessage_RefusesAnOversizedContentLength(t *testing.T) {
	for _, hdr := range []string{"Content-Length: 99999999999999\r\n\r\n", "Content-Length: -5\r\n\r\n"} {
		_, err := readMessage(bufio.NewReader(strings.NewReader(hdr)))
		if err == nil {
			t.Errorf("%q: accepted", hdr)
		}
	}
}

// TestReadLineMessage_SkipsNoiseLines pins N-126e: a banner, a log line,
// or JSON that is not a JSON-RPC record no longer ends an ACP / MCP
// connection; the next real record is returned.
func TestReadLineMessage_SkipsNoiseLines(t *testing.T) {
	in := "Starting server v1.2…\n{\"level\":\"info\"}\n\n{\"jsonrpc\":\"2.0\",\"method\":\"ping\"}\n"
	m, err := readLineMessage(bufio.NewReader(strings.NewReader(in)))
	if err != nil || m.Method != "ping" {
		t.Fatalf("got %+v, %v; want the ping record", m, err)
	}
}

// TestReadCappedLine_SkipsAnOverlongLine: a line past the cap is read
// through to its newline and returned empty (skipped), and the next
// line arrives intact — even when the long line overruns bufio's buffer.
func TestReadCappedLine_SkipsAnOverlongLine(t *testing.T) {
	r := bufio.NewReaderSize(strings.NewReader(strings.Repeat("y", 10000)+"\nnext\n"), 16)
	line, err := readCappedLine(r, 100)
	if err != nil || len(line) != 0 {
		t.Fatalf("overlong line: %q, %v; want empty", line, err)
	}
	line, err = readCappedLine(r, 100)
	if err != nil || string(line) != "next\n" {
		t.Fatalf("following line: %q, %v", line, err)
	}
}

// waitReaped waits (up to 2s) for Close's reaper to have waited for the
// process; false on timeout.
func waitReaped(c *Client) bool {
	select {
	case <-c.reaped:
		return c.cmd.ProcessState != nil // safe: Wait returned before reaped closed
	case <-time.After(2 * time.Second):
		return false
	}
}
