package mddb

import (
	"context"
	"testing"
)

// symbolCases are the typed cases of CodeSymbol, run by TestTypedMethods and
// TestTypedMethodErrors.
func symbolCases() []typedCase {
	return []typedCase{{
		name: "CodeSymbol",
		call: func(ctx context.Context, c *Client) (any, error) {
			return c.CodeSymbol(ctx, SymbolRequest{Collection: "src", Name: "Close", Path: "net/conn.go",
				Include: []string{"references", "implementations"}})
		},
		method: "POST", path: "/v1/symbol",
		wantBody: `"name":"Close","path":"net/conn.go","include":["references","implementations"]`,
		respBody: `{"collection":"src","symbol":"Close","definitions":[{"collection":"src","key":"net/conn.go","path":"net/conn.go",` +
			`"lines":{"start":5,"end":7},"symbol":"Conn.Close","kind":"method","container":"Conn","tokens":0}],` +
			`"references":[{"collection":"src","key":"app/main.go","path":"app/main.go","lines":{"start":4,"end":4},` +
			`"target":"net/conn.go#Conn.Close","tokens":2}],"implementations":[],"precision":"semantic",` +
			`"semanticSource":"gopls/0.24.0","ambiguous":true,"candidates":[{"path":"net/conn.go","name":"Conn.Close","kind":"method"}]}`,
		check: func(t *testing.T, got any) {
			r := got.(*SymbolResult)
			if r.Precision != "semantic" || r.SemanticSource != "gopls/0.24.0" || !r.Ambiguous ||
				r.Candidates[0].Name != "Conn.Close" || r.Definitions[0].Container != "Conn" ||
				r.References[0].Target != "net/conn.go#Conn.Close" {
				t.Errorf("SymbolResult = %+v", r)
			}
		},
	}}
}
