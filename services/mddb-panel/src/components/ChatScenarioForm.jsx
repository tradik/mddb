import { inputClass, labelClass } from '../lib/chat-scenario';
import ChatScenarioStyleFields from './ChatScenarioStyleFields';
import ChatScenarioLlmFields from './ChatScenarioLlmFields';
import ChatScenarioLoggingFields from './ChatScenarioLoggingFields';
import ChatSkillPicker from './ChatSkillPicker';
import ChatMcpServers from './ChatMcpServers';
import CollectionChecklist from './CollectionChecklist';

// #403: the scenario editor. The form state is flat (lib/chat-scenario.js);
// `set(field, value)` changes one field. A stored scenario keeps its name:
// the name is its key, and renaming would be a second scenario.

function TextArea({ id, label, value, onChange, rows, placeholder, hint }) {
  return (
    <div>
      <label htmlFor={id} className={labelClass}>{label}</label>
      <textarea id={id} rows={rows} value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} className={inputClass} />
      <p className="mt-1 text-xs text-fg-subtle">{hint}</p>
    </div>
  );
}

export default function ChatScenarioForm({ form, set, collections, skills, secrets, isNew }) {
  return (
    <div className="space-y-4">
      <div className="grid gap-3 md:grid-cols-2">
        <div>
          <label htmlFor="chat-name" className={labelClass}>Name</label>
          <input
            id="chat-name"
            value={form.name}
            onChange={(e) => set('name', e.target.value)}
            disabled={!isNew}
            placeholder="support"
            className={`${inputClass} disabled:bg-surface-subtle disabled:text-fg-muted`}
          />
        </div>
        <div>
          <label htmlFor="chat-description" className={labelClass}>Description</label>
          <input id="chat-description" value={form.description} onChange={(e) => set('description', e.target.value)} placeholder="Who talks to this assistant, and about what" className={inputClass} />
        </div>
      </div>

      <label className="flex items-center gap-2 text-sm text-fg-soft">
        <input
          type="checkbox"
          checked={form.enabled}
          onChange={(e) => set('enabled', e.target.checked)}
          className="h-4 w-4 rounded border-line-strong text-accent focus:ring-focus"
        />
        Enabled — the chat service offers this scenario
      </label>

      <TextArea
        id="chat-operator-prompt"
        label="Operator prompt"
        rows={6}
        value={form.operatorPrompt}
        onChange={(v) => set('operatorPrompt', v)}
        placeholder="You are the support assistant of … Answer in the user's language."
        hint="Who the assistant is, its tone and its language policy."
      />
      <TextArea
        id="chat-guardrails"
        label="Guardrails"
        rows={3}
        value={form.guardrails}
        onChange={(v) => set('guardrails', v)}
        placeholder="No legal or medical advice. Never quote prices."
        hint="What it must not do or say, and topics to avoid."
      />

      <CollectionChecklist
        collections={collections}
        selected={form.allowedCollections}
        onChange={(v) => set('allowedCollections', v)}
        legend="Allowed collections"
        hint="The assistant may search only these. With none selected it has no collection to search."
      />

      <ChatSkillPicker selected={form.skills} available={skills} onChange={(v) => set('skills', v)} />

      <ChatMcpServers rows={form.mcpServers} onChange={(v) => set('mcpServers', v)} secrets={secrets} scenarioEnabled={form.enabled} />

      <ChatScenarioStyleFields form={form} set={set} />
      <ChatScenarioLlmFields form={form} set={set} />
      <ChatScenarioLoggingFields form={form} set={set} />
    </div>
  );
}
