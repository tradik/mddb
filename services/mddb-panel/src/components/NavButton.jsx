import { useStore } from '../lib/store';

/** One entry of the sidebar menu; the open page is highlighted and marked current. */
export default function NavButton({ mode, icon: Icon, label }) {
  const { viewMode, setViewMode } = useStore();
  const active = viewMode === mode;
  return (
    <button
      type="button"
      onClick={() => setViewMode(mode)}
      aria-current={active ? 'page' : undefined}
      className={`w-full flex items-center space-x-2 px-3 py-2 rounded-lg transition-colors ${active
        ? 'bg-info-muted text-info'
        : 'text-fg-muted hover:bg-surface-subtle'
        }`}
    >
      <Icon className="w-4 h-4" aria-hidden="true" />
      <span className="text-sm font-medium">{label}</span>
    </button>
  );
}
