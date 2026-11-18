// =============================================================================
// File: internal/lsp/writer.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// writer.go takes the server's stdin pipe off the caller's goroutine
// (N-123). A send used to be a blocking pipe write under writeMu, made
// from the MAIN LOOP (didOpen / didChange carry the whole document). A
// server that is alive but not reading — deadlocked, swapping, SIGSTOPped
// — fills the pipe within a few edits, and the next write blocked the
// editor for good; Close wrote `exit` through the same path, so quit and
// "Restart language server" hung too.
//
// Now every frame goes through a bounded queue to ONE writer goroutine:
//
//	Call / Notify / reply ──enqueue (never blocks)──► outbox ──► writeLoop ──► stdin
//	                              │                                 │
//	                     queue full / over budget          a frame stuck > writeStall
//	                              └────────────► fail(err) ◄────────┘
//	                                     kill the process, close both pipes:
//	                                     the read loop ends, onExit fires,
//	                                     the app marks the server dead
//
// FIFO order is the single writer's; JSON-RPC needs no more than that
// (responses correlate by id). Close never writes: it queues `exit`,
// tells the writer to drain and hang up, and a reaper kills the process
// if it has not exited within closeGrace.

package lsp

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/rohanthewiz/ced/internal/crash"
)

// writeStall is how long ONE frame's write may block before the server
// is presumed deaf. A healthy server drains its stdin on a goroutine of
// its own, so even a multi-megabyte didOpen goes through in
// milliseconds; fifteen seconds is far past a busy one and still ends a
// wedge long before the user gives up on the editor (which, with the
// writer off the loop, never freezes now). closeGrace is how long Close
// waits for the process to exit on its own (after `exit` and stdin's
// EOF) before killing it. Vars only so tests can shorten them.
var (
	writeStall = 15 * time.Second
	closeGrace = 2 * time.Second
)

const (

	// maxQueuedFrames / maxQueuedBytes bound the outbox. Edits flush at
	// most every 300ms per file, so a queue this deep is a server that
	// stopped reading long ago — the stall timer usually fires first.
	// The byte budget is what binds on big files: a few hundred
	// whole-document didChanges of a 300kB file.
	maxQueuedFrames = 4096
	maxQueuedBytes  = 64 << 20
)

// errConnClosed is what a send on a connection that is shutting down, or
// already gone, returns.
var errConnClosed = errors.New("lsp: connection closed")

// startLoops launches the connection's two goroutines. Every constructor
// ends here, so the channels the loops share exist before either runs.
func (c *Client) startLoops() {
	c.outbox = make(chan []byte, maxQueuedFrames)
	c.stopW = make(chan struct{})
	c.dead = make(chan struct{})
	c.wdone = make(chan struct{})
	c.reaped = make(chan struct{})
	// Read the tunables ONCE, here: a test that shortens them must not
	// race the goroutines of a client an earlier test left running.
	c.stall, c.grace = writeStall, closeGrace
	go c.readLoop()
	go c.writeLoop()
}

// enqueue hands one framed message to the writer without blocking. A
// full queue or a byte budget overrun means the server stopped reading:
// the connection is failed (process killed) rather than left to grow.
func (c *Client) enqueue(frame []byte) error {
	c.mu.Lock()
	switch {
	case c.failErr != nil:
		err := c.failErr
		c.mu.Unlock()
		return err
	case c.wclosed:
		c.mu.Unlock()
		return errConnClosed
	case c.queued+len(frame) > maxQueuedBytes:
		c.mu.Unlock()
		return c.fail(fmt.Errorf("lsp: server not reading (%d bytes queued)", c.queued))
	}
	select {
	case c.outbox <- frame:
		c.queued += len(frame)
		c.mu.Unlock()
		return nil
	default:
		c.mu.Unlock()
		return c.fail(fmt.Errorf("lsp: server not reading (%d messages queued)", maxQueuedFrames))
	}
}

