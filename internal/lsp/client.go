// =============================================================================
// File: internal/lsp/client.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-09
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// client.go is the JSON-RPC 2.0 transport under the LSP client: framing
// (Content-Length headers over stdio), request/response correlation,
// notification dispatch, and the handful of server→client requests a
// minimal client must answer for gopls not to stall.
//
//	main loop ──Call/Notify──► Client ──stdin──► gopls
//	    ▲                        │
//	    │   onNotify (goroutine) │◄──stdout── readLoop goroutine
//	    └── caller posts a tcell event; only the main loop touches App
//
// Thread model: Call and Notify are safe from any goroutine (writes are
// mutex-serialised). The read loop runs on its own goroutine and calls
// onNotify / resolves pending Calls from there — so onNotify must never
// touch editor state directly; the app layer posts custom tcell events
// instead, the same goroutine→main-loop bridge every background job in
// this codebase uses.

package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// callTimeout bounds how long a Call waits for the server's response.
// Definition and hover on a warm gopls answer in tens of milliseconds;
// five seconds covers a cold server still type-checking a big package
// without letting a wedged server leak goroutines forever.
const callTimeout = 5 * time.Second

// message is the JSON-RPC envelope, used for both directions. Which
// fields are set determines the shape: ID+Method = request,
// ID+Result/Error = response, Method alone = notification.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *respError      `json:"error,omitempty"`
}

// respError is the JSON-RPC error object of a failed response.
type respError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Client is a JSON-RPC connection to one language server process.
type Client struct {
	writeMu sync.Mutex
	w       io.Writer
	r       *bufio.Reader

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan *message
	closed  bool

	// ndjson selects newline-delimited JSON framing instead of the LSP
	// Content-Length headers. The Agent Client Protocol (ACP) speaks
	// the same JSON-RPC 2.0 envelope but frames each message as one
	// line — the only wire difference, which is why ACP rides this
	// client rather than getting a transport of its own. Set only by
	// the ACP constructors; zero value keeps LSP framing.
	ndjson bool

	// onNotify receives server→client notifications (method + raw
	// params). Called on the read-loop goroutine — implementations must
	// hand off to their own event loop, not mutate shared state.
	onNotify func(method string, params json.RawMessage)

	// onRequest, when set, answers server→client REQUESTS (id +
	// method): its return value is marshalled into the response, or its
	// error into a JSON-RPC error object. Each invocation runs on its
	// OWN goroutine (never the read loop), so a handler may block while
	// it waits for an answer — a permission prompt waits on the user —
	// without stalling notification delivery. It still must not mutate
	// app state directly; post events and wait on a reply channel. Nil
	// keeps the built-in LSP auto-responder (empty
	// workspace/configuration, null for the rest); ACP sets it because
	// its server requests (permission prompts, fs access) carry real
	// semantics a null can't answer.
	//
	// A hook may DECLINE one method and keep the auto-responder for the
	// rest by returning ErrRequestUnhandled — see that sentinel. ACP
	// hooks never do; the LSP hook does, because it exists to answer
	// exactly one request (workspace/applyEdit) and the auto-responder's
	// workspace/configuration answer is load-bearing.
	onRequest func(method string, params json.RawMessage) (any, error)

	// onExit fires once when the read loop ends (server exited, pipe
	// closed, or protocol error). Also called from the read-loop
	// goroutine.
	onExit func(err error)

	// cmd is the spawned server process when the client came from
	// Start; nil for clients built over arbitrary pipes (tests).
	cmd *exec.Cmd

	// caps is the server's declared capability set, captured from the
	// initialize response. Only one member is read today — completion's
	// trigger characters — and it is read because completion is the
	// first verb whose TRIGGER is the server's business rather than the
	// editor's: which characters open the popup is a language fact
	// (`.` in Go, `->` and `::` in C++), and hard-coding it here would
	// be the editor guessing at the server's job.
	//
	// Guarded by mu with the rest of the mutable state: Initialize runs
	// on the start goroutine and the accessors are called from the main
	// loop.
	caps serverCapabilities
}

// serverCapabilities is the slice of the initialize response this client
// keeps. Everything else the server declares is either implied by the
// requests ced sends (it finds out by asking) or unused.
type serverCapabilities struct {
	Completion struct {
		TriggerChars   []string `json:"triggerCharacters"`
		ResolveProvide bool     `json:"resolveProvider"`
	} `json:"completionProvider"`
}

