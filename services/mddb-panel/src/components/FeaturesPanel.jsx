import { useState } from 'react';
import { ToggleRight, RefreshCw } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useFeatures, featureLabel } from '../lib/features';
import FeatureRow from './FeatureRow';
import FeatureBulkBar from './FeatureBulkBar';
import { groupFeatures } from '../lib/feature-groups';

// GO-049 (2.19.0): the Features page under Administration. One switch per
// optional server module; core storage, search, auth and backup are not
// switchable and not listed. Everyone signed in sees the list; switching
// needs admin on "*" and the server's 403 is shown as it is.

/** The message for a failed switch: a 409 names the lock. */
function toggleError(name, err) {
  if (err.status === 409) {
    const by = err.body?.lockedBy || 'the operator';
    return `${featureLabel(name)} is locked: forced off by ${by}. Change the server's environment and restart it to turn it on.`;
  }
  return err.message;
}

/** The message for a list that could not be loaded. */
function loadError(err) {
  if (err.status === 404) {
    return 'This server does not report module switches (GET /v1/features answered 404), so every module is treated as on.';
  }
  return `Could not load the module list: ${err.message}. Until it loads, every module is treated as on.`;
}

export default function FeaturesPanel() {
  const { list, version, loading, error: listError, reload } = useFeatures();
  const [busy, setBusy] = useState(null);
  const [error, setError] = useState(null);
  const [message, setMessage] = useState(null);

  const toggle = async (name, enabled) => {
    setBusy(name);
    setError(null);
    setMessage(null);
    try {
      await mddbClient.setFeature(name, enabled);
      setMessage(`${featureLabel(name)} turned ${enabled ? 'on' : 'off'}.`);
      await reload();
    } catch (err) {
      setError(toggleError(name, err));
    } finally {
      setBusy(null);
    }
  };

  // "Turn on/off all": one module at a time, in the planned order; a failure
  // is reported and the rest still run, then the list is read again.
  const applyAll = async (enabled, names) => {
    setBusy('*');
    setError(null);
    setMessage(null);
    const failed = [];
    for (const name of names) {
      try {
        await mddbClient.setFeature(name, enabled);
      } catch (err) {
        failed.push(toggleError(name, err));
      }
    }
    const done = names.length - failed.length;
    setMessage(`${done} ${done === 1 ? 'module' : 'modules'} turned ${enabled ? 'on' : 'off'}.`);
    if (failed.length) setError(failed.join(' '));
    await reload();
    setBusy(null);
  };

  return (
    <div className="h-full overflow-y-auto bg-surface-muted p-6">
      <div className="mb-4 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <ToggleRight className="h-6 w-6 text-accent" aria-hidden="true" />
          <h2 className="text-xl font-bold text-fg">Features</h2>
          <span className="text-xs text-fg-subtle">version {version}</span>
        </div>
        <div className="flex items-center gap-3">
          <FeatureBulkBar features={list} busy={busy !== null || loading} onApply={applyAll} />
          <button
            type="button"
            onClick={() => reload()}
            disabled={loading}
            className="flex items-center gap-1.5 rounded px-3 py-1.5 text-sm text-fg-soft hover:bg-surface-subtle disabled:opacity-50"
          >
            <RefreshCw className="h-4 w-4" aria-hidden="true" /> Reload
          </button>
        </div>
      </div>
      <p className="mb-4 max-w-3xl text-sm text-fg-muted">
        Each optional module of the server can be switched off. A module that is off answers its API routes with
        404 &quot;feature disabled&quot; and its pages leave this menu. Documents, collections, full-text and vector
        search, auth and backups are always on. A module forced off with MDDB_FEATURES_FORCE_OFF stays off until the
        operator changes the environment. Switching needs admin on &quot;*&quot;.
      </p>

      {error && <p role="alert" className="mb-3 rounded border border-danger-line bg-danger-soft p-2 text-sm text-danger-strong">{error}</p>}
      {message && <output className="mb-3 block rounded border border-success-line bg-success-soft p-2 text-sm text-success-strong">{message}</output>}
      {listError && <p role="alert" className="mb-3 rounded border border-warning-line bg-warning-soft p-2 text-sm text-warning-strong">{loadError(listError)}</p>}

      {loading && list.length === 0 ? (
        <p className="text-sm text-fg-muted">Loading modules…</p>
      ) : (
        <div aria-label="Modules" role="region" className="max-w-5xl space-y-6">
          {groupFeatures(list).map((group) => (
            <section key={group.id} aria-labelledby={`features-${group.id}`}>
              <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                <h3 id={`features-${group.id}`} className="text-sm font-semibold uppercase tracking-wide text-fg-muted">
                  {group.label}
                </h3>
                <FeatureBulkBar features={group.features} scope={group.label} busy={busy !== null} onApply={applyAll} />
              </div>
              <ul aria-label={`${group.label} modules`} className="grid gap-3 lg:grid-cols-2">
                {group.features.map((feature) => (
                  <FeatureRow key={feature.name} feature={feature} busy={busy !== null} onToggle={toggle} />
                ))}
              </ul>
            </section>
          ))}
        </div>
      )}
    </div>
  );
}
