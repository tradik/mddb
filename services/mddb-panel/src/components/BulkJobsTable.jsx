import { RefreshCw } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { describeBulkJob } from '../lib/use-bulk-jobs';
import { formatUnix } from '../lib/time-format';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';

const STATUS_CLASS = {
  pending: 'bg-surface-subtle text-fg',
  processing: 'bg-info-muted text-info-strong',
  completed: 'bg-success-muted text-success-strong',
  failed: 'bg-danger-muted text-danger-strong',
  cancelled: 'bg-warning-muted text-warning-strong',
};

/**
 * BulkJobsTable lists the bulk ingest jobs (/v1/bulk-ingest-jobs), newest
 * first, with live progress for running ones, the first errors of a job and
 * Cancel for a job still pending (/v1/bulk-ingest-job/{id} DELETE).
 */
export default function BulkJobsTable({ jobs, error, onRefresh }) {
  const cancelling = useAction();

  const cancel = (id) => cancelling.run(async () => {
    await mddbClient.cancelBulkIngestJob(id);
    onRefresh();
  });

  return (
    <section aria-labelledby="bulk-jobs-title" className="bg-surface rounded-lg shadow">
      <div className="px-4 py-3 border-b border-line flex items-center justify-between">
        <div>
          <h3 id="bulk-jobs-title" className="text-base font-semibold text-fg">Bulk import jobs</h3>
          <p className="text-xs text-fg-muted">Kept in server memory; a restart forgets them. One worker runs them in order.</p>
        </div>
        <button type="button" onClick={onRefresh} aria-label="Refresh bulk import jobs" className="p-2 text-fg-soft rounded hover:bg-surface-subtle">
          <RefreshCw className="w-4 h-4" aria-hidden="true" />
        </button>
      </div>
      <div className="p-4 space-y-2">
        <ApiErrorNotice error={error || cancelling.error} />
        {!error && jobs.length === 0 && <p className="text-sm text-fg-muted">No bulk import jobs since the server started.</p>}
      </div>
      {!error && jobs.length > 0 && (
        <div className="overflow-x-auto">
          <table className="min-w-full text-sm" aria-label="Bulk import jobs">
            <thead>
              <tr className="text-left text-fg-soft">
                <th scope="col" className="px-4 py-2">Collection</th>
                <th scope="col" className="px-4 py-2">Status</th>
                <th scope="col" className="px-4 py-2">Submitted</th>
                <th scope="col" className="px-4 py-2">Progress</th>
                <th scope="col" className="px-4 py-2"><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line-soft">
              {jobs.map((job) => (
                <tr key={job.id}>
                  <td className="px-4 py-2 text-fg">{job.collection}</td>
                  <td className="px-4 py-2">
                    <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_CLASS[job.status] || STATUS_CLASS.pending}`}>{job.status}</span>
                  </td>
                  <td className="px-4 py-2 text-fg-soft">{formatUnix(job.submittedAt)}</td>
                  <td className="px-4 py-2 text-fg-soft">
                    {job.status === 'processing' && (
                      <progress max={job.total} value={job.processed || 0} aria-label={`Progress of ${job.collection}`} className="block w-32" />
                    )}
                    {describeBulkJob(job)}
                    {job.errors?.length > 0 && (
                      <details className="text-xs text-danger-strong">
                        <summary className="cursor-pointer">{job.errors.length} error messages</summary>
                        <ul className="list-disc pl-4">{job.errors.map((e) => <li key={e}>{e}</li>)}</ul>
                      </details>
                    )}
                  </td>
                  <td className="px-4 py-2">
                    {job.status === 'pending' && (
                      <button
                        type="button"
                        onClick={() => cancel(job.id)}
                        disabled={cancelling.busy}
                        className="text-sm text-danger-strong underline disabled:opacity-50"
                      >
                        Cancel <span className="sr-only">job for {job.collection}</span>
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
