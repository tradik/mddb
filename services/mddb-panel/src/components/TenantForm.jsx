import { useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { QUOTA_INPUTS, LIMIT_INPUTS, toForm, fromForm } from '../lib/tenant-usage';

// GO-054: a global admin sets a tenant's quota and request limits. An empty
// field is no quota limit, or the server default for a request limit; -1
// lifts a request limit's default for this tenant. Saving sends both parts;
// a part with nothing set is removed on the server.

/** One fieldset of number inputs. */
function NumberFields({ legend, hint, inputs, values, onChange, prefix }) {
  return (
    <fieldset className="rounded border border-line p-3">
      <legend className="px-1 text-sm font-semibold text-fg">{legend}</legend>
      <p className="mb-2 text-xs text-fg-subtle">{hint}</p>
      <div className="grid gap-3 sm:grid-cols-2">
        {inputs.map((f) => (
          <div key={f.name}>
            <label htmlFor={`${prefix}-${f.name}`} className="mb-1 block text-xs text-fg-muted">{f.label}</label>
            <input
              id={`${prefix}-${f.name}`}
              type="number"
              step={f.share ? '0.01' : '1'}
              value={values[f.name]}
              onChange={(e) => onChange(f.name, e.target.value)}
              className="w-full rounded border border-line bg-surface px-2 py-1 text-sm text-fg"
            />
          </div>
        ))}
      </div>
    </fieldset>
  );
}

export default function TenantForm({ report, onSaved }) {
  const [quota, setQuota] = useState(() => toForm(QUOTA_INPUTS, report.quota));
  const [limits, setLimits] = useState(() => toForm(LIMIT_INPUTS, report.limits));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const [message, setMessage] = useState(null);

  const run = async (action, done) => {
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      await action();
      setMessage(done);
      await onSaved();
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  const save = (e) => {
    e.preventDefault();
    const q = fromForm(QUOTA_INPUTS, quota, 0);
    const l = fromForm(LIMIT_INPUTS, limits, -1);
    const invalid = q.error || l.error;
    if (invalid) {
      setError(invalid);
      setMessage(null);
      return;
    }
    void run(() => mddbClient.setTenant(report.tenant, { quota: q.part, limits: l.part }), `Saved the quota and limits of ${report.tenant}.`);
  };

  const remove = () => {
    void run(() => mddbClient.deleteTenant(report.tenant), `${report.tenant} has no quota or limits of its own any more.`);
  };

  return (
    <form onSubmit={save} aria-labelledby="tenant-form" className="space-y-3 rounded border border-line bg-surface p-4">
      <h3 id="tenant-form" className="text-sm font-semibold text-fg">Set the quota and limits of {report.tenant}</h3>
      <NumberFields
        legend="Quota (all collections together)"
        hint="Empty or 0: no limit. A write past a storage limit is refused with 507; an exhausted embedding budget makes embeddings wait until midnight UTC."
        inputs={QUOTA_INPUTS}
        values={quota}
        onChange={(name, v) => setQuota((prev) => ({ ...prev, [name]: v }))}
        prefix="tq"
      />
      <NumberFields
        legend="Request limits"
        hint="Empty or 0: the server default. -1: no limit for this tenant whatever the default. A request past a limit is refused with 429 and Retry-After."
        inputs={LIMIT_INPUTS}
        values={limits}
        onChange={(name, v) => setLimits((prev) => ({ ...prev, [name]: v }))}
        prefix="tl"
      />
      {error && <p role="alert" className="rounded border border-danger-line bg-danger-soft p-2 text-sm text-danger-strong">{error}</p>}
      {message && <output className="block rounded border border-success-line bg-success-soft p-2 text-sm text-success-strong">{message}</output>}
      <div className="flex gap-2">
        <button type="submit" disabled={busy} className="rounded bg-primary-700 px-3 py-1.5 text-sm text-white hover:bg-primary-800 disabled:opacity-50">
          Save
        </button>
        <button type="button" onClick={remove} disabled={busy} className="rounded border border-line px-3 py-1.5 text-sm text-danger-strong hover:bg-danger-soft disabled:opacity-50">
          Remove its quota and limits
        </button>
      </div>
    </form>
  );
}
