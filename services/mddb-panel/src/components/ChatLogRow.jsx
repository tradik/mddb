import { useState } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { formatUnix } from '../lib/time-format';
import { outcomeLabels, ratingText, scoreText, whoOf } from '../lib/chat-log';

// #406: one logged turn — a summary row, and on demand the whole turn: the
// question and answer, every tool call with its query, result keys and best
// score, the model, latency, tokens and the visitor's rating and comment.
// #413: an answer the loop guard stopped is marked "loop stopped".

function Detail({ label, children }) {
  return (
    <div>
      <dt className="text-xs font-medium text-fg-muted">{label}</dt>
      <dd className="whitespace-pre-wrap break-words text-sm text-fg">{children}</dd>
    </div>
  );
}

function ToolCalls({ calls }) {
  if (!calls?.length) return <p className="text-sm text-fg-muted">No tools were called.</p>;
  return (
    <table className="w-full text-left text-xs">
      <thead className="text-fg-muted">
        <tr><th scope="col">Tool (server/tool when external)</th><th scope="col">Collection</th><th scope="col">Query</th><th scope="col">Result keys</th><th scope="col">Top score</th><th scope="col">ms</th></tr>
      </thead>
      <tbody className="text-fg">
        {calls.map((c, i) => (
          <tr key={i} className="border-t border-line align-top">
            <td className="py-1 pr-2 font-mono">{c.server ? `${c.server}/${c.name}` : c.name}</td>
            <td className="py-1 pr-2">{c.collection || '—'}</td>
            <td className="py-1 pr-2">{c.query || '—'}</td>
            <td className="py-1 pr-2 font-mono">{c.resultKeys?.length ? c.resultKeys.join(', ') : 'nothing found'}</td>
            <td className="py-1 pr-2">{scoreText(c)}</td>
            <td className="py-1">{c.durationMs || '—'}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export default function ChatLogRow({ turn }) {
  const [open, setOpen] = useState(false);
  const detailId = `chat-turn-${turn.conversationId}-${turn.messageId}`;
  const tokens = turn.tokens ? `${turn.tokens.prompt} in / ${turn.tokens.completion} out` : '—';
  return (
    <>
      <tr className="border-t border-line align-top">
        <td className="p-2">
          <button
            type="button"
            aria-expanded={open}
            aria-controls={detailId}
            aria-label={`${open ? 'Hide' : 'Show'} the turn of ${formatUnix(turn.at)}`}
            onClick={() => setOpen(!open)}
            className="rounded p-1 text-fg-muted hover:bg-surface-subtle focus:outline-none focus:ring-2 focus:ring-focus"
          >
            {open ? <ChevronDown className="h-4 w-4" aria-hidden="true" /> : <ChevronRight className="h-4 w-4" aria-hidden="true" />}
          </button>
        </td>
        <td className="whitespace-nowrap p-2">{formatUnix(turn.at)}</td>
        <td className="p-2">{turn.scenario}</td>
        <td className="p-2">{whoOf(turn)}</td>
        <td className="p-2">{turn.question || '—'}</td>
        <td className="p-2 text-fg-soft">{turn.answerExcerpt}</td>
        <td className="p-2">
          {outcomeLabels[turn.outcome] || turn.outcome}
          {turn.loopStopped && <span className="ml-1 whitespace-nowrap rounded bg-warning-muted px-1.5 py-0.5 text-xs font-medium text-warning-strong">loop stopped</span>}
        </td>
        <td className="whitespace-nowrap p-2">{ratingText(turn.rating)}</td>
      </tr>
      {open && (
        <tr id={detailId} className="bg-surface-muted">
          <td />
          <td colSpan={7} className="p-3">
            <dl className="grid gap-3 md:grid-cols-2">
              <Detail label="Question">{turn.question || '—'}</Detail>
              <Detail label="Answer">{turn.answer || '—'}</Detail>
              <Detail label="Model">{turn.model ? `${turn.model}${turn.provider ? ` (${turn.provider})` : ''}` : '—'}</Detail>
              <Detail label="Latency and tokens">{`${turn.latencyMs ? `${turn.latencyMs} ms` : '—'} · ${tokens}`}</Detail>
              <Detail label="Rating">{ratingText(turn.rating)}{turn.comment ? ` — ${turn.comment}` : ''}</Detail>
              <Detail label="Conversation">{`${turn.conversationId} / ${turn.messageId}`}</Detail>
              {turn.loopStopped && <Detail label="Loop guard">The answer repeated itself and was stopped where the repetition began (#413).</Detail>}
            </dl>
            <h4 className="mb-1 mt-3 text-xs font-semibold text-fg">Tools</h4>
            <ToolCalls calls={turn.toolCalls} />
          </td>
        </tr>
      )}
    </>
  );
}
