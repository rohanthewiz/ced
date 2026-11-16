// =============================================================================
// File: internal/spell/spell.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// Adapted from cats-todo's internal/spell (same author), where it checks
// the prompt editor's prose.
// =============================================================================

// Package spell is the word list behind ced's struct-tag spell check: a
// set of known English words, the rules for deciding whether a piece of
// an identifier is one of them, and ranked corrections when it is not.
//
// It is deliberately a CHECKER, not a speller. There are no affix rules
// and no language model — just "is this word in the list", asked of the
// parts of a tag name the identifier splitter (ident.go) decides are
// words. All of the judgement is in that split and in which tags are read
// at all (structtag.go); this file is only the list and the lookup.
//
// The word list is SCOWL's en_US (size 60) expanded from its hunspell
// affix rules into a flat list — about 90k forms, 250 KB gzipped, embedded
// in the binary — plus two curated lists:
//
//   - extra.txt, the everyday vocabulary of software work that a
//     general-purpose English list leaves out (json, goroutine, refactor…),
//     shared with cats-todo;
//   - ident.txt, the words that only make sense inside identifiers — the
//     abbreviations (addr, cfg, ctx), struct-tag options (omitempty) and
//     short connectors (at, by, id) that tag names are built from.
//
// All of it is embedded so the check works the same on every machine and
// needs nothing installed — the property ced's validator has too. A
// system dictionary would have made the feature Unix-only and, on macOS,
// considerably noisier (its web2 list is headwords only: no plurals).
//
// # Two tiers of "known"
//
//	words   ─ everything above + the user's dictionaries   → Known
//	trusted ─ ident.txt + the user's dictionaries          → compound parts
//
// A word in either set is spelled correctly. The second set exists for
// the RUN-TOGETHER name ("createdat", "apikey"): splitting one into known
// pieces is what lets it pass, and SCOWL is full of three-letter words
// (pas, con, sep, rec) that would let a typo split too — "pasword" is
// "pas" + "word". So a compound piece must be a list word of four letters
// or more, or a word someone chose on purpose: the curated identifier list
// or the user's own dictionary. See ident.go.
package spell

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

//go:embed en_US.txt.gz
var enUS []byte

//go:embed extra.txt
var extra []byte

//go:embed ident.txt
var ident []byte

// Dictionary is a set of known words. It is safe for concurrent reads once
// built; Add is the one mutator and must not race a reader (see Add).
type Dictionary struct {
	// words is every spelling the check accepts, as listed (case kept, so
	// a proper noun stays capitalized for Suggest to hand back).
	words map[string]struct{}
	// trusted is the subset that may be a PIECE of a run-together name
	// whatever its length, keyed lowercase. See the package comment.
	trusted map[string]struct{}
}

// Load builds the dictionary from the embedded lists plus each of
// userFiles that exists. The files are one word per line, '#' comments and
// blank lines ignored — the same shape as extra.txt, so a user can grow
// one in an editor.
//
// A missing user file is not an error: it is the normal state of a fresh
// install. A file that exists but cannot be read is skipped and REPORTED:
// the dictionary still comes back (the embedded lists are what make the
// check useful at all, and an editor feature must degrade rather than
// vanish), with the error alongside so the caller can hold it and say why
// a word the user added is still flagged. This is where ced parts from
// cats-todo, whose Load refuses outright.
func Load(userFiles ...string) (*Dictionary, error) {
	d := newEmpty(100_000)

	zr, err := gzip.NewReader(bytes.NewReader(enUS))
	if err != nil {
		return nil, err
	}
	if err := d.addFrom(zr, false); err != nil {
		return nil, err
	}
	if err := d.addFrom(bytes.NewReader(extra), false); err != nil {
		return nil, err
	}
	if err := d.addFrom(bytes.NewReader(ident), true); err != nil {
		return nil, err
	}

	var errs []error
	for _, path := range userFiles {
		if path == "" {
			continue
		}
		if err := d.addFile(path); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
		}
	}
	return d, errors.Join(errs...)
}

// New builds a dictionary holding exactly words and none of the embedded
// lists. It is for tests that want to say precisely which words are known
// without paying for the 90k-word gunzip; the words are plain list words,
// not trusted ones (Add them for that).
func New(words ...string) *Dictionary {
	d := newEmpty(len(words))
	for _, w := range words {
		if w = strings.TrimSpace(w); w != "" {
			d.words[w] = struct{}{}
		}
	}
	return d
}

// newEmpty allocates the two sets, sized for the expected word count.
func newEmpty(size int) *Dictionary {
	return &Dictionary{
		words:   make(map[string]struct{}, size),
		trusted: make(map[string]struct{}),
	}
}

// addFile reads one user dictionary into both sets. A missing file is
// silently fine (see Load); the error comes back only for a file that is
// there and cannot be read.
func (d *Dictionary) addFile(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	return d.addFrom(f, true)
}

// addFrom reads one word per line into the set, and into the trusted set
// as well when trust is true. Comments and surrounding space are dropped;
// a "word" with spaces inside is kept whole and will simply never match,
// which is harmless.
func (d *Dictionary) addFrom(r io.Reader, trust bool) error {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		d.words[line] = struct{}{}
		if trust {
			d.trusted[strings.ToLower(line)] = struct{}{}
		}
	}
	return sc.Err()
}

// Len is how many words the dictionary holds.
func (d *Dictionary) Len() int { return len(d.words) }

// Known reports whether word is in the dictionary, allowing for the ways a
// correctly spelled word differs from its listed form:
//
//   - a capital the identifier convention put there ("Name" in userName),
//   - a proper noun written lowercase ("english", "monday") — the list
//     has these capitalized, and flagging the case would be flagging
//     style, which is not this checker's business,
//   - a possessive or a typographic apostrophe, which a tag name never
//     holds but Suggest (shared with prose) still folds.
func (d *Dictionary) Known(word string) bool {
	if word == "" {
		return false
	}
	word = strings.ReplaceAll(word, "’", "'")
	if d.has(word) {
		return true
	}
	if base, ok := strings.CutSuffix(word, "'s"); ok && d.has(base) {
		return true
	}
	return false
}

// has is the case-lenient lookup Known describes: as written, then
// lowercased, then with just the first letter capitalized.
func (d *Dictionary) has(w string) bool {
	if _, ok := d.words[w]; ok {
		return true
	}
	lower := strings.ToLower(w)
	if lower != w {
		if _, ok := d.words[lower]; ok {
			return true
		}
	}
	// Title-case is built from the lowercase form so "aARON" does not
	// sneak through as "Aaron".
	first, size := utf8.DecodeRuneInString(lower)
	title := string(unicode.ToUpper(first)) + lower[size:]
	if title != w {
		if _, ok := d.words[title]; ok {
			return true
		}
	}
	return false
}

// isTrusted reports whether w (any case) is in the trusted tier — a word
// that may be a piece of a run-together name at any length.
func (d *Dictionary) isTrusted(w string) bool {
	_, ok := d.trusted[strings.ToLower(w)]
	return ok
}
