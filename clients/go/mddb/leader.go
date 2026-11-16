package mddb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cluster leader following (GO-051).
//
// A node of an mddbd cluster that is not the leader answers a write with
// 421 Misdirected Request and names the leader in the X-MDDB-Leader header
// (and in the body's leaderAddr). While an election runs and no leader is
// known it answers 503 with Retry-After. Reads are served by every node.
//
// With WithFollowLeader(true) the client resends such a write once to the
// leader and remembers it: a request (method and path) that was redirected
// once is a write, so later requests of that kind go to the leader directly,
// while every other request keeps going to the configured base URL. Without
// the option nothing is followed and the 421 becomes an *APIError whose
// LeaderAddr says where the write belongs.

const (
	// headerLeader names the leader's HTTP base URL on a 421 answer.
	headerLeader = "X-MDDB-Leader"
	// maxNoLeaderWaits bounds the retries of a 503 "no leader" answer.
	maxNoLeaderWaits = 3
	// maxNoLeaderWait caps a server's Retry-After so a bad value cannot
	// stall the caller.
	maxNoLeaderWait = 3 * time.Second
	// defaultNoLeaderWait is used when the 503 carries no usable Retry-After.
	defaultNoLeaderWait = time.Second
	// maxWriteRoutes bounds the remembered write routes; past it they are
	// forgotten and learned again, one redirect each.
	maxWriteRoutes = 1024
	// maxErrorBody bounds how much of an error answer is read.
	maxErrorBody = 1 << 20
	// noLeaderPrefix starts the error message of a 503 "no leader" body.
	noLeaderPrefix = "no leader"
)

// errBodyNotReplayable is returned when a request body must be sent again
// but can be read only once.
var errBodyNotReplayable = errors.New("mddb: request body cannot be sent again")

// WithFollowLeader makes the client follow a cluster leader (GO-051): a
// write refused with 421 is resent once to the leader the answer names, and
// a 503 sent while an election is in progress is retried up to three times,
// honouring Retry-After (capped at three seconds). Off by default.
func WithFollowLeader(on bool) Option { return func(c *Client) { c.followLeader = on } }

// Leader returns the leader base URL the client has learned, or "" when it
// has not followed one (or forgot it after the leader stopped answering).
func (c *Client) Leader() string {
	c.leader.mu.Lock()
	defer c.leader.mu.Unlock()
	return c.leader.url
}

// leaderState is the learned leader and the requests known to be writes.
type leaderState struct {
	mu     sync.Mutex
	url    string
	writes map[string]bool
}

// errorBody is the JSON body of a 421 or 503 cluster answer.
type errorBody struct {
	Error      string `json:"error"`
	LeaderAddr string `json:"leaderAddr"`
	LeaderID   string `json:"leaderId"`
	Term       int64  `json:"term"`
}

// newAPIError builds the error for an answer with status >= 400, filling the
// leader fields a 421 or a 503 "no leader" answer carries.
func newAPIError(status int, header http.Header, body []byte) *APIError {
	e := &APIError{StatusCode: status, Body: string(body)}
	if status != http.StatusMisdirectedRequest && status != http.StatusServiceUnavailable {
		return e
	}
	var b errorBody
	_ = json.Unmarshal(body, &b)
	switch {
	case status == http.StatusMisdirectedRequest:
		e.LeaderAddr = header.Get(headerLeader)
		if e.LeaderAddr == "" {
			e.LeaderAddr = b.LeaderAddr
		}
		e.LeaderID, e.Term = b.LeaderID, b.Term
	case strings.HasPrefix(b.Error, noLeaderPrefix):
		e.NoLeader, e.Term = true, b.Term
	}
	return e
}

// routeKey identifies a kind of request: method and path, without a query.
func routeKey(method, path string) string {
	path, _, _ = strings.Cut(path, "?")
	return method + " " + path
}

// target is the base URL a request of kind key goes to first.
func (c *Client) target(key string) string {
	if !c.followLeader {
		return c.baseURL
	}
	c.leader.mu.Lock()
	defer c.leader.mu.Unlock()
	if c.leader.url != "" && c.leader.writes[key] {
		return c.leader.url
	}
	return c.baseURL
}

// rememberLeader records the leader and that key is a write.
func (c *Client) rememberLeader(key, leader string) {
	c.leader.mu.Lock()
	defer c.leader.mu.Unlock()
	if c.leader.writes == nil || len(c.leader.writes) >= maxWriteRoutes {
		c.leader.writes = map[string]bool{}
	}
	c.leader.url = leader
	c.leader.writes[key] = true
}

// forgetLeader drops the learned leader when base is it and it could not be
// reached, so the next write asks the configured node again.
func (c *Client) forgetLeader(base string) {
	c.leader.mu.Lock()
	defer c.leader.mu.Unlock()
	if base != c.baseURL && base == c.leader.url {
		c.leader.url = ""
	}
}

