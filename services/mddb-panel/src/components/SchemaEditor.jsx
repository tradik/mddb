import { useEffect, useState } from 'react';
import { Save, Trash2 } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { parseJson, jsonObjectError } from '../lib/json-text';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';
import ConfirmDialog from './ConfirmDialog';
import JsonTextarea from './JsonTextarea';

/** The server's compact schema string, indented for editing. */
const pretty = (text) => {
  const { value, error } = parseJson(text);
  return error || value === undefined ? text : JSON.stringify(value, null, 2);
};

/** Shown when the field holds valid JSON that is not an object. */
const NOT_OBJECT = 'A schema is a JSON object, e.g. {"required": ["title"]}.';

/**
 * SchemaEditor edits one collection's metadata schema (/v1/schema/get and
 * /v1/schema/set): JSON checked as it is typed, saved as the string the
 * server expects, or deleted after a confirmation naming the collection —
 * which stops validation; stored documents are not touched.
 */
export default function SchemaEditor({ collection, onChanged }) {
  const [text, setText] = useState('');
  const [enabled, setEnabled] = useState(false);
  const [loadError, setLoadError] = useState(null);
  const [saved, setSaved] = useState(null);
  const [confirming, setConfirming] = useState(false);
  const { busy, error, run } = useAction();

  useEffect(() => {
    mddbClient.getSchema(collection)
      .then((data) => {
        setText(pretty(data.schema || ''));
        setEnabled(Boolean(data.enabled));
        setLoadError(null);
      })
      .catch(setLoadError);
  }, [collection]);

  const invalid = jsonObjectError(text, NOT_OBJECT);

  const save = () => run(async () => {
    await mddbClient.setSchema(collection, JSON.stringify(parseJson(text).value));
    setEnabled(true);
    setSaved(`Schema of ${collection} saved; new and updated documents are validated against it.`);
    onChanged();
  });

  const remove = () => run(async () => {
    await mddbClient.deleteSchema(collection);
    setConfirming(false);
    setText('');
    setEnabled(false);
    setSaved(`Schema of ${collection} deleted; validation is off.`);
    onChanged();
  });

  if (loadError) return <ApiErrorNotice error={loadError} collection={collection} />;

  return (
    <div className="space-y-3">
      <p className="text-sm text-gray-800">
        {enabled ? 'Validation is on for this collection.' : 'This collection has no schema; documents are not validated.'}
      </p>
      <JsonTextarea
        label={`Schema of ${collection}`}
        value={text}
        onChange={(value) => { setText(value); setSaved(null); }}
        error={invalid}
        rows={14}
        placeholder={'{\n  "required": ["category"],\n  "properties": {\n    "category": { "type": "string", "enum": ["blog", "news"] }\n  }\n}'}
        hint="Rules for metadata: required keys, and per key a type, enum, pattern or minItems/maxItems. See docs/SCHEMA-VALIDATION.md."
      />
      <ApiErrorNotice error={error} collection={collection} />
      {saved && <p role="status" className="text-sm text-green-800">{saved}</p>}
      <div className="flex gap-2">
        <button
          type="button"
          onClick={save}
          disabled={busy || Boolean(invalid) || !text.trim()}
          className="inline-flex items-center gap-1 px-4 py-2 bg-blue-700 text-white rounded-lg hover:bg-blue-800 disabled:opacity-50 text-sm"
        >
          <Save className="w-4 h-4" aria-hidden="true" /> Save schema
        </button>
        {enabled && (
          <button
            type="button"
            onClick={() => setConfirming(true)}
            className="inline-flex items-center gap-1 px-4 py-2 text-red-800 border border-red-300 rounded-lg hover:bg-red-50 text-sm"
          >
            <Trash2 className="w-4 h-4" aria-hidden="true" /> Delete schema
          </button>
        )}
      </div>
      {confirming && (
        <ConfirmDialog
          title="Delete schema"
          message={<>Delete the schema of <strong>{collection}</strong>? Validation stops; stored documents are kept as they are.</>}
          busy={busy}
          onConfirm={remove}
          onCancel={() => setConfirming(false)}
        />
      )}
    </div>
  );
}
