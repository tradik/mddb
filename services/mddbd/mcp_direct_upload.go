package main

import (
	"context"
	"fmt"
)

// MCPUploadFileRequest is what the upload_file tool sends: one file, base64
// decoded, with the options /v1/upload takes.
type MCPUploadFileRequest struct {
	Collection, Filename, Key, Lang string
	Content                         []byte
	Meta                            map[string][]string
	TextOnly                        bool
	// ExtractImages, FetchRemoteImages and Images are RAG-010's options:
	// images become image documents, remote links are fetched, and Images
	// are the files the Markdown file's links name.
	ExtractImages, FetchRemoteImages bool
	Images                           []IngestImage
}

// UploadFile converts and stores one file the way /v1/upload does (GO-046,
// RAG-010): the conversion knows the collection, so a collection of type
// images and image extraction apply, and an image document's bytes are
// stored with it.
func (c *DirectClient) UploadFile(ctx context.Context, req *MCPUploadFileRequest) (map[string]interface{}, error) {
	s := c.server
	images := s.imageOptionsFor(req.Collection, req.ExtractImages, req.FetchRemoteImages, ingestImageFiles(req.Images))
	// One conversion path for every surface (AGT-001); text_only is the
	// RAG-004 option upload and ingest already had.
	conv, err := s.convertFile(ctx, req.Collection, formatFromFilename(req.Filename), req.Content, req.TextOnly, images)
	if err != nil {
		return nil, err
	}
	return uploadFileResult(req, conv, func(d convertedDoc) (*MCPDocument, error) {
		var doc *MCPDocument
		err := s.withImageBytes(req.Collection, req.Lang, d, func() (err error) {
			doc, err = c.Add(ctx, &MCPAddRequest{Collection: req.Collection, Key: d.Key, Lang: req.Lang, ContentMD: d.Markdown, Meta: d.Meta})
			return err
		})
		return doc, err
	})
}

// uploadFileResult stores a converted file through add — the file's own
// document first, then what it held (GO-046) — and answers with the
// upload_file tool's result: the file's document, then the keys of the rest.
func uploadFileResult(req *MCPUploadFileRequest, conv conversion, add func(convertedDoc) (*MCPDocument, error)) (map[string]interface{}, error) {
	meta := req.Meta
	if meta == nil {
		meta = map[string][]string{}
	}
	contentMD, converted := conv.Markdown, conv.Converted

	// Extract frontmatter for md/txt; explicitly passed keys win over it.
	if !converted {
		fmMeta, body := parseFrontmatter(contentMD)
		if fmMeta != nil {
			contentMD = body
			for k, v := range fmMeta {
				if _, exists := meta[k]; !exists {
					meta[k] = v
				}
			}
		}
	}

	key := req.Key
	if key == "" {
		key = deriveKeyFromFilename(req.Filename)
	}
	if key == "" {
		return nil, fmt.Errorf("cannot derive key from filename; provide key explicitly")
	}

	meta["upload_format"] = []string{conv.Format}
	meta["upload_filename"] = []string{req.Filename}
	if converted {
		meta["upload_converted"] = []string{"true"}
	}

	conv.Markdown = contentMD
	var result map[string]interface{}
	var parts []string
	for _, d := range convertedDocs(conv, key, meta) {
		doc, err := add(d)
		if err != nil {
			return nil, err
		}
		if result != nil {
			parts = append(parts, d.Key)
			continue
		}
		result = map[string]interface{}{
			"key":       d.Key,
			"format":    d.Format,
			"converted": d.Converted,
			"document":  doc,
		}
		if d.Converted {
			result["conversionConfidence"] = d.Confidence
			result["conversionWarnings"] = d.Warnings
		}
	}
	if len(parts) > 0 {
		result["parts"] = parts
	}
	return result, nil
}
