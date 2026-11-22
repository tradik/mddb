import { History } from 'lucide-react';
import { formatUnix } from '../lib/time-format';
import { formatBytes } from '../lib/storage-format';
import { isRestorable } from '../lib/backup-history';
import HelpPopover from './HelpPopover';

const STATE_CLASS = {
  success: 'bg-success-soft text-success-strong',
  error: 'bg-danger-soft text-danger-strong',
};
const OTHER_STATE = 'bg-surface-subtle text-fg-soft';
const BUTTON = 'px-2 py-1 text-xs rounded border border-line-strong text-fg hover:bg-surface-subtle whitespace-nowrap disabled:opacity-50';

/** The size of a listed file, and the collection files that stay on the server. */
function Size({ row }) {
  if (row.bytes === undefined) return '—';
  return (
    <>
      {formatBytes(row.bytes)}
      {row.collectionFiles > 0 && <span className="block text-xs text-fg-muted">plus {row.collectionFiles} collection files, kept on the server</span>}
    </>
  );
}

/** What the list holds, behind the help button: it depends on whether the server lists its backup files. */
function ListHelp({ listed }) {
  if (listed) {
    return (
      <HelpPopover label="the backup list">
        <p>The list holds the backup files in the server&apos;s backup folder, <code className="text-fg-soft">MDDB_BACKUP_DIR</code> (<code className="text-fg-soft">GET /v1/backups</code>), with the runs of scheduled backups from the automation log. A run that wrote a listed file shares its row.</p>
        <p>Only <code className="text-fg-soft">.db</code> files in the folder itself are listed: not backups in a folder inside it, nor those a scheduled backup stores in object storage. &quot;Stored and checked&quot; means a checksum file lies beside the backup; the list never checks a file again, a restore does.</p>
        <p>Download sends the main file (<code className="text-fg-soft">GET /v1/backups/&lt;name&gt;/download</code>); in the per-collection layout the collection files in <code className="text-fg-soft">&lt;name&gt;.d/</code> stay on the server. Every download is written to the audit log. See docs/BACKUP.md.</p>
      </HelpPopover>
    );
  }
  return (
    <HelpPopover label="the backup list">
      <p>The list holds the runs of scheduled backups from the automation log (<code className="text-fg-soft">GET /v1/automation-logs</code>) and the backups taken on this page since it opened.</p>
      <p>This server has no call that lists the files of its backup folder, reports their size or serves one for download (it came in 2.19.11): a backup taken another way (the CLI, MCP, a script) is not listed. Copy files off the server from <code className="text-fg-soft">MDDB_BACKUP_DIR</code>, and check one by hand with <code className="text-fg-soft">mddbd -verify-db</code>.</p>
    </HelpPopover>
  );
}

/**
 * BackupHistory lists the backups the page knows of: the backup files the
 * server lists (lib/backup-files.js) merged with the runs of the scheduled
 * backups and the ones taken on the page (lib/backup-history.js). A listed
 * file can be downloaded (`onDownload`; `downloading` while one is on its
 * way); a backup on the server's disk can be picked for the restore field
 * (`onPick`); one stored elsewhere is fetched back by hand first.
 * `canRestore` is false for an account that may not restore. `listed` says
 * whether the server lists its files; `more` reads the next page of them
 * when there is one.
 */
export default function BackupHistory({ rows, canRestore, onPick, listed = false, onDownload, downloading = false, more }) {
  return (
    <section aria-labelledby="backup-history-title" className="bg-surface rounded-lg shadow">
      <div className="p-4 space-y-1">
        <h2 id="backup-history-title" className="text-lg font-semibold text-fg flex items-center gap-2">
          <History className="w-5 h-5 text-accent" aria-hidden="true" /> Backups
          <ListHelp listed={listed} />
        </h2>
        {rows.length === 0 && <p className="text-sm text-fg-muted">No backups yet. Take one above, or schedule them below.</p>}
      </div>
      {rows.length > 0 && (
        <div className="overflow-x-auto">
          <table className="min-w-full text-sm" aria-label="Backups">
            <thead>
              <tr className="text-left text-fg-soft">
                <th scope="col" className="px-4 py-2">Backup</th>
                {listed && <th scope="col" className="px-4 py-2 hidden sm:table-cell">Size</th>}
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
                    {listed && <span className="block text-xs text-fg-muted sm:hidden"><Size row={row} /></span>}
                  </td>
                  {listed && <td className="px-4 py-2 text-fg-soft hidden sm:table-cell"><Size row={row} /></td>}
                  <td className="px-4 py-2 text-fg-soft whitespace-nowrap">{formatUnix(row.takenAt)}</td>
                  <td className="px-4 py-2">
                    <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${STATE_CLASS[row.status] || OTHER_STATE}`}>{row.state}</span>
                    {row.error && <span className="block text-xs text-danger-strong">{row.error}</span>}
                  </td>
                  <td className="px-4 py-2">
                    <div className="flex flex-wrap gap-2">
                      {canRestore && row.bytes !== undefined && (
                        <button type="button" onClick={() => onDownload(row)} disabled={downloading} aria-label={`Download ${row.name}`} className={BUTTON}>
                          Download
                        </button>
                      )}
                      {canRestore && row.ok && isRestorable(row.where) && (
                        <button type="button" onClick={() => onPick(row.where)} aria-label={`Use ${row.name} for restore`} className={BUTTON}>
                          Use for restore
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {more && (
        <div className="p-4">
          <button type="button" onClick={more} className={BUTTON}>Show older backups</button>
        </div>
      )}
    </section>
  );
}
