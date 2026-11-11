import { lineDiff, diffStats } from '../lib/line-diff';

// #404: a line diff of two texts. Each changed line carries a "+" or "-"
// sign and a screen-reader word as well as its colour, so the change does not
// rest on colour alone (WCAG 2.2 SC 1.4.1); the colours keep 4.5:1 text contrast.

const styles = {
  same: { sign: ' ', word: '', className: 'text-gray-800' },
  added: { sign: '+', word: 'added: ', className: 'bg-green-50 text-green-900' },
  removed: { sign: '-', word: 'removed: ', className: 'bg-red-50 text-red-900' },
};

export default function LineDiff({ before, after, label }) {
  const diff = lineDiff(before, after);
  const { added, removed } = diffStats(diff);
  return (
    <figure className="mt-2">
      <figcaption className="text-xs text-gray-700">
        {label}: {added} line{added === 1 ? '' : 's'} added, {removed} removed
      </figcaption>
      {added + removed === 0 ? (
        <p className="mt-1 text-sm text-gray-700">No change.</p>
      ) : (
        <pre className="mt-1 max-h-96 overflow-auto rounded border border-gray-200 bg-white p-2 text-xs">
          {diff.map((d, i) => {
            const s = styles[d.type];
            return (
              <div key={i} className={s.className}>
                <span aria-hidden="true">{s.sign} </span>
                {s.word && <span className="sr-only">{s.word}</span>}
                {d.text}
              </div>
            );
          })}
        </pre>
      )}
    </figure>
  );
}
