package main

import (
	"encoding/json"
	"errors"
	"math/rand"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

// The ground truth is what a language server says, written by the lspindex
// driver's local test (go test -tags lspreal -run TestRealDir with
// LSPREAL_OUT) straight from the server, never through MDDB: otherwise the
// benchmark would check MDDB against itself.

// truthFile is that test's output: the driver's Result.
type truthFile struct {
	Server string                 `json:"server"`
	Files  map[string]*truthFacts `json:"files"`
}

type truthFacts struct {
	Defs []struct {
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Start int    `json:"start"`
	} `json:"defs"`
	Refs []struct {
		Target string `json:"target"`
		Lines  []int  `json:"lines"`
	} `json:"refs"`
	Impls []struct {
		Target string `json:"target"`
		Local  string `json:"local"`
	} `json:"impls"`
}

// symbol is one definition with everything the server says about it.
type symbol struct {
	Path    string `json:"path"`
	Name    string `json:"name"` // qualified
	Kind    string `json:"kind"`
	Start   int    `json:"start"`
	Stratum string `json:"stratum"`
	refs    map[string]bool
	impls   int
}

// Strata the sample is drawn from (CODE-013).
const (
	stratumFunction  = "function"
	stratumAmbiguous = "ambiguous-method"
	stratumInterface = "interface"
	// stratumMethod (methods with a name of their own) only fills places
	// the others leave: a PHP tree has methods where Go has functions.
	stratumMethod = "method"
)

func (s *symbol) target() string { return s.Path + "#" + s.Name }

// bare is the name an agent types: the last segment of the qualified name,
// without a PHP namespace.
func (s *symbol) bare() string {
	n := s.Name[strings.LastIndexByte(s.Name, '.')+1:]
	return n[strings.LastIndexByte(n, '\\')+1:]
}

// loc is how a reference is compared: document and line.
func loc(path string, line int) string { return path + ":" + strconv.Itoa(line) }

// loadTruth reads a truth file into its symbols, sorted by target. The
// definition's own line is not a reference (code_symbol leaves it out in
// both modes).
func loadTruth(file string) (string, []*symbol, error) {
	b, err := os.ReadFile(file) // #nosec G304 -- the operator names the file
	if err != nil {
		return "", nil, err
	}
	var t truthFile
	if err := json.Unmarshal(b, &t); err != nil {
		return "", nil, err
	}
	if len(t.Files) == 0 {
		return "", nil, errors.New("truth file has no files")
	}
	byTarget := map[string]*symbol{}
	var out []*symbol
	for p, ff := range t.Files {
		for _, d := range ff.Defs {
			s := &symbol{Path: p, Name: d.Name, Kind: d.Kind, Start: d.Start, refs: map[string]bool{}}
			if byTarget[s.target()] == nil {
				byTarget[s.target()] = s
				out = append(out, s)
			}
		}
	}
	for p, ff := range t.Files {
		for _, r := range ff.Refs {
			if s := byTarget[r.Target]; s != nil {
				for _, l := range r.Lines {
					if p != s.Path || l != s.Start {
						s.refs[loc(p, l)] = true
					}
				}
			}
		}
		for _, i := range ff.Impls {
			if s := byTarget[i.Target]; s != nil {
				s.impls++
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].target() < out[j].target() })
	return t.Server, out, nil
}

// isTestFile tells test code apart: the sample is drawn from the code an
// agent changes, while references from tests still count.
func isTestFile(p string) bool {
	base := path.Base(p)
	return strings.HasSuffix(base, "_test.go") || strings.HasPrefix(base, "test_") ||
		strings.HasSuffix(base, "_test.py") || base == "conftest.py" || strings.HasSuffix(base, "Test.php")
}

// stratify sorts the symbols outside test files into the three strata. A
// method is ambiguous when another definition shares its bare name. The
// interface stratum holds interfaces; a language without them (Python)
// uses the classes something implements or subclasses.
func stratify(all []*symbol) map[string][]*symbol {
	names := map[string]int{}
	for _, s := range all {
		names[s.bare()]++
	}
	out := map[string][]*symbol{}
	var implemented []*symbol
	for _, s := range all {
		if isTestFile(s.Path) {
			continue
		}
		switch {
		case s.Kind == "function":
			out[stratumFunction] = append(out[stratumFunction], s)
		case s.Kind == "method" && names[s.bare()] > 1:
			out[stratumAmbiguous] = append(out[stratumAmbiguous], s)
		case s.Kind == "method":
			out[stratumMethod] = append(out[stratumMethod], s)
		case s.Kind == "interface":
			out[stratumInterface] = append(out[stratumInterface], s)
		case s.Kind == "class" && s.impls > 0:
			implemented = append(implemented, s)
		}
	}
	if len(out[stratumInterface]) == 0 {
		out[stratumInterface] = implemented
	}
	return out
}

// sample draws n symbols with a fixed seed: 40% functions, 40% ambiguous
// methods, 20% interfaces; a short stratum's places go to the others in that
// order, then to methods with a name of their own. Same truth, n and seed,
// same sample.
func sample(all []*symbol, n int, seed int64) []*symbol {
	strata := stratify(all)
	order := []string{stratumFunction, stratumAmbiguous, stratumInterface, stratumMethod}
	quota := map[string]int{stratumFunction: n * 2 / 5, stratumAmbiguous: n * 2 / 5}
	quota[stratumInterface] = n - quota[stratumFunction] - quota[stratumAmbiguous]
	rng := rand.New(rand.NewSource(seed)) // #nosec G404 -- a reproducible sample, not a secret
	for _, name := range order {
		list := strata[name]
		rng.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
	}
	var out []*symbol
	taken := map[string]int{}
	for _, name := range order {
		k := min(quota[name], len(strata[name]))
		taken[name] = k
		out = append(out, tag(strata[name][:k], name)...)
	}
	for _, name := range order { // fill what short strata left
		k := min(n-len(out), len(strata[name])-taken[name])
		out = append(out, tag(strata[name][taken[name]:taken[name]+k], name)...)
		taken[name] += k
	}
	return out
}

func tag(list []*symbol, stratum string) []*symbol {
	for _, s := range list {
		s.Stratum = stratum
	}
	return list
}
