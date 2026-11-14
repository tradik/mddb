package lspindex

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The server table: the one place that knows about languages. Adding a
// language is one entry here plus a test; index.go does not change.
//
// Launch commands were checked against each server's own help and
// documentation on 2026-11-14 (gopls v0.24.0 `gopls help serve`; pyrefly 1.4.1
// `pyrefly lsp --help`; basedpyright 1.40.2 docs, `--stdio` required;
// phpactor 2026.10.07.0 `phpactor language-server --help`, stdio by default;
// PHPantom 0.11.1 `phpantom_lsp --help`, stdio by default; clangd 23.1.0
// `clangd --help`, see servers_clangd.go; rust-analyzer 1.98.0, stdio with
// no arguments, see servers_rust.go). servers_test.go pins them.

// ServerSpec describes how to index one language.
type ServerSpec struct {
	// Language is the value of meta.language for the files indexed.
	Language string
	// LanguageID is the languageId sent in textDocument/didOpen.
	LanguageID string
	// EnvVar names the variable that replaces Candidates with a command line
	// of the operator's choosing (split on whitespace, no shell).
	EnvVar string
	// Candidates are tried in order; the first whose program is on PATH runs.
	Candidates []Candidate
	// Kinds maps LSP SymbolKind to a fact kind where the language differs
	// from defaultKinds; an empty string drops the kind.
	Kinds map[int]string
	// ImplKinds are the SymbolKinds asked for textDocument/implementation.
	ImplKinds []int
	// Normalize cleans a symbol name from documentSymbol and may return a
	// dotted name (gopls `(*Server).LookupSymbol` becomes Server.LookupSymbol);
	// nil keeps it.
	Normalize func(name string) string
	// Namespace returns, for a file's content, the prefix of a top-level
	// name declared at a 0-based line (PHP `namespace App\Http;` gives
	// `App\Http\`); nil means none.
	Namespace func(src []byte) func(line int) string
	// SkipRefs reports definitions not worth a references request.
	SkipRefs func(path string, d Def) bool
	// Settings returns the initializationOptions; nil sends none.
	Settings func(opt Options) map[string]any

	// The hooks below exist for clangd (servers_clangd.go); nil or zero
	// leaves a language as it was.

	// Group names the server several languages share. autoindex runs the
	// languages of one group as one server, so references between them (a
	// .cpp file using a .h header, which counts as C) are seen.
	Group string
	// LanguageIDOf replaces LanguageID per file.
	LanguageIDOf func(path string) string
	// Classify revises the kind a symbol maps to; "" drops it as a
	// definition (its children are still walked).
	Classify func(n *documentSymbol, kind string) string
	// NamespaceNodes makes a Namespace symbol prefix its children's names
	// (geo.Shape) without being a definition or a container itself.
	NamespaceNodes bool
	// Args returns arguments added to the table's command line (also when
	// MDDB_LSP_* names the table's program elsewhere).
	Args func(opt Options) []string
	// CompileCommands means the server needs a compilation database
	// (compile_commands.json); see FindCompileCommands.
	CompileCommands bool
	// Fallback reads one line of the server's stderr and returns the file
	// it says it compiled without the project's build settings, or "". Any
	// such file among those indexed fails the run (ErrFallbackFlags), with
	// FallbackHint saying what to do.
	Fallback     func(line string) string
	FallbackHint string
	// WaitIndexed waits for the server's progress to end however long it
	// takes, within Options.Timeout, instead of at most maxLoadWait.
	WaitIndexed bool

	// The hooks below exist for rust-analyzer (servers_rust.go).

	// Owner returns, for a node that is no definition, the name of the type
	// its children belong to (a Rust impl block's self type), or "". The
	// node then qualifies its children (Square.area) and stands for the type
	// where an implementation answer points at it; it is not reported.
	Owner func(n *documentSymbol) string
	// ImplParents, when set, asks implementation of a definition of
	// ImplKinds only when it is top-level or its parent is of one of these
	// kinds: a trait's methods, not an impl block's.
	ImplParents []int
	// ServerStatus declares rust-analyzer's experimental/serverStatus
	// notification; the run then waits for the server to report itself
	// quiescent before references are asked, and refuses facts when it
	// reports health "error", with StatusHint saying what to do.
	ServerStatus bool
	StatusHint   string
}

