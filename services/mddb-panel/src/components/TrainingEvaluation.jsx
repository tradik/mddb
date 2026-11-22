import { AlertTriangle } from 'lucide-react';
import { evaluationView, percent } from '../lib/training';
import { hintClass } from './training-styles';

/** A metric's value, or a dash when that side has none. */
const metric = (v) => (typeof v === 'number' ? v.toFixed(3) : '–');

/** The difference in points, with its sign, or a dash when there is none. */
function deltaText(delta) {
  if (delta === null) return '–';
  const sign = delta > 0 ? '+' : '';
  return `${sign}${(delta * 100).toFixed(1)} pts`;
}

/**
 * The before/after of a run: the base model's score, the tuned model's, the
 * difference and the named metrics, with a few scored records. All of it is
 * the evaluate command's report, shown as text.
 */
export default function TrainingEvaluation({ evaluation }) {
  const view = evaluationView(evaluation);
  if (!view) return null;
  if (view.skipped) return <p className={`${hintClass} mt-2`}>Not evaluated: {view.skipped}.</p>;
  return (
    <div className="mt-3">
      <h4 className="text-sm font-semibold text-fg">Evaluation on the validation part</h4>
      <div className="mt-2 grid grid-cols-3 gap-2 text-center">
        <div className="rounded bg-surface-muted p-2">
          <div className={hintClass}>Before</div>
          <div className="text-lg font-semibold text-fg">{view.before ? percent(view.before.score) : 'no baseline'}</div>
        </div>
        <div className="rounded bg-surface-muted p-2">
          <div className={hintClass}>After</div>
          <div className="text-lg font-semibold text-fg">{view.after ? percent(view.after.score) : '–'}</div>
        </div>
        <div className="rounded bg-surface-muted p-2">
          <div className={hintClass}>Difference</div>
          <div className="text-lg font-semibold text-fg">{deltaText(view.delta)}</div>
        </div>
      </div>
      {[...view.notes, ...view.errors].map((n) => (
        <p key={n} className="mt-2 flex items-start gap-1 text-sm text-warning-strong">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" /> {n}
        </p>
      ))}
      {view.metrics.length > 0 && (
        <table className="mt-2 text-sm">
          <thead><tr className="text-left text-fg-soft"><th className="pr-4 font-medium">Metric</th><th className="pr-4 font-medium">Before</th><th className="font-medium">After</th></tr></thead>
          <tbody>
            {view.metrics.map((m) => (
              <tr key={m.name} className="text-fg"><td className="pr-4">{m.name}</td><td className="pr-4">{metric(m.before)}</td><td>{metric(m.after)}</td></tr>
            ))}
          </tbody>
        </table>
      )}
      {view.samples.length > 0 && (
        <div className="mt-2 overflow-x-auto">
          <table className="min-w-full text-xs">
            <thead><tr className="text-left text-fg-soft"><th className="pr-3 font-medium">Input</th><th className="pr-3 font-medium">Expected</th><th className="pr-3 font-medium">Answer</th><th className="font-medium">Right</th></tr></thead>
            <tbody>
              {view.samples.map((s, i) => (
                <tr key={`${i}-${s.input}`} className="align-top text-fg">
                  <td className="pr-3 break-words">{s.input}</td><td className="pr-3 break-words">{s.expected}</td>
                  <td className="pr-3 break-words">{s.actual}</td><td>{s.correct ? 'yes' : 'no'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
