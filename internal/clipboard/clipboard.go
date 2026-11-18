// =============================================================================
// File: internal/clipboard/clipboard.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package clipboard pushes text onto the host system clipboard via the
// OSC 52 terminal escape sequence. OSC 52 is the right primitive for an
// SSH-first editor: it travels over the SSH channel, and every modern
// terminal we care about — iTerm2, WezTerm, Kitty, Alacritty, Ghostty,
// gnome-terminal, the macOS default — honors it.
//
// Inside tmux it is not enough on its own (N-151). Measured against tmux
// 3.5a with nothing configured (the N-139 harness): tmux ships
// `allow-passthrough off`, so the DCS-wrapped sequence is DROPPED, and
// `set-clipboard external`, so a plain OSC 52 from the pane is ignored
// too — a copy put nothing on the outer terminal. What does work there is
// asking tmux itself: `tmux load-buffer -w -` makes tmux emit the OSC 52
// to its own terminal. So inside tmux CopyToSystem does both: the
// load-buffer (tmux ≥ 3.2, which also leaves the text in tmux's paste
// buffers) and the passthrough sequence (for a tmux with set-clipboard
// off but passthrough allowed).
//
// We deliberately do not try to read the system clipboard from a TUI:
// most terminals refuse to expose it for security reasons. Paste from
// outside the editor instead arrives via the user's terminal paste
// (Cmd-V / right-click), which delivers the text as keypresses; we
// handle those in the normal key-input path.
package clipboard

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/child"
	"github.com/rohanthewiz/ced/internal/crash"
)

// TmuxLoadBuffer hands text to tmux's own clipboard path (see the
// package comment). A package var so tests pin it to a no-op: a test run
// inside a developer's tmux must never write their paste buffers.
var TmuxLoadBuffer = runTmuxLoadBuffer

// runTmuxLoadBuffer runs `tmux load-buffer -w -` with text on stdin.
// Detached and bounded like every child ced runs (internal/child): it
// needs only $TMUX to find the server, never a terminal, and a tmux that
// does not answer is abandoned after five seconds. An old tmux without
// -w fails here harmlessly; the passthrough sequence still goes out.
func runTmuxLoadBuffer(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "load-buffer", "-w", "-")
	child.Detach(cmd)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// CopyToSystem pushes text onto the host system clipboard via OSC 52.
//
// We open /dev/tty directly rather than writing to stdout so we don't
// race tcell's screen rendering. When TMUX is set we ALSO hand the text
// to tmux (TmuxLoadBuffer, on a goroutine — callers are on the main loop
// and a fork must not hold a frame), and wrap the OSC 52 sequence in
// tmux's escape passthrough. See the package comment for why both.
func CopyToSystem(text string) error {
	if os.Getenv("TMUX") != "" {
		load := TmuxLoadBuffer
		go func() {
			defer crash.Recover()
			_ = load(text)
		}()
	}
	f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	seq := fmt.Sprintf("\x1b]52;c;%s\x07", encoded)
	if os.Getenv("TMUX") != "" {
		// tmux passthrough: wrap inner escape sequences so tmux forwards
		// them verbatim to the host terminal.
		seq = fmt.Sprintf("\x1bPtmux;\x1b%s\x1b\\", seq)
	}
	_, err = f.WriteString(seq)
	return err
}
