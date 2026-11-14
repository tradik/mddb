package main

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// Sending an autoindex run's documents (AGT-001): batches for /v1/ingest.

// ingestFiles sends the files in batches of at most 200 documents or 4 MiB.
// Unchanged content is skipped by the server (skipDuplicates), which is what
// makes a second run cheap. sync, when facts were computed, adds them and
// holds back the documents the collection already has as they are.
func ingestFiles(ctx context.Context, client *mddb.Client, rep *autoindexReport, files []autoFile, o autoindexOptions, sync *semanticSync) {
	b := &ingestBatch{client: client, rep: rep, o: o, sync: sync}
	for _, f := range files {
		content, err := os.ReadFile(f.abs) // #nosec G304 -- a file under the directory the user asked to index
		if err != nil {
			rep.Failed++
			rep.Errors = append(rep.Errors, f.Rel+": "+err.Error())
			continue
		}
		b.add(ctx, f, content)
	}
	b.flush(ctx)
}

// ingestBatch collects documents for one /v1/ingest call and records what
// each call did in the report.
type ingestBatch struct {
	client *mddb.Client
	rep    *autoindexReport
	o      autoindexOptions
	sync   *semanticSync // nil without --semantic
	docs   []mddb.IngestDocument
	size   int
}

// add queues a file's document, sending the batch first when the file
// would take it over the document or byte limit.
func (b *ingestBatch) add(ctx context.Context, f autoFile, content []byte) {
	isMarkdown := docExtensions[strings.ToLower(filepath.Ext(f.Rel))]
	// The server reads the frontmatter, once, with the /v1/upload parser.
	doc := mddb.IngestDocument{Key: f.Rel, Lang: b.o.lang, ExtractFrontmatter: isMarkdown && !b.o.noFrontmatter}
	if b.o.images && isMarkdown {
		doc.Images = markdownImageFiles(b.rep.Root, f.Rel, content) // RAG-010
	}
	doc.Meta = fileMeta(f, b.rep.Git)
	for k, v := range b.o.extraMeta {
		doc.Meta[k] = v
	}
	if b.sync != nil && !b.sync.attach(f.Rel, content, doc.Meta) {
		return
	}
	n := wireSize(f, content, doc.Images)
	if len(b.docs) > 0 && (len(b.docs) >= autoindexBatchDocs || b.size+n > autoindexBatchBytes) {
		b.flush(ctx)
	}
	if f.Format != "" {
		// The server converts it, with the converters /v1/upload uses.
		doc.Format, doc.Content = f.Format, content
		b.rep.Converted = append(b.rep.Converted, autoConverted{Rel: f.Rel, Format: f.Format})
	} else {
		doc.ContentMD = string(content)
	}
	b.docs = append(b.docs, doc)
	b.size += n
}

// flush sends the queued documents, if any, and empties the batch.
func (b *ingestBatch) flush(ctx context.Context) {
	if len(b.docs) == 0 {
		return
	}
	rep := b.rep
	resp, err := b.client.Ingest(ctx, mddb.IngestRequest{Collection: rep.Collection, Documents: b.docs,
		Options: mddb.IngestOptions{SkipDuplicates: true, Profile: b.o.profile, ExtractImages: b.o.images}})
	if err != nil {
		rep.Failed += len(b.docs)
		rep.Errors = append(rep.Errors, err.Error())
	} else {
		rep.Added, rep.Updated, rep.Unchanged = rep.Added+resp.Added, rep.Updated+resp.Updated, rep.Unchanged+resp.Skipped
		rep.Failed += resp.Failed
		rep.Errors = append(rep.Errors, resp.Errors...)
		rep.noteConversions(resp.Conversions)
	}
	b.docs, b.size = nil, 0
}

// wireSize is what a file adds to a batch: its bytes, or their base64 when
// it is sent for conversion, and the base64 of the images sent with it.
func wireSize(f autoFile, content []byte, images []mddb.IngestImage) int {
	n := len(content)
	if f.Format != "" {
		n = base64.StdEncoding.EncodedLen(len(content))
	}
	for _, im := range images {
		n += base64.StdEncoding.EncodedLen(len(im.Content))
	}
	return n
}
