package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"mddb/internal/envconf"
	"strings"
)

// Confirming destructive tools with the human behind the client (MCP-004).
//
// A destructiveHint annotation is advice: a client may ignore it, and an agent
// will then drop a collection without anyone being asked. With
// MDDB_MCP_CONFIRM_DESTRUCTIVE set, the server asks for itself — through MCP
// elicitation, which a client turns into a question for its user — and does
// nothing unless the answer is yes.
//
// There is exactly one gate, confirmDestructive, called from the tool
// dispatcher. How the question reaches the client differs by protocol era and
// is supplied by whoever served the request, as an mcpConfirmer in the
// context (mcp_elicit.go):
//
//   - 2026-07-28: the result of the call is an InputRequiredResult (multi
//     round-trip request); the client asks, then repeats the call with the
//     answer in inputResponses.
//   - 2025-11-25 and older: the server sends elicitation/create to the client
//     mid-call and waits for its response.
//
// A request with no confirmer in its context comes from a client that cannot
// be asked: one that did not declare the elicitation capability, or a
// transport with no channel back to it.

// mcpConfirmEnv selects the policy.
const mcpConfirmEnv = "MDDB_MCP_CONFIRM_DESTRUCTIVE"

// mcpConfirmMode is the policy for destructive tools.
type mcpConfirmMode string

const (
	// mcpConfirmOff never asks. The default, and how MDDB behaved before.
	mcpConfirmOff mcpConfirmMode = "off"
	// mcpConfirmElicit asks a client that can be asked and serves one that
	// cannot exactly as before.
	mcpConfirmElicit mcpConfirmMode = "elicit"
	// mcpConfirmRequire asks a client that can be asked and refuses a client
	// that cannot.
	mcpConfirmRequire mcpConfirmMode = "require"
)

// mcpConfirmInputKey names the one question in an InputRequiredResult, and
// the answer to it in the retried request's inputResponses.
const mcpConfirmInputKey = "confirm"

// mcpConfirmArgsPreview bounds the arguments quoted in the question. A batch
// delete can carry thousands of keys; the human needs to recognise the call,
// not read it all.
const mcpConfirmArgsPreview = 400

// mcpConfirmModeFromEnv reads the policy. Read per call, like the toolsets,
// so the value an operator sees in the environment is the one in force. An
// unknown value is reported and treated as off — the behaviour before the
// setting existed — rather than refusing every destructive call on a typo.
func mcpConfirmModeFromEnv() mcpConfirmMode {
	v := strings.ToLower(strings.TrimSpace(envconf.String(mcpConfirmEnv, "")))
	switch mcpConfirmMode(v) {
	case "", mcpConfirmOff:
		return mcpConfirmOff
	case mcpConfirmElicit, mcpConfirmRequire:
		return mcpConfirmMode(v)
	}
	slog.Warn("unknown "+mcpConfirmEnv+"; destructive MCP tools are not confirmed", "value", v) // #nosec G706 -- operator's environment, not request input
	return mcpConfirmOff
}

// mcpNeedsConfirmation reports whether a tool is gated: the built-in tools
// annotated destructive and not idempotent (destructive() in
// mcp_annotations.go). Repeating one of these does further damage or cannot
// be undone — dropping a collection, deleting by filter, restoring a backup.
// The idempotent deletes of a single named object are not gated.
func mcpNeedsConfirmation(name string) bool {
	ann := mcpToolAnnotations[name]
	return ann != nil && ann.DestructiveHint != nil && *ann.DestructiveHint &&
		ann.IdempotentHint != nil && !*ann.IdempotentHint
}

// mcpConfirmRequest is the question put to the client.
type mcpConfirmRequest struct {
	// Tool is the tool waiting on the answer.
	Tool string
	// Params are the elicitation/create params: message and requestedSchema.
	Params map[string]interface{}
	// State binds an answer to this exact call: a hash of the tool and its
	// arguments, sent as requestState in a multi round-trip request so an
	// answer given for one call cannot be replayed with other arguments.
	State string
}

