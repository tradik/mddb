package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"

	"mddb/internal/storage"
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

// datasetRecords reads the collections and returns the records of the
// dataset in their final order, and what each collection gave.
func (s *Server) datasetRecords(req *datasetRequest) ([]datasetRecord, []collectionSummary, error) {
	client := NewDirectClient(s)
	seen := map[[32]byte]bool{}
	var records []datasetRecord
	summaries := make([]collectionSummary, 0, len(req.Collections))
	for _, c := range req.Collections {
		docs, err := client.collectExportDocs(&MCPExportRequest{Collection: c, FilterMeta: req.FilterMeta})
		if err != nil {
			return nil, nil, fmt.Errorf("collection %s: %w", c, err)
		}
		sum := collectionSummary{Name: c, License: s.collectionLicence(c)}
		for i := range docs {
			if req.Lang != "" && !strings.EqualFold(docs[i].Lang, req.Lang) {
				continue
			}
			sum.Documents++
			rec, ok := buildRecord(req, c, sum.License, &docs[i])
			if !ok {
				sum.Skipped++
				continue
			}
			if req.dedupe() {
				h := rec.hash()
				if seen[h] {
					sum.Duplicates++
					continue
				}
				seen[h] = true
			}
			records = append(records, rec)
		}
		summaries = append(summaries, sum)
	}
	if req.Shuffle {
		// #nosec G404 G115 -- a reproducible shuffle for a seed the caller chose, not a secret; a negative
		// seed wrapping to a large one is still a seed
		rng := rand.New(rand.NewPCG(uint64(req.Seed), 0)) // NOSONAR -- reproducible order, not security
		rng.Shuffle(len(records), func(i, j int) { records[i], records[j] = records[j], records[i] })
	}
	limit := req.MaxRecords
	if limit == 0 {
		limit = maxDatasetRecords
	}
	if len(records) > limit {
		records = records[:limit]
	}
	for i := range summaries {
		for j := range records {
			if records[j].Meta.Collection == summaries[i].Name {
				summaries[i].Records++
			}
		}
	}
	return records, summaries, nil
}

// buildRecord turns a document into a record; false when its text, or a
// field a template names, is empty.
func buildRecord(req *datasetRequest, collection, licence string, d *storage.Doc) (datasetRecord, bool) {
	rec := datasetRecord{Meta: recordMeta{Collection: collection, Key: d.Key, Lang: d.Lang, License: licence}}
	if req.Format == datasetText {
		rec.Text = strings.TrimSpace(d.ContentMD)
		return rec, rec.Text != ""
	}
	var ok bool
	if rec.Input, ok = renderTemplate(req.Pairs.Input, collection, d); !ok {
		return rec, false
	}
	if rec.Output, ok = renderTemplate(req.Pairs.Output, collection, d); !ok {
		return rec, false
	}
	if req.Pairs.System != "" {
		if rec.System, ok = renderTemplate(req.Pairs.System, collection, d); !ok {
			return rec, false
		}
	}
	return rec, true
}

// renderTemplate fills a template from a document; false when a field it
// names is empty.
func renderTemplate(t, collection string, d *storage.Doc) (string, bool) {
	complete := true
	out := fieldPattern.ReplaceAllStringFunc(t, func(ph string) string {
		v := fieldValue(ph[1:len(ph)-1], collection, d)
		if v == "" {
			complete = false
		}
		return v
	})
	out = strings.TrimSpace(out)
	return out, complete && out != ""
}

// fieldValue is one field of a document; several metadata values are joined
// with ", ".
func fieldValue(field, collection string, d *storage.Doc) string {
	switch field {
	case "content":
		return strings.TrimSpace(d.ContentMD)
	case "key":
		return d.Key
	case "lang":
		return d.Lang
	case "collection":
		return collection
	default:
		return strings.Join(d.Meta[strings.TrimPrefix(field, "meta.")], ", ")
	}
}

// hash identifies a record's content for duplicate removal; where it came
// from does not count.
func (r *datasetRecord) hash() [32]byte {
	return sha256.Sum256([]byte(r.Text + "\x00" + r.System + "\x00" + r.Input + "\x00" + r.Output))
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
