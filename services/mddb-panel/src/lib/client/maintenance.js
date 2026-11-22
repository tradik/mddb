/**
 * Collection and database maintenance the client did not cover yet: a
 * collection's checksum (compare it across replicas), restoring the
 * database from a backup file, and the backup files themselves (2.19.11):
 * the list of MDDB_BACKUP_DIR and one file as a download. Backup, export and
 * revision truncation are the client's existing `backup`, `export` and
 * `truncate`.
 */
import { postJson, getWithQuery } from './http';

export const maintenanceMethods = {
  /** `{ collection, checksum, documentCount }`. */
  checksum(collection) {
    return getWithQuery(this, '/checksum', { collection });
  },

  /** Replaces the live database with the backup file `from` (admin on "*"). */
  restore(from) {
    return postJson(this, '/restore', { from });
  },

  /**
   * The backup files on the server, newest first:
   * `{ backups: [{ name, path, bytes, modifiedAt, state, collectionFiles }], total, limit, offset, note }`.
   * A server before 2.19.11 answers 404.
   */
  listBackups({ limit, offset } = {}) {
    return getWithQuery(this, '/backups', { limit, offset });
  },

  /** One backup file as `{ blob, filename }` (admin on "*"). */
  downloadBackup(name) {
    return this.requestBlob(`/backups/${encodeURIComponent(name)}/download`);
  },
};
