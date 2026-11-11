import { useId } from 'react';
import { useStore } from '../lib/store';

/**
 * CollectionPicker is a labelled select of the server's collections (from
 * the stats the sidebar loads). `placeholder` adds an empty first option,
 * for forms where "no collection" or "all" is a valid choice.
 */
export default function CollectionPicker({ value, onChange, label = 'Collection', placeholder, disabled = false }) {
  const id = useId();
  const stats = useStore((s) => s.stats);
  const names = (stats?.collections || []).map((c) => c.name);

  return (
    <div>
      <label htmlFor={id} className="block text-sm font-medium text-gray-800 mb-1">{label}</label>
      <select
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm bg-white"
      >
        {placeholder !== undefined && <option value="">{placeholder}</option>}
        {names.map((name) => <option key={name} value={name}>{name}</option>)}
      </select>
    </div>
  );
}
