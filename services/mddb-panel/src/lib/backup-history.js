/**
 * The Backup & Restore page's lists (2.19.11), built from what the server
 * already reports: the cron rules with a backup action (GET /v1/automation)
 * and the runs of those rules in the automation log (GET /v1/automation-logs,
 * `action: "backup"`, `target` = where the backup went). The server has no
 * call that lists backup files or serves one for download, so the page shows
 * the backups it knows of: the scheduled runs and the ones taken on the page.
 */

/** The cron rules that take a backup, from GET /v1/automation (a list or `{ rules }`). */
export function backupRules(data) {
  const rules = Array.isArray(data) ? data : data?.rules || [];
  return rules.filter((rule) => rule.type === 'cron' && rule.backup);
}

/** The last part of a path or key: what a person calls the backup. */
export function fileName(where = '') {
  return where.split('/').filter(Boolean).at(-1) || where;
}

/**
 * Whether POST /v1/restore can take `where`: a file on the server's disk.
 * A backup stored elsewhere (`s3://bucket/key`) is fetched back first.
 */
export function isRestorable(where) {
  return Boolean(where) && !/^[a-z][a-z0-9+.-]*:\/\//i.test(where);
}

/** Plain words for a run's state; a stored backup passed its check. */
export const STATE_LABELS = {
  success: 'Stored and checked',
  error: 'Failed',
  skipped: 'Skipped',
};

/** One row of the backup list. */
function row({ id, where, takenAt, status, error, ruleId, ruleName }) {
  return { id, where, name: fileName(where), takenAt, status, state: STATE_LABELS[status] || status, ok: status === 'success', error, ruleId, ruleName };
}

/** The automation log's backup runs as rows, newest first. */
export function backupRuns(logs = []) {
  return logs
    .filter((entry) => entry.action === 'backup')
    .map((entry) => row({ ...entry, where: entry.target || '', takenAt: entry.timestamp }))
    .sort((a, b) => b.takenAt - a.takenAt);
}

/** A backup taken on the page now (GET /v1/backup answers `{ backup }`). */
export function takenNow(where, now = Date.now()) {
  const takenAt = Math.floor(now / 1000);
  return row({ id: `now-${takenAt}-${where}`, where, takenAt, status: 'success' });
}

/** Where a rule's backups go, in plain words. */
export function ruleTarget(backup) {
  if (backup.target === 's3') {
    const bucket = backup.bucket || backup.storageConfig?.bucket;
    return bucket ? `Object storage, bucket ${bucket}` : 'Object storage';
  }
  return backup.path ? `Server disk, folder ${backup.path}` : 'Server disk, backup folder';
}

/** How many backups a rule keeps, in plain words. */
export function ruleKeep(backup) {
  return backup.keep > 0 ? `keeps the newest ${backup.keep}` : 'keeps all';
}