// initializeResult is the response envelope Initialize decodes.
type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
}

// CompletionTriggerChars returns the characters the server asked to be
// re-consulted on. Empty (including "the server declared no completion
// provider at all") means the caller falls back to its own minimal set —
// see the app layer's completionTriggerChars.
func (c *Client) CompletionTriggerChars() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.caps.Completion.TriggerChars...)
}

// CompletionResolves reports whether the server answers
// completionItem/resolve. A false here is not a degradation: resolve
// only ever enriches the detail pane (see ResolveCompletion).
func (c *Client) CompletionResolves() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps.Completion.ResolveProvide
}

// NewClient wraps an existing reader/writer pair (the server's stdout /
// stdin) and starts the read loop. Split from Start so tests can drive
// the protocol over in-memory pipes without spawning a process.
func NewClient(r io.Reader, w io.Writer, onNotify func(string, json.RawMessage), onExit func(error)) *Client {
	c := &Client{
		w:        w,
		r:        bufio.NewReader(r),
		pending:  map[int64]chan *message{},
		onNotify: onNotify,
		onExit:   onExit,
	}
	go c.readLoop()
	return c
}

// Start launches the server binary with args in dir and returns a
// Client wired to its stdio. The caller should have verified the
// binary exists (exec.LookPath) — a missing binary errors here too,
// but checking first keeps "gopls not installed" a silent no-op
// instead of a surfaced failure.
func Start(dir, bin string, args []string, onNotify func(string, json.RawMessage), onExit func(error)) (*Client, error) {
	return StartWithRequests(dir, bin, args, onNotify, nil, onExit)
}

// StartWithRequests is Start for a client that must answer server→client
// REQUESTS as well as notifications — ced's gopls connection does, because
// workspace/applyEdit is how a command-only code action delivers its edit.
//
// It is a separate constructor rather than a parameter on Start for the
// reason NewClientACP is: the hook has to be in place BEFORE the read loop
// starts, so it cannot be assigned afterwards without racing the very
// goroutine that reads it. A hook that declines a method with
// ErrRequestUnhandled leaves the built-in auto-responder in charge of it.
func StartWithRequests(dir, bin string, args []string,
	onNotify func(string, json.RawMessage),
	onRequest func(string, json.RawMessage) (any, error),
	onExit func(error),
) (*Client, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	// The server's stderr goes to our stderr, which tcell has taken
	// over — effectively /dev/null. Deliberate: gopls logs are noise
	// for an editor user, and capturing them would need another drain
	// goroutine for no user-visible benefit.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{
		w:         stdin,
		r:         bufio.NewReader(stdout),
		pending:   map[int64]chan *message{},
		onNotify:  onNotify,
		onRequest: onRequest,
		onExit:    onExit,
		cmd:       cmd,
	}
	go c.readLoop()
	return c, nil
}

// Call sends a request and blocks until the response arrives, then
// unmarshals its result into result (skipped when result is nil).
// Times out after callTimeout so a wedged server can't hang the
// calling goroutine forever. Safe from any goroutine.
func (c *Client) Call(method string, params, result any) error {
	return c.CallWithTimeout(method, params, result, callTimeout)
}

// CallWithTimeout is Call with an explicit response deadline. It exists
// for requests that legitimately outlive the 5s LSP budget — the
// Copilot device-flow confirmation blocks server-side until the user
// finishes authenticating in a browser, which takes minutes. Callers
// that can't say why they need longer should use Call.
func (c *Client) CallWithTimeout(method string, params, result any, timeout time.Duration) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("lsp: connection closed")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan *message, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.send(&message{JSONRPC: "2.0", ID: &id, Method: method, Params: marshalParams(params)}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}

	select {
	case resp := <-ch:
		if resp == nil {
			return fmt.Errorf("lsp: connection closed")
		}
		if resp.Error != nil {
			return fmt.Errorf("lsp: %s: %s (code %d)", method, resp.Error.Message, resp.Error.Code)
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("lsp: %s timed out", method)
	}
}

