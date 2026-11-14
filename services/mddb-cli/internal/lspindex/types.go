// Package lspindex drives a language server headlessly over stdio and turns
// its answers into semantic code facts (CODE-009): where each symbol is
// defined, which files reference it and which types implement an interface.
//
// The package is a library. Index takes a checkout, the files to index and one
// language's ServerSpec, and returns facts per file; Encode turns one file's
// facts into the sym* meta keys described in testdata/semantic-facts/README.md.
// Choosing files, flags and sending documents belong to the caller
// (mddb-cli autoindex --semantic, CODE-011).
//
// The server runs on the operator's machine with the operator's toolchain;
// nothing here is reachable from the network, and the command line comes from
// the built-in table or the operator's environment, never from data.
package lspindex

import "time"

// Result is what Index learned about the files it was given.
type Result struct {
	// Server is the language server and its version as `name/version`
	// (e.g. gopls/0.24.0): the `source` field of symIndex.
	Server string
	// Files maps each indexed file (slash-separated, relative to the root) to
	// its facts. A file whose documentSymbol request failed is absent, so the
	// caller sends no facts for it and the server keeps its heuristics.
	Files map[string]*FileFacts
	// Stats describes the run.
	Stats Stats
}

// FileFacts are the facts about one file, sorted.
type FileFacts struct {
	Defs  []Def  `json:"defs,omitempty"`
	Refs  []Ref  `json:"refs,omitempty"`
	Impls []Impl `json:"impls,omitempty"`
}

// Def is one definition in the file.
type Def struct {
	// Name is the qualified name: Server.LookupSymbol, Outer.Inner.method,
	// App\Http\Kernel.handle.
	Name string `json:"name"`
	// Kind is one of function, method, class, interface, struct, type, enum,
	// constructor.
	Kind string `json:"kind"`
	// Start and End are the definition's span, lines counted from 1.
	Start int `json:"start"`
	End   int `json:"end"`
	// Container is the qualified name of the enclosing definition, if any.
	Container string `json:"container,omitempty"`
	// Detail is the server's one-line signature. It is not encoded (the span
	// already shows it in the content); callers may print it.
	Detail string `json:"detail,omitempty"`
}

// Ref is every line of this file that references one symbol.
type Ref struct {
	// Target is the defining file's path, '#', and the qualified name.
	Target string `json:"target"`
	// Lines are ascending and unique, counted from 1.
	Lines []int `json:"lines"`
}

// Impl records that a definition in this file implements an interface.
type Impl struct {
	// Target is the interface's file path, '#', and its qualified name.
	Target string `json:"target"`
	// Local is the qualified name of the implementing definition here.
	Local string `json:"local"`
}

// Stats describes one Index run.
type Stats struct {
	Files int // files with facts in Result.Files
	Defs  int // definitions kept
	Refs  int // reference lines kept
	Impls int // implementations kept
	// CrossFileRefs counts reference locations in another file than the
	// definition: the canary's measure of a loaded workspace.
	CrossFileRefs int

	Requests int // requests sent to the server, initialize and shutdown included
	// Skipped counts symbols or files whose request failed, returned an error
	// or timed out; the run continues without them.
	Skipped int
	// TimedOut counts requests cancelled after Options.RequestTimeout (they
	// are also in Skipped).
	TimedOut int
	// Unencodable counts facts left out because a name or path breaks the
	// format's character or length rules.
	Unencodable int
	// Dropped counts locations outside the root or in files not being indexed.
	Dropped int

	Elapsed time.Duration
	// LoadWait is how long the run waited for the server's work-done
	// progress (loading, indexing) to end before asking references.
	LoadWait time.Duration
	// PeakRssMb is the server's peak resident memory (VmHWM) in MiB, read
	// from /proc before shutdown; 0 where /proc is not available.
	PeakRssMb int
}

// Options tune one Index run. The zero value is usable.
type Options struct {
	// Concurrency is the number of requests in flight (default 4).
	Concurrency int
	// RequestTimeout bounds one request (default 30 s); a request past it is
	// cancelled with $/cancelRequest and its symbol counted in Stats.Skipped.
	RequestTimeout time.Duration
	// Timeout bounds the whole run, server start-up included (default 1 h).
	Timeout time.Duration
	// MemoryLimitMb > 0 is passed to servers that accept a memory limit.
	MemoryLimitMb int
	// Getenv and LookPath replace os.Getenv and exec.LookPath when choosing
	// the server command; nil means the real ones.
	Getenv   func(string) string
	LookPath func(string) (string, error)
}

const (
	defaultConcurrency    = 4
	defaultRequestTimeout = 30 * time.Second
	defaultTimeout        = time.Hour
)

// withDefaults fills the zero fields.
func (o Options) withDefaults() Options {
	if o.Concurrency <= 0 {
		o.Concurrency = defaultConcurrency
	}
	if o.RequestTimeout <= 0 {
		o.RequestTimeout = defaultRequestTimeout
	}
	if o.Timeout <= 0 {
		o.Timeout = defaultTimeout
	}
	return o
}
