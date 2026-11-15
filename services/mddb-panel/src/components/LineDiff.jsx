import { lineDiff, diffStats } from '../lib/line-diff';

// #404: a line diff of two texts. Each changed line carries a "+" or "-"
// sign and a screen-reader word as well as its colour, so the change does not
// rest on colour alone (WCAG 2.2 SC 1.4.1); the colours keep 4.5:1 text contrast.

const styles = {
  same: { sign: ' ', word: '', className: 'text-fg-soft' },
  added: { sign: '+', word: 'added: ', className: 'bg-success-soft text-success-strong' },
  removed: { sign: '-', word: 'removed: ', className: 'bg-danger-soft text-danger-strong' },
};

export default function LineDiff({ before, after, label }) {
  const diff = lineDiff(before, after);
  const { added, removed } = diffStats(diff);
  return (
    <figure className="mt-2">
      <figcaption className="text-xs text-fg-muted">
        {label}: {added} line{added === 1 ? '' : 's'} added, {removed} removed
      </figcaption>
      {added + removed === 0 ? (
        <p className="mt-1 text-sm text-fg-muted">No change.</p>
      ) : (
        <pre className="mt-1 max-h-96 overflow-auto rounded border border-line bg-surface p-2 text-xs">
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
