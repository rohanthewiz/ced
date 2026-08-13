// =============================================================================
// File: internal/cats/detect_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package cats

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// clearCatsEnv unsets the pane environment for one test, so a developer
// running the suite from INSIDE a cats pane gets the same answers as CI.
// Without it, "not in cats" cases would pass or fail depending on where the
// test was run from — the worst kind of flake.
func clearCatsEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{EnvMarker, EnvPaneID, EnvControlSocket, EnvHookSocket} {
		t.Setenv(k, "")
	}
}

// Outside cats, detection is Tier 0 with a reason and nothing else — the
// state every non-cats terminal is in.
func TestDetectEnvOutsideCats(t *testing.T) {
	clearCatsEnv(t)
	c := DetectEnv()
	if c.InCats || c.Tier1() || c.Hooks {
		t.Fatalf("expected Tier 0, got %+v", c)
	}
	if c.Reason == "" {
		t.Fatal("a Tier-0 verdict must say why")
	}
}

// The marker alone is not enough: a process that outlived its pane can
// inherit CATS_ENV, and without a pane handle there is nothing to address.
func TestDetectEnvNeedsBothHalves(t *testing.T) {
	clearCatsEnv(t)
	t.Setenv(EnvMarker, "1")
	if DetectEnv().InCats {
		t.Fatal("marker without a pane handle must not count as being in cats")
	}
	t.Setenv(EnvMarker, "")
	t.Setenv(EnvPaneID, "w1:p3")
	if DetectEnv().InCats {
		t.Fatal("pane handle without the marker must not count as being in cats")
	}
}

// Inside a pane, the addresses come through, and the hook capability tracks
// whether the socket is actually THERE rather than merely named.
func TestDetectEnvInsideCats(t *testing.T) {
	clearCatsEnv(t)
	hook := sockPath(t)
	t.Setenv(EnvMarker, "1")
	t.Setenv(EnvPaneID, "w1:p3")
	t.Setenv(EnvControlSocket, "/tmp/ctl.sock")
	t.Setenv(EnvHookSocket, hook)

	if c := DetectEnv(); c.Hooks {
		t.Fatal("a hook socket that does not exist must not report available")
	}
	ln, err := net.Listen("unix", hook)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	c := DetectEnv()
	if !c.InCats || !c.Hooks {
		t.Fatalf("got %+v", c)
	}
	if c.PaneHandle != "w1:p3" || c.ControlSocket != "/tmp/ctl.sock" || c.HookSocket != hook {
		t.Fatalf("addresses not carried through: %+v", c)
	}
	// The public handle carries no internal id — that costs a pane.list.
	if c.PaneIDKnown {
		t.Fatal("w1:p3 must not resolve to an internal id locally")
	}
	if c.Tier1() {
		t.Fatal("env alone must never reach Tier 1 — only a probe can")
	}
}

// Only the "p_<n>" fallback handle carries the internal pane id; everything
// else has to be resolved against the server.
func TestParsePaneHandle(t *testing.T) {
	cases := []struct {
		in   string
		want uint32
		ok   bool
	}{
		{"p_0", 0, true},
		{"p_42", 42, true},
		{"w1:p3", 0, false},
		{"p_", 0, false},
		{"p_abc", 0, false},
		{"p_-1", 0, false},
		{"p_99999999999999999999", 0, false}, // past uint32
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := ParsePaneHandle(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ParsePaneHandle(%q) = %d, %v; want %d, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// A live control socket that answers ping is Tier 1, and the server's
// self-identification is kept for the status surface.
func TestProbeReachesTier1(t *testing.T) {
	s := startFake(t, func(req request, conn net.Conn) {
		reply(t, conn, Pong{Protocol: 1, Service: "catway"})
	})
	c := Caps{InCats: true, PaneHandle: "w1:p3", ControlSocket: s.path}.Probe()
	if !c.Tier1() {
		t.Fatalf("expected Tier 1, got %+v", c)
	}
	if c.Service != "catway" || c.Protocol != 1 {
		t.Fatalf("identity not recorded: %+v", c)
	}
	if c.Reason != "" {
		t.Fatalf("Tier 1 needs no reason, got %q", c.Reason)
	}
}

// A socket FILE proves nothing: a crashed server leaves one behind, and
// even a successful dial only proves something is listening. The probe
// insists on a reply, and says so when it doesn't get one.
func TestProbeRejectsDeadSocket(t *testing.T) {
	// Nothing listening at all.
	c := Caps{InCats: true, PaneHandle: "w1:p3", ControlSocket: sockPath(t)}.Probe()
	if c.Control || c.Tier1() {
		t.Fatalf("a missing socket must not reach Tier 1: %+v", c)
	}
	if !strings.Contains(c.Reason, "not answering") {
		t.Fatalf("reason = %q", c.Reason)
	}

	// A listener that accepts and then says nothing — the wedged-server
	// case. The probe must give up on its own timeout rather than hang.
	path := sockPath(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close() //nolint:revive // held open on purpose for the test's lifetime
		}
	}()
	prev := ProbeTimeout
	ProbeTimeout = 150 * time.Millisecond
	defer func() { ProbeTimeout = prev }()

	start := time.Now()
	c = Caps{InCats: true, PaneHandle: "w1:p3", ControlSocket: path}.Probe()
	if c.Control {
		t.Fatal("a silent listener must not reach Tier 1")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("probe took %v — the timeout is not bounding it", elapsed)
	}
}

// Probing outside cats does no IO and changes nothing: the env verdict is
// final, so a Tier-0 editor never dials anything at startup.
func TestProbeOutsideCatsIsANoop(t *testing.T) {
	c := Caps{Reason: "not running inside a cats pane"}
	if got := c.Probe(); got != c {
		t.Fatalf("probe changed a Tier-0 verdict: %+v", got)
	}
}

// Detect is the two halves in one call, for callers that can afford the IO.
func TestDetectEndToEnd(t *testing.T) {
	clearCatsEnv(t)
	s := startFake(t, func(req request, conn net.Conn) {
		reply(t, conn, Pong{Protocol: 1, Service: "catway"})
	})
	hook := sockPath(t)
	ln, err := net.Listen("unix", hook)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	t.Setenv(EnvMarker, "1")
	t.Setenv(EnvPaneID, "p_7")
	t.Setenv(EnvControlSocket, s.path)
	t.Setenv(EnvHookSocket, hook)

	c := Detect()
	if !c.Tier1() || !c.Hooks {
		t.Fatalf("got %+v", c)
	}
	if !c.PaneIDKnown || c.PaneID != 7 {
		t.Fatalf("pane id not parsed from p_7: %+v", c)
	}
}

// The dial helper always bounds itself, even when handed no timeout — an
// unbounded read on a wedged server is how a background goroutine leaks.
func TestDialAlwaysHasADeadline(t *testing.T) {
	path := sockPath(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			defer conn.Close()
			select {} // never answers
		}
	}()
	prev := ProbeTimeout
	ProbeTimeout = 100 * time.Millisecond
	defer func() { ProbeTimeout = prev }()

	conn, err := dial(path, 0)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("expected the read to time out")
	}
}

// os.Stat is what decides Hooks, so a path pointing at a plain file counts
// too — the reporter's own dial is the real check, and refusing to try
// would be a second, weaker copy of it.
func TestDetectEnvHookPathIsNotProbed(t *testing.T) {
	clearCatsEnv(t)
	f := sockPath(t)
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(EnvMarker, "1")
	t.Setenv(EnvPaneID, "p_1")
	t.Setenv(EnvHookSocket, f)
	if !DetectEnv().Hooks {
		t.Fatal("an existing hook path should report available")
	}
}
