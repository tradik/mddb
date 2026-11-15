import { inputClass, labelClass } from '../lib/chat-scenario';

// #406: the scenario's turn log. Off by default; when on, mddb-chat stores
// every turn of this scenario — anonymous visitors included — for review on
// the Log tab. Redaction is on by default.

const checkClass = 'h-4 w-4 rounded border-line-strong text-accent focus:ring-focus';

export default function ChatScenarioLoggingFields({ form, set }) {
  return (
    <fieldset className="rounded border border-line p-3">
      <legend className="px-1 text-sm font-semibold text-fg">Conversation log</legend>
      <p id="chat-log-privacy" className="mb-2 text-xs text-fg-muted">
        Privacy: when on, every turn is stored — the question, the answer, the searches and the rating — anonymous
        visitors included. Only global administrators can read the log. With redaction, e-mail addresses, tokens and
        card-like numbers are replaced with [redacted] before anything is stored. Anonymous visitors&apos; logs are kept
        for the retention below; a signed-in user&apos;s log follows that user&apos;s conversation.
      </p>
      <label className="flex items-center gap-2 text-sm text-fg-soft">
        <input
          type="checkbox"
          checked={form.loggingEnabled}
          onChange={(e) => set('loggingEnabled', e.target.checked)}
          aria-describedby="chat-log-privacy"
          className={checkClass}
        />
        Log every turn of this scenario
      </label>
      <div className="mt-2 grid gap-3 md:grid-cols-2">
        <div>
          <label htmlFor="chat-log-retention" className={labelClass}>Log retention (days)</label>
          <input
            id="chat-log-retention"
            type="number"
            min="0"
            max="3650"
            step="1"
            value={form.logRetentionDays}
            onChange={(e) => set('logRetentionDays', e.target.value)}
            className={inputClass}
          />
          <p className="mt-1 text-xs text-fg-subtle">Empty or 0: the server default (30 days).</p>
        </div>
        <label className="flex items-center gap-2 self-start pt-5 text-sm text-fg-soft">
          <input type="checkbox" checked={form.logRedact} onChange={(e) => set('logRedact', e.target.checked)} className={checkClass} />
          Redact e-mails, tokens and card numbers
        </label>
      </div>
    </fieldset>
  );
}
