import { useState } from 'react';
import { Archive } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';
import ConfirmDialog from './ConfirmDialog';

/**
 * BackupRestoreCard takes a backup now (/v1/backup — a verified copy next to
 * the database, optionally named) and restores the database from a backup
 * file (/v1/restore) after a confirmation naming the file: the live data is
 * replaced. Both need admin on "*".
 */
export default function BackupRestoreCard() {
  const [name, setName] = useState('');
  const [from, setFrom] = useState('');
  const [confirming, setConfirming] = useState(false);
  const [message, setMessage] = useState(null);
  const { busy, error, run } = useAction();

  const backup = () => run(async () => {
    const r = await mddbClient.backup(name.trim() || undefined);
    setMessage(`Backup written: ${r.backup}. Restore takes this file name.`);
    setFrom(r.backup);
  });

  const restore = () => run(async () => {
    const r = await mddbClient.restore(from.trim());
    setConfirming(false);
    setMessage(`Database restored from ${r.restored}.`);
  });

  return (
    <div className="bg-white rounded-lg shadow mb-6 p-6 space-y-3">
      <h2 className="text-lg font-semibold text-gray-900 flex items-center gap-2">
        <Archive className="w-5 h-5 text-primary-600" aria-hidden="true" /> Backup and restore
      </h2>
      <p className="text-sm text-gray-700">Backups are written next to the database and checked for integrity before they are kept. Needs admin on &quot;*&quot;.</p>
      <div className="flex flex-wrap gap-3 items-end">
        <label className="text-sm text-gray-800">
          <span className="block mb-1">Backup file name (optional)</span>
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="backup-<timestamp>.db" className="px-3 py-2 border border-gray-300 rounded-lg" />
        </label>
        <button type="button" onClick={backup} disabled={busy} className="px-3 py-2 text-sm rounded-lg bg-primary-700 text-white hover:bg-primary-800 disabled:opacity-50">Back up now</button>
        <label className="text-sm text-gray-800">
          <span className="block mb-1">Restore from file</span>
          <input value={from} onChange={(e) => setFrom(e.target.value)} className="px-3 py-2 border border-gray-300 rounded-lg" />
        </label>
        <button type="button" onClick={() => setConfirming(true)} disabled={busy || !from.trim()} className="px-3 py-2 text-sm rounded-lg text-red-800 border border-red-300 hover:bg-red-50 disabled:opacity-50">Restore</button>
      </div>
      {message && <p role="status" className="text-sm text-gray-900">{message}</p>}
      <ApiErrorNotice error={error} admin />
      {confirming && (
        <ConfirmDialog
          title="Restore the database"
          message={<>Replace all current data with the backup <strong>{from.trim()}</strong>? Every collection is affected; followers re-snapshot.</>}
          confirmLabel="Restore"
          busy={busy}
          onConfirm={restore}
          onCancel={() => setConfirming(false)}
        />
      )}
    </div>
  );
}
