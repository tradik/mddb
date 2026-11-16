import { usageRows } from '../lib/quota-usage';

// What a collection uses of its quota (GO-043), in the collection settings
// dialog: one bar per limit, e.g. "1,234 / 5,000 documents". Each bar is a
// native <progress> (role progressbar) whose aria-valuetext repeats the
// visible text, so a screen reader hears the numbers, not only a percentage.
// The track is styled through ::-webkit-progress-bar and the element itself
// (Firefox); the fill through ::-webkit-progress-value / ::-moz-progress-bar.
//
// Colours (WCAG 2.2, 1.4.11 non-text contrast >= 3:1 against the
// surface-strong track): the fills are the role tokens accent, warning and
// danger (FE-015), 4.8:1, 5.7:1 and 5.2:1 in light mode and 7.6:1, 8.8:1 and
// 6.6:1 in dark mode. The state is also said in words, never by colour alone.

const FILL = {
  ok: '[&::-webkit-progress-value]:bg-accent [&::-moz-progress-bar]:bg-accent',
  near: '[&::-webkit-progress-value]:bg-warning [&::-moz-progress-bar]:bg-warning',
  over: '[&::-webkit-progress-value]:bg-danger [&::-moz-progress-bar]:bg-danger',
};

const BAR = 'block w-full h-2 appearance-none overflow-hidden rounded-full border-0 bg-surface-strong'
  + ' [&::-webkit-progress-bar]:bg-surface-strong [&::-webkit-progress-value]:rounded-full [&::-moz-progress-bar]:rounded-full';

const NOTE = {
  near: 'Close to the limit.',
  over: 'At or over the limit: new writes are refused, or embeddings wait until midnight UTC.',
};

const COLLECTION_NOTE = 'Embedding use counts from midnight UTC, only while the collection has an embedding budget, and from zero after a restart.';

// The tenant view (GO-054) reuses the bars with a tenant's limits: `fields`,
// `title`, `note` and the heading's `id` default to the collection's.
export default function QuotaUsage({ quota, usage, fields, title = 'Quota usage', note = COLLECTION_NOTE, id = 'cc-quota-usage' }) {
  const rows = usageRows(quota, usage, fields);
  if (rows.length === 0) return null;

  return (
    <section aria-labelledby={id} className="border border-line rounded p-3">
      <h3 id={id} className="text-sm font-medium text-fg-muted mb-2">{title}</h3>
      <ul className="space-y-3">
        {rows.map((r) => (
          <li key={r.key}>
            <div className="flex justify-between text-xs text-fg-muted mb-1">
              <span>{r.text}</span>
              <span>{r.percent}%</span>
            </div>
            <progress
              aria-label={r.label}
              max={100}
              value={Math.min(r.percent, 100)}
              aria-valuetext={`${r.text} (${r.percent}%)`}
              className={`${BAR} ${FILL[r.level]}`}
            >
              {r.text} ({r.percent}%)
            </progress>
            {NOTE[r.level] && (
              <p className={`text-xs mt-1 ${r.level === 'over' ? 'text-danger' : 'text-warning-strong'}`}>{NOTE[r.level]}</p>
            )}
          </li>
        ))}
      </ul>
      <p className="text-xs text-fg-subtle mt-2">{note}</p>
    </section>
  );
}
