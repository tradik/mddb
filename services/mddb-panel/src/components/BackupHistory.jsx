import { History } from 'lucide-react';
import { formatUnix } from '../lib/time-format';
import { isRestorable } from '../lib/backup-history';
import HelpPopover from './HelpPopover';

const STATE_CLASS = {
  success: 'bg-success-soft text-success-strong',
  error: 'bg-danger-soft text-danger-strong',
};
const OTHER_STATE = 'bg-surface-subtle text-fg-soft';

/**
 * BackupHistory lists the backups the page knows of (lib/backup-history.js):
 * the runs of the scheduled backups and the ones taken on the page. A backup
 * on the server's disk can be picked for the restore field (`onPick`); one stored
 * elsewhere is fetched back by hand first. `canRestore` is false for an
 * account that may not restore.
 */
export default function BackupHistory({ rows, canRestore, onPick }) {
  return (
    <section aria-labelledby="backup-history-title" className="bg-surface rounded-lg shadow">
      <div className="p-4 space-y-1">
        <h2 id="backup-history-title" className="text-lg font-semibold text-fg flex items-center gap-2">
          <History className="w-5 h-5 text-accent" aria-hidden="true" /> Backups
          <HelpPopover label="the backup list">
            <p>The list holds the runs of scheduled backups from the automation log (<code className="text-fg-soft">GET /v1/automation-logs</code>) and the backups taken on this page since it opened.</p>
            <p>The server has no call that lists the files of its backup folder, reports their size or serves one for download: a backup taken another way (the CLI, MCP, a script) is not listed. Copy files off the server from <code className="text-fg-soft">MDDB_BACKUP_DIR</code>, and check one by hand with <code className="text-fg-soft">mddbd -verify-db</code>.</p>
          </HelpPopover>
        </h2>
        {rows.length === 0 && <p className="text-sm text-fg-muted">No backups yet. Take one above, or schedule them below.</p>}
      </div>
      {rows.length > 0 && (
        <div className="overflow-x-auto">
          <table className="min-w-full text-sm" aria-label="Backups">
            <thead>
              <tr className="text-left text-fg-soft">
                <th scope="col" className="px-4 py-2">Backup</th>
                <th scope="col" className="px-4 py-2">Taken</th>
                <th scope="col" className="px-4 py-2">State</th>
                <th scope="col" className="px-4 py-2"><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line-soft">
              {rows.map((row) => (
                <tr key={row.id}>
                  <td className="px-4 py-2 text-fg">
                    <span className="font-medium break-all">{row.name || '—'}</span>
                    {row.ruleName && <span className="block text-xs text-fg-muted">Scheduled: {row.ruleName}</span>}
                  </td>
                  <td className="px-4 py-2 text-fg-soft whitespace-nowrap">{formatUnix(row.takenAt)}</td>
                  <td className="px-4 py-2">
                    <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${STATE_CLASS[row.status] || OTHER_STATE}`}>{row.state}</span>
                    {row.error && <span className="block text-xs text-danger-strong">{row.error}</span>}
                  </td>
                  <td className="px-4 py-2">
                    {canRestore && row.ok && isRestorable(row.where) && (
                      <button type="button" onClick={() => onPick(row.where)} aria-label={`Use ${row.name} for restore`} className="px-2 py-1 text-xs rounded border border-line-strong text-fg hover:bg-surface-subtle whitespace-nowrap">
                        Use for restore
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
