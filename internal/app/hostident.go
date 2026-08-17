// =============================================================================
// File: internal/app/hostident.go
// Author: Rohan Allison
// =============================================================================

// Host identity: tell the hosting terminal who we are and what we're on.
//
// Two escape sequences, emitted on the tty out-of-band from tcell:
//
//   - OSC 7  — the workspace directory as a file:// URL, once at startup.
//   - OSC 2  — the window title, tracking the active tab (name + dirty dot).
//
// A plain terminal uses these for its title bar, which is nice. A
// multiplexer host is the real audience: cats scans both out of the raw
// pane stream and turns them into pane/tab names, sidebar cwd, tab-cwd
// inheritance for new panes, and the context line on notifications —
// so the pane running ced is labeled by the file being edited instead
// of a bare process name. tmux likewise consumes OSC 2 for its pane
// title. That consumption is the point, which is why — unlike the
// OSC 52 path in internal/clipboard — nothing here is wrapped in the
// tmux passthrough escape: we are talking TO the host mux, not through
// it.
//
// Emission mechanics follow internal/clipboard: open /dev/tty directly
// so we never race tcell's own output buffer (OSC sequences don't move
// the cursor, so interleaving with a frame is harmless). The writer
// lives behind a function field on App; New() installs the real one,
// tests either leave it nil (feature off — no test may scribble titles
// on the developer's terminal) or install a capture function.
//
// The title is re-checked from the after-event chain rather than from
// the dozen call sites that can change the active tab (clicks, leader
// keys, session restore, remote opens, tab closes). Cost when idle is
// two string compares and two bool compares — same budget as the other
// after-event hooks.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hostIdentInit installs the tty writer and sends the one-time part of
// the identity: a title-stack push (so a well-behaved terminal restores
// its old title after Close's pop), the workspace OSC 7, and the initial
// title. rootDir never changes within an App's lifetime — a folder
// switch tears the App down and builds a new one (see folder.go) — so
// startup is the only place OSC 7 is needed.
func (a *App) hostIdentInit() {
	a.hostIdentWrite = hostIdentTTYWrite
	// XTPUSHTITLE. Terminals without a title stack ignore it.
	_ = a.hostIdentWrite("\x1b[22;0t")
	_ = a.hostIdentWrite(osc7CwdSeq(hostname(), a.rootDir))
	a.hostIdentAfterEvent()
}

// hostIdentClose hands the title back on the way out: an EMPTY OSC 2
// first, then the pop matching init's push. Called from Close; a nil
// writer (tests, or an App that never ran hostIdentInit) makes it a
// no-op.
//
// The order is the whole point, because the two sequences serve two
// different terminals and the pop is the one that can be ignored:
//
//   - A terminal WITH a title stack (xterm, tmux, cats) applies the pop
//     and restores whatever the title was before ced launched — the
//     clear is overwritten a byte later and costs nothing.
//   - A terminal WITHOUT one (Terminal.app, plenty of emulators, and any
//     mux that doesn't proxy 22/23t) silently drops both the push and the
//     pop, so before this the pane kept the last file ced had open,
//     forever — a shell prompt labeled "app.go · ced" long after ced
//     exited. The empty title is what those terminals actually act on;
//     most then fall back to their own default, and a shell that reports
//     its title re-labels the pane on its next prompt.
//
// Clearing FIRST and popping second is therefore strictly better than
// either alone: nothing is lost where the stack works, and a stale lie is
// removed where it doesn't. OSC 7 is deliberately not re-emitted — the
// cwd ced reported is still true of the shell that gets the pane back.
func (a *App) hostIdentClose() {
	if a.hostIdentWrite == nil {
		return
	}
	_ = a.hostIdentWrite(osc2TitleSeq(""))
	// XTPOPTITLE.
	_ = a.hostIdentWrite("\x1b[23;0t")
}

// hostIdentAfterEvent re-emits the title when the active tab's identity
// changed: a different file, the dirty flag flipping, or the last tab
// closing. Keyed on (path, dirty, hasTab) rather than the built title
// string so the idle path allocates nothing.
func (a *App) hostIdentAfterEvent() {
	if a.hostIdentWrite == nil {
		return
	}
	path, dirty, hasTab := "", false, false
	if t := a.activeTabPtr(); t != nil {
		path, dirty, hasTab = t.Path, t.Dirty, true
	}
	if a.identSent && path == a.lastIdentPath && dirty == a.lastIdentDirty && hasTab == a.lastIdentHasTab {
		return
	}
	a.lastIdentPath, a.lastIdentDirty, a.lastIdentHasTab, a.identSent = path, dirty, hasTab, true

	title := ""
	switch {
	case hasTab:
		title = hostIdentTitle(a.activeTabPtr().DisplayName(), dirty)
	default:
		// No tab open: name the workspace, so a freshly launched ced on
		// a folder still labels its pane usefully.
		title = hostIdentTitle(filepath.Base(a.rootDir), false)
	}
	_ = a.hostIdentWrite(osc2TitleSeq(title))
}

// hostIdentTitle builds the window title: the ced dirty dot, the name,
// and a " · ced" suffix so the pane is recognizable as the editor in a
// mux tab bar full of shells and agents.
func hostIdentTitle(name string, dirty bool) string {
	if dirty {
		return "● " + name + " · ced"
	}
	return name + " · ced"
}

// osc2TitleSeq wraps a title in OSC 2 ... BEL. Control bytes are
// stripped first: the title contains a user-controlled filename, and an
// embedded ESC or BEL would terminate the sequence early and leak the
// remainder into the host's parser as garbage input.
func osc2TitleSeq(title string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, title)
	return "\x1b]2;" + clean + "\x07"
}

// osc7CwdSeq builds OSC 7 ... BEL carrying dir as a file://host/path
// URL — the shape shells emit for cwd reporting and the one cats' OSC
// scanner parses. The hostname matters on the remote topology: a cats
// host resolving the path server-side needs to know which machine it
// names.
func osc7CwdSeq(host, dir string) string {
	return "\x1b]7;file://" + host + fileURLPath(dir) + "\x07"
}

// fileURLPath percent-encodes an absolute path for a file:// URL,
// byte-wise per RFC 3986: unreserved characters and the path separator
// pass through, everything else (spaces, unicode, and — critically for
// sequence integrity — any control byte) becomes %XX.
func fileURLPath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~', c == '/':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// hostname is os.Hostname with the error folded to "" — an empty host
// in a file:// URL means localhost to every consumer we care about, and
// a title/cwd hint is not worth surfacing an error for.
func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// hostIdentTTYWrite is the real writer: open /dev/tty per emission and
// write the sequence whole, mirroring internal/clipboard. Title changes
// happen on tab switches and dirty-flag flips, not per keystroke, so
// the open/close cost is irrelevant.
func hostIdentTTYWrite(seq string) error {
	f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(seq)
	return err
}
