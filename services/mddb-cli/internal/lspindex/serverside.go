package lspindex

import "encoding/json"

// What the client does with messages the server starts: requests it must
// answer and notifications it keeps.

// maxErrMsgs bounds the server error messages kept for ErrWorkspaceNotLoaded.
const maxErrMsgs = 20

// answer replies to a request from the server. gopls and pyrefly wait for
// these answers before they go on (progress tokens, capability registration,
// configuration), so leaving one unanswered stalls the whole run.
func (c *conn) answer(m *message) {
	reply := &message{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage("null")}
	switch m.Method {
	case "window/workDoneProgress/create", "client/registerCapability", "client/unregisterCapability",
		"window/showMessageRequest", "workspace/semanticTokens/refresh", "workspace/codeLens/refresh",
		"workspace/inlayHint/refresh", "workspace/diagnostic/refresh":
	case "workspace/configuration":
		// One entry per requested item; null means "use your defaults".
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(m.Params, &p)
		reply.Result = mustRaw(make([]any, len(p.Items)))
	case "workspace/workspaceFolders":
		reply.Result = mustRaw(c.folders)
	default:
		reply.Result = nil
		reply.Error = &rpcError{Code: codeMethodNotFound, Message: "not supported by mddb-cli: " + m.Method}
	}
	// A failed write means the stream is gone; the read loop sees that next.
	_ = c.send(reply)
}

// observe keeps the server's error messages: an unloaded workspace is often
// reported only there, while every request still succeeds.
func (c *conn) observe(m *message) {
	switch m.Method {
	case "$/progress":
		c.trackProgress(m.Params)
		return
	case "experimental/serverStatus":
		c.trackStatus(m.Params)
		return
	}
	if m.Method != "window/showMessage" && m.Method != "window/logMessage" {
		return
	}
	var p struct {
		Type    int    `json:"type"`
		Message string `json:"message"`
	}
	if json.Unmarshal(m.Params, &p) != nil || p.Type != messageTypeError {
		return
	}
	c.mu.Lock()
	if len(c.errMsgs) < maxErrMsgs {
		c.errMsgs = append(c.errMsgs, clipMessage(p.Message))
	}
	c.mu.Unlock()
}

// serverErrors returns the error messages seen so far.
func (c *conn) serverErrors() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.errMsgs...)
}

// messageTypeError is MessageType.Error.
const messageTypeError = 1

// clipMessage keeps a server message readable inside an error.
func clipMessage(s string) string {
	const limit = 300
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}