// Notify sends a fire-and-forget notification. Safe from any goroutine.
func (c *Client) Notify(method string, params any) error {
	return c.send(&message{JSONRPC: "2.0", Method: method, Params: marshalParams(params)})
}

// Close tears the connection down: best-effort shutdown/exit handshake
// when a process is attached, then kill as a backstop so a deaf server
// can't outlive the editor. Idempotent.
func (c *Client) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()

	// Polite LSP goodbye. Fire-and-forget notifications only — a full
	// shutdown Call would block Close for callTimeout on a wedged
	// server, and the editor is exiting either way.
	_ = c.Notify("exit", nil)
	if wc, ok := c.w.(io.Closer); ok {
		_ = wc.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		// Reap on a goroutine so Close never blocks on a slow exit;
		// kill after a grace period if the exit notification wasn't
		// enough.
		proc := c.cmd
		go func() {
			done := make(chan struct{})
			go func() { _ = proc.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = proc.Process.Kill()
				<-done
			}
		}()
	}
}

// send frames and writes one message. Serialised by writeMu so
// concurrent Calls/Notifies can't interleave their bytes. Framing
// follows the connection's dialect: Content-Length headers for LSP,
// one JSON object per line for ACP.
func (c *Client) send(m *message) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.ndjson {
		// json.Marshal never emits a raw newline inside an object, so
		// body+"\n" is exactly one well-formed ndjson record.
		if _, err := c.w.Write(body); err != nil {
			return err
		}
		_, err = c.w.Write([]byte("\n"))
		return err
	}
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.w.Write(body)
	return err
}

// marshalParams pre-encodes params so the envelope marshal can't fail
// halfway. nil params stay nil (the field is omitempty).
func marshalParams(params any) json.RawMessage {
	if params == nil {
		return nil
	}
	b, err := json.Marshal(params)
	if err != nil {
		// Params are always our own structs; a marshal failure is a
		// programming error. Sending null keeps the wire valid.
		return json.RawMessage("null")
	}
	return b
}

