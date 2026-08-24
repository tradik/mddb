package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// SRCH-005. Oversampling existed as `topK * 3` in five places, so the
// recall/latency trade-off every comparable engine exposes was fixed at
// whatever those constants said.

func TestOversampledTopKMatchesTheOldConstants(t *testing.T) {
	// The whole compatibility claim: default factor, historical floors, same
	// numbers the hardcoded expressions produced.
	cases := []struct {
		topK, floor, want int
	}{
		{10, 20, 30}, // 10*3 = 30, above the floor
		{5, 20, 20},  // 5*3 = 15, below the floor
		{1, 50, 50},  // hybrid's floor
		{100, 20, 300},
		{0, 20, 20},
	}
	for _, c := range cases {
		if got := OversampledTopK(c.topK, DefaultOversample, c.floor); got != c.want {
			t.Errorf("OversampledTopK(%d, default, %d) = %d, want %d", c.topK, c.floor, got, c.want)
		}
	}
}

func TestOversampleFactorScalesTheCandidateSet(t *testing.T) {
	if got := OversampledTopK(10, 1.0, 5); got != 10 {
		t.Errorf("factor 1.0 gave %d candidates for topK 10, want 10", got)
	}
	if got := OversampledTopK(10, 10.0, 5); got != 100 {
		t.Errorf("factor 10.0 gave %d, want 100", got)
	}
	// A non-positive factor falls back to the default rather than asking the
	// index for nothing.
	if got := OversampledTopK(10, 0, 5); got != 30 {
		t.Errorf("factor 0 gave %d, want the default's 30", got)
	}
	if got := OversampledTopK(10, -2, 5); got != 30 {
		t.Errorf("a negative factor gave %d, want the default's 30", got)
	}
}

// The ceiling on the factor is meaningless if the product can still grow
// without limit.
func TestOversampledTopKStaysBounded(t *testing.T) {
	if got := OversampledTopK(1000, MaxOversample, 20); got > 1000*int(MaxOversample) {
		t.Errorf("candidate set grew past the ceiling: %d", got)
	}
	// A large floor must not push a small request past the ceiling either.
	if got := OversampledTopK(1, 1.0, 5000); got > 5000 {
		t.Errorf("floor produced %d candidates", got)
	}

	// The ceiling actually bites when the factor would exceed it. The factor
	// argument bypasses ValidateOversample here on purpose: the bound has to
	// hold even if a future caller forgets to validate.
	if got := OversampledTopK(100, 50.0, 20); got != 100*int(MaxOversample) {
		t.Errorf("an unvalidated factor of 50 gave %d candidates, want the ceiling of %d",
			got, 100*int(MaxOversample))
	}

	// The floor still wins where the two cross.
	if got := OversampledTopK(1, DefaultOversample, 50); got != 50 {
		t.Errorf("the ceiling overrode the floor: %d", got)
	}
}

func TestValidateOversample(t *testing.T) {
	for _, ok := range []float64{0, 1.0, 3.0, 5.5, 10.0} {
		if err := ValidateOversample(ok); err != nil {
			t.Errorf("%v was rejected: %v", ok, err)
		}
	}
	for _, bad := range []float64{0.5, -1, 10.1, 1000} {
		if err := ValidateOversample(bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
}

func TestResolveOversamplePrecedence(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()

	// No profile: the constant the code has always used.
	if got := srv.ResolveOversample("plain", 0); got != DefaultOversample {
		t.Errorf("unconfigured collection gave %v, want %v", got, DefaultOversample)
	}

	setProfile(t, srv, "tuned", &RetrievalProfileDef{Oversample: 6})
	if got := srv.ResolveOversample("tuned", 0); got != 6 {
		t.Errorf("the profile was ignored: %v", got)
	}
	// An explicit request wins, as everywhere else (RAG-001).
	if got := srv.ResolveOversample("tuned", 2); got != 2 {
		t.Errorf("the request was overridden by the profile: %v", got)
	}
}

// An out-of-range tuning value is 422: the body parsed, the number is the
// problem, and a caller deciding whether to fix its serialisation or its
// numbers needs the difference.
func TestRESTRejectsOutOfRangeOversample(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()

	cases := map[string]string{
		"/v1/vector-search": `{"collection":"c","query":"q","oversample":99}`,
		"/v1/hybrid-search": `{"collection":"c","query":"q","oversample":0.1}`,
		"/v1/cross-search":  `{"targetCollections":["c"],"query":"q","oversample":-5}`,
	}
	handlers := map[string]http.HandlerFunc{
		"/v1/vector-search": srv.handleVectorSearch,
		"/v1/hybrid-search": srv.handleHybridSearch,
		"/v1/cross-search":  srv.handleCrossSearch,
	}
	for path, body := range cases {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		w := httptest.NewRecorder()
		handlers[path](w, req)

		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s gave %d, want 422: %s", path, w.Code, w.Body.String())
		}
		var out struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !strings.Contains(out.Error, "oversample") {
			t.Errorf("%s: the error does not name the parameter: %s", path, w.Body.String())
		}
	}
}

// An omitted parameter must reach the search paths as today's behaviour.
func TestOmittedOversampleIsAccepted(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()

	req := httptest.NewRequest("POST", "/v1/vector-search",
		strings.NewReader(`{"collection":"c","query":"q"}`))
	w := httptest.NewRecorder()
	srv.handleVectorSearch(w, req)

	// It fails later for want of an embedding provider, but it must not be
	// rejected for the parameter it did not send.
	if w.Code == http.StatusUnprocessableEntity {
		t.Errorf("an omitted oversample was rejected: %s", w.Body.String())
	}
}

func TestProfileValidatesOversample(t *testing.T) {
	if err := (&RetrievalProfileDef{Oversample: 99}).Validate(); err == nil {
		t.Error("a profile with an out-of-range oversample was accepted")
	}
	if err := (&RetrievalProfileDef{Oversample: 4}).Validate(); err != nil {
		t.Errorf("a valid oversample was rejected: %v", err)
	}
}

func TestOversampleSurvivesTheProtoRoundTrip(t *testing.T) {
	original := &RetrievalProfileDef{Oversample: 7.5}
	back := retrievalProfileFromProto(retrievalProfileToProto(original))
	if back.Oversample != 7.5 {
		t.Errorf("oversample = %v after the round trip, want 7.5", back.Oversample)
	}
}
