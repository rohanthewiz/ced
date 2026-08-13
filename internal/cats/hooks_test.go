// =============================================================================
// File: internal/cats/hooks_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package cats

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// hookServer is a fake hook socket: one request per connection, captured
// for assertions. It answers nothing, because the reporter reads nothing —
// which is itself part of the contract being pinned here.
type hookServer struct {
	t    *testing.T
	path string
	got  chan hookRequest
}

// startHookServer binds a fake hook socket for the test's lifetime.
func startHookServer(t *testing.T) *hookServer {
	t.Helper()
	s := &hookServer{t: t, path: sockPath(t), got: make(chan hookRequest, 16)}
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil && len(line) == 0 {
					return
				}
				var req hookRequest
				if err := json.Unmarshal(line, &req); err != nil {
					return
				}
				select {
				case s.got <- req:
				default:
				}
			}()
		}
	}()
	return s
}

// next returns the next report the server received.
func (s *hookServer) next() hookRequest {
	s.t.Helper()
	select {
	case r := <-s.got:
		return r
	case <-time.After(2 * time.Second):
		s.t.Fatal("no hook report arrived")
		return hookRequest{}
	}
}

// A state report carries the identity cats arbitrates on: the pane handle
// from the environment, an unreserved source, a label, the state, and the
// badge text.
func TestReportStateWireShape(t *testing.T) {
	s := startHookServer(t)
	r := NewReporter(s.path, "w1:p3")
	r.ReportState(StateBlocked, "file changed on disk")

	req := s.next()
	if req.Method != methodReportAgent {
		t.Fatalf("method = %q", req.Method)
	}
	p := req.Params
	if p.PaneID != "w1:p3" {
		t.Fatalf("pane = %q", p.PaneID)
	}
	// Not "cats:ced": the cats: prefix names cats' own built-in agent
	// integrations, whose state is detection-driven and whose hook state
	// reports are ignored.
	if p.Source != "ced" || strings.HasPrefix(p.Source, "cats:") {
		t.Fatalf("source = %q", p.Source)
	}
	if p.Agent != "ced" {
		t.Fatalf("agent = %q", p.Agent)
	}
	if p.State != StateBlocked || p.CustomStatus != "file changed on disk" {
		t.Fatalf("state = %q / %q", p.State, p.CustomStatus)
	}
	if p.Seq == nil || *p.Seq == 0 {
		t.Fatal("every report must carry a seq — the server accepts a seq-less one only once, ever")
	}
}

// Two reporters for the same pane — a restarted ced, which a folder switch
// alone produces — must not restart the sequence, or the server's per-pane
// high-water mark would drop every report from the new instance as stale.
// The clock seed is what prevents that, and it is why the seq is not a
// plain counter.
func TestReportSeqSurvivesARestart(t *testing.T) {
	s := startHookServer(t)

	first := NewReporter(s.path, "p_1")
	first.ReportState(StateWorking, "before")
	firstSeq := *s.next().Params.Seq
	first.Release()
	s.next()

	second := NewReporter(s.path, "p_1") // the "restarted" editor
	second.ReportState(StateIdle, "after")
	if got := *s.next().Params.Seq; got <= firstSeq {
		t.Fatalf("a restarted reporter began at %d, at or below the old high-water mark %d", got, firstSeq)
	}
}

// Seq is stamped in the order the TRANSITIONS happened, not the order the
// writes complete. That distinction is the whole mechanism: reports go out
// on their own goroutines and genuinely do arrive out of order (this test
// saw 3 land before 2 before the numbering was pinned), and the server's
// per-source high-water mark is what discards the stale one. It can only do
// that if the number describes the transition rather than the delivery.
func TestReportSeqFollowsTransitionOrder(t *testing.T) {
	s := startHookServer(t)
	r := NewReporter(s.path, "p_1")
	r.ReportState(StateWorking, "searching") // first transition
	r.ReportState(StateIdle, "")             // second
	r.Release()                              // third

	seqOf := map[string]uint64{}
	for i := 0; i < 3; i++ {
		req := s.next()
		if req.Params.Seq == nil {
			t.Fatalf("report %d has no seq", i)
		}
		key := req.Params.State
		if req.Method == methodReleaseAgent {
			key = "release"
		}
		seqOf[key] = *req.Params.Seq
	}
	if seqOf[StateWorking] >= seqOf[StateIdle] || seqOf[StateIdle] >= seqOf["release"] {
		t.Fatalf("seqs do not follow transition order: %v", seqOf)
	}
}

