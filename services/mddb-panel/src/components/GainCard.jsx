import { useEffect, useState } from 'react';
import { TrendingDown } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { compactNumber, formatShares } from '../lib/gain-format';

// AGT-004: what retrieval saved its callers over the last 30 days. Token
// counts are estimates (4 bytes per token); "avoided" counts only hits
// returned as a passage or chunk, against the document they came from.
export default function GainCard() {
  const [report, setReport] = useState(null);
  const [error, setError] = useState(null);

  useEffect(() => {
    mddbClient
      .getGain({ window: '30d' })
      .then(setReport)
      .catch((e) => setError(e.message));
  }, []);

  if (error) {
    return (
      <div className="bg-surface rounded-lg shadow mb-6 p-6 text-sm text-fg-subtle">
        Retrieval gain unavailable: {error}
      </div>
    );
  }
  if (!report) return null;

  const t = report.totals || {};
  const rows = [
    ['Searches', compactNumber(report.totalSearches), formatShares(t.searches, 4)],
    ['Returned', `${compactNumber(t.tokensReturned)} tokens`, `${compactNumber(t.fragmentHits)} passages, ${compactNumber(t.documentHits)} documents`],
    ['Without retrieval', `${compactNumber(report.tokensWithoutRetrieval)} tokens`, 'the same hits as whole documents'],
    ['Saved', `${compactNumber(t.tokensAvoided)} tokens`, `${report.savedPercent}%`],
    ['Algorithms', formatShares(t.algorithms) || '–', formatShares(t.fusion) ? `fusion ${formatShares(t.fusion)}` : ''],
  ];

  return (
    <div className="bg-surface rounded-lg shadow mb-6 p-6">
      <div className="flex items-center mb-4">
        <TrendingDown className="w-5 h-5 text-accent mr-2" />
        <h2 className="text-lg font-semibold text-fg">Retrieval gain, last {report.windowDays} days</h2>
      </div>
      <dl className="grid grid-cols-1 sm:grid-cols-3 gap-x-4 gap-y-2 text-sm">
        {rows.map(([label, value, note]) => (
          <div key={label} className="contents">
            <dt className="text-fg-subtle">{label}</dt>
            <dd className="font-medium text-fg">{value}</dd>
            <dd className="text-fg-subtle">{note}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
