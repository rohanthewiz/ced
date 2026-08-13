// =============================================================================
// File: internal/app/catsrun_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"encoding/json"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/cats"
)

// noRunGuard collapses the shell-settling sleep. A quarter second per run is
// invisible to a user and is pure dead time here.
func noRunGuard(t *testing.T) {
	t.Helper()
	prev := catsRunScrollGuard
	catsRunScrollGuard = 0
	t.Cleanup(func() { catsRunScrollGuard = prev })
}

// -----------------------------------------------------------------------------
// The exit-code protocol
// -----------------------------------------------------------------------------

// THE test of this file. wait_for_output is seeded with what is already on
// the pane's screen, and a shell ECHOES the command it was given — so a
// marker that matched its own invocation would report "finished, exit 0" the
// instant it was typed, every time, for every command. The format string is
// what prevents it: the echo carries %s where the pattern needs digits.
func TestCatsRunMarkerCannotMatchItsOwnEcho(t *testing.T) {
	marker := "[ced run 41.3] exit:"
	script := catsRunScript("/w/proj", "go test ./...", marker)
	pattern := regexp.MustCompile(regexp.QuoteMeta(marker) + "[0-9]+")

	typed := "sh -c " + catsShellQuote(script) + "\n"
	if pattern.MatchString(typed) {
		t.Fatalf("the echoed command satisfies its own wait: %q", typed)
	}
	// And the line the command actually prints does match.
	if !pattern.MatchString("\n" + marker + "0\n") {
		t.Fatal("the printed result does not match the wait's pattern")
	}
	// The cd is joined with && so a failed cd cannot run the command
	// somewhere else — and its own status is then what gets reported.
	if !strings.HasPrefix(script, "cd '/w/proj' && ( go test ./... );") {
		t.Fatalf("script = %q", script)
	}
}

// The exit code is read out of the matched line, rightmost first: a wrapped
// echo can put the marker's format string on the same line as the result.
func TestCatsRunExit(t *testing.T) {
	const m = "[ced run 1.1] exit:"
	if code, ok := catsRunExit(m+"0", m); !ok || code != 0 {
		t.Fatalf("exit 0 → %d, %v", code, ok)
	}
	if code, ok := catsRunExit("noise "+m+"137 trailing", m); !ok || code != 137 {
		t.Fatalf("exit 137 → %d, %v", code, ok)
	}
	if code, ok := catsRunExit(m+"%s "+m+"2", m); !ok || code != 2 {
		t.Fatalf("with the echo on the same line → %d, %v", code, ok)
	}
	if _, ok := catsRunExit("nothing here", m); ok {
		t.Fatal("a line without the marker must not yield a code")
	}
	if _, ok := catsRunExit(m+"%s", m); ok {
		t.Fatal("the echoed format string is not an exit code")
	}
}

// Four outcomes, four phrasings. "Failed" and "we stopped watching" send the
// user to different places, so they must never be said the same way.
func TestCatsRunOutcome(t *testing.T) {
	const m = "[ced run 1.1] exit:"
	cases := []struct {
		name    string
		matched bool
		text    string
		err     error
		want    string
	}{
		{"success", true, m + "0", nil, "finished ✓"},
		{"failure", true, m + "1", nil, "failed ✗ exit 1"},
		{"no code", true, "something else", nil, "finished"},
		{"timeout", false, "", nil, "still running"},
		{"broken socket", false, "", errors.New("socket closed"), "lost track of the run"},
	}
	for _, c := range cases {
		got := catsRunOutcome("make", m, c.matched, c.text, c.err)
		if !strings.Contains(got, c.want) {
			t.Fatalf("%s: %q does not say %q", c.name, got, c.want)
		}
	}
}

// -----------------------------------------------------------------------------
// The sequence
// -----------------------------------------------------------------------------

