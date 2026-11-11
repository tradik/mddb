import { useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import { formatUnix, dateInputToUnix } from '../lib/time-format';
import ApiErrorNotice from './ApiErrorNotice';

/**
 * TemporalEventsTab lists one document's create, update and access events
 * (/v1/temporal/query) in a time range — by default the last 30 days. A
 * server without MDDB_TEMPORAL=true has no such route (404).
 */
export default function TemporalEventsTab({ collection }) {
  const [key, setKey] = useState('');
  const [lang, setLang] = useState('en');
  const [eventType, setEventType] = useState('');
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [result, setResult] = useState(null);
  const { busy, error, run } = useAction();

  const submit = (e) => {
    e.preventDefault();
    run(async () => setResult(await mddbClient.temporalQuery({
      collection,
      key: key.trim(),
      lang: lang.trim() || 'en',
      eventType: eventType || undefined,
      from: dateInputToUnix(from) || undefined,
      to: dateInputToUnix(to) || undefined,
      limit: 100,
    })));
  };

  const field = (label, value, setValue, type = 'text') => (
    <label className="text-sm text-gray-800">
      <span className="block mb-1">{label}</span>
      <input type={type} value={value} onChange={(e) => setValue(e.target.value)} className="px-2 py-1.5 border border-gray-300 rounded" />
    </label>
  );

  const events = result?.events || [];

  return (
    <div className="space-y-3">
      <form onSubmit={submit} className="flex flex-wrap gap-3 items-end">
        {field('Document key', key, setKey)}
        {field('Language', lang, setLang)}
        <label className="text-sm text-gray-800">
          <span className="block mb-1">Event</span>
          <select value={eventType} onChange={(e) => setEventType(e.target.value)} className="px-2 py-1.5 border border-gray-300 rounded">
            <option value="">All events</option>
            <option value="create">Create</option>
            <option value="update">Update</option>
            <option value="access">Access</option>
          </select>
        </label>
        {field('From', from, setFrom, 'date')}
        {field('To', to, setTo, 'date')}
        <button type="submit" disabled={busy || !key.trim()} className="px-3 py-1.5 text-sm bg-blue-700 text-white rounded hover:bg-blue-800 disabled:opacity-50">
          Show events
        </button>
      </form>
      {error?.status === 404
        ? <p role="alert" className="text-sm text-amber-950">Temporal tracking is off on this server: start it with MDDB_TEMPORAL=true.</p>
        : <ApiErrorNotice error={error} collection={collection} />}
      {result && (
        <div role="status">
          <p className="text-sm text-gray-800 mb-1">{result.total ?? events.length} events of {result.docId || key.trim()}.</p>
          {events.length > 0 && (
            <table className="min-w-full text-sm" aria-label="Document events">
              <thead><tr className="text-left text-gray-800"><th scope="col" className="pr-4">When</th><th scope="col">Event</th></tr></thead>
              <tbody className="divide-y divide-gray-100">
                {events.map((ev, i) => (
                  <tr key={`${ev.timestamp}-${ev.eventType}-${i}`}><td className="pr-4 text-gray-900">{formatUnix(ev.timestamp)}</td><td className="text-gray-900">{ev.eventType}</td></tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </div>
  );
}
