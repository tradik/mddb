import { useEffect, useState } from 'react';
import mddbClient from './mddb-client';
import { backupRules, backupRuns } from './backup-history';

/** How many runs of each scheduled backup the page reads from the log. */
export const RUNS_PER_RULE = 20;

/** The backup rules and their recent runs: one rule list, one log read per rule. */
async function loadHistory() {
  const rules = backupRules(await mddbClient.listAutomation('cron'));
  const logs = await Promise.all(rules.map((rule) => mddbClient.listAutomationLogs({ ruleId: rule.id, limit: RUNS_PER_RULE })));
  return { rules, runs: backupRuns(logs.flatMap((data) => data?.logs || [])) };
}

/**
 * The scheduled backups and their recent runs, `{ rules, runs, error }`,
 * for the Backup & Restore page. Nothing is loaded while `enabled` is false
 * (automations off, or an account that may not see them).
 */
export function useBackupHistory(enabled) {
  const [history, setHistory] = useState({ rules: [], runs: [], error: null });

  useEffect(() => {
    if (!enabled) return;
    loadHistory().then(
      (found) => setHistory({ ...found, error: null }),
      (error) => setHistory({ rules: [], runs: [], error }),
    );
  }, [enabled]);

  return history;
}
