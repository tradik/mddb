import { useState } from 'react';
import { PackagePlus, Plus, X } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { saveBlob } from '../lib/download';
import { useAction } from '../lib/use-action';
import { buildRequest, packFileName } from '../lib/corpus-packs';
import ApiErrorNotice from './ApiErrorNotice';

// FE-022: create a pack from one collection or several and download it.
// Several collections need a pack name; each keeps its own name on install
// and may carry its own licence. The build streams from the server, so a
// large pack shows a busy state until the download starts.

const inputClass = 'w-full px-2 py-1.5 border border-line-strong rounded text-sm bg-surface text-fg focus:outline-none focus:ring-2 focus:ring-focus';
const emptyForm = { name: '', version: '', description: '', license: '', sources: [], licenses: {} };

/** An id for a new source row, unused by the rows there are; it keys the React list. */
const nextRowId = (sources) => sources.reduce((max, s) => Math.max(max, s.id), -1) + 1;

function SourceRows({ sources, onChange }) {
  const set = (id, field, value) => onChange(sources.map((s) => (s.id === id ? { ...s, [field]: value } : s)));
  const add = () => onChange([...sources, { id: nextRowId(sources), repo: '', commit: '', license: '' }]);
  return (
    <fieldset className="space-y-2">
      <legend className="text-sm text-fg-soft">Where the documents came from (optional)</legend>
      {sources.map((s, i) => (
        <div key={s.id} className="grid grid-cols-1 sm:grid-cols-[2fr_1fr_1fr_auto] gap-2">
          <input aria-label={`Source ${i + 1} address`} value={s.repo} onChange={(e) => set(s.id, 'repo', e.target.value)} placeholder="https://…" className={inputClass} />
          <input aria-label={`Source ${i + 1} version or commit`} value={s.commit} onChange={(e) => set(s.id, 'commit', e.target.value)} placeholder="version (optional)" className={inputClass} />
          <input aria-label={`Source ${i + 1} licence`} value={s.license} onChange={(e) => set(s.id, 'license', e.target.value)} placeholder="licence (default: the pack's)" className={inputClass} />
          <button type="button" onClick={() => onChange(sources.filter((r) => r.id !== s.id))} aria-label={`Remove source ${i + 1}`} className="justify-self-start rounded p-1.5 text-fg-soft hover:bg-surface-subtle">
            <X className="h-4 w-4" aria-hidden="true" />
          </button>
        </div>
      ))}
      <button type="button" onClick={add} className="flex items-center gap-1 text-sm text-info-strong hover:text-fg">
        <Plus className="h-4 w-4" aria-hidden="true" /> Add a source
      </button>
    </fieldset>
  );
}

/**
 * CorpusBuildForm; `collections` are the names to pick from, `preset` those
 * to start with (a new preset needs a new `key`).
 */
export default function CorpusBuildForm({ collections, preset }) {
  const { busy, error, run } = useAction();
  const [chosen, setChosen] = useState(preset || []);
  const [form, setForm] = useState(emptyForm);
  const several = chosen.length > 1;

  const toggle = (name) => setChosen(chosen.includes(name) ? chosen.filter((c) => c !== name) : [...chosen, name]);
  const field = (key) => ({ value: form[key], onChange: (e) => setForm({ ...form, [key]: e.target.value }) });
  const ready = chosen.length > 0 && form.license.trim() && (!several || form.name.trim());

  const submit = (e) => {
    e.preventDefault();
    run(async () => {
      const body = buildRequest({ ...form, collections: chosen });
      saveBlob(await mddbClient.buildCorpus(body), packFileName(body));
    });
  };

  return (
    <form onSubmit={submit} aria-labelledby="corpus-build" className="bg-surface rounded-lg shadow p-4 sm:p-6 space-y-3">
      <h3 id="corpus-build" className="flex items-center gap-2 text-lg font-semibold text-fg">
        <PackagePlus className="w-5 h-5 text-accent" aria-hidden="true" /> Create a pack
      </h3>
      <fieldset>
        <legend className="text-sm text-fg-soft mb-1">Collections to put in the pack</legend>
        {collections.length === 0 && <p className="text-sm text-fg-subtle">There are no collections yet.</p>}
        <div className="max-h-48 overflow-y-auto grid grid-cols-1 sm:grid-cols-2 gap-1">
          {collections.map((name) => (
            <label key={name} className="flex items-center gap-2 text-sm text-fg min-w-0">
              <input type="checkbox" checked={chosen.includes(name)} onChange={() => toggle(name)} />
              <span className="truncate" title={name}>{name}</span>
            </label>
          ))}
        </div>
      </fieldset>
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        {several && (
          <label className="text-sm text-fg-soft"><span>Pack name (required for several collections)</span>
            <input {...field('name')} placeholder="handbook" className={inputClass} />
          </label>
        )}
        <label className="text-sm text-fg-soft"><span>Version (optional)</span>
          <input {...field('version')} placeholder="1.0" className={inputClass} />
        </label>
        <label className="text-sm text-fg-soft sm:col-span-2"><span>Description (optional)</span>
          <input {...field('description')} className={inputClass} />
        </label>
        <label className="text-sm text-fg-soft sm:col-span-2"><span>Licence (required)</span>
          <input {...field('license')} placeholder="CC-BY-4.0" aria-describedby="corpus-license-why" className={inputClass} />
          <span id="corpus-license-why" className="block text-xs text-fg-subtle mt-1">
            A pack is made to be shared. Say under which terms its documents may be used — put in only what you may share.
            Servers refuse a pack that names no licence.
          </span>
        </label>
      </div>
      {several && (
        <fieldset className="space-y-2">
          <legend className="text-sm text-fg-soft">Licence of each collection, if it differs from the pack&apos;s</legend>
          {chosen.map((name) => (
            <input key={name} aria-label={`Licence of ${name}`} placeholder={`${name}: the pack's licence`} value={form.licenses[name] || ''}
              onChange={(e) => setForm({ ...form, licenses: { ...form.licenses, [name]: e.target.value } })} className={inputClass} />
          ))}
        </fieldset>
      )}
      <SourceRows sources={form.sources} onChange={(sources) => setForm({ ...form, sources })} />
      <ApiErrorNotice error={error} admin />
      <button type="submit" disabled={!ready || busy} className="flex items-center px-3 py-1.5 bg-primary-700 text-white text-sm rounded hover:bg-primary-800 disabled:opacity-50">
        <PackagePlus className="w-4 h-4 mr-1.5" aria-hidden="true" />
        {busy ? 'Creating…' : 'Create and download'}
      </button>
      {busy && <output className="block text-sm text-fg-muted">Taking a snapshot of the collections… a large pack takes a while before the download starts.</output>}
    </form>
  );
}
