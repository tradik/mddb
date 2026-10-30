import { useState } from 'react';
import { PlayCircle } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { describeLastResult } from '../lib/automation-form';

/**
 * LastRunResult shows what the latest run of a sync or backup rule did
 * (2.18.11): the status, a sync's counts and the error. Nothing before the
 * first run.
 */
export function LastRunResult({ rule }) {
  const text = describeLastResult(rule);
  if (!text) return null;
  const failed = rule.lastResult.status === 'error';
  return (
    <div
      className={`text-xs mt-0.5 ${failed ? 'text-red-700' : 'text-gray-600'}`}
      title="Last run: status, added +, updated ~, unchanged =, deleted −, failed ✕"
    >
      Last run: {text}
    </div>
  );
}

/**
 * RunNowButton runs a sync or backup rule now (2.18.11) — through the code
 * its schedule fires — and reports the job id, or why the server refused
 * (409 while the previous run is still going). The run goes on in the
 * background: Refresh shows its result once it is done.
 */
export function RunNowButton({ rule }) {
  const [running, setRunning] = useState(false);
  const [message, setMessage] = useState(null);

  const run = async () => {
    setRunning(true);
    setMessage(null);
    try {
      const started = await mddbClient.runAutomation(rule.id);
      setMessage({ ok: true, text: `Started: job ${started.jobId}` });
    } catch (err) {
      setMessage({ ok: false, text: err.message });
    } finally {
      setRunning(false);
    }
  };

  return (
    <span className="inline-flex items-center gap-1.5">
      {message && (
        <span role="status" className={`text-xs ${message.ok ? 'text-green-800' : 'text-red-700'}`}>
          {message.text}
        </span>
      )}
      <button
        onClick={run}
        disabled={running}
        className="p-1.5 text-green-700 hover:bg-green-50 rounded transition-colors"
        title="Run now"
        aria-label={`Run ${rule.name} now`}
      >
        {running ? (
          <div className="animate-spin rounded-full h-4 w-4 border-2 border-green-700 border-t-transparent"></div>
        ) : (
          <PlayCircle className="w-4 h-4" />
        )}
      </button>
    </span>
  );
}
