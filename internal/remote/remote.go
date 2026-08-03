// =============================================================================
// File: internal/remote/remote.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Package remote lets one ced instance hand a file to another one that
// is already running, over a unix socket in the user's own runtime
// directory. It is what makes `EDITOR="ced --wait"` work inside tmux:
// `git commit` in pane 2 opens its message in the editor already running
// in pane 1, instead of nesting a second editor inside the first.
//
// Two halves, and they never share a process:
//
//	Listen(root, handler)   the editor side — one socket per instance
//	Open(path, wait)        the CLI side — find an instance, hand it a file
//
// DISCOVERY IS BY PROJECT ROOT, NOT BY "THE" INSTANCE. Everything in ced
// is rooted: the tree, the finder index, gopls's rootUri, the terminal's
// cwd. Delivering a file to an instance rooted somewhere else would open
// it into a workspace where none of that applies, so a client picks the
// instance whose root CONTAINS the file, most-specific first, and
// reports ErrNoInstance when none does. The caller's answer to that is to
// start a normal editor — a fallback, never a failure.
//
// SOCKETS ARE NAMED PER PROCESS (<root-hash>-<pid>.sock), not per root.
// A deterministic per-root name would force every second instance on the
// same project to decide whether to take the socket over, which is a
// question with no good answer — the first instance is still running and
// still wants it. Per-process names let every instance listen; the client
// probes them all and unlinks the ones nobody answers, so a crashed
// instance's socket file is cleaned up by the next client rather than
// needing a reaper.
//
// The wire format is one JSON object per line in each direction — the
// same ndjson shape internal/lsp already speaks, for the same reason:
// it needs no framing library and it is readable in a terminal when
// something goes wrong.
package remote

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rohanthewiz/ced/internal/session"
)

// ErrNoInstance is returned by Open when no running instance claims a
// root that contains the requested file. It is the signal to fall back
// to starting a normal editor, so callers should branch on it rather
// than treating it as a failure.
var ErrNoInstance = errors.New("no running ced instance owns that path")

// probeTimeout bounds the hello round-trip used to discover an
// instance's root. It is short on purpose: the directory may hold
// sockets for instances that are wedged (a stopped process, a full
// event queue), and the client's job is to fall back to a local editor
// quickly rather than to diagnose them.
const probeTimeout = 500 * time.Millisecond

// -----------------------------------------------------------------------------
// Wire format
// -----------------------------------------------------------------------------

// request is one line from the client. Op is "hello" (what root do you
// serve?) or "open" (please open this file).
type request struct {
	Op   string `json:"op"`
	Path string `json:"path,omitempty"`
	Wait bool   `json:"wait,omitempty"`
}

// response is one line from the editor. An "open" request gets one of
// these immediately (OK reports whether the file landed), and — when the
// request asked to wait — a second one with Done set once the editor is
// finished with the file.
type response struct {
	Root string `json:"root,omitempty"`
	OK   bool   `json:"ok"`
	Err  string `json:"err,omitempty"`
	Done bool   `json:"done,omitempty"`
}

// -----------------------------------------------------------------------------
// Where the sockets live
// -----------------------------------------------------------------------------

// socketDirFn resolves the directory holding one socket per running
// instance. A package var so tests can point it at t.TempDir() — without
// the seam a test run would probe (and unlink) the developer's real
// sockets, and a running editor is exactly the thing a test must not
// touch.
var socketDirFn = defaultSocketDir

// defaultSocketDir prefers $XDG_RUNTIME_DIR (Linux's tmpfs-backed,
// per-user, cleaned-at-logout directory — precisely what this is for)
// and falls back to a per-uid folder under the system temp dir, which is
// what macOS gives us. NOT ~/.config/ced: a socket is runtime state that
// must not survive a reboot, and it has no business in a directory the
// user hand-edits.
//
// Unix socket paths are capped at ~104 bytes by the kernel, so the names
// below stay short (a 16-hex-digit root hash plus the pid). A path that
// still overruns simply fails to listen, and remote open degrades to
// off — the silent-degradation contract every other integration keeps.
func defaultSocketDir() string {
	if run := os.Getenv("XDG_RUNTIME_DIR"); run != "" {
		return filepath.Join(run, "ced")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("ced-%d", os.Getuid()))
}

// socketName builds the per-instance file name: a short hash of the
// project root so the file is recognisable at a glance, plus the pid so
// two instances on one project can both listen.
func socketName(root string, pid int) string {
	sum := sha256.Sum256([]byte(root))
	return fmt.Sprintf("%s-%d.sock", hex.EncodeToString(sum[:4]), pid)
}

// -----------------------------------------------------------------------------
// The editor side
// -----------------------------------------------------------------------------