// A first run splits a pane, types the wrapped command into it, and then
// WAITS on that same pane for the marker.
func TestCatsRunSpawnsAndWaits(t *testing.T) {
	noRunGuard(t)
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	s.waitMatched, s.waitText = true, "[ced run" // enough to satisfy the wait
	openTestFileTab(t, a, "main.go")

	a.catsRun("go test ./...")

	sent := waitForCall(t, s, cats.MethodPaneSendIn)
	var in struct {
		Pane   uint32 `json:"pane"`
		Text   string `json:"text"`
		Submit bool   `json:"submit"`
	}
	if err := json.Unmarshal(sent.Params, &in); err != nil {
		t.Fatalf("send_input params: %v", err)
	}
	if in.Pane != 100 {
		t.Fatalf("typed into pane %d, want the new one (100)", in.Pane)
	}
	if !in.Submit {
		t.Fatal("a run has to be submitted — this is a shell we made for it")
	}
	// sh -c, not the user's login shell: $? is not fish's spelling of the
	// exit status, and the protocol must not depend on which shell they use.
	if !strings.HasPrefix(in.Text, "sh -c ") || !strings.Contains(in.Text, "go test ./...") {
		t.Fatalf("command = %q", in.Text)
	}

	wait := waitForCall(t, s, cats.MethodWaitOutput)
	var w struct {
		Pane    uint32 `json:"pane"`
		Pattern string `json:"pattern"`
		Regex   bool   `json:"regex"`
	}
	if err := json.Unmarshal(wait.Params, &w); err != nil {
		t.Fatalf("wait params: %v", err)
	}
	if w.Pane != in.Pane {
		t.Fatalf("waited on pane %d but typed into %d", w.Pane, in.Pane)
	}
	if !w.Regex {
		t.Fatal("the marker pattern is a regex — a substring match would catch the echo")
	}
	if !regexp.MustCompile(w.Pattern).MatchString("[ced run 0.1] exit:0") &&
		!strings.Contains(w.Pattern, "exit:") {
		t.Fatalf("pattern = %q", w.Pattern)
	}
}

// The second run REUSES the first one's pane. Twelve runs must not end in
// twelve panes — but the reuse is conditional, so it costs one pane.list.
func TestCatsRunReusesItsPane(t *testing.T) {
	noRunGuard(t)
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	s.waitMatched = true
	s.paneRows = []cats.PaneInfo{{Pane: 9, Handle: "w1:p9"}, {Pane: 100, Handle: "w1:p10"}}
	a.cats.runPane, a.cats.runPaneOK = 100, true
	openTestFileTab(t, a, "main.go")

	a.catsRun("make")

	sent := waitForCall(t, s, cats.MethodPaneSendIn)
	for _, m := range s.methods() {
		if m == cats.MethodPaneSplit {
			t.Fatal("a reusable pane was split anyway")
		}
	}
	var in struct {
		Pane uint32 `json:"pane"`
	}
	if err := json.Unmarshal(sent.Params, &in); err != nil {
		t.Fatalf("params: %v", err)
	}
	if in.Pane != 100 {
		t.Fatalf("typed into pane %d, want the remembered one", in.Pane)
	}
}

// A remembered pane that an AGENT has since claimed is not reused. Typing a
// build command at a coding agent is the same mistake the self-check in
// catsagents.go exists to prevent, arriving by a different door.
func TestCatsRunWillNotTypeIntoAnAgentsPane(t *testing.T) {
	noRunGuard(t)
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	s.waitMatched = true
	s.paneRows = []cats.PaneInfo{{Pane: 9}, {Pane: 100, Agent: "claude", AgentState: cats.StateWorking}}
	a.cats.runPane, a.cats.runPaneOK = 100, true
	openTestFileTab(t, a, "main.go")

	a.catsRun("make")

	waitForCall(t, s, cats.MethodPaneSplit) // it split rather than reusing
}

// One run at a time, because the pane is reused: a second command typed at a
// pane already running something goes into that program's stdin.
func TestCatsRunRefusesASecondRun(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	withCtlSpy(t, a)
	openTestFileTab(t, a, "main.go")
	a.cats.runActive = 1

	a.menuCatsRunFile()

	if a.modal != nil {
		t.Fatal("a second run prompt opened while one was in flight")
	}
	if !strings.Contains(a.statusMsg, "already in flight") {
		t.Fatalf("status = %q", a.statusMsg)
	}
}

