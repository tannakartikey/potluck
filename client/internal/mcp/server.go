// Package mcp implements a minimal Model Context Protocol (MCP) server over stdio that
// exposes EXACTLY Potluck's curated tools (fetch_url, read_document) and nothing else. This
// is the v2 "constrained tool surface": the agent CLI is pointed at this server (via
// --mcp-config + --strict-mcp-config) so the only callable tools are the two project-owned,
// hardened ones — never Bash, Read, Write, or arbitrary network. The agent talks JSON-RPC
// 2.0 to this process over its stdin/stdout. stdlib-only.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/tannakartikey/potluck/client/internal/tools"
)

// ProtocolVersion is the MCP revision we advertise if the client doesn't pin one.
const ProtocolVersion = "2025-06-18"

// Server serves the curated tools over a JSON-RPC 2.0 stdio transport.
// Server exposes only read_document. Web research uses the agent's native WebSearch/WebFetch
// (reliable, provider-maintained); we don't reimplement fetch/search. read_document stays ours
// because it must be CONFINED to the task's input dir — the native Read tool is denied.
type Server struct {
	Reader  *tools.Reader
	Name    string
	Version string
}

func NewServer(reader *tools.Reader) *Server {
	return &Server{Reader: reader, Name: "potluck", Version: "0.4"}
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
			"name": "read_document",
			"description": "Extract the text of a document present in the task's input directory " +
				"(an attachment). Supports text, HTML, and (best-effort) PDF. Cannot read outside the " +
				"input directory. For the open web, use the native WebSearch / WebFetch tools.",
			"inputSchema": strObj(map[string]interface{}{
				"path": map[string]interface{}{"type": "string", "description": "Path to the document, relative to the input directory."},
			}, "path"),
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

	switch p.Name {
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
	default:
		return toolErr("unknown tool: " + p.Name + " (this server exposes only read_document)"), nil
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
