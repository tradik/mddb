import { useState } from 'react';
import { ArrowDown, ArrowUp, Plus, X } from 'lucide-react';
import { labelClass, inputClass } from '../lib/chat-scenario';
import { moveItem } from '../lib/chat-skill';

// #404: the skills a scenario attaches, in the order they are composed into
// its prompt. Add one of the stored skills, move it up or down, remove it.
// Every control is a button or a select, so the picker works by keyboard.
//
// `available` is null when the skill list could not be read (it is admin
// only): the picker then still shows, orders and removes the names the
// scenario has, and says why nothing can be added.

const iconButton = 'rounded p-1 text-gray-700 hover:bg-gray-100 focus:outline-none focus:ring-2 focus:ring-primary-500 disabled:opacity-40';

export default function ChatSkillPicker({ selected, available, onChange }) {
  const known = new Set((available || []).map((s) => s.name));
  const addable = (available || []).filter((s) => !selected.includes(s.name));
  const [choice, setChoice] = useState('');
  const pick = choice && addable.some((s) => s.name === choice) ? choice : addable[0]?.name || '';

  return (
    <fieldset>
      <legend className={labelClass}>Skills, in prompt order</legend>
      {selected.length === 0 ? (
        <p className="text-sm text-gray-600">No skills attached.</p>
      ) : (
        <ol className="space-y-1 rounded border border-gray-300 p-2">
          {selected.map((name, i) => (
            <li key={name} className="flex items-center gap-2 text-sm text-gray-800">
              <span className="w-5 text-right text-xs text-gray-600">{i + 1}.</span>
              <span className="flex-1 truncate">{name}</span>
              {available && !known.has(name) && (
                <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs text-amber-900">missing</span>
              )}
              <button type="button" aria-label={`Move ${name} up`} disabled={i === 0} onClick={() => onChange(moveItem(selected, i, -1))} className={iconButton}>
                <ArrowUp className="h-4 w-4" aria-hidden="true" />
              </button>
              <button type="button" aria-label={`Move ${name} down`} disabled={i === selected.length - 1} onClick={() => onChange(moveItem(selected, i, 1))} className={iconButton}>
                <ArrowDown className="h-4 w-4" aria-hidden="true" />
              </button>
              <button type="button" aria-label={`Remove ${name}`} onClick={() => onChange(selected.filter((n) => n !== name))} className={iconButton}>
                <X className="h-4 w-4" aria-hidden="true" />
              </button>
            </li>
          ))}
        </ol>
      )}
      {available === null ? (
        <p className="mt-1 text-xs text-gray-600">The skill list needs admin on &quot;*&quot;; the attached names are kept as they are.</p>
      ) : (
        <div className="mt-2 flex items-end gap-2">
          <div className="flex-1">
            <label htmlFor="chat-skill-add" className={labelClass}>Add a skill</label>
            <select id="chat-skill-add" value={pick} onChange={(e) => setChoice(e.target.value)} disabled={addable.length === 0} className={inputClass}>
              {addable.length === 0 && <option value="">No other skills stored</option>}
              {addable.map((s) => (
                <option key={s.name} value={s.name}>{s.description ? `${s.name} — ${s.description}` : s.name}</option>
              ))}
            </select>
          </div>
          <button
            type="button"
            onClick={() => onChange([...selected, pick])}
            disabled={!pick}
            className="flex items-center gap-1 rounded border border-gray-300 px-2 py-1.5 text-sm text-gray-800 hover:bg-gray-100 disabled:opacity-50"
          >
            <Plus className="h-4 w-4" aria-hidden="true" /> Add
          </button>
        </div>
      )}
      <p className="mt-1 text-xs text-gray-600">Skills come after the guardrails in the prompt, which say the guardrails take precedence.</p>
    </fieldset>
  );
}
