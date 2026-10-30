package main

import (
	"context"
	"errors"
	"mime"
	"net/url"
)

// What every source of a sync (INT-022) provides: a listing and a fetch that
// skips what has not changed. sync_http.go and sync_s3.go implement it.

// errSourceGone is a source that answers 404 or 410, or an object that has
// vanished between the listing and the fetch.
var errSourceGone = errors.New("the source is gone")

// syncItem is one source a run looks at.
type syncItem struct {
	ref  string // the URL or object key, kept as the document's syncSource
	key  string // the document key derived from it
	etag string // an S3 listing's ETag; empty for a page
	size int64  // an S3 listing's size; 0 when unknown
}

// syncState is what the previous run left on a document: its validators.
type syncState struct{ etag, lastModified, hash string }

// syncFetched is one fetched source.
type syncFetched struct {
	notModified bool
	body        []byte
	etag        string
	modified    string
	contentType string
}

// syncSource is where a sync reads from: a sitemap or URL list over HTTP(S),
// or an S3 prefix.
type syncSource interface {
	// list returns the sources up to the action's maxDocuments, and whether
	// there were more.
	list(ctx context.Context) ([]syncItem, bool, error)
	// fetch reads one source unless prev shows it unchanged.
	fetch(ctx context.Context, it syncItem, prev syncState) (syncFetched, error)
}

// newSyncSource connects to the source an action names.
func newSyncSource(a *SyncAction) (syncSource, error) {
	if a.Source == SyncSourceS3 {
		return newS3SyncSource(a)
	}
	return &httpSyncSource{action: a}, nil
}

// syncContentTypes maps the media types a source names to the converter's
// formats; anything else falls back to the extension of the source's path.
var syncContentTypes = map[string]string{
	"text/html": "html", "application/xhtml+xml": "html",
	"text/markdown": "md", "text/x-markdown": "md", "text/plain": "txt",
	"text/csv": "csv", "application/pdf": "pdf",
}

// syncFormat names the format of a fetched source: its Content-Type when the
// converter knows it, else the extension of its path. The converter itself
// still recognises PDF and the zip formats by their content.
func syncFormat(contentType, ref string) string {
	mt, _, _ := mime.ParseMediaType(contentType)
	if f, ok := syncContentTypes[mt]; ok {
		return f
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return formatFromFilename(u.Path)
}
