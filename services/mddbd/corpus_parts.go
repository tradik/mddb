package main

import (
	"errors"
	"fmt"
	"strings"

	"mddb/internal/migrate"
)

// The collections of a pack (FE-022). A format-1 pack holds one collection,
// described by the manifest's own fields; a format-2 pack holds several, one
// file each, listed in the manifest's "collections". Each collection keeps
// its own name and keys, so documents of two collections never meet — a key
// used in both stays two documents — and each installs as the collection it
// was built from.
//
// File names inside a pack are never taken from the manifest: the i-th
// collection's file is called corpusMemberName(format, i), whatever the
// manifest says about it.

// corpusMaxCollections bounds the collections of one pack.
const corpusMaxCollections = 64

// corpusPart is one collection of a pack: what installs it and checks it.
type corpusPart struct {
	Name          string           `json:"name"`
	License       string           `json:"license"`
	Sources       []corpusSource   `json:"sources,omitempty"`
	SchemaVersion uint64           `json:"schemaVersion"`
	Embedding     *corpusEmbedding `json:"embedding"` // null: full-text only
	DocCount      int              `json:"docCount"`
	SHA256        string           `json:"sha256"` // of its file
	Bytes         int64            `json:"bytes"`  // of its file
}

// corpusMemberName is the archive member, and the staging file, holding the
// i-th collection of a pack of the given format.
func corpusMemberName(format, i int) string {
	if format == corpusFormatMulti {
		return fmt.Sprintf("collection-%d.db", i+1)
	}
	return corpusDataName
}

// parts are the pack's collections, in archive order. A format-1 pack's one
// collection is described by the manifest itself.
func (m *corpusManifest) parts() []corpusPart {
	if m.Format == corpusFormatMulti {
		return m.Collections
	}
	return []corpusPart{{Name: m.Name, License: m.License, Sources: m.Sources, SchemaVersion: m.SchemaVersion,
		Embedding: m.Embedding, DocCount: m.DocCount, SHA256: m.SHA256, Bytes: m.Bytes}}
}

// setParts records the described collections in the manifest: in its own
// fields for format 1, in Collections and the totals for format 2.
func (m *corpusManifest) setParts(parts []corpusPart) {
	if m.Format != corpusFormatMulti {
		p := parts[0]
		m.SchemaVersion, m.Embedding, m.DocCount, m.SHA256, m.Bytes = p.SchemaVersion, p.Embedding, p.DocCount, p.SHA256, p.Bytes
		return
	}
	m.Collections, m.DocCount, m.Bytes = parts, 0, 0
	for _, p := range parts {
		m.DocCount += p.DocCount
		m.Bytes += p.Bytes
		m.SchemaVersion = max(m.SchemaVersion, p.SchemaVersion)
	}
}

// validPartList refuses a format-2 collection list that is empty, too long
// or names a collection twice.
func validPartList(parts []corpusPart) error {
	if len(parts) == 0 || len(parts) > corpusMaxCollections {
		return fmt.Errorf("a pack of several collections lists 1 to %d of them, this one %d", corpusMaxCollections, len(parts))
	}
	seen := map[string]bool{}
	for _, p := range parts {
		if seen[p.Name] {
			return fmt.Errorf("collection %q is named twice", p.Name)
		}
		seen[p.Name] = true
	}
	return nil
}

// validateIdentity refuses a collection without a valid name or licence, or
// with a source without one.
func (p *corpusPart) validateIdentity() error {
	if err := validPackName(p.Name); err != nil {
		return err
	}
	if strings.TrimSpace(p.License) == "" {
		return fmt.Errorf("collection %q has no licence", p.Name)
	}
	return (&corpusManifest{License: p.License, Sources: p.Sources}).validateLicenses()
}

// validate checks a collection read from a manifest: its identity, a storage
// format this server knows, and a checksum of its file (member).
func (p *corpusPart) validate(member, mddbVersion string) error {
	if err := p.validateIdentity(); err != nil {
		return err
	}
	if p.SchemaVersion > migrate.Latest() {
		return fmt.Errorf("the pack was built by mddb %s with storage format %d; this server knows format %d — upgrade it first",
			mddbVersion, p.SchemaVersion, migrate.Latest())
	}
	if len(p.SHA256) != 64 {
		return errors.New("the manifest has no sha256 of " + member)
	}
	return nil
}