// Handler is the editor's callback for an "open" request. It runs on the
// connection's own goroutine, so an implementation must NOT touch editor
// state directly — post an event and wait for the main loop, the same
// contract the ACP request hooks keep.
//
// It returns a channel the server waits on when the client asked to
// wait; the editor closes that channel when it is finished with the file
// (the tab closed, or the editor exited). A nil channel means "nothing to
// wait for", which is the right answer for a non-wait request and the
// safe answer if the editor cannot track this file.
type Handler func(path string, wait bool) (done <-chan struct{}, err error)

// Server is one instance's listening socket. Everything it does happens
// on goroutines it owns; the only thing that reaches the editor is the
// Handler call.
type Server struct {
	root     string
	path     string
	ln       net.Listener
	handler  Handler
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   bool
	closeSig chan struct{}
}

// Listen creates this instance's socket and starts accepting. root is
// the project root the instance is serving — it is what clients match
// their file against, so it must be the same absolute path the editor
// itself uses.
//
// Errors are the caller's cue to run without remote support rather than
// to fail: an unwritable runtime directory or an over-long socket path
// costs the handoff, not the editor.
func Listen(root string, handler Handler) (*Server, error) {
	if handler == nil {
		return nil, errors.New("remote: nil handler")
	}
	root = session.Normalize(root)
	dir := socketDirFn()
	if dir == "" {
		return nil, errors.New("remote: no runtime directory")
	}
	// 0700: the socket is a request to open files in this user's editor,
	// so the directory is the access control. Nothing here is readable by
	// another account.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, socketName(root, os.Getpid()))
	// A file at our own pid's name is a leftover from a previous process
	// that died with this pid — nobody is listening on it, so removing it
	// is safe and is the only way Listen can succeed.
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	s := &Server{
		root:     root,
		path:     path,
		ln:       ln,
		handler:  handler,
		conns:    make(map[net.Conn]struct{}),
		closeSig: make(chan struct{}),
	}
	go s.accept()
	return s, nil
}

// Root reports the project root this server answers for.
func (s *Server) Root() string { return s.root }

// Path reports the socket file this server is bound to. Exported for
// tests and for the status surfaces — knowing the socket exists is how a
// user confirms `ced --remote` has somewhere to go.
func (s *Server) Path() string { return s.path }

// accept runs the listener loop until Close. Each connection gets its
// own goroutine so a client that asked to wait can block for as long as
// the user takes without stalling the next one.
func (s *Server) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return // listener closed, or unrecoverable — either way we're done
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			return
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		go s.serve(conn)
	}
}

// serve handles one connection: read a single request, answer it, and —
// for a wait request — hold the line open until the editor says it is
// done with the file.
func (s *Server) serve(conn net.Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()

	// A read deadline on the REQUEST only. Once we're waiting on the
	// editor there is no deadline that could be right — the user is
	// writing a commit message, and how long that takes is up to them.
	_ = conn.SetReadDeadline(time.Now().Add(probeTimeout))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		writeLine(conn, response{Err: "bad request"})
		return
	}

	switch req.Op {
	case "hello":
		writeLine(conn, response{Root: s.root, OK: true})
	case "open":
		done, err := s.handler(req.Path, req.Wait)
		if err != nil {
			writeLine(conn, response{Root: s.root, Err: err.Error()})
			return
		}
		if !writeLine(conn, response{Root: s.root, OK: true}) {
			return
		}
		if !req.Wait || done == nil {
			return
		}
		// Two ways this ends, and both mean "stop waiting": the editor
		// closed the file, or the server itself is shutting down. The
		// second is what stops a `ced --wait` from hanging forever when
		// the editor it was waiting on quits.
		select {
		case <-done:
		case <-s.closeSig:
		}
		writeLine(conn, response{OK: true, Done: true})
	default:
		writeLine(conn, response{Err: "unknown op " + req.Op})
	}
}

// writeLine encodes one response as a JSON line. Reports whether the
// write landed so a caller can stop early on a client that hung up.
func writeLine(conn net.Conn, r response) bool {
	b, err := json.Marshal(r)
	if err != nil {
		return false
	}
	_, err = conn.Write(append(b, '\n'))
	return err == nil
}

// Close stops accepting, releases every waiting client, and unlinks the
// socket file. Safe to call more than once.
//
// Releasing the waiters is the load-bearing half: a client blocked on
// "tell me when you're done with this file" would otherwise sit there
// until it was killed, and since that client is usually `git commit`,
// the shell it was launched from would sit there too.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.closeSig)
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	err := s.ln.Close()
	// Give the waiters a moment to write their done line before the
	// connections are torn down under them; anything still open after
	// that is a client that stopped reading.
	time.Sleep(10 * time.Millisecond)
	for _, c := range conns {
		_ = c.Close()
	}
	_ = os.Remove(s.path)
	return err
}

// -----------------------------------------------------------------------------
// The client side
// -----------------------------------------------------------------------------