// readLoop drains server messages until the pipe closes, routing each
// to the pending Call it answers, the notification callback, or the
// server-request auto-responder.
func (c *Client) readLoop() {
	var loopErr error
	for {
		m, err := c.read()
		if err != nil {
			if err != io.EOF {
				loopErr = err
			}
			break
		}
		switch {
		case m.ID != nil && m.Method != "":
			// Server→client request. A minimal client still has to
			// answer these or gopls blocks waiting (it really does —
			// workspace/configuration gates type-checking).
			c.respondToServer(m)
		case m.ID != nil:
			c.mu.Lock()
			ch := c.pending[*m.ID]
			delete(c.pending, *m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		case m.Method != "":
			if c.onNotify != nil {
				c.onNotify(m.Method, m.Params)
			}
		}
	}

	// Connection over — fail every in-flight Call so nothing blocks
	// until its timeout, and let the owner know.
	c.mu.Lock()
	c.closed = true
	for id, ch := range c.pending {
		delete(c.pending, id)
		ch <- nil
	}
	c.mu.Unlock()
	if c.onExit != nil {
		c.onExit(loopErr)
	}
}

// read parses one incoming message in the connection's framing
// dialect — Content-Length headers for LSP, one line per message for
// ACP.
func (c *Client) read() (*message, error) {
	if c.ndjson {
		return readLineMessage(c.r)
	}
	return readMessage(c.r)
}

// ErrRequestUnhandled is what an onRequest hook returns to say "not my
// method" — the request then falls through to the built-in auto-responder
// as though no hook were installed.
//
// It exists because the LSP hook is NARROW. A client that installs a hook
// to answer workspace/applyEdit would otherwise also inherit
// workspace/configuration, which gopls BLOCKS on while type-checking:
// answering it with a null (the honest default for a method a handler
// doesn't know) wedges the server on the first file opened. Declining is
// the difference between adding one capability and taking over the whole
// server-request surface.
var ErrRequestUnhandled = errors.New("lsp: request not handled")

// respondToServer answers a server→client request. An installed onRequest
// hook gets first refusal — result on success, a JSON-RPC error object on
// failure, or ErrRequestUnhandled to fall through. The built-in LSP
// auto-responder then applies: the emptiest legal payload.
// workspace/configuration must echo one element per requested item
// (gopls waits on it); everything else — registration, progress-token
// creation, message requests — accepts a null result.
func (c *Client) respondToServer(m *message) {
	if c.onRequest != nil {
		// Each hook invocation gets its own goroutine: ACP request
		// handlers legitimately block for a long time (a permission
		// prompt waits on the user's answer), and the read loop must
		// keep draining streamed notifications and Call responses
		// meanwhile. JSON-RPC correlates responses by id, so replying
		// out of order is legal — and send is writeMu-serialised, so
		// concurrent replies can't interleave bytes.
		go func() {
			res, err := c.onRequest(m.Method, m.Params)
			switch {
			case errors.Is(err, ErrRequestUnhandled):
				c.autoRespond(m)
			case err != nil:
				// -32601 (method not found) is the honest default for a
				// request this client declines to handle; the message
				// says why.
				_ = c.send(&message{JSONRPC: "2.0", ID: m.ID,
					Error: &respError{Code: -32601, Message: err.Error()}})
			default:
				raw, _ := json.Marshal(res)
				_ = c.send(&message{JSONRPC: "2.0", ID: m.ID, Result: raw})
			}
		}()
		return
	}
	c.autoRespond(m)
}

// autoRespond is the built-in answer for a server request nothing else
// handles: the emptiest payload the spec allows.
func (c *Client) autoRespond(m *message) {
	var result any
	if m.Method == "workspace/configuration" {
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(m.Params, &p)
		empties := make([]any, len(p.Items))
		for i := range empties {
			empties[i] = map[string]any{}
		}
		result = empties
	}
	raw, _ := json.Marshal(result)
	_ = c.send(&message{JSONRPC: "2.0", ID: m.ID, Result: raw})
}

// readMessage parses one Content-Length-framed JSON-RPC message.
// Unknown headers (Content-Type) are skipped; a missing or malformed
// Content-Length is a hard protocol error — there is no way to
// resynchronise a byte stream once framing is lost.
func readMessage(r *bufio.Reader) (*message, error) {
	contentLen := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // blank line ends the header block
		}
		if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("lsp: bad Content-Length %q", v)
			}
			contentLen = n
		}
	}
	if contentLen < 0 {
		return nil, fmt.Errorf("lsp: missing Content-Length header")
	}
	body := make([]byte, contentLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var m message
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("lsp: bad message body: %w", err)
	}
	return &m, nil
}

// -----------------------------------------------------------------------------
// LSP-level convenience wrappers
// -----------------------------------------------------------------------------

