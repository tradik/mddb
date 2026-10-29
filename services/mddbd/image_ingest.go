package main

import (
	"bytes"
	"context"

	proto "mddb/proto"
)

// Images on the ingest path (RAG-010): REST /v1/ingest, gRPC Ingest, MCP
// ingest_documents and `mddb-cli autoindex --images` all reach these.

// ingestImageFiles finds the files sent with an ingest document by the path
// its links name.
func ingestImageFiles(images []IngestImage) func(string) ([]byte, bool) {
	if len(images) == 0 {
		return nil
	}
	files := make(map[string][]byte, len(images))
	for _, im := range images {
		files[im.Path] = im.Content
	}
	return localFiles(files)
}

// markdownImageDocs is a Markdown ingest document with its images as
// documents under it. A document whose key names another format is not
// Markdown and is stored as it is.
func markdownImageDocs(ctx context.Context, key, content string, meta map[string][]string, opts imageOptions) []convertedDoc {
	switch formatFromFilename(key) {
	case "md", "markdown", "":
	default:
		return []convertedDoc{{Key: key, Markdown: content, Meta: meta}}
	}
	c := conversion{Format: "md", Markdown: content}
	c.Images, c.ImageWarnings = extractImages(ctx, "md", nil, content, opts)
	return convertedDocs(c, key, meta)
}

// changedImages are the image documents whose bytes differ from the stored
// ones: kept by skipDuplicates even when their text is unchanged.
func (s *Server) changedImages(collection string, images map[string][]byte) map[string]bool {
	changed := map[string]bool{}
	for id, data := range images {
		if !bytes.Equal(s.imageBytes(collection, id), data) {
			changed[id] = true
		}
	}
	return changed
}

// storeIngestImages stores the bytes of the image documents an ingest wrote.
func (s *Server) storeIngestImages(collection string, images map[string][]byte, processed []*ProcessedDoc) {
	for _, p := range processed {
		if p == nil || p.Error != nil {
			continue
		}
		if data, ok := images[p.DocID]; ok {
			_ = s.putImageBytes(collection, p.DocID, data)
		}
	}
}

// ingestImagesFromProto converts gRPC ingest images.
func ingestImagesFromProto(in []*proto.IngestImage) []IngestImage {
	if len(in) == 0 {
		return nil
	}
	out := make([]IngestImage, len(in))
	for i, im := range in {
		out[i] = IngestImage{Path: im.Path, Content: im.Content}
	}
	return out
}
