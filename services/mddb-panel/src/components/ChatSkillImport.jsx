import { useState } from 'react';
import { Download, Save } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { inputClass, labelClass } from '../lib/chat-scenario';
import { formatBytes, sourceLabel } from '../lib/chat-skill';

// #404: import a SKILL.md from an https URL. The server fetches it through
// its SSRF guard and answers with a preview; the whole text is shown here,
// front matter included, before anything is saved — a skill becomes part of
// system prompts, so an operator reads what they let in. A GitHub URL comes
// back pinned to a commit.

export default function ChatSkillImport({ busy, onSave, onError }) {
  const [url, setUrl] = useState('');
  const [preview, setPreview] = useState(null);
  const [name, setName] = useState('');
  const [fetching, setFetching] = useState(false);

  const fetchPreview = async (e) => {
    e.preventDefault();
    setFetching(true);
    setPreview(null);
    try {
      const p = await mddbClient.importChatSkill({ url: url.trim() });
      setPreview(p);
      setName(p.name || '');
    } catch (err) {
      onError(err.message);
    } finally {
      setFetching(false);
    }
  };

  const save = () => onSave({ name, description: preview.description || '', content: preview.content, source: preview.source });

  return (
    <div className="space-y-3 rounded-lg bg-white p-4 shadow">
      <form onSubmit={fetchPreview} className="flex items-end gap-2">
        <div className="flex-1">
          <label htmlFor="skill-import-url" className={labelClass}>SKILL.md URL (https)</label>
          <input
            id="skill-import-url"
            type="url"
            required
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder="https://github.com/owner/repo/blob/main/SKILL.md"
            className={inputClass}
          />
        </div>
        <button type="submit" disabled={fetching} className="flex items-center gap-1.5 rounded border border-gray-300 px-3 py-1.5 text-sm text-gray-800 hover:bg-gray-100 disabled:opacity-50">
          <Download className="h-4 w-4" aria-hidden="true" /> {fetching ? 'Fetching…' : 'Fetch preview'}
        </button>
      </form>
      <p className="text-xs text-gray-600">Nothing is saved until you read the text below and press Save.</p>

      {preview && (
        <section aria-labelledby="skill-import-heading" className="space-y-2">
          <h3 id="skill-import-heading" className="text-sm font-semibold text-gray-900">Preview</h3>
          <p className="text-xs text-gray-700">
            {sourceLabel(preview.source)} · instructions {formatBytes(preview.bytes)}
            {preview.description && ` · ${preview.description}`}
          </p>
          {preview.exists && (
            <p role="status" className="rounded border border-amber-300 bg-amber-50 p-2 text-xs text-amber-900">
              A skill named {preview.name} exists: saving under that name stores a new version of it.
            </p>
          )}
          <pre aria-label="The whole fetched text" tabIndex={0} className="max-h-96 overflow-auto whitespace-pre-wrap rounded bg-gray-50 p-3 text-xs text-gray-900">{preview.raw}</pre>
          <div className="flex items-end gap-2">
            <div className="flex-1">
              <label htmlFor="skill-import-name" className={labelClass}>Save as</label>
              <input id="skill-import-name" value={name} onChange={(e) => setName(e.target.value)} className={inputClass} />
            </div>
            <button type="button" onClick={save} disabled={busy} className="flex items-center gap-1.5 rounded bg-primary-700 px-3 py-1.5 text-sm text-white hover:bg-primary-800 disabled:opacity-50">
              <Save className="h-4 w-4" aria-hidden="true" /> Save
            </button>
          </div>
        </section>
      )}
    </div>
  );
}
