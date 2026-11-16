// What the Cluster panel shows, from GET /v1/replication/status (GO-050).
//
// The panel used to work out health from replication_lag_ms, which the
// server never filled in. The server now measures it, and says itself
// whether the node is healthy and why not, so the panel shows that.

/** Bytes as a short human figure. */
export function formatBytes(bytes) {
  if (!bytes) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), sizes.length - 1);
  return `${Math.round((bytes / Math.pow(k, i)) * 100) / 100} ${sizes[i]}`;
}

/** Seconds of uptime as days, hours and minutes. */
export function formatUptime(seconds) {
  if (!seconds) return '-';
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h ${minutes}m`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}

/** Milliseconds of lag; inexact marks a lower bound. */
export function formatLag(ms, exact = true) {
  const prefix = exact ? '' : '≥ ';
  if (!ms) return `${prefix}0ms`;
  if (ms < 1000) return `${prefix}${ms}ms`;
  if (ms < 60000) return `${prefix}${(ms / 1000).toFixed(1)}s`;
  return `${prefix}${(ms / 60000).toFixed(1)}m`;
}

/** Entries behind, when there are any. */
export function formatEntries(n) {
  if (!n) return '';
  return `${n.toLocaleString()} ${n === 1 ? 'entry' : 'entries'} behind`;
}

/** The node's own health: the server's verdict, "standalone" without a role. */
export function nodeHealth(status) {
  if (!status) return 'unknown';
  if (status.role === 'standalone') return 'standalone';
  return status.healthy ? 'healthy' : 'unhealthy';
}

/**
 * The lag the node's card and history show: a follower's own, a leader's
 * furthest-behind follower, nothing on a standalone node.
 */
export function nodeLag(status) {
  if (!status) return null;
  if (status.role === 'follower') {
    return { ms: status.replication_lag_ms || 0, entries: status.lag_entries || 0, exact: true };
  }
  const followers = status.followers || [];
  if (status.role !== 'leader' || followers.length === 0) return null;
  const worst = followers.reduce((a, b) => ((b.lag_ms || 0) > (a.lag_ms || 0) ? b : a));
  return { ms: worst.lag_ms || 0, entries: worst.lag_entries || 0, exact: worst.lag_exact !== false };
}

/** Share of the binlog's size limit in use, 0–100, or null without a limit. */
export function binlogUse(status) {
  const max = status?.binlog_max_size_bytes;
  if (!max) return null;
  return Math.min(100, Math.round(((status.binlog_size_bytes || 0) / max) * 100));
}

const badges = {
  healthy: 'bg-success-muted text-success-strong',
  warning: 'bg-warning-muted text-warning-strong',
  unhealthy: 'bg-danger-muted text-danger-strong',
  leader: 'bg-info-muted text-info-strong',
  follower: 'bg-note-muted text-note-strong',
  candidate: 'bg-warning-muted text-warning-strong', // GO-051: an election in progress
};

/** Badge colours for a status or a role, in the theme's tokens. */
export function badgeClass(name) {
  return badges[name] || 'bg-surface-subtle text-fg-soft';
}

const dots = { healthy: 'bg-success', warning: 'bg-warning', unhealthy: 'bg-danger' };

/** The dot beside a status. */
export function dotClass(name) {
  return dots[name] || 'bg-line-strong';
}

/** The bar colour for a lag in the history chart. */
export function lagBarClass(ms) {
  if (ms > 30000) return 'bg-danger';
  if (ms > 1000) return 'bg-warning';
  return 'bg-success';
}
