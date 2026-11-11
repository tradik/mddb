import { useEffect, useState } from 'react';
import { RefreshCw } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { describeCronAction, isRunnable } from '../lib/automation-form';
import { LastRunResult, RunNowButton } from './CronRunNow';

/** The rules of a `GET /v1/automation` answer, which is a list or `{ rules }`. */
function rulesOf(data) {
  if (Array.isArray(data)) return data;
  return data?.rules || [];
}

/** One line saying what a rule does: a cron's action, a trigger's events. */
function describeRule(rule) {
  if (rule.type === 'cron') return describeCronAction(rule);
  if (rule.type === 'trigger') {
    const events = rule.events?.length ? `on ${rule.events.join(', ')}` : 'on every change';
    return rule.query ? `${events} matching "${rule.query}"` : events;
  }
  return '';
}

/**
 * CollectionSources lists the syncs writing to and the triggers watching one
 * collection (2.19.0, `GET /v1/automation?collection=`), with each sync's or
 * backup's last result and a Run now button.
 */
export default function CollectionSources({ collection }) {
  const [rules, setRules] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [reloads, setReloads] = useState(0);

  useEffect(() => {
    let cancelled = false;
    mddbClient
      .listAutomation(undefined, { collection })
      .then((data) => {
        if (cancelled) return;
        setRules(rulesOf(data));
        setError(null);
      })
      .catch((err) => {
        if (!cancelled) setError(err.message);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [collection, reloads]);

  const refresh = () => {
    setLoading(true);
    setReloads((n) => n + 1);
  };

  return (
    <section aria-labelledby="collection-sources-title" className="space-y-2">
      <div className="flex items-center justify-between">
        <h3 id="collection-sources-title" className="text-sm font-semibold text-gray-900">
          Sources and triggers
        </h3>
        <button
          type="button"
          onClick={refresh}
          disabled={loading}
          className="p-1 text-gray-600 hover:bg-gray-100 rounded"
          aria-label="Refresh sources and triggers"
        >
          <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
        </button>
      </div>
      {error && <p role="alert" className="text-xs text-red-700">{error}</p>}
      {!error && !loading && rules.length === 0 && (
        <p className="text-xs text-gray-600">No synced sources or triggers act on this collection.</p>
      )}
      {rules.length > 0 && (
        <ul className="divide-y divide-gray-200 border border-gray-200 rounded-lg">
          {rules.map((rule) => (
            <li key={rule.id} className="p-2 flex items-start justify-between gap-2">
              <div className="min-w-0">
                <div className="text-sm text-gray-900">
                  <span className="font-medium">{rule.name || rule.id}</span>
                  <span className="ml-2 text-xs uppercase text-gray-600">{rule.type}</span>
                  <span className={`ml-2 text-xs ${rule.enabled ? 'text-green-800' : 'text-gray-600'}`}>
                    {rule.enabled ? 'enabled' : 'disabled'}
                  </span>
                </div>
                <div className="text-xs text-gray-600">
                  {rule.schedule && <span className="font-mono mr-1.5">{rule.schedule}</span>}
                  {describeRule(rule)}
                </div>
                <LastRunResult rule={rule} />
              </div>
              {isRunnable(rule) && <RunNowButton rule={rule} />}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
