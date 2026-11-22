package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Training datasets: the text of one or several collections as JSON Lines,
// for a trainer the operator runs. Vectors are not training data, so only
// text leaves: one record per document ("text"), or an input and an output
// built from fields the request names ("pairs"). The documents are read the
// way /v1/export reads them (collectExportDocs), in both storage layouts.

// Dataset formats.
const (
	datasetText  = "text"
	datasetPairs = "pairs"
)

// Limits of a dataset request.
const (
	maxDatasetCollections = 16
	maxDatasetRecords     = 1_000_000
	maxDatasetValidation  = 0.5
	maxPairTemplate       = 2000
	datasetPreviewRecords = 3
	datasetPreviewChars   = 500
)

var (
	errDatasetInvalid = errors.New("invalid dataset request")
	errDatasetLicence = errors.New("licence acknowledgement required")
	errDatasetEmpty   = errors.New("the dataset has no records")
	// fieldPattern is a placeholder of a pairs template.
	fieldPattern = regexp.MustCompile(`\{(content|key|lang|collection|meta\.[A-Za-z0-9_.:-]{1,128})\}`)
)

// datasetRequest is what a dataset is built from.
type datasetRequest struct {
	Name                  string              `json:"name,omitempty"`
	Collections           []string            `json:"collections"`
	Format                string              `json:"format"`
	FilterMeta            map[string][]string `json:"filterMeta,omitempty"`
	Lang                  string              `json:"lang,omitempty"`
	MaxRecords            int                 `json:"maxRecords,omitempty"`
	Shuffle               bool                `json:"shuffle,omitempty"`
	Seed                  int64               `json:"seed,omitempty"`
	ValidationRatio       float64             `json:"validationRatio,omitempty"`
	Dedupe                *bool               `json:"dedupe,omitempty"`
	Pairs                 *pairMapping        `json:"pairs,omitempty"`
	AcknowledgeUnlicensed bool                `json:"acknowledgeUnlicensed,omitempty"`
}

// pairMapping is a template per side of a pairs record, over {content},
// {key}, {lang}, {collection} and {meta.<name>}.
type pairMapping struct {
	Input  string `json:"input"`
	Output string `json:"output"`
	System string `json:"system,omitempty"`
}

// datasetRecord is one line of a dataset file.
type datasetRecord struct {
	Text   string     `json:"text,omitempty"`
	System string     `json:"system,omitempty"`
	Input  string     `json:"input,omitempty"`
	Output string     `json:"output,omitempty"`
	Meta   recordMeta `json:"meta"`
}

// recordMeta says where a record came from and under which licence.
type recordMeta struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Lang       string `json:"lang,omitempty"`
	License    string `json:"license,omitempty"`
}

// collectionSummary is what one collection gave.
type collectionSummary struct {
	Name       string `json:"name"`
	License    string `json:"license,omitempty"`
	Documents  int    `json:"documents"`
	Records    int    `json:"records"`
	Duplicates int    `json:"duplicates"`
	Skipped    int    `json:"skipped"`
}

// datasetSummary describes a built dataset.
type datasetSummary struct {
	Format                 string              `json:"format"`
	Records                int                 `json:"records"`
	TrainRecords           int                 `json:"trainRecords"`
	ValidationRecords      int                 `json:"validationRecords"`
	Bytes                  int64               `json:"bytes"`
	Collections            []collectionSummary `json:"collections"`
	UnlicensedAcknowledged []string            `json:"unlicensedAcknowledged,omitempty"`
	Preview                []datasetRecord     `json:"preview"`
	Options                datasetRequest      `json:"options"`
}