// writeLoop is the one goroutine that writes the server's stdin. It runs
// until the connection fails, or Close asks it to stop — then it drains
// what was queued before that (the `exit` notification is the last
// frame) and closes stdin, which is the EOF a server exits on.
func (c *Client) writeLoop() {
	defer crash.Recover() // the goroutine every constructor launches
	defer close(c.wdone)
	defer c.closeWriter()
	for {
		select {
		case frame := <-c.outbox:
			if !c.writeFrame(frame) {
				return
			}
		case <-c.stopW:
			for {
				select {
				case frame := <-c.outbox:
					if !c.writeFrame(frame) {
						return
					}
				default:
					return
				}
			}
		case <-c.dead:
			return
		}
	}
}

// writeFrame writes one frame under the stall deadline: a write that is
// still blocked after writeStall fails the connection, and the fail
// path closes the pipe, which is what unblocks the write. false means the
// writer is done.
func (c *Client) writeFrame(frame []byte) bool {
	stall := time.AfterFunc(c.stall, func() {
		_ = c.fail(fmt.Errorf("lsp: server stopped reading (a write blocked for %s)", c.stall))
	})
	_, err := c.w.Write(frame)
	stall.Stop()
	c.mu.Lock()
	c.queued -= len(frame)
	c.mu.Unlock()
	if err != nil {
		_ = c.fail(fmt.Errorf("lsp: write to server: %w", err))
		return false
	}
	return true
}

// markDead records why the connection ended (the first reason wins) and
// wakes everything waiting on it. Calls fail from here on.
func (c *Client) markDead(err error) {
	c.deadOnce.Do(func() {
		c.mu.Lock()
		c.failErr = err
		c.closed = true
		c.mu.Unlock()
		close(c.dead)
	})
}

// fail ends a connection that cannot go on — the server stopped reading,
// or a write broke. It kills the process and closes both pipes, so the
// read loop ends and onExit reports the server gone (carrying this
// reason), exactly as a crash would. Returns the recorded reason, which
// may be an earlier one.
func (c *Client) fail(err error) error {
	c.markDead(err)
	c.killProcess()
	c.closeWriter()
	c.closeReader()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failErr
}

// failure returns why the connection failed, or nil.
func (c *Client) failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failErr
}

// killProcess kills the server process, when there is one. Safe to call
// repeatedly and after it has exited.
func (c *Client) killProcess() {
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

// closeWriter closes the server's stdin once. Closing it from another
// goroutine is what unblocks a write stuck on a full pipe (os.File and
// io.Pipe both return an error to the blocked writer).
func (c *Client) closeWriter() {
	c.wcloseOnce.Do(func() {
		if wc, ok := c.w.(io.Closer); ok {
			_ = wc.Close()
		}
	})
}

// closeReader closes the server's stdout, when the reader can be closed,
// so a read loop blocked on a deaf server ends too.
func (c *Client) closeReader() {
	if c.rc != nil {
		_ = c.rc.Close()
	}
}

// reap waits for the connection to wind down after Close and makes sure
// it does: a process gets closeGrace to exit on the EOF and is killed
// after; a pipe-only client (tests) gets the same grace for its writer to
// drain. The process is ALWAYS waited for — even one that exited on its
// own long before Close (N-126c: the old early return left a zombie).
func (c *Client) reap() {
	defer crash.Recover()
	defer close(c.reaped)
	if c.cmd == nil || c.cmd.Process == nil {
		select {
		case <-c.wdone:
		case <-time.After(c.grace):
			_ = c.fail(errConnClosed)
		}
		return
	}
	exited := make(chan struct{})
	go func() {
		defer crash.Recover()
		_ = c.cmd.Wait()
		close(exited)
	}()
	select {
	case <-exited:
	case <-time.After(c.grace):
		_ = c.fail(errConnClosed)
		<-exited
	}
}
