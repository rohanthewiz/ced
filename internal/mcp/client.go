// =============================================================================
// File: internal/mcp/client.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// client.go is ced's own Model Context Protocol client — enough of the
// protocol to connect to a stdio server, see what tools it offers, and
// run one.
//
// It is deliberately thin, and it rides internal/lsp rather than an SDK
// for the same reason ACP does: MCP is JSON-RPC 2.0 with newline
// framing, which is exactly what that package's Client already speaks
// (lsp.StartNDJSON). A dependency here would buy nothing but a build
// graph. House rules it inherits from every other integration:
//
//   - Silent degradation. A server that isn't installed, won't start,
//     or answers garbage marks that ONE server unavailable with a
//     readable reason; the editor and every other server carry on.
//   - Events only, at the app layer. Nothing here touches editor state;
//     the app runs these calls on goroutines and posts tcell events.
//   - stdio only. http/sse servers parse and are handed to the chat
//     agent (which may support them), but ced's own client declines
//     them rather than half-implementing a second transport.
//
// Two protocol details worth knowing when reading this:
//
//   - The handshake is three steps, not two: initialize (request),
//     then a notifications/initialized notification. A server may
//     legally refuse everything until that notification lands.
//   - Servers make requests of the CLIENT too. ced answers ping and
//     roots/list (with the workspace root — that is how a server knows
//     which project it is looking at) and honestly declines the rest:
//     sampling and elicitation want an LLM and a user prompt this
//     client does not have.
package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/lsp"
	"github.com/rohanthewiz/ced/internal/version"
)

// protocolVersion is the MCP revision ced negotiates. Servers that speak
// an older revision answer with theirs and we proceed anyway — the
// handful of methods used here (tools/list, tools/call) are stable
// across every published revision, so refusing a mismatch would reject
// working servers for no benefit.
const protocolVersion = "2025-06-18"

const (
	// connectTimeout covers initialize. Generous on purpose: the common
	// launcher commands (npx, uvx, pipx) may download the server package
	// on first run, which is slow but not broken.
	connectTimeout = 60 * time.Second
	// listTimeout covers tools/list — a local lookup once the server is
	// up, so a wedged server shows as an error quickly.
	listTimeout = 15 * time.Second
	// toolTimeout covers tools/call. Tool work is arbitrary (a web
	// fetch, a database query), so this is a walk-away backstop, not an
	// expectation.
	toolTimeout = 120 * time.Second
)

// Tool is one entry from tools/list.
type Tool struct {
	Name        string
	Title       string
	Description string
	// InputSchema is the raw JSON Schema for the tool's arguments. Kept
	// raw: ced doesn't validate arguments (the server does, and its
	// error message is better than one we'd invent), it only shows the
	// schema so the user knows what to type.
	InputSchema json.RawMessage
}

// Label is the tool's display name — its title when the server gave
// one, otherwise the wire name.
func (t Tool) Label() string {
	if t.Title != "" {
		return t.Title
	}
	return t.Name
}

// ServerInfo is what the server said about itself during initialize.
type ServerInfo struct {
	Name     string
	Version  string
	Protocol string
	// Instructions is the server's optional "how to use me" blurb.
	Instructions string
	// HasTools reports whether the server advertised the tools
	// capability. A server without it is legal (it may serve only
	// resources or prompts) and simply has nothing for ced to run.
	HasTools bool
}

// ToolResult is one tools/call answer, flattened for display.
type ToolResult struct {
	// Text is every content block rendered to plain text, in order.
	Text string
	// IsError marks a tool that ran and reported failure — distinct
	// from a transport/protocol error, which comes back as a Go error.
	IsError bool
}

// Client is a live connection to one MCP server.
type Client struct {
	conn *lsp.Client
	info ServerInfo
	srv  Server
}

// dial launches the server process. A package var so tests can drive
// the protocol over in-memory pipes without spawning anything — the
// same seam NewClient gives the LSP package.
var dial = func(dir string, s Server,
	onRequest func(string, json.RawMessage) (any, error),
	onExit func(error)) (*lsp.Client, error) {
	return lsp.StartNDJSON(dir, s.Command, s.Args, s.EnvPairs(), nil, onRequest, onExit)
}

// Connect starts s and runs the MCP handshake, returning a ready
// client. dir is the working directory for the child process and root
// the workspace path reported to roots/list — both are ced's project
// root in practice; they're separate parameters so a caller can't
// accidentally pass a relative one for the URI (the malformed-rootUri
// bug the LSP layer already learned about).
//
// onExit fires when the server process dies, from the read-loop
// goroutine — post an event, don't touch app state.
func Connect(dir string, s Server, root string, onExit func(error)) (*Client, error) {
	if s.Transport != TransportStdio {
		return nil, fmt.Errorf("%s: ced connects to %s servers only (%s is declared to the chat agent instead)",
			s.Name, TransportStdio, s.Transport)
	}
	if _, err := exec.LookPath(s.Command); err != nil {
		return nil, fmt.Errorf("%s: %s not found on PATH", s.Name, s.Command)
	}

	c := &Client{srv: s}
	conn, err := dial(dir, s, serveRequest(root), onExit)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.Name, err)
	}
	c.conn = conn

	if err := c.handshake(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%s: %w", s.Name, err)
	}
	return c, nil
}

