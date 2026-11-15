import { useState } from 'react';
import { X, Plus, Trash2, Search } from 'lucide-react';
import { useStore } from '../lib/store';

/** The sort fields the server's search accepts (services/mddbd/util.go sortDocs). */
const SORT_FIELDS = [
  { value: 'addedAt', label: 'Added date' },
  { value: 'updatedAt', label: 'Updated date' },
  { value: 'key', label: 'Key' },
];

const MIN_LIMIT = 1;
const MAX_LIMIT = 1000;
const DEFAULT_LIMIT = 100;

const INPUT = 'w-full px-3 py-2 border border-gray-400 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-600';
const HEADING = 'text-sm font-semibold text-gray-800 mb-2';

/** clampLimit reads the page-size field: a whole number in 1..1000, else 100. */
function clampLimit(raw) {
  const n = Number.parseInt(raw, 10);
  if (Number.isNaN(n)) return DEFAULT_LIMIT;
  return Math.min(MAX_LIMIT, Math.max(MIN_LIMIT, n));
}

/** The order toggle: one pressed button of two. */
function OrderButton({ pressed, onClick, children }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={pressed}
      className={`flex-1 px-3 py-2 rounded-lg text-sm font-medium transition-colors ${
        pressed ? 'bg-primary-700 text-white' : 'bg-gray-100 text-gray-800 hover:bg-gray-200'
      }`}
    >
      {children}
    </button>
  );
}

/**
 * FilterPanel narrows and orders a collection's document list (docs/PANEL.md
 * "Advanced Filtering"): metadata filters (applied with Apply), the sort field
 * and order, and the page size — all kept in the store, which the list's
 * search (`/v1/search`: filterMeta, sort, asc, limit) reads.
 */
export default function FilterPanel({ onClose }) {
  const { filters, setFilters, sortBy, setSortBy, sortAsc, setSortAsc, limit, setLimit } = useStore();
  const [localFilters, setLocalFilters] = useState(filters);
  const [newFilterKey, setNewFilterKey] = useState('');
  const [newFilterValue, setNewFilterValue] = useState('');

  const handleAddFilter = () => {
    const key = newFilterKey.trim();
    const value = newFilterValue.trim();
    if (!key || !value) return;
    setLocalFilters({ ...localFilters, [key]: [value] });
    setNewFilterKey('');
    setNewFilterValue('');
  };

  const handleRemoveFilter = (key) => {
    const updated = { ...localFilters };
    delete updated[key];
    setLocalFilters(updated);
  };

  const handleApply = () => {
    setFilters(localFilters);
    onClose();
  };

  const handleClear = () => {
    setLocalFilters({});
    setFilters({});
  };

  const entries = Object.entries(localFilters);

  return (
    <section aria-label="Sort and filter documents" className="mt-2 border border-gray-200 rounded-lg bg-white">
      <div className="flex items-center justify-between px-3 py-2 border-b border-gray-200">
        <h3 className="text-sm font-semibold text-gray-900">Sort &amp; filter</h3>
        <button type="button" onClick={onClose} aria-label="Close sort and filter" className="p-1 text-gray-600 hover:text-gray-900 rounded">
          <X className="w-4 h-4" aria-hidden="true" />
        </button>
      </div>

      <div className="p-3 space-y-4">
        <fieldset>
          <legend className={HEADING}>Metadata filters</legend>
          {entries.length > 0 && (
            <ul className="space-y-2 mb-2">
              {entries.map(([key, values]) => (
                <li key={key} className="flex items-center justify-between gap-2 p-2 bg-gray-50 rounded-lg">
                  <div className="min-w-0">
                    <div className="text-xs font-medium text-gray-600 truncate" title={key}>{key}</div>
                    <div className="text-sm text-gray-900 break-words">{[values].flat().join(', ')}</div>
                  </div>
                  <button
                    type="button"
                    onClick={() => handleRemoveFilter(key)}
                    aria-label={`Remove filter ${key}`}
                    className="p-1 text-red-700 hover:text-red-900 flex-shrink-0"
                  >
                    <Trash2 className="w-4 h-4" aria-hidden="true" />
                  </button>
                </li>
              ))}
            </ul>
          )}
          <div className="grid grid-cols-2 gap-2">
            <input
              type="text"
              aria-label="Filter key"
              placeholder="Key (e.g. author)"
              value={newFilterKey}
              onChange={(e) => setNewFilterKey(e.target.value)}
              className={INPUT}
            />
            <input
              type="text"
              aria-label="Filter value"
              placeholder="Value"
              value={newFilterValue}
              onChange={(e) => setNewFilterValue(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handleAddFilter()}
              className={INPUT}
            />
          </div>
          <button
            type="button"
            onClick={handleAddFilter}
            className="mt-2 w-full flex items-center justify-center gap-2 px-3 py-2 bg-gray-100 text-gray-900 rounded-lg hover:bg-gray-200 text-sm font-medium"
          >
            <Plus className="w-4 h-4" aria-hidden="true" />
            Add filter
          </button>
        </fieldset>

        <div className="grid grid-cols-2 gap-3">
          <div>
            <label htmlFor="document-sort" className={`block ${HEADING}`}>Sort by</label>
            <select id="document-sort" value={sortBy} onChange={(e) => setSortBy(e.target.value)} className={INPUT}>
              {SORT_FIELDS.map((f) => <option key={f.value} value={f.value}>{f.label}</option>)}
            </select>
          </div>
          <div>
            <label htmlFor="document-limit" className={`block ${HEADING}`}>Per page</label>
            <input
              id="document-limit"
              type="number"
              value={limit}
              onChange={(e) => setLimit(clampLimit(e.target.value))}
              min={MIN_LIMIT}
              max={MAX_LIMIT}
              className={INPUT}
            />
          </div>
        </div>
        <fieldset>
          <legend className={HEADING}>Order</legend>
          <div className="flex items-center gap-2">
            <OrderButton pressed={!sortAsc} onClick={() => setSortAsc(false)}>Descending</OrderButton>
            <OrderButton pressed={sortAsc} onClick={() => setSortAsc(true)}>Ascending</OrderButton>
          </div>
        </fieldset>
      </div>

      <div className="px-3 pb-3 flex gap-2">
        <button
          type="button"
          onClick={handleApply}
          className="flex-1 flex items-center justify-center gap-2 px-4 py-2 bg-primary-700 text-white rounded-lg hover:bg-primary-800 text-sm font-medium"
        >
          <Search className="w-4 h-4" aria-hidden="true" />
          Apply filters
        </button>
        <button
          type="button"
          onClick={handleClear}
          className="flex-1 px-4 py-2 bg-gray-100 text-gray-800 rounded-lg hover:bg-gray-200 text-sm"
        >
          Clear filters
        </button>
      </div>
    </section>
  );
}
