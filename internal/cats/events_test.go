// =============================================================================
// File: internal/cats/events_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package cats

import (
	"bufio"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"
)

// fastReconnect collapses the backoff for one test. Reconnection is the
// behavior under test in half this file, and at the shipped 500ms floor
// every one of those tests would spend most of its life asleep.
func fastReconnect(t *testing.T) {
	t.Helper()
	prevMin, prevMax := reconnectMin, reconnectMax
	reconnectMin, reconnectMax = 5*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { reconnectMin, reconnectMax = prevMin, prevMax })
}

// streamServer is a fake events.subscribe endpoint: it acks each
// subscription and then writes whatever the test pushes at it. Unlike
// fakeServer it keeps connections open, which is the whole point of the
// streaming method.
type streamServer struct {
	t    *testing.T
	path string

	mu    sync.Mutex
	ln    net.Listener
	conns []net.Conn
	subs  chan struct{}   // one token per accepted subscription
	lines chan rawRequest // the subscribe request as it arrived on the wire
	// refuse makes the server answer the subscribe with ok:false.
	refuse bool
	// withEvent writes one event in the SAME write as the ack, which is
	// how a real server can hand a client both at once.
	withEvent *Event
}

// startStream binds a fake streaming server at a fresh path.
func startStream(t *testing.T) *streamServer {
	s := &streamServer{
		t: t, path: sockPath(t),
		subs:  make(chan struct{}, 8),
		lines: make(chan rawRequest, 8),
	}
	s.listen()
	t.Cleanup(s.stop)
	return s
}

// listen (re)binds the socket and serves subscriptions until stopped. Split
// out so a test can kill the server and bring it back at the same address —
// exactly what a cats restart looks like from here.
func (s *streamServer) listen() {
	s.t.Helper()
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		s.t.Fatalf("listen: %v", err)
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
}

// serve acks one subscription and holds the connection open.
func (s *streamServer) serve(conn net.Conn) {
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		_ = conn.Close()
		return
	}
	var raw rawRequest
	if err := json.Unmarshal(line, &raw); err == nil {
		select {
		case s.lines <- raw:
		default:
		}
	}
	s.mu.Lock()
	refuse, withEvent := s.refuse, s.withEvent
	s.mu.Unlock()

	if refuse {
		b, _ := json.Marshal(response{ID: "ced-events", OK: false, Error: "unknown event"})
		_, _ = conn.Write(append(b, '\n'))
		_ = conn.Close()
		return
	}
	out, _ := json.Marshal(response{ID: "ced-events", OK: true})
	out = append(out, '\n')
	if withEvent != nil {
		b, _ := json.Marshal(withEvent)
		out = append(out, b...)
		out = append(out, '\n')
	}
	if _, err := conn.Write(out); err != nil {
		_ = conn.Close()
		return
	}
	s.mu.Lock()
	s.conns = append(s.conns, conn)
	s.mu.Unlock()
	s.subs <- struct{}{}
}

// push writes one event to every live subscriber.
func (s *streamServer) push(name string, data any) {
	s.t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		s.t.Fatalf("marshal event: %v", err)
	}
	b, _ := json.Marshal(Event{Name: name, Data: raw})
	b = append(b, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		_, _ = c.Write(b)
	}
}

