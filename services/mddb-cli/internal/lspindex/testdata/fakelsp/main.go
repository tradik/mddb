// Command fakelsp is a scripted language server for the lspindex tests: it
// speaks LSP over stdio and answers from a JSON script named by
// FAKELSP_SCRIPT. The tests build it into a temporary directory; it is never
// committed as a binary, and CI needs no real language server.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type msg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

type fake struct {
	mu       sync.Mutex // the read loop against the progress timer
	s        script
	out      *os.File
	root     string
	record   map[string]any
	answers  []any
	hanging  map[string]bool
	failed   map[string]bool
	asked    bool
	waiting  int
	deferred []*msg
}

func main() {
	var s script
	path := os.Getenv("FAKELSP_SCRIPT")
	if len(os.Args) > 1 && strings.HasSuffix(os.Args[1], ".json") {
		path = os.Args[1]
	}
	data, err := os.ReadFile(path) // #nosec G304 G703 -- test fixture: the test that starts the fake names its script
	if err == nil {
		err = json.Unmarshal(data, &s)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakelsp: script:", err)
		os.Exit(2)
	}
	fmt.Fprint(os.Stderr, s.Stderr)
	f := &fake{s: s, out: os.Stdout, hanging: map[string]bool{}, failed: map[string]bool{},
		record: map[string]any{"argv": os.Args[1:], "pid": os.Getpid()}}
	in := bufio.NewReader(os.Stdin)
	for {
		m, err := read(in)
		if err != nil {
			f.mu.Lock()
			f.save()
			f.mu.Unlock()
			if s.IgnoreExit {
				time.Sleep(time.Hour)
			}
			os.Exit(0)
		}
		f.mu.Lock()
		f.handle(m)
		f.mu.Unlock()
	}
}

func read(r *bufio.Reader) (*msg, error) {
	n := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
			n, _ = strconv.Atoi(v)
		}
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var m msg
	return &m, json.Unmarshal(body, &m)
}

func (f *fake) write(v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(f.out, "Content-Length: %d\r\n\r\n%s", len(b), b)
}

func (f *fake) progress(kind string) {
	f.write(map[string]any{"jsonrpc": "2.0", "method": "$/progress",
		"params": map[string]any{"token": "load", "value": map[string]any{"kind": kind, "title": "Loading"}}})
}

func (f *fake) reply(id json.RawMessage, result any) {
	f.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (f *fake) replyError(id json.RawMessage, code int, text string) {
	f.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": text}})
}

func (f *fake) save() {
	if f.s.Record == "" {
		return
	}
	f.record["answers"] = f.answers
	b, _ := json.Marshal(f.record)
	_ = os.WriteFile(f.s.Record, b, 0o600)
}

func (f *fake) handle(m *msg) {
	switch {
	case m.Method == "" && len(m.ID) > 0: // an answer to a server request
		f.answers = append(f.answers, m)
		f.waiting--
		if f.waiting == 0 {
			for _, d := range f.deferred {
				f.handle(d)
			}
			f.deferred = nil
		}
	case m.Method == "initialize":
		var p struct {
			RootURI string `json:"rootUri"`
		}
		_ = json.Unmarshal(m.Params, &p)
		u, _ := url.Parse(p.RootURI)
		f.root = u.Path
		f.record["initialize"] = m.Params
		f.save()
		if f.s.Garbage == "initialize" {
			fmt.Fprint(f.out, "Content-Lenth 12\r\n\r\n{}")
			return
		}
		f.reply(m.ID, map[string]any{"capabilities": map[string]any{}, "serverInfo": f.s.ServerInfo})
		if f.s.Die == "initialize" {
			os.Exit(3) // after answering: the client's next writes fail
		}
	case m.Method == "initialized":
		for _, p := range f.s.Messages {
			f.write(map[string]any{"jsonrpc": "2.0", "method": "window/logMessage", "params": p})
		}
		// A progress report without begin is noise the client must ignore.
		f.write(map[string]any{"jsonrpc": "2.0", "method": "$/progress", "params": map[string]any{"token": 1}})
		if f.s.ProgressOpen || f.s.ProgressEndMs > 0 {
			f.progress("begin")
		}
		if f.s.ProgressEndMs > 0 {
			time.AfterFunc(time.Duration(f.s.ProgressEndMs)*time.Millisecond, func() {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.progress("end")
				f.record["asked"] = append(asSlice(f.record["asked"]), "progress end")
			})
		}
	case m.Method == "shutdown":
		f.save()
		f.reply(m.ID, nil)
	case m.Method == "exit":
		f.save()
		if f.s.IgnoreExit {
			time.Sleep(time.Hour)
		}
		os.Exit(0)
	case m.Method == "$/cancelRequest":
		var p struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(m.Params, &p)
		f.record["cancelled"] = append(asSlice(f.record["cancelled"]), string(p.ID))
		if f.hanging[string(p.ID)] {
			f.replyError(p.ID, -32800, "cancelled")
		}
	case strings.HasPrefix(m.Method, "textDocument/") && len(m.ID) > 0:
		f.request(m)
	}
}
