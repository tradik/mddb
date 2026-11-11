import { format } from 'date-fns';
import { featureLabel } from '../lib/features';

// GO-049 (2.19.0): one module on the Features page — its switch, what it
// does, what it needs and who changed it last.

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
  const locked = Boolean(feature.lockedBy);
  const stored = feature.configured ?? feature.enabled;
  const note = stateNote(feature);
  const changed = changedLine(feature);
  const requires = feature.requires || [];

  return (
    <li className="rounded-lg bg-white p-4 shadow">
      <div className="flex flex-wrap items-center justify-between gap-3">
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
            className="relative h-6 w-11 flex-shrink-0 rounded-full bg-gray-500 transition-colors after:absolute after:left-0.5 after:top-0.5 after:h-5 after:w-5 after:rounded-full after:bg-white after:transition-transform peer-checked:bg-primary-700 peer-checked:after:translate-x-5 peer-focus-visible:ring-2 peer-focus-visible:ring-primary-700 peer-focus-visible:ring-offset-2 peer-disabled:cursor-not-allowed peer-disabled:opacity-60"
          />
          <span className="text-sm font-semibold text-gray-900">{featureLabel(name)}</span>
        </label>
        <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${feature.enabled ? 'bg-green-100 text-green-800' : 'bg-gray-200 text-gray-800'}`}>
          {feature.enabled ? 'Running' : 'Off'}
        </span>
      </div>
      <div id={`${id}-desc`} className="mt-2 space-y-1 text-sm text-gray-700">
        {feature.description && <p>{feature.description}</p>}
        <p className="text-xs text-gray-600">
          <code className="text-gray-800">{name}</code> · default {onOff(feature.default)}
          {requires.length > 0 && <> · requires {requires.join(', ')}</>}
        </p>
        {note && <p className="text-amber-800">{note}</p>}
        {changed && <p className="text-xs text-gray-600">{changed}</p>}
      </div>
    </li>
  );
}
