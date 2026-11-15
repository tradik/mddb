import { useEffect, useState } from 'react';
import { FileText, MessageSquare, RefreshCw } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { formatBytes } from '../lib/chat-skill';
import ChatMcpStates from './ChatMcpStates';

// #403: what the model is told. The server composes the system prompt of a
// stored scenario — operator prompt, guardrails, the off-topic clause, the
// skills, the style clause, then each allowed collection's responsePrompt — so the
// preview is the prompt mddb-chat sends, not a copy of the rules made here.
// Since #404 it also carries the skills, the prompt's size against
// MDDB_CHAT_PROMPT_MAX_BYTES, and the skill names that are not stored.
//
// `version` is the store's counter: it changes when the scenario is saved,
// which reloads the preview.

export function TryItPlaceholder({ name }) {
  return (
    <section aria-labelledby="chat-try-heading" className="rounded-lg border border-dashed border-line-strong bg-surface-muted p-4">
      <h3 id="chat-try-heading" className="flex items-center gap-2 text-sm font-semibold text-fg">
        <MessageSquare className="h-4 w-4 text-fg-muted" aria-hidden="true" /> Try it
      </h3>
      <p className="mt-1 text-sm text-fg-muted">
        Talking to <strong>{name}</strong> from here needs the chat service (mddb-chat) connected to this panel, which is not
        set up yet. Until then, open the chat widget that mddb-chat serves: it picks up a saved scenario without a restart.
      </p>
    </section>
  );
}

/** The prompt's size, the server's warning, and the skills it could not find. */
export function PromptFacts({ data }) {
  const missing = data.missingSkills || [];
  return (
    <div className="mt-2 space-y-2 text-xs">
      <p className="text-fg-muted">
        {formatBytes(data.bytes)} · about {data.approxTokens ?? 0} tokens (bytes ÷ 4)
      </p>
      {data.warning && (
        <p role="status" className="rounded border border-warning-line bg-warning-soft p-2 text-warning-strong">{data.warning}</p>
      )}
      {missing.length > 0 && (
        <p role="status" className="rounded border border-warning-line bg-warning-soft p-2 text-warning-strong">
          Skipped skills that are not stored: {missing.join(', ')}
        </p>
      )}
      <ChatMcpStates servers={data.mcpServers} />
    </div>
  );
}

export default function ChatScenarioPreview({ name, version }) {
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const [reloads, setReloads] = useState(0);

  useEffect(() => {
    let current = true;
    mddbClient
      .getChatScenarioPrompt(name)
      .then((data) => {
        if (!current) return;
        setData(data);
        setError(null);
      })
      .catch((err) => current && setError(err.message));
    return () => {
      current = false;
    };
  }, [name, version, reloads]);

  return (
    <div className="space-y-4">
      <section aria-labelledby="chat-prompt-heading" className="rounded-lg border border-line bg-surface p-4">
        <div className="flex items-center justify-between">
          <h3 id="chat-prompt-heading" className="flex items-center gap-2 text-sm font-semibold text-fg">
            <FileText className="h-4 w-4 text-accent" aria-hidden="true" /> Composed prompt
          </h3>
          <button
            type="button"
            onClick={() => setReloads((n) => n + 1)}
            className="flex items-center gap-1 rounded px-2 py-1 text-xs font-medium text-fg-soft hover:bg-surface-subtle"
          >
            <RefreshCw className="h-3 w-3" aria-hidden="true" /> Reload
          </button>
        </div>
        <p className="mt-1 text-xs text-fg-subtle">
          The saved scenario as the model receives it. A collection&apos;s paragraph comes from its response prompt, so reload after changing one.
        </p>
        {error && <p role="alert" className="mt-2 rounded border border-danger-line bg-danger-soft p-2 text-sm text-danger-strong">{error}</p>}
        {data !== null && !error && (
          <>
            <PromptFacts data={data} />
            <pre className="mt-2 max-h-96 overflow-auto whitespace-pre-wrap rounded bg-surface-muted p-3 text-xs text-fg">{data.prompt}</pre>
          </>
        )}
      </section>
      <TryItPlaceholder name={name} />
    </div>
  );
}
