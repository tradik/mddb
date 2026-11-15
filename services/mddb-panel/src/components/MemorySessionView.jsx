import { useCallback, useEffect, useState } from 'react';
import { FileText } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { formatUnix } from '../lib/time-format';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';
import MemoryMessageForm from './MemoryMessageForm';

/** The role a stored message carries in its metadata. */
const roleOf = (doc) => doc.meta?.role?.[0] || 'unknown';

/**
 * MemorySessionView shows one session's messages (/v1/memory/history, in the
 * order the server returns them), adds a message to it and asks the server
 * for a summary (/v1/memory/summarize), which is stored as a document too.
 */
export default function MemorySessionView({ session, onChanged }) {
  const { sessionId } = session;
  const [messages, setMessages] = useState(null);
  const [total, setTotal] = useState(0);
  const [error, setError] = useState(null);
  const [summary, setSummary] = useState(null);
  const summarizing = useAction();

  const load = useCallback(() => mddbClient.getMemoryHistory({ sessionId })
    .then((data) => {
      setMessages(data.messages || []);
      setTotal(data.total || 0);
      setError(null);
    })
    .catch(setError), [sessionId]);

  useEffect(() => {
    load();
  }, [load]);

  const summarize = () => summarizing.run(async () => setSummary(await mddbClient.summarizeMemorySession({ sessionId })));

  const added = () => {
    load();
    onChanged();
  };

  return (
    <section aria-labelledby="memory-session-title" className="bg-surface rounded-lg shadow p-4 space-y-4">
      <div className="flex items-center justify-between gap-2">
        <h3 id="memory-session-title" className="text-base font-semibold text-fg">
          {session.title || 'Session'} <span className="font-mono text-xs text-fg-muted">{sessionId}</span>
        </h3>
        <button
          type="button"
          onClick={summarize}
          disabled={summarizing.busy}
          className="inline-flex items-center gap-1 px-3 py-1.5 text-sm bg-surface-subtle text-fg rounded hover:bg-surface-strong disabled:opacity-50"
        >
          <FileText className="w-4 h-4" aria-hidden="true" />
          {summarizing.busy ? 'Summarizing…' : 'Summarize'}
        </button>
      </div>
      <ApiErrorNotice error={error || summarizing.error} />
      {summary && (
        <div role="status" className="rounded border border-success-line bg-success-soft p-3 text-sm">
          <p className="font-medium text-success-strong mb-1">Summary of {summary.messages} messages, stored as {summary.summaryId}</p>
          <pre className="whitespace-pre-wrap text-fg">{summary.summary}</pre>
        </div>
      )}
      {messages === null && !error && <p className="text-sm text-fg-muted">Loading messages…</p>}
      {messages?.length === 0 && <p className="text-sm text-fg-muted">This session has no messages yet.</p>}
      {messages?.length > 0 && (
        <ol aria-label={`Messages (${total})`} className="space-y-2">
          {messages.map((m) => (
            <li key={m.id} className="border border-line rounded p-2">
              <p className="text-xs text-fg-muted"><span className="font-semibold text-fg">{roleOf(m)}</span> · {formatUnix(m.addedAt)}</p>
              <p className="text-sm text-fg whitespace-pre-wrap">{m.contentMd}</p>
            </li>
          ))}
        </ol>
      )}
      <MemoryMessageForm sessionId={sessionId} onAdded={added} />
    </section>
  );
}
