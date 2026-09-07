package main

import "net/http"

// JSON-RPC error codes as MCP 2026-07-28 allocates them (MCP-001).
//
// The revision partitions the server-error range that JSON-RPC leaves to
// implementations. -32000..-32019 is where implementations put their own codes
// before there was a policy, and is now closed to new ones; -32020..-32099
// belongs to the specification, and a server may only emit codes from it that
// the specification defines, with the meanings it gives them.
//
// This is why the codes below are named constants in one file rather than
// integers at the point of use: the range they come from is the load-bearing
// part, and it is invisible at a call site that writes -32020 inline.
const (
	// mcpErrParse and the four codes after it are plain JSON-RPC 2.0.
	mcpErrParse          = -32700
	mcpErrInvalidRequest = -32600
	mcpErrMethodNotFound = -32601
	mcpErrInvalidParams  = -32602
	mcpErrInternal       = -32603

	// mcpErrHeaderMismatch reports that a Streamable HTTP request's mirrored
	// headers disagree with its body, or that a required one is missing. It
	// matters because a proxy may route on the header while the server acts on
	// the body, and the two must not be able to say different things.
	mcpErrHeaderMismatch = -32020
	// mcpErrMissingClientCapability reports that serving the request needs a
	// capability the client did not declare for it. MDDB never emits it —
	// every tool answers out of the database, so no request of ours needs
	// anything from the client — but the code is named here because the
	// transport has to map it to a status when it is read from elsewhere, and
	// because the number is spoken for either way.
	mcpErrMissingClientCapability = -32021
	// mcpErrUnsupportedProtocolVersion reports a revision this build does not
	// answer for, and names the ones it does so the client can retry.
	mcpErrUnsupportedProtocolVersion = -32022

	// mcpErrLegacyResourceNotFound is the code 2025-11-25 and earlier used for
	// a missing resource. 2026-07-28 replaces it with plain -32602 and forbids
	// emitting it, so it survives here only to be translated on the way out of
	// a legacy request. The specification will not reuse the number.
	mcpErrLegacyResourceNotFound = -32002
)

// mcpError builds a JSON-RPC error object.
func mcpError(code int, message string) map[string]interface{} {
	return map[string]interface{}{"code": code, "message": message}
}

// mcpErrorWithData builds a JSON-RPC error object carrying a data payload.
func mcpErrorWithData(code int, message string, data map[string]interface{}) map[string]interface{} {
	err := mcpError(code, message)
	err["data"] = data
	return err
}

// mcpUnsupportedVersionError names every revision this server will answer for,
// which is what lets a client retry rather than give up. A client that asked
// for a revision MDDB cannot speak has no other way to find one it can.
func mcpUnsupportedVersionError(requested string) map[string]interface{} {
	return mcpErrorWithData(mcpErrUnsupportedProtocolVersion, "Unsupported protocol version",
		map[string]interface{}{
			"supported": activeMCPVersions(),
			"requested": requested,
		})
}

// mcpErrorResponse wraps an error object as a complete JSON-RPC response.
func mcpErrorResponse(id interface{}, errObj map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   errObj,
	}
}

// mcpErrorHTTPStatus maps an error code to the status the Streamable HTTP
// transport must answer with.
//
// The transport is specific about two of these. A method this build does not
// implement is 404 — with a JSON-RPC body, which is what distinguishes it from
// the 404 of a server that hosts no MCP endpoint at all. Everything the
// revision defines about a malformed or unservable request is 400, and a
// client is told to read the body before concluding the server is legacy.
func mcpErrorHTTPStatus(code int) int {
	switch code {
	case mcpErrMethodNotFound:
		return http.StatusNotFound
	case mcpErrParse, mcpErrInvalidRequest, mcpErrInvalidParams,
		mcpErrHeaderMismatch, mcpErrMissingClientCapability, mcpErrUnsupportedProtocolVersion:
		return http.StatusBadRequest
	default:
		return http.StatusOK
	}
}
