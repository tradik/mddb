package main

import (
	"crypto/sha256"
	"fmt"
	"math/rand/v2"
	"strings"

	"mddb/internal/storage"
)

// The records of a training dataset: each collection's documents read,
// turned into records, duplicates dropped, then shuffled, cut to the limit
// and counted per collection.

// datasetCollector gathers the records of a dataset across collections; seen
// holds the hashes of the records kept, for duplicate removal.
type datasetCollector struct {
	req     *datasetRequest
	seen    map[[32]byte]bool
	records []datasetRecord
}

// datasetRecords reads the collections and returns the records of the
// dataset in their final order, and what each collection gave.
func (s *Server) datasetRecords(req *datasetRequest) ([]datasetRecord, []collectionSummary, error) {
	client := NewDirectClient(s)
	col := &datasetCollector{req: req, seen: map[[32]byte]bool{}}
	summaries := make([]collectionSummary, 0, len(req.Collections))
	for _, c := range req.Collections {
		docs, err := client.collectExportDocs(&MCPExportRequest{Collection: c, FilterMeta: req.FilterMeta})
		if err != nil {
			return nil, nil, fmt.Errorf("collection %s: %w", c, err)
		}
		summaries = append(summaries, col.addCollection(c, s.collectionLicence(c), docs))
	}
	records := orderRecords(req, col.records)
	countRecords(summaries, records)
	return records, summaries, nil
}

// addCollection turns one collection's documents into records and says
// what it gave.
func (col *datasetCollector) addCollection(c, licence string, docs []storage.Doc) collectionSummary {
	sum := collectionSummary{Name: c, License: licence}
	for i := range docs {
		if col.req.Lang != "" && !strings.EqualFold(docs[i].Lang, col.req.Lang) {
			continue
		}
		sum.Documents++
		rec, ok := buildRecord(col.req, c, licence, &docs[i])
		switch {
		case !ok:
			sum.Skipped++
		case col.duplicate(&rec):
			sum.Duplicates++
		default:
			col.records = append(col.records, rec)
		}
	}
	return sum
}

// duplicate reports a record already kept, remembering it otherwise; never
// when the request keeps duplicates.
func (col *datasetCollector) duplicate(rec *datasetRecord) bool {
	if !col.req.dedupe() {
		return false
	}
	h := rec.hash()
	if col.seen[h] {
		return true
	}
	col.seen[h] = true
	return false
}

// orderRecords shuffles the records when the request asks and cuts them to
// its limit.
func orderRecords(req *datasetRequest, records []datasetRecord) []datasetRecord {
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
	return records
}

// countRecords sets how many of the final records each collection gave;
// every record comes from one of the summaries' collections.
func countRecords(summaries []collectionSummary, records []datasetRecord) {
	index := make(map[string]int, len(summaries))
	for i := range summaries {
		index[summaries[i].Name] = i
	}
	for j := range records {
		summaries[index[records[j].Meta.Collection]].Records++
	}
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