// -----------------------------------------------------------------------------
// The hook badge
// -----------------------------------------------------------------------------

// The point of the wait: while a run is in flight the editor's own pane says
// `working`, so cats' working→idle edge raises the "finished" notification —
// the toast, badge, or phone push that reaches a user who walked away.
//
// A failed run is NOT reported as blocked: blocked means the editor is asking
// a question the user did not ask for, and a channel that pages for answers
// as well as questions gets muted.
func TestCatsRunBadgesTheHookWhileItRuns(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if state, _ := a.catsSelfState(); state != cats.StateIdle {
		t.Fatalf("idle editor reports %q", state)
	}

	a.cats.runActive, a.cats.runLabel = 1, "go test ./..."
	state, status := a.catsSelfState()
	if state != cats.StateWorking || !strings.Contains(status, "go test") {
		t.Fatalf("running → %q / %q", state, status)
	}

	a.catsRunDone(&catsEvent{notice: "make: failed ✗ exit 1", pane: 100, paneOK: true})
	if state, _ := a.catsSelfState(); state != cats.StateIdle {
		t.Fatalf("a finished run left the pane at %q — a failure is an answer, not a question", state)
	}
	if !a.cats.runPaneOK || a.cats.runPane != 100 {
		t.Fatal("the run's pane was not remembered for the next one")
	}
	if a.statusMsg != "make: failed ✗ exit 1" {
		t.Fatalf("status = %q", a.statusMsg)
	}
}

// An unidentifiable pane is not remembered — the next run splits a fresh one
// rather than typing into a guess.
func TestCatsRunForgetsAnUnidentifiablePane(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.cats.runActive = 1
	a.catsRunDone(&catsEvent{notice: "Run failed: nope"})
	if a.cats.runPaneOK {
		t.Fatal("a pane that could not be identified was remembered anyway")
	}
}

// -----------------------------------------------------------------------------
// The command
// -----------------------------------------------------------------------------

// The guess is an interpreter invocation anyone can correct at a glance, and
// the path in it is project-relative because the run's cwd is the root.
func TestCatsRunGuess(t *testing.T) {
	cases := map[string]string{
		"cmd/x/main.go": "go run 'cmd/x/main.go'",
		"tools/go.py":   "python3 'tools/go.py'",
		"web/app.mjs":   "node 'web/app.mjs'",
		"Makefile":      "make",
		"Cargo.toml":    "cargo run",
		"notes.txt":     "",
		"":              "",
	}
	for in, want := range cases {
		if got := catsRunGuess(in); got != want {
			t.Fatalf("guess(%q) = %q, want %q", in, got, want)
		}
	}
}

// The last command is offered again on the file it was run for, and NOT on a
// different one — being handed `go test` on a Python file is where a
// remembered command turns into a trap.
func TestCatsRunInitialRemembersPerFile(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.cats.lastRunCmd, a.cats.lastRunFile = "go test ./... -race", "/w/a.go"

	if got := a.catsRunInitial("/w/a.go"); got != "go test ./... -race" {
		t.Fatalf("same file → %q", got)
	}
	other := a.rootDir + "/script.py"
	if got := a.catsRunInitial(other); got != "python3 'script.py'" {
		t.Fatalf("other file → %q, want the guess", got)
	}
}

// -----------------------------------------------------------------------------
// Tier 0
// -----------------------------------------------------------------------------

// The ladder's rule: the FEATURE survives the downgrade. Outside cats "run
// this" opens ced's own terminal with the command typed in — and NOT
// submitted, the same division of labour as sending a selection to an agent.
func TestCatsRunFallsBackToTheTerminalPanel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openTestFileTab(t, a, "main.go")

	a.catsRun("go run main.go")

	if !a.term.open || !a.term.focused {
		t.Fatal("the Tier-0 fallback did not open ced's terminal")
	}
	if got := a.term.input.String(); got != "go run main.go" {
		t.Fatalf("terminal input = %q", got)
	}
	if !strings.Contains(a.statusMsg, "press Enter") {
		t.Fatalf("status = %q — the user has to know the command is staged", a.statusMsg)
	}
}