// serveRequest builds the server→client request handler for a
// connection rooted at root. Runs on its own goroutine per request (the
// lsp.Client contract) and must stay self-contained — it has no access
// to the editor, by design.
func serveRequest(root string) func(string, json.RawMessage) (any, error) {
	return func(method string, _ json.RawMessage) (any, error) {
		switch method {
		case "ping":
			// The spec's liveness check: an empty result IS the answer.
			return map[string]any{}, nil
		case "roots/list":
			// Declaring the roots capability obliges us to answer this,
			// and it's how a server scopes itself to the open project.
			return map[string]any{"roots": []map[string]any{{
				"uri":  lsp.PathToURI(root),
				"name": filepath.Base(root),
			}}}, nil
		}
		// sampling/createMessage and elicitation/create land here: both
		// need something ced hasn't got (an LLM of its own, a prompt
		// surface owned by this package). An honest method-not-found lets
		// the server fall back; a fabricated answer would not.
		return nil, fmt.Errorf("ced does not handle %s", method)
	}
}

// handshake runs initialize + notifications/initialized and records what
// the server reported about itself.
func (c *Client) handshake() error {
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			// listChanged false: ced re-lists on demand (the tool picker
			// is opened per use), so subscribing would only add a
			// notification nobody acts on.
			"roots": map[string]any{"listChanged": false},
		},
		"clientInfo": map[string]any{"name": "ced", "version": version.Version},
	}
	var res struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools *struct{} `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Instructions string `json:"instructions"`
	}
	if err := c.conn.CallWithTimeout("initialize", params, &res, connectTimeout); err != nil {
		return err
	}
	if err := c.conn.Notify("notifications/initialized", map[string]any{}); err != nil {
		return err
	}
	c.info = ServerInfo{
		Name:         res.ServerInfo.Name,
		Version:      res.ServerInfo.Version,
		Protocol:     res.ProtocolVersion,
		Instructions: res.Instructions,
		HasTools:     res.Capabilities.Tools != nil,
	}
	if c.info.Name == "" {
		c.info.Name = c.srv.Name
	}
	return nil
}

// Info reports what the server said about itself at handshake time.
func (c *Client) Info() ServerInfo { return c.info }

// Server returns the declaration this connection was built from.
func (c *Client) Server() Server { return c.srv }

// ListTools fetches the server's whole tool inventory, following
// pagination cursors. A server that advertised no tools capability is
// answered locally with an empty list rather than a protocol error —
// "this server has no tools" is a normal, sayable thing.
func (c *Client) ListTools() ([]Tool, error) {
	if !c.info.HasTools {
		return nil, nil
	}
	var out []Tool
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Tools []struct {
				Name        string          `json:"name"`
				Title       string          `json:"title"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := c.conn.CallWithTimeout("tools/list", params, &page, listTimeout); err != nil {
			return nil, err
		}
		for _, t := range page.Tools {
			if t.Name == "" {
				continue
			}
			out = append(out, Tool{
				Name:        t.Name,
				Title:       t.Title,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
		// A server that keeps handing back the same cursor would spin
		// here forever; treat a repeat as the end of the list.
		if page.NextCursor == "" || page.NextCursor == cursor {
			return out, nil
		}
		cursor = page.NextCursor
	}
}

// CallTool runs one tool and flattens its content blocks to text. args
// may be nil for a tool that takes none — the field is still sent as an
// empty object, which servers expect.
func (c *Client) CallTool(name string, args map[string]any) (ToolResult, error) {
	if args == nil {
		args = map[string]any{}
	}
	var res struct {
		Content           []contentBlock  `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	err := c.conn.CallWithTimeout("tools/call",
		map[string]any{"name": name, "arguments": args}, &res, toolTimeout)
	if err != nil {
		return ToolResult{}, err
	}
	out := ToolResult{Text: renderContent(res.Content), IsError: res.IsError}
	if out.Text == "" && len(res.StructuredContent) > 0 {
		// Structured-only results are legal; show them rather than an
		// empty box that reads as "nothing happened".
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, res.StructuredContent, "", "  "); err == nil {
			out.Text = pretty.String()
		} else {
			out.Text = string(res.StructuredContent)
		}
	}
	return out, nil
}

// contentBlock is one element of a tool result's content array. Only
// text is rendered verbatim; the rest are summarised (see
// renderContent).
type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	MimeType string `json:"mimeType"`
	Resource struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"resource"`
}

// renderContent flattens content blocks into the text the result modal
// shows. Non-text blocks become a one-line marker instead of being
// dropped: a tool that answered only with an image should not look like
// a tool that answered with nothing.
func renderContent(blocks []contentBlock) string {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "resource":
			if b.Resource.Text != "" {
				parts = append(parts, b.Resource.Text)
			} else {
				parts = append(parts, "[resource "+b.Resource.URI+"]")
			}
		case "resource_link":
			parts = append(parts, "[resource "+b.Resource.URI+"]")
		default:
			kind := b.Type
			if kind == "" {
				kind = "content"
			}
			if b.MimeType != "" {
				kind += " " + b.MimeType
			}
			parts = append(parts, "["+kind+"]")
		}
	}
	return strings.Join(parts, "\n")
}

// Close tears the connection and its process down. Idempotent, like the
// underlying client's.
func (c *Client) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}
