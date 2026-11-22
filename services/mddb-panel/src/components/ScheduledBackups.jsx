import { CalendarClock } from 'lucide-react';
import { useStore } from '../lib/store';
import { ruleKeep, ruleTarget } from '../lib/backup-history';
import HelpPopover from './HelpPopover';

/**
 * ScheduledBackups lists the cron rules that take a backup: when each runs,
 * where its backups go and how many it keeps. Rules are made and changed on
 * the Automation page, which the buttons open.
 */
export default function ScheduledBackups({ rules }) {
  const setViewMode = useStore((s) => s.setViewMode);
  const openAutomation = () => setViewMode('automation');
  const button = 'px-2 py-1 text-xs rounded border border-line-strong text-fg hover:bg-surface-subtle whitespace-nowrap';

  return (
    <section aria-labelledby="scheduled-backups-title" className="bg-surface rounded-lg shadow p-4 space-y-3">
      <h2 id="scheduled-backups-title" className="text-lg font-semibold text-fg flex items-center gap-2">
        <CalendarClock className="w-5 h-5 text-accent" aria-hidden="true" /> Scheduled backups
        <HelpPopover label="scheduled backups">
          <p>A scheduled backup is a cron rule with a <code className="text-fg-soft">backup</code> action (<code className="text-fg-soft">POST /v1/automation</code>). It runs only where crons run (<code className="text-fg-soft">MDDB_CRONS=true</code>): in a cluster, on one node.</p>
          <p>Each run writes and checks the backup, stores it on its target and then deletes the oldest ones over the number it keeps. See docs/BACKUP.md, &quot;Scheduled backups&quot;.</p>
        </HelpPopover>
      </h2>
      {rules.length === 0 ? (
        <div className="flex flex-wrap items-center gap-3">
          <p className="text-sm text-fg-muted">Nothing is scheduled. A cron rule on the Automation page can take a backup every night.</p>
          <button type="button" onClick={openAutomation} className={button}>Open Automation</button>
        </div>
      ) : (
        <ul className="divide-y divide-line-soft">
          {rules.map((rule) => (
            <li key={rule.id} className="py-2 flex flex-wrap items-center justify-between gap-2">
              <div className="min-w-0 text-sm">
                <p className="font-medium text-fg break-all">
                  {rule.name}
                  {!rule.enabled && <span className="ml-2 text-xs font-normal text-fg-muted">(paused)</span>}
                </p>
                <p className="text-fg-muted">
                  <code className="text-fg-soft">{rule.schedule}</code> · {ruleTarget(rule.backup)} · {ruleKeep(rule.backup)}
                </p>
              </div>
              <button type="button" onClick={openAutomation} aria-label={`Open ${rule.name} in Automation`} className={button}>Open in Automation</button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
