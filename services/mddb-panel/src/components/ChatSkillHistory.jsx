import { useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { formatBytes, sourceLabel } from '../lib/chat-skill';
import LineDiff from './LineDiff';

// #404: who uses a skill and its kept earlier versions (the server keeps the
// last 10). "Compare" reads one version and diffs it against the current one.

export default function ChatSkillHistory({ skill, onError }) {
  const [older, setOlder] = useState(null);
  const used = skill.usedBy || [];
  const history = skill.history || [];

  const compare = async (version) => {
    try {
      setOlder(await mddbClient.getChatSkill(skill.name, version));
    } catch (err) {
      onError(err.message);
    }
  };

  return (
    <section aria-labelledby="skill-history-heading" className="rounded-lg bg-white p-4 shadow">
      <h3 className="text-sm font-semibold text-gray-900">Used by</h3>
      {used.length === 0 ? (
        <p className="text-sm text-gray-700">No scenario attaches this skill.</p>
      ) : (
        <ul className="list-inside list-disc text-sm text-gray-800">
          {used.map((name) => <li key={name}>{name}</li>)}
        </ul>
      )}
      <h3 id="skill-history-heading" className="mt-3 text-sm font-semibold text-gray-900">Earlier versions</h3>
      {history.length === 0 ? (
        <p className="text-sm text-gray-700">This is the first version.</p>
      ) : (
        <ul className="space-y-1 text-sm text-gray-800">
          {history.map((r) => (
            <li key={r.version} className="flex items-center justify-between gap-2">
              <span>v{r.version} · {formatBytes(r.bytes)} · {sourceLabel(r.source)}</span>
              <button type="button" onClick={() => compare(r.version)} className="rounded px-2 py-0.5 text-xs font-medium text-primary-800 hover:bg-primary-50">
                Compare v{r.version} with v{skill.version}
              </button>
            </li>
          ))}
        </ul>
      )}
      {older && <LineDiff label={`v${older.version} → v${skill.version}`} before={older.content} after={skill.content} />}
    </section>
  );
}