// Release withdraws the claim, so a pane stops being labeled "ced" the
// moment the editor exits instead of wearing a stale badge.
func TestReleaseWireShape(t *testing.T) {
	s := startHookServer(t)
	NewReporter(s.path, "p_9").Release()

	req := s.next()
	if req.Method != methodReleaseAgent {
		t.Fatalf("method = %q", req.Method)
	}
	if req.Params.PaneID != "p_9" || req.Params.Source != SourceCed || req.Params.Agent != AgentCed {
		t.Fatalf("params = %+v", req.Params)
	}
	// A release names no state: it is a withdrawal, not a transition.
	if req.Params.State != "" {
		t.Fatalf("release carried state %q", req.Params.State)
	}
}

// Release reaches the socket. It writes inline rather than on a goroutine —
// it runs from Close, where the process is about to exit and a goroutine
// posted on the way out would be killed before it ever dialed.
//
// The inline-ness itself is not observable from here: this test still
// passes for an async implementation, because a test process does not exit
// the way the editor does. Arrival is what it pins; the timing is pinned by
// construction (Release calls send, ReportState calls `go send`).
func TestReleaseReachesTheSocket(t *testing.T) {
	s := startHookServer(t)
	NewReporter(s.path, "p_2").Release()
	if got := s.next(); got.Method != methodReleaseAgent {
		t.Fatalf("method = %q", got.Method)
	}
}

// Without an address there is no reporter, and a nil reporter is a working
// no-op — the Tier-0 path every call site takes without checking.
func TestNilReporterIsANoop(t *testing.T) {
	if NewReporter("", "p_1") != nil {
		t.Fatal("no socket must yield no reporter")
	}
	if NewReporter("/tmp/x.sock", "") != nil {
		t.Fatal("no pane handle must yield no reporter")
	}
	var r *Reporter
	r.ReportState(StateIdle, "")
	r.Release()
}

// A dead socket costs nothing and says nothing. A status report is not
// worth surfacing an error for, and the alternative — a flash on every
// failed report — would be a permanent error message on a host that simply
// does not run the hook API.
func TestReportToDeadSocketIsSilent(t *testing.T) {
	r := NewReporter(sockPath(t), "p_1")
	r.ReportState(StateWorking, "x")
	r.Release()
	time.Sleep(20 * time.Millisecond) // let the fire-and-forget goroutine finish
}

// The custom status is trimmed to what cats will actually display, without
// splitting a multi-byte character — the server truncates in BYTES and does
// not check rune boundaries, so cutting it correctly here is what keeps
// invalid UTF-8 out of someone else's UI.
func TestClampStatus(t *testing.T) {
	if got := clampStatus("  disk conflict  "); got != "disk conflict" {
		t.Fatalf("got %q", got)
	}
	// Control bytes would terminate an escape sequence early in a host
	// that renders the status — the same guard titles get.
	if got := clampStatus("a\x1b]0;evil\x07b"); got != "a]0;evilb" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("z", 40)
	if got := clampStatus(long); len(got) != customStatusMax {
		t.Fatalf("len = %d, want %d", len(got), customStatusMax)
	}
	// 11 three-byte runes = 33 bytes: the cut lands mid-character and must
	// back off to the boundary rather than emit a broken one.
	multi := strings.Repeat("字", 11)
	got := clampStatus(multi)
	if len(got) > customStatusMax {
		t.Fatalf("%d bytes exceeds the server's cap", len(got))
	}
	if strings.ContainsRune(got, '�') || len([]rune(got)) != 10 {
		t.Fatalf("cut mid-rune: %q (%d runes)", got, len([]rune(got)))
	}
}