// drop closes the listener and every live connection: the cats-went-away
// case.
func (s *streamServer) drop() {
	s.mu.Lock()
	if s.ln != nil {
		_ = s.ln.Close()
		s.ln = nil
	}
	conns := s.conns
	s.conns = nil
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

func (s *streamServer) stop() { s.drop() }

// waitSub blocks until the server has accepted another subscription.
func (s *streamServer) waitSub(what string) {
	s.t.Helper()
	select {
	case <-s.subs:
	case <-time.After(3 * time.Second):
		s.t.Fatalf("no subscription arrived: %s", what)
	}
}

// collector accumulates what a Stream delivers, safely across goroutines.
type collector struct {
	mu     sync.Mutex
	events []Event
	ups    []bool
}

func (c *collector) onEvent(e Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *collector) onState(up bool, _ error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ups = append(c.ups, up)
}

// waitEvents blocks until at least n events have arrived, then returns them.
func (c *collector) waitEvents(t *testing.T, n int) []Event {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		got := len(c.events)
		c.mu.Unlock()
		if got >= n {
			c.mu.Lock()
			defer c.mu.Unlock()
			return append([]Event(nil), c.events...)
		}
		time.Sleep(2 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t.Fatalf("only %d of %d events arrived", len(c.events), n)
	return nil
}

// The happy path: subscribe, get told the link is up, receive events with
// their payloads intact.
func TestStreamDelivers(t *testing.T) {
	s := startStream(t)
	var c collector
	st := Subscribe(s.path, SubscribeFilter{Events: []string{EventPaneNotify}}, c.onEvent, c.onState)
	defer st.Close()
	s.waitSub("initial")

	s.push(EventPaneNotify, PaneNotifyEvent{Pane: 7, Agent: "claude", Kind: "attention", Message: "needs you"})
	got := c.waitEvents(t, 1)
	if got[0].Name != EventPaneNotify {
		t.Fatalf("name = %q", got[0].Name)
	}
	var p PaneNotifyEvent
	if err := json.Unmarshal(got[0].Data, &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if p.Pane != 7 || p.Agent != "claude" || p.Message != "needs you" {
		t.Fatalf("payload = %+v", p)
	}
	c.mu.Lock()
	ups := append([]bool(nil), c.ups...)
	c.mu.Unlock()
	if len(ups) == 0 || !ups[0] {
		t.Fatalf("expected an up transition first, got %v", ups)
	}
}

// The subscribe filter reaches the server: a narrow subscription must be
// narrowed SERVER-side, or an idle editor is woken by every title change in
// the session.
func TestStreamSendsItsFilter(t *testing.T) {
	s := startStream(t)
	pane := uint32(4)
	st := Subscribe(s.path, SubscribeFilter{Pane: &pane, Events: []string{EventPaneNotify}}, nil, nil)
	defer st.Close()
	s.waitSub("filtered")

	select {
	case req := <-s.lines:
		if req.Method != MethodSubscribe {
			t.Fatalf("method = %q", req.Method)
		}
		if got := string(req.Params); got != `{"pane":4,"events":["pane_notify"]}` {
			t.Fatalf("filter reached the server as %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no subscribe request reached the server")
	}
}

// An event written in the same breath as the ack still arrives. This is the
// regression guard for the buffering trap: a second json.Decoder for the
// pump would leave that first event stranded in the handshake decoder's
// buffer, and it would never be seen again.
func TestStreamEventBundledWithAck(t *testing.T) {
	s := startStream(t)
	raw, _ := json.Marshal(PaneNotifyEvent{Pane: 2, Agent: "codex", Kind: "finished"})
	s.mu.Lock()
	s.withEvent = &Event{Name: EventPaneNotify, Data: raw}
	s.mu.Unlock()

	var c collector
	st := Subscribe(s.path, SubscribeFilter{}, c.onEvent, c.onState)
	defer st.Close()

	got := c.waitEvents(t, 1)
	var p PaneNotifyEvent
	if err := json.Unmarshal(got[0].Data, &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if p.Agent != "codex" {
		t.Fatalf("payload = %+v", p)
	}
}

// The subscription survives a cats restart. This is the difference between
// a stream and a unary call: a call that fails is a feature that didn't
// happen, but a subscription that stays dead is a feature that stopped
// working and never said so.
func TestStreamReconnects(t *testing.T) {
	fastReconnect(t)
	s := startStream(t)
	var c collector
	st := Subscribe(s.path, SubscribeFilter{}, c.onEvent, c.onState)
	defer st.Close()
	s.waitSub("initial")

	s.push(EventPaneNotify, PaneNotifyEvent{Pane: 1, Kind: "attention"})
	c.waitEvents(t, 1)

	s.drop()   // cats goes away
	s.listen() // …and comes back at the same address
	s.waitSub("after restart")

	s.push(EventPaneNotify, PaneNotifyEvent{Pane: 2, Kind: "attention"})
	got := c.waitEvents(t, 2)
	var p PaneNotifyEvent
	if err := json.Unmarshal(got[1].Data, &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if p.Pane != 2 {
		t.Fatalf("second event = %+v", p)
	}
	c.mu.Lock()
	ups := append([]bool(nil), c.ups...)
	c.mu.Unlock()
	if len(ups) < 3 || !ups[0] || ups[len(ups)-1] != true {
		t.Fatalf("expected up → down → up, got %v", ups)
	}
}

// A refused subscription is retried, not accepted as final: the server that
// rejects an event name today may be a different build tomorrow.
func TestStreamRetriesARefusal(t *testing.T) {
	fastReconnect(t)
	s := startStream(t)
	s.mu.Lock()
	s.refuse = true
	s.mu.Unlock()

	var c collector
	st := Subscribe(s.path, SubscribeFilter{}, c.onEvent, c.onState)
	defer st.Close()

	time.Sleep(60 * time.Millisecond) // several refused attempts
	s.mu.Lock()
	s.refuse = false
	s.mu.Unlock()
	s.waitSub("after the refusal cleared")

	s.push(EventPaneNotify, PaneNotifyEvent{Pane: 3, Kind: "attention"})
	c.waitEvents(t, 1)
}

// A stream that never connects gives up quietly and stays retryable. The
// editor must not care that this goroutine exists.
func TestStreamDeadSocketIsQuiet(t *testing.T) {
	fastReconnect(t)
	var c collector
	st := Subscribe(sockPath(t), SubscribeFilter{}, c.onEvent, c.onState)
	time.Sleep(40 * time.Millisecond)
	st.Close()

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) != 0 {
		t.Fatalf("no server, but %d events arrived", len(c.events))
	}
	for _, up := range c.ups {
		if up {
			t.Fatal("reported the link up with nothing listening")
		}
	}
}

// Close interrupts a blocked read and waits for the goroutine, so a closed
// Stream can never deliver another callback — which is what lets the app
// tear one down during shutdown without racing a screen being finalized.
func TestStreamCloseIsFinal(t *testing.T) {
	s := startStream(t)
	var c collector
	st := Subscribe(s.path, SubscribeFilter{}, c.onEvent, c.onState)
	s.waitSub("initial")

	st.Close()
	st.Close() // idempotent

	before := len(c.waitEventsNoFail())
	s.push(EventPaneNotify, PaneNotifyEvent{Pane: 9, Kind: "attention"})
	time.Sleep(30 * time.Millisecond)
	if after := len(c.waitEventsNoFail()); after != before {
		t.Fatalf("delivered %d events after Close", after-before)
	}
}

// waitEventsNoFail snapshots what has arrived so far without asserting.
func (c *collector) waitEventsNoFail() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Event(nil), c.events...)
}

// A nil Stream is the Tier-0 form; closing one must be a no-op rather than
// a panic, because the app's shutdown path calls it unconditionally.
func TestNilStreamCloses(t *testing.T) {
	var st *Stream
	st.Close()
}
