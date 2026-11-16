import { formatAmount } from '../lib/quota-usage';
import { refusalTotal } from '../lib/tenant-usage';

// GO-054: one row per tenant — what its collections hold (against its quota
// where one is set), the jobs it has waiting, the requests it has running and
// the requests and writes refused since the server started.

/** "1,234 of 5,000 (25%)", or the number alone without a limit. */
function ofLimit(used, limit, bytes = false) {
  const amount = formatAmount(used, bytes);
  if (!(limit > 0)) return amount;
  return `${amount} of ${formatAmount(limit, bytes)} (${Math.round((used / limit) * 100)}%)`;
}

/** The jobs a tenant has waiting in every background queue. */
function queued(queues) {
  return Object.values(queues).reduce((sum, q) => sum + q.depth, 0);
}

/** The searches, writes and reindexes a tenant has running. */
function running(inFlight) {
  return Object.values(inFlight).reduce((sum, n) => sum + n, 0);
}

const CELL = 'px-3 py-2 text-sm';

export default function TenantsTable({ tenants, selected, onSelect }) {
  return (
    <div className="mb-6 overflow-x-auto rounded border border-line bg-surface">
      <table className="min-w-full">
        <caption className="sr-only">Tenants, their usage against their quota, queued jobs, running requests and refusals</caption>
        <thead className="bg-surface-subtle text-left text-xs uppercase tracking-wide text-fg-muted">
          <tr>
            <th scope="col" className={CELL}>Tenant</th>
            <th scope="col" className={CELL}>Documents</th>
            <th scope="col" className={CELL}>Stored</th>
            <th scope="col" className={CELL}>Collections</th>
            <th scope="col" className={CELL}>Queued jobs</th>
            <th scope="col" className={CELL}>Running</th>
            <th scope="col" className={CELL}>Refused</th>
            <th scope="col" className={CELL}><span className="sr-only">Details</span></th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line text-fg">
          {tenants.map((t) => {
            const q = t.quota || {};
            const u = t.usage;
            return (
              <tr key={t.tenant} className={t.tenant === selected ? 'bg-accent-soft' : undefined}>
                <th scope="row" className={`${CELL} font-medium`}>{t.tenant}</th>
                <td className={CELL}>{ofLimit(u.documents, q.maxDocuments)}</td>
                <td className={CELL}>{ofLimit(u.bytes, q.maxBytes, true)}</td>
                <td className={CELL}>{ofLimit(u.collections, q.maxCollections)}</td>
                <td className={CELL}>{formatAmount(queued(t.queues))}</td>
                <td className={CELL}>{formatAmount(running(t.inFlight))}</td>
                <td className={CELL}>{formatAmount(refusalTotal(t.refusals))}</td>
                <td className={CELL}>
                  <button
                    type="button"
                    onClick={() => onSelect(t.tenant)}
                    aria-pressed={t.tenant === selected}
                    aria-label={`Details of ${t.tenant}`}
                    className="rounded px-2 py-1 text-sm text-accent-strong hover:bg-surface-subtle"
                  >
                    Details
                  </button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
