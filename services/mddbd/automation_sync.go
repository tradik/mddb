package main

import (
	"cmp"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Synced sources (INT-022): the sync action of a cron rule.
//
// A cron rule with a `sync` block keeps a collection in step with a source on
// its schedule — the pages of a sitemap, a list of URLs, or the objects under
// a prefix of an S3-compatible bucket. The same scheduler that fires webhooks
// and takes backups (OPS-023) runs it; this file holds the action's shape and
// its validation, sync_run.go the run, sync_http.go and sync_s3.go the
// sources.

// Sync sources.
const (
	SyncSourceSitemap = "sitemap"
	SyncSourceURLs    = "urls"
	SyncSourceS3      = "s3"
)

// Bounds of a run. A rule may lower them; the caps are what it may raise
// them to.
const (
	defaultSyncMaxDocuments = 1000
	maxSyncMaxDocuments     = 50000
	defaultSyncMaxBytes     = 10 << 20  // per document, as /v1/upload
	maxSyncMaxBytes         = 100 << 20 // the upload hard cap
	// maxSyncKeyLength keeps a derived key well inside what bbolt and the
	// clients that write a file per key accept.
	maxSyncKeyLength = 200
)

// Metadata a synced document carries: which rule owns it and where it came
// from — the mapping that makes a prune exact — and the validators the next
// run compares.
const (
	syncMetaRule         = "syncRule"
	syncMetaSource       = "syncSource"
	syncMetaHash         = "syncHash"
	syncMetaETag         = "syncEtag"
	syncMetaLastModified = "syncLastModified"
)

// SyncAction is what a cron rule with a sync does on each run.
type SyncAction struct {
	// Source is "sitemap", "urls" or "s3".
	Source string `json:"source"`
	// Collection and Lang are where the documents go.
	Collection string `json:"collection"`
	Lang       string `json:"lang"`
	// URL is the sitemap (or sitemap index) of a sitemap source.
	URL string `json:"url,omitempty"`
	// URLs are the pages of a urls source.
	URLs []string `json:"urls,omitempty"`
	// Bucket and Prefix select the objects of an s3 source; an empty bucket
	// takes storageConfig.bucket, an empty prefix storageConfig.prefix.
	Bucket string `json:"bucket,omitempty"`
	Prefix string `json:"prefix,omitempty"`
	// StorageConfig holds the S3 endpoint, region, credentials and TLS flag —
	// the block an S3-backed collection and an S3 backup are configured with.
	// Reads blank its secretKey (secretKeySet tells it is there).
	StorageConfig *StorageConfigDef `json:"storageConfig,omitempty"`
	// Prune deletes the documents of this rule whose source is gone: no
	// longer listed, or answering 404/410. Off by default.
	Prune bool `json:"prune,omitempty"`
	// MaxDocuments bounds the sources one run looks at (default 1000, at most
	// 50000). A source listing more is synced up to the bound and not pruned.
	MaxDocuments int `json:"maxDocuments,omitempty"`
	// MaxBytes bounds one fetched document (default 10 MiB, at most 100 MiB).
	MaxBytes int64 `json:"maxBytes,omitempty"`
}

// normalize fills the defaults and rejects what cannot run.
func (a *SyncAction) normalize() error {
	a.Source = strings.TrimSpace(a.Source)
	a.Collection = strings.TrimSpace(a.Collection)
	a.Lang = strings.TrimSpace(a.Lang)
	if a.Collection == "" || a.Lang == "" {
		return errors.New("a sync needs collection and lang")
	}
	if a.MaxDocuments < 0 || a.MaxDocuments > maxSyncMaxDocuments {
		return fmt.Errorf("sync maxDocuments must be 0 (default %d) to %d", defaultSyncMaxDocuments, maxSyncMaxDocuments)
	}
	if a.MaxBytes < 0 || a.MaxBytes > maxSyncMaxBytes {
		return fmt.Errorf("sync maxBytes must be 0 (default %d) to %d", defaultSyncMaxBytes, maxSyncMaxBytes)
	}
	a.MaxDocuments = cmp.Or(a.MaxDocuments, defaultSyncMaxDocuments)
	a.MaxBytes = cmp.Or(a.MaxBytes, defaultSyncMaxBytes)

	switch a.Source {
	case SyncSourceSitemap:
		return checkSyncURL(a.URL)
	case SyncSourceURLs:
		if len(a.URLs) == 0 {
			return errors.New("a urls sync needs urls")
		}
		for _, u := range a.URLs {
			if err := checkSyncURL(u); err != nil {
				return err
			}
		}
	case SyncSourceS3:
		if a.StorageConfig != nil {
			a.Bucket = cmp.Or(strings.TrimSpace(a.Bucket), a.StorageConfig.Bucket)
			a.Prefix = cmp.Or(a.Prefix, a.StorageConfig.Prefix)
			a.StorageConfig.SecretKeySet = false // output-only (GO-035)
		}
		if a.StorageConfig == nil || a.StorageConfig.Endpoint == "" || a.Bucket == "" {
			return errors.New("an s3 sync needs storageConfig.endpoint and a bucket")
		}
	default:
		return fmt.Errorf("invalid sync source: %q (valid: sitemap, urls, s3)", a.Source)
	}
	return nil
}

// checkSyncURL accepts an absolute http(s) URL. Where it may point is decided
// when it is fetched, by the SSRF guard: a name resolves differently over
// time, and a check here would only pass for the address of the moment.
func checkSyncURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("sync url %q: an absolute http or https URL is required", raw)
	}
	return nil
}

