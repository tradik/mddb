import { useState } from 'react';
import { Play, FlaskConical } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { parameterDefaults, parameterRequest } from '../lib/training';
import TrainingParamField from './TrainingParamField';
import { inputClass, labelClass, primaryButton, secondaryButton, cardClass, hintClass } from './training-styles';

/**
 * Section 3 of the Model training page: start a run of a trainer profile on
 * a built dataset, with the parameters the profile declares. A trial run is
 * the same run on a small sample, to check the whole chain before a long one.
 */
export default function TrainingRunForm({ profiles, datasets, onStarted }) {
  const [profileName, setProfileName] = useState(profiles[0]?.name || '');
  const profile = profiles.find((p) => p.name === profileName);
  const [values, setValues] = useState(() => parameterDefaults(profile));
  const [datasetId, setDatasetId] = useState('');
  const [name, setName] = useState('');
  const [evaluate, setEvaluate] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const ready = datasets.filter((d) => d.status === 'completed');

  const pickProfile = (n) => {
    setProfileName(n);
    setValues(parameterDefaults(profiles.find((p) => p.name === n)));
  };

  const start = async (trial) => {
    setBusy(true);
    setError(null);
    try {
      const req = { profile: profileName, datasetId, parameters: parameterRequest(values), trial };
      if (name.trim()) req.name = name.trim();
      if (profile.evaluate) req.evaluate = evaluate;
      const res = await mddbClient.startTrainingRun(req);
      onStarted(res.job);
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  if (profiles.length === 0) {
    return (
      <section className={cardClass} aria-labelledby="training-run-title">
        <h3 id="training-run-title" className="text-lg font-semibold text-fg mb-2">Start a run</h3>
        <p className="text-sm text-fg-soft">
          No trainer is configured on this server. Its operator lists trainer commands in a profile file
          (MDDB_TRAINERS_CONFIG); until then you can build and download datasets.
        </p>
      </section>
    );
  }
  const disabled = busy || !datasetId;
  return (
    <section className={cardClass} aria-labelledby="training-run-title">
      <h3 id="training-run-title" className="text-lg font-semibold text-fg mb-2">Start a run</h3>
      {error && <p role="alert" className="mb-3 rounded border border-danger-line bg-danger-soft p-2 text-sm text-danger-strong">{error}</p>}
      <div className="grid gap-3 md:grid-cols-3">
        <div>
          <label htmlFor="training-profile" className={labelClass}>Trainer</label>
          <select id="training-profile" className={inputClass} value={profileName} onChange={(e) => pickProfile(e.target.value)}>
            {profiles.map((p) => <option key={p.name} value={p.name}>{p.name}</option>)}
          </select>
          {profile.description && <p className={`${hintClass} mt-1`}>{profile.description}</p>}
        </div>
        <div>
          <label htmlFor="training-run-dataset" className={labelClass}>Dataset</label>
          <select id="training-run-dataset" className={inputClass} value={datasetId} onChange={(e) => setDatasetId(e.target.value)}>
            <option value="">Choose a built dataset</option>
            {ready.map((d) => <option key={d.id} value={d.id}>{d.name || d.id} ({d.summary?.records} records)</option>)}
          </select>
        </div>
        <div>
          <label htmlFor="training-run-name" className={labelClass}>Name (optional)</label>
          <input id="training-run-name" className={inputClass} value={name} onChange={(e) => setName(e.target.value)} />
        </div>
      </div>
      {profile.parameters?.length > 0 && (
        <div className="mt-3 grid gap-3 grid-cols-1 sm:grid-cols-2 md:grid-cols-3">
          {profile.parameters.map((p) => (
            <TrainingParamField key={p.name} param={p} value={values[p.name]} idPrefix="training-param"
              onChange={(k, v) => setValues({ ...values, [k]: v })} />
          ))}
        </div>
      )}
      {profile.evaluate && (
        <label className="mt-3 flex items-center gap-2 text-sm text-fg">
          <input type="checkbox" checked={evaluate} onChange={(e) => setEvaluate(e.target.checked)} />
          Score the model {profile.evaluate.baseline ? 'before and after' : 'after'} training on the validation part
        </label>
      )}
      <div className="mt-4 flex flex-wrap items-center gap-2">
        <button type="button" className={primaryButton} disabled={disabled} onClick={() => start(false)}>
          <Play className="h-4 w-4" aria-hidden="true" /> Start
        </button>
        <button type="button" className={secondaryButton} disabled={disabled} onClick={() => start(true)}>
          <FlaskConical className="h-4 w-4" aria-hidden="true" /> Trial run
        </button>
      </div>
      <p className={`${hintClass} mt-2`}>
        A trial run is a quick check from start to finish on a small sample ({profile.trialRecords} records), with the
        trainer&apos;s trial settings, before you commit to a long run.
      </p>
    </section>
  );
}
