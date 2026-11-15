import { useCallback, useEffect, useState } from 'react';
import { MessagesSquare, RefreshCw } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import ApiErrorNotice from './ApiErrorNotice';
import MemorySessionsTable from './MemorySessionsTable';
import MemorySessionView from './MemorySessionView';
import MemorySessionForm from './MemorySessionForm';
import MemoryRecallBox from './MemoryRecallBox';

/**
 * MemoryPanel is the Agent Memory page: the conversation sessions agents keep
 * through /v1/memory/*, filtered by user and scenario; one session's history
 * with "add a message" and "summarize"; and a recall box that shows what an
 * agent would get back for a query. Sessions cannot be deleted through the
 * API — they expire after their TTL (30 days by default).
 */
export default function MemoryPanel() {
  const [userId, setUserId] = useState('');
  const [scenario, setScenario] = useState('');
  const [data, setData] = useState({ sessions: [], total: 0 });
  const [error, setError] = useState(null);
  const [selected, setSelected] = useState(null);

  const load = useCallback(() => mddbClient
    .listMemorySessions({ userId: userId.trim() || undefined, scenario: scenario.trim() || undefined })
    .then((answer) => {
      setData({ sessions: answer.sessions || [], total: answer.total || 0 });
      setError(null);
    })
    .catch(setError), [userId, scenario]);

  useEffect(() => {
    load();
    // Filters apply on Refresh/Enter, not on every keystroke.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const filter = (e) => {
    e.preventDefault();
    load();
  };

  const created = (session) => {
    setSelected(session);
    load();
  };

  return (
    <div className="h-full overflow-y-auto p-6 bg-surface-muted space-y-4">
      <div>
        <h2 className="text-xl font-bold text-fg flex items-center gap-2">
          <MessagesSquare className="w-5 h-5" aria-hidden="true" /> Agent Memory
        </h2>
        <p className="text-sm text-fg-muted">
          Conversation sessions agents store through the memory API. Messages are embedded for recall; sessions expire after their TTL.
        </p>
      </div>
      <MemoryRecallBox userId={userId.trim()} />
      <section aria-labelledby="memory-sessions-title" className="bg-surface rounded-lg shadow p-4 space-y-3">
        <h3 id="memory-sessions-title" className="text-base font-semibold text-fg">Sessions</h3>
        <form onSubmit={filter} className="flex flex-wrap gap-2 items-end">
          <label className="text-sm text-fg-soft">
            <span className="block mb-1">Filter by user ID</span>
            <input value={userId} onChange={(e) => setUserId(e.target.value)} className="px-3 py-2 border border-line-strong rounded-lg" />
          </label>
          <label className="text-sm text-fg-soft">
            <span className="block mb-1">Filter by scenario</span>
            <input value={scenario} onChange={(e) => setScenario(e.target.value)} className="px-3 py-2 border border-line-strong rounded-lg" />
          </label>
          <button type="submit" className="inline-flex items-center gap-1 px-3 py-2 bg-surface-subtle text-fg rounded-lg hover:bg-surface-strong text-sm">
            <RefreshCw className="w-4 h-4" aria-hidden="true" /> Refresh
          </button>
        </form>
        <ApiErrorNotice error={error} collection="*" />
        {!error && (
          <MemorySessionsTable sessions={data.sessions} total={data.total} selectedId={selected?.sessionId} onSelect={setSelected} />
        )}
      </section>
      <MemorySessionForm onCreated={created} />
      {selected && <MemorySessionView key={selected.sessionId} session={selected} onChanged={load} />}
    </div>
  );
}