// validate checks a request before anything is read.
func (r *datasetRequest) validate() error {
	if len(r.Collections) == 0 || len(r.Collections) > maxDatasetCollections {
		return fmt.Errorf("%w: 1 to %d collections", errDatasetInvalid, maxDatasetCollections)
	}
	seen := map[string]bool{}
	for _, c := range r.Collections {
		if c == "" || seen[c] {
			return fmt.Errorf("%w: collection %q is empty or named twice", errDatasetInvalid, c)
		}
		seen[c] = true
	}
	if r.MaxRecords < 0 || r.MaxRecords > maxDatasetRecords {
		return fmt.Errorf("%w: maxRecords from 0 (no limit) to %d", errDatasetInvalid, maxDatasetRecords)
	}
	if r.ValidationRatio < 0 || r.ValidationRatio > maxDatasetValidation {
		return fmt.Errorf("%w: validationRatio from 0 to %g", errDatasetInvalid, maxDatasetValidation)
	}
	switch r.Format {
	case datasetText:
		r.Pairs = nil
		return nil
	case datasetPairs:
		return r.Pairs.validate()
	default:
		return fmt.Errorf("%w: format is %q or %q", errDatasetInvalid, datasetText, datasetPairs)
	}
}

// validate checks the templates of a pairs dataset.
func (m *pairMapping) validate() error {
	if m == nil || m.Input == "" || m.Output == "" {
		return fmt.Errorf("%w: a pairs dataset needs pairs.input and pairs.output", errDatasetInvalid)
	}
	for _, t := range []string{m.Input, m.Output, m.System} {
		if len(t) > maxPairTemplate {
			return fmt.Errorf("%w: a template is longer than %d bytes", errDatasetInvalid, maxPairTemplate)
		}
		rest := fieldPattern.ReplaceAllString(t, "")
		if strings.ContainsAny(rest, "{}") {
			return fmt.Errorf("%w: template %q: placeholders are {content}, {key}, {lang}, {collection} and {meta.<name>}", errDatasetInvalid, t)
		}
	}
	return nil
}

// dedupe reports whether exact duplicates are dropped (the default).
func (r *datasetRequest) dedupe() bool { return r.Dedupe == nil || *r.Dedupe }

// collectionLicence is the licence a collection records: the one of the
// corpus pack it was installed from, "" when unknown.
func (s *Server) collectionLicence(name string) string {
	if s.CollectionManager == nil {
		return ""
	}
	if cfg, ok := s.CollectionManager.Get(name); ok && cfg.Corpus != nil {
		return strings.TrimSpace(cfg.Corpus.License)
	}
	return ""
}

// checkLicences refuses collections without licence information unless the
// request acknowledges them, and returns those it acknowledged.
func (s *Server) checkLicences(req *datasetRequest) ([]string, error) {
	var unlicensed []string
	for _, c := range req.Collections {
		if s.collectionLicence(c) == "" {
			unlicensed = append(unlicensed, c)
		}
	}
	if len(unlicensed) > 0 && !req.AcknowledgeUnlicensed {
		return nil, fmt.Errorf("%w: %s record no licence; check you may train on them and send acknowledgeUnlicensed: true",
			errDatasetLicence, strings.Join(unlicensed, ", "))
	}
	return unlicensed, nil
}

// splitRecords cuts the validation part off the end: ratio of the records,
// at least one when the ratio is above zero and there are two or more.
func splitRecords(records []datasetRecord, ratio float64) (train, validation []datasetRecord) {
	n := int(float64(len(records)) * ratio)
	if ratio > 0 && n == 0 && len(records) >= 2 {
		n = 1
	}
	return records[:len(records)-n], records[len(records)-n:]
}

// previewRecords is the first records, their long fields cut.
func previewRecords(records []datasetRecord) []datasetRecord {
	out := make([]datasetRecord, 0, datasetPreviewRecords)
	for i := 0; i < len(records) && i < datasetPreviewRecords; i++ {
		r := records[i]
		r.Text, r.System = cutText(r.Text, datasetPreviewChars), cutText(r.System, datasetPreviewChars)
		r.Input, r.Output = cutText(r.Input, datasetPreviewChars), cutText(r.Output, datasetPreviewChars)
		out = append(out, r)
	}
	return out
}

// cutText cuts s to n characters, marking the cut.
func cutText(s string, n int) string {
	if t, cut := truncateRunes(s, n); cut {
		return t + "…"
	}
	return s
}
