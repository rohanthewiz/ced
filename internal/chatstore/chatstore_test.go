// =============================================================================
// File: internal/chatstore/chatstore_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package chatstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// conv builds a minimal saveable conversation for the tests below.
func conv(id, title string, updated time.Time) Conversation {
	return Conversation{
		ID:      id,
		Title:   title,
		Started: updated.Add(-time.Minute),
		Updated: updated,
		Msgs:    []Msg{{Role: "user", Text: title}},
	}
}

// TestSaveLoadRoundTrip pins the basic contract: what goes in comes back
// out, under the id it was saved with, in a file named for that id.
func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 28, 14, 25, 0, 0, time.UTC)
	in := conv("20260828-142500-000", "why does the caret drift?", now)
	in.Agent = "Copilot"
	in.Msgs = append(in.Msgs, Msg{Role: "agent", Text: "because caretGoalCol"})

	if err := Save(dir, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, in.ID+".json")); err != nil {
		t.Fatalf("archive file: %v", err)
	}
	out, err := Load(dir, in.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Title != in.Title || out.Agent != in.Agent || len(out.Msgs) != 2 {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}
	if out.Msgs[1].Text != "because caretGoalCol" {
		t.Errorf("agent message = %q", out.Msgs[1].Text)
	}
}

// TestSaveRewritesSameID pins the shape the live conversation depends
// on: re-saving one id overwrites its file rather than accumulating a
// row per turn. A conversation is ONE entry in the Recent list however
// many times it is written.
func TestSaveRewritesSameID(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	c := conv("20260828-142500-000", "first", now)
	if err := Save(dir, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	c.Msgs = append(c.Msgs, Msg{Role: "agent", Text: "answer"})
	c.Updated = now.Add(time.Minute)
	if err := Save(dir, c); err != nil {
		t.Fatalf("re-Save: %v", err)
	}
	metas, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(metas))
	}
	if metas[0].Count != 2 {
		t.Errorf("message count = %d, want 2", metas[0].Count)
	}
}

// TestSaveSkipsEmpty pins the no-op cases. Teardown archives
// unconditionally, so "there was no conversation" has to be silence
// rather than an error — and it must not leave a file behind that would
// show up as an unopenable blank row in the picker.
func TestSaveSkipsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, Conversation{ID: "x", Updated: time.Now()}); err != nil {
		t.Fatalf("empty msgs: %v", err)
	}
	if err := Save(dir, Conversation{Msgs: []Msg{{Role: "user", Text: "hi"}}}); err != nil {
		t.Fatalf("empty id: %v", err)
	}
	metas, _ := List(dir)
	if len(metas) != 0 {
		t.Fatalf("List returned %d entries, want 0", len(metas))
	}
}

// TestListNewestFirst pins the ordering the picker reads as recency.
func TestListNewestFirst(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	for i, name := range []string{"oldest", "middle", "newest"} {
		if err := Save(dir, conv(NewID(base.Add(time.Duration(i)*time.Hour)), name,
			base.Add(time.Duration(i)*time.Hour))); err != nil {
			t.Fatalf("Save %s: %v", name, err)
		}
	}
	metas, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"newest", "middle", "oldest"}
	if len(metas) != len(want) {
		t.Fatalf("List returned %d entries, want %d", len(metas), len(want))
	}
	for i, w := range want {
		if metas[i].Title != w {
			t.Errorf("metas[%d].Title = %q, want %q", i, metas[i].Title, w)
		}
	}
}

// TestListMissingDirIsQuiet pins the common case for a user who has
// never chatted: no directory is not a failure to report.
func TestListMissingDirIsQuiet(t *testing.T) {
	metas, err := List(filepath.Join(t.TempDir(), "never-created"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 0 {
		t.Fatalf("List returned %d entries, want 0", len(metas))
	}
}

// TestListSkipsUnreadableFile pins per-file degradation (the theme
// registry's rule): one truncated transcript costs itself, not the
// whole picker.
func TestListSkipsUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, conv("20260828-142500-000", "good", time.Now())); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A stray non-JSON file in the directory costs nothing either.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	metas, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 1 || metas[0].Title != "good" {
		t.Fatalf("List = %+v, want just the good entry", metas)
	}
}

