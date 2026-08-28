// =============================================================================
// File: internal/chatstore/chatstore.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Package chatstore persists the chat panel's conversations: one JSON
// file per conversation under ~/.config/ced/chats, newest-first listing,
// and a cap that trims the oldest away.
//
// WHY A DIRECTORY, NOT A KEY IN state.json. A conversation is a
// DOCUMENT — hundreds of messages, some of them long — while state.json
// is a small index rewritten on every folder switch. Folding transcripts
// into it would make each of those rewrites proportional to how much the
// user has chatted, and one corrupt write would cost the tab list too.
// One file per conversation is also what makes "keep the last few" a
// trim of the oldest files rather than a rewrite of a growing blob, and
// what lets a user delete a single conversation with rm. Same reasoning
// the themes and plugins directories are built on; this package owns the
// schema, userconfig owns only the path.
//
// WHY THE TRANSCRIPT AND NOT THE SESSION. What is stored here is what
// the user SAW — the panel's own message list. The agent's server-side
// memory lives in the ACP session and dies with the process, so a
// restored conversation is a reading surface, not a resumed one; the app
// layer says so in the panel when it loads one. Storing an agent session
// id here would be storing a handle that is already invalid by the time
// anybody could use it.
//
// The store is stateless: every function takes the directory, so the app
// layer can point it at a temp dir in tests without a global.
package chatstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// MaxConversations caps how many conversations the directory keeps.
	// A trim from the OLDEST, the recency-queue shape session.MaxEntries
	// uses: the feature is "the last few chats", so an unbounded archive
	// would be a slowly growing directory nobody asked for and a picker
	// nobody can read. Generous enough that a week of work survives it.
	MaxConversations = 30

	// MaxTitle bounds a derived title's runes. The picker row carries a
	// timestamp and a project marker beside it, so a title long enough to
	// push those off the row costs more than it says.
	MaxTitle = 72

	// ext is the file suffix; anything else in the directory is ignored,
	// so a user's own notes beside the archive cost nothing.
	ext = ".json"
)

// Msg is one archived transcript entry. Role is the app layer's role
// spelled as a string ("user", "agent", "tool", "info") rather than its
// integer: an archive outlives the enum, and a reordered constant block
// must not silently restyle every stored conversation.
type Msg struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// Conversation is one archived chat panel session.
type Conversation struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Agent   string    `json:"agent,omitempty"`
	Model   string    `json:"model,omitempty"`
	Root    string    `json:"root,omitempty"`
	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
	Msgs    []Msg     `json:"msgs"`
}

// Meta is the listing view: everything a picker row needs, without the
// transcript. List still reads whole files (they are small and few, and
// a partial JSON parse would need a second schema to stay honest), but
// returning only this keeps the caller from holding every conversation
// in memory to draw a list of titles.
type Meta struct {
	ID      string
	Title   string
	Agent   string
	Root    string
	Updated time.Time
	Count   int // messages, so a row can say how much is in there
}

// NewID mints a conversation id from a wall-clock instant. Sortable by
// construction, which is what lets a listing fall back on filename order
// when a file's own Updated stamp is unreadable, and human-legible so a
// user browsing the directory can find the one they want.
//
// The tail is the full NANOSECOND field, not a rounded one. The ids two
// consecutive conversations get are minted microseconds apart — clearing
// a chat saves the old one and immediately mints the new one's id — so a
// millisecond tail collides there, and a collision means the second
// conversation silently overwrites the first's file. Sub-nanosecond
// spacing is not reachable: a save writes a file between the two calls.
func NewID(now time.Time) string {
	return fmt.Sprintf("%s-%09d", now.Format("20060102-150405"), now.Nanosecond())
}

