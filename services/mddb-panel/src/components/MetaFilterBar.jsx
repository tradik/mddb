import { useState, useEffect } from 'react';
import { Filter, X } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';

export default function MetaFilterBar({ collection }) {
  const { searchFilterMeta, setSearchFilterMeta, clearSearchFilterMeta } = useStore();
  const [metaKeys, setMetaKeys] = useState(null);
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (!collection) {
      setMetaKeys(null);
      return;
    }

    let cancelled = false;
    setLoading(true);
    mddbClient.getMetaKeys(collection)
      .then((data) => {
        if (!cancelled) {
          const meta = data.meta || {};
          setMetaKeys(Object.keys(meta).length > 0 ? meta : null);
        }
      })
      .catch(() => {
        if (!cancelled) setMetaKeys(null);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => { cancelled = true; };
  }, [collection]);

  if (loading || !metaKeys) return null;

  const activeCount = Object.values(searchFilterMeta).reduce((sum, arr) => sum + arr.length, 0);

  const toggleValue = (key, value) => {
    const current = searchFilterMeta[key] || [];
    const isSelected = current.includes(value);
    const updated = { ...searchFilterMeta };

    if (isSelected) {
      const filtered = current.filter(v => v !== value);
      if (filtered.length === 0) {
        delete updated[key];
      } else {
        updated[key] = filtered;
      }
    } else {
      updated[key] = [...current, value];
    }

    setSearchFilterMeta(updated);
  };

  const MAX_VALUES = 20;

  return (
    <div className="border border-line rounded-lg">
      <button
        onClick={() => setOpen(!open)}
        className="w-full flex items-center justify-between px-3 py-2 text-xs font-semibold text-fg-subtle uppercase tracking-wider hover:bg-surface-muted rounded-lg"
      >
        <span className="flex items-center space-x-1.5">
          <Filter className="w-3.5 h-3.5" />
          <span>Filter by metadata</span>
          {activeCount > 0 && (
            <span className="inline-flex items-center justify-center px-1.5 py-0.5 rounded-full text-[10px] font-bold bg-accent-muted text-accent">
              {activeCount}
            </span>
          )}
        </span>
        <span className="text-fg-faint">{open ? '\u25B2' : '\u25BC'}</span>
      </button>

      {open && (
        <div className="px-3 pb-3 space-y-2 max-h-[70vh] overflow-y-auto">
          {Object.entries(metaKeys).map(([key, values]) => (
            <div key={key}>
              <span className="text-xs font-medium text-fg-faint mb-1 block">{key}</span>
              <div className="flex flex-wrap gap-1">
                {values.slice(0, MAX_VALUES).map((val) => {
                  const isSelected = (searchFilterMeta[key] || []).includes(val);
                  return (
                    <button
                      key={val}
                      onClick={() => toggleValue(key, val)}
                      className={`px-2 py-0.5 rounded text-xs border transition-colors ${
                        isSelected
                          ? 'bg-accent-muted text-accent border-primary-400'
                          : 'bg-surface-muted text-fg-subtle border-line hover:bg-surface-subtle'
                      }`}
                    >
                      {val}
                    </button>
                  );
                })}
                {values.length > MAX_VALUES && (
                  <span className="text-xs text-fg-faint self-center">
                    (+{values.length - MAX_VALUES} more)
                  </span>
                )}
              </div>
            </div>
          ))}

          {activeCount > 0 && (
            <button
              onClick={clearSearchFilterMeta}
              className="flex items-center space-x-1 text-xs text-danger hover:text-danger mt-1"
            >
              <X className="w-3 h-3" />
              <span>Clear filters</span>
            </button>
          )}
        </div>
      )}
    </div>
  );
}