// Initialize runs the initialize → initialized handshake for the
// workspace rooted at rootDir. The capability set is the minimal
// honest one: full-text sync, plaintext-preferred hover, and the
// publishDiagnostics / definition defaults.
func (c *Client) Initialize(rootDir string) error {
	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   PathToURI(rootDir),
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"synchronization":    map[string]any{"didSave": true},
				"publishDiagnostics": map[string]any{},
				"definition":         map[string]any{},
				"references":         map[string]any{},
				// Rename is declared bare, without prepareSupport. That
				// option asks the server to validate a position and hand
				// back a placeholder BEFORE the user types a new name, and
				// it buys a round trip ced doesn't need: the word under the
				// cursor already seeds the prompt (cursorWord), and gopls
				// refuses an illegal rename with a better message than a
				// prepare step's silence. Declaring it would also mean
				// handling the request's three response shapes for a check
				// the rename itself repeats.
				"rename": map[string]any{},
				// codeActionLiteralSupport is what makes a server send
				// CodeAction objects (title + kind + a ready-made edit)
				// instead of the 3.8 bare-Command shape, which could only
				// ever be executed blind. The valueSet is the families ced
				// asks about; it is a HINT, not a filter — a server may
				// still answer with kinds outside it, and ParseCodeActions
				// takes whatever arrives.
				//
				// resolveSupport and dataSupport are deliberately absent,
				// the same trade rename makes with prepareSupport. Declaring
				// them tells the server to send actions with NO edit and
				// wait for a codeAction/resolve round trip; not declaring
				// them makes it compute the edits up front, so a picked row
				// applies immediately instead of asking again. The cost is
				// that the server does more work for actions nobody picks —
				// which is its own cheapest work, and it buys away a whole
				// second request shape plus the "the row did nothing"
				// failure it would introduce.
				"codeAction": map[string]any{
					"codeActionLiteralSupport": map[string]any{
						"codeActionKind": map[string]any{
							"valueSet": []string{"quickfix", "refactor", "source"},
						},
					},
				},
				// Hierarchical symbols are the shape worth asking for:
				// gopls nests a type's methods under it, which is what
				// makes the "go to symbol" picker read like an outline
				// rather than an alphabet soup. Servers that can't
				// oblige answer with the flat form anyway, and
				// ParseDocumentSymbols takes either.
				"documentSymbol": map[string]any{
					"hierarchicalDocumentSymbolSupport": true,
				},
				// Plaintext first here too: the tooltip that renders this
				// is the hover modal, which is a dumb text box.
				"signatureHelp": map[string]any{
					"signatureInformation": map[string]any{
						"documentationFormat": []string{"plaintext", "markdown"},
					},
				},
				"hover": map[string]any{
					// Plaintext first: the hover modal is a dumb text
					// box, and gopls honours the preference order.
					"contentFormat": []string{"plaintext", "markdown"},
				},
				// Completion. Three declarations, each of which changes
				// what the server sends, and each chosen so that what
				// arrives is something ced can actually honour:
				//
				//   - snippetSupport:false. A snippet's `${1:name}` tab
				//     stops need an expansion engine with its own modal
				//     keyboard mode; ced has none, so declaring support
				//     would mean writing placeholder syntax into the
				//     user's buffer. Declining gets literal text
				//     instead, which is exactly what the popup inserts.
				//   - documentationFormat plaintext-first, for the same
				//     reason hover and signature help ask for it: the
				//     detail pane is a dumb text box.
				//   - resolveSupport is ABSENT, the same trade codeAction
				//     makes above. Declaring it tells the server it may
				//     defer additionalTextEdits to a resolve round trip —
				//     and additionalTextEdits is the auto-import, the
				//     single most valuable thing an accepted completion
				//     does. Not declaring it makes the server compute
				//     those edits up front, so an accept applies
				//     immediately and can never half-land while a second
				//     request is in flight. completionItem/resolve is
				//     still WIRED (see ResolveCompletion), but only ever
				//     to enrich documentation for the row being looked
				//     at — never for correctness.
				//
				// contextSupport is what lets the request say WHY it
				// fired (typed a '.', invoked deliberately, re-asking an
				// incomplete list); gopls answers differently for each.
				"completion": map[string]any{
					"contextSupport": true,
					"completionItem": map[string]any{
						"snippetSupport":      false,
						"deprecatedSupport":   true,
						"preselectSupport":    true,
						"documentationFormat": []string{"plaintext", "markdown"},
					},
				},
			},
			// The workspace-edit declaration is what stops a server from
			// asking for something this client would have to refuse after
			// the fact. documentChanges asks for the modern array shape —
			// it is the only one carrying document versions, which is what
			// a staleness check compares against — while the deliberately
			// EMPTY resourceOperations list says, honestly, that ced cannot
			// create, rename or delete files on a server's behalf. A
			// conforming server then declines a package rename ITSELF, with
			// its own reason, before anything has been applied. That is a
			// far better message than one ced could synthesise afterwards.
			"workspace": map[string]any{
				"workspaceEdit": map[string]any{
					"documentChanges":    true,
					"resourceOperations": []string{},
				},
				// applyEdit says this client will answer a
				// workspace/applyEdit REQUEST, which is the only way a
				// command-only code action can change anything: the command
				// runs server-side and the edit comes back unprompted.
				// Without it a conforming server has no route to apply what
				// it just computed, and those actions become rows that
				// quietly do nothing.
				"applyEdit": true,
				// executeCommand needs no options; declaring it is how the
				// server learns those commands are runnable at all.
				"executeCommand": map[string]any{},
			},
		},
	}
	// The response IS read now, where it used to be discarded: completion
	// is the first verb whose trigger set belongs to the server (see
	// serverCapabilities). A response that fails to decode is not fatal —
	// the handshake succeeded, and an empty capability set degrades to
	// the caller's fallback trigger characters.
	var res initializeResult
	if err := c.Call("initialize", params, &res); err != nil {
		return err
	}
	c.mu.Lock()
	c.caps = res.Capabilities
	c.mu.Unlock()
	return c.Notify("initialized", map[string]any{})
}