// Candidate is one way to start a server.
type Candidate struct {
	// Name is the server's name in Result.Server; empty means the name the
	// server reports about itself.
	Name string
	// Argv is the program and its arguments.
	Argv []string
	// VersionArgs make the program print its version (see Version); nil
	// means --version.
	VersionArgs []string
}

// servers is the table, keyed by language.
var servers = map[string]ServerSpec{
	"go": {
		Language: "go", LanguageID: "go", EnvVar: "MDDB_LSP_GO",
		// gopls has no --version flag; its version is a subcommand.
		Candidates: []Candidate{{Name: "gopls", Argv: []string{"gopls", "serve"}, VersionArgs: []string{"version"}}},
		// gopls reports every named type that is not a struct or interface
		// (type Num int, aliases) as Class.
		Kinds:     map[int]string{symbolClass: "type"},
		ImplKinds: []int{symbolInterface},
		Normalize: goNormalize,
		SkipRefs:  goSkipRefs,
		Settings:  goSettings,
	},
	"python": {
		Language: "python", LanguageID: "python", EnvVar: "MDDB_LSP_PYTHON",
		Candidates: []Candidate{
			// basedpyright first: measured for CODE-009, pyrefly 1.4.1 answers
			// textDocument/implementation for no class, while basedpyright
			// finds ABC subclasses and Protocol implementations, and
			// implementations are what the facts add over the heuristics.
			{Name: "basedpyright", Argv: []string{"basedpyright-langserver", "--stdio"}},
			// lazy-blocking: references wait for the workspace index instead
			// of answering from the files indexed so far, which is what a
			// batch run needs (pyrefly's help names it the deterministic mode).
			{Name: "pyrefly", Argv: []string{"pyrefly", "lsp", "--indexing-mode", "lazy-blocking"}},
		},
		ImplKinds: []int{symbolInterface, symbolClass},
	},
	"php": {
		Language: "php", LanguageID: "php", EnvVar: "MDDB_LSP_PHP",
		Candidates: []Candidate{
			{Name: "phpactor", Argv: []string{"phpactor", "language-server"}},
			{Name: "phpantom", Argv: []string{"phpantom_lsp"}},
		},
		ImplKinds: []int{symbolInterface, symbolClass},
		Namespace: phpNamespace,
	},
	"c":    clangdSpec("c", "MDDB_LSP_C"),
	"cpp":  clangdSpec("cpp", "MDDB_LSP_CPP"),
	"rust": rustSpec(),
}

// Spec returns the table entry for a language.
func Spec(language string) (ServerSpec, error) {
	s, ok := servers[language]
	if !ok {
		return ServerSpec{}, fmt.Errorf("%w: %q (supported: %s)", ErrUnsupportedLanguage, language, strings.Join(Languages(), ", "))
	}
	return s, nil
}

// Languages lists the languages in the table, sorted.
func Languages() []string {
	out := make([]string, 0, len(servers))
	for l := range servers {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// ErrUnsupportedLanguage is returned by Spec for a language not in the table.
var ErrUnsupportedLanguage = errors.New("no language server for language")

// defaultKinds maps LSP SymbolKind to the format's kinds. Everything else —
// variables, constants, fields, properties, parameters, modules — is not a
// definition the facts describe.
var defaultKinds = map[int]string{
	symbolClass:       "class",
	symbolMethod:      "method",
	symbolConstructor: "constructor",
	symbolEnum:        "enum",
	symbolInterface:   "interface",
	symbolFunction:    "function",
	symbolStruct:      "struct",
}

// languageID is the languageId of one file.
func (s ServerSpec) languageID(path string) string {
	if s.LanguageIDOf != nil {
		return s.LanguageIDOf(path)
	}
	return s.LanguageID
}

// kindOf maps an LSP kind for this language; "" means not a definition.
func (s ServerSpec) kindOf(lspKind int) string {
	if k, ok := s.Kinds[lspKind]; ok {
		return k
	}
	return defaultKinds[lspKind]
}
