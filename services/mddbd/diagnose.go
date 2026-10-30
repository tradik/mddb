package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	json "mddb/internal/jsonx"
)

// GET /v1/admin/diagnose (OPS-025): one tar.gz a user can attach to an issue,
// describing the instance without its data. `mddb-cli diagnose` saves it.
//
// Every file is built already redacted (diagnose_parts.go,
// diagnose_redact.go, config_allowlist.go) and listed in MANIFEST.json with
// its size, its SHA-256 and the redaction applied, so a user can read what
// they are about to send — `mddb-cli diagnose --inspect` prints the manifest
// without writing anything. TestDiagnoseBundleCarriesNoCanary is the privacy
// contract: seeded strings must appear in no file.
//
// Read-only: it works on a read-only instance and on a replication follower.

const (
	// diagnoseTimeout bounds the default bundle; the opt-in integrity check
	// gets diagnoseIntegrityTimeout instead.
	diagnoseTimeout          = 30 * time.Second
	diagnoseIntegrityTimeout = 30 * time.Minute
	// maxDiagnoseFile is the most one file may hold; a larger one is left
	// out and the manifest says why.
	maxDiagnoseFile = 8 << 20
)

// neverInBundle is what the bundle promises not to carry, repeated in the
// manifest so the promise travels with the file.
var neverInBundle = []string{
	"document content, metadata values, document IDs and keys",
	"query text, vectors, summaries",
	"API keys, tokens, passwords, DSNs, request headers",
	"actors, user names, IP addresses",
	"file paths and host names",
	"collection names, unless the bundle was made with keepNames",
}

// diagnoseOptions are the bundle's opt-in parts.
type diagnoseOptions struct {
	Profiles  bool `json:"profiles"`
	Integrity bool `json:"integrity"`
	KeepNames bool `json:"keepNames"`
}

// manifestFile describes one file of the bundle.
type manifestFile struct {
	Name      string `json:"name"`
	Size      int    `json:"size"`
	SHA256    string `json:"sha256"`
	Redaction string `json:"redaction"`
}

// manifestOmission is a part the bundle does not carry, and why.
type manifestOmission struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// diagnoseManifest is MANIFEST.json.
type diagnoseManifest struct {
	Format    int                `json:"format"`
	Version   string             `json:"version"`
	CreatedAt string             `json:"createdAt"`
	Options   diagnoseOptions    `json:"options"`
	Files     []manifestFile     `json:"files"`
	Omitted   []manifestOmission `json:"omitted"`
	Never     []string           `json:"neverIncluded"`
}

// diagnosePart is one file to build.
type diagnosePart struct {
	name      string
	redaction string
	build     func(ctx context.Context) ([]byte, error)
	// skip, when set, is why the part is not built (an option off, a
	// subsystem disabled).
	skip string
}

// jsonPart encodes v as an indented JSON file, "<set>" written as such
// rather than HTML-escaped.
func jsonPart(v any, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err = enc.Encode(v)
	return buf.Bytes(), err
}

// diagnoseParts lists the bundle's files in the order they are written.
func (s *Server) diagnoseParts(opts diagnoseOptions, names *bundleNames) []diagnosePart {
	collections := "collections hashed (c_ + 8 hex of SHA-256)"
	if opts.KeepNames {
		collections = "collection names kept (keepNames)"
	}
	handler := func(name, path string, h http.HandlerFunc) diagnosePart {
		return diagnosePart{name: name, redaction: collections + "; strings other than enumerations and timestamps replaced",
			build: func(ctx context.Context) ([]byte, error) { return s.handlerPart(ctx, h, path, names) }}
	}
	parts := []diagnosePart{
		{name: "build.json", redaction: "none: build and runtime facts only",
			build: func(context.Context) ([]byte, error) { return jsonPart(s.buildPart(), nil) }},
		{name: "config.json", redaction: "configuration allowlist: secrets set/unset, paths and hosts set/unset, URLs scheme only, listen addresses port only, host lists counted; " + collections,
			build: func(context.Context) ([]byte, error) { return jsonPart(s.configPart(names), nil) }},
		{name: "env.json", redaction: "MDDB_* names; values through the configuration allowlist, unclassified ones withheld",
			build: func(context.Context) ([]byte, error) { return jsonPart(envSnapshot(), nil) }},
		handler("stats.json", "/v1/stats", s.handleStats),
		handler("storage.json", "/v1/storage", s.handleStorage),
		handler("vector-stats.json", "/v1/vector-stats", s.handleVectorStats),
		handler("indexing.json", "/v1/indexing-status", s.handleIndexingStatus),
		{name: "metrics.prom", redaction: "collection labels hashed; HTTP paths cut to their endpoint; other label values kept only as lowercase identifiers",
			build: func(ctx context.Context) ([]byte, error) { return s.metricsPart(ctx, names), nil }},
		{name: "query-shape.json", redaction: "counters only; " + collections,
			build: func(context.Context) ([]byte, error) {
				return jsonPart(s.Metrics.QueryShape().Report(names.name), nil)
			}},
		{name: "audit-summary.json", redaction: "action × result counts; no actors, IPs, keys or details",
			build: func(context.Context) ([]byte, error) { return jsonPart(s.auditPart()) }},
		{name: "errors.json", redaction: "error class and file:line of the last 200 errors; no messages",
			build: func(context.Context) ([]byte, error) { return jsonPart(errorsPart(), nil) }},
		{name: "goroutines.pprof", redaction: "goroutine profile: function names and source file:line of the build; no values",
			build: func(context.Context) ([]byte, error) { return profilePart("goroutine") }},
		{name: "heap.pprof", redaction: "heap profile: allocation sites (function names, source file:line); no values",
			build: func(context.Context) ([]byte, error) { return profilePart("heap") }},
		{name: "integrity.json", redaction: "problem counts per database file, no bbolt messages; " + collections,
			build: func(ctx context.Context) ([]byte, error) { return jsonPart(s.integrityPart(ctx, names)) }},
	}
	for i := range parts {
		switch p := &parts[i]; {
		case p.name == "audit-summary.json" && (s.AuditManager == nil || !s.AuditManager.Enabled()):
			p.skip = "audit log disabled"
		case (p.name == "goroutines.pprof" || p.name == "heap.pprof") && !opts.Profiles:
			p.skip = "opt-in: profiles"
		case p.name == "integrity.json" && !opts.Integrity:
			p.skip = "opt-in: integrity"
		}
	}
	return parts
}

