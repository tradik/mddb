package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// How the destructive-tool gate (mcp_confirm.go) reaches the client, per
// protocol era (MCP-004). Each confirmer is installed in a request's context
// by whoever served the request, and only when the client declared the
// elicitation capability.

// mcpDeclaresElicitation reports whether a capabilities object declares
// elicitation. Both eras spell it the same way: an "elicitation" object, empty
// meaning form mode, which is the only mode MDDB uses.
func mcpDeclaresElicitation(caps interface{}) bool {
	m, _ := caps.(map[string]interface{})
	_, ok := m["elicitation"].(map[string]interface{})
	return ok
}

// mcpModernConfirmer returns the confirmer for a 2026-07-28 request, or nil
// when its _meta does not declare elicitation.
//
// The revision has no server-to-client requests on a call: the server answers
// with an InputRequiredResult and the client repeats the call carrying the
// answer in params.inputResponses and the echoed params.requestState. Nothing
// is held between the two calls, so the confirmer is built from the request
// alone.
func mcpModernConfirmer(req map[string]interface{}) mcpConfirmer {
	if !mcpDeclaresElicitation(mcpRequestMeta(req)[metaClientCapabilities]) {
		return nil
	}
	params := mcpParams(req)
	responses, _ := params["inputResponses"].(map[string]interface{})
	state, _ := params["requestState"].(string)
	return mcpRoundTripConfirmer{responses: responses, state: state}
}

// mcpRoundTripConfirmer answers from a retried 2026-07-28 request.
type mcpRoundTripConfirmer struct {
	responses map[string]interface{}
	state     string
}

// confirm returns the answer the client sent, or asks for one. An answer
// whose requestState belongs to a different call — other arguments, another
// tool — is not an answer to this one, and the question is asked again.
func (c mcpRoundTripConfirmer) confirm(_ context.Context, q mcpConfirmRequest) (map[string]interface{}, error) {
	answer, ok := c.responses[mcpConfirmInputKey].(map[string]interface{})
	if !ok || c.state != q.State {
		return nil, &mcpInputRequiredError{q: q}
	}
	return answer, nil
}

// mcpConfirmTimeout bounds how long a handshake-era call waits for the human.
// A variable so tests need not wait for one.
var mcpConfirmTimeout = 5 * time.Minute

// mcpRequestConfirmer asks a 2025-11-25 client with elicitation/create, a
// request from the server, and waits for the response the transport routes
// back through pending.
type mcpRequestConfirmer struct {
	// send writes a server-to-client message on the channel back to the
	// client: stdout on stdio, the POST's SSE stream over Streamable HTTP.
	send    func(msg map[string]interface{}) error
	pending *mcpPendingRequests
}

// confirm sends the question and waits for the answer, the caller giving up,
// or the timeout, whichever comes first.
func (c *mcpRequestConfirmer) confirm(ctx context.Context, q mcpConfirmRequest) (map[string]interface{}, error) {
	id, ch := c.pending.register()
	defer c.pending.forget(id)

	if err := c.send(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "elicitation/create",
		"params":  q.Params,
	}); err != nil {
		return nil, fmt.Errorf("%s could not ask for confirmation (%w); nothing was changed", q.Tool, err)
	}

	timer := time.NewTimer(mcpConfirmTimeout)
	defer timer.Stop()
	select {
	case resp, open := <-ch:
		if !open {
			return nil, fmt.Errorf("%s was not confirmed: the client went away; nothing was changed", q.Tool)
		}
		if errObj, failed := resp["error"].(map[string]interface{}); failed {
			return nil, fmt.Errorf("%s was not confirmed: the client answered with an error (%v); nothing was changed", q.Tool, errObj["message"])
		}
		result, _ := resp["result"].(map[string]interface{})
		return result, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("%s was not confirmed (%w); nothing was changed", q.Tool, ctx.Err())
	case <-timer.C:
		return nil, fmt.Errorf("%s was not confirmed within %s; nothing was changed", q.Tool, mcpConfirmTimeout)
	}
}

// mcpPendingRequests matches the client's responses to the requests the
// server sent, by id.
type mcpPendingRequests struct {
	mu      sync.Mutex
	waiting map[string]chan map[string]interface{}
	closed  bool
}

func newMCPPendingRequests() *mcpPendingRequests {
	return &mcpPendingRequests{waiting: map[string]chan map[string]interface{}{}}
}

// register reserves a fresh id and the channel its response arrives on. Once
// the registry is closed the channel comes back closed: nothing will answer.
func (p *mcpPendingRequests) register() (string, chan map[string]interface{}) {
	b := make([]byte, 12)
	_, _ = rand.Read(b) // crypto/rand.Read does not fail on supported platforms
	id := "mddb-confirm-" + hex.EncodeToString(b)
	ch := make(chan map[string]interface{}, 1)

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		close(ch)
		return id, ch
	}
	p.waiting[id] = ch
	return id, ch
}

// forget drops an id whose wait is over, answered or not.
func (p *mcpPendingRequests) forget(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.waiting, id)
}

// deliver hands a response to whoever is waiting for its id. A response no
// one is waiting for — late, or never asked for — is dropped.
func (p *mcpPendingRequests) deliver(resp map[string]interface{}) {
	id, _ := resp["id"].(string)
	p.mu.Lock()
	defer p.mu.Unlock()
	if ch, ok := p.waiting[id]; ok {
		delete(p.waiting, id)
		ch <- resp // buffered, and each id is delivered at most once
	}
}

// closeAll ends every wait: the client is gone and cannot answer.
func (p *mcpPendingRequests) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for id, ch := range p.waiting {
		close(ch)
		delete(p.waiting, id)
	}
}

// mcpIsResponse reports whether a message from the client is a response to a
// server request rather than a request or notification of its own.
func mcpIsResponse(msg map[string]interface{}) bool {
	if _, isCall := msg["method"]; isCall {
		return false
	}
	_, hasID := msg["id"]
	_, hasResult := msg["result"]
	_, hasError := msg["error"]
	return hasID && (hasResult || hasError)
}

// errMCPNoStream is returned when a transport cannot carry a server request.
var errMCPNoStream = errors.New("the transport cannot stream to this client")