// The terminal row is the same story: at Tier 0 it opens the panel rather
// than refusing, and says which terminal the user is getting.
func TestCatsTerminalFallsBackToThePanel(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.menuCatsTerminal()

	if !a.term.open {
		t.Fatal("the Tier-0 terminal did not open")
	}
	if !strings.Contains(a.statusMsg, "ced's own terminal") {
		t.Fatalf("status = %q", a.statusMsg)
	}
}

// A pane that already sits in the project root gets no `cd`: a fresh pane
// whose first line is a pointless cd reads as an editor that does not know
// where it is.
func TestCatsTerminalCdsOnlyWhenItHasTo(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	a.cats.panes = []cats.PaneInfo{{Pane: 9, Cwd: a.rootDir}}

	a.menuCatsTerminal()

	waitForCall(t, s, cats.MethodPaneSplit)
	// Give a stray send_input time to arrive before declaring it absent.
	time.Sleep(50 * time.Millisecond)
	for _, m := range s.methods() {
		if m == cats.MethodPaneSendIn {
			t.Fatal("typed a cd into a pane that was already in the project")
		}
	}

	// From somewhere else, the cd is sent.
	b := newTestApp(t, t.TempDir())
	sb := withCtlSpy(t, b)
	b.cats.panes = []cats.PaneInfo{{Pane: 9, Cwd: "/somewhere/else"}}

	b.menuCatsTerminal()

	sent := waitForCall(t, sb, cats.MethodPaneSendIn)
	var in struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(sent.Params, &in); err != nil {
		t.Fatalf("params: %v", err)
	}
	if in.Text != "cd "+catsShellQuote(b.rootDir)+"\n" {
		t.Fatalf("text = %q", in.Text)
	}
}

// -----------------------------------------------------------------------------
// The protocol, against a real shell
// -----------------------------------------------------------------------------

// Everything above pins what ced SENDS. This one runs the exact line — the
// outer quoting and the inner script — through a real /bin/sh and reads the
// marker back, because the whole feature rests on a claim about shell
// behaviour ("$? is the command's status, and printf will show it") that no
// amount of fake socket can check.
func TestCatsRunScriptReportsTheRealExitCode(t *testing.T) {
	dir := t.TempDir()
	marker := "[ced run 7.1] exit:"
	pattern := regexp.MustCompile(regexp.QuoteMeta(marker) + "([0-9]+)")

	for _, c := range []struct {
		cmd  string
		want string
	}{
		{"true", "0"},
		{"exit 3", "3"},
		{"sh -c 'exit 42'", "42"},           // quotes inside the command survive both layers
		{"no-such-command-here-xyz", "127"}, // the shell's own not-found status
	} {
		line := "sh -c " + catsShellQuote(catsRunScript(dir, c.cmd, marker))
		out, err := exec.Command("sh", "-c", line).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v (%s)", c.cmd, err, out)
		}
		m := pattern.FindStringSubmatch(string(out))
		if m == nil {
			t.Fatalf("%s: no marker in %q", c.cmd, out)
		}
		if m[1] != c.want {
			t.Fatalf("%s: exit %s, want %s", c.cmd, m[1], c.want)
		}
	}

	// And a cd that fails takes the command with it, rather than running it
	// in whatever directory the pane happened to be sitting in.
	line := "sh -c " + catsShellQuote(catsRunScript(dir+"/nope", "echo RAN", marker))
	out, _ := exec.Command("sh", "-c", line).CombinedOutput()
	if strings.Contains(string(out), "RAN") {
		t.Fatalf("the command ran despite a failed cd: %q", out)
	}
	if m := pattern.FindStringSubmatch(string(out)); m == nil || m[1] == "0" {
		t.Fatalf("a failed cd must report non-zero: %q", out)
	}
}
