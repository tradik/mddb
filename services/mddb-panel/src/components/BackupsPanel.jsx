import { useState } from 'react';
import { Archive } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import { useMe } from '../lib/use-me';
import { useFeatures } from '../lib/features';
import { useAction } from '../lib/use-action';
import { saveBlob } from '../lib/download';
import { isEntryAvailable, navConditions } from '../lib/nav-groups';
import { takenNow } from '../lib/backup-history';
import { mergeBackupRows } from '../lib/backup-files';
import { useBackupHistory } from '../lib/use-backup-history';
import { useBackupFiles } from '../lib/use-backup-files';
import ApiErrorNotice from './ApiErrorNotice';
import BackupRestoreCard from './BackupRestoreCard';
import BackupHistory from './BackupHistory';
import ScheduledBackups from './ScheduledBackups';

/**
 * The Backup & Restore page (2.19.11), in the System group: take a backup
 * now, see every backup file on the server with its size, download one or
 * pick it to restore, and the scheduled backups that automation rules take.
 * Everything here is server-wide and needs admin on "*": a signed-in account
 * without it reads why, and the buttons are off. The scheduled part follows
 * the Automation page: it is left out where that page is. A server that
 * cannot list its backup files (before 2.19.11) leaves the list to the
 * scheduled runs and the backups taken here, without sizes or downloads.
 */
export default function BackupsPanel() {
  const config = useStore((s) => s.config);
  const { features } = useFeatures();
  const { me, error: meError } = useMe();
  // No account to ask about (authentication off) counts as allowed.
  const allowed = me?.admin !== false;
  const meKnown = me !== null || meError !== null;
  const automations = meKnown && allowed && isEntryAvailable({ mode: 'automation', when: 'automations' }, { features, ...navConditions(config) });
  const history = useBackupHistory(automations);
  const files = useBackupFiles(meKnown && allowed);
  const downloads = useAction();
  const [from, setFrom] = useState('');
  const [taken, setTaken] = useState([]);

  const others = [...taken, ...history.runs];
  const rows = files.files ? mergeBackupRows(files.files, others) : others;
  const hasMore = files.files !== null && files.files.length < files.total;

  const download = (row) => downloads.run(async () => saveBlob(await mddbClient.downloadBackup(row.name), row.name));

  const backedUp = (where) => {
    setTaken((now) => [takenNow(where), ...now]);
    files.reload();
  };

  const pick = (where) => {
    setFrom(where);
    document.getElementById('restore-from').focus();
  };

  return (
    <div className="bg-surface-muted p-6">
      <div className="max-w-4xl mx-auto space-y-6">
        <header>
          <h1 className="text-2xl font-bold text-fg mb-2 flex items-center gap-2">
            <Archive className="w-6 h-6 text-accent" aria-hidden="true" /> Backup &amp; Restore
          </h1>
          <p className="text-fg-subtle">Copies of the whole database: take one now, keep them on a schedule, bring one back.</p>
        </header>
        {!allowed && (
          <p role="note" className="rounded-lg border border-warning-line bg-warning-soft p-3 text-sm text-warning-strong">
            Backups cover every collection, so only an administrator of the whole server can take or restore them. You can see this page, not use it.
          </p>
        )}
        <BackupRestoreCard from={from} onFromChange={setFrom} onBackedUp={backedUp} disabled={!allowed} />
        <ApiErrorNotice error={files.error || downloads.error} admin />
        <BackupHistory
          rows={rows}
          canRestore={allowed}
          onPick={pick}
          listed={files.files !== null}
          onDownload={download}
          downloading={downloads.busy}
          more={hasMore ? files.more : undefined}
        />
        {automations && (
          <>
            <ApiErrorNotice error={history.error} admin />
            <ScheduledBackups rules={history.rules} />
          </>
        )}
      </div>
    </div>
  );
}
