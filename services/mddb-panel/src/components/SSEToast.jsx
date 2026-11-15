import { useState, useEffect, useCallback } from 'react';
import { Radio, X } from 'lucide-react';

const EVENT_ICONS = {
  'doc.added': { icon: '+', color: 'bg-green-700', label: 'Added' },
  'doc.updated': { icon: '~', color: 'bg-blue-700', label: 'Updated' },
  'doc.deleted': { icon: '-', color: 'bg-red-700', label: 'Deleted' },
};

const MAX_TOASTS = 5;
const TOAST_DURATION = 4000;

export default function SSEToast({ connected, lastEvent }) {
  const [toasts, setToasts] = useState([]);

  useEffect(() => {
    if (!lastEvent) return;

    const toast = {
      id: Date.now() + Math.random(),
      event: lastEvent.event,
      collection: lastEvent.collection,
      key: lastEvent.key,
      readOnly: lastEvent.readOnly,
      timestamp: Date.now(),
    };

    setToasts((prev) => [toast, ...prev].slice(0, MAX_TOASTS));

    const timer = setTimeout(() => {
      setToasts((prev) => prev.filter((t) => t.id !== toast.id));
    }, TOAST_DURATION);

    return () => clearTimeout(timer);
  }, [lastEvent]);

  const dismiss = useCallback((id) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  return (
    <div className="fixed bottom-4 right-4 z-50 flex flex-col gap-2 max-w-sm">
      {/* Connection indicator */}
      <div className={`flex items-center gap-1.5 text-xs px-2 py-1 rounded-full self-end ${
        connected ? 'bg-success-muted text-success' : 'bg-surface-subtle text-fg-faint'
      }`}>
        <Radio className="w-3 h-3" />
        <span>{connected ? 'Live' : 'Offline'}</span>
      </div>

      {/* Toast notifications */}
      {toasts.map((toast) => {
        const meta = EVENT_ICONS[toast.event] || EVENT_ICONS['doc.updated'];
        return (
          <div
            key={toast.id}
            className="bg-surface border border-line rounded-lg shadow-lg p-3 flex items-start gap-3 animate-slide-in"
          >
            <div className={`${meta.color} text-white w-6 h-6 rounded-full flex items-center justify-center text-xs font-bold flex-shrink-0`}>
              {meta.icon}
            </div>
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-fg truncate">
                {meta.label}: {toast.key}
              </p>
              <p className="text-xs text-fg-faint truncate">
                {toast.collection}
                {toast.readOnly === false && (
                  <span className="ml-1 text-info">(writable)</span>
                )}
              </p>
            </div>
            <button
              onClick={() => dismiss(toast.id)}
              className="text-fg-faint hover:text-fg-subtle flex-shrink-0"
            >
              <X className="w-3.5 h-3.5" />
            </button>
          </div>
        );
      })}
    </div>
  );
}