// Open hands path to a running instance that owns it. With wait set it
// blocks until that instance is finished with the file — the editor
// closed the tab, or exited.
//
// Returns ErrNoInstance when nothing is listening for a root containing
// path; the caller's answer to that is to start a normal editor.
func Open(path string, wait bool) error {
	target := normalizeFile(path)
	sock, err := findInstance(target)
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("unix", sock, probeTimeout)
	if err != nil {
		return ErrNoInstance
	}
	defer func() { _ = conn.Close() }()

	if !writeRequest(conn, request{Op: "open", Path: target, Wait: wait}) {
		return errors.New("remote: could not send the open request")
	}
	rd := bufio.NewReader(conn)
	ack, err := readResponse(rd, probeTimeout, conn)
	if err != nil {
		return err
	}
	if !ack.OK {
		if ack.Err != "" {
			return errors.New("remote: " + ack.Err)
		}
		return errors.New("remote: the editor refused the file")
	}
	if !wait {
		return nil
	}
	// No deadline from here on: the user is editing. A closed connection
	// (the editor exited) reads as EOF and means the same thing as the
	// done line — stop waiting — so it is not an error.
	if _, err := readResponse(rd, 0, conn); err != nil {
		return nil
	}
	return nil
}

// findInstance probes every socket in the runtime directory and returns
// the one whose root contains target, preferring the most specific root
// when several match (a repo opened alongside a subdirectory of itself).
//
// Sockets nobody answers are unlinked as we go: they belong to instances
// that crashed, and leaving them would make every future probe pay for
// them again.
func findInstance(target string) (string, error) {
	dir := socketDirFn()
	if dir == "" {
		return "", ErrNoInstance
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ErrNoInstance
	}
	// Deterministic order so a tie between two instances on the same
	// root resolves the same way every time rather than by readdir luck.
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sock") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	best, bestLen := "", -1
	for _, name := range names {
		sock := filepath.Join(dir, name)
		root, ok := probeRoot(sock)
		if !ok {
			_ = os.Remove(sock)
			continue
		}
		if !contains(root, target) {
			continue
		}
		if len(root) > bestLen {
			best, bestLen = sock, len(root)
		}
	}
	if best == "" {
		return "", ErrNoInstance
	}
	return best, nil
}

// probeRoot asks one socket which project root it serves. The bool is
// false for anything that isn't a live ced instance — a refused dial, a
// timeout, a malformed answer — which is the caller's cue to unlink it.
func probeRoot(sock string) (string, bool) {
	conn, err := net.DialTimeout("unix", sock, probeTimeout)
	if err != nil {
		return "", false
	}
	defer func() { _ = conn.Close() }()
	if !writeRequest(conn, request{Op: "hello"}) {
		return "", false
	}
	resp, err := readResponse(bufio.NewReader(conn), probeTimeout, conn)
	if err != nil || !resp.OK || resp.Root == "" {
		return "", false
	}
	return resp.Root, true
}

// writeRequest encodes one request as a JSON line.
func writeRequest(conn net.Conn, req request) bool {
	b, err := json.Marshal(req)
	if err != nil {
		return false
	}
	_ = conn.SetWriteDeadline(time.Now().Add(probeTimeout))
	_, err = conn.Write(append(b, '\n'))
	_ = conn.SetWriteDeadline(time.Time{})
	return err == nil
}

// readResponse reads one JSON line. A zero timeout means "block" — used
// for the wait leg, where the answer arrives whenever the user is done.
func readResponse(rd *bufio.Reader, timeout time.Duration, conn net.Conn) (response, error) {
	if timeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
		defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	}
	line, err := rd.ReadBytes('\n')
	if err != nil {
		return response{}, err
	}
	var resp response
	if err := json.Unmarshal(line, &resp); err != nil {
		return response{}, err
	}
	return resp, nil
}

// -----------------------------------------------------------------------------
// Path matching
// -----------------------------------------------------------------------------

// normalizeFile resolves path the same way the editor resolves its root,
// so the containment test compares like with like. The DIRECTORY is what
// gets symlink-resolved, not the file: `ced --wait` is routinely pointed
// at a file that doesn't exist yet (a new note, a fresh scratch file),
// and EvalSymlinks on a missing path answers nothing useful.
func normalizeFile(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	dir := session.Normalize(filepath.Dir(abs))
	if dir == "" {
		return abs
	}
	return filepath.Join(dir, filepath.Base(abs))
}

// Owns reports whether an instance rooted at root should accept path.
// Exported because the editor asks the same question again on the
// receiving side: a client already refuses to pick a mismatched
// instance, but a request that didn't come from ced's own CLI has to
// meet the same rule, and both sides must agree on what "inside the
// project" means down to the symlink resolution.
func Owns(root, path string) bool {
	return contains(session.Normalize(root), normalizeFile(path))
}

// contains reports whether target lies inside root. Lexical, on already-
// normalized paths, and it requires a separator at the boundary so
// /home/ro/proj does not claim a file in /home/ro/project-notes.
func contains(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if root == target {
		return true
	}
	if !strings.HasSuffix(root, string(filepath.Separator)) {
		root += string(filepath.Separator)
	}
	return strings.HasPrefix(target, root)
}
