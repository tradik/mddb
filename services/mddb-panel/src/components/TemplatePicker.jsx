import { useEffect, useId, useState } from 'react';
import { builtinTemplates, loadCustomTemplates, templateCollection } from '../lib/custom-templates';

const builtins = builtinTemplates();

/**
 * TemplatePicker is the "Start from template" select of a new document
 * (FE-018): the built-in templates and the team's own from `_templates`.
 * Picking one calls `onPick({ contentMd, meta })`. When the custom ones
 * cannot be read — no read permission on `_templates`, server error — the
 * built-in ones are still offered, with a note saying why the rest are missing.
 */
export default function TemplatePicker({ onPick }) {
  const id = useId();
  const [custom, setCustom] = useState(null);
  const [error, setError] = useState(null);
  const [value, setValue] = useState('');

  useEffect(() => {
    let live = true;
    loadCustomTemplates().then((result) => {
      if (!live) return;
      setCustom(result.templates);
      setError(result.error);
    });
    return () => { live = false; };
  }, []);

  const choose = (e) => {
    setValue(e.target.value);
    const picked = [...builtins, ...(custom || [])].find((t) => t.id === e.target.value);
    if (picked) onPick({ contentMd: picked.contentMd, meta: picked.meta });
  };

  return (
    <div>
      <label htmlFor={id} className="block text-sm font-medium text-gray-700 mb-2">
        Start from template
      </label>
      <select
        id={id}
        value={value}
        onChange={choose}
        className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm bg-white focus:outline-none focus:ring-2 focus:ring-primary-500"
      >
        <option value="">Blank document</option>
        <optgroup label="Built-in">
          {builtins.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
        </optgroup>
        {custom === null && <option disabled value="loading">Loading custom templates…</option>}
        {custom?.length > 0 && (
          <optgroup label={`Custom (${templateCollection})`}>
            {custom.map((t) => <option key={t.id} value={t.id}>{t.name} ({t.lang})</option>)}
          </optgroup>
        )}
      </select>
      <p className="text-xs text-gray-600 mt-1">
        A template replaces the content and the metadata below.
      </p>
      {error && (
        <p role="status" className="text-xs text-amber-900 mt-1">
          {error.status === 403
            ? `Custom templates are not shown: you have no read permission on ${templateCollection}.`
            : `Custom templates could not be loaded: ${error.message}`}
        </p>
      )}
    </div>
  );
}
