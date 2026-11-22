import { useState } from 'react';
import { Archive } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';
import ConfirmDialog from './ConfirmDialog';
import HelpPopover from './HelpPopover';

/** What a restore replaces, in the words the card and its dialog use. */
export const RESTORE_WARNING =
  'A restore replaces everything in the database with the backup: every collection, its documents and their history. Whatever was written after the backup was taken is lost.';

/**
 * BackupRestoreCard takes a backup now (/v1/backup — a verified copy next to
 * the database, optionally named) and restores the database from a backup
 * file (/v1/restore) after a confirmation naming the file: the live data is
 * replaced. Both need admin on "*". The Backup & Restore page owns the file
 * to restore (`from`), so a backup picked in its list lands here;
 * `onBackedUp` hears of each backup taken; `disabled` is an account that may
 * not do either.
 */
export default function BackupRestoreCard({ from, onFromChange, onBackedUp, disabled = false }) {
  const [name, setName] = useState('');
  const [confirming, setConfirming] = useState(false);
  const [message, setMessage] = useState(null);
  const { busy, error, run } = useAction();
  const locked = busy || disabled;

  const backup = () => run(async () => {
    const r = await mddbClient.backup(name.trim() || undefined);
    setMessage(`Backup written: ${r.backup}. It is ready to restore below.`);
    onFromChange(r.backup);
    onBackedUp(r.backup);
  });

  const restore = () => run(async () => {
    const r = await mddbClient.restore(from.trim());
    setConfirming(false);
    setMessage(`Database restored from ${r.restored}.`);
  });

  return (
    <section aria-labelledby="backup-now-title" className="bg-surface rounded-lg shadow p-6 space-y-3">
      <h2 id="backup-now-title" className="text-lg font-semibold text-fg flex items-center gap-2">
        <Archive className="w-5 h-5 text-accent" aria-hidden="true" /> Back up and restore
        <HelpPopover label="backups">
          <p>Back up now calls <code className="text-fg-soft">GET /v1/backup?to=&lt;name&gt;</code>; restore calls <code className="text-fg-soft">POST /v1/restore</code>. Both need admin on &quot;*&quot;.</p>
          <p>Backups go to the server&apos;s backup folder, <code className="text-fg-soft">MDDB_BACKUP_DIR</code>. Each copy passes the full integrity check before it is kept (<code className="text-fg-soft">MDDB_BACKUP_VERIFY</code>, on by default); a restore checks the file again and refuses a damaged one.</p>
          <p>In a cluster, followers take a fresh copy from the leader after a restore. See docs/BACKUP.md.</p>
        </HelpPopover>
      </h2>
      <p className="text-sm text-fg-muted">Save a copy of the whole database on the server, or bring the database back to a saved copy.</p>
      <div className="flex flex-wrap gap-3 items-end">
        <label className="text-sm text-fg-soft max-w-full">
          <span className="block mb-1">Backup file name (optional)</span>
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="backup-<timestamp>.db" className="max-w-full px-3 py-2 border border-line-strong rounded-lg" />
        </label>
        <button type="button" onClick={backup} disabled={locked} className="px-3 py-2 text-sm rounded-lg bg-primary-700 text-white hover:bg-primary-800 disabled:opacity-50">Back up now</button>
      </div>
      <p className="text-sm text-fg-muted">{RESTORE_WARNING}</p>
      <div className="flex flex-wrap gap-3 items-end">
        <label className="text-sm text-fg-soft max-w-full">
          <span className="block mb-1">Restore from file</span>
          <input id="restore-from" value={from} onChange={(e) => onFromChange(e.target.value)} className="max-w-full px-3 py-2 border border-line-strong rounded-lg" />
        </label>
        <button type="button" onClick={() => setConfirming(true)} disabled={locked || !from.trim()} className="px-3 py-2 text-sm rounded-lg text-danger-strong border border-danger-line hover:bg-danger-soft disabled:opacity-50">Restore</button>
      </div>
      {message && <p role="status" className="text-sm text-fg">{message}</p>}
      <ApiErrorNotice error={error} admin />
      {confirming && (
        <ConfirmDialog
          title="Restore the database"
          message={<>Restore from <strong>{from.trim()}</strong>? {RESTORE_WARNING}</>}
          confirmLabel="Restore"
          busy={busy}
          onConfirm={restore}
          onCancel={() => setConfirming(false)}
        />
      )}
    </section>
  );
}
