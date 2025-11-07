import { useState } from 'react';
import { X, Plus, Trash2, Search } from 'lucide-react';
import { useStore } from '../lib/store';

export default function FilterPanel({ onClose }) {
  const { filters, setFilters, sortBy, setSortBy, sortAsc, setSortAsc, limit, setLimit } = useStore();
  const [localFilters, setLocalFilters] = useState(filters);
  const [newFilterKey, setNewFilterKey] = useState('');
  const [newFilterValue, setNewFilterValue] = useState('');

  const handleAddFilter = () => {
    if (newFilterKey && newFilterValue) {
      setLocalFilters({
        ...localFilters,
        [newFilterKey]: [newFilterValue],
      });
      setNewFilterKey('');
      setNewFilterValue('');
    }
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

  return (
    <div className="w-80 border-r border-gray-200 bg-white flex flex-col">
      {/* Header */}
      <div className="p-4 border-b border-gray-200">
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-lg font-semibold text-gray-900">Filters</h3>
          <button
            onClick={onClose}
            className="p-1 text-gray-400 hover:text-gray-600 rounded"
          >
            <X className="w-5 h-5" />
          </button>
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto p-4 space-y-6">
        {/* Metadata Filters */}
        <div>
          <h4 className="text-sm font-semibold text-gray-700 mb-3">
            Metadata Filters
          </h4>
          
          {/* Existing Filters */}
          {Object.entries(localFilters).length > 0 && (
            <div className="space-y-2 mb-3">
              {Object.entries(localFilters).map(([key, values]) => (
                <div
                  key={key}
                  className="flex items-center justify-between p-2 bg-gray-50 rounded-lg"
                >
                  <div className="flex-1">
                    <div className="text-xs font-medium text-gray-500">{key}</div>
                    <div className="text-sm text-gray-900">
                      {Array.isArray(values) ? values.join(', ') : values}
                    </div>
                  </div>
                  <button
                    onClick={() => handleRemoveFilter(key)}
                    className="p-1 text-red-500 hover:text-red-700"
                  >
                    <Trash2 className="w-4 h-4" />
                  </button>
                </div>
              ))}
            </div>
          )}

          {/* Add New Filter */}
          <div className="space-y-2">
            <input
              type="text"
              placeholder="Filter key (e.g., author)"
              value={newFilterKey}
              onChange={(e) => setNewFilterKey(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
            />
            <input
              type="text"
              placeholder="Filter value"
              value={newFilterValue}
              onChange={(e) => setNewFilterValue(e.target.value)}
              onKeyPress={(e) => e.key === 'Enter' && handleAddFilter()}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
            />
            <button
              onClick={handleAddFilter}
              className="w-full flex items-center justify-center space-x-2 px-3 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors"
            >
              <Plus className="w-4 h-4" />
              <span className="text-sm font-medium">Add Filter</span>
            </button>
          </div>
        </div>

        {/* Sort Options */}
        <div>
          <h4 className="text-sm font-semibold text-gray-700 mb-3">Sort</h4>
          <div className="space-y-2">
            <select
              value={sortBy}
              onChange={(e) => setSortBy(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
            >
              <option value="addedAt">Added Date</option>
              <option value="updatedAt">Updated Date</option>
              <option value="key">Key</option>
            </select>
            
            <div className="flex items-center space-x-2">
              <button
                onClick={() => setSortAsc(false)}
                className={`flex-1 px-3 py-2 rounded-lg text-sm font-medium transition-colors ${
                  !sortAsc
                    ? 'bg-primary-600 text-white'
                    : 'bg-gray-100 text-gray-700 hover:bg-gray-200'
                }`}
              >
                Descending
              </button>
              <button
                onClick={() => setSortAsc(true)}
                className={`flex-1 px-3 py-2 rounded-lg text-sm font-medium transition-colors ${
                  sortAsc
                    ? 'bg-primary-600 text-white'
                    : 'bg-gray-100 text-gray-700 hover:bg-gray-200'
                }`}
              >
                Ascending
              </button>
            </div>
          </div>
        </div>

        {/* Limit */}
        <div>
          <h4 className="text-sm font-semibold text-gray-700 mb-3">Limit</h4>
          <input
            type="number"
            value={limit}
            onChange={(e) => setLimit(parseInt(e.target.value) || 100)}
            min="1"
            max="1000"
            className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary-500"
          />
        </div>
      </div>

      {/* Footer */}
      <div className="p-4 border-t border-gray-200 space-y-2">
        <button
          onClick={handleApply}
          className="w-full flex items-center justify-center space-x-2 px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors"
        >
          <Search className="w-4 h-4" />
          <span className="font-medium">Apply Filters</span>
        </button>
        <button
          onClick={handleClear}
          className="w-full px-4 py-2 bg-gray-100 text-gray-700 rounded-lg hover:bg-gray-200 transition-colors"
        >
          Clear All
        </button>
      </div>
    </div>
  );
}
