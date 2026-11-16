// cluster-format: the figures and colours of the Cluster panel (GO-050).
import { describe, test, expect } from 'vitest';
import {
  badgeClass, binlogUse, dotClass, formatBytes, formatEntries, formatLag, formatUptime, lagBarClass, nodeHealth, nodeLag,
} from './cluster-format';

describe('cluster-format', () => {
  test('formats sizes, uptime, lag and entries', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(1536)).toBe('1.5 KB');
    expect(formatBytes(1024 ** 5 * 2)).toBe('2048 TB');
    expect(formatUptime(0)).toBe('-');
    expect(formatUptime(90061)).toBe('1d 1h 1m');
    expect(formatUptime(3700)).toBe('1h 1m');
    expect(formatUptime(120)).toBe('2m');
    expect(formatLag(0)).toBe('0ms');
    expect(formatLag(250)).toBe('250ms');
    expect(formatLag(2500)).toBe('2.5s');
    expect(formatLag(90000, false)).toBe('≥ 1.5m');
    expect(formatEntries(0)).toBe('');
    expect(formatEntries(1)).toBe('1 entry behind');
    expect(formatEntries(2000)).toBe(`${(2000).toLocaleString()} entries behind`);
  });

  test('health is the server verdict', () => {
    expect(nodeHealth(null)).toBe('unknown');
    expect(nodeHealth({ role: 'standalone' })).toBe('standalone');
    expect(nodeHealth({ role: 'leader', healthy: true })).toBe('healthy');
    expect(nodeHealth({ role: 'follower', healthy: false })).toBe('unhealthy');
  });

  test('lag of a follower, of a leader, and of neither', () => {
    expect(nodeLag(null)).toBeNull();
    expect(nodeLag({ role: 'follower' })).toEqual({ ms: 0, entries: 0, exact: true });
    expect(nodeLag({ role: 'follower', replication_lag_ms: 40, lag_entries: 3 })).toEqual({ ms: 40, entries: 3, exact: true });
    expect(nodeLag({ role: 'leader' })).toBeNull();
    expect(nodeLag({ role: 'standalone', followers: [{ lag_ms: 1 }] })).toBeNull();
    const followers = [{ lag_ms: 5, lag_entries: 1 }, { lag_ms: 900, lag_entries: 7, lag_exact: false }, {}];
    expect(nodeLag({ role: 'leader', followers })).toEqual({ ms: 900, entries: 7, exact: false });
    expect(nodeLag({ role: 'leader', followers: [{}] })).toEqual({ ms: 0, entries: 0, exact: true });
    expect(nodeLag({ role: 'leader', followers: [{}, { lag_ms: 5 }] })).toEqual({ ms: 5, entries: 0, exact: true });
  });

  test('binlog use against its limit', () => {
    expect(binlogUse(null)).toBeNull();
    expect(binlogUse({ binlog_max_size_bytes: 0 })).toBeNull();
    expect(binlogUse({ binlog_max_size_bytes: 200 })).toBe(0);
    expect(binlogUse({ binlog_size_bytes: 50, binlog_max_size_bytes: 200 })).toBe(25);
    expect(binlogUse({ binlog_size_bytes: 900, binlog_max_size_bytes: 200 })).toBe(100);
  });

  test('colours come from the theme tokens', () => {
    expect(badgeClass('healthy')).toBe('bg-success-muted text-success-strong');
    expect(badgeClass('leader')).toBe('bg-info-muted text-info-strong');
    expect(badgeClass('candidate')).toBe('bg-warning-muted text-warning-strong');
    expect(badgeClass('disconnected')).toBe('bg-surface-subtle text-fg-soft');
    expect(dotClass('warning')).toBe('bg-warning');
    expect(dotClass('disconnected')).toBe('bg-line-strong');
    expect(lagBarClass(40000)).toBe('bg-danger');
    expect(lagBarClass(2000)).toBe('bg-warning');
    expect(lagBarClass(10)).toBe('bg-success');
  });
});
