package lspindex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadMessageRejectsBrokenFrames(t *testing.T) {
	cases := map[string]string{
		"no colon":       "garbage\r\n\r\n{}",
		"bad length":     "Content-Length: x\r\n\r\n{}",
		"negative":       "Content-Length: -1\r\n\r\n",
		"too long":       "Content-Length: 999999999999\r\n\r\n",
		"no length":      "Content-Type: application/json\r\n\r\n{}",
		"not json":       "Content-Length: 3\r\n\r\nabc",
		"endless header": strings.Repeat("X-Pad: 1\r\n", maxHeaderLines+1),
	}
	for name, in := range cases {
		_, err := readMessage(bufio.NewReader(strings.NewReader(in)))
		if !errors.Is(err, errProtocol) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	for name, in := range map[string]string{"eof in header": "Content-Length: 2\r\n", "short body": "Content-Length: 9\r\n\r\n{}"} {
		_, err := readMessage(bufio.NewReader(strings.NewReader(in)))
		if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestReadMessageAcceptsHeaderVariants(t *testing.T) {
	// Lower-case name, padding, a bare \n and a Content-Type are all legal.
	in := "content-length:  14 \nContent-Type: application/vscode-jsonrpc; charset=utf-8\r\n\r\n{\"id\":1,\"a\":1}"
	m, err := readMessage(bufio.NewReader(strings.NewReader(in)))
	if err != nil || string(m.ID) != "1" {
		t.Fatalf("m = %+v, err = %v", m, err)
	}
}

func TestWriteMessageRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeMessage(&buf, &message{JSONRPC: "2.0", ID: rawID(7), Method: "x", Params: mustRaw(map[string]int{"a": 1})}); err != nil {
		t.Fatal(err)
	}
	m, err := readMessage(bufio.NewReader(&buf))
	if err != nil || m.Method != "x" || string(m.ID) != "7" || string(m.Params) != `{"a":1}` {
		t.Fatalf("m = %+v, err = %v", m, err)
	}
	if err := writeMessage(&buf, &message{Result: json.RawMessage("{")}); err == nil {
		t.Error("invalid raw JSON written")
	}
}

func TestClip(t *testing.T) {
	if got := clip(strings.Repeat("a", 100)); len(got) != 83 || !strings.HasSuffix(got, "...") {
		t.Errorf("clip = %q", got)
	}
	if got := clipMessage(strings.Repeat("a", 400)); len(got) != 303 {
		t.Errorf("clipMessage = %d bytes", len(got))
	}
}

func TestMustRawPanicsOnUnmarshalable(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	mustRaw(make(chan int))
}

// pipeConn is a conn talking to a test acting as the server.
func pipeConn(t *testing.T) (*conn, *bufio.Reader, io.Writer, func()) {
	t.Helper()
	toServer, clientOut := io.Pipe()
	clientIn, fromServer := io.Pipe()
	c := newConn(clientOut, clientIn, nil)
	return c, bufio.NewReader(toServer), fromServer, func() {
		_ = fromServer.Close()
		<-c.done
		_ = toServer.Close()
	}
}

func TestConnIgnoresStrayResponses(t *testing.T) {
	c, in, out, stop := pipeConn(t)
	defer stop()
	go func() {
		m, _ := readMessage(in)
		// A response with a string id, one for an unknown request, then the real one.
		_ = writeMessage(out, &message{JSONRPC: "2.0", ID: json.RawMessage(`"x"`), Result: json.RawMessage("1")})
		_ = writeMessage(out, &message{JSONRPC: "2.0", ID: rawID(99), Result: json.RawMessage("2")})
		_ = writeMessage(out, &message{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage("3")})
	}()
	var got int
	if err := c.call(context.Background(), "m", nil, &got); err != nil || got != 3 {
		t.Fatalf("got %d, err %v", got, err)
	}
}

func TestConnBadResultAndErrors(t *testing.T) {
	c, in, out, stop := pipeConn(t)
	defer stop()
	go func() {
		m, _ := readMessage(in)
		_ = writeMessage(out, &message{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage(`"text"`)})
		m, _ = readMessage(in)
		_ = writeMessage(out, &message{JSONRPC: "2.0", ID: m.ID, Error: &rpcError{Code: codeRequestCancelled, Message: "gone"}})
		_, _ = readMessage(in) // the notification below
	}()
	var got int
	if err := c.call(context.Background(), "m", nil, &got); !errors.Is(err, errProtocol) {
		t.Errorf("bad result: %v", err)
	}
	var re *rpcError
	if err := c.call(context.Background(), "m", nil, nil); !errors.As(err, &re) || re.Code != codeRequestCancelled ||
		err.Error() != "language server error -32800: gone" {
		t.Errorf("error answer: %v", err)
	}
	c.observe(&message{Method: "window/logMessage", Params: json.RawMessage(`{"type":"x"}`)})
	c.observe(&message{Method: "textDocument/publishDiagnostics", Params: json.RawMessage(`{}`)})
	for range maxErrMsgs + 1 {
		c.observe(&message{Method: "window/showMessage", Params: json.RawMessage(`{"type":1,"message":"m"}`)})
	}
	if len(c.serverErrors()) != maxErrMsgs {
		t.Errorf("kept %d messages", len(c.serverErrors()))
	}
	_ = c.notify("n", nil)
}

func TestConnWriteFailure(t *testing.T) {
	r, w := io.Pipe()
	_ = r.Close()
	c := newConn(w, strings.NewReader(""), nil)
	<-c.done
	if err := c.call(context.Background(), "m", nil, nil); !errors.Is(err, errServerGone) {
		t.Errorf("err = %v", err)
	}
	// didOpen failing ends the run: the server is gone.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := newRun(ServerSpec{LanguageID: "go"}, Options{}.withDefaults(), root, c)
	if _, err := run.openAll([]string{"x.go"}); err == nil {
		t.Error("didOpen on a closed stream succeeded")
	}
}
