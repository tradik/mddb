package httpclient

import (
	"strings"
	"testing"
)

// SEC-013. An embedding provider's apiUrl deliberately bypasses the SSRF
// guard so a local Ollama keeps working. These tests pin where that exemption
// stops.

func TestValidateServiceURLAllowsLoopbackWithoutOptIn(t *testing.T) {
	// The documented setup. If this ever needs an environment variable, the
	// out-of-the-box Ollama instructions are broken.
	for _, raw := range []string{
		"http://localhost:11434",
		"http://127.0.0.1:11434",
		"http://127.0.0.1:11434/api",
		"https://localhost:8443",
		"http://[::1]:11434",
	} {
		if err := ValidateServiceURL(raw); err != nil {
			t.Errorf("%s was refused: %v", raw, err)
		}
	}
}

func TestValidateServiceURLBlocksTheRestOfThePrivateSpace(t *testing.T) {
	// Each of these is a Server-Side Request Forgery target that arrived
	// through a field named apiUrl.
	for _, raw := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5:8080/admin",
		"http://192.168.1.1",
		"http://172.16.0.2:9200",
		"http://100.64.0.1",
	} {
		err := ValidateServiceURL(raw)
		if err == nil {
			t.Errorf("%s was accepted", raw)
			continue
		}
		// The message has to name the way out, or an operator with a real
		// Ollama on their LAN is stuck at a refusal with no next step.
		if !strings.Contains(err.Error(), "MDDB_OUTBOUND_ALLOW_PRIVATE") {
			t.Errorf("%s: refusal does not say how to opt in: %v", raw, err)
		}
	}
}

func TestValidateServiceURLHonoursTheOptIn(t *testing.T) {
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "true")
	if err := ValidateServiceURL("http://10.0.0.5:11434"); err != nil {
		t.Errorf("the opt-in did not take: %v", err)
	}
}

func TestValidateServiceURLHonoursTheAllowlist(t *testing.T) {
	t.Setenv("MDDB_OUTBOUND_ALLOWLIST", "ollama.internal")
	if err := ValidateServiceURL("http://ollama.internal:11434"); err != nil {
		t.Errorf("the allowlist did not take: %v", err)
	}
}

func TestValidateServiceURLAllowsAPublicEndpoint(t *testing.T) {
	// The hosted providers still have to work.
	if err := ValidateServiceURL("https://api.openai.com/v1"); err != nil {
		t.Errorf("a public endpoint was refused: %v", err)
	}
}

func TestValidateServiceURLAcceptsAnEmptyValue(t *testing.T) {
	// Empty means "use the provider's own default endpoint", which is a
	// constant in our code rather than anything a client supplied.
	if err := ValidateServiceURL(""); err != nil {
		t.Errorf("an empty apiUrl was refused: %v", err)
	}
}

func TestValidateServiceURLRejectsWhatIsNotAnHTTPURL(t *testing.T) {
	cases := map[string]string{
		"a scheme that is not HTTP": "file:///etc/passwd",
		"gopher":                    "gopher://127.0.0.1:11211/_stats",
		"no host":                   "http://",
		"not a URL at all":          "://nonsense",
	}
	for name, raw := range cases {
		if err := ValidateServiceURL(raw); err == nil {
			t.Errorf("%s (%q) was accepted", name, raw)
		}
	}
}

// Loopback is decided without resolving the name. Resolution is what a
// rebinding attack subverts, and "localhost" is not a name worth re-checking.
func TestIsLoopbackHost(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "127.1.2.3", "::1"} {
		if !isLoopbackHost(host) {
			t.Errorf("%s should be loopback", host)
		}
	}
	for _, host := range []string{"example.com", "10.0.0.1", "169.254.169.254", ""} {
		if isLoopbackHost(host) {
			t.Errorf("%s should not be loopback", host)
		}
	}
}
