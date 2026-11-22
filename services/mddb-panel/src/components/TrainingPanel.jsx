import { useCallback, useEffect, useState } from 'react';
import { GraduationCap, RefreshCw } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { saveBlob } from '../lib/download';
import { isActive, statusLabel, TRAINING_GUIDE_URL, TRAINING_POLL_MS } from '../lib/training';
import TrainingDatasetForm from './TrainingDatasetForm';
import TrainingDatasetSummary from './TrainingDatasetSummary';
import TrainingRunForm from './TrainingRunForm';
import TrainingRunDetail from './TrainingRunDetail';
import { cardClass, hintClass, secondaryButton } from './training-styles';

// The Model training page (module modelTraining, off by default; the menu
// hides it while off). MDDB builds the dataset and starts the trainer the
// server's operator configured; it trains nothing itself. While a job runs
// the page reads the lists again every two seconds.

export default function TrainingPanel() {
  const [info, setInfo] = useState(null);
  const [datasets, setDatasets] = useState([]);
  const [runs, setRuns] = useState([]);
  const [selected, setSelected] = useState(null);
  const [detail, setDetail] = useState(null);
  const [error, setError] = useState(null);

  const fail = (err) => setError(err.message);
  const load = useCallback(() => Promise
    .all([mddbClient.getTrainingProfiles(), mddbClient.listTrainingDatasets(), mddbClient.listTrainingRuns()])
    .then(([p, d, r]) => {
      setInfo(p);
      setDatasets(d.jobs);
      setRuns(r.jobs);
      setError(null);
    })
    .catch(fail), []);
  const loadDetail = useCallback((id) => mddbClient.getTrainingRun(id).then(setDetail).catch(fail), []);
  const select = (id) => {
    setSelected(id);
    void loadDetail(id);
  };

  useEffect(() => {
    load();
  }, [load]);

  const busy = datasets.some(isActive) || runs.some(isActive);
  useEffect(() => {
    if (!busy) return undefined;
    const timer = setInterval(() => {
      void load();
      if (selected) void loadDetail(selected);
    }, TRAINING_POLL_MS);
    return () => clearInterval(timer);
  }, [busy, selected, load, loadDetail]);

  // Runs an action and reads the lists again; a refusal is shown after the
  // reload, which would otherwise clear it.
  // A deleted run is not read again (reloadDetail false).
  const act = (action, reloadDetail = true) => async (...args) => {
    let failure = null;
    try {
      await action(...args);
    } catch (err) {
      failure = err.message;
    }
    await load();
    if (selected && reloadDetail) await loadDetail(selected);
    if (failure) setError(failure);
  };
  const download = act(async (fetchFile, name) => saveBlob(await fetchFile(), name));
  const removeRun = act(async (id) => {
    if (!confirm('Delete this run and its files, the result included?')) return;
    await mddbClient.deleteTrainingRun(id);
    setSelected(null);
    setDetail(null);
  }, false);
  const removeDataset = act(async (id) => {
    if (confirm('Delete this dataset and its files?')) await mddbClient.deleteTrainingDataset(id);
  });

  const profiles = info?.profiles || [];
  return (
    <div className="h-full overflow-y-auto bg-surface-muted p-4 sm:p-6">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <GraduationCap className="h-6 w-6 text-accent" aria-hidden="true" />
          <h2 className="text-xl font-bold text-fg">Model Training</h2>
        </div>
        <button type="button" onClick={() => void load()} className={secondaryButton}>
          <RefreshCw className="h-4 w-4" aria-hidden="true" /> Reload
        </button>
      </div>
      <section className={cardClass} aria-label="About model training">
        <p className="text-sm text-fg">
          Turn your collections into a training file, and start the training program that this server&apos;s operator set up.
          MDDB does not train models itself and adds no training software: it prepares the data, starts the program, keeps
          its log and hands you the file it produced. Searching what you already have needs no model at all.
        </p>
        <p className="mt-2 text-sm text-fg-soft">
          The module is on.{' '}
          {info && (info.configured
            ? `Trainers configured: ${profiles.map((p) => p.name).join(', ')}.`
            : 'No trainer is configured yet, so only datasets can be built.')}{' '}
          <a href={TRAINING_GUIDE_URL} target="_blank" rel="noopener noreferrer" className="text-accent underline">How to make a model file, step by step</a>
        </p>
        {info?.errors?.length > 0 && (
          <ul className="mt-2 text-sm text-warning-strong">
            {info.errors.map((e) => <li key={e}>Profile refused: {e}</li>)}
          </ul>
        )}
      </section>
      {error && <p role="alert" className="mb-4 rounded border border-danger-line bg-danger-soft p-2 text-sm text-danger-strong">{error}</p>}

      <TrainingDatasetForm onBuilt={() => void load()} />
      {datasets.length > 0 && (
        <section className={cardClass} aria-labelledby="training-datasets-title">
          <h3 id="training-datasets-title" className="text-lg font-semibold text-fg mb-2">Datasets</h3>
          <div className="grid gap-3">
            {datasets.map((d) => (
              <TrainingDatasetSummary key={d.id} dataset={d} onDelete={removeDataset}
                onDownload={(id, part) => download(() => mddbClient.downloadTrainingDataset(id, part), `${id}-${part}.jsonl`)} />
            ))}
          </div>
        </section>
      )}

      {info && <TrainingRunForm key={profiles.map((p) => p.name).join(',')} profiles={profiles} datasets={datasets} onStarted={(job) => { select(job.id); void load(); }} />}

      <section className={cardClass} aria-labelledby="training-runs-title">
        <h3 id="training-runs-title" className="text-lg font-semibold text-fg mb-2">Runs</h3>
        {runs.length === 0 ? <p className={hintClass}>No runs yet.</p> : (
          <ul className="divide-y divide-line">
            {runs.map((r) => (
              <li key={r.id}>
                <button type="button" onClick={() => select(r.id)} aria-pressed={selected === r.id}
                  className="flex w-full flex-wrap justify-between gap-2 py-2 text-left text-sm text-fg hover:bg-surface-subtle">
                  <span className="break-all">{r.name || r.id}{r.trial && ' (trial)'}</span>
                  <span className="text-fg-soft">{r.profile} · {statusLabel(r.status)}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
        {detail && (
          <TrainingRunDetail run={detail.job} log={detail.log}
            onCancel={act((id) => mddbClient.cancelTrainingRun(id))}
            onEvaluate={act((id) => mddbClient.evaluateTrainingRun(id))}
            onDownload={(id) => download(() => mddbClient.downloadTrainingResult(id), detail.job.result.file)}
            onDelete={removeRun} />
        )}
      </section>
    </div>
  );
}
