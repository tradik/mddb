package webhooks

// Incident event names fired on the WebhookManager so operators can subscribe
// with standard /v1/webhooks registrations.
const (
	EventAuthFailureBurst   = "security.auth_failure_burst"
	EventRateLimitExceeded  = "security.rate_limit_exceeded"
	EventReplicationLagHigh = "ops.replication_lag_high"
	EventPanicRecovered     = "ops.panic_recovered"
	EventDiskUsageHigh      = "ops.disk_usage_high"
	// EventBackupFailed is a scheduled backup (OPS-023) that did not complete.
	EventBackupFailed = "backup.failed"
	// EventQuotaExceeded is a write or embedding refused by a collection
	// quota, the disk reserve or the vector RAM limit (GO-043).
	EventQuotaExceeded = "quota.exceeded"
	// EventSourceSynced is a finished run of a synced source (INT-022),
	// with what it added, updated, deleted and failed on.
	EventSourceSynced = "source.synced"
)
