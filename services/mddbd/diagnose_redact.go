package main

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Redaction for the diagnose bundle (OPS-025). The rule throughout is an
// allowlist: what is kept is named — a collection becomes its hash, a string
// survives only under a key known to hold an enumeration or a timestamp, a
// metric label only as a lowercase identifier — and everything else is
// replaced. A field added to a handler later is redacted until someone
// decides it is safe, rather than shipped until someone notices.

// redacted replaces a value the bundle does not carry.
const redacted = "<redacted>"

// collectionHash is a collection's name in the bundle: c_ and the first 8
// hex digits of its SHA-256, the same in every file so they can be joined.
func collectionHash(name string) string {
	sum := sha256.Sum256([]byte(name))
	return "c_" + hex.EncodeToString(sum[:4])
}

// bundleNames maps the collections the bundle may mention to the name it
// shows: their hash, or themselves under --keep-names.
type bundleNames struct {
	names map[string]string
	keep  bool
}

func newBundleNames(collections []string, keep bool) *bundleNames {
	b := &bundleNames{names: make(map[string]string, len(collections)), keep: keep}
	for _, c := range collections {
		if c != "" {
			b.names[c] = c
			if !keep {
				b.names[c] = collectionHash(c)
			}
		}
	}
	return b
}

// name is how the bundle shows collection c (hashed even when it was not
// among the known ones).
func (b *bundleNames) name(c string) string {
	if n, ok := b.names[c]; ok {
		return n
	}
	if b.keep {
		return c
	}
	return collectionHash(c)
}

// lookup reports the bundle name of s when s is a known collection.
func (b *bundleNames) lookup(s string) (string, bool) {
	n, ok := b.names[s]
	return n, ok
}

// safeStringKeys are the JSON keys whose string values the bundle keeps:
// enumerations, timestamps, durations and model names. Any other string is
// replaced, whatever it holds.
var safeStringKeys = map[string]bool{
	"mode": true, "uptime": true, "statsComputedAt": true, "provider": true, "model": true,
	"quantization": true, "chunkMode": true, "oldestPendingAt": true, "algorithm": true,
	"state": true, "status": true, "phase": true, "role": true, "type": true, "kind": true,
	"since": true, "startedAt": true, "finishedAt": true, "updatedAt": true, "format": true, "wait": true,
}

// scrubJSON walks a decoded JSON value: collection names become their bundle
// name wherever they appear, as a key or as a value; numbers and booleans
// stay; a string under any key outside safeStringKeys is replaced.
func (b *bundleNames) scrubJSON(key string, v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			nk := k
			if n, ok := b.lookup(k); ok {
				nk = n
			}
			out[nk] = b.scrubJSON(k, val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = b.scrubJSON(key, val)
		}
		return out
	case string:
		if n, ok := b.lookup(t); ok {
			return n
		}
		if safeStringKeys[key] {
			return t
		}
		return redacted
	default:
		return v
	}
}

var (
	// promLabelRe finds one label pair of a Prometheus series.
	promLabelRe = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\.)*)"`)
	// safeLabelValue is a lowercase identifier: an operation, a mode, an
	// algorithm, a tool name.
	safeLabelValue = regexp.MustCompile(`^[a-z0-9_.+:/-]{0,48}$`)
	// fixedLabelValues are the upper-case values a label may hold: HTTP
	// methods and the histogram's last bound.
	fixedLabelValues = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
		"HEAD": true, "OPTIONS": true, "+Inf": true}
)

// scrubMetrics redacts a /metrics snapshot: collection labels become their
// bundle name, an HTTP path is cut to its endpoint (a path can carry a user
// name, a key hash or a job ID), and any other label value survives only as
// a lowercase identifier that is not a collection.
func (b *bundleNames) scrubMetrics(text string) string {
	return promLabelRe.ReplaceAllStringFunc(text, func(pair string) string {
		m := promLabelRe.FindStringSubmatch(pair)
		label, value := m[1], m[2]
		switch {
		case label == "path":
			value = endpointOf(value)
		case value == "" || fixedLabelValues[value]:
		default:
			if n, ok := b.lookup(value); ok {
				value = n
			} else if !safeLabelValue.MatchString(value) {
				value = redacted
			}
		}
		return label + `="` + value + `"`
	})
}

// endpointOf cuts a request path to its endpoint: "/v1/" and the first
// segment, with "/*" when more followed.
func endpointOf(path string) string {
	rest, ok := strings.CutPrefix(path, "/v1/")
	if !ok {
		if path == "/health" || path == "/metrics" {
			return path
		}
		return "/other"
	}
	first, more, _ := strings.Cut(rest, "/")
	if more != "" || strings.HasSuffix(rest, "/") {
		return "/v1/" + first + "/*"
	}
	return "/v1/" + first
}
