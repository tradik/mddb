// backup-files (2.19.11): the server's backup files as rows, merged with
// the scheduled runs and the backups taken on the page.
import { describe, test, expect } from 'vitest';
import { fileRow, mergeBackupRows, FILE_STATE_LABELS } from './backup-files';
import { takenNow } from './backup-history';

const nightly = { name: 'mddb-1.db', path: '/data/backups/mddb-1.db', bytes: 2048, modifiedAt: '1970-01-01T00:01:40Z', state: 'checksummed', collectionFiles: 2 };
const manual = { name: 'snap.db', path: '/data/backups/snap.db', bytes: 10, modifiedAt: '1970-01-01T00:00:50Z', state: 'unknown' };

describe('fileRow', () => {
  test('a checksummed file is stored and checked; any other claims nothing', () => {
    expect(fileRow(nightly)).toEqual({
      id: 'file-/data/backups/mddb-1.db', where: '/data/backups/mddb-1.db', name: 'mddb-1.db', takenAt: 100,
      status: 'success', state: 'Stored and checked', ok: true, bytes: 2048, collectionFiles: 2,
    });
    const row = fileRow(manual);
    expect([row.status, row.state, row.collectionFiles]).toEqual(['listed', FILE_STATE_LABELS.unknown, 0]);
  });

  test('without a path or a readable date it still has a row', () => {
    const row = fileRow({ name: 'x.db', state: 'odd', modifiedAt: 'never' });
    expect([row.id, row.where, row.takenAt, row.state]).toEqual(['file-x.db', 'x.db', 0, 'On the server']);
  });
});

describe('mergeBackupRows', () => {
  test('a run or a backup taken here that wrote a listed file is that row', () => {
    const runs = [
      { id: 'r1', where: '/data/backups/snap.db', ruleName: 'nightly', status: 'success', takenAt: 60 },
      { id: 'r2', where: '/data/backups/nightly/mddb-0.db', ruleName: 'nightly', status: 'success', takenAt: 30 },
      { id: 'r3', where: '/data/backups/mddb-1.db', status: 'skipped', takenAt: 99 },
      takenNow('/data/backups/mddb-1.db', 200_000),
    ];
    const rows = mergeBackupRows([manual, nightly], runs);
    expect(rows.map((r) => r.id)).toEqual(['file-/data/backups/mddb-1.db', 'file-/data/backups/snap.db', 'r2']);
    expect(rows[1]).toMatchObject({ ruleName: 'nightly', status: 'success', state: 'Stored and checked', bytes: 10 });
    expect(rows[0].ruleName).toBeUndefined();
  });

  test('without listed files the rows stay as they are', () => {
    const runs = [{ id: 'a', where: 'x', takenAt: 1 }, { id: 'b', where: 'y', takenAt: 2 }];
    expect(mergeBackupRows([], runs).map((r) => r.id)).toEqual(['b', 'a']);
  });
});
