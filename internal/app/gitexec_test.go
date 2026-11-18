// =============================================================================
// File: internal/app/gitexec_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/child"
)

// TestGitChildrenUseTheConstructor pins N-122's "one constructor": no
// source file outside internal/child builds a git child by hand, so none
// can miss the session / prompt / deadline wiring. Tests are exempt —
// their fixtures run git directly on purpose.
func TestGitChildrenUseTheConstructor(t *testing.T) {
	handBuilt := regexp.MustCompile(`exec\.Command(Context)?\([^)]*"git"`)
	for _, dir := range []string{".", "../finder"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
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
				if strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue
				}
				if handBuilt.MatchString(line) {
					t.Errorf("%s:%d builds a git child by hand — use gitReadOutput / child.Git: %s",
						f, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestEndReadChildren_CancelsReadsNotWrites pins the two lifetimes: Close
// ends the read context and installs a fresh one, and a write's context
// is untouched by it.
func TestEndReadChildren_CancelsReadsNotWrites(t *testing.T) {
	read := gitReadLife()
	write, cancel := gitWriteCtx()
	defer cancel()
	endReadChildren()
	if read.Err() == nil {
		t.Error("endReadChildren left the read context live")
	}
	if write.Err() != nil {
		t.Error("endReadChildren cancelled a write")
	}
	if gitReadLife().Err() != nil {
		t.Error("the next read lifetime starts out cancelled")
	}
}

// TestEndReadChildren_EndsARunningRead runs a long child under a read
// deadline and expects Close's cancel to end it promptly — the group
// signal, not the two-minute deadline.
func TestEndReadChildren_EndsARunningRead(t *testing.T) {
	ctx, cancel := gitReadCtx()
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", "sleep 30 & sleep 30")
	child.Detach(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	endReadChildren()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a read child outlived endReadChildren")
	}
}

// TestGitReadOutput_RunsGit is the happy path for the read helpers.
func TestGitReadOutput_RunsGit(t *testing.T) {
	dir := initRepo(t)
	out, err := gitReadOutput("-C", dir, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		t.Fatalf("rev-parse: %q, %v", out, err)
	}
	if err := gitReadRun("-C", dir, "rev-parse", "--verify", "--quiet", "no-such-ref"); err == nil {
		t.Error("gitReadRun reported success for a missing ref")
	}
}

// TestRunGitCmd_CredentialPromptFailsIntoTheModal reproduces N-122's
// original report against a local server that answers 401: git used to
// write `Username for …:` over the frame and wait forever. Now the
// prompt is refused, the command fails fast, and git's reason arrives in
// the done-event the "Git failed" modal shows.
func TestRunGitCmd_CredentialPromptFailsIntoTheModal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	dir := initRepo(t)
	a := newTestApp(t, dir)
	var got *gitCmdDoneEvent
	// An empty credential.helper list clears any helper the machine has
	// configured globally, so the terminal prompt is git's only option.
	a.runGitCmdHook("Fetch", func(_ *App, e *gitCmdDoneEvent) bool {
		got = e
		return true
	}, "-c", "credential.helper=", "ls-remote", srv.URL+"/repo.git")
	pumpAppEvents(t, a, func() bool { return got != nil })
	if got.err == nil {
		t.Fatal("ls-remote against a 401 succeeded")
	}
	if !strings.Contains(string(got.output), "terminal prompts disabled") {
		t.Errorf("output does not name the refused prompt:\n%s", got.output)
	}
}
