// =============================================================================
// File: internal/app/entrycmd_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-08
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/history"
)

// TestExpandEntryTemplate_QuotesAndBothPlaceholders pins the expansion:
// both placeholders, every occurrence, a plain path left bare, and a
// path with shell-active characters single-quoted.
func TestExpandEntryTemplate_QuotesAndBothPlaceholders(t *testing.T) {
	got := expandEntryTemplate("cd {{DIR}} && wc -l {{DIR_ENTRY}} {{DIR_ENTRY}}", "/p/a.go", "/p")
	if want := "cd /p && wc -l /p/a.go /p/a.go"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	evil := "/p/$(rm -rf ~).txt"
	got = expandEntryTemplate("cat {{DIR_ENTRY}}", evil, "/p")
	if want := "cat " + catsShellQuote(evil); got != want {
		t.Fatalf("got %q, want %q — an untrusted name must never reach the shell bare", got, want)
	}
}

// TestExpandEntryTemplate_AlreadyQuotedPlaceholder: a placeholder the
// user wrapped in quotes is replaced quotes and all, rather than getting
// a second pair that would break the path.
func TestExpandEntryTemplate_AlreadyQuotedPlaceholder(t *testing.T) {
	entry := "/p/my file.txt"
	want := "ls " + catsShellQuote(entry)
	for _, tmpl := range []string{`ls "{{DIR_ENTRY}}"`, `ls '{{DIR_ENTRY}}'`, `ls {{DIR_ENTRY}}`} {
		if got := expandEntryTemplate(tmpl, entry, "/p"); got != want {
			t.Fatalf("%s → %q, want %q", tmpl, got, want)
		}
	}
}

// TestEntryCommandLine_AppendsWithoutAPlaceholder pins xargs' convention
// and the {{DIR}} rule for a file (its parent) versus a folder (itself).
func TestEntryCommandLine_AppendsWithoutAPlaceholder(t *testing.T) {
	if got := entryCommandLine("wc -l", "/p/a.go", false); got != "wc -l /p/a.go" {
		t.Fatalf("append: %q", got)
	}
	if got := entryCommandLine("ls {{DIR}}", "/p/a.go", false); got != "ls /p" {
		t.Fatalf("file's DIR: %q", got)
	}
	if got := entryCommandLine("ls {{DIR}}", "/p/pkg", true); got != "ls /p/pkg" {
		t.Fatalf("folder's DIR: %q", got)
	}
}

// TestEntryCmdTitleName_MarksFoldersAndKeepsTheTail pins the title noun.
func TestEntryCmdTitleName_MarksFoldersAndKeepsTheTail(t *testing.T) {
	if got := entryCmdTitleName("/p/pkg", true); got != "pkg/" {
		t.Fatalf("folder: %q", got)
	}
	long := strings.Repeat("x", 40) + ".go"
	got := entryCmdTitleName("/p/"+long, false)
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, ".go") || len([]rune(got)) != entryCmdTitleMax {
		t.Fatalf("long: %q", got)
	}
}

// TestEntryCommand_PromptRunsAndRemembersTheTemplate is the tree row end
// to end: the prompt opens seeded with the example, Enter runs the
// expanded line in ced's panel, and history keeps the TEMPLATE so the
// next prompt offers it for whatever entry is clicked next.
func TestEntryCommand_PromptRunsAndRemembersTheTemplate(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)

	ctxEntryCommand(a, treeNodeFor(t, a, "pkg"))
	p := promptOf(a)
	if p == nil {
		t.Fatal("the row should open a prompt")
	}
	if got := p.field.String(); got != entryCmdExample {
		t.Fatalf("seed = %q, want the example", got)
	}
	if p.histKind != history.EntryCommands {
		t.Fatalf("histKind = %q, want the entry-command list", p.histKind)
	}
	p.submit(a)
	waitTermIdle(t, a)

	want := "ls -la " + sub
	if got := termEvals(t, a); len(got) != 1 || got[0] != want {
		t.Fatalf("evals = %q, want [%q]", got, want)
	}
	if got := a.searchHistory(history.EntryCommands); len(got) == 0 || got[0] != entryCmdExample {
		t.Fatalf("history = %q, want the template first", got)
	}
	if got := a.entryCmdSeed(); got != entryCmdExample {
		t.Fatalf("next seed = %q", got)
	}
}

// TestRunEntryCommand_BusyPanelStages: a running command would refuse
// the submit, so the expansion is staged rather than lost.
func TestRunEntryCommand_BusyPanelStages(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	a.focusTermPanel()
	a.term.running = true

	a.runEntryCommand("du -sh", root, true)

	if got := a.term.input.String(); got != "du -sh "+shellArg(a.rootDir) {
		t.Fatalf("input = %q, want it staged", got)
	}
	if !strings.Contains(a.statusMsg, "busy") {
		t.Fatalf("statusMsg = %q", a.statusMsg)
	}
}

// TestEntryCommandLabel_NamesTheTarget pins the ≡ row label: the file
// when one is open, the project otherwise.
func TestEntryCommandLabel_NamesTheTarget(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a.go")
	if err := os.WriteFile(file, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	if got := a.entryCommandLabel(); got != "Shell command on project…" {
		t.Fatalf("no tab: %q", got)
	}
	a.openFile(file)
	if got := a.entryCommandLabel(); got != "Shell command on file…" {
		t.Fatalf("with tab: %q", got)
	}
}
