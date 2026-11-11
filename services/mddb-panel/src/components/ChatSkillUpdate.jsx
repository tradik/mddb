import { useState } from 'react';
import { Check, RefreshCw } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { sourceLabel } from '../lib/chat-skill';
import LineDiff from './LineDiff';

// #404: "Update from source" — the only way an imported skill is fetched
// again. The server answers with what the source holds now; the change is
// shown as a line diff, and only a confirm stores it, naming the hash of the
// text shown so a source that moved in between is refused.

export default function ChatSkillUpdate({ skill, onSaved, onError }) {
  const [result, setResult] = useState(null);
  const [busy, setBusy] = useState(false);

  const call = async (options) => {
    setBusy(true);
    try {
      const out = await mddbClient.updateChatSkill(skill.name, options);
      if (out.saved) {
        setResult(null);
        onSaved(`Updated ${skill.name} to version ${out.skill.version} from ${sourceLabel(out.preview.source)}.`);
      } else {
        setResult(out);
      }
    } catch (err) {
      onError(err.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section aria-labelledby="skill-update-heading" className="rounded-lg bg-white p-4 shadow">
      <div className="flex items-center justify-between gap-2">
        <h3 id="skill-update-heading" className="text-sm font-semibold text-gray-900">Source</h3>
        <button type="button" onClick={() => call({})} disabled={busy} className="flex items-center gap-1 rounded border border-gray-300 px-2 py-1 text-xs font-medium text-gray-800 hover:bg-gray-100 disabled:opacity-50">
          <RefreshCw className="h-3 w-3" aria-hidden="true" /> {busy ? 'Fetching…' : 'Update from source'}
        </button>
      </div>
      <p className="mt-1 text-xs text-gray-700">
        {sourceLabel(skill.source)}, fetched {skill.source.fetchedAt}. It is fetched again only when you ask.
      </p>
      {result && !result.changed && (
        <p role="status" className="mt-2 text-sm text-gray-800">The source holds the stored text: nothing to update.</p>
      )}
      {result && result.changed && (
        <div className="mt-2">
          <p className="text-xs text-gray-700">Now at {sourceLabel(result.preview.source)}.</p>
          {result.preview.description !== skill.description && (
            <LineDiff label="Description" before={skill.description} after={result.preview.description} />
          )}
          <LineDiff label="Instructions" before={skill.content} after={result.preview.content} />
          <button
            type="button"
            onClick={() => call({ confirm: true, sha256: result.preview.sha256 })}
            disabled={busy}
            className="mt-2 flex items-center gap-1.5 rounded bg-primary-700 px-3 py-1.5 text-sm text-white hover:bg-primary-800 disabled:opacity-50"
          >
            <Check className="h-4 w-4" aria-hidden="true" /> Save as version {skill.version + 1}
          </button>
        </div>
      )}
    </section>
  );
}
