import { useState } from 'react';
import { Database } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import { emptyDatasetForm, withFormat, datasetRequest } from '../lib/training';
import { inputClass, labelClass, primaryButton, cardClass, hintClass } from './training-styles';

/**
 * Section 2 of the Model training page: build a dataset from one or several
 * collections. Text gives one record per document; pairs gives an input and
 * an output from fields of the document. onBuilt gets the new dataset job.
 */
export default function TrainingDatasetForm({ onBuilt }) {
  const { stats } = useStore();
  const [form, setForm] = useState(emptyDatasetForm);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const collections = (stats?.collections || []).map((c) => c.name);
  const set = (key) => (e) => setForm({ ...form, [key]: e.target.type === 'checkbox' ? e.target.checked : e.target.value });

  const submit = async (e) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await mddbClient.buildTrainingDataset(datasetRequest(form));
      onBuilt(res.job);
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className={cardClass} onSubmit={submit} aria-labelledby="training-dataset-title">
      <div className="flex items-center gap-2 mb-2">
        <Database className="w-5 h-5 text-accent" aria-hidden="true" />
        <h3 id="training-dataset-title" className="text-lg font-semibold text-fg">Build a dataset</h3>
      </div>
      <p className={`${hintClass} mb-4`}>
        The text of the chosen collections becomes a JSON Lines file, with a part held back for checking the result.
        Each record names its collection and, when the collection records one, its licence.
      </p>
      {error && <p role="alert" className="mb-3 rounded border border-danger-line bg-danger-soft p-2 text-sm text-danger-strong">{error}</p>}
      <div className="grid gap-3 md:grid-cols-2">
        <div>
          <label htmlFor="training-collections" className={labelClass}>Collections</label>
          <select id="training-collections" multiple className={`${inputClass} h-28`} value={form.collections}
            onChange={(e) => setForm({ ...form, collections: [...e.target.selectedOptions].map((o) => o.value) })}>
            {collections.map((c) => <option key={c} value={c}>{c}</option>)}
          </select>
        </div>
        <div className="grid gap-3">
          <div>
            <label htmlFor="training-format" className={labelClass}>Format</label>
            <select id="training-format" className={inputClass} value={form.format} onChange={(e) => setForm(withFormat(form, e.target.value))}>
              <option value="pairs">Pairs: an input and the output to learn</option>
              <option value="text">Text: one record per document</option>
            </select>
          </div>
          <div>
            <label htmlFor="training-name" className={labelClass}>Name (optional)</label>
            <input id="training-name" className={inputClass} value={form.name} onChange={set('name')} />
          </div>
        </div>
      </div>
      {form.format === 'pairs' && (
        <fieldset className="mt-3 grid gap-3 md:grid-cols-3">
          <legend className={labelClass}>Fields: {'{content}'}, {'{key}'}, {'{lang}'}, {'{collection}'} or {'{meta.name}'}</legend>
          <div>
            <label htmlFor="training-input" className={labelClass}>Input</label>
            <input id="training-input" className={inputClass} value={form.input} onChange={set('input')}
              placeholder="A {meta.cuisine} dish with {meta.ingredients}" />
          </div>
          <div>
            <label htmlFor="training-output" className={labelClass}>Output</label>
            <input id="training-output" className={inputClass} value={form.output} onChange={set('output')} />
          </div>
          <div>
            <label htmlFor="training-system" className={labelClass}>System text (optional)</label>
            <input id="training-system" className={inputClass} value={form.system} onChange={set('system')} />
          </div>
        </fieldset>
      )}
      <div className="mt-3 grid gap-3 grid-cols-2 md:grid-cols-4">
        <div>
          <label htmlFor="training-filter-key" className={labelClass}>Metadata key</label>
          <input id="training-filter-key" className={inputClass} value={form.filterKey} onChange={set('filterKey')} />
        </div>
        <div>
          <label htmlFor="training-filter-value" className={labelClass}>Value</label>
          <input id="training-filter-value" className={inputClass} value={form.filterValue} onChange={set('filterValue')} />
        </div>
        <div>
          <label htmlFor="training-lang" className={labelClass}>Language</label>
          <input id="training-lang" className={inputClass} value={form.lang} onChange={set('lang')} placeholder="any" />
        </div>
        <div>
          <label htmlFor="training-max" className={labelClass}>Most records</label>
          <input id="training-max" type="number" min="0" className={inputClass} value={form.maxRecords} onChange={set('maxRecords')} placeholder="all" />
        </div>
        <div>
          <label htmlFor="training-split" className={labelClass}>Validation share</label>
          <input id="training-split" type="number" min="0" max="0.5" step="0.05" className={inputClass} value={form.validationRatio} onChange={set('validationRatio')} />
        </div>
        <div>
          <label htmlFor="training-seed" className={labelClass}>Shuffle seed</label>
          <input id="training-seed" type="number" className={inputClass} value={form.seed} onChange={set('seed')} disabled={!form.shuffle} />
        </div>
        <label className="flex items-center gap-2 text-sm text-fg">
          <input type="checkbox" checked={form.shuffle} onChange={set('shuffle')} /> Shuffle
        </label>
        <label className="flex items-center gap-2 text-sm text-fg">
          <input type="checkbox" checked={form.dedupe} onChange={set('dedupe')} /> Remove exact duplicates
        </label>
      </div>
      <label className="mt-3 flex items-start gap-2 text-sm text-fg">
        <input type="checkbox" className="mt-1" checked={form.acknowledgeUnlicensed} onChange={set('acknowledgeUnlicensed')} />
        I have checked that I may train on collections that record no licence.
      </label>
      <button type="submit" className={`${primaryButton} mt-4`} disabled={busy || form.collections.length === 0}>
        {busy ? 'Building…' : 'Build dataset'}
      </button>
    </form>
  );
}
