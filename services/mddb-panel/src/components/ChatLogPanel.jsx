import { useEffect, useState } from 'react';
import { Download, ScrollText } from 'lucide-react';
import mddbClient from '../lib/mddb-client';
import { saveBlob } from '../lib/download';
import { emptyLogFilters, logParams } from '../lib/chat-log';
import ChatLogFilters from './ChatLogFilters';
import ChatLogRow from './ChatLogRow';
import ChatGaps from './ChatGaps';

// #406: the Log tab of the Chat page — the turns of scenarios with logging
// on, filtered, expandable and exportable, and the docs gaps they reveal.
// Global admin only: the log holds every user's questions.

const viewButton = (active) => `rounded px-3 py-1.5 text-sm font-medium focus:outline-none focus:ring-2 focus:ring-primary-500 ${active ? 'bg-primary-700 text-white' : 'text-gray-800 hover:bg-gray-100'}`;

export default function ChatLogPanel() {
  const [view, setView] = useState('turns');
  const [filters, setFilters] = useState(emptyLogFilters);
  const [scenarios, setScenarios] = useState([]);
  const [turns, setTurns] = useState(null);
  const [cursor, setCursor] = useState('');
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    mddbClient.listChatScenarios().then((data) => setScenarios((data.scenarios || []).map((s) => s.name)), () => setScenarios([]));
  }, []);

  // Shows a page: the first replaces the list, a later one (`after`) extends it.
  const show = (page, after) => {
    setTurns((prev) => (after ? [...prev, ...(page.turns || [])] : page.turns || []));
    setCursor(page.nextCursor || '');
    setError(null);
  };

  useEffect(() => {
    if (view !== 'turns') return undefined;
    let live = true;
    mddbClient.chatLog({ ...logParams(filters), cursor: '' }).then(
      (page) => live && show(page, ''),
      (err) => live && setError(err.message),
    );
    return () => { live = false; };
  }, [filters, view]);

  const loadMore = async () => {
    setBusy(true);
    try {
      show(await mddbClient.chatLog({ ...logParams(filters), cursor }), cursor);
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  const exportAs = async (format) => {
    setError(null);
    try {
      saveBlob(await mddbClient.chatLogExport(logParams(filters), format), `chat-log.${format}`);
    } catch (err) {
      setError(err.message);
    }
  };

  return (
    <>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <ScrollText className="h-6 w-6 text-primary-700" aria-hidden="true" />
          <h2 className="text-xl font-bold text-gray-900">Conversation log</h2>
        </div>
        <div className="flex gap-1" role="group" aria-label="Log view">
          <button type="button" aria-pressed={view === 'turns'} onClick={() => setView('turns')} className={viewButton(view === 'turns')}>Turns</button>
          <button type="button" aria-pressed={view === 'gaps'} onClick={() => setView('gaps')} className={viewButton(view === 'gaps')}>Docs gaps</button>
        </div>
      </div>
      <p className="mb-4 max-w-3xl text-sm text-gray-700">
        Turns of the scenarios whose conversation log is on, anonymous visitors included, newest first. Only global
        administrators see this page; signed-in users are shown by name and id, anonymous visitors are not identified.
      </p>
      <ChatLogFilters filters={filters} scenarios={scenarios} onChange={setFilters} />
      {error && <p role="alert" className="mb-3 rounded border border-red-200 bg-red-50 p-2 text-sm text-red-800">{error}</p>}
      {view === 'gaps' ? <ChatGaps filters={filters} /> : (
        <section aria-label="Logged turns" className="rounded-lg bg-white p-3 shadow">
          <div className="mb-2 flex justify-end gap-2">
            {['csv', 'json'].map((format) => (
              <button key={format} type="button" onClick={() => exportAs(format)} className="flex items-center gap-1 rounded border border-gray-300 px-2 py-1 text-sm text-gray-800 hover:bg-gray-100">
                <Download className="h-4 w-4" aria-hidden="true" /> Export {format.toUpperCase()}
              </button>
            ))}
          </div>
          {turns?.length === 0 && <p className="text-sm text-gray-700">No logged turns match.</p>}
          {turns?.length > 0 && (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead className="text-xs text-gray-700">
                  <tr>
                    <th scope="col" className="p-2"><span className="sr-only">Details</span></th>
                    {['Time', 'Scenario', 'User', 'Question', 'Answer', 'Outcome', 'Rating'].map((h) => <th key={h} scope="col" className="p-2">{h}</th>)}
                  </tr>
                </thead>
                <tbody className="text-gray-900">
                  {turns.map((t) => <ChatLogRow key={`${t.conversationId}/${t.messageId}`} turn={t} />)}
                </tbody>
              </table>
            </div>
          )}
          {cursor && (
            <button type="button" disabled={busy} onClick={loadMore} className="mt-3 rounded px-3 py-1.5 text-sm font-medium text-primary-700 hover:bg-primary-50 disabled:opacity-50">
              {busy ? 'Loading…' : 'Load more'}
            </button>
          )}
        </section>
      )}
    </>
  );
}
