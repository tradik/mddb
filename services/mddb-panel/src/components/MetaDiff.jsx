import { metaDiff } from '../lib/meta-diff';

// FE-013: the metadata keys one version adds, removes or changes against
// another, next to the LineDiff of their content. Each change is named in
// words ("added", "removed", "changed"), not by colour alone (WCAG 2.2
// SC 1.4.1); the colours keep 4.5:1 text contrast.

const styles = {
  added: { word: 'added', className: 'bg-success-soft text-success-strong' },
  removed: { word: 'removed', className: 'bg-danger-soft text-danger-strong' },
  changed: { word: 'changed', className: 'bg-warning-soft text-warning-strong' },
};

/** Lists how the metadata `before` differs from `after`. */
export default function MetaDiff({ before, after, label }) {
  const changes = metaDiff(before, after);
  return (
    <figure className="mt-2">
      <figcaption className="text-xs text-fg-muted">
        {label}: {changes.length} key{changes.length === 1 ? '' : 's'} differ
      </figcaption>
      {changes.length === 0 ? (
        <p className="mt-1 text-sm text-fg-muted">No metadata change.</p>
      ) : (
        <ul className="mt-1 rounded border border-line bg-surface p-2 text-xs space-y-1">
          {changes.map((c) => (
            <li key={c.key} className={`rounded px-1 ${styles[c.type].className}`}>
              <span className="font-semibold">{styles[c.type].word}</span>{' '}
              <span className="font-mono">{c.key}</span>:{' '}
              {c.type === 'changed' ? `${c.before} → ${c.after}` : c.before ?? c.after}
            </li>
          ))}
        </ul>
      )}
    </figure>
  );
}
