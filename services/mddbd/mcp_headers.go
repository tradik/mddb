package main

import (
	"encoding/base64"
	"net/http"
	"strings"
)

// The request metadata headers MCP 2026-07-28 requires on every Streamable
// HTTP POST (MCP-001).
//
// The transport mirrors three body fields into headers so that a load
// balancer, gateway or trace collector can route and inspect a call without
// parsing JSON. That only holds if the two agree, which is why the server has
// to check rather than trust: if a proxy routes on the header while the server
// executes on the body, a request can be authorized as one call and performed
// as another. A disagreement is -32020 and the request is not served.

const (
	mcpHeaderProtocolVersion = "MCP-Protocol-Version"
	mcpHeaderMethod          = "Mcp-Method"
	mcpHeaderName            = "Mcp-Name"

	// A header value that cannot be written as plain ASCII travels
	// base64-encoded between these markers, which are case-sensitive and
	// lowercase. A tool named in Japanese, or a resource URI with a space, is
	// the ordinary case here rather than an exotic one.
	mcpHeaderB64Prefix = "=?base64?"
	mcpHeaderB64Suffix = "?="
)

// mcpDecodeHeaderValue returns a header value with the Base64 sentinel
// removed, and reports whether the value was well-formed.
//
// A value that is not encoded is returned unchanged: the sentinel is opt-in,
// and clients only reach for it when the raw value would not survive a header.
func mcpDecodeHeaderValue(value string) (string, bool) {
	if !strings.HasPrefix(value, mcpHeaderB64Prefix) || !strings.HasSuffix(value, mcpHeaderB64Suffix) {
		return value, true
	}
	encoded := value[len(mcpHeaderB64Prefix) : len(value)-len(mcpHeaderB64Suffix)]
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	return string(decoded), true
}

// mcpHeaderNameSource returns the body value Mcp-Name mirrors for a method,
// and whether the method requires the header at all.
//
// Only the three methods that address a named thing carry it. tools/list has
// no name to mirror, and demanding one would fail every conforming client.
func mcpHeaderNameSource(method string, req map[string]interface{}) (string, bool) {
	params := mcpParams(req)
	switch method {
	case "tools/call", "prompts/get":
		name, _ := params["name"].(string)
		return name, true
	case "resources/read":
		uri, _ := params["uri"].(string)
		return uri, true
	default:
		return "", false
	}
}

// mcpValidateModernHeaders checks a modern POST's mirrored headers against its
// body and returns a JSON-RPC error object, or nil when they agree.
//
// The protocol version is checked here for its header/body agreement only. The
// question of whether this build serves that revision at all belongs to the
// request itself and is answered the same way on every transport, in
// mcpValidateModernMeta — a stdio client has no headers and must still be told
// the same thing.
func mcpValidateModernHeaders(header http.Header, req map[string]interface{}) map[string]interface{} {
	method, _ := req["method"].(string)

	headerVersion := strings.TrimSpace(header.Get(mcpHeaderProtocolVersion))
	if headerVersion == "" {
		return mcpError(mcpErrHeaderMismatch, "Header mismatch: "+mcpHeaderProtocolVersion+" is required")
	}
	bodyVersion, _ := mcpMetaString(mcpRequestMeta(req), metaProtocolVersion)
	if bodyVersion != "" && headerVersion != bodyVersion {
		return mcpError(mcpErrHeaderMismatch, "Header mismatch: "+mcpHeaderProtocolVersion+
			" header value '"+headerVersion+"' does not match body value '"+bodyVersion+"'")
	}

	headerMethod := header.Get(mcpHeaderMethod)
	if headerMethod == "" {
		return mcpError(mcpErrHeaderMismatch, "Header mismatch: "+mcpHeaderMethod+" is required")
	}
	if headerMethod != method {
		return mcpError(mcpErrHeaderMismatch, "Header mismatch: "+mcpHeaderMethod+
			" header value '"+headerMethod+"' does not match body value '"+method+"'")
	}

	bodyName, required := mcpHeaderNameSource(method, req)
	if !required {
		return nil
	}
	rawName := header.Get(mcpHeaderName)
	if rawName == "" {
		return mcpError(mcpErrHeaderMismatch, "Header mismatch: "+mcpHeaderName+" is required for "+method)
	}
	headerName, ok := mcpDecodeHeaderValue(rawName)
	if !ok {
		return mcpError(mcpErrHeaderMismatch, "Header mismatch: "+mcpHeaderName+" is not valid Base64")
	}
	if headerName != bodyName {
		return mcpError(mcpErrHeaderMismatch, "Header mismatch: "+mcpHeaderName+
			" header value '"+headerName+"' does not match body value '"+bodyName+"'")
	}
	return nil
}
