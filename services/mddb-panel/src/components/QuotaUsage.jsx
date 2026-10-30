import { usageRows } from '../lib/quota-usage';

// What a collection uses of its quota (GO-043), in the collection settings
// dialog: one bar per limit, e.g. "1,234 / 5,000 documents". Each bar is a
// progressbar whose aria-valuetext repeats the visible text, so a screen
// reader hears the numbers, not only a percentage.
//
// Colours (WCAG 2.2, 1.4.11 non-text contrast >= 3:1 against the gray-200
// track, 1.4.3 text >= 4.5:1 on white): primary-700 4.8:1, amber-700 4.1:1,
// red-700 5.2:1 against the track; notes in amber-800 (7.1:1) and red-700
// (6.5:1) on white. The state is also said in words, never by colour alone.

const FILL = {
  ok: 'bg-primary-700',
  near: 'bg-amber-700',
  over: 'bg-red-700',
};

const NOTE = {
  near: 'Close to the limit.',
  over: 'At or over the limit: new writes are refused, or embeddings wait until midnight UTC.',
};

export default function QuotaUsage({ quota, usage }) {
  const rows = usageRows(quota, usage);
  if (rows.length === 0) return null;

  return (
    <section aria-labelledby="cc-quota-usage" className="border border-gray-200 rounded p-3">
      <h3 id="cc-quota-usage" className="text-sm font-medium text-gray-700 mb-2">Quota usage</h3>
      <ul className="space-y-3">
        {rows.map((r) => (
          <li key={r.key}>
            <div className="flex justify-between text-xs text-gray-700 mb-1">
              <span>{r.text}</span>
              <span>{r.percent}%</span>
            </div>
            <div
              role="progressbar"
              aria-label={r.label}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={Math.min(r.percent, 100)}
              aria-valuetext={`${r.text} (${r.percent}%)`}
              className="w-full h-2 rounded-full bg-gray-200"
            >
              <div className={`h-2 rounded-full ${FILL[r.level]}`} style={{ width: `${Math.min(r.percent, 100)}%` }} />
            </div>
            {NOTE[r.level] && (
              <p className={`text-xs mt-1 ${r.level === 'over' ? 'text-red-700' : 'text-amber-800'}`}>{NOTE[r.level]}</p>
            )}
          </li>
        ))}
      </ul>
      <p className="text-xs text-gray-600 mt-2">
        Embedding use counts from midnight UTC, only while the collection has an embedding budget, and from zero after a restart.
      </p>
    </section>
  );
}
