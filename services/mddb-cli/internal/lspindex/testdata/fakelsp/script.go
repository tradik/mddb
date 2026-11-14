package main

import "encoding/json"

// script drives the fake. Request keys are "<method> <path>" for
// documentSymbol and "<method> <path>:<line>" (0-based) for references and
// implementation, method being the part after "textDocument/".
type script struct {
	ServerInfo     map[string]string          `json:"serverInfo"`
	Record         string                     `json:"record"`         // file to write what the fake saw
	Stderr         string                     `json:"stderr"`         // written to stderr at start
	Messages       []json.RawMessage          `json:"messages"`       // window/logMessage params sent after initialized
	ServerRequests []string                   `json:"serverRequests"` // sent before the first documentSymbol answer
	Symbols        map[string]json.RawMessage `json:"symbols"`        // path -> documentSymbol result
	References     map[string][]loc           `json:"references"`     // path:line -> locations
	Implementation map[string][]loc           `json:"implementation"` // path:line -> locations
	Fail           []string                   `json:"fail"`           // request keys answered with an error
	FailOnce       []string                   `json:"failOnce"`       // request keys answered with an error the first time
	Hang           []string                   `json:"hang"`           // request keys never answered until cancelled
	Die            string                     `json:"die"`            // request key on which the fake exits
	Garbage        string                     `json:"garbage"`        // method answered with a broken header
	BadResult      []string                   `json:"badResult"`      // request keys answered with a wrong-shaped result
	IgnoreExit     bool                       `json:"ignoreExit"`     // stay alive after exit; the client must kill
	ProgressEndMs  int                        `json:"progressEndMs"`  // >0: "loading" progress from initialized, ended after this
	ProgressOpen   bool                       `json:"progressOpen"`   // a progress that never ends
	Raw            map[string]json.RawMessage `json:"raw"`            // request key -> literal result
}

// loc is a location: a path relative to the root, an absolute path or a URI.
type loc struct {
	Path string `json:"path"`
	URI  string `json:"uri"`
	Line int    `json:"line"`
	Char int    `json:"char"`
	End  int    `json:"end"` // end line, defaults to line
}
