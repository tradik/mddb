package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mddb-cli/internal/lspindex"
)

// autoindex --semantic (CODE-011): before the documents go out, a language
// server is run over the checkout, one language after another (two at once
// would need the sum of their memory), and what it says about each file —
// definitions, references, implementations — goes with the file's document
// as the sym* meta keys of testdata/semantic-facts/README.md.
//
// "auto" is forgiving: a language without a server, or whose server fails,
// keeps the heuristics and the run goes on with a warning. A list is a
// demand: anything missing or failing stops the run before a byte is sent.

const (
	semanticAuto = "auto"
	// defaultSemanticTimeout bounds one language's server: gopls took two
	// minutes over mddbd, so twenty leave room for a large monorepo.
	defaultSemanticTimeout     = 20 * time.Minute
	defaultSemanticConcurrency = 4
)

// semanticFacts computes the facts for a run, nil without --semantic. An
// error (list mode only) means nothing may be sent.
func semanticFacts(ctx context.Context, root string, files []autoFile, rep *autoindexReport, o semanticOptions) (map[string]fileFacts, error) {
	if o.langs == "" {
		return nil, nil
	}
	rep.Semantic = &semanticReport{Mode: "list"}
	if o.langs == semanticAuto {
		rep.Semantic.Mode = semanticAuto
	}
	plans, err := planSemantic(o, files, rep.announced, rep.Semantic)
	if err != nil {
		return nil, err
	}
	return runSemantic(ctx, root, plans, o, rep.Semantic)
}

// semanticOptions are the --semantic flags.
type semanticOptions struct {
	// langs is "" (off), "auto" or a comma-separated list of languages.
	langs       string
	timeout     time.Duration
	concurrency int
	memoryMb    int
	// lookPath replaces exec.LookPath in tests; nil means the real one.
	lookPath func(string) (string, error)
}

// fileFacts are one file's facts and the server that produced them.
type fileFacts struct {
	source string
	facts  *lspindex.FileFacts
}

// semanticPlan is a language to index and the files to index for it.
type semanticPlan struct {
	language string
	spec     lspindex.ServerSpec
	files    []string
}

// planSemantic picks the languages to index. announced is the server's
// `semantic` map from /v1/code-languages, nil when it has none.
func planSemantic(o semanticOptions, files []autoFile, announced map[string]bool, rep *semanticReport) ([]semanticPlan, error) {
	byLang := map[string][]string{}
	for _, f := range files {
		if f.Format == "" && f.Language != "" {
			byLang[f.Language] = append(byLang[f.Language], f.Rel)
		}
	}
	explicit := o.langs != semanticAuto
	langs := lspindex.Languages()
	if explicit {
		langs = splitLanguages(o.langs)
	}
	if !explicit && announced == nil {
		rep.warn("the server keeps no semantic facts (it predates 2.19.3); documents go without them")
		return nil, nil
	}
	var plans []semanticPlan
	for _, lang := range langs {
		p, err := planLanguage(lang, o, byLang[lang], announced)
		if err != nil {
			if explicit {
				return nil, err
			}
			rep.warn(err.Error() + "; " + lang + " files keep the heuristics")
			continue
		}
		if p == nil {
			if explicit {
				rep.warn("no " + lang + " files to index")
			}
			continue
		}
		plans = append(plans, *p)
	}
	return plans, nil
}

// errNotAnnounced is a language the server does not keep facts for.
var errNotAnnounced = errors.New("the server keeps no semantic facts for this language (upgrade mddbd to 2.19.3 or later)")

// planLanguage checks one language: the driver knows it, the server keeps
// its facts, a server binary is installed. nil, nil means nothing to do: no
// files of the language in the tree, or (auto) a language the server does
// not announce.
func planLanguage(lang string, o semanticOptions, files []string, announced map[string]bool) (*semanticPlan, error) {
	spec, err := lspindex.Spec(lang)
	if err != nil {
		return nil, err
	}
	if !announced[lang] {
		if o.langs == semanticAuto {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: %w", lang, errNotAnnounced)
	}
	if len(files) == 0 {
		return nil, nil
	}
	if _, err := lspindex.Find(spec, lspindex.Options{LookPath: o.lookPath}); err != nil {
		return nil, err
	}
	return &semanticPlan{language: lang, spec: spec, files: files}, nil
}

// splitLanguages parses a --semantic list: comma-separated, trimmed,
// lower-cased, each once, in the order given.
func splitLanguages(list string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range strings.Split(list, ",") {
		l = strings.ToLower(strings.TrimSpace(l))
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

// runSemantic indexes each planned language in turn and returns the facts by
// file. In auto mode a failed language is a warning; in list mode the error.
func runSemantic(ctx context.Context, root string, plans []semanticPlan, o semanticOptions, rep *semanticReport) (map[string]fileFacts, error) {
	facts := map[string]fileFacts{}
	opt := lspindex.Options{Concurrency: o.concurrency, Timeout: o.timeout, MemoryLimitMb: o.memoryMb, LookPath: o.lookPath}
	for _, p := range plans {
		res, err := lspindex.Index(ctx, root, p.files, p.spec, opt)
		if err != nil {
			if o.langs != semanticAuto {
				return nil, fmt.Errorf("semantic facts for %s: %w", p.language, err)
			}
			rep.Languages = append(rep.Languages, semanticLanguage{Language: p.language, Error: err.Error()})
			rep.warn(p.language + " files go without semantic facts: " + err.Error())
			continue
		}
		rep.Languages = append(rep.Languages, languageStats(p.language, res))
		for rel, ff := range res.Files {
			facts[rel] = fileFacts{source: res.Server, facts: ff}
		}
	}
	return facts, nil
}

// semanticLanguagesUsage lists what --semantic accepts, for the help text.
func semanticLanguagesUsage() string { return strings.Join(lspindex.Languages(), ",") }
