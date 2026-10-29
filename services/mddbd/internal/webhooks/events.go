package webhooks

// Incident event names fired on the WebhookManager so operators can subscribe
// with standard /v1/webhooks registrations.
const (
	EventAuthFailureBurst   = "security.auth_failure_burst"
	EventRateLimitExceeded  = "security.rate_limit_exceeded"
	EventReplicationLagHigh = "ops.replication_lag_high"
	EventPanicRecovered     = "ops.panic_recovered"
	EventDiskUsageHigh      = "ops.disk_usage_high"
	// EventBackupFailed: a scheduled backup (OPS-023) did not complete.
	EventBackupFailed = "backup.failed"
)
