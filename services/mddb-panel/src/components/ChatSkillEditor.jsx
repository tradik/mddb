import { useState } from 'react';
import { FileText, Save, Trash2 } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { inputClass, labelClass } from '../lib/chat-scenario';
import { toSkillForm, sourceLabel } from '../lib/chat-skill';

// #404: a skill's fields. Paste a whole SKILL.md into the instructions and
// "Read front matter" asks the server to split it — its parser, not one
// written here — into name, description and body. Editing the instructions
// of an imported skill detaches it from its source: the text is no longer
// what the source holds.

export default function ChatSkillEditor({ skill, isNew, busy, onSave, onDelete, onError }) {
  const [form, setForm] = useState(() => toSkillForm(skill));
  const set = (field, value) => setForm((f) => ({ ...f, [field]: value }));

  const readFrontMatter = async () => {
    try {
      const p = await mddbClient.importChatSkill({ content: form.content });
      setForm((f) => ({ ...f, name: isNew && p.name ? p.name : f.name, description: p.description || f.description, content: p.content }));
    } catch (err) {
      onError(err.message);
    }
  };

  const submit = (e) => {
    e.preventDefault();
    onSave(form);
  };

  return (
    <form onSubmit={submit} className="space-y-3 rounded-lg bg-white p-4 shadow">
      <div className="grid gap-3 md:grid-cols-2">
        <div>
          <label htmlFor="skill-name" className={labelClass}>Name</label>
          <input
            id="skill-name"
            value={form.name}
            onChange={(e) => set('name', e.target.value)}
            disabled={!isNew}
            placeholder="humanizer"
            className={`${inputClass} disabled:bg-gray-100 disabled:text-gray-700`}
          />
        </div>
        <div>
          <label htmlFor="skill-description" className={labelClass}>Description</label>
          <input id="skill-description" value={form.description} onChange={(e) => set('description', e.target.value)} className={inputClass} />
        </div>
      </div>
      <div>
        <label htmlFor="skill-content" className={labelClass}>Instructions (Markdown, or a whole SKILL.md)</label>
        <textarea
          id="skill-content"
          rows={14}
          value={form.content}
          onChange={(e) => setForm((f) => ({ ...f, content: e.target.value, source: null }))}
          className={`${inputClass} font-mono`}
        />
        <p className="mt-1 text-xs text-gray-600">
          Source: {sourceLabel(form.source)}.{form.source && ' Editing the instructions detaches the skill from it.'}
        </p>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <button type="submit" disabled={busy} className="flex items-center gap-1.5 rounded bg-primary-700 px-3 py-1.5 text-sm text-white hover:bg-primary-800 disabled:opacity-50">
          <Save className="h-4 w-4" aria-hidden="true" /> {busy ? 'Working…' : 'Save'}
        </button>
        <button type="button" onClick={readFrontMatter} disabled={busy || !form.content.trim()} className="flex items-center gap-1.5 rounded border border-gray-300 px-3 py-1.5 text-sm text-gray-800 hover:bg-gray-100 disabled:opacity-50">
          <FileText className="h-4 w-4" aria-hidden="true" /> Read front matter
        </button>
        {!isNew && (
          <button type="button" onClick={onDelete} disabled={busy} className="flex items-center gap-1.5 rounded px-3 py-1.5 text-sm text-red-700 hover:bg-red-50 disabled:opacity-50">
            <Trash2 className="h-4 w-4" aria-hidden="true" /> Delete
          </button>
        )}
      </div>
    </form>
  );
}
