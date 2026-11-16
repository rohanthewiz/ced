// =============================================================================
// File: internal/spell/userdict.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// Adapted from cats-todo's internal/spell (same author).
// =============================================================================

// The user's own word lists: reading one is Load's business (it takes
// their paths), and this file is the other half — teaching a running
// dictionary a word, and writing that word down so the next launch still
// knows it.
//
// The two go together and are deliberately not one call. Adding to the
// file is what makes the word permanent; adding to the loaded set is what
// makes the underline go away now, without re-reading ninety thousand
// words to learn one. A caller does both, and a caller whose write failed
// does neither, so the list on disk and the list in memory cannot disagree
// about what was accepted.
package spell

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Add teaches the loaded dictionary one word, as if it had been in a user
// dictionary all along — so it is TRUSTED too, and may be a piece of a
// run-together name ("acme" makes "acmeid" pass). It is the in-memory half
// of accepting a word (see AppendWord for the other), and it exists
// because the alternative — reloading — costs the whole gunzip and rebuild
// to learn a single string.
//
// It is the one method that modifies a Dictionary after Load, so it must
// not be called while another goroutine is reading one. ced only reads a
// dictionary on its main loop (decoration, picker), which is also where
// Add runs; a reload builds a fresh Dictionary on a goroutine and hands it
// over whole.
func (d *Dictionary) Add(word string) {
	word = strings.TrimSpace(word)
	if word == "" {
		return
	}
	d.words[word] = struct{}{}
	d.trusted[strings.ToLower(word)] = struct{}{}
}

// dictHeader is written above the first word of a dictionary file this
// package creates. The file is meant to be opened and edited by hand —
// that is the whole point of a plain list — so it says what it is on the
// way in, rather than being a mystery file that appeared in a config
// directory or a repository.
const dictHeader = `# ced dictionary — one word per line; '#' starts a comment.
# Words listed here are never flagged by ced's struct-tag spell check.
`

// AppendWord adds word to the dictionary file at path, creating the file
// (and the directory holding it) if it is not there yet. It is the on-disk
// half of accepting a word.
//
// A word the file already lists is not written twice: the check costs one
// read of a small file, and the alternative is a list that grows a
// duplicate every time the same jargon is accepted in a different session.
// The comparison is case-insensitive because lookup is (see
// Dictionary.has) — a file holding both "Kubernetes" and "kubernetes"
// would be two lines saying one thing.
//
// The word is written exactly as given; ced's caller lowercases an
// identifier piece first, since its capital is the camelCase convention's
// and not the word's.
func AppendWord(path, word string) error {
	word = strings.TrimSpace(word)
	if word == "" {
		return errors.New("no word to add")
	}
	if strings.ContainsAny(word, " \t\n") {
		// A line with a space in it is read back whole and can then never
		// match a word, so it would be an entry that silently does nothing.
		return errors.New("a dictionary word cannot contain spaces")
	}
	if path == "" {
		return errors.New("no dictionary file to add it to")
	}
	listed, needsNewline, err := readDictFile(path)
	if err != nil {
		return err
	}
	if listed[strings.ToLower(word)] {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	var out strings.Builder
	if listed == nil {
		out.WriteString(dictHeader) // a file being created says what it is
	} else if needsNewline {
		// A hand-edited file may end mid-line; appending to that would
		// join the new word onto the last one and lose both.
		out.WriteString("\n")
	}
	out.WriteString(word)
	out.WriteString("\n")
	if _, err := f.WriteString(out.String()); err != nil {
		return err
	}
	return f.Close()
}

// readDictFile reports which words a dictionary file already lists
// (lowercased, nil when the file does not exist yet) and whether its last
// line is unfinished.
func readDictFile(path string) (listed map[string]bool, needsNewline bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	listed = make(map[string]bool)
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		listed[strings.ToLower(line)] = true
	}
	if err := sc.Err(); err != nil {
		return nil, false, err
	}
	return listed, len(data) > 0 && data[len(data)-1] != '\n', nil
}
