import { RefreshCw } from 'lucide-react';
import { describeJob } from '../lib/use-reindex-jobs';

// The recent vector reindex jobs (GET /v1/vector-reindex/jobs, 2.18.4),
// newest first. The server keeps the last 50 in memory.

const STATUS_CLASS = {
  running: 'bg-info-muted text-info-strong',
  completed: 'bg-success-muted text-success-strong',
  failed: 'bg-danger-muted text-danger-strong',
};

/** A table of jobs with a refresh button; `error` replaces the table. */
export default function ReindexJobsTable({ jobs, error, onRefresh }) {
  return (
    <div className="bg-surface rounded-lg shadow">
      <div className="px-6 py-4 border-b border-line flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-fg">Recent reindex jobs</h2>
          <p className="text-sm text-fg-subtle mt-1">Kept in server memory (last 50); a restart forgets them.</p>
        </div>
        <button
          type="button"
          onClick={onRefresh}
          aria-label="Refresh reindex jobs"
          className="p-2 text-fg-muted rounded hover:bg-surface-subtle"
        >
          <RefreshCw className="w-4 h-4" aria-hidden="true" />
        </button>
      </div>
      {error && <p role="alert" className="px-6 py-4 text-sm text-danger">{error}</p>}
      {!error && jobs.length === 0 && <p className="px-6 py-4 text-sm text-fg-subtle">No reindex jobs since the server started.</p>}
      {!error && jobs.length > 0 && (
        <div className="overflow-x-auto">
          <table className="min-w-full text-sm">
            <thead>
              <tr className="text-left text-fg-muted">
                <th scope="col" className="px-6 py-2">Collection</th>
                <th scope="col" className="px-6 py-2">Status</th>
                <th scope="col" className="px-6 py-2">Started</th>
                <th scope="col" className="px-6 py-2">Outcome</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line-soft">
              {jobs.map((job) => (
                <tr key={job.jobId}>
                  <td className="px-6 py-2 text-fg">
                    {job.collection}
                    {job.force && <span className="ml-1 text-xs text-fg-subtle">(force)</span>}
                  </td>
                  <td className="px-6 py-2">
                    <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_CLASS[job.status] || 'bg-surface-subtle text-fg'}`}>
                      {job.status}
                    </span>
                  </td>
                  <td className="px-6 py-2 text-fg-soft">{job.startedAt ? new Date(job.startedAt).toLocaleString() : '—'}</td>
                  <td className="px-6 py-2 text-fg-soft">{describeJob(job)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
