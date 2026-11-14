package lspindex

import (
	"fmt"
	"slices"
	"sort"
)

// canaryMinFiles is the tree size from which some reference must cross a
// file boundary. Below it a project may honestly be a few unrelated scripts.
const canaryMinFiles = 20

// canary detects a server that runs without having loaded the workspace.
// There is no standard "loaded" signal (only per-server $/progress), and the
// symptoms vary with the server's version. Two were measured with gopls and
// a go older than go.work asks for:
//
//   - every request succeeds, but the server sees each open file alone, so
//     no reference crosses a file boundary (168 locations instead of 2591);
//   - documentSymbol succeeds while most references requests answer with an
//     error ("no package metadata", 12129 of 12762), the rest look plausible.
//
// Either way the facts would be confidently wrong, so the run fails.
func (r *run) canary() error {
	withDefs := 0
	for _, syms := range r.defs {
		if slices.ContainsFunc(syms, isDefinition) {
			withDefs++
		}
	}
	if withDefs < canaryMinFiles {
		return nil
	}
	if r.refsLost*2 > r.refsAsked {
		return fmt.Errorf("%w: %d of %d references requests failed; "+workspaceHint,
			ErrWorkspaceNotLoaded, r.refsLost, r.refsAsked)
	}
	if r.stats.CrossFileRefs == 0 {
		return fmt.Errorf("%w: no reference crosses a file boundary in %d files with definitions; "+workspaceHint,
			ErrWorkspaceNotLoaded, withDefs)
	}
	return nil
}

// result assembles the sorted facts. Every file that answered documentSymbol
// is present, even with no facts: "indexed, nothing to say" differs from
// "not indexed" for the caller.
func (r *run) result() *Result {
	res := &Result{Server: r.server, Files: make(map[string]*FileFacts, len(r.defs)), Stats: r.stats}
	for path, syms := range r.defs {
		ff := &FileFacts{Defs: definitions(syms)}
		for target, set := range r.refs[path] {
			ref := Ref{Target: target, Lines: sortedLines(set)}
			if !refEncodable(ref) {
				res.Stats.Unencodable++
				continue
			}
			ff.Refs = append(ff.Refs, ref)
			res.Stats.Refs += len(ref.Lines)
		}
		sort.Slice(ff.Refs, func(i, j int) bool { return ff.Refs[i].Target < ff.Refs[j].Target })
		for impl := range r.impls[path] {
			if !implEncodable(impl) {
				res.Stats.Unencodable++
				continue
			}
			ff.Impls = append(ff.Impls, impl)
		}
		sort.Slice(ff.Impls, func(i, j int) bool {
			a, b := ff.Impls[i], ff.Impls[j]
			return a.Target < b.Target || (a.Target == b.Target && a.Local < b.Local)
		})
		res.Stats.Defs += len(ff.Defs)
		res.Stats.Impls += len(ff.Impls)
		res.Files[path] = ff
	}
	res.Stats.Files = len(res.Files)
	return res
}

// definitions are the reported definitions among syms: owners are not.
func definitions(syms []symbol) []Def {
	var out []Def
	for _, s := range syms {
		if !s.owner {
			out = append(out, s.def)
		}
	}
	return out
}

func sortedLines(set map[int]bool) []int {
	lines := make([]int, 0, len(set))
	for n := range set {
		lines = append(lines, n)
	}
	sort.Ints(lines)
	return lines
}
