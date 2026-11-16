import QuotaUsage from './QuotaUsage';
import TenantForm from './TenantForm';
import { formatAmount } from '../lib/quota-usage';
import { TENANT_USAGE_FIELDS, LIMIT_INPUTS, barLimits, refusalList, queueSummary, limitText } from '../lib/tenant-usage';

// GO-054: one tenant — its usage against its quota, the limits that apply to
// its requests, its place in the background queues, what it has running and
// what was refused. A global admin also gets the form that sets the quota
// and the limits.

const TENANT_NOTE = 'Usage counts every collection of the tenant. Vector RAM is an estimate: the tenant\'s share of the in-memory vectors times the index\'s size. Embedding use counts from midnight UTC, only while the tenant has an embedding budget, and from zero after a restart.';

const QUEUES = [
  { key: 'index', label: 'Metadata indexing' },
  { key: 'embedding', label: 'Embedding' },
  { key: 'embeddingParked', label: 'Embedding, parked (waiting for room or for the budget)' },
];

const SECTION = 'rounded border border-line bg-surface p-4';
const H3 = 'mb-2 text-sm font-semibold text-fg';

export default function TenantDetail({ report, admin, onSaved }) {
  const limits = report.effectiveLimits;
  const refusals = refusalList(report.refusals);
  const { inFlight, queues } = report;
  const names = report.collectionNames;

  return (
    <section aria-labelledby="tenant-detail" className="max-w-5xl space-y-4">
      <h2 id="tenant-detail" className="text-lg font-bold text-fg">Tenant {report.tenant}</h2>

      {report.quota ? (
        <QuotaUsage quota={barLimits(report)} usage={report.usage} fields={TENANT_USAGE_FIELDS}
          title="Usage against the tenant quota" note={TENANT_NOTE} id="tenant-quota-usage" />
      ) : (
        <p className="text-sm text-fg-muted">
          No tenant quota: its collections hold {formatAmount(report.usage.documents)} documents in {formatAmount(report.usage.collections)} collections, limited only by each collection&apos;s own quota and the server&apos;s reserves.
        </p>
      )}

      <div className="grid gap-4 lg:grid-cols-2">
        <section aria-labelledby="tenant-limits" className={SECTION}>
          <h3 id="tenant-limits" className={H3}>Request limits in force</h3>
          <dl className="grid grid-cols-[1fr_auto] gap-x-4 gap-y-1 text-sm">
            {LIMIT_INPUTS.map((f) => (
              <div key={f.name} className="contents">
                <dt className="text-fg-muted">{f.label}</dt>
                <dd className="text-fg">{limitText(limits[f.name])}</dd>
              </div>
            ))}
          </dl>
          <p className="mt-2 text-xs text-fg-subtle">The tenant&apos;s own limits over the server defaults; &quot;none&quot; is no limit.</p>
        </section>

        <section aria-labelledby="tenant-queues" className={SECTION}>
          <h3 id="tenant-queues" className={H3}>Queues and running work</h3>
          <ul className="space-y-1 text-sm text-fg">
            {QUEUES.map((q) => (
              <li key={q.key}>
                <span className="text-fg-muted">{q.label}:</span> {formatAmount(queues[q.key].depth)} waiting
                {q.key === 'embeddingParked' ? null : <span className="text-fg-subtle"> — {queueSummary(queues[q.key])}</span>}
              </li>
            ))}
            <li>
              <span className="text-fg-muted">Running now:</span> {formatAmount(inFlight.searches)} searches, {formatAmount(inFlight.writes)} writes, {formatAmount(inFlight.reindexes)} reindexes
            </li>
          </ul>
        </section>
      </div>

      <section aria-labelledby="tenant-refusals" className={SECTION}>
        <h3 id="tenant-refusals" className={H3}>Refused since the server started</h3>
        {refusals.length === 0 ? (
          <p className="text-sm text-fg-muted">Nothing refused.</p>
        ) : (
          <ul className="space-y-1 text-sm text-fg">
            {refusals.map((r) => (
              <li key={r.reason}><code className="text-fg-soft">{r.reason}</code>: {formatAmount(r.count)}</li>
            ))}
          </ul>
        )}
      </section>

      <section aria-labelledby="tenant-collections" className={SECTION}>
        <h3 id="tenant-collections" className={H3}>Collections ({names.length})</h3>
        {names.length === 0 ? (
          <p className="text-sm text-fg-muted">The tenant has no collections yet.</p>
        ) : (
          <p className="text-sm text-fg">{names.join(', ')}</p>
        )}
      </section>

      {admin ? (
        <TenantForm report={report} onSaved={onSaved} />
      ) : (
        <p className="text-sm text-fg-muted">Only a global admin can change a tenant&apos;s quota and limits.</p>
      )}
    </section>
  );
}
