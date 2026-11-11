import { useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';

/**
 * MemorySessionForm starts a session (/v1/memory/session) for a user, with an
 * optional scenario and title. Without a TTL the server keeps it 30 days.
 */
export default function MemorySessionForm({ onCreated }) {
  const [userId, setUserId] = useState('');
  const [scenario, setScenario] = useState('');
  const [title, setTitle] = useState('');
  const { busy, error, run } = useAction();

  const submit = (e) => {
    e.preventDefault();
    run(async () => {
      const session = await mddbClient.createMemorySession({
        userId: userId.trim(),
        scenario: scenario.trim() || undefined,
        title: title.trim() || undefined,
      });
      setTitle('');
      onCreated(session);
    });
  };

  const field = (label, value, setValue) => (
    <label className="text-sm text-gray-800">
      <span className="block mb-1">{label}</span>
      <input value={value} onChange={(e) => setValue(e.target.value)} className="w-full px-3 py-2 border border-gray-300 rounded-lg" />
    </label>
  );

  return (
    <details className="bg-white rounded-lg shadow p-4">
      <summary className="cursor-pointer text-sm font-semibold text-gray-900">Start a session</summary>
      <form onSubmit={submit} className="mt-3 space-y-2">
        <div className="grid grid-cols-1 md:grid-cols-3 gap-2">
          {field('User ID', userId, setUserId)}
          {field('Scenario (optional)', scenario, setScenario)}
          {field('Title (optional)', title, setTitle)}
        </div>
        <ApiErrorNotice error={error} />
        <button
          type="submit"
          disabled={busy || !userId.trim()}
          className="px-4 py-2 bg-blue-700 text-white rounded-lg hover:bg-blue-800 disabled:opacity-50 text-sm"
        >
          {busy ? 'Starting…' : 'Start session'}
        </button>
      </form>
    </details>
  );
}
