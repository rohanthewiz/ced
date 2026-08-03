// =============================================================================
// File: internal/search/search.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Package search scans a list of files for a query and returns every
// occurrence, in document order. It is the engine behind "Find in
// project"; the UI that shows the results is app/projectsearch.go.
//
// Pure Go rather than shelling out to ripgrep or grep. Both would be
// faster on a huge tree, but neither is present on every machine ced runs
// on, and the whole promise of this editor is one static binary that
// works when it lands. The cost is bounded by three things instead:
//
//   - The file list comes from the FINDER'S INDEX, which is already built
//     from `git ls-files` (or a .gitignore-aware walk). So node_modules,
//     vendor, build output and dot-directories are excluded before the
//     search starts, for free and by the project's own rules.
//   - Files are skipped on the same two grounds a tab refuses to open
//     them: too big, or binary. A grep hit inside a minified bundle is
//     noise, and one inside a .so is nonsense.
//   - Results are capped and the cap is REPORTED. A silent truncation
//     reads as "that's all of them", which is the one wrong answer a
//     search tool can give.
//
// Matching itself is delegated to editor.FindAll, so a project search and
// an in-file search agree on what counts as a hit — case-insensitive
// substring, every occurrence on a line, no overlaps. One matcher, two
// callers; a second implementation here would drift.
package search

import (
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/rohanthewiz/ced/internal/editor"
)

// DefaultLimit caps how many hits a search returns. Ten thousand is far
// past what anyone reads and still small enough to hold and draw without
// thinking about it; a query that hits it is a query that wants
// narrowing, which is what the truncation notice says.
const DefaultLimit = 10000

// DefaultMaxFileBytes is the per-file size ceiling. Well under the
// editor's own open limit: a file this big is generated, and generated
// files are exactly what a project search should not fill up with.
const DefaultMaxFileBytes = 2 << 20

// sniffBytes is how much of a file's head is checked for a NUL before
// deciding it is binary — the same test, and the same reasoning, as the
// editor's open guard.
const sniffBytes = 8 << 10

// Hit is one occurrence: where it is, how wide, and the whole line it
// sits on so the UI can render context without re-reading the file.
type Hit struct {
	Path  string // absolute
	Line  int    // 0-based
	Col   int    // 0-based rune column
	Width int    // rune width of the match
	Text  string // the raw line, untrimmed
}

// Options configures one search. The zero value is usable except for
// Query: Limit and MaxFileBytes fall back to the defaults above.
type Options struct {
	Query        string
	Limit        int
	MaxFileBytes int64
}

// Project searches every file in paths (relative to rootDir) and returns
// the hits in a stable order — by path, then by position — along with
// whether the limit truncated the answer.
//
// Files are read concurrently because this is IO-bound and a project of
// any size is thousands of small reads; the results are re-sorted at the
// end, so the concurrency is invisible to the caller. Errors on
// individual files (deleted between the index and the read, unreadable,
// binary) are SKIPPED rather than reported: one bad file must not cost
// the user the other nine hundred.
func Project(rootDir string, paths []string, opts Options) (hits []Hit, truncated bool) {
	if opts.Query == "" || len(paths) == 0 {
		return nil, false
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	maxBytes := opts.MaxFileBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxFileBytes
	}

	// Bounded fan-out: enough to keep the disk busy, few enough that a
	// search can't exhaust file descriptors on a large tree.
	const workers = 8
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		out  []Hit
		jobs = make(chan string)
	)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rel := range jobs {
				found := searchFile(filepath.Join(rootDir, rel), opts.Query, maxBytes)
				if len(found) == 0 {
					continue
				}
				mu.Lock()
				out = append(out, found...)
				mu.Unlock()
			}
		}()
	}
	for _, p := range paths {
		jobs <- p
	}
	close(jobs)
	wg.Wait()

	// Sorted here rather than by walking paths in order, because the
	// workers finish out of order and a search whose row order depends on
	// disk timing would be maddening to use.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Col < out[j].Col
	})
	if len(out) > limit {
		return out[:limit], true
	}
	return out, false
}

// searchFile returns every hit in one file, or nothing if the file can't
// or shouldn't be searched.
func searchFile(path, query string, maxBytes int64) []Hit {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > maxBytes {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil || looksBinary(data) {
		return nil
	}
	buf := editor.NewBuffer(string(data))
	matches := editor.FindAll(buf, query)
	if len(matches) == 0 {
		return nil
	}
	hits := make([]Hit, 0, len(matches))
	for _, m := range matches {
		if m.Line < 0 || m.Line >= len(buf.Lines) {
			continue
		}
		hits = append(hits, Hit{
			Path: path, Line: m.Line, Col: m.Col, Width: m.Width,
			Text: buf.Lines[m.Line],
		})
	}
	return hits
}

// looksBinary reports whether data's head holds a NUL byte — the same
// one-test sniff the editor's open guard uses, for the same reason: it
// catches executables, archives, images and UTF-16 without a
// content-type table.
func looksBinary(data []byte) bool {
	head := data
	if len(head) > sniffBytes {
		head = head[:sniffBytes]
	}
	for _, b := range head {
		if b == 0 {
			return true
		}
	}
	return false
}