// DeriveTitle picks the label a conversation is remembered by: the first
// line of the first thing the USER said. Not the agent's answer — the
// question is what the user is scanning for, and an answer's opening
// line is usually a restatement of it or a preamble. Returns "" when
// there is nothing to name it by; the caller decides what an unnamed
// conversation reads as.
func DeriveTitle(msgs []Msg) string {
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		line := strings.TrimSpace(m.Text)
		if i := strings.IndexAny(line, "\r\n"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		r := []rune(line)
		if len(r) > MaxTitle {
			return strings.TrimSpace(string(r[:MaxTitle-1])) + "…"
		}
		return line
	}
	return ""
}

// Save writes c into dir as <id>.json and prunes the directory back to
// MaxConversations. Rewriting the same id is the normal case — the app
// re-saves the live conversation after every turn — so this is a
// temp-file + rename, the same atomicity fileio.Save buys: a crash
// mid-write must not leave a half-written transcript where a readable
// one used to be.
//
// An empty id or an empty message list is a no-op rather than an error:
// the caller archives unconditionally at teardown, and "there was no
// conversation" is not a failure worth reporting.
func Save(dir string, c Conversation) error {
	if dir == "" || c.ID == "" || len(c.Msgs) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	out, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, c.ID+ext)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	prune(dir)
	return nil
}

// List returns the archived conversations newest first. A file that
// won't parse costs itself and nothing else (the theme-registry rule):
// one hand-edited or truncated transcript must not take the whole
// picker down with it. A missing directory is the common case for a
// user who has never chatted, and reports no error at all.
func List(dir string) ([]Meta, error) {
	if dir == "" {
		return nil, nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var out []Meta
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		c, err := readFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, Meta{
			ID: c.ID, Title: c.Title, Agent: c.Agent,
			Root: c.Root, Updated: c.Updated, Count: len(c.Msgs),
		})
	}
	sortNewestFirst(out)
	return out, nil
}

// Load reads one conversation by id.
func Load(dir, id string) (Conversation, error) {
	if dir == "" || id == "" {
		return Conversation{}, errors.New("no conversation named")
	}
	// The id becomes a filename, so it must not be able to reach out of
	// the archive. Ids are minted by NewID, but a picker row's id makes
	// a round trip through the app layer and the file it came from.
	if id != filepath.Base(id) {
		return Conversation{}, fmt.Errorf("bad conversation id %q", id)
	}
	return readFile(filepath.Join(dir, id+ext))
}

// Remove deletes one conversation. A missing file is success — the
// caller wanted it gone.
func Remove(dir, id string) error {
	if dir == "" || id == "" || id != filepath.Base(id) {
		return nil
	}
	err := os.Remove(filepath.Join(dir, id+ext))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// readFile parses one archive file, filling in an id from the filename
// when the stored one is missing — the id IS the file's identity, and a
// conversation that can't name itself can't be reopened or overwritten.
func readFile(path string) (Conversation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Conversation{}, err
	}
	var c Conversation
	if err := json.Unmarshal(data, &c); err != nil {
		return Conversation{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.ID == "" {
		c.ID = strings.TrimSuffix(filepath.Base(path), ext)
	}
	return c, nil
}

// sortNewestFirst orders by Updated, falling back to the id — which is
// a timestamp by construction — so conversations with an unreadable or
// zero stamp still land in a sensible place instead of clumping at one
// end of the list.
func sortNewestFirst(ms []Meta) {
	sort.SliceStable(ms, func(i, j int) bool {
		if !ms[i].Updated.Equal(ms[j].Updated) {
			return ms[i].Updated.After(ms[j].Updated)
		}
		return ms[i].ID > ms[j].ID
	})
}

// prune trims the directory back to MaxConversations, oldest first.
// Best-effort by design: a failure to delete an old conversation must
// never fail the save of a new one, which is the write the user's work
// is actually in.
func prune(dir string) {
	metas, err := List(dir)
	if err != nil || len(metas) <= MaxConversations {
		return
	}
	for _, m := range metas[MaxConversations:] {
		_ = Remove(dir, m.ID)
	}
}
