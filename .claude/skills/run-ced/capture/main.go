// =============================================================================
// File: .claude/skills/run-ced/capture/main.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// capture runs ced on a real pseudo-terminal, plays a scripted sequence
// of keystrokes at it, and reconstructs the screen the editor painted.
//
// Why this exists: ced is a tcell app, so it does nothing useful without
// a terminal on the other end — `./bin/ced . < /dev/null` exits, and the
// test suite's SimulationScreen proves the draw functions run but not
// that the real binary starts, sizes itself, and paints. This is the
// gap: a genuine PTY, real keystrokes, and the actual ANSI byte stream
// replayed into a grid.
//
// Two outputs, for two different readers:
//
//   - -text writes the screen as plain text on stdout. This is the one
//     an agent should read: it makes "did the file open / did the panel
//     appear / what does the status bar say" a grep.
//   - -out writes the screen as HTML with every cell's real color. This
//     is the one a human should look at, and the only way to check
//     anything about the palette.
//
// The single most important flag is SNAP in the script. Quitting ced
// restores the terminal and clears it, so a capture taken after the quit
// key is a blank screen — always SNAP before you quit.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"html"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/mattn/go-runewidth"
	"golang.org/x/sys/unix"
)

// defaultScript opens the first Go-ish file the fuzzy finder matches and
// scrolls into it — enough that the capture shows a tree, a tab bar,
// line numbers, syntax colors, and a status bar all at once.
const defaultScript = `1500@;300@{esc}p;500@main.go;700@{enter};900@{down x12};900@SNAP;400@{esc}q;600@`

func main() {
	var (
		bin      = flag.String("bin", "./bin/ced", "path to the ced binary")
		dir      = flag.String("dir", ".", "project directory to open")
		out      = flag.String("out", "", "write the screen as HTML to this path")
		text     = flag.Bool("text", false, "write the screen as plain text to stdout")
		fragment = flag.Bool("fragment", false, "-out emits just the <pre> block, not a whole page")
		script   = flag.String("script", defaultScript, "keystroke script (see -help-script)")
		config   = flag.String("config", "", "config.json contents for a throwaway XDG_CONFIG_HOME")
		theme    = flag.String("theme", "", `shorthand for -config '{"theme":"NAME"}'`)
		cols     = flag.Int("cols", 150, "terminal columns")
		rows     = flag.Int("rows", 44, "terminal rows")
		helpScr  = flag.Bool("help-script", false, "explain the script syntax and exit")
	)
	flag.Parse()

	if *helpScr {
		fmt.Print(scriptHelp)
		return
	}

	// Every run gets a throwaway config home, so a capture can never read
	// or write the developer's own ~/.config/ced.
	home, err := os.MkdirTemp("", "ced-capture-")
	if err != nil {
		fail(err)
	}
	defer os.RemoveAll(home)
	body := *config
	if body == "" && *theme != "" {
		body = `{"theme":"` + *theme + `"}`
	}
	if body == "" {
		// Keep captures deterministic and offline: no sidecar, no
		// background writes to the project being photographed.
		body = `{"copilot":"off","autosave":"off"}`
	}
	if err := os.MkdirAll(home+"/ced", 0o755); err != nil {
		fail(err)
	}
	if err := os.WriteFile(home+"/ced/config.json", []byte(body+"\n"), 0o644); err != nil {
		fail(err)
	}

	steps, err := parseScript(*script)
	if err != nil {
		fail(err)
	}
	data, err := run(*bin, *dir, home, *cols, *rows, steps)
	if err != nil {
		fail(err)
	}

	sc := newScreen(*cols, *rows)
	sc.feed(data)

	if *text {
		fmt.Print(sc.text())
	}
	if *out != "" {
		payload := sc.htmlFrag()
		if !*fragment {
			payload = sc.page()
		}
		if err := os.WriteFile(*out, []byte(payload), 0o644); err != nil {
			fail(err)
		}
		fmt.Fprintf(os.Stderr, "wrote %s (%d bytes of ANSI captured)\n", *out, len(data))
	}
	if !*text && *out == "" {
		fmt.Fprintln(os.Stderr, "nothing requested — pass -text and/or -out")
		os.Exit(2)
	}
}