// leaderURL validates a leader address: only an absolute http or https URL
// is followed. It returns the URL without a trailing slash, or "".
func leaderURL(addr string) string {
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return strings.TrimRight(addr, "/")
}

// peekBody reads a (small) error answer and puts it back, so it can be
// inspected here and still become an *APIError later.
func peekBody(resp *http.Response) []byte {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(data))
	return data
}

// retryAfter is the wait a 503 asks for, capped at maxNoLeaderWait.
func retryAfter(h http.Header) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After")))
	if err != nil || secs < 0 {
		return defaultNoLeaderWait
	}
	return min(time.Duration(secs)*time.Second, maxNoLeaderWait)
}

// sleepCtx waits d, or less when ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// route is one request's progress through redirects and election waits.
type route struct {
	key        string
	base       string
	redirected bool
	waits      int
}

// next decides whether resp is answered by another attempt: a 421 naming a
// usable leader moves the request there once; a 503 sent during an election
// waits and asks the same node again, at most maxNoLeaderWaits times.
func (c *Client) next(ctx context.Context, rt *route, resp *http.Response, replayable bool) (bool, error) {
	switch resp.StatusCode {
	case http.StatusMisdirectedRequest:
		if rt.redirected {
			return false, nil
		}
		e := newAPIError(resp.StatusCode, resp.Header, peekBody(resp))
		leader := leaderURL(e.LeaderAddr)
		if leader == "" {
			return false, nil
		}
		c.rememberLeader(rt.key, leader)
		rt.base, rt.redirected = leader, true
		return replayable, nil
	case http.StatusServiceUnavailable:
		if !replayable || rt.waits >= maxNoLeaderWaits {
			return false, nil
		}
		if !newAPIError(resp.StatusCode, resp.Header, peekBody(resp)).NoLeader {
			return false, nil
		}
		rt.waits++
		return true, c.sleep(ctx, retryAfter(resp.Header))
	}
	return false, nil
}

// exchange sends a request, following the leader when the client is set to,
// and returns the final answer with its body unread (an error answer that was
// inspected on the way is buffered back).
func (c *Client) exchange(ctx context.Context, method, path, contentType string, body *requestBody) (*http.Response, error) {
	key := routeKey(method, path)
	rt := &route{key: key, base: c.target(key)}
	for {
		resp, err := c.attempt(ctx, method, rt.base+path, contentType, body)
		if err != nil {
			c.forgetLeader(rt.base)
			return nil, err
		}
		if !c.followLeader {
			return resp, nil
		}
		again, err := c.next(ctx, rt, resp, body.replayable())
		if err != nil || again {
			_ = resp.Body.Close()
		}
		if err != nil {
			return nil, err
		}
		if !again {
			return resp, nil
		}
	}
}

// attempt sends one request to rawURL.
func (c *Client) attempt(ctx context.Context, method, rawURL, contentType string, body *requestBody) (*http.Response, error) {
	r, err := body.open()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, r)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.authorize(req)
	return c.httpc.Do(req)
}

// requestBody is a request body that can be sent more than once: an encoded
// buffer always can, a stream only when it is an io.Seeker. follow is the
// client's WithFollowLeader setting; without it a stream is handed to the
// transport untouched, exactly as before GO-051.
type requestBody struct {
	buf    []byte
	stream io.Reader
	follow bool
	opened bool
	start  int64
}

// replayable reports whether open may be called again after the first time.
func (b *requestBody) replayable() bool {
	if b.stream == nil {
		return true
	}
	_, ok := b.stream.(io.Seeker)
	return ok
}

// open returns the body for one attempt. A seekable stream is rewound to
// where it stood on the first attempt and hidden from the transport's Close,
// which would otherwise close a file before it could be sent again.
func (b *requestBody) open() (io.Reader, error) {
	switch {
	case b.stream == nil && b.buf == nil:
		return nil, nil
	case b.stream == nil:
		return bytes.NewReader(b.buf), nil
	case !b.follow:
		return b.stream, nil
	}
	s, seekable := b.stream.(io.Seeker)
	if !seekable {
		if b.opened {
			return nil, errBodyNotReplayable
		}
		b.opened = true
		return b.stream, nil
	}
	offset, whence := b.start, io.SeekStart
	if !b.opened {
		offset, whence = 0, io.SeekCurrent
	}
	at, err := s.Seek(offset, whence)
	if err != nil {
		return nil, err
	}
	b.start, b.opened = at, true
	return keepOpen(b.stream), nil
}

// keepOpen hides r's Close method, if it has one, from the HTTP transport.
func keepOpen(r io.Reader) io.Reader {
	if _, ok := r.(io.Closer); ok {
		return struct{ io.Reader }{r}
	}
	return r
}
