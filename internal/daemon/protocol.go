package daemon

import "encoding/json"

// Contract is the daemon wire contract version.
const Contract = "sila/daemon/v1"

// Request is one NDJSON JSON-RPC frame.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is one NDJSON JSON-RPC reply.
type Response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

// RPCError is a JSON-RPC error body.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// NewError builds an error response.
func NewError(id any, code int, message string) Response {
	return Response{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: message}}
}

// NewResult builds a success response.
func NewResult(id any, result any) Response {
	return Response{JSONRPC: "2.0", ID: id, Result: result}
}