// describe names the source for logs and the webhook: the sitemap, the
// number of URLs, or s3://bucket/prefix.
func (a *SyncAction) describe() string {
	switch a.Source {
	case SyncSourceSitemap:
		return a.URL
	case SyncSourceURLs:
		return fmt.Sprintf("%d urls", len(a.URLs))
	}
	return "s3://" + a.Bucket + "/" + a.Prefix
}

// syncKeyUnsafe is what a derived key may not contain.
var syncKeyUnsafe = regexp.MustCompile(`[^a-z0-9._-]+`)

// syncKey derives a document key from the path of a source — a URL's path or
// an object key below the prefix: lower case, the last segment's extension
// dropped, every run of other characters (the slashes included) one '-'.
// "/blog/Post 1.html" is "blog-post-1"; the root is "index".
func syncKey(p string) string {
	p = strings.Trim(p, "/")
	p = strings.TrimSuffix(p, path.Ext(p))
	key := strings.Trim(syncKeyUnsafe.ReplaceAllString(strings.ToLower(p), "-"), "-.")
	if len(key) > maxSyncKeyLength {
		key = strings.TrimRight(key[:maxSyncKeyLength], "-.")
	}
	return cmp.Or(key, "index")
}

// redactedSync returns a copy of a sync action safe to hand a client.
func redactedSync(a *SyncAction) *SyncAction {
	if a == nil {
		return nil
	}
	c := *a
	c.URLs = slices.Clone(a.URLs)
	c.StorageConfig = redactedStorageConfig(a.StorageConfig)
	return &c
}

// redactedStorageConfig returns a copy of sc with the secret key blanked and
// secretKeySet set (GO-035); nil stays nil.
func redactedStorageConfig(sc *StorageConfigDef) *StorageConfigDef {
	if sc == nil {
		return nil
	}
	c := *sc
	c.SecretKeySet = c.SecretKey != ""
	c.SecretKey = ""
	return &c
}

// keepStoredSecret fills an update's blank secret key from the stored config.
func keepStoredSecret(update, stored *StorageConfigDef) {
	if update != nil && update.SecretKey == "" && stored != nil {
		update.SecretKey = stored.SecretKey
	}
}

// carryOverAction keeps what a client could not send back: the stored secret
// keys when an update has none, and the whole stored action when an update
// names none — a client that does not know the fields (the MCP tools, an
// older gRPC client) toggling `enabled`.
func carryOverAction(update *AutomationRule, existing *AutomationRule) {
	if update.WebhookID == "" && update.Backup == nil && update.Sync == nil {
		if existing.Backup != nil {
			b := *existing.Backup
			update.Backup = &b
		}
		if existing.Sync != nil {
			s := *existing.Sync
			update.Sync = &s
		}
		return
	}
	if update.Backup != nil && existing.Backup != nil {
		keepStoredSecret(update.Backup.StorageConfig, existing.Backup.StorageConfig)
	}
	if update.Sync != nil && existing.Sync != nil {
		keepStoredSecret(update.Sync.StorageConfig, existing.Sync.StorageConfig)
	}
}
