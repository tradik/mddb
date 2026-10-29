package mddb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTypedMethods(t *testing.T) {
	for _, tc := range typedCases() {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod, gotPath, gotBody string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.RequestURI()
				b, _ := readBody(r)
				gotBody = string(b)
				_, _ = w.Write([]byte(tc.respBody))
			}))
			defer srv.Close()

			got, err := tc.call(context.Background(), New(srv.URL))
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if gotMethod != tc.method || gotPath != tc.path {
				t.Errorf("hit %s %s, want %s %s", gotMethod, gotPath, tc.method, tc.path)
			}
			if tc.wantBody != "" && !strings.Contains(gotBody, tc.wantBody) {
				t.Errorf("body %s does not contain %s", gotBody, tc.wantBody)
			}
			if tc.wantBody == "" && gotBody != "" {
				t.Errorf("unexpected request body %s", gotBody)
			}
			if tc.check != nil {
				tc.check(t, got)
			}
		})
	}
}

// TestTypedMethodErrors covers the failure paths of every typed method: an
// HTTP error status becomes *APIError, and a malformed body a decode error
// (SetCollectionConfig does not decode, so it only has the first).
func TestTypedMethodErrors(t *testing.T) {
	statusSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"nope"}`, http.StatusBadRequest)
	}))
	defer statusSrv.Close()
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{not json`))
	}))
	defer badSrv.Close()

	for _, tc := range typedCases() {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.call(context.Background(), New(statusSrv.URL))
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
				t.Errorf("want *APIError 400, got %v", err)
			}
			if tc.name == "SetCollectionConfig" || tc.name == "SearchAdvisor" {
				return // these return the body undecoded
			}
			if _, err := tc.call(context.Background(), New(badSrv.URL)); err == nil || !strings.Contains(err.Error(), "decode response") {
				t.Errorf("want decode error, got %v", err)
			}
		})
	}
}

// TestRequestTypesOmitZero checks that zero optional fields are not sent, so
// the server applies its own defaults rather than a client-chosen zero.
func TestRequestTypesOmitZero(t *testing.T) {
	cases := []struct {
		name string
		req  any
		want string
	}{
		{"FTSRequest", FTSRequest{Collection: "c", Query: "q"}, `{"collection":"c","query":"q"}`},
		{"VectorSearchRequest", VectorSearchRequest{Collection: "c", Query: "q"}, `{"collection":"c","query":"q"}`},
		{"HybridSearchRequest", HybridSearchRequest{Collection: "c", Query: "q"}, `{"collection":"c","query":"q"}`},
		{"VectorReindexRequest", VectorReindexRequest{Collection: "c"}, `{"collection":"c"}`},
		{"SetCollectionConfigRequest", SetCollectionConfigRequest{Collection: "c"}, `{"collection":"c"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tc.want {
				t.Errorf("got %s, want %s", b, tc.want)
			}
		})
	}
}
