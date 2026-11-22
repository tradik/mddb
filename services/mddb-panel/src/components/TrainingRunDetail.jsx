import { Download, Square, Trash2, RefreshCw } from 'lucide-react';
import { statusLabel, isActive, formatBytes } from '../lib/training';
import TrainingEvaluation from './TrainingEvaluation';
import { secondaryButton, hintClass } from './training-styles';

/**
 * One run: its state, stage and progress, the end of its log, the result to
 * download, its evaluation, and the actions its state allows.
 */
export default function TrainingRunDetail({ run, log, onCancel, onDownload, onEvaluate, onDelete }) {
  const active = isActive(run);
  const finished = run.status === 'completed' || run.status === 'completedWithWarnings';
  return (
    <div className="mt-4 rounded border border-line p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0 text-sm text-fg">
          <span className="font-medium break-all">{run.name || run.id}</span>
          {run.trial && <span className="ml-2 rounded-full bg-surface-subtle px-2 py-0.5 text-xs text-fg-soft">trial</span>}
          <div className={hintClass}>
            {run.profile} · {statusLabel(run.status)}{run.stage && ` · stage ${run.stage}`}
            {typeof run.progress === 'number' && ` · ${run.progress}%`}{run.progressMessage && ` ${run.progressMessage}`}
            {typeof run.exitCode === 'number' && ` · exit code ${run.exitCode}`}
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          {active && (
            <button type="button" className={secondaryButton} onClick={() => onCancel(run.id)}>
              <Square className="h-4 w-4" aria-hidden="true" /> Cancel
            </button>
          )}
          {run.result && (
            <button type="button" className={secondaryButton} onClick={() => onDownload(run.id)}>
              <Download className="h-4 w-4" aria-hidden="true" /> {run.result.file} ({formatBytes(run.result.bytes)})
            </button>
          )}
          {finished && (
            <button type="button" className={secondaryButton} onClick={() => onEvaluate(run.id)}>
              <RefreshCw className="h-4 w-4" aria-hidden="true" /> Evaluate again
            </button>
          )}
          {!active && (
            <button type="button" className={secondaryButton} onClick={() => onDelete(run.id)} aria-label={`Delete run ${run.name || run.id}`}>
              <Trash2 className="h-4 w-4" aria-hidden="true" />
            </button>
          )}
        </div>
      </div>
      {run.error && <p className="mt-2 text-sm text-danger-strong">{run.error}</p>}
      {run.warnings?.map((w) => <p key={w} className="mt-1 text-sm text-warning-strong">{w}</p>)}
      {Object.keys(run.parameters || {}).length > 0 && (
        <p className={`${hintClass} mt-1 break-words`}>
          Parameters: {Object.entries(run.parameters).map(([k, v]) => `${k}=${v}`).join(', ')}
        </p>
      )}
      <TrainingEvaluation evaluation={run.evaluation} />
      <h4 className="mt-3 text-sm font-semibold text-fg">Log</h4>
      <pre className="mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-words rounded bg-surface-muted p-2 text-xs text-fg">
        {log || 'Nothing written yet.'}
      </pre>
    </div>
  );
}
