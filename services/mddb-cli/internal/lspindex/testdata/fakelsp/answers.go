package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func (f *fake) request(m *msg) {
	method := strings.TrimPrefix(m.Method, "textDocument/")
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position struct {
			Line int `json:"line"`
		} `json:"position"`
	}
	_ = json.Unmarshal(m.Params, &p)
	rel, _ := filepath.Rel(f.root, uriToPath(p.TextDocument.URI))
	rel = filepath.ToSlash(rel)
	key := method + " " + rel
	if method != "documentSymbol" {
		key += ":" + strconv.Itoa(p.Position.Line)
	}
	f.record["asked"] = append(asSlice(f.record["asked"]), key)
	if method == "documentSymbol" && !f.asked && len(f.s.ServerRequests) > 0 {
		f.asked = true
		f.deferred = append(f.deferred, m)
		for i, r := range f.s.ServerRequests {
			f.waiting++
			params := any(map[string]any{"token": i})
			if r == "workspace/configuration" {
				params = map[string]any{"items": []any{map[string]any{"section": "a"}, map[string]any{"section": "b"}}}
			}
			f.write(map[string]any{"jsonrpc": "2.0", "id": "s" + strconv.Itoa(i), "method": r, "params": params})
		}
		return
	}
	switch {
	case contains(f.s.FailOnce, key) && !f.failed[key]:
		f.failed[key] = true
		f.replyError(m.ID, -32603, "scripted first failure")
		return
	case key == f.s.Die:
		os.Exit(3)
	case contains(f.s.Hang, key):
		f.hanging[string(m.ID)] = true
		return
	case contains(f.s.Fail, key):
		f.replyError(m.ID, -32603, "scripted failure")
		return
	case contains(f.s.BadResult, key):
		f.reply(m.ID, "not a list")
		return
	}
	if raw, ok := f.s.Raw[key]; ok {
		f.reply(m.ID, raw)
		return
	}
	switch method {
	case "documentSymbol":
		f.reply(m.ID, f.s.Symbols[rel])
	case "references":
		f.reply(m.ID, f.locations(f.s.References[strings.TrimPrefix(key, "references ")]))
	case "implementation":
		f.reply(m.ID, f.locations(f.s.Implementation[strings.TrimPrefix(key, "implementation ")]))
	default:
		f.replyError(m.ID, -32601, "unknown method")
	}
}

func (f *fake) locations(locs []loc) []any {
	out := []any{}
	for _, l := range locs {
		uri := l.URI
		if uri == "" {
			path := l.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(f.root, path)
			}
			uri = pathToURI(path)
		}
		end := max(l.End, l.Line)
		out = append(out, map[string]any{"uri": uri, "range": map[string]any{
			"start": map[string]int{"line": l.Line, "character": l.Char},
			"end":   map[string]int{"line": end, "character": l.Char + 1}}})
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// uriToPath and pathToURI convert as a real server does, so on Windows the
// fake reports file:///C:/... like gopls rather than a path with backslashes.
func uriToPath(uri string) string {
	u, _ := url.Parse(uri)
	p := u.Path
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

func pathToURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
