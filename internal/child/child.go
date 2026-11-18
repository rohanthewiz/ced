// =============================================================================
// File: internal/child/child.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Package child builds the processes ced runs on the user's behalf — git,
// formatters, plugins, custom actions — so that none of them can reach
// the terminal ced is drawing on, and so that a deadline really ends
// them (N-122, N-126).
//
// The two failure modes it exists for, both reproduced before the fix:
//
//  1. A child that opens /dev/tty draws over the frame and steals keys.
//     git asks `Username for …:` on an HTTPS remote with no cached
//     credentials; ssh asks for a passphrase or a host-key confirmation;
//     pinentry-curses asks for a signing passphrase. All of them open
//     the CONTROLLING terminal directly, so redirecting stdin/stdout does
//     not help. Under a pty against a 401 server git wrote its prompt
//     over the frame, waited forever, and the user's keystrokes were
//     split between tcell and git.
//
//  2. A deadline that kills only the direct child leaves the wait hung.
//     `git` over HTTPS forks `git remote-http`; `sh -c` forks the user's
//     pipeline. Killing the parent leaves the grandchild holding the
//     write end of the output pipe, so CombinedOutput never returns.
//
// The fix, applied to every command by Detach:
//
//	Setsid ──────────► new session, no controlling tty: /dev/tty opens
//	                   fail, so prompts become errors that land in the
//	                   caller's "failed" modal instead of on screen.
//	                   Also a new process GROUP (pgid == pid).
//	Cancel ──────────► on ctx done: SIGTERM to the whole group (-pid),
//	                   so helpers die with their parent. TERM first, not
//	                   KILL: git removes its lock files on TERM.
//	WaitDelay ───────► if the group lingers (or a grandchild escaped it
//	                   and holds the pipe), Go kills the leader and
//	                   closes the pipes after this delay, so Wait always
//	                   returns.
//
// The git environment (GitEnv) closes the rest of the door: prompts off
// even where a helper could find a terminal some other way, GPG_TTY
// dropped so gpg-agent never aims pinentry-curses at ced's tty, and
// optional locks off on READS so the background `git status` never
// takes index.lock under a git command the user runs in another pane.
//
// POSIX-only, like the rest of ced (the terminal panel's grsh already
// rules out a Windows build).
package child

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// WaitDelay is how long a cancelled child's group gets to exit on SIGTERM
// before Go kills the leader and closes its pipes. Five seconds covers a
// git that is cleaning up lock files; anything slower is stuck.
const WaitDelay = 5 * time.Second

// Detach applies the session / group / deadline wiring described in the
// package comment to cmd. Call it before Start. cmd MUST come from
// exec.CommandContext: exec refuses to start a command that carries a
// Cancel hook but no context (pass context.Background() when nothing
// should ever cancel it — the session and WaitDelay still apply).
func Detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	cmd.Cancel = func() error { return signalGroup(cmd, syscall.SIGTERM) }
	cmd.WaitDelay = WaitDelay
}

// signalGroup sends sig to cmd's whole process group. Setsid made the
// child its group's leader, so the group id is its pid and -pid
// addresses every process in it. A group that is already gone reports
// os.ErrProcessDone, which exec.Cmd treats as "nothing to cancel" rather
// than as the command's error.
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// GitEnv returns the environment for a git child. base nil means ced's
// own environment (the exec.Cmd convention: setting Env at all REPLACES
// the inherited one, so a caller's extra variables arrive already merged
// with os.Environ — see gitNoEditorEnv in internal/app). read marks a
// command that changes nothing in the repository.
//
// Variables we set are first REMOVED from base, so a value the user
// exported in their shell profile can't shadow ours (the first
// occurrence wins for some readers, the last for others — exec.Cmd
// dedupes keeping the last, but a reader of the raw environ may not).
func GitEnv(base []string, read bool) []string {
	if base == nil {
		base = os.Environ()
	}
	env := make([]string, 0, len(base)+2)
	for _, kv := range base {
		switch {
		case strings.HasPrefix(kv, "GPG_TTY="),
			strings.HasPrefix(kv, "GIT_TERMINAL_PROMPT="),
			read && strings.HasPrefix(kv, "GIT_OPTIONAL_LOCKS="):
			continue
		}
		env = append(env, kv)
	}
	// GIT_TERMINAL_PROMPT=0 makes git fail a credential request rather
	// than ask. Credential helpers (osxkeychain, a cache, an askpass GUI)
	// still run: only the TERMINAL fallback is off.
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	if read {
		// Without it, `git status` refreshes the index and writes it back
		// opportunistically, taking index.lock — and a user running
		// `git commit` in another pane at that instant gets "Unable to
		// create index.lock: File exists".
		env = append(env, "GIT_OPTIONAL_LOCKS=0")
	}
	return env
}

// Git builds a detached git child bound to ctx: GitEnv over env, then
// Detach. The caller owns ctx (and so the deadline) and runs the command
// the usual way (Output, CombinedOutput, Run, or Start + Wait).
func Git(ctx context.Context, env []string, read bool, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = GitEnv(env, read)
	Detach(cmd)
	return cmd
}
