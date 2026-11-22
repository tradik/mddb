/**
 * The backup files the server lists (GET /v1/backups, 2.19.11) as rows of
 * the Backup & Restore page's list, merged with the rows the page builds
 * from the automation log and the backups taken on the page
 * (lib/backup-history.js). A scheduled run or a backup taken here that wrote
 * a listed file is that file's row, not a second one: both carry the file's
 * full path on the server (`path`, the run's `target`, GET /v1/backup's
 * `backup`), so the path is what they are matched on.
 */
import { STATE_LABELS } from './backup-history';

/**
 * Plain words for a listed file. A file with a checksum beside it was
 * written by a scheduled run that checked it; of any other the server keeps
 * no record, so the page claims nothing.
 */
export const FILE_STATE_LABELS = {
  checksummed: STATE_LABELS.success,
  unknown: 'On the server',
};

/** A listed backup file as a row of the list. */
export function fileRow(file) {
  const checked = file.state === 'checksummed';
  return {
    id: `file-${file.path || file.name}`,
    where: file.path || file.name,
    name: file.name,
    takenAt: Math.floor(Date.parse(file.modifiedAt) / 1000) || 0,
    status: checked ? 'success' : 'listed',
    state: FILE_STATE_LABELS[file.state] || FILE_STATE_LABELS.unknown,
    ok: true,
    bytes: file.bytes,
    collectionFiles: file.collectionFiles || 0,
  };
}

/**
 * The listed files and the page's other rows as one list, newest first. A
 * row whose backup is a listed file joins that file's row: it adds its rule's
 * name, and a run that stored and checked the backup says so.
 */
export function mergeBackupRows(files, others) {
  const rows = files.map(fileRow);
  const byPath = new Map(rows.map((row) => [row.where, row]));
  const rest = others.filter((other) => {
    const file = byPath.get(other.where);
    if (!file) return true;
    file.ruleName ||= other.ruleName;
    if (other.status === 'success') {
      file.status = 'success';
      file.state = STATE_LABELS.success;
    }
    return false;
  });
  return [...rows, ...rest].sort((a, b) => b.takenAt - a.takenAt);
}
