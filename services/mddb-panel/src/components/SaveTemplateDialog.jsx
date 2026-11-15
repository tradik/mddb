import { useId, useState } from 'react';
import { Loader2 } from 'lucide-react';
import ApiErrorNotice from './ApiErrorNotice';
import { saveTemplate, templateCollection } from '../lib/custom-templates';

/**
 * SaveTemplateDialog is "Save as template" (FE-018): the open document
 * becomes a template in `_templates` under a name (its key by default), with
 * its metadata as the template's defaults unless that box is cleared. The
 * write needs write permission on `_templates`; a refusal shows as "Not permitted".
 */
export default function SaveTemplateDialog({ document, onClose }) {
  const id = useId();
  const [name, setName] = useState(document.key);
  const [includeMeta, setIncludeMeta] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState(null);
  const [saved, setSaved] = useState(null);

  const save = async (e) => {
    e.preventDefault();
    setSaving(true);
    setError(null);
    try {
      await saveTemplate({ name, document, includeMeta });
      setSaved(name.trim());
    } catch (err) {
      setError(err);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <dialog open aria-modal="true" aria-labelledby={`${id}-title`} className="relative m-0 border-0 bg-surface text-fg rounded-lg shadow-xl w-full max-w-md mx-4 p-6 space-y-4">
        <h3 id={`${id}-title`} className="text-lg font-semibold text-fg">Save as template</h3>
        {saved ? (
          <>
            <output className="block text-sm text-success-strong">
              Saved "{saved}" to {templateCollection}. It is offered in New Document on every panel of this server.
            </output>
            <div className="flex justify-end">
              <button type="button" onClick={onClose} className="px-4 py-2 text-sm text-white bg-primary-700 rounded-lg hover:bg-primary-800">Close</button>
            </div>
          </>
        ) : (
          <form onSubmit={save} className="space-y-4">
            <p className="text-sm text-fg-muted">
              Templates are documents in the {templateCollection} collection. A template with the same name and language is replaced; its earlier version stays in the revision history.
            </p>
            <div>
              <label htmlFor={`${id}-name`} className="block text-sm font-medium text-fg-soft mb-1">Template name</label>
              <input
                id={`${id}-name`}
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="w-full px-3 py-2 border border-line-strong rounded-lg text-sm"
              />
            </div>
            <label className="flex items-center gap-2 text-sm text-fg-soft">
              <input type="checkbox" checked={includeMeta} onChange={(e) => setIncludeMeta(e.target.checked)} />
              <span>Keep this document's metadata as the template's defaults</span>
            </label>
            <ApiErrorNotice error={error} collection={templateCollection} />
            <div className="flex justify-end gap-3">
              <button type="button" onClick={onClose} className="px-4 py-2 text-sm text-fg-soft bg-surface-subtle rounded-lg hover:bg-surface-strong">Cancel</button>
              <button
                type="submit"
                disabled={saving || !name.trim()}
                className="flex items-center gap-2 px-4 py-2 text-sm text-white bg-primary-700 rounded-lg hover:bg-primary-800 disabled:opacity-50"
              >
                {saving && <Loader2 className="w-4 h-4 animate-spin" />}
                {saving ? 'Saving...' : 'Save template'}
              </button>
            </div>
          </form>
        )}
      </dialog>
    </div>
  );
}
