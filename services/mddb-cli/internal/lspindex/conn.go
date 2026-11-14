package lspindex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// conn is a JSON-RPC client over one server's stdin and stdout. Requests may
// be in flight concurrently; the read loop routes each response to its
// caller by id and answers the server's own requests.
type conn struct {
	w   io.Writer
	wmu sync.Mutex // one frame at a time on w

	nextID   atomic.Int64
	requests atomic.Int64

	mu           sync.Mutex
	pending      map[int64]chan *message
	errMsgs      []string        // window/showMessage and window/logMessage errors
	progress     map[string]bool // work-done progress begun and not ended, by token
	lastProgress time.Time       // the last progress event, or the start

	folders []workspaceFolder // answer to workspace/workspaceFolders

	done chan struct{} // closed when the read loop ends
	err  error         // why it ended; written before done is closed
}

// errServerGone is wrapped by every error caused by the stream ending.
var errServerGone = errors.New("language server stopped answering")

func newConn(w io.Writer, r io.Reader, folders []workspaceFolder) *conn {
	c := &conn{w: w, pending: map[int64]chan *message{}, folders: folders, done: make(chan struct{}),
		progress: map[string]bool{}, lastProgress: time.Now()}
	go c.readLoop(bufio.NewReaderSize(r, 64<<10))
	return c
}

// call sends a request and waits for its response, the end of the stream or
// ctx. When ctx ends first the server is told with $/cancelRequest, so it
// stops working on an answer nobody will read.
func (c *conn) call(ctx context.Context, method string, params, result any) error {
	id := c.nextID.Add(1)
	ch := make(chan *message, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	c.requests.Add(1)
	if err := c.send(&message{JSONRPC: "2.0", ID: rawID(id), Method: method, Params: mustRaw(params)}); err != nil {
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if result == nil || len(m.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(m.Result, result); err != nil {
			return fmt.Errorf("%w: %s result: %v", errProtocol, method, err)
		}
		return nil
	case <-c.done:
		return c.err
	case <-ctx.Done():
		// Best effort: if the write fails the stream is gone anyway.
		_ = c.notify("$/cancelRequest", map[string]int64{"id": id})
		return ctx.Err()
	}
}

// notify sends a notification.
func (c *conn) notify(method string, params any) error {
	return c.send(&message{JSONRPC: "2.0", Method: method, Params: mustRaw(params)})
}

// send writes one message. A failed write means the server's end of the
// pipe is closed: the server is gone.
func (c *conn) send(m *message) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := writeMessage(c.w, m); err != nil {
		return fmt.Errorf("%w: sending %s: %v", errServerGone, m.Method, err)
	}
	return nil
}

// readLoop runs until the stream ends or breaks the protocol.
func (c *conn) readLoop(r *bufio.Reader) {
	for {
		m, err := readMessage(r)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				err = fmt.Errorf("%w: output closed", errServerGone)
			} else {
				err = fmt.Errorf("%w: %w", errServerGone, err)
			}
			c.err = err
			close(c.done)
			return
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			c.answer(m)
		case m.Method != "":
			c.observe(m)
		default:
			c.deliver(m)
		}
	}
}

// deliver hands a response to the waiting caller. A response to a cancelled
// request finds nobody waiting and is dropped.
func (c *conn) deliver(m *message) {
	id, err := strconv.ParseInt(string(m.ID), 10, 64)
	if err != nil {
		return
	}
	c.mu.Lock()
	ch := c.pending[id]
	c.mu.Unlock()
	if ch != nil {
		ch <- m
	}
}

func rawID(id int64) json.RawMessage {
	return json.RawMessage(strconv.FormatInt(id, 10))
}

// mustRaw marshals values built by this package: maps, slices and structs
// of plain fields, which cannot fail to marshal.
func mustRaw(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("lspindex: marshal %T: %v", v, err))
	}
	return b
}
