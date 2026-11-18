// =============================================================================
// File: internal/app/postresult_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// fillQueue posts filler events until tcell refuses one, so the screen's
// queue is exactly full — the state N-124's drops happen in.
func fillQueue(t *testing.T, scr tcell.Screen) int {
	t.Helper()
	for n := 0; n < 10000; n++ {
		if scr.PostEvent(&autoScrollEvent{when: time.Now()}) != nil {
			return n
		}
	}
	t.Fatal("the event queue never filled")
	return 0
}

// TestPostResult_WaitsForRoomInsteadOfDropping pins N-124: with the queue
// full, plain PostEvent drops a completion, postResult holds it until the
// loop takes something off the queue and then delivers it.
func TestPostResult_WaitsForRoomInsteadOfDropping(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatal(err)
	}
	defer scr.Fini()
	n := fillQueue(t, scr)
	done := &formatDoneEvent{when: time.Now(), tabPath: "/x.go"}
	if scr.PostEvent(done) == nil {
		t.Fatal("fixture: PostEvent accepted an event on a full queue")
	}
	delivered := make(chan bool, 1)
	go func() { delivered <- postResult(scr, done) }()
	time.Sleep(20 * time.Millisecond)
	_ = scr.PollEvent() // the loop drains one event: room for one
	select {
	case ok := <-delivered:
		if !ok {
			t.Fatal("postResult gave up although room appeared")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("postResult never delivered")
	}
	for i := 0; i < n; i++ {
		if ev, ok := scr.PollEvent().(*formatDoneEvent); ok && ev == done {
			return
		}
	}
	t.Error("the delivered event was not in the queue")
}

// TestPostResult_GivesUpOnAQueueNobodyDrains: a screen whose loop is gone
// (closed, switched away from) never makes room; postResult returns false
// after postResultGiveUp instead of leaking its goroutine for good.
func TestPostResult_GivesUpOnAQueueNobodyDrains(t *testing.T) {
	old := postResultGiveUp
	postResultGiveUp = 50 * time.Millisecond
	t.Cleanup(func() { postResultGiveUp = old })
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatal(err)
	}
	defer scr.Fini()
	fillQueue(t, scr)
	start := time.Now()
	if postResult(scr, &termDoneEvent{when: time.Now()}) {
		t.Error("delivered onto a queue nothing drained")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("gave up after %s", d)
	}
	if postResult(nil, &termDoneEvent{}) {
		t.Error("a nil screen reported delivery")
	}
}

// TestCompletionEventsArePostedReliably keeps N-124 from regressing one
// site at a time: no …Done / …Ready / …Exit / …Result event is posted
// with plain PostEvent outside the few sites that check its error
// themselves.
func TestCompletionEventsArePostedReliably(t *testing.T) {
	plain := regexp.MustCompile(`_ = \w+\.PostEvent\(&(\w+(Done|Ready|Exit|Result|Failed)Event)`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if m := plain.FindStringSubmatch(line); m != nil {
				t.Errorf("%s:%d posts %s with PostEvent — use postResult (a drop is permanent stuck state)", f, i+1, m[1])
			}
		}
	}
}
