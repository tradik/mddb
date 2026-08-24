package httpclient

import (
	"fmt"
	"net"
	"net/url"
)

// ValidateServiceURL checks a URL that names a service this deployment runs
// itself — today, an embedding provider's `apiUrl` (SEC-013).
//
// These URLs are deliberately outside the SSRF guard: the headline use is a
// local Ollama on http://localhost:11434, which SafeDialContext would refuse.
// The exemption is the feature. What the exemption should not extend to is the
// rest of the private address space: an `apiUrl` of http://169.254.169.254/…
// or http://10.0.0.5:8080/admin is not an embedding service, and reaching it
// through this field is Server-Side Request Forgery whatever the field is
// called. Admin permission gates who can set it, which limits who can aim it —
// not what it can hit, and not what a replicated config carries onward.
//
// So the policy splits the difference where the difference actually is:
//
//   - **Loopback is allowed.** It reaches only this host, which whoever
//     configures the server already controls, and it is the documented setup.
//   - **Everything else follows the ordinary outbound policy**, including
//     `MDDB_OUTBOUND_ALLOW_PRIVATE=true` and `MDDB_OUTBOUND_ALLOWLIST` for an
//     Ollama that lives on another machine on a trusted network.
//
// The check is at configuration time. It is not a dialer: these providers keep
// their own http.Client, so a URL that passes here still dials directly.
// Like ValidateOutboundURL, it returns the URL to use rather than only an
// error, so the unvalidated string cannot be reached for by mistake.
func ValidateServiceURL(raw string) (string, error) {
	if raw == "" {
		return "", nil // Provider falls back to its own default endpoint.
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("apiUrl is not a URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("apiUrl must be http or https, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("apiUrl has no host")
	}

	if isLoopbackHost(host) {
		return u.String(), nil
	}
	if err := validateOutboundURL(u); err != nil {
		return "", fmt.Errorf(
			"apiUrl %q resolves to a private or reserved address. Loopback needs no "+
				"opt-in; for a service elsewhere on a trusted network set "+
				"MDDB_OUTBOUND_ALLOW_PRIVATE=true or add the host to "+
				"MDDB_OUTBOUND_ALLOWLIST: %w", raw, err)
	}
	return u.String(), nil
}

// isLoopbackHost reports whether host names this machine.
//
// A bare "localhost" is treated as loopback without resolving it. Resolution
// is what a rebinding attack subverts, and the answer here is fixed anyway:
// whoever can rewrite this host's name resolution has already won by an easier
// route than an embedding endpoint.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
