// backup-history (2.19.11): the Backup & Restore page's lists from the cron
// rules and their automation-log runs.
import { describe, test, expect } from 'vitest';
import { backupRules, backupRuns, fileName, isRestorable, takenNow, ruleTarget, ruleKeep } from './backup-history';

describe('backupRules', () => {
  const rules = [
    { id: 'a', type: 'cron', backup: { path: 'nightly' } },
    { id: 'b', type: 'cron', webhookId: 'w' },
    { id: 'c', type: 'trigger', backup: {} },
  ];

  test('keeps the cron rules with a backup, from a list or { rules }', () => {
    expect(backupRules(rules).map((r) => r.id)).toEqual(['a']);
    expect(backupRules({ rules }).map((r) => r.id)).toEqual(['a']);
    expect(backupRules({})).toEqual([]);
    expect(backupRules(undefined)).toEqual([]);
  });
});

describe('backupRuns', () => {
  test('backup runs only, newest first, in plain words', () => {
    const rows = backupRuns([
      { id: '1', action: 'backup', target: '/data/backups/nightly/mddb-1.db', timestamp: 10, status: 'success', ruleId: 'a', ruleName: 'nightly' },
      { id: '2', action: 'webhook', timestamp: 30, status: 'success' },
      { id: '3', action: 'backup', timestamp: 20, status: 'error', error: 'disk full', ruleId: 'a', ruleName: 'nightly' },
      { id: '4', action: 'backup', target: 's3://b/k.db', timestamp: 5, status: 'skipped' },
      { id: '5', action: 'backup', target: 'x.db', timestamp: 1, status: 'odd' },
    ]);
    expect(rows.map((r) => r.id)).toEqual(['3', '1', '4', '5']);
    expect(rows[0]).toMatchObject({ name: '', where: '', state: 'Failed', ok: false, error: 'disk full', ruleName: 'nightly' });
    expect(rows[1]).toMatchObject({ name: 'mddb-1.db', state: 'Stored and checked', ok: true, takenAt: 10, status: 'success' });
    expect(rows[2].state).toBe('Skipped');
    expect(rows[3].state).toBe('odd');
    expect(backupRuns()).toEqual([]);
  });
});

describe('helpers', () => {
  test('fileName is the last part of a path or key', () => {
    expect(fileName('/a/b/c.db')).toBe('c.db');
    expect(fileName('c.db')).toBe('c.db');
    expect(fileName('/')).toBe('/');
    expect(fileName()).toBe('');
  });

  test('only a file on the server disk can be restored directly', () => {
    expect(isRestorable('/data/backups/x.db')).toBe(true);
    expect(isRestorable('x.db')).toBe(true);
    expect(isRestorable('s3://bucket/x.db')).toBe(false);
    expect(isRestorable('')).toBe(false);
  });

  test('takenNow is a stored backup at the current second', () => {
    expect(takenNow('/d/b.db', 5_500)).toEqual({
      id: 'now-5-/d/b.db', where: '/d/b.db', name: 'b.db', takenAt: 5, status: 'success', state: 'Stored and checked', ok: true,
      error: undefined, ruleId: undefined, ruleName: undefined,
    });
    expect(takenNow('a.db').takenAt).toBeGreaterThan(0);
  });

  test('ruleTarget and ruleKeep say where backups go and how many stay', () => {
    expect(ruleTarget({ path: 'nightly' })).toBe('Server disk, folder nightly');
    expect(ruleTarget({})).toBe('Server disk, backup folder');
    expect(ruleTarget({ target: 's3', bucket: 'b1' })).toBe('Object storage, bucket b1');
    expect(ruleTarget({ target: 's3', storageConfig: { bucket: 'b2' } })).toBe('Object storage, bucket b2');
    expect(ruleTarget({ target: 's3' })).toBe('Object storage');
    expect(ruleKeep({ keep: 7 })).toBe('keeps the newest 7');
    expect(ruleKeep({})).toBe('keeps all');
  });
});