// fail reports a fatal error and exits non-zero.
func fail(err error) {
	fmt.Fprintln(os.Stderr, "capture:", err)
	os.Exit(1)
}

const scriptHelp = `Script syntax
=============

Steps are separated by ";". Each step is:

    [delayMs]@[payload]

The delay is how long to WAIT BEFORE the payload is sent (default 500ms).
An empty payload is a pure wait. A payload is literal text to type,
except for these tokens:

    SNAP            capture the screen at this point (see below)
    {esc} {enter} {tab} {bs} {space}
    {up} {down} {left} {right} {home} {end} {pgup} {pgdn}
    {esc}x          ced's leader form — e.g. {esc}p is "find file",
                    {esc}q quit, {esc}t sidebar, {esc}` + "`" + ` terminal,
                    {esc}g git panel, {esc}L git log, {esc}a palette
    {down x12}      repeat a key N times

SNAP is the one you cannot skip. Quitting restores and clears the
terminal, so a capture taken after {esc}q is a blank screen. Put SNAP on
the step BEFORE the quit.

Examples

  open a file and photograph it (the default):
    1500@;300@{esc}p;500@main.go;700@{enter};900@{down x12};900@SNAP;400@{esc}q

  open the ≡ menu:
    1500@;400@{esc}{esc};700@SNAP;300@{esc};300@{esc}q

  open the theme picker and pick the 3rd row:
    1500@;400@{esc}a;500@theme;800@{enter};700@{down x2};600@SNAP;400@{enter};900@SNAP

  show the git panel:
    1500@;500@{esc}g;900@SNAP;400@{esc}q
`

// --- script ----------------------------------------------------------

// step is one scripted interaction: wait, then either snapshot or send
// bytes. snap captures what has been received SO FAR, which is what
// makes photographing a live screen before the quit possible.
type step struct {
	wait time.Duration
	keys string
	snap bool
}

// parseScript turns the -script string into steps. Errors name the
// offending step so a typo in a long script is findable.
func parseScript(s string) ([]step, error) {
	var out []step
	for i, raw := range strings.Split(s, ";") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		wait := 500 * time.Millisecond
		payload := raw
		if at := strings.Index(raw, "@"); at >= 0 {
			if at > 0 {
				ms, err := strconv.Atoi(strings.TrimSpace(raw[:at]))
				if err != nil {
					return nil, fmt.Errorf("step %d (%q): bad delay: %w", i+1, raw, err)
				}
				wait = time.Duration(ms) * time.Millisecond
			}
			payload = raw[at+1:]
		}
		if strings.TrimSpace(payload) == "SNAP" {
			out = append(out, step{wait: wait, snap: true})
			continue
		}
		keys, err := expandKeys(payload)
		if err != nil {
			return nil, fmt.Errorf("step %d (%q): %w", i+1, raw, err)
		}
		out = append(out, step{wait: wait, keys: keys})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("script is empty")
	}
	return out, nil
}

// namedKeys maps the {token} names to the bytes a terminal sends.
var namedKeys = map[string]string{
	"esc": "\x1b", "enter": "\r", "tab": "\t", "bs": "\x7f", "space": " ",
	"up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D",
	"home": "\x1b[H", "end": "\x1b[F", "pgup": "\x1b[5~", "pgdn": "\x1b[6~",
}

// expandKeys resolves {token} and {token xN} forms, leaving other text
// to be typed literally.
func expandKeys(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			b.WriteByte(s[i])
			continue
		}
		j := strings.IndexByte(s[i:], '}')
		if j < 0 {
			return "", fmt.Errorf("unclosed { in %q", s)
		}
		tok := strings.TrimSpace(s[i+1 : i+j])
		n := 1
		if x := strings.LastIndex(tok, " x"); x > 0 {
			cnt, err := strconv.Atoi(tok[x+2:])
			if err != nil || cnt < 1 {
				return "", fmt.Errorf("bad repeat in {%s}", tok)
			}
			n, tok = cnt, strings.TrimSpace(tok[:x])
		}
		seq, ok := namedKeys[strings.ToLower(tok)]
		if !ok {
			return "", fmt.Errorf("unknown key {%s}", tok)
		}
		b.WriteString(strings.Repeat(seq, n))
		i += j
	}
	return b.String(), nil
}

