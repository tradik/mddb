package main

import "fmt"

// The two outcomes of the destructive-tool gate (mcp_confirm.go) that are not
// an ordinary tool failure, and so are answered differently by the tools/call
// handler: one is a protocol error, the other is not an error at all.

// mcpConfirmUnavailableError refuses a gated tool under require when the
// client cannot be asked. It travels as -32021 MissingRequiredClientCapability,
// naming the capability that would have let the call through.
type mcpConfirmUnavailableError struct{ tool string }

func (e *mcpConfirmUnavailableError) Error() string {
	return fmt.Sprintf("%s needs the user's confirmation (%s=require), but this client did not declare the elicitation capability, so it cannot be asked; nothing was changed", e.tool, mcpConfirmEnv)
}

// rpcError is the JSON-RPC error object for the refusal.
func (e *mcpConfirmUnavailableError) rpcError() map[string]interface{} {
	return mcpErrorWithData(mcpErrMissingClientCapability, e.Error(), map[string]interface{}{
		"requiredCapabilities": map[string]interface{}{"elicitation": map[string]interface{}{}},
	})
}

// mcpInputRequiredError is not a failure: it carries the InputRequiredResult a
// 2026-07-28 call answers with while it waits for the client's answer.
type mcpInputRequiredError struct{ q mcpConfirmRequest }

func (e *mcpInputRequiredError) Error() string {
	return e.q.Tool + " is waiting for the user's confirmation"
}

// result is the InputRequiredResult: one elicitation, and the state the
// client echoes back with its answer.
func (e *mcpInputRequiredError) result() map[string]interface{} {
	return map[string]interface{}{
		"resultType": mcpResultTypeInputRequired,
		"inputRequests": map[string]interface{}{
			mcpConfirmInputKey: map[string]interface{}{
				"method": "elicitation/create",
				"params": e.q.Params,
			},
		},
		"requestState": e.q.State,
	}
}
