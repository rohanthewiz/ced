// =============================================================================
// File: internal/search/search_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package search

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedTree writes files (name → contents) under a fresh temp dir and
// returns the root plus the relative paths, in the order given.
func seedTree(t *testing.T, files map[string]string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	var paths []string
	for name, body := range files {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, name)
	}
	return root, paths
}

// TestProject_FindsEveryOccurrenceInDocumentOrder pins the core contract:
// every hit, including several on one line, ordered by path then position
// so the row order can never depend on which worker finished first.
func TestProject_FindsEveryOccurrenceInDocumentOrder(t *testing.T) {
	root, paths := seedTree(t, map[string]string{
		"a.go":        "package a\n\nfunc target() {}\n",
		"sub/b.go":    "target target\n",
		"sub/c.go":    "nothing here\n",
		"zzz/last.go": "// target at the end\n",
	})

	hits, truncated := Project(root, paths, Options{Query: "target"})
	if truncated {
		t.Fatal("four small files must not truncate")
	}
	if len(hits) != 4 {
		t.Fatalf("expected 4 hits, got %d: %+v", len(hits), hits)
	}
	want := []string{"a.go", "sub/b.go", "sub/b.go", "zzz/last.go"}
	for i, h := range hits {
		if rel, _ := filepath.Rel(root, h.Path); rel != want[i] {
			t.Fatalf("hit %d from %s, want %s", i, rel, want[i])
		}
	}
	// The two hits on one line must be ordered by column.
	if hits[1].Col != 0 || hits[2].Col != 7 {
		t.Fatalf("same-line hits out of order: %d then %d", hits[1].Col, hits[2].Col)
	}
	if hits[0].Text != "func target() {}" {
		t.Fatalf("hit should carry its whole line, got %q", hits[0].Text)
	}
}

// TestProject_CaseInsensitiveLikeTheInFileSearch: the two scopes share
// editor.FindAll precisely so they can't disagree about what a hit is.
func TestProject_CaseInsensitiveLikeTheInFileSearch(t *testing.T) {
	root, paths := seedTree(t, map[string]string{"a.txt": "Hello HELLO hello\n"})
	hits, _ := Project(root, paths, Options{Query: "hello"})
	if len(hits) != 3 {
		t.Fatalf("expected 3 case-insensitive hits, got %d", len(hits))
	}
}

// TestProject_SkipsBinaryAndOversizeFiles keeps the result list free of
// the two kinds of file a hit in is worthless: a compiled artifact and a
// generated bundle.
func TestProject_SkipsBinaryAndOversizeFiles(t *testing.T) {
	root, paths := seedTree(t, map[string]string{
		"real.go":  "needle\n",
		"blob.bin": "\x00\x01needle\n",
		"big.js":   "needle" + strings.Repeat(" ", 4096),
	})

	hits, _ := Project(root, paths, Options{Query: "needle", MaxFileBytes: 1024})
	if len(hits) != 1 {
		t.Fatalf("expected only the real source file, got %d hits: %+v", len(hits), hits)
	}
	if !strings.HasSuffix(hits[0].Path, "real.go") {
		t.Fatalf("wrong file survived: %s", hits[0].Path)
	}
}

// TestProject_TruncationIsReported is the one wrong answer a search tool
// can give: a short list that looks complete.
func TestProject_TruncationIsReported(t *testing.T) {
	body := strings.Repeat("needle\n", 50)
	root, paths := seedTree(t, map[string]string{"many.txt": body})

	hits, truncated := Project(root, paths, Options{Query: "needle", Limit: 10})
	if !truncated {
		t.Fatal("hitting the limit must be reported")
	}
	if len(hits) != 10 {
		t.Fatalf("expected the limit's worth of hits, got %d", len(hits))
	}

	hits, truncated = Project(root, paths, Options{Query: "needle", Limit: 500})
	if truncated || len(hits) != 50 {
		t.Fatalf("under the limit: truncated=%v hits=%d", truncated, len(hits))
	}
}

// TestProject_MissingFileCostsOnlyItself — the index is a snapshot, so a
// file can be deleted between building it and searching it. That must
// cost that file and nothing else.
func TestProject_MissingFileCostsOnlyItself(t *testing.T) {
	root, paths := seedTree(t, map[string]string{"a.go": "needle\n", "b.go": "needle\n"})
	if err := os.Remove(filepath.Join(root, "a.go")); err != nil {
		t.Fatal(err)
	}
	paths = append(paths, "never/existed.go")

	hits, _ := Project(root, paths, Options{Query: "needle"})
	if len(hits) != 1 {
		t.Fatalf("expected the surviving file's hit only, got %d", len(hits))
	}
}

// TestProject_EmptyInputsReturnNothing guards the trivial guards — an
// empty query especially, which editor.FindAll would answer with nil but
// which should never have got that far.
func TestProject_EmptyInputsReturnNothing(t *testing.T) {
	root, paths := seedTree(t, map[string]string{"a.go": "needle\n"})
	if hits, _ := Project(root, paths, Options{Query: ""}); hits != nil {
		t.Fatal("an empty query must return nothing")
	}
	if hits, _ := Project(root, nil, Options{Query: "needle"}); hits != nil {
		t.Fatal("an empty file list must return nothing")
	}
	_ = root
}

// TestProject_ManyFilesStayOrdered exercises the worker pool: with more
// files than workers, the sort is what makes the output deterministic,
// and a missing sort would show up here as a flake rather than a failure.
func TestProject_ManyFilesStayOrdered(t *testing.T) {
	files := make(map[string]string, 64)
	for i := range 64 {
		files[fmt.Sprintf("f%02d.txt", i)] = "needle\n"
	}
	root, paths := seedTree(t, files)

	for range 5 {
		hits, _ := Project(root, paths, Options{Query: "needle"})
		if len(hits) != 64 {
			t.Fatalf("expected 64 hits, got %d", len(hits))
		}
		for i := 1; i < len(hits); i++ {
			if hits[i-1].Path >= hits[i].Path {
				t.Fatalf("results out of order at %d: %s then %s", i, hits[i-1].Path, hits[i].Path)
			}
		}
	}
}
