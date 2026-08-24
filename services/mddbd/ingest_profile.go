package main

import "fmt"

// Named ingest profiles (RAG-004).
//
// MDDB has always been able to ingest faster by skipping steps — the flags
// exist — but only as separate switches a caller had to discover one at a time,
// and `wiki_import.go` set SkipFTS with the comment "faster bulk import",
// making the same choice a third way. So the trade-off was available and
// undiscoverable, which is the worst of both.
//
// A profile names it. `fast` is a deliberate exchange of parsing fidelity and
// bookkeeping for throughput, chosen once and recorded in the response, rather
// than reverse-engineered from a combination of booleans.

// Ingest profiles.
const (
	// IngestProfileDefault preserves every step: full Markdown conversion,
	// revisions, webhooks, duplicate handling.
	IngestProfileDefault = "default"
	// IngestProfileFast trades parsing fidelity and bookkeeping for
	// throughput. Chosen for a bulk load where the corpus matters more than
	// any individual document's structure.
	IngestProfileFast = "fast"
)

// IngestProfile is a resolved set of ingest behaviours.
type IngestProfile struct {
	Name                    string
	SkipDuplicates          bool
	SkipEmbeddings          bool
	SkipFTS                 bool
	SkipWebhooks            bool
	AutoConfigureCollection bool
	SaveRevision            bool
	// TextOnly extracts plain text from heavy formats instead of rebuilding
	// their structure as Markdown.
	TextOnly bool
}

// ValidateIngestProfile rejects a profile name MDDB does not implement.
//
// A typo must not silently fall back to the default: a caller who asked for
// `fast` and got default behaviour would see a slow bulk load and no reason
// why.
func ValidateIngestProfile(name string) error {
	switch name {
	case "", IngestProfileDefault, IngestProfileFast:
		return nil
	default:
		return fmt.Errorf("unknown ingest profile %q: must be %q or %q",
			name, IngestProfileDefault, IngestProfileFast)
	}
}

// ResolveIngestProfile expands a profile name into behaviours, then lets any
// explicitly-set flag override it.
//
// Precedence matches RAG-001: an explicit request field wins over the preset.
// A caller asking for `fast` but wanting revisions kept says so, rather than
// having to abandon the profile.
//
// Note what `fast` does NOT set: SkipEmbeddings. Fast means cheaper parsing and
// less bookkeeping, not a collection you cannot search semantically — that is a
// separate decision with a separate flag, and folding it in would make `fast`
// mean something a caller did not ask for.
func ResolveIngestProfile(opts *IngestOptionsHTTP) (IngestProfile, error) {
	if opts == nil {
		return IngestProfile{Name: IngestProfileDefault}, nil
	}
	if err := ValidateIngestProfile(opts.Profile); err != nil {
		return IngestProfile{}, err
	}

	p := IngestProfile{Name: IngestProfileDefault}
	if opts.Profile == IngestProfileFast {
		p = IngestProfile{
			Name: IngestProfileFast,
			// Revisions and webhooks are per-document bookkeeping whose
			// cost scales with the load; on a bulk import nobody is
			// watching the individual events.
			SkipWebhooks: true,
			SaveRevision: false,
			// A bulk load re-run after a failure should not double the
			// corpus.
			SkipDuplicates: true,
			TextOnly:       true,
		}
	}

	// Explicit flags override the preset. Only true values can override,
	// because JSON cannot distinguish false from absent — so `fast` plus
	// `saveRevision: true` keeps revisions, while `fast` alone does not.
	if opts.SkipDuplicates {
		p.SkipDuplicates = true
	}
	if opts.SkipEmbeddings {
		p.SkipEmbeddings = true
	}
	if opts.SkipFTS {
		p.SkipFTS = true
	}
	if opts.SkipWebhooks {
		p.SkipWebhooks = true
	}
	if opts.AutoConfigureCollection {
		p.AutoConfigureCollection = true
	}
	if opts.SaveRevision {
		p.SaveRevision = true
	}
	if opts.TextOnly {
		p.TextOnly = true
	}

	return p, nil
}