// TestPruneKeepsNewest pins the retention cap: the archive is "the last
// few chats", so a save past the cap trims the OLDEST away and never
// touches what the user just wrote.
func TestPruneKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < MaxConversations+5; i++ {
		when := base.Add(time.Duration(i) * time.Hour)
		if err := Save(dir, conv(NewID(when), "chat", when)); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}
	metas, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != MaxConversations {
		t.Fatalf("kept %d conversations, want %d", len(metas), MaxConversations)
	}
	newest := base.Add(time.Duration(MaxConversations+4) * time.Hour)
	if !metas[0].Updated.Equal(newest) {
		t.Errorf("newest kept = %v, want %v", metas[0].Updated, newest)
	}
}

// TestLoadRejectsEscapingID pins the containment guard: an id becomes a
// filename, and one that walks out of the archive must be refused rather
// than read.
func TestLoadRejectsEscapingID(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir, "../../etc/passwd"); err == nil {
		t.Fatal("Load accepted an escaping id")
	}
	if err := Remove(dir, "../../etc/passwd"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

// TestRemoveMissingIsSuccess pins Remove's contract: the caller wanted
// the conversation gone, and it is.
func TestRemoveMissingIsSuccess(t *testing.T) {
	if err := Remove(t.TempDir(), "20260828-142500-000"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

// TestDeriveTitleUsesFirstUserLine pins what a conversation is
// remembered by: the user's question, first line only, not the agent's
// answer and not an editor-side status note.
func TestDeriveTitleUsesFirstUserLine(t *testing.T) {
	got := DeriveTitle([]Msg{
		{Role: "info", Text: "starting Copilot chat"},
		{Role: "user", Text: "  why does the caret drift?\nand also this  "},
		{Role: "agent", Text: "because caretGoalCol"},
	})
	if got != "why does the caret drift?" {
		t.Errorf("DeriveTitle = %q", got)
	}
	if got := DeriveTitle([]Msg{{Role: "agent", Text: "hello"}}); got != "" {
		t.Errorf("DeriveTitle with no user message = %q, want empty", got)
	}
}

// TestDeriveTitleTruncates pins the width cap — a title long enough to
// push the timestamp and project marker off a picker row says less than
// a short one, not more.
func TestDeriveTitleTruncates(t *testing.T) {
	long := strings.Repeat("a", MaxTitle*2)
	got := DeriveTitle([]Msg{{Role: "user", Text: long}})
	if n := len([]rune(got)); n > MaxTitle {
		t.Fatalf("title is %d runes, want <= %d", n, MaxTitle)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated title = %q, want an ellipsis", got)
	}
}

// TestNewIDsSortByTime pins that ids are sortable by construction —
// that's what lets a listing fall back on them when a stamp is missing.
func TestNewIDsSortByTime(t *testing.T) {
	early := NewID(time.Date(2026, 8, 28, 14, 25, 0, 0, time.UTC))
	late := NewID(time.Date(2026, 8, 28, 14, 25, 1, 0, time.UTC))
	if !(early < late) {
		t.Errorf("ids do not sort by time: %q >= %q", early, late)
	}
	// Two conversations started inside the same second must not collide.
	a := NewID(time.Date(2026, 8, 28, 14, 25, 0, 1e6, time.UTC))
	b := NewID(time.Date(2026, 8, 28, 14, 25, 0, 9e6, time.UTC))
	if a == b {
		t.Errorf("sub-second ids collided: %q", a)
	}
}

// TestLoadFillsIDFromFilename pins the recovery path: a hand-edited file
// that lost its id field still opens, because the filename IS the
// identity everything else looks it up by.
func TestLoadFillsIDFromFilename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20260828-142500-000.json")
	if err := os.WriteFile(path, []byte(`{"title":"x","msgs":[{"role":"user","text":"x"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir, "20260828-142500-000")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ID != "20260828-142500-000" {
		t.Errorf("ID = %q, want it filled from the filename", c.ID)
	}
}
