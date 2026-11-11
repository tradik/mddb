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
    <section aria-labelledby="chat-try-heading" className="rounded-lg border border-dashed border-gray-400 bg-gray-50 p-4">
      <h3 id="chat-try-heading" className="flex items-center gap-2 text-sm font-semibold text-gray-900">
        <MessageSquare className="h-4 w-4 text-gray-700" aria-hidden="true" /> Try it
      </h3>
      <p className="mt-1 text-sm text-gray-700">
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
      <p className="text-gray-700">
        {formatBytes(data.bytes)} · about {data.approxTokens ?? 0} tokens (bytes ÷ 4)
      </p>
      {data.warning && (
        <p role="status" className="rounded border border-amber-300 bg-amber-50 p-2 text-amber-900">{data.warning}</p>
      )}
      {missing.length > 0 && (
        <p role="status" className="rounded border border-amber-300 bg-amber-50 p-2 text-amber-900">
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
      <section aria-labelledby="chat-prompt-heading" className="rounded-lg border border-gray-200 bg-white p-4">
        <div className="flex items-center justify-between">
          <h3 id="chat-prompt-heading" className="flex items-center gap-2 text-sm font-semibold text-gray-900">
            <FileText className="h-4 w-4 text-primary-700" aria-hidden="true" /> Composed prompt
          </h3>
          <button
            type="button"
            onClick={() => setReloads((n) => n + 1)}
            className="flex items-center gap-1 rounded px-2 py-1 text-xs font-medium text-gray-800 hover:bg-gray-100"
          >
            <RefreshCw className="h-3 w-3" aria-hidden="true" /> Reload
          </button>
        </div>
        <p className="mt-1 text-xs text-gray-600">
          The saved scenario as the model receives it. A collection&apos;s paragraph comes from its response prompt, so reload after changing one.
        </p>
        {error && <p role="alert" className="mt-2 rounded border border-red-200 bg-red-50 p-2 text-sm text-red-800">{error}</p>}
        {data !== null && !error && (
          <>
            <PromptFacts data={data} />
            <pre className="mt-2 max-h-96 overflow-auto whitespace-pre-wrap rounded bg-gray-50 p-3 text-xs text-gray-900">{data.prompt}</pre>
          </>
        )}
      </section>
      <TryItPlaceholder name={name} />
    </div>
  );
}
