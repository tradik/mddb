import ChatNumberField from './ChatNumberField';
import { inputClass, labelClass } from '../lib/chat-scenario';
import { loopGuardModes, loopGuardUnits } from '../lib/chat-loop-guard';

// #413: the repetition-loop guard of a scenario. mddb-chat stops an answer
// once most of what the model writes repeats what it already wrote; these
// fields override its [llm.loop_guard] settings for this scenario only — a
// code-heavy scenario may loosen them. Empty fields keep the service's value.

export default function ChatScenarioLoopGuardFields({ form, set }) {
  return (
    <div className="mt-3 rounded border border-gray-200 p-3">
      <h4 className="text-xs font-semibold text-gray-900">Repetition-loop guard</h4>
      <p className="mt-1 text-xs text-gray-600">
        Stops an answer that keeps repeating itself and ends it with “[Response stopped: repetition detected]”.
        Code blocks and tables are never counted.
      </p>
      <div className="mt-2 grid gap-3 md:grid-cols-3">
        <div>
          <label htmlFor="chat-loop-guard-mode" className={labelClass}>Guard</label>
          <select id="chat-loop-guard-mode" value={form.loopGuardMode} onChange={(e) => set('loopGuardMode', e.target.value)} className={inputClass}>
            {loopGuardModes.map((m) => <option key={m.value} value={m.value}>{m.label}</option>)}
          </select>
        </div>
        <div>
          <label htmlFor="chat-loop-guard-unit" className={labelClass}>Unit</label>
          <select id="chat-loop-guard-unit" value={form.loopGuardUnit} onChange={(e) => set('loopGuardUnit', e.target.value)} className={inputClass}>
            {loopGuardUnits.map((u) => <option key={u.value} value={u.value}>{u.label}</option>)}
          </select>
        </div>
        <ChatNumberField id="chat-loop-guard-ratio" label="Repeated share" value={form.loopGuardRatio} onChange={(v) => set('loopGuardRatio', v)} step="0.05" max="1" hint="Above 0, at most 1; 0.8 by default." />
        <ChatNumberField id="chat-loop-guard-ngram" label="Fragment length" value={form.loopGuardNgram} onChange={(v) => set('loopGuardNgram', v)} max="4096" hint="Units compared at a time." />
        <ChatNumberField id="chat-loop-guard-window" label="Window" value={form.loopGuardWindow} onChange={(v) => set('loopGuardWindow', v)} max="65536" hint="Latest units the share is measured over." />
        <ChatNumberField id="chat-loop-guard-min-output" label="Judge after" value={form.loopGuardMinOutput} onChange={(v) => set('loopGuardMinOutput', v)} hint="Units an answer has before it is judged." />
      </div>
    </div>
  );
}
