package main

import (
	"context"
	"fmt"

	"mddb-cli/internal/lspindex"
)

// The language servers `autoindex --semantic` would start (CODE-011), as
// `mddb-cli diagnose` lists them: which program, which version. Only the
// program's name is shown, never its directory, which would name the user
// (the bundle's no-user-data rule, OPS-025).

// languageServer is one language's server on this machine.
type languageServer struct {
	Language string `json:"language"`
	Found    bool   `json:"found"`
	Program  string `json:"program,omitempty"`
	Version  string `json:"version,omitempty"`
}

// detectLanguageServers finds each language's server the way autoindex
// does — MDDB_LSP_* first, then PATH — and asks it for its version.
func detectLanguageServers(ctx context.Context) []languageServer {
	var out []languageServer
	for _, lang := range lspindex.Languages() {
		spec, _ := lspindex.Spec(lang) // a language Languages lists has a spec
		ls := languageServer{Language: lang}
		if c, err := lspindex.Find(spec, lspindex.Options{}); err == nil {
			ls.Found, ls.Program, ls.Version = true, c.Program(), lspindex.Version(ctx, c)
		}
		out = append(out, ls)
	}
	return out
}

// printLanguageServers is the text form of the list.
func printLanguageServers(list []languageServer) {
	fmt.Println("\nLanguage servers (for autoindex --semantic):")
	for _, ls := range list {
		switch {
		case !ls.Found:
			fmt.Printf("  %-7s not found\n", ls.Language)
		case ls.Version == "":
			fmt.Printf("  %-7s %s (version unknown)\n", ls.Language, ls.Program)
		default:
			fmt.Printf("  %-7s %s: %s\n", ls.Language, ls.Program, ls.Version)
		}
	}
}
