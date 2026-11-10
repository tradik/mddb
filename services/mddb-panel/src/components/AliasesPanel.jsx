import { useEffect, useState } from 'react';
import { Link2, Trash2, ArrowRight } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';

// GO-044 (2.18.8): an alias is a stable name for a collection. Clients use the
// alias; moving it to another collection (a rebuilt one with a new embedding
// model, say) is one atomic step, so no client changes a name. The server
// lists only aliases whose collection the caller may read; creating or moving
// one needs write on the new target and on the old one.

const inputClass = 'w-full px-2 py-1.5 border border-gray-300 rounded text-sm focus:outline-none focus:ring-2 focus:ring-primary-500';

/** Aliases view: list, create or move, delete. */
export default function AliasesPanel() {
  const { stats } = useStore();
  const [aliases, setAliases] = useState([]);
  const [form, setForm] = useState({ alias: '', collection: '' });
  const [error, setError] = useState(null);
  const [message, setMessage] = useState(null);
  const [busy, setBusy] = useState(false);

  const load = () => mddbClient
    .listAliases()
    .then((data) => setAliases(data.aliases || []))
    .catch((err) => setError(err.message));

  useEffect(() => {
    load();
  }, []);

  // Runs one change, reports it and reloads the list; a refusal is shown.
  const run = async (action) => {
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      setMessage(await action());
      await load();
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  const save = (e) => {
    e.preventDefault();
    const alias = form.alias.trim();
    const collection = form.collection.trim();
    const moved = aliases.some((a) => a.alias === alias);
    run(async () => {
      await mddbClient.setAlias(alias, collection);
      setForm({ alias: '', collection: '' });
      return `${moved ? 'Moved' : 'Created'} alias ${alias} → ${collection}.`;
    });
  };

  const remove = (alias) => {
    if (!confirm(`Delete the alias "${alias}"? Its collection stays; clients using the alias get "not found".`)) return;
    run(async () => {
      await mddbClient.deleteAlias(alias);
      return `Deleted alias ${alias}.`;
    });
  };

  const collections = (stats?.collections || []).map((c) => c.name);

  return (
    <div className="flex-1 overflow-y-auto p-6 bg-gray-50">
      <div className="max-w-4xl mx-auto">
        <div className="bg-white rounded-lg shadow p-6">
          <div className="flex items-center mb-2">
            <Link2 className="w-5 h-5 text-primary-600 mr-2" aria-hidden="true" />
            <h2 className="text-lg font-semibold text-gray-900">Collection aliases</h2>
          </div>
          <p className="text-sm text-gray-600 mb-4">
            An alias is a stable name for a collection: REST, gRPC, MCP and GraphQL accept it wherever they take a
            collection. Moving an alias to another collection is atomic — a running search sees the old collection or
            the new one, never both. An alias cannot take the name of an existing collection or point at another
            alias, and deleting it leaves its collection.
          </p>

          {error && <p role="alert" className="mb-3 rounded border border-red-200 bg-red-50 p-2 text-sm text-red-800">{error}</p>}
          {message && <output className="mb-3 block rounded border border-green-200 bg-green-50 p-2 text-sm text-green-800">{message}</output>}

          {aliases.length === 0 ? (
            <p className="text-sm text-gray-600">No aliases.</p>
          ) : (
            <ul className="divide-y divide-gray-200 mb-4">
              {aliases.map((a) => (
                <li key={a.alias} className="py-2 flex items-center justify-between gap-3">
                  <span className="flex items-center gap-2 text-sm">
                    <span className="font-medium text-gray-900">{a.alias}</span>
                    <ArrowRight className="w-4 h-4 text-gray-600" aria-label="points at" />
                    <span className="text-gray-700">{a.collection}</span>
                  </span>
                  <button
                    type="button"
                    onClick={() => remove(a.alias)}
                    disabled={busy}
                    aria-label={`Delete alias ${a.alias}`}
                    className="rounded p-1.5 text-red-700 hover:bg-red-50 disabled:opacity-50"
                  >
                    <Trash2 className="h-4 w-4" />
                  </button>
                </li>
              ))}
            </ul>
          )}

          <form onSubmit={save} className="mt-4 grid gap-2 md:grid-cols-[1fr_1fr_auto] items-end">
            <div>
              <label htmlFor="alias-name" className="block text-xs text-gray-600">Alias</label>
              <input
                id="alias-name"
                value={form.alias}
                onChange={(e) => setForm({ ...form, alias: e.target.value })}
                placeholder="docs"
                className={inputClass}
              />
            </div>
            <div>
              <label htmlFor="alias-collection" className="block text-xs text-gray-600">Collection</label>
              <input
                id="alias-collection"
                list="alias-collections"
                value={form.collection}
                onChange={(e) => setForm({ ...form, collection: e.target.value })}
                placeholder="docs-v2"
                className={inputClass}
              />
              <datalist id="alias-collections">
                {collections.map((name) => <option key={name} value={name} />)}
              </datalist>
            </div>
            <button
              type="submit"
              disabled={busy || !form.alias.trim() || !form.collection.trim()}
              className="px-3 py-1.5 bg-primary-700 text-white text-sm rounded hover:bg-primary-800 disabled:opacity-50"
            >
              Save alias
            </button>
          </form>
          <p className="mt-2 text-xs text-gray-600">Saving an existing alias moves it to the new collection.</p>
        </div>
      </div>
    </div>
  );
}
