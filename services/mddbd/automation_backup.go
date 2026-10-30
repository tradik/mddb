package main

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Scheduled backups (OPS-023): the backup action of a cron rule.
//
// A cron rule used to do one thing — fire a webhook. With a `backup` block
// instead of a `webhookId` it takes a backup on its schedule, through the same
// code as GET /v1/backup (backupTo), and keeps the newest `keep` of them on
// its target: a directory inside MDDB_BACKUP_DIR, or an S3-compatible bucket
// reached through the same StorageConfigDef an S3-backed collection uses.

// Backup targets.
const (
	BackupTargetLocal = "local"
	BackupTargetS3    = "s3"
)

// defaultBackupPrefix starts a scheduled backup's name when the rule gives none.
const defaultBackupPrefix = "mddb-"

// backupStampLayout is the UTC time in a scheduled backup's name. It sorts
// lexically in time order, which is what the retention relies on.
const backupStampLayout = "20060102T150405Z"

// BackupAction is what a cron rule with a backup does on each run.
type BackupAction struct {
	// Target is "local" (default) or "s3".
	Target string `json:"target,omitempty"`
	// Path is the directory, inside MDDB_BACKUP_DIR, a local backup goes to.
	// Empty means MDDB_BACKUP_DIR itself.
	Path string `json:"path,omitempty"`
	// Bucket is the S3 bucket; empty takes storageConfig.bucket.
	Bucket string `json:"bucket,omitempty"`
	// Prefix starts every backup's name (local) or key (S3); retention only
	// ever looks at names that start with it. Empty takes storageConfig.prefix,
	// then "mddb-".
	Prefix string `json:"prefix,omitempty"`
	// Keep is how many backups the target keeps; 0 keeps all of them.
	Keep int `json:"keep,omitempty"`
	// Collections is ["*"] (default: everything) or the collections whose
	// files go into the backup — a subset needs the per-collection layout.
	Collections []string `json:"collections,omitempty"`
	// StorageConfig holds the S3 endpoint, region, credentials and TLS flag,
	// the same block an S3-backed collection is configured with. Reads blank
	// its secretKey (secretKeySet tells it is there); an update without one
	// keeps the stored key.
	StorageConfig *StorageConfigDef `json:"storageConfig,omitempty"`
}

// backupPrefixPattern is what a prefix may contain: a file-name prefix
// locally, and "/" as well in S3, where it groups keys like a directory.
var backupPrefixPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]*$`)

// normalize fills the defaults and rejects what cannot run.
func (a *BackupAction) normalize() error {
	a.Target = cmp.Or(strings.TrimSpace(a.Target), BackupTargetLocal)
	a.Path = strings.TrimSpace(a.Path)
	a.Bucket = strings.TrimSpace(a.Bucket)
	if a.StorageConfig != nil {
		a.Bucket = cmp.Or(a.Bucket, a.StorageConfig.Bucket)
		a.Prefix = cmp.Or(a.Prefix, a.StorageConfig.Prefix)
		a.StorageConfig.SecretKeySet = false // output-only (GO-035)
	}
	a.Prefix = cmp.Or(strings.TrimSpace(a.Prefix), defaultBackupPrefix)
	if !backupPrefixPattern.MatchString(a.Prefix) || strings.Contains(a.Prefix, "..") {
		return fmt.Errorf("backup prefix %q: only letters, digits, '.', '_', '-' and '/'", a.Prefix)
	}
	if a.Keep < 0 {
		return errors.New("backup keep must be 0 (keep all) or more")
	}
	if len(a.Collections) == 0 {
		a.Collections = []string{"*"}
	}
	if slices.Contains(a.Collections, "*") && len(a.Collections) > 1 {
		return errors.New(`backup collections: "*" stands alone`)
	}

	switch a.Target {
	case BackupTargetLocal:
		if strings.Contains(a.Prefix, "/") {
			return errors.New("a local backup prefix names files: no '/' (put the directory in path)")
		}
		if a.Path != "" && !filepath.IsLocal(a.Path) {
			return fmt.Errorf("backup path %q must be a relative path inside MDDB_BACKUP_DIR", a.Path)
		}
	case BackupTargetS3:
		if a.StorageConfig == nil || a.StorageConfig.Endpoint == "" || a.Bucket == "" {
			return errors.New("an s3 backup needs storageConfig.endpoint and a bucket")
		}
	default:
		return fmt.Errorf("invalid backup target: %s (valid: local, s3)", a.Target)
	}
	return nil
}

// allCollections reports whether the backup takes every collection.
func (a *BackupAction) allCollections() bool {
	return slices.Contains(a.Collections, "*")
}

// name is the backup's name for a run at now: prefix, UTC stamp, ".db".
func (a *BackupAction) name(now time.Time) string {
	return a.Prefix + now.UTC().Format(backupStampLayout) + ".db"
}

// isBackupName reports whether name is one of this action's backups — the only
// names its retention may delete.
func (a *BackupAction) isBackupName(name string) bool {
	rest, ok := strings.CutPrefix(name, a.Prefix)
	if !ok {
		return false
	}
	stamp, ok := strings.CutSuffix(rest, ".db")
	if !ok {
		return false
	}
	_, err := time.Parse(backupStampLayout, stamp)
	return err == nil
}

// redacted returns a copy of the rule safe to hand a client: the S3 secret
// keys of its backup or sync blanked and secretKeySet set (GO-035). The
// stored rule is not changed.
func (r AutomationRule) redacted() AutomationRule {
	if r.Backup != nil {
		b := *r.Backup
		b.Collections = slices.Clone(b.Collections)
		b.StorageConfig = redactedStorageConfig(b.StorageConfig)
		r.Backup = &b
	}
	r.Sync = redactedSync(r.Sync)
	return r
}

// validateCronAction checks that a cron rule does exactly one thing: fire an
// existing webhook, take a backup, or sync a source (INT-022).
func (am *AutomationManager) validateCronAction(rule *AutomationRule) error {
	actions := 0
	for _, set := range []bool{rule.WebhookID != "", rule.Backup != nil, rule.Sync != nil} {
		if set {
			actions++
		}
	}
	switch {
	case actions > 1:
		return errors.New("a cron rule takes one of webhookId, backup and sync")
	case rule.Backup != nil:
		return rule.Backup.normalize()
	case rule.Sync != nil:
		return rule.Sync.normalize()
	case rule.WebhookID == "":
		return errors.New("webhookId, backup or sync is required for cron")
	case am.GetWebhook(rule.WebhookID) == nil:
		return fmt.Errorf("webhook not found: %s", rule.WebhookID)
	}
	return nil
}
