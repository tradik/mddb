package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"mddb/internal/httpclient"
)

// Installing a pack the server fetches itself (AGT-005, #378), for the
// clients that cannot upload one: an MCP agent hands over a URL, a gRPC
// client a URL or a stream. The rules are the CLI's — https only, a redirect
// may not leave TLS, an optional sha256 of the pack file checked before
// anything is installed — and the fetch goes through the server's SSRF guard
// (openOutbound), capped at MDDB_CORPUS_MAX_BYTES.

// corpusFetchTimeout bounds one pack download. A pack is a whole collection,
// so it is far longer than the 10 s an imported page gets.
const corpusFetchTimeout = 30 * time.Minute

// corpusFetchClient is the client packs are fetched with: the pooled,
// SSRF-guarded one, with redirects held to https. A test replaces it to
// trust its own TLS server.
var corpusFetchClient = func() *http.Client {
	c := httpclient.NewPooledClientWithTimeout(corpusFetchTimeout)
	c.CheckRedirect = httpsOnlyRedirect(c.CheckRedirect)
	return c
}

// httpsOnlyRedirect refuses a redirect that leaves TLS, then applies next.
func httpsOnlyRedirect(next func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return fmt.Errorf("refusing a redirect to %s: packs are fetched over https only", req.URL.Scheme)
		}
		return next(req, via)
	}
}

// installCorpusFromURL downloads the pack at rawURL and installs it. wantSum,
// when set, is the sha256 the pack file must have.
func (s *Server) installCorpusFromURL(ctx context.Context, rawURL, wantSum string) (*corpusInstallResult, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("%w: %q: packs are fetched over https only", errCorpusInvalid, rawURL)
	}
	resp, err := openOutbound(ctx, corpusFetchClient(), rawURL, http.Header{"User-Agent": {"mddbd/" + VERSION}})
	if err != nil {
		return nil, fmt.Errorf("fetching the pack: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s answered %s", errCorpusInvalid, u.Redacted(), resp.Status)
	}
	return s.installCorpusChecked(resp.Body, wantSum)
}

// installCorpusChecked installs the pack read from r, refusing one larger
// than MDDB_CORPUS_MAX_BYTES. With wantSum set the pack is first written to
// the staging directory and installed only when its sha256 matches, so a
// pack that is not the one asked for changes nothing.
func (s *Server) installCorpusChecked(r io.Reader, wantSum string) (*corpusInstallResult, error) {
	r = &corpusCapReader{r: r, left: corpusMaxBytes()}
	if wantSum == "" {
		return s.installCorpus(r)
	}
	f, err := os.CreateTemp(s.corpusWorkDir(), ".mddb-pack-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		return nil, err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != strings.ToLower(strings.TrimSpace(wantSum)) {
		return nil, fmt.Errorf("%w: checksum mismatch (expected %s, got %s): nothing was installed", errCorpusInvalid, wantSum, got)
	}
	// Read back from the start of what was written and checked.
	return s.installCorpus(io.NewSectionReader(f, 0, n))
}

// errCorpusTooLarge is a pack past MDDB_CORPUS_MAX_BYTES.
var errCorpusTooLarge = fmt.Errorf("%w: the pack is larger than MDDB_CORPUS_MAX_BYTES", errCorpusInvalid)

// corpusCapReader reads at most left bytes from r and fails, rather than
// stopping quietly, on the byte after them.
type corpusCapReader struct {
	r    io.Reader
	left int64
}

// Read implements io.Reader.
func (c *corpusCapReader) Read(p []byte) (int, error) {
	if c.left < 0 {
		return 0, errCorpusTooLarge
	}
	if c.left < int64(len(p)) {
		p = p[:c.left+1] // one byte past the cap tells a larger pack from one that ends there
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	if c.left < 0 {
		return n, errCorpusTooLarge
	}
	return n, err
}