// DidOpen announces a newly-opened document with its full text.
func (c *Client) DidOpen(path, languageID string, version int, text string) error {
	return c.Notify("textDocument/didOpen", DidOpenParams{
		TextDocument: TextDocumentItem{
			URI:        PathToURI(path),
			LanguageID: languageID,
			Version:    version,
			Text:       text,
		},
	})
}

// DidChange sends the document's full new text (see DidChangeParams
// for why full sync).
func (c *Client) DidChange(path string, version int, text string) error {
	return c.Notify("textDocument/didChange", DidChangeParams{
		TextDocument:   VersionedTextDocumentIdentifier{URI: PathToURI(path), Version: version},
		ContentChanges: []ContentChange{{Text: text}},
	})
}

// DidSave announces a document was written to disk.
func (c *Client) DidSave(path string) error {
	return c.Notify("textDocument/didSave", DidSaveParams{
		TextDocument: TextDocumentIdentifier{URI: PathToURI(path)},
	})
}

// DidClose announces a document is no longer open in the editor.
func (c *Client) DidClose(path string) error {
	return c.Notify("textDocument/didClose", DidCloseParams{
		TextDocument: TextDocumentIdentifier{URI: PathToURI(path)},
	})
}

// Definition asks where the symbol at pos is defined. Servers may
// answer with a single Location, an array, or null; all normalise to a
// (possibly empty) slice here so callers only handle one shape.
func (c *Client) Definition(path string, pos Position) ([]Location, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: PathToURI(path)},
		Position:     pos,
	}
	var raw json.RawMessage
	if err := c.Call("textDocument/definition", params, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var many []Location
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, nil
	}
	var one Location
	if err := json.Unmarshal(raw, &one); err == nil {
		return []Location{one}, nil
	}
	return nil, nil
}

// referencesTimeout is the response deadline for textDocument/references.
// Longer than the 5s general budget because this is the one verb whose
// cost scales with the PROJECT rather than the file: gopls has to type-
// check every package that could import the symbol, and on a cold server
// in a large module that legitimately runs past five seconds. Definition
// and hover answer from the file's own package and keep the default.
const referencesTimeout = 30 * time.Second

// References asks for every use of the symbol at pos. includeDecl adds
// the declaration itself to the list.
//
// Unlike Definition, there is no shape normalisation to do: the spec
// allows only an array or null here, so a single object would be a
// server bug rather than a variant worth tolerating.
func (c *Client) References(path string, pos Position, includeDecl bool) ([]Location, error) {
	params := ReferenceParams{
		TextDocument: TextDocumentIdentifier{URI: PathToURI(path)},
		Position:     pos,
		Context:      ReferenceContext{IncludeDeclaration: includeDecl},
	}
	var raw json.RawMessage
	if err := c.CallWithTimeout("textDocument/references", params, &raw, referencesTimeout); err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var locs []Location
	if err := json.Unmarshal(raw, &locs); err != nil {
		return nil, err
	}
	return locs, nil
}

// renameTimeout is the response deadline for textDocument/rename.
//
// It borrows references' 30s budget for references' reason and then some:
// a rename IS a project-wide reference search — gopls has to type-check
// every package that could import the symbol — plus the work of building
// an edit for every file that search turned up. If any verb in this client
// deserves more than the 5s default, it is this one, and a timeout here is
// especially expensive to the user, who has already typed the new name.
const renameTimeout = 30 * time.Second

// Rename asks the server to rewrite every binding of the symbol at pos to
// newName, and returns the workspace edit it wants applied. Applying it is
// the caller's business (see app/workspaceedit.go); this only asks.
//
// A nil edit with a nil error is a REAL ANSWER, not a failure — it is what
// a server sends when the rename is legal but changes nothing. The two
// ways a server refuses are an RPC error, whose message carries the reason
// ("renaming this package is not supported"), and an edit that also names
// resource operations, which the app layer refuses by name because ced
// declared at initialize that it cannot perform them.
func (c *Client) Rename(path string, pos Position, newName string) (*WorkspaceEdit, error) {
	params := RenameParams{
		TextDocument: TextDocumentIdentifier{URI: PathToURI(path)},
		Position:     pos,
		NewName:      newName,
	}
	var raw json.RawMessage
	if err := c.CallWithTimeout("textDocument/rename", params, &raw, renameTimeout); err != nil {
		return nil, err
	}
	return ParseWorkspaceEdit(raw), nil
}

