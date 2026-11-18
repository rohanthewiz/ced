// =============================================================================
// File: internal/child/child_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package child

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestGitEnv_SetsPromptOffAndDropsGPGTTY pins the write-side environment:
// prompts off, GPG_TTY gone, a user's own GIT_TERMINAL_PROMPT replaced
// rather than doubled, and optional locks left alone on a write.
func TestGitEnv_SetsPromptOffAndDropsGPGTTY(t *testing.T) {
	base := []string{"PATH=/bin", "GPG_TTY=/dev/ttys004", "GIT_TERMINAL_PROMPT=1", "HOME=/h"}
	env := GitEnv(base, false)
	if slices.Contains(env, "GPG_TTY=/dev/ttys004") {
		t.Error("GPG_TTY survived")
	}
	if n := countPrefix(env, "GIT_TERMINAL_PROMPT="); n != 1 || !slices.Contains(env, "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("GIT_TERMINAL_PROMPT: %v", env)
	}
	if countPrefix(env, "GIT_OPTIONAL_LOCKS=") != 0 {
		t.Error("a write asked for optional locks off")
	}
	if !slices.Contains(env, "PATH=/bin") || !slices.Contains(env, "HOME=/h") {
		t.Errorf("unrelated variables lost: %v", env)
	}
}

// TestGitEnv_ReadTurnsOptionalLocksOff pins the read side: exactly one
// GIT_OPTIONAL_LOCKS, set to 0, even over a user's own value.
func TestGitEnv_ReadTurnsOptionalLocksOff(t *testing.T) {
	env := GitEnv([]string{"GIT_OPTIONAL_LOCKS=1"}, true)
	if countPrefix(env, "GIT_OPTIONAL_LOCKS=") != 1 || !slices.Contains(env, "GIT_OPTIONAL_LOCKS=0") {
		t.Errorf("GIT_OPTIONAL_LOCKS: %v", env)
	}
}

// TestGitEnv_NilBaseInheritsTheProcessEnvironment pins the exec.Cmd
// convention: nil means "ours", not "empty" — a git with no PATH or HOME
// would fail in confusing ways.
func TestGitEnv_NilBaseInheritsTheProcessEnvironment(t *testing.T) {
	t.Setenv("CED_CHILD_PROBE", "yes")
	if !slices.Contains(GitEnv(nil, false), "CED_CHILD_PROBE=yes") {
		t.Error("nil base did not inherit the process environment")
	}
}

// TestDetach_ChildHasNoControllingTerminal runs a shell that tries to open
// /dev/tty — the door every prompt in the package comment goes through —
// and expects it to fail. Without Setsid the child would inherit the test
// runner's terminal whenever `go test` runs in one.
func TestDetach_ChildHasNoControllingTerminal(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "if : < /dev/tty 2>/dev/null; then echo tty; else echo none; fi")
	Detach(cmd)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "none" {
		t.Errorf("child could open /dev/tty (%q)", got)
	}
}

// TestDetach_DeadlineEndsTheWholeGroup is the hang the package exists
// for: a shell whose background grandchild keeps the output pipe open.
// Killing only `sh` would leave Output waiting for the grandchild's
// sleep; the group signal (and WaitDelay behind it) must return well
// inside that.
func TestDetach_DeadlineEndsTheWholeGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", "sleep 30 & sleep 30")
	Detach(cmd)
	start := time.Now()
	_, err := cmd.Output()
	if err == nil {
		t.Fatal("a cancelled child reported success")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Output returned after %s; the group survived the deadline", d)
	}
}

// TestGit_BuildsADetachedGitCommand is the smoke test for the composed
// constructor: argv, environment and the detach wiring all present.
func TestGit_BuildsADetachedGitCommand(t *testing.T) {
	cmd := Git(context.Background(), []string{"PATH=/bin"}, true, "-C", "/x", "status")
	if want := []string{"git", "-C", "/x", "status"}; !slices.Equal(cmd.Args, want) {
		t.Errorf("args = %v, want %v", cmd.Args, want)
	}
	if !slices.Contains(cmd.Env, "GIT_OPTIONAL_LOCKS=0") || !slices.Contains(cmd.Env, "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("env = %v", cmd.Env)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid || cmd.Cancel == nil || cmd.WaitDelay != WaitDelay {
		t.Error("Git did not detach the command")
	}
}

// countPrefix counts the entries of env starting with prefix.
func countPrefix(env []string, prefix string) int {
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			n++
		}
	}
	return n
}
