// =============================================================================
// File: internal/app/gitexec.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitexec.go is where every git child ced starts gets its deadline and
// its lifetime (N-122). The process wiring itself — no controlling
// terminal, prompts off, a group kill — lives in internal/child; this
// file decides HOW LONG each kind of command may run and WHO may end it.
//
// Two kinds, two policies:
//
//	READS  (status, log, diff, blame, rev-parse, ls-remote …)
//	  deadline gitReadTimeout; GIT_OPTIONAL_LOCKS=0; ended by App.Close.
//	  A read writes nothing, and once the App is gone its answer has
//	  nowhere to land, so killing it at exit costs nothing and stops a
//	  hung one outliving the editor (Setsid takes it off the session,
//	  so a terminal hangup no longer reaches it).
//
//	WRITES (commit, push, pull, checkout, apply, cherry-pick …)
//	  deadline gitWriteTimeout; NOT ended by Close. A write killed
//	  mid-way can leave index.lock behind or a half-done operation,
//	  which is worse than letting it finish after ced exits (it is in
//	  its own session, so it does). The deadline is generous because a
//	  push of a big branch or a pre-commit hook running a test suite is
//	  legitimately slow; what it catches is a child that will NEVER
//	  finish.
//
// Call sites never build `exec.Command("git", …)` themselves:
// TestGitChildrenUseTheConstructor pins that across the package.
package app

import (
	"context"
	"sync"
	"time"

	"github.com/rohanthewiz/ced/internal/child"
)

const (
	// gitReadTimeout bounds a read. The slowest reads in practice are a
	// pickaxe log filter and a whole-file blame of a long, old file
	// (seconds); two minutes is far past both and still ends a git
	// stuck on a dead network mount.
	gitReadTimeout = 2 * time.Minute
	// gitWriteTimeout bounds a write — see the file comment for why it
	// is long.
	gitWriteTimeout = 15 * time.Minute
)

// readLife is the context every READ child descends from. App.Close
// cancels it (endReadChildren) and installs a fresh one, because a
// folder switch builds a new App in the same process and its reads must
// not start out cancelled. Package-level rather than per-App because the
// read loaders (loadGitStatus and friends) are plain functions of a root
// directory, called from goroutines that hold no App.
var readLife = struct {
	sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
}{}

// gitReadLife returns the live read context, creating it on first use.
func gitReadLife() context.Context {
	readLife.Lock()
	defer readLife.Unlock()
	if readLife.ctx == nil {
		readLife.ctx, readLife.cancel = context.WithCancel(context.Background())
	}
	return readLife.ctx
}

// endReadChildren cancels every in-flight read child (SIGTERM to its
// group, see internal/child) and starts a fresh read lifetime for
// whatever runs next. Called from App.Close.
func endReadChildren() {
	readLife.Lock()
	defer readLife.Unlock()
	if readLife.cancel != nil {
		readLife.cancel()
	}
	readLife.ctx, readLife.cancel = context.WithCancel(context.Background())
}

// gitReadCtx is the deadline for one read child.
func gitReadCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(gitReadLife(), gitReadTimeout)
}

// gitWriteCtx is the deadline for one write child (or one sequence of
// them — runGitCmdSeqHook shares a deadline across its set, since the
// user sees one gesture).
func gitWriteCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), gitWriteTimeout)
}

// gitReadOutput runs a READ-ONLY git command and returns its stdout: the
// constructor's spelling of `exec.Command("git", args...).Output()`.
func gitReadOutput(args ...string) ([]byte, error) {
	ctx, cancel := gitReadCtx()
	defer cancel()
	return child.Git(ctx, nil, true, args...).Output()
}

// gitReadRun runs a READ-ONLY git command for its exit status alone
// (`rev-parse --verify`, `diff --quiet`, `cat-file -e`).
func gitReadRun(args ...string) error {
	ctx, cancel := gitReadCtx()
	defer cancel()
	return child.Git(ctx, nil, true, args...).Run()
}

// gitReadCombined is gitReadOutput with stderr folded in, for a read whose
// failure is shown to the user verbatim (the git status report).
func gitReadCombined(args ...string) ([]byte, error) {
	ctx, cancel := gitReadCtx()
	defer cancel()
	return child.Git(ctx, nil, true, args...).CombinedOutput()
}
