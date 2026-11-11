import { useState } from 'react';
import mddbClient from '../lib/mddb-client';
import { useAction } from '../lib/use-action';
import ApiErrorNotice from './ApiErrorNotice';

/** The roles /v1/memory/message accepts. */
const ROLES = ['user', 'assistant', 'system', 'tool'];

/**
 * MemoryMessageForm adds one message to a session (/v1/memory/message). The
 * server embeds it for recall; `onAdded` reloads the history.
 */
export default function MemoryMessageForm({ sessionId, onAdded }) {
  const [role, setRole] = useState('user');
  const [content, setContent] = useState('');
  const { busy, error, run } = useAction();

  const submit = (e) => {
    e.preventDefault();
    run(async () => {
      await mddbClient.addMemoryMessage({ sessionId, role, content: content.trim() });
      setContent('');
      onAdded();
    });
  };

  return (
    <form onSubmit={submit} className="space-y-2 border-t border-gray-200 pt-3">
      <h4 className="text-sm font-semibold text-gray-900">Add a message</h4>
      <div className="flex gap-2 items-start">
        <label className="text-sm text-gray-800">
          <span className="block mb-1">Role</span>
          <select value={role} onChange={(e) => setRole(e.target.value)} className="px-2 py-2 border border-gray-300 rounded-lg">
            {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
          </select>
        </label>
        <label className="flex-1 text-sm text-gray-800">
          <span className="block mb-1">Content</span>
          <textarea
            value={content}
            onChange={(e) => setContent(e.target.value)}
            rows={2}
            className="w-full px-3 py-2 border border-gray-300 rounded-lg"
          />
        </label>
      </div>
      <ApiErrorNotice error={error} />
      <button
        type="submit"
        disabled={busy || !content.trim()}
        className="px-4 py-2 bg-blue-700 text-white rounded-lg hover:bg-blue-800 disabled:opacity-50 text-sm"
      >
        {busy ? 'Adding…' : 'Add message'}
      </button>
    </form>
  );
}
