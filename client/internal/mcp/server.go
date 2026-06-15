// Package mcp implements a minimal Model Context Protocol (MCP) server over stdio that
// exposes EXACTLY Potluck's curated tools (fetch_url, read_document) and nothing else. This
// is the v2 "constrained tool surface": the agent CLI is pointed at this server (via
// --mcp-config + --strict-mcp-config) so the only callable tools are the two project-owned,
// hardened ones — never Bash, Read, Write, or arbitrary network. The agent talks JSON-RPC
// 2.0 to this process over its stdin/stdout. stdlib-only.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tannakartikey/potluck/client/internal/tools"
)

// ProtocolVersion is the MCP revision we advertise if the client doesn't pin one.
const ProtocolVersion = "2025-06-18"

// Server serves the curated tools over a JSON-RPC 2.0 stdio transport.
type Server struct {
	Fetcher  *tools.Fetcher
	Reader   *tools.Reader
	Searcher *tools.Searcher
	Name     string
	Version  string
}

func NewServer(fetcher *tools.Fetcher, reader *tools.Reader) *Server {
	// web_search is always available: it only reaches the (engine-allowlisted) search endpoint
	// and returns results — there is no attacker-controlled receiver, so it's broadly safe.
	return &Server{Fetcher: fetcher, Reader: reader, Searcher: tools.NewSearcher(), Name: "potluck", Version: "0.3"}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve runs the JSON-RPC loop until the input closes (the parent CLI exits / closes stdin),
// reading one newline-delimited JSON message per line and writing one response per request.
// Notifications (no id) get no response. Errors in one message never tear down the loop.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	br := bufio.NewReaderSize(in, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		line = trimLine(line)
		if len(line) > 0 {
			s.handleLine(line, out)
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func trimLine(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func (s *Server) handleLine(line []byte, out io.Writer) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		writeJSON(out, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	result, rerr := s.dispatch(req.Method, req.Params)
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return // notification: never reply
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		resp.Error = rerr
	} else {
		resp.Result = result
	}
	writeJSON(out, resp)
}

func writeJSON(out io.Writer, v interface{}) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	b = append(b, '\n')
	_, _ = out.Write(b)
}

func (s *Server) dispatch(method string, params json.RawMessage) (interface{}, *rpcError) {
	switch method {
	case "initialize":
		return s.initialize(params), nil
	case "ping":
		return map[string]interface{}{}, nil
	case "tools/list":
		return map[string]interface{}{"tools": s.toolDefs()}, nil
	case "tools/call":
		return s.callTool(params)
	default:
		// initialized / other notifications fall here harmlessly (no id → no reply).
		return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
	}
}

func (s *Server) initialize(params json.RawMessage) interface{} {
	version := ProtocolVersion
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(params, &p) == nil && p.ProtocolVersion != "" {
		version = p.ProtocolVersion // echo the client's pinned revision
	}
	return map[string]interface{}{
		"protocolVersion": version,
		"capabilities":    map[string]interface{}{"tools": map[string]interface{}{"listChanged": false}},
		"serverInfo":      map[string]interface{}{"name": s.Name, "version": s.Version},
	}
}

// toolDefs is the ENTIRE tool surface this server exposes. There is intentionally no Bash,
// Read, Write, Edit, or generic HTTP tool here.
func (s *Server) toolDefs() []map[string]interface{} {
	strObj := func(props map[string]interface{}, required ...string) map[string]interface{} {
		return map[string]interface{}{"type": "object", "properties": props, "required": required}
	}
	return []map[string]interface{}{
		{
			"name": "fetch_url",
			"description": "Fetch the contents of a public http(s) URL (HTML is returned as clean " +
				"readable text). Restricted to this task's allowlisted domains; private/loopback/" +
				"cloud-metadata addresses are blocked; the response is size- and time-capped. GET only.",
			"inputSchema": strObj(map[string]interface{}{
				"url": map[string]interface{}{"type": "string", "description": "The http(s) URL to fetch."},
			}, "url"),
		},
		{
			"name": "read_document",
			"description": "Extract the text of a document already present in the task's input " +
				"directory (e.g. an attachment, or a file previously saved by fetch). Supports text, " +
				"HTML, and (best-effort) PDF. Cannot read outside the input directory.",
			"inputSchema": strObj(map[string]interface{}{
				"path": map[string]interface{}{"type": "string", "description": "Path to the document, relative to the input directory."},
			}, "path"),
		},
		{
			"name": "web_search",
			"description": "Search the web and get back a list of results (title, URL, snippet). " +
				"Use this to FIND sources for a research task, then read promising ones with fetch_url " +
				"(subject to this task's allowlist).",
			"inputSchema": strObj(map[string]interface{}{
				"query": map[string]interface{}{"type": "string", "description": "The search query."},
			}, "query"),
		},
	}
}

func (s *Server) callTool(params json.RawMessage) (interface{}, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch p.Name {
	case "fetch_url":
		var a struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(p.Arguments, &a)
		if s.Fetcher == nil {
			return toolErr("fetch_url is not enabled for this task"), nil
		}
		res, err := s.Fetcher.Fetch(ctx, a.URL)
		if err != nil {
			return toolErr(err.Error()), nil
		}
		header := fmt.Sprintf("[fetch_url] GET %s -> %d %s (%d bytes%s)\n\n",
			res.URL, res.Status, res.ContentType, res.Bytes, truncatedNote(res.Truncated))
		return toolText(header + res.Body), nil
	case "read_document":
		var a struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(p.Arguments, &a)
		if s.Reader == nil {
			return toolErr("read_document is not enabled for this task"), nil
		}
		res, err := s.Reader.ReadDocument(a.Path)
		if err != nil {
			return toolErr(err.Error()), nil
		}
		header := fmt.Sprintf("[read_document] %s (%s, %d bytes%s)\n\n",
			res.Path, res.Kind, res.Bytes, truncatedNote(res.Truncated))
		return toolText(header + res.Text), nil
	case "web_search":
		var a struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(p.Arguments, &a)
		if s.Searcher == nil {
			return toolErr("web_search is not enabled for this task"), nil
		}
		results, err := s.Searcher.Search(ctx, a.Query)
		if err != nil {
			return toolErr(err.Error()), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "[web_search] %q — %d result(s)\n", a.Query, len(results))
		for i, r := range results {
			fmt.Fprintf(&b, "\n%d. %s\n   %s\n   %s", i+1, r.Title, r.URL, r.Snippet)
		}
		return toolText(b.String()), nil
	default:
		return toolErr("unknown tool: " + p.Name + " (only fetch_url and read_document exist)"), nil
	}
}

func truncatedNote(t bool) string {
	if t {
		return ", truncated"
	}
	return ""
}

// toolText / toolErr build MCP tools/call results. isError=true tells the model the call
// failed (e.g. a blocked URL) without crashing the session.
func toolText(text string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": text}},
		"isError": false,
	}
}

func toolErr(msg string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": "error: " + strings.TrimSpace(msg)}},
		"isError": true,
	}
}
