import { useCallback, useEffect, useState } from 'react';
import mddbClient from './mddb-client';

/** How many backup files the page reads at a time. */
export const BACKUP_FILES_PAGE = 200;

const NONE = { files: null, total: 0, error: null };

/**
 * Whether a failed list means the server has no such call: a release before
 * 2.19.11 answers 404 (405 behind some proxies), and a page server that
 * answers its own HTML instead fails to parse, with no status at all.
 */
function unsupported(error) {
  return !error.status || error.status === 404 || error.status === 405;
}

/**
 * The backup files on the server for the Backup & Restore page:
 * `{ files, total, error, more, reload }`. `files` is null where the server
 * cannot list them (an older release) or nothing was loaded; the page then
 * shows what it did before. `more()` reads the next page, `reload()` starts
 * over. Nothing is loaded while `enabled` is false.
 */
export function useBackupFiles(enabled) {
  const [list, setList] = useState(NONE);
  const [version, setVersion] = useState(0);

  useEffect(() => {
    if (!enabled) return;
    mddbClient.listBackups({ limit: BACKUP_FILES_PAGE }).then(
      (data) => setList({ files: data.backups, total: data.total, error: null }),
      (error) => setList({ ...NONE, error: unsupported(error) ? null : error }),
    );
  }, [enabled, version]);

  const more = useCallback(() => {
    mddbClient.listBackups({ limit: BACKUP_FILES_PAGE, offset: list.files.length }).then(
      (data) => setList((now) => ({ files: [...now.files, ...data.backups], total: data.total, error: null })),
      (error) => setList((now) => ({ ...now, error })),
    );
  }, [list.files]);

  const reload = useCallback(() => setVersion((n) => n + 1), []);

  return { ...list, more, reload };
}
