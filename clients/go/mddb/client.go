// Package mddb is the official Go client for the MDDB document server.
//
// It is the single shared HTTP client used by mddb-cli and by external Go
// integrations, so neither has to re-implement request building, authentication
// or response parsing (GO-015 — previously mddb-cli carried its own copy).
//
// Typical use:
//
//	c := mddb.New("http://localhost:8080", mddb.WithAPIKey(os.Getenv("MDDB_API_KEY")))
//	doc, err := c.Add(ctx, mddb.AddRequest{Collection: "blog", Key: "hello", Lang: "en", ContentMD: "# Hi"})
//
// Timeouts: a call whose context has no deadline is bounded by the client's
// default timeout (DefaultTimeout, changed with WithTimeout). A call whose
// context carries its own deadline uses that deadline instead, so a long bulk
// ingest or reindex can be given more than the default by the caller.
package mddb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultTimeout is the request timeout applied to calls whose context has no
// deadline of its own.
const DefaultTimeout = 30 * time.Second

// Client talks to an MDDB server over its HTTP/JSON API. Construct one with New
// and reuse it; it is safe for concurrent use by multiple goroutines.
type Client struct {
	baseURL string
	httpc   *http.Client
	apiKey  string
	token   string
	verbose io.Writer
	timeout time.Duration // applied only when the call's context has no deadline

	followLeader bool                                             // WithFollowLeader (GO-051)
	leader       leaderState                                      // the learned cluster leader
	sleep        func(ctx context.Context, d time.Duration) error // election waits; replaced in tests
}

// Option configures a Client at construction time.
type Option func(*Client)

// WithAPIKey authenticates requests with the X-API-Key header.
func WithAPIKey(key string) Option { return func(c *Client) { c.apiKey = key } }

// WithToken authenticates requests with a Bearer JWT (Authorization header).
// Ignored when an API key is also set (the API key takes precedence).
func WithToken(tok string) Option { return func(c *Client) { c.token = tok } }

// WithHTTPClient supplies a custom *http.Client (transport, proxies). A
// Timeout set on h is a hard cap the client cannot lift; leave it zero and use
// WithTimeout or a context deadline instead.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpc = h
		}
	}
}

// WithTimeout sets the timeout applied to calls whose context has no deadline
// (default DefaultTimeout; 0 disables it). A context deadline always wins, so
// it may be longer or shorter than d.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.timeout = d }
}

// WithVerbose writes the JSON request body of each call to w (e.g. os.Stderr).
func WithVerbose(w io.Writer) Option { return func(c *Client) { c.verbose = w } }

// New returns a Client for the given base URL (e.g. "http://localhost:8080").
// A trailing slash on baseURL is trimmed so callers may pass either form.
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpc:   &http.Client{},
		timeout: DefaultTimeout,
		sleep:   sleepCtx,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// BaseURL returns the server base URL the client targets.
func (c *Client) BaseURL() string { return c.baseURL }

// APIError is returned for any non-2xx/3xx HTTP response.
//
// A cluster node that is not the leader refuses a write with 421: LeaderAddr
// is then the leader's HTTP base URL (from the X-MDDB-Leader header, else the
// body's leaderAddr), with LeaderID and Term from the body. A 503 sent while
// an election is in progress has NoLeader set. WithFollowLeader acts on both.
type APIError struct {
	StatusCode int
	Body       string
	LeaderAddr string // 421: where the write belongs
	LeaderID   string // 421: the leader's node ID
	Term       int64  // 421 and 503 "no leader": the election term
	NoLeader   bool   // 503: no leader is known yet
}

func (e *APIError) Error() string {
	return fmt.Sprintf("mddb: server error (%d): %s", e.StatusCode, e.Body)
}

// Do issues an HTTP request and returns the raw response body. It marshals body
// to JSON when non-nil, applies authentication, and converts any HTTP status
// >= 400 into an *APIError. It is the low-level escape hatch behind every typed
// method; callers needing an undocumented endpoint can use it directly.
func (c *Client) Do(ctx context.Context, method, path string, body any) ([]byte, error) {
	status, header, respBody, err := c.send(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, newAPIError(status, header, respBody)
	}
	return respBody, nil
}

// send issues the request and returns the status code, headers and body
// without judging the status, so callers such as Status can read a non-2xx
// body.
func (c *Client) send(ctx context.Context, method, path string, body any) (int, http.Header, []byte, error) {
	if _, ok := ctx.Deadline(); !ok && c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	rb := &requestBody{follow: c.followLeader}
	contentType := ""
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("mddb: encode request: %w", err)
		}
		rb.buf, contentType = data, "application/json"
		if c.verbose != nil {
			_, _ = fmt.Fprintf(c.verbose, "Request: %s\n", data)
		}
	}

	resp, err := c.exchange(ctx, method, path, contentType, rb)
	if err != nil {
		return 0, nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, err
	}
	return resp.StatusCode, resp.Header, respBody, nil
}

// authorize sets the request's credentials: the API key, or else the token.
func (c *Client) authorize(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	} else if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// DoStream issues a request whose body is body, sent as it is read, with
// Content-Type contentType (none when empty), and returns the response body
// unread. It is for transfers too large to hold in memory — corpus packs
// (AGT-005) are gigabytes. The caller closes the body. A status >= 400 is an
// *APIError carrying the body, which is then read and closed here.
//
// No default timeout applies: a transfer of that size has no sensible fixed
// bound, so the context alone limits the call.
//
// With WithFollowLeader a 421 is followed only when body is nil or an
// io.Seeker (an *os.File is), since the body must be sent a second time;
// otherwise the leader is remembered for the next call and the 421 is
// returned as an *APIError carrying LeaderAddr.
func (c *Client) DoStream(ctx context.Context, method, path, contentType string, body io.Reader) (io.ReadCloser, error) {
	resp, err := c.exchange(ctx, method, path, contentType, &requestBody{stream: body, follow: c.followLeader})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer func() { _ = resp.Body.Close() }()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, newAPIError(resp.StatusCode, resp.Header, msg)
	}
	return resp.Body, nil
}

// doJSON issues a request and unmarshals the response body into out (when non-nil).
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	data, err := c.Do(ctx, method, path, body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("mddb: decode response: %w", err)
	}
	return nil
}
