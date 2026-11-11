/**
 * Collection and database maintenance the client did not cover yet: a
 * collection's CRC32 checksum (compare it across replicas) and restoring the
 * database from a backup file. Backup, export and revision truncation are
 * the client's existing `backup`, `export` and `truncate`.
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
};
