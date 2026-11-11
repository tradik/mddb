import { useId } from 'react';
import { useStore } from '../lib/store';

/**
 * CollectionNameInput is a labelled text field for a target collection that
 * may not exist yet (imports create it), suggesting the existing ones.
 * CollectionPicker is the select for an existing collection.
 */
export default function CollectionNameInput({ value, onChange, label = 'Collection' }) {
  const id = useId();
  const stats = useStore((s) => s.stats);
  const names = (stats?.collections || []).map((c) => c.name);

  return (
    <label className="block text-sm text-gray-800">
      <span className="block mb-1">{label}</span>
      <input list={`${id}-names`} value={value} onChange={(e) => onChange(e.target.value)} className="w-full px-3 py-2 border border-gray-300 rounded-lg" />
      <datalist id={`${id}-names`}>{names.map((n) => <option key={n} value={n} />)}</datalist>
    </label>
  );
}
