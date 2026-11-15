import { useCallback, useEffect, useState } from 'react';
import { FileCheck } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { useStore } from '../lib/store';
import ApiErrorNotice from './ApiErrorNotice';
import CollectionPicker from './CollectionPicker';
import SchemaEditor from './SchemaEditor';
import SchemaValidateBox from './SchemaValidateBox';

/**
 * SchemasPanel is the Collection Schemas page: which collections validate
 * their metadata (/v1/schema/list), an editor for the schema of the chosen
 * collection and a dry-run "validate a document" box.
 */
export default function SchemasPanel() {
  const currentCollection = useStore((s) => s.currentCollection);
  const firstCollection = useStore((s) => s.stats?.collections?.[0]?.name);
  const [collection, setCollection] = useState(currentCollection || firstCollection || '');
  const [schemas, setSchemas] = useState([]);
  const [error, setError] = useState(null);

  const load = useCallback(() => mddbClient.listSchemas()
    .then((data) => {
      setSchemas(data.schemas || []);
      setError(null);
    })
    .catch(setError), []);

  useEffect(() => {
    load();
  }, [load]);

  return (
    <div className="h-full overflow-y-auto p-6 bg-surface-muted space-y-4">
      <div>
        <h2 className="text-xl font-bold text-fg flex items-center gap-2">
          <FileCheck className="w-5 h-5" aria-hidden="true" /> Collection Schemas
        </h2>
        <p className="text-sm text-fg-muted">
          A schema makes the server refuse documents whose metadata breaks its rules. Validation is opt-in per collection.
        </p>
      </div>
      <section aria-labelledby="schemas-list-title" className="bg-surface rounded-lg shadow p-4 space-y-2">
        <h3 id="schemas-list-title" className="text-base font-semibold text-fg">Collections with a schema</h3>
        <ApiErrorNotice error={error} />
        {!error && schemas.length === 0 && <p className="text-sm text-fg-muted">No collection has a schema.</p>}
        {schemas.length > 0 && (
          <ul className="flex flex-wrap gap-2">
            {schemas.map((s) => (
              <li key={s.collection}>
                <button
                  type="button"
                  onClick={() => setCollection(s.collection)}
                  aria-pressed={s.collection === collection}
                  className="px-3 py-1 rounded-full border border-info-line text-sm text-info-strong hover:bg-info-soft aria-pressed:bg-info-muted"
                >
                  {s.collection}
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>
      <section aria-labelledby="schema-edit-title" className="bg-surface rounded-lg shadow p-4 space-y-3">
        <h3 id="schema-edit-title" className="text-base font-semibold text-fg">Edit a schema</h3>
        <div className="max-w-xs">
          <CollectionPicker value={collection} onChange={setCollection} placeholder="Choose a collection" />
        </div>
        {collection && <SchemaEditor key={collection} collection={collection} onChanged={load} />}
      </section>
      {collection && <SchemaValidateBox key={`validate-${collection}`} collection={collection} />}
    </div>
  );
}
