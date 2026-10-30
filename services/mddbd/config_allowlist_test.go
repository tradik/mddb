package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// notConfigKeys are MDDB_-prefixed literals that are not configuration: the
// encryption format's magic bytes.
var notConfigKeys = map[string]bool{"MDDB_ENC_V": true, "MDDB_ENC_V1": true, "MDDB_ENC_V2": true}

// serverEnvNames returns every "MDDB_…" string literal in this package's
// non-test source, internal packages included: the variables the server
// reads, plus the few literals notConfigKeys names.
func serverEnvNames(t *testing.T) map[string]bool {
	t.Helper()
	keyRe := regexp.MustCompile(`"(MDDB_[A-Z0-9_]+)`)
	found := map[string]bool{}
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path) // #nosec G304 G122 -- walking this package's own source
		if err != nil {
			return err
		}
		for _, m := range keyRe.FindAllStringSubmatch(string(src), -1) {
			found[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) < 150 {
		t.Fatalf("found %d variables: the scan is broken", len(found))
	}
	return found
}

// TestEveryConfigKeyIsClassified reads the server's source for every
// MDDB_* variable name it mentions and fails on one the allowlist does not
// classify — before it can leak into a diagnose bundle (OPS-025). Classify a
// new variable in config_allowlist.go: public only when its value can never
// name a host, a path, a person or a secret.
func TestEveryConfigKeyIsClassified(t *testing.T) {
	found := serverEnvNames(t)
	var missing, stale []string
	for k := range found {
		if _, ok := configKeys[k]; !ok && !notConfigKeys[k] {
			missing = append(missing, k)
		}
	}
	for k := range configKeys {
		if !found[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("unclassified configuration variables (add them to configKeys): %s", strings.Join(missing, ", "))
	}
	if len(stale) > 0 {
		t.Errorf("configKeys classifies variables the server no longer reads: %s", strings.Join(stale, ", "))
	}
}

func TestRenderConfigByClassAndAudience(t *testing.T) {
	for _, c := range []struct {
		key, value string
		set        bool
		aud        configAudience
		want       string
	}{
		{"MDDB_NOT_A_KEY", "v", true, audienceBundle, cfgUnclassified},
		{"MDDB_MODE", "", false, audienceBundle, cfgUnset},
		{"MDDB_HTTP_ADDR", "", false, audienceEndpoint, ""},
		{"MDDB_MODE", "read", true, audienceBundle, "read"},
		{"MDDB_AUTH_JWT_SECRET", "s3cret", true, audienceEndpoint, cfgSet},
		{"MDDB_EMBEDDING_API_URL", "https://u:p@api.example/v1?k=1#f", true, audienceEndpoint, "https://api.example/v1"},
		{"MDDB_EMBEDDING_API_URL", "://bad", true, audienceEndpoint, cfgSet},
		{"MDDB_PATH", "/data/mddb.db", true, audienceEndpoint, "/data/mddb.db"},
		{"MDDB_HTTP_ADDR", "10.0.0.5:11023", true, audienceBundle, ":11023"},
		{"MDDB_HTTP_ADDR", "/run/mddb.sock", true, audienceBundle, cfgSet},
		{"MDDB_EMBEDDING_API_URL", "http://10.0.0.9:11434", true, audienceBundle, "<set: http>"},
		{"MDDB_EMBEDDING_API_URL", "no-scheme", true, audienceBundle, cfgSet},
		{"MDDB_TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.1", true, audienceBundle, "<2 entries>"},
		{"MDDB_PATH", "/data/mddb.db", true, audienceBundle, cfgSet},
	} {
		if got := renderConfig(c.key, c.value, c.set, c.aud); got != c.want {
			t.Errorf("renderConfig(%s, %q, %v, %d) = %q, want %q", c.key, c.value, c.set, c.aud, got, c.want)
		}
	}
}

func TestConfigEndpointAndBundleShareTheAllowlist(t *testing.T) {
	t.Setenv("MDDB_EMBEDDING_PROVIDER", "openai")
	t.Setenv("MDDB_EMBEDDING_API_URL", "https://user:token@proxy.internal/v1")
	t.Setenv("MDDB_JWT_TYPO", "x")
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Config.HTTP.Addr = "10.1.2.3:11023"

	rec := httptest.NewRecorder()
	s.handleConfig(rec, httptest.NewRequest(http.MethodGet, "/v1/config", nil))
	var endpoint ConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &endpoint); err != nil {
		t.Fatal(err)
	}
	// The endpoint keeps what an operator needs and drops the credentials.
	if endpoint.VectorConfig.APIURL != "https://proxy.internal/v1" || endpoint.Protocols.HTTP.Addr != "10.1.2.3:11023" ||
		endpoint.DatabasePath != s.Path {
		t.Errorf("/v1/config %+v %+v", endpoint.VectorConfig, endpoint.Protocols.HTTP)
	}
	bundle := s.configResponse(audienceBundle)
	if bundle.VectorConfig.APIURL != "<set: https>" || bundle.Protocols.HTTP.Addr != ":11023" || bundle.DatabasePath != cfgSet {
		t.Errorf("bundle %+v %+v %q", bundle.VectorConfig, bundle.Protocols.HTTP, bundle.DatabasePath)
	}
	if env := envSnapshot(); env["MDDB_JWT_TYPO"] != cfgUnclassified || env["MDDB_EMBEDDING_PROVIDER"] != "openai" {
		t.Errorf("env %v", env)
	}
	if set := configSettings(audienceEndpoint); set["MDDB_EMBEDDING_API_URL"] != "https://proxy.internal/v1" {
		t.Errorf("settings %v", set)
	}
}
