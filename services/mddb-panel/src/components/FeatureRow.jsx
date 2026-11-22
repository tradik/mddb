import { format } from 'date-fns';
import { featureLabel } from '../lib/features';
import HelpPopover from './HelpPopover';

// GO-049 (2.19.0): one module on the Features page — its switch, what it
// does, what it needs and who changed it last. FE-021 (2.19.11): the card
// shows the server's plain title and summary; the technical detail sits
// behind the help control.

/** Where docs/FEATURES.md describes every module. */
const FEATURES_DOCS = 'https://github.com/tradik/mddb/blob/main/docs/FEATURES.md#optional-modules';

const onOff = (value) => (value ? 'on' : 'off');

/** "Changed 2026-11-11 10:00 by admin", or null when the server sent neither. */
function changedLine({ updatedAt, updatedBy }) {
  if (!updatedAt && !updatedBy) return null;
  const when = updatedAt ? ` ${format(new Date(updatedAt), 'yyyy-MM-dd HH:mm')}` : '';
  const who = updatedBy ? ` by ${updatedBy}` : '';
  return `Changed${when}${who}`;
}

/** Why a module is off although its switch may say otherwise, or null. */
function stateNote(feature) {
  if (feature.lockedBy) {
    return `Forced off by ${feature.lockedBy}; the operator has to change the server's environment to turn it on.`;
  }
  if (feature.blockedBy?.length) {
    return `Off until ${feature.blockedBy.map(featureLabel).join(', ')} is turned on.`;
  }
  return null;
}

export default function FeatureRow({ feature, busy, onToggle }) {
  const { name } = feature;
  const id = `feature-${name}`;
  const title = feature.title || featureLabel(name);
  const locked = Boolean(feature.lockedBy);
  const stored = feature.configured ?? feature.enabled;
  const note = stateNote(feature);
  const changed = changedLine(feature);
  const requires = feature.requires || [];
  // An older server sends no summary; its description is then the best text.
  const summary = feature.summary || feature.description;
  const detail = feature.summary ? feature.description : '';

  return (
    <li className="rounded-lg bg-surface p-4 shadow">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-1">
          <label htmlFor={id} className="relative inline-flex cursor-pointer items-center gap-3">
            <input
              id={id}
              type="checkbox"
              role="switch"
              className="peer sr-only"
              checked={Boolean(stored) && !locked}
              disabled={locked || busy}
              onChange={(e) => onToggle(name, e.target.checked)}
              aria-describedby={`${id}-desc`}
            />
            <span
              aria-hidden="true"
              className="relative h-6 w-11 flex-shrink-0 rounded-full bg-gray-500 transition-colors after:absolute after:left-0.5 after:top-0.5 after:h-5 after:w-5 after:rounded-full after:bg-white after:transition-transform peer-checked:bg-primary-700 peer-checked:after:translate-x-5 peer-focus-visible:ring-2 peer-focus-visible:ring-focus peer-focus-visible:ring-offset-2 peer-disabled:cursor-not-allowed peer-disabled:opacity-60"
            />
            <span className="text-sm font-semibold text-fg">{title}</span>
          </label>
          <HelpPopover label={title}>
            {detail && <p>{detail}</p>}
            <p>
              Setting name <code className="text-fg-soft">{name}</code>. A new server starts with it {onOff(feature.default)}.
            </p>
            {requires.length > 0 && <p>Needs {requires.map(featureLabel).join(', ')}.</p>}
            <p>
              <a href={FEATURES_DOCS} target="_blank" rel="noreferrer" className="text-info underline">
                About this module in the docs
              </a>
            </p>
          </HelpPopover>
        </div>
        <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${feature.enabled ? 'bg-success-muted text-success-strong' : 'bg-surface-strong text-fg-soft'}`}>
          {feature.enabled ? 'Running' : 'Off'}
        </span>
      </div>
      <div id={`${id}-desc`} className="mt-2 space-y-1 text-sm text-fg-muted">
        {summary && <p>{summary}</p>}
        {note && <p className="text-warning-strong">{note}</p>}
        {changed && <p className="text-xs text-fg-subtle">{changed}</p>}
      </div>
    </li>
  );
}
