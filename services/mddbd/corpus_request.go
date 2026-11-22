package main

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	json "mddb/internal/jsonx"
)

// The body of POST /v1/corpus/build (AGT-005), and the manifest it plans.
// "collection" names the one collection of a pack, as it always has;
// "collections" (FE-022) names one or several, each a name or an object
// with its own licence and sources. Naming both is an error.

// corpusBuildRequest is the body of POST /v1/corpus/build.
type corpusBuildRequest struct {
	Collection string `json:"collection,omitempty"`
	// Collections are the collections of the pack; Name is then the pack's
	// name, required for more than one.
	Collections []corpusBuildCollection `json:"collections,omitempty"`
	Name        string                  `json:"name,omitempty"`
	Version     string                  `json:"version,omitempty"`
	Description string                  `json:"description,omitempty"`
	License     string                  `json:"license"`
	Sources     []corpusSource          `json:"sources,omitempty"`
}

// corpusBuildCollection is one collection of a build. A collection without a
// licence of its own is under the pack's.
type corpusBuildCollection struct {
	Name    string         `json:"name"`
	License string         `json:"license,omitempty"`
	Sources []corpusSource `json:"sources,omitempty"`
}

// UnmarshalJSON takes a bare name as well as an object.
func (c *corpusBuildCollection) UnmarshalJSON(b []byte) error {
	if bytes.HasPrefix(bytes.TrimSpace(b), []byte(`"`)) {
		*c = corpusBuildCollection{}
		return json.Unmarshal(b, &c.Name)
	}
	type plain corpusBuildCollection // the same fields, without this method
	return json.Unmarshal(b, (*plain)(c))
}

// plan is the manifest the request asks for and its collections, not yet
// described: format 1 for one collection without a licence or sources of its
// own, format 2 otherwise. Every refusal wraps errCorpusInvalid.
func (req corpusBuildRequest) plan() (*corpusManifest, []corpusPart, error) {
	m := &corpusManifest{Format: corpusFormat, Name: req.Name, Version: req.Version,
		Description: req.Description, MddbVersion: VERSION, CreatedAt: time.Now().UTC(),
		License: req.License, Sources: req.Sources}
	cols := req.Collections
	switch {
	case req.Collection != "" && len(cols) > 0:
		return nil, nil, fmt.Errorf("%w: name the pack's collections in \"collection\" or in \"collections\", not both", errCorpusInvalid)
	case len(cols) == 0:
		cols = []corpusBuildCollection{{Name: req.Collection}}
	}
	if len(cols) == 1 && cols[0].License == "" && len(cols[0].Sources) == 0 {
		if m.Name != "" && m.Name != cols[0].Name {
			return nil, nil, fmt.Errorf("%w: a pack of one collection installs under the collection's name, %q, not %q", errCorpusInvalid, cols[0].Name, m.Name)
		}
		m.Name = cols[0].Name
		return m, m.parts(), m.validateIdentity()
	}
	m.Format = corpusFormatMulti
	parts := make([]corpusPart, len(cols))
	for i, c := range cols {
		parts[i] = corpusPart{Name: c.Name, License: c.License, Sources: c.Sources}
		if parts[i].License == "" {
			parts[i].License = req.License
		}
	}
	m.Collections = parts
	if m.Name == "" {
		return nil, nil, fmt.Errorf("%w: a pack of several collections needs a name of its own (\"name\")", errCorpusInvalid)
	}
	return m, parts, m.validateIdentity()
}

// validateIdentity refuses a planned manifest whose name, licences or
// collection list cannot make a pack.
func (m *corpusManifest) validateIdentity() error {
	err := errors.Join(validPackName(m.Name), m.validateLicenses())
	if err == nil && m.Format == corpusFormatMulti {
		err = validPartList(m.Collections)
	}
	for _, p := range m.parts() {
		if err != nil {
			break
		}
		err = p.validateIdentity()
	}
	if err != nil {
		return fmt.Errorf("%w: %w", errCorpusInvalid, err)
	}
	return nil
}