// mcpConfirmer delivers a question to the client and returns its answer, an
// ElicitResult ({"action": …, "content": {…}}).
type mcpConfirmer interface {
	confirm(ctx context.Context, q mcpConfirmRequest) (map[string]interface{}, error)
}

type mcpConfirmerKey struct{}

// withMCPConfirmer attaches the way to ask this request's client. A nil
// confirmer is recorded too: it hides one attached further out, which is how
// a stateless request on a stdio process whose handshake declared
// elicitation is kept from using the handshake's channel.
func withMCPConfirmer(ctx context.Context, c mcpConfirmer) context.Context {
	return context.WithValue(ctx, mcpConfirmerKey{}, c)
}

// mcpConfirmerFrom returns the request's confirmer, or nil.
func mcpConfirmerFrom(ctx context.Context) mcpConfirmer {
	c, _ := ctx.Value(mcpConfirmerKey{}).(mcpConfirmer)
	return c
}

// confirmDestructive is the gate. It returns nil when the call may go ahead,
// and an error — nothing having been changed — when it may not.
func confirmDestructive(ctx context.Context, name string, args map[string]interface{}) error {
	if !mcpNeedsConfirmation(name) {
		return nil
	}
	mode := mcpConfirmModeFromEnv()
	if mode == mcpConfirmOff {
		return nil
	}
	c := mcpConfirmerFrom(ctx)
	if c == nil {
		if mode == mcpConfirmRequire {
			return &mcpConfirmUnavailableError{tool: name}
		}
		return nil
	}
	answer, err := c.confirm(ctx, newMCPConfirmRequest(name, args))
	if err != nil {
		return err
	}
	return checkMCPConfirmation(name, args, answer)
}

// newMCPConfirmRequest builds the question for one call. Every gated tool is
// asked for confirm: true; dropping a collection also asks for its name to be
// typed, so a yes clicked out of habit is not enough to lose one.
func newMCPConfirmRequest(name string, args map[string]interface{}) mcpConfirmRequest {
	encoded, _ := json.Marshal(args) // map keys sort, so the encoding is canonical
	sum := sha256.Sum256(append([]byte(name+"\x00"), encoded...))

	preview := string(encoded)
	if len(preview) > mcpConfirmArgsPreview {
		preview = preview[:mcpConfirmArgsPreview] + "…"
	}
	message := fmt.Sprintf("An MCP client asked MDDB to run %s with %s. This cannot be undone. Allow it?", name, preview)

	props := map[string]interface{}{
		"confirm": map[string]interface{}{
			"type":        "boolean",
			"title":       "Allow " + name,
			"description": "Yes runs the tool; anything else changes nothing.",
		},
	}
	required := []string{"confirm"}
	if name == "delete_collection" {
		collection, _ := args["collection"].(string)
		props["collection"] = map[string]interface{}{
			"type":        "string",
			"title":       "Collection name",
			"description": fmt.Sprintf("Type %q to confirm it should be deleted.", collection),
		}
		required = append(required, "collection")
	}

	return mcpConfirmRequest{
		Tool: name,
		Params: map[string]interface{}{
			"message": message,
			"requestedSchema": map[string]interface{}{
				"type":       "object",
				"properties": props,
				"required":   required,
			},
		},
		State: hex.EncodeToString(sum[:]),
	}
}

// checkMCPConfirmation reads an ElicitResult. Only an accepted answer with
// confirm: true — and, for delete_collection, the collection's own name —
// lets the call through. Declining, cancelling and anything malformed refuse.
func checkMCPConfirmation(name string, args, answer map[string]interface{}) error {
	action, _ := answer["action"].(string)
	if action != "accept" {
		return fmt.Errorf("%s was not confirmed (the user chose %q); nothing was changed", name, action)
	}
	content, _ := answer["content"].(map[string]interface{})
	if yes, _ := content["confirm"].(bool); !yes {
		return fmt.Errorf("%s was not confirmed; nothing was changed", name)
	}
	if name == "delete_collection" {
		want, _ := args["collection"].(string)
		got, _ := content["collection"].(string)
		if strings.TrimSpace(got) != want {
			return fmt.Errorf("delete_collection was not confirmed: the name typed (%q) is not %q; nothing was changed", got, want)
		}
	}
	return nil
}