// executeCommandTimeout is the response deadline for
// workspace/executeCommand, and it is the longest in this client by a wide
// margin for a reason no other call has: THE USER IS INSIDE IT.
//
// A command-only code action runs server-side and then sends
// workspace/applyEdit back — a request ced answers by planning the edit and,
// when it reaches files the user never opened, asking them to confirm. The
// server is still blocked in executeCommand while that confirmation sits on
// screen, so the budget has to cover a human reading a dialog, not a
// type-check. Commands that shell out (a dependency upgrade, a code
// generator) spend real time too. The app layer's own wait on the applyEdit
// answer is deliberately shorter, so a user who walks away releases the
// server rather than the other way round.
const executeCommandTimeout = 2 * time.Minute

// CodeActions asks what the server can do to the given range: quick fixes
// for the diagnostics on it, refactorings, source-level transformations.
//
// diags are the client's copy of the server's diagnostics for that range,
// echoed back verbatim — that is how a quick fix is matched to the problem
// it fixes. The 5s default budget applies: unlike references and rename,
// this question is scoped to one file's own package.
func (c *Client) CodeActions(path string, rng Range, diags []Diagnostic) ([]CodeAction, error) {
	params := CodeActionParams{
		TextDocument: TextDocumentIdentifier{URI: PathToURI(path)},
		Range:        rng,
		Context:      CodeActionContext{Diagnostics: diags},
	}
	// The spec makes context.diagnostics required, and "none" is an empty
	// array rather than null — a server is entitled to refuse the latter.
	if params.Context.Diagnostics == nil {
		params.Context.Diagnostics = []Diagnostic{}
	}
	var raw json.RawMessage
	if err := c.Call("textDocument/codeAction", params, &raw); err != nil {
		return nil, err
	}
	return ParseCodeActions(raw), nil
}

// ExecuteCommand runs one of the server's own commands — the second half of
// a code action that carried no edit of its own.
//
// The result is discarded deliberately: a command's return value is
// server-private, and the part that concerns the editor arrives separately
// as a workspace/applyEdit request DURING this call. What this reports is
// only whether the command itself failed.
func (c *Client) ExecuteCommand(cmd string, args []json.RawMessage) error {
	return c.CallWithTimeout("workspace/executeCommand",
		ExecuteCommandParams{Command: cmd, Arguments: args}, nil, executeCommandTimeout)
}

// SignatureHelpAt asks which callable the position sits inside and which
// of its parameters is being typed, normalised to the flat editor-facing
// form (see ParseSignatureHelp). A nil result with a nil error means the
// position is not inside a call — a real answer, not a failure.
func (c *Client) SignatureHelpAt(path string, pos Position) (*Signature, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: PathToURI(path)},
		Position:     pos,
	}
	var raw json.RawMessage
	if err := c.Call("textDocument/signatureHelp", params, &raw); err != nil {
		return nil, err
	}
	return ParseSignatureHelp(raw), nil
}

// DocumentSymbols asks for every symbol declared in path, normalised to
// the flat editor-facing form (see ParseDocumentSymbols for why both
// response shapes collapse here rather than at the call site). A nil
// slice with a nil error means "this document declares nothing" — which
// is a real answer for an empty file, not a failure.
func (c *Client) DocumentSymbols(path string) ([]Symbol, error) {
	params := DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: PathToURI(path)},
	}
	var raw json.RawMessage
	if err := c.Call("textDocument/documentSymbol", params, &raw); err != nil {
		return nil, err
	}
	return ParseDocumentSymbols(raw), nil
}

// HoverAt asks for hover documentation at pos. A nil result with nil
// error means "the server has nothing to say here".
func (c *Client) HoverAt(path string, pos Position) (*Hover, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: PathToURI(path)},
		Position:     pos,
	}
	var raw json.RawMessage
	if err := c.Call("textDocument/hover", params, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var h Hover
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, err
	}
	return &h, nil
}