// builtPart is a part's outcome: its bytes, or why the bundle goes without.
type builtPart struct {
	body   []byte
	reason string
}

// runPart builds one part, unless it is skipped, failed or too large.
func runPart(ctx context.Context, p diagnosePart) builtPart {
	if p.skip != "" {
		return builtPart{reason: p.skip}
	}
	body, err := p.build(ctx)
	switch {
	case err != nil:
		return builtPart{reason: "failed: " + errorClass(err)}
	case len(body) > maxDiagnoseFile:
		return builtPart{reason: "too large"}
	}
	return builtPart{body: body}
}

// writeDiagnoseBundle builds every part and writes the tar.gz to w:
// MANIFEST.json first, then the files in order. Parts are built in memory
// first, because the manifest that leads the archive lists their hashes.
func (s *Server) writeDiagnoseBundle(ctx context.Context, w io.Writer, opts diagnoseOptions) error {
	now := time.Now().UTC()
	names := newBundleNames(s.knownCollections(), opts.KeepNames)
	man := diagnoseManifest{Format: 1, Version: VERSION, CreatedAt: now.Format(time.RFC3339), Options: opts,
		Files: []manifestFile{}, Omitted: []manifestOmission{}, Never: neverInBundle}
	type file struct {
		name string
		body []byte
	}
	var files []file
	for _, p := range s.diagnoseParts(opts, names) {
		built := runPart(ctx, p)
		if built.reason != "" {
			man.Omitted = append(man.Omitted, manifestOmission{Name: p.name, Reason: built.reason})
			continue
		}
		sum := sha256.Sum256(built.body)
		man.Files = append(man.Files, manifestFile{Name: p.name, Size: len(built.body), SHA256: hex.EncodeToString(sum[:]),
			Redaction: p.redaction})
		files = append(files, file{p.name, built.body})
	}
	manifest, _ := json.MarshalIndent(man, "", "  ") // strings, numbers and booleans: cannot fail
	zw := gzip.NewWriter(w)
	tw := tar.NewWriter(zw)
	var err error
	for _, f := range append([]file{{"MANIFEST.json", manifest}}, files...) {
		err = errors.Join(err, tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body)),
			ModTime: now, Typeflag: tar.TypeReg, Format: tar.FormatPAX}))
		_, werr := tw.Write(f.body)
		err = errors.Join(err, werr)
	}
	return errors.Join(err, tw.Close(), zw.Close())
}

// handleDiagnose serves GET /v1/admin/diagnose[?profiles=true][&integrity=true][&keepNames=true].
func (s *Server) handleDiagnose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !requireAdmin(w, r, s) {
		return
	}
	q := r.URL.Query()
	opts := diagnoseOptions{Profiles: q.Get("profiles") == "true", Integrity: q.Get("integrity") == "true",
		KeepNames: q.Get("keepNames") == "true"}
	timeout := diagnoseTimeout
	if opts.Integrity {
		timeout = diagnoseIntegrityTimeout
		liftWriteDeadline(w, r) // a large database outlasts the write timeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="mddb-diagnose-`+time.Now().UTC().Format("20060102-150405")+`.tar.gz"`)
	w.WriteHeader(http.StatusOK)
	if err := s.writeDiagnoseBundle(ctx, w, opts); err != nil {
		slog.Debug("diagnose bundle not delivered", "class", errorClass(err)) // the client went away
	}
}
