import { inputClass, labelClass, offTopicModes, emojiPolicies, verbosityLevels } from '../lib/chat-scenario';

// #403: how the assistant answers — what it does with an off-topic question,
// and the style clause of its system prompt (emoji, length, Markdown, how a
// source is cited).

function Choice({ id, label, value, options, onChange }) {
  return (
    <div>
      <label htmlFor={id} className={labelClass}>{label}</label>
      <select id={id} value={value} onChange={(e) => onChange(e.target.value)} className={inputClass}>
        {options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>
    </div>
  );
}

export default function ChatScenarioStyleFields({ form, set }) {
  return (
    <fieldset className="rounded-lg border border-gray-200 p-4">
      <legend className="px-1 text-sm font-semibold text-gray-900">Behaviour and style</legend>
      <div className="grid gap-3 md:grid-cols-3">
        <Choice id="chat-off-topic" label="Off-topic questions" value={form.offTopicMode} options={offTopicModes} onChange={(v) => set('offTopicMode', v)} />
        <Choice id="chat-emoji" label="Emoji" value={form.emoji} options={emojiPolicies} onChange={(v) => set('emoji', v)} />
        <Choice id="chat-verbosity" label="Answer length" value={form.verbosity} options={verbosityLevels} onChange={(v) => set('verbosity', v)} />
      </div>

      {form.offTopicMode === 'redirect' && (
        <div className="mt-3">
          <label htmlFor="chat-redirect" className={labelClass}>Redirect reply</label>
          <input
            id="chat-redirect"
            value={form.redirectTemplate}
            onChange={(e) => set('redirectTemplate', e.target.value)}
            placeholder="I can only help with questions about our products."
            className={inputClass}
          />
          <p className="mt-1 text-xs text-gray-600">Sent instead of an answer. Left empty, a built-in sentence is used.</p>
        </div>
      )}

      <div className="mt-3">
        <label htmlFor="chat-citation" className={labelClass}>Citation format</label>
        <input
          id="chat-citation"
          value={form.citationTemplate}
          onChange={(e) => set('citationTemplate', e.target.value)}
          placeholder="https://example.org/blob/main/{{key}}"
          className={`${inputClass} font-mono`}
        />
        <p className="mt-1 text-xs text-gray-600">How a source is cited, for example a link template with the document key.</p>
      </div>

      <label className="mt-3 flex items-center gap-2 text-sm text-gray-800">
        <input
          type="checkbox"
          checked={form.markdown}
          onChange={(e) => set('markdown', e.target.checked)}
          className="h-4 w-4 rounded border-gray-400 text-primary-700 focus:ring-primary-500"
        />
        Format answers in Markdown
      </label>
    </fieldset>
  );
}
