package lspindex

import (
	"path"
	"slices"
	"strings"
)

// clangd, the C and C++ server (#418). Checked against clangd 23.1.0
// (`clangd --help` and its answers over a C and a C++ tree) on 2026-11-14:
//
//   - it reads the compiler flags of every file from a compilation database
//     (compile_commands.json). Without one it compiles a file with guessed
//     flags ("clangd fallback") and answers from that, saying so only on
//     stderr at --log=info — hence the log level and Fallback;
//   - references come from the background index (--background-index, on by
//     default), announced as work-done progress "backgroundIndexProgress",
//     which on a large tree outlasts maxLoadWait — hence WaitIndexed;
//   - documentSymbol lists a function's declarations (header prototypes,
//     in-class member declarations) beside its definition; tags tell them
//     apart (clangdClassify);
//   - implementation answers for C++ classes and virtual methods only, so
//     C, which has neither, is asked nothing.
//
// -j is left to clangd: Options.Concurrency counts requests in flight, while
// clangd's workers also build the background index, which wants every core.

// clangdSpec is the table entry for C or C++. Both share one server (Group):
// a C++ project's .h headers count as C, and its references cross between
// the two languages.
func clangdSpec(language, envVar string) ServerSpec {
	return ServerSpec{
		Language: language, LanguageID: language, EnvVar: envVar,
		Candidates:      []Candidate{{Name: "clangd", Argv: []string{"clangd", "--background-index", "--log=info"}}},
		ImplKinds:       []int{symbolClass, symbolMethod},
		Normalize:       clangdNormalize,
		Group:           "clangd",
		LanguageIDOf:    clangdLanguageID,
		Classify:        clangdClassify,
		NamespaceNodes:  true,
		Args:            clangdArgs,
		CompileCommands: true,
		Fallback:        clangdFallback,
		FallbackHint: "clangd found no compilation database for them and guessed their flags, so the facts would be partial. " +
			"Generate compile_commands.json (CMake: -DCMAKE_EXPORT_COMPILE_COMMANDS=ON; Make: bear -- make; " +
			"Meson and Ninja write one) and pass its directory with --compile-commands, then run again",
		WaitIndexed: true,
	}
}

// clangdArgs points clangd at the compilation database autoindex found.
func clangdArgs(opt Options) []string {
	if opt.CompileCommandsDir == "" {
		return nil
	}
	return []string{"--compile-commands-dir=" + opt.CompileCommandsDir}
}

// cppExtensions are the C++ files of mddbd's code languages (.h is C there).
var cppExtensions = map[string]bool{".cc": true, ".cpp": true, ".cxx": true, ".hpp": true, ".hh": true, ".hxx": true}

// clangdLanguageID is the languageId of one file. clangd takes the language
// from the compile command; the id only has to be honest.
func clangdLanguageID(file string) string {
	if cppExtensions[strings.ToLower(path.Ext(file))] {
		return "cpp"
	}
	return "c"
}

// clangdNormalize turns C++ names into the format's dotted ones:
// `Square::area` (an out-of-line definition) becomes Square.area, template
// arguments go (`List<T>::push` is List.push), and an anonymous namespace,
// struct or union, which clangd names "(anonymous namespace)" or
// "(unnamed struct at f.c:3:1)", gives "": no name of its own, its members
// are named as if it were not there. An operator keeps its symbols.
func clangdNormalize(name string) string {
	if strings.HasPrefix(name, "(") {
		return ""
	}
	head, op := name, ""
	if i := strings.Index(name, "operator"); i == 0 || (i > 0 && strings.HasSuffix(name[:i], "::")) {
		head, op = name[:i], name[i:]
	}
	return strings.ReplaceAll(stripTemplateArgs(head), "::", ".") + op
}

// stripTemplateArgs drops every <...>, nested ones included.
func stripTemplateArgs(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SymbolTag values clangd 23 sends (the LSP 3.18 proposal's).
const (
	tagAbstract    = 9
	tagDeclaration = 18
	tagDefinition  = 19
)

// clangdClassify drops a function, method or constructor that is only
// declared — a header prototype, a member declared in its class and
// defined in a .cpp file — so a name has one definition, where its body
// is. A pure virtual method has no body anywhere and stays: it is what
// overrides implement. An alias (`typedef`, `using`) is a type, whatever
// kind clangd gives it. A clangd without tags keeps every declaration.
func clangdClassify(n *documentSymbol, kind string) string {
	switch {
	case callableKinds[kind] && slices.Contains(n.Tags, tagDeclaration) &&
		!slices.Contains(n.Tags, tagDefinition) && !slices.Contains(n.Tags, tagAbstract):
		return ""
	case n.Detail == "type alias":
		return "type"
	}
	return kind
}

// clangd's stderr lines (--log=info) about a file compiled with guessed flags.
const (
	clangdNoDatabase  = "Failed to find compilation database for "
	clangdBuilding    = "ASTWorker building file "
	clangdFallbackCmd = " with command clangd fallback"
)

// clangdFallback returns the file a stderr line says clangd compiled
// without a compilation database, or "".
func clangdFallback(line string) string {
	line = strings.TrimRight(line, "\r\n ")
	if _, file, ok := strings.Cut(line, clangdNoDatabase); ok {
		return file
	}
	if _, rest, ok := strings.Cut(line, clangdBuilding); ok && strings.HasSuffix(rest, clangdFallbackCmd) {
		if i := strings.LastIndex(rest, " version "); i > 0 {
			return rest[:i]
		}
	}
	return ""
}
