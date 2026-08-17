// Package mcp implements a Model Context Protocol server over Streamable HTTP, so
// verdande can be added to Claude as a connector.
//
// MCP is JSON-RPC 2.0 with a fixed handshake and a fixed set of methods. It is
// implemented directly here rather than through an SDK: the surface verdande needs
// is initialize, tools/list and tools/call, and the whole thing is smaller than the
// dependency would be.
//
// Authentication is a personal API token in the Authorization header, checked by
// the same middleware as the rest of the API — so a tool call can reach exactly
// what its owner can reach, and nothing else. There is no separate service identity
// to reason about, which is the point.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// The protocol version this server speaks. Claude sends its own in initialize; a
// mismatch is not fatal, and answering with what we actually implement is what the
// spec asks for.
const ProtocolVersion = "2024-11-05"

// --- JSON-RPC ---------------------------------------------------------------------

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// The JSON-RPC error codes, as the spec numbers them.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

func result(id json.RawMessage, value any) *Response {
	return &Response{JSONRPC: "2.0", ID: id, Result: value}
}

func failure(id json.RawMessage, code int, message string) *Response {
	return &Response{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: message}}
}

// --- tools -------------------------------------------------------------------------

// Tool is one thing Claude can do. The schema is what it reads to decide how to
// call it, so the descriptions are written for a model rather than for a developer:
// they say when to use the tool, not merely what it does.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Handler executes a tool call for a particular user. Returning an error produces a
// tool error rather than a protocol error — a model can read and recover from the
// first, and can do nothing at all with the second.
type Handler func(ctx context.Context, userID string, args json.RawMessage) (any, error)

type Server struct {
	tools    []Tool
	handlers map[string]Handler
	// Name and Version identify this instance in the connector list.
	Name    string
	Version string
}

func NewServer(name, version string) *Server {
	return &Server{Name: name, Version: version, handlers: map[string]Handler{}}
}

func (s *Server) Register(tool Tool, handler Handler) {
	s.tools = append(s.tools, tool)
	s.handlers[tool.Name] = handler
}

// Handle dispatches one JSON-RPC message.
//
// A nil response means the message was a notification — it had no id, so the spec
// says to answer with nothing at all. Answering anyway is a protocol violation that
// some clients tolerate and others disconnect over.
func (s *Server) Handle(ctx context.Context, userID string, raw []byte) *Response {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return failure(nil, CodeParseError, "invalid JSON")
	}
	if req.JSONRPC != "2.0" {
		return failure(req.ID, CodeInvalidRequest, "jsonrpc must be \"2.0\"")
	}

	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		return result(req.ID, map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities": map[string]any{
				// Only tools. verdande has no prompts or resources to offer that
				// the tools do not already cover, and declaring capabilities that
				// answer with nothing wastes a round trip on every connection.
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{"name": s.Name, "version": s.Version},
		})

	case "notifications/initialized", "initialized":
		return nil

	case "ping":
		return result(req.ID, map[string]any{})

	case "tools/list":
		return result(req.ID, map[string]any{"tools": s.tools})

	case "tools/call":
		return s.callTool(ctx, userID, req)

	default:
		if isNotification {
			return nil
		}
		return failure(req.ID, CodeMethodNotFound, "unknown method: "+req.Method)
	}
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) callTool(ctx context.Context, userID string, req Request) *Response {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return failure(req.ID, CodeInvalidParams, "could not read the call parameters")
	}

	handler, ok := s.handlers[params.Name]
	if !ok {
		return failure(req.ID, CodeMethodNotFound, "unknown tool: "+params.Name)
	}

	value, err := handler(ctx, userID, params.Arguments)
	if err != nil {
		// A failed tool call is a *result* with isError set, not a JSON-RPC error.
		// The distinction matters: this way the model is told what went wrong and
		// can try something else, rather than the conversation failing.
		return result(req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
			"isError": true,
		})
	}

	// Tool results are text content. JSON is rendered indented because a model
	// reads it as text, and one line of dense JSON is markedly harder for it to
	// pick a field out of than a formatted block.
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return failure(req.ID, CodeInternalError, "could not encode the result")
	}
	return result(req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(encoded)}},
	})
}

// --- schema helpers -------------------------------------------------------------------

// Schema builds a JSON Schema object for a tool's arguments.
func Schema(properties map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
}

func Str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func Int(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func Bool(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func Enum(description string, values ...any) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}

func StrArray(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "string"},
	}
}

// ArgError is what a handler returns when the model called it wrongly. Phrased as
// an instruction rather than a complaint, because the reader is a model deciding
// what to do next.
func ArgError(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
