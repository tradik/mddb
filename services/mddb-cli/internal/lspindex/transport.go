package lspindex

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// JSON-RPC 2.0 over the base protocol of LSP: each message is a header block
// ("Content-Length: N", optionally "Content-Type: ...", CRLF line ends, an
// empty line) followed by N bytes of JSON. Written by hand because the
// driver needs a dozen structures and six methods; an LSP SDK would be a
// dependency to track for that.

// maxMessageBytes bounds one incoming message. gopls answers references for
// a widely used symbol in a few MB; a length past this is a broken stream,
// not an answer, and allocating it would let a confused server exhaust memory.
const maxMessageBytes = 256 << 20

// maxHeaderLines bounds the header block, so a stream of garbage without an
// empty line fails instead of being read forever.
const maxHeaderLines = 16

// errProtocol marks a stream that does not follow the base protocol.
var errProtocol = errors.New("language server broke the protocol")

// message is any JSON-RPC message: request (id and method), notification
// (method only) or response (id and result or error).
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("language server error %d: %s", e.Code, e.Message)
}

// Error codes the client sends or recognises.
const (
	codeMethodNotFound   = -32601
	codeRequestCancelled = -32800
)

// writeMessage frames v and writes it in one call, so concurrent writers
// serialised by the caller's mutex never interleave a header and a body.
func writeMessage(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	frame := make([]byte, 0, len(body)+32)
	frame = append(frame, "Content-Length: "...)
	frame = strconv.AppendInt(frame, int64(len(body)), 10)
	frame = append(frame, "\r\n\r\n"...)
	frame = append(frame, body...)
	_, err = w.Write(frame)
	return err
}

// readMessage reads one framed message.
func readMessage(r *bufio.Reader) (*message, error) {
	length := -1
	for lines := 0; ; lines++ {
		if lines == maxHeaderLines {
			return nil, fmt.Errorf("%w: header block longer than %d lines", errProtocol, maxHeaderLines)
		}
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("%w: header line %q", errProtocol, clip(line))
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 || n > maxMessageBytes {
				return nil, fmt.Errorf("%w: Content-Length %q", errProtocol, clip(value))
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("%w: message without Content-Length", errProtocol)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var m message
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("%w: %v", errProtocol, err)
	}
	return &m, nil
}

// clip shortens server output quoted in an error.
func clip(s string) string {
	const limit = 80
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}
