import { AlertTriangle, X } from 'lucide-react';
import { useLimitNotice, reportLimit } from '../lib/limit-notice';

// #368: the server returns at most MDDB_SEARCH_MAX_LIMIT results per search
// (a collection's retrieval.maxTopK can lower it). A capped answer looks like
// a complete one, so the panel says when it was cut, whichever view searched.
export default function LimitCappedNotice() {
  const notice = useLimitNotice();
  if (!notice) return null;

  // <output> is a polite live region (role "status"); it holds phrasing
  // content only, so the layout uses block spans.
  return (
    <output className="fixed bottom-4 left-4 z-50 block max-w-md rounded-lg border border-amber-300 bg-amber-50 p-3 shadow-lg">
      <span className="flex items-start gap-2">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-700" aria-hidden="true" />
        <span className="block text-sm text-amber-900">
          The last search ({notice.endpoint}) was capped at{' '}
          <strong>{notice.limit ?? 'the server limit'}</strong> results. Page with offset, or ask the
          operator to raise <code className="rounded bg-amber-100 px-1">MDDB_SEARCH_MAX_LIMIT</code>{' '}
          or the collection&apos;s <code className="rounded bg-amber-100 px-1">maxTopK</code>.
        </span>
        <button
          type="button"
          onClick={() => reportLimit(null)}
          className="rounded p-0.5 text-amber-800 hover:bg-amber-100"
          aria-label="Dismiss"
        >
          <X className="h-4 w-4" />
        </button>
      </span>
    </output>
  );
}
