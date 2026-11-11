import { useState } from 'react';
import ConfirmDialog from './ConfirmDialog';
import { bulkTargets } from '../lib/feature-groups';
import { featureLabel } from '../lib/features';

// The Features page (2.19.0): "Turn on all" and "Turn off all" for every
// module, or for one category. Both ask first and name what will change;
// modules forced off by the operator are skipped and said so.

/** The confirmation text: what changes and what is skipped. */
function confirmText({ names, locked }, enabled) {
  const what = names.map(featureLabel).join(', ');
  const skipped = locked.length ? ` Skipped, forced off by the operator: ${locked.map(featureLabel).join(', ')}.` : '';
  const effect = enabled
    ? 'Their API routes, tools and pages come back.'
    : 'Their API routes answer "feature disabled", their tools and pages disappear. Stored data stays.';
  return `${what} will be turned ${enabled ? 'on' : 'off'}. ${effect}${skipped}`;
}

export default function FeatureBulkBar({ features, scope, busy, onApply }) {
  const [pending, setPending] = useState(null);
  const plans = { true: bulkTargets(features, true), false: bulkTargets(features, false) };

  const button = (enabled) => (
    <button
      type="button"
      disabled={busy || plans[enabled].names.length === 0}
      onClick={() => setPending(enabled)}
      className="rounded border border-gray-500 px-2.5 py-1 text-xs font-medium text-gray-800 hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-50"
      aria-label={`Turn ${enabled ? 'on' : 'off'} all${scope ? ` in ${scope}` : ''}`}
    >
      Turn {enabled ? 'on' : 'off'} all
    </button>
  );

  const confirm = async () => {
    const enabled = pending;
    setPending(null);
    await onApply(enabled, plans[enabled].names);
  };

  return (
    <div className="flex gap-2">
      {button(true)}
      {button(false)}
      {pending !== null && (
        <ConfirmDialog
          title={`Turn ${pending ? 'on' : 'off'} ${scope ? `all of ${scope}` : 'every module'}?`}
          message={confirmText(plans[pending], pending)}
          confirmLabel={`Turn ${pending ? 'on' : 'off'}`}
          onConfirm={confirm}
          onCancel={() => setPending(null)}
        />
      )}
    </div>
  );
}
