import { useState } from 'react';
import { CheckCircle2 } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { parseJson, jsonObjectError } from '../lib/json-text';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';
import JsonTextarea from './JsonTextarea';

/** Shown when the field holds valid JSON that is not an object. */
const NOT_OBJECT = 'Metadata is a JSON object of value lists, e.g. {"category": ["blog"]}.';

/**
 * SchemaValidateBox dry-runs a document's metadata against the collection's
 * schema (/v1/validate): nothing is stored; the server answers valid or not,
 * with its errors and warnings.
 */
export default function SchemaValidateBox({ collection }) {
  const [text, setText] = useState('{\n  "category": ["blog"]\n}');
  const [result, setResult] = useState(null);
  const { busy, error, run } = useAction();
  const invalid = jsonObjectError(text, NOT_OBJECT);

  const validate = () => run(async () => setResult(await mddbClient.validateMeta(collection, parseJson(text).value || {})));

  return (
    <section aria-labelledby="schema-validate-title" className="bg-white rounded-lg shadow p-4 space-y-3">
      <h3 id="schema-validate-title" className="text-base font-semibold text-gray-900">Validate a document</h3>
      <JsonTextarea
        label={`Metadata to check against ${collection}`}
        value={text}
        onChange={(value) => { setText(value); setResult(null); }}
        error={invalid}
        rows={6}
      />
      <button
        type="button"
        onClick={validate}
        disabled={busy || Boolean(invalid)}
        className="inline-flex items-center gap-1 px-4 py-2 bg-blue-700 text-white rounded-lg hover:bg-blue-800 disabled:opacity-50 text-sm"
      >
        <CheckCircle2 className="w-4 h-4" aria-hidden="true" /> Validate
      </button>
      <ApiErrorNotice error={error} collection={collection} />
      {result && (
        <div role="status" className="text-sm space-y-1">
          <p className={result.valid ? 'text-green-800 font-medium' : 'text-red-800 font-medium'}>
            {result.valid ? 'Valid: the server would accept this metadata.' : 'Invalid: the server would refuse this document.'}
          </p>
          {[...(result.errors || []).map((m) => ['error', m]), ...(result.warnings || []).map((m) => ['warning', m])].map(([kind, m]) => (
            <p key={`${kind}-${m}`} className={kind === 'error' ? 'text-red-800' : 'text-amber-900'}>{kind === 'error' ? 'Error' : 'Warning'}: {m}</p>
          ))}
        </div>
      )}
    </section>
  );
}
