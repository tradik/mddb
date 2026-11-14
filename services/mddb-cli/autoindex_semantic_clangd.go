package main

import (
	"fmt"
	"slices"

	"mddb-cli/internal/lspindex"
)

// C and C++ through clangd (#418): the compilation database clangd needs,
// and one server for both languages.

// compileCommandsHint says how to get a compilation database.
const compileCommandsHint = "generate compile_commands.json (CMake: -DCMAKE_EXPORT_COMPILE_COMMANDS=ON; " +
	"Make: bear -- make; Meson and Ninja write one) or name it with --compile-commands"

// findCompileCommands finds the compilation database for a server that
// needs one: --compile-commands, else the root or <root>/build. Without it
// clangd would guess every file's flags, so it is missing like a server is.
func findCompileCommands(o semanticOptions) (string, error) {
	dir, err := lspindex.FindCompileCommands(o.root, o.compileCommands)
	if err != nil {
		return "", fmt.Errorf("%w; %s", err, compileCommandsHint)
	}
	return dir, nil
}

// joinPlans runs the languages of one server (ServerSpec.Group) as one plan
// when they would start the same command: a C++ project's .h headers count
// as C, and two separate clangd runs would each lose the references that
// cross between them. The joint plan is named c+cpp in the report.
func joinPlans(plans []semanticPlan) []semanticPlan {
	var out []semanticPlan
	for _, p := range plans {
		i := slices.IndexFunc(out, func(q semanticPlan) bool {
			return p.spec.Group != "" && q.spec.Group == p.spec.Group &&
				slices.Equal(q.argv, p.argv) && q.compileCommands == p.compileCommands
		})
		if i < 0 {
			out = append(out, p)
			continue
		}
		out[i].language += "+" + p.language
		out[i].files = append(out[i].files, p.files...)
	}
	return out
}
