import { Plus, Trash2 } from 'lucide-react';
import NumberField from './ChatNumberField';
import ChatScenarioLoopGuardFields from './ChatScenarioLoopGuardFields';
import { inputClass, labelClass } from '../lib/chat-scenario';

// #403: the model and its parameters, the extra request body (for example
// reasoning_effort) as a key/value list, and the session limits. An empty
// number means "not set by the scenario": the chat service's own default
// applies. #413 adds the repetition-loop guard.

function ExtraBodyEditor({ entries, onChange }) {
  const update = (index, field, value) => onChange(entries.map((entry, i) => (i === index ? { ...entry, [field]: value } : entry)));
  return (
    <div className="mt-3">
      <div className="flex items-center justify-between">
        <span className={labelClass}>Extra request body</span>
        <button
          type="button"
          onClick={() => onChange([...entries, { key: '', value: '' }])}
          className="flex items-center gap-1 rounded px-2 py-1 text-xs font-medium text-primary-700 hover:bg-primary-50"
        >
          <Plus className="h-3 w-3" aria-hidden="true" /> Add field
        </button>
      </div>
      {entries.length === 0 && <p className="text-xs text-gray-600">Nothing extra is sent to the model API.</p>}
      {entries.map((entry, index) => (
        <div key={index} className="mt-1 flex items-center gap-2">
          <input
            aria-label={`Extra field ${index + 1} name`}
            value={entry.key}
            onChange={(e) => update(index, 'key', e.target.value)}
            placeholder="reasoning_effort"
            className={`${inputClass} font-mono`}
          />
          <input
            aria-label={`Extra field ${index + 1} value`}
            value={entry.value}
            onChange={(e) => update(index, 'value', e.target.value)}
            placeholder="low"
            className={`${inputClass} font-mono`}
          />
          <button
            type="button"
            onClick={() => onChange(entries.filter((_, i) => i !== index))}
            aria-label={`Remove extra field ${index + 1}`}
            className="shrink-0 rounded p-1.5 text-red-700 hover:bg-red-50"
          >
            <Trash2 className="h-4 w-4" aria-hidden="true" />
          </button>
        </div>
      ))}
      <p className="mt-1 text-xs text-gray-600">Values that read as JSON (numbers, true, objects) are sent as such; anything else as text.</p>
    </div>
  );
}

export default function ChatScenarioLlmFields({ form, set }) {
  return (
    <fieldset className="rounded-lg border border-gray-200 p-4">
      <legend className="px-1 text-sm font-semibold text-gray-900">Model and limits</legend>
      <div className="grid gap-3 md:grid-cols-3">
        <div>
          <label htmlFor="chat-model" className={labelClass}>Model</label>
          <input id="chat-model" value={form.model} onChange={(e) => set('model', e.target.value)} placeholder="the chat service's default" className={inputClass} />
        </div>
        <NumberField id="chat-temperature" label="Temperature" value={form.temperature} onChange={(v) => set('temperature', v)} step="0.1" max="2" hint="0 to 2; empty leaves it to the model." />
        <NumberField id="chat-max-tokens" label="Max answer tokens" value={form.maxTokens} onChange={(v) => set('maxTokens', v)} />
      </div>
      <ExtraBodyEditor entries={form.extraBody} onChange={(entries) => set('extraBody', entries)} />
      <ChatScenarioLoopGuardFields form={form} set={set} />
      <div className="mt-3 grid gap-3 md:grid-cols-3">
        <NumberField id="chat-max-turns" label="Max turns per session" value={form.maxTurns} onChange={(v) => set('maxTurns', v)} />
        <NumberField id="chat-token-budget" label="Token budget per session" value={form.tokenBudgetPerSession} onChange={(v) => set('tokenBudgetPerSession', v)} />
        <NumberField id="chat-rate-limit" label="Messages per minute" value={form.messagesPerMinute} onChange={(v) => set('messagesPerMinute', v)} />
      </div>
    </fieldset>
  );
}