// --- PTY -------------------------------------------------------------

// openPTY allocates a master/slave pty pair and sizes it. Darwin needs
// the TIOCPTY* ioctl dance rather than grantpt(3); a zero-sized pty
// makes tcell draw nothing, which is why the winsize is set on both ends.
func openPTY(cols, rows int) (*os.File, *os.File, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	fd := int(m.Fd())
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		return nil, nil, fmt.Errorf("grant: %w", err)
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		return nil, nil, fmt.Errorf("unlock: %w", err)
	}
	var buf [128]byte
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd),
		uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&buf[0]))); errno != 0 {
		return nil, nil, fmt.Errorf("ptsname: %v", errno)
	}
	end := bytes.IndexByte(buf[:], 0)
	if end < 0 {
		end = len(buf)
	}
	s, err := os.OpenFile(string(buf[:end]), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	ws := &unix.Winsize{Row: uint16(rows), Col: uint16(cols)}
	_ = unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, ws)
	_ = unix.IoctlSetWinsize(int(s.Fd()), unix.TIOCSWINSZ, ws)
	return m, s, nil
}

// run launches ced and plays the script, returning the bytes it painted
// (up to the SNAP step, when there is one).
func run(bin, dir, cfgHome string, cols, rows int, steps []step) ([]byte, error) {
	m, s, err := openPTY(cols, rows)
	if err != nil {
		return nil, err
	}
	defer m.Close()

	cmd := exec.Command(bin, ".")
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = s, s, s
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"XDG_CONFIG_HOME="+cfgHome,
		"LINES="+strconv.Itoa(rows),
		"COLUMNS="+strconv.Itoa(cols),
	)
	cmd.SysProcAttr = &unix.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s.Close() // the child owns it now; keeping it open would hide the EOF

	var mu sync.Mutex
	var buf bytes.Buffer
	go func() {
		b := make([]byte, 1<<16)
		for {
			n, err := m.Read(b)
			if n > 0 {
				mu.Lock()
				buf.Write(b[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	var snap []byte
	for _, st := range steps {
		time.Sleep(st.wait)
		if st.snap {
			mu.Lock()
			snap = append([]byte(nil), buf.Bytes()...)
			mu.Unlock()
		}
		if st.keys != "" {
			if _, err := m.WriteString(st.keys); err != nil {
				break
			}
		}
	}

	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
	}
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if snap != nil {
		return snap, nil
	}
	return append([]byte(nil), buf.Bytes()...), nil
}

// --- virtual screen --------------------------------------------------

// style is one cell's rendition.
type style struct {
	fg, bg string // "" means the terminal default
	bold   bool
	rev    bool
}

// cell is one screen position. A zero rune marks the trailing half of a
// double-width character.
type cell struct {
	r  rune
	st style
}

// screen is the virtual grid the ANSI stream is replayed into.
type screen struct {
	g    [][]cell
	x, y int
	cur  style
	w, h int
}

// newScreen builds a blank grid.
func newScreen(w, h int) *screen {
	s := &screen{w: w, h: h}
	s.g = make([][]cell, h)
	for i := range s.g {
		s.g[i] = make([]cell, w)
		for j := range s.g[i] {
			s.g[i][j] = cell{r: ' '}
		}
	}
	return s
}

// put writes a rune at the cursor and advances by its display width.
func (s *screen) put(r rune) {
	if s.y < 0 || s.y >= s.h || s.x >= s.w {
		return
	}
	w := runewidth.RuneWidth(r)
	if w == 0 {
		return
	}
	st := s.cur
	if st.rev {
		st.fg, st.bg = st.bg, st.fg
	}
	s.g[s.y][s.x] = cell{r: r, st: st}
	for k := 1; k < w && s.x+k < s.w; k++ {
		s.g[s.y][s.x+k] = cell{r: 0, st: st}
	}
	s.x += w
}

// clearAll blanks the grid with the current background.
func (s *screen) clearAll() {
	st := s.cur
	st.fg = ""
	for i := range s.g {
		for j := range s.g[i] {
			s.g[i][j] = cell{r: ' ', st: st}
		}
	}
}

// sgr applies a Select Graphic Rendition parameter list.
func (s *screen) sgr(params []int) {
	if len(params) == 0 {
		params = []int{0}
	}
	for i := 0; i < len(params); i++ {
		p := params[i]
		switch {
		case p == 0:
			s.cur = style{}
		case p == 1:
			s.cur.bold = true
		case p == 22:
			s.cur.bold = false
		case p == 7:
			s.cur.rev = true
		case p == 27:
			s.cur.rev = false
		case p == 39:
			s.cur.fg = ""
		case p == 49:
			s.cur.bg = ""
		case p == 38 || p == 48:
			if i+4 < len(params) && params[i+1] == 2 {
				c := fmt.Sprintf("#%02x%02x%02x", params[i+2]&0xff, params[i+3]&0xff, params[i+4]&0xff)
				if p == 38 {
					s.cur.fg = c
				} else {
					s.cur.bg = c
				}
				i += 4
			} else if i+2 < len(params) && params[i+1] == 5 {
				c := xterm256(params[i+2])
				if p == 38 {
					s.cur.fg = c
				} else {
					s.cur.bg = c
				}
				i += 2
			}
		case p >= 30 && p <= 37:
			s.cur.fg = basic16[p-30]
		case p >= 40 && p <= 47:
			s.cur.bg = basic16[p-40]
		case p >= 90 && p <= 97:
			s.cur.fg = basic16[p-90+8]
		case p >= 100 && p <= 107:
			s.cur.bg = basic16[p-100+8]
		}
	}
}

// basic16 is the standard ANSI palette, for the rare non-truecolor cell.
var basic16 = []string{
	"#000000", "#cc0000", "#4e9a06", "#c4a000", "#3465a4", "#75507b", "#06989a", "#d3d7cf",
	"#555753", "#ef2929", "#8ae234", "#fce94f", "#729fcf", "#ad7fa8", "#34e2e2", "#eeeeec",
}

// xterm256 maps an indexed color to hex.
func xterm256(n int) string {
	switch {
	case n < 16:
		return basic16[n]
	case n < 232:
		n -= 16
		lv := []int{0, 95, 135, 175, 215, 255}
		return fmt.Sprintf("#%02x%02x%02x", lv[n/36], lv[(n/6)%6], lv[n%6])
	default:
		v := 8 + (n-232)*10
		return fmt.Sprintf("#%02x%02x%02x", v, v, v)
	}
}

// feed replays an ANSI byte stream into the grid. It implements the
// subset tcell actually emits — absolute cursor moves, erases, SGR, and
// text — which is enough to reconstruct the painted screen exactly.
func (s *screen) feed(data []byte) {
	rs := []rune(string(data))
	for i := 0; i < len(rs); i++ {
		switch r := rs[i]; r {
		case 0x1b:
			if i+1 >= len(rs) {
				return
			}
			switch rs[i+1] {
			case '[':
				j := i + 2
				for j < len(rs) && !((rs[j] >= 'A' && rs[j] <= 'Z') || (rs[j] >= 'a' && rs[j] <= 'z')) {
					j++
				}
				if j >= len(rs) {
					return
				}
				s.csi(string(rs[i+2:j]), rs[j])
				i = j
			case ']': // OSC — title, clipboard; ends at BEL or ST
				j := i + 2
				for j < len(rs) && rs[j] != 0x07 {
					if rs[j] == 0x1b && j+1 < len(rs) && rs[j+1] == '\\' {
						j++
						break
					}
					j++
				}
				i = j
			case '(', ')', '#':
				i += 2
			default:
				i++
			}
		case '\r':
			s.x = 0
		case '\n':
			if s.y++; s.y >= s.h {
				s.y = s.h - 1
			}
		case '\b':
			if s.x > 0 {
				s.x--
			}
		case '\t':
			s.x = (s.x/8 + 1) * 8
		default:
			if r >= 0x20 {
				s.put(r)
			}
		}
	}
}

// csi dispatches one control sequence.
func (s *screen) csi(param string, final rune) {
	if strings.HasPrefix(param, "?") || strings.HasPrefix(param, ">") {
		return // private modes: mouse, alt screen, bracketed paste
	}
	var ps []int
	for _, f := range strings.Split(param, ";") {
		n, _ := strconv.Atoi(f)
		ps = append(ps, n)
	}
	at := func(i, def int) int {
		if i < len(ps) && param != "" && ps[i] != 0 {
			return ps[i]
		}
		return def
	}
	switch final {
	case 'H', 'f':
		s.y, s.x = at(0, 1)-1, at(1, 1)-1
	case 'A':
		s.y -= at(0, 1)
	case 'B':
		s.y += at(0, 1)
	case 'C':
		s.x += at(0, 1)
	case 'D':
		s.x -= at(0, 1)
	case 'G':
		s.x = at(0, 1) - 1
	case 'J':
		if len(ps) > 0 && ps[0] == 2 {
			s.clearAll()
			s.x, s.y = 0, 0
		}
	case 'K':
		if s.y >= 0 && s.y < s.h {
			st := s.cur
			st.fg = ""
			for j := s.x; j < s.w; j++ {
				s.g[s.y][j] = cell{r: ' ', st: st}
			}
		}
	case 'm':
		if param == "" {
			ps = []int{0}
		}
		s.sgr(ps)
	}
	if s.x < 0 {
		s.x = 0
	}
	if s.y < 0 {
		s.y = 0
	}
}

// text renders the grid as plain text with trailing blanks trimmed —
// the form an agent greps to check what the editor actually shows.
func (s *screen) text() string {
	var b strings.Builder
	for _, row := range s.g {
		var line strings.Builder
		for _, c := range row {
			if c.r == 0 {
				continue
			}
			line.WriteRune(c.r)
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// htmlFrag renders the grid as a <pre> block with one span per run of
// identical styling — the colors are the ones ced actually emitted.
func (s *screen) htmlFrag() string {
	var b strings.Builder
	b.WriteString(`<pre class="scr">`)
	for _, row := range s.g {
		var run strings.Builder
		var cur style
		open := false
		flush := func() {
			if !open {
				return
			}
			var sty []string
			if cur.fg != "" {
				sty = append(sty, "color:"+cur.fg)
			}
			if cur.bg != "" {
				sty = append(sty, "background:"+cur.bg)
			}
			if cur.bold {
				sty = append(sty, "font-weight:700")
			}
			fmt.Fprintf(&b, `<span style="%s">%s</span>`,
				strings.Join(sty, ";"), html.EscapeString(run.String()))
			run.Reset()
			open = false
		}
		for _, c := range row {
			if c.r == 0 {
				continue
			}
			if !open || c.st != cur {
				flush()
				cur, open = c.st, true
			}
			run.WriteRune(c.r)
		}
		flush()
		b.WriteByte('\n')
	}
	b.WriteString("</pre>")
	return b.String()
}

// page wraps the fragment in a standalone document, so the -out file can
// be opened directly in a browser. Background comes from the grid's own
// first cell, so the page matches whatever theme was captured.
func (s *screen) page() string {
	bg, fg := "#111", "#ccc"
	if len(s.g) > 1 && len(s.g[1]) > 4 {
		if c := s.g[1][4]; c.st.bg != "" {
			bg = c.st.bg
		}
	}
	return `<!doctype html><meta charset="utf-8"><title>ced capture</title>
<style>
body{margin:0;background:#0d0e12;color:#ccc;
     font-family:ui-sans-serif,system-ui,sans-serif}
.well{overflow-x:auto;background:` + bg + `}
.scr{font-family:ui-monospace,"SF Mono",Menlo,Consolas,monospace;font-size:12px;
     line-height:1.28;margin:0;padding:14px;white-space:pre;display:inline-block;
     min-width:100%;color:` + fg + `;font-variant-ligatures:none}
</style>
<div class="well">` + s.htmlFrag() + `</div>
`
}
