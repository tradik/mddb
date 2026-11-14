package lspindex

import (
	"errors"
	"fmt"
	"path/filepath"
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
// PHPantom 0.11.1 `phpantom_lsp --help`, stdio by default). servers_test.go
// pins them.

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

// ErrNoServer is returned when neither the environment nor PATH provides a
// server for the language.
var ErrNoServer = errors.New("language server not found")

// resolve picks the command to run: the operator's override first, then the
// first candidate on PATH.
func (s ServerSpec) resolve(getenv func(string) string, lookPath func(string) (string, error)) (Candidate, error) {
	if s.EnvVar != "" {
		if argv := strings.Fields(getenv(s.EnvVar)); len(argv) > 0 {
			prog, err := lookPath(argv[0])
			if err != nil {
				return Candidate{}, fmt.Errorf("%w: %s=%q: %v", ErrNoServer, s.EnvVar, getenv(s.EnvVar), err)
			}
			// A table program at another path keeps the table's name, so
			// symIndex says pyrefly/1.4.1 however pyrefly was found.
			found := Candidate{Argv: append([]string{prog}, argv[1:]...)}
			for _, c := range s.Candidates {
				if c.Argv[0] == filepath.Base(argv[0]) {
					found.Name, found.VersionArgs = c.Name, c.VersionArgs
				}
			}
			return found, nil
		}
	}
	var tried []string
	for _, c := range s.Candidates {
		prog, err := lookPath(c.Argv[0])
		if err == nil {
			return Candidate{Name: c.Name, Argv: append([]string{prog}, c.Argv[1:]...), VersionArgs: c.VersionArgs}, nil
		}
		tried = append(tried, strings.Join(c.Argv, " "))
	}
	return Candidate{}, fmt.Errorf("%w for %s: none of [%s] is on PATH; install one or set %s to a command line",
		ErrNoServer, s.Language, strings.Join(tried, "; "), s.EnvVar)
}

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

// kindOf maps an LSP kind for this language; "" means not a definition.
func (s ServerSpec) kindOf(lspKind int) string {
	if k, ok := s.Kinds[lspKind]; ok {
		return k
	}
	return defaultKinds[lspKind]
}
