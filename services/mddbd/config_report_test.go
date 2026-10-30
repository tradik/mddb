package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// TestHandleConfig_ReportsSearchTelemetryAndProxies checks the settings the
// panel shows next to the protocols: the ES subset and its prefix, the
// result cap, OpenTelemetry export (scheme and host only) and how the client
// address is found behind proxies (a count of proxies, never which).
func TestHandleConfig_ReportsSearchTelemetryAndProxies(t *testing.T) {
	withClientAddressing(t)
	t.Setenv("MDDB_ES_COMPAT", "true")
	t.Setenv("MDDB_OTEL_ENDPOINT", "https://user:token@collector.internal:4318/v1/traces?k=1")
	settings := map[string]string{trustedProxiesEnv: "10.0.0.0/8, 192.0.2.7", clientIPHeaderEnv: "CF-Connecting-IP",
		proxyProtocolEnv: "on"}
	if err := configureClientAddressing(func(k string) string { return settings[k] }); err != nil {
		t.Fatal(err)
	}
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.SearchMaxLimit = 250

	rec := httptest.NewRecorder()
	s.handleConfig(rec, httptest.NewRequest(http.MethodGet, "/v1/config", nil))
	var resp ConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.ESCompat.Enabled || resp.ESCompat.Prefix != "/es" || resp.SearchMaxLimit != 250 {
		t.Errorf("search: %+v limit %d", resp.ESCompat, resp.SearchMaxLimit)
	}
	if !resp.Telemetry.Enabled || resp.Telemetry.Endpoint != "https://collector.internal:4318" {
		t.Errorf("telemetry: %+v", resp.Telemetry)
	}
	want := ClientAddressingStatus{TrustedProxies: 2, Header: "Cf-Connecting-Ip", ProxyProtocol: true}
	if resp.ClientAddressing != want {
		t.Errorf("client addressing: %+v, want %+v", resp.ClientAddressing, want)
	}
	body := rec.Body.String()
	if strings.Contains(body, "10.0.0.0") || strings.Contains(body, "192.0.2.7") || strings.Contains(body, "token") {
		t.Errorf("/v1/config leaks a proxy address or a credential: %s", body)
	}

	// The diagnose bundle renders the same fields for a stranger: the scheme.
	if b := s.configResponse(audienceBundle); b.Telemetry.Endpoint != "<set: https>" || b.ClientAddressing.TrustedProxies != 2 {
		t.Errorf("bundle telemetry %+v addressing %+v", b.Telemetry, b.ClientAddressing)
	}
}

// TestHandleConfig_SearchTelemetryAndProxyDefaults is the same report on a
// server with none of them configured.
func TestHandleConfig_SearchTelemetryAndProxyDefaults(t *testing.T) {
	withClientAddressing(t)
	trustedProxies.Store(nil)
	clientIPHeaderSetting.Store(nil)
	proxyProtocolOn.Store(false)
	t.Setenv("MDDB_ES_COMPAT", "")
	t.Setenv("MDDB_OTEL_ENDPOINT", " ")
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	resp := s.configResponse(audienceEndpoint)
	if resp.ESCompat.Enabled || resp.ESCompat.Prefix != "/es" || resp.SearchMaxLimit != defaultSearchMaxLimit {
		t.Errorf("search: %+v limit %d", resp.ESCompat, resp.SearchMaxLimit)
	}
	if resp.Telemetry.Enabled || resp.Telemetry.Endpoint != "" {
		t.Errorf("telemetry: %+v", resp.Telemetry)
	}
	want := ClientAddressingStatus{Header: "X-Forwarded-For"}
	if resp.ClientAddressing != want {
		t.Errorf("client addressing: %+v, want %+v", resp.ClientAddressing, want)
	}
}

// TestRenderConfigOrigin covers the origin class: /v1/config shows the
// collector's scheme and host, the bundle its scheme.
func TestRenderConfigOrigin(t *testing.T) {
	for _, c := range []struct {
		value string
		aud   configAudience
		want  string
	}{
		{"https://u:p@otel.example:4318/v1/traces?k=1#f", audienceEndpoint, "https://otel.example:4318"},
		{"otel.example:4318", audienceEndpoint, cfgSet},
		{"://bad", audienceEndpoint, cfgSet},
		{"http://10.0.0.9:4318", audienceBundle, "<set: http>"},
	} {
		if got := renderConfig("MDDB_OTEL_ENDPOINT", c.value, true, c.aud); got != c.want {
			t.Errorf("renderConfig(MDDB_OTEL_ENDPOINT, %q, %d) = %q, want %q", c.value, c.aud, got, c.want)
		}
	}
}
