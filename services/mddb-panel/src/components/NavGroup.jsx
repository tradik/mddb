import { ChevronDown, ChevronRight } from 'lucide-react';
import NavButton from './NavButton';

/**
 * One collapsible group of the sidebar menu. The header is a disclosure
 * button (aria-expanded, aria-controls); a `pinned` group — the open page's,
 * or any group while the menu is searched — stays open and ignores clicks.
 */
export default function NavGroup({ group, open, pinned = false, onToggle }) {
  const listId = `nav-group-${group.id}`;
  const Chevron = open ? ChevronDown : ChevronRight;
  return (
    <div>
      <button
        type="button"
        aria-expanded={open}
        aria-controls={listId}
        aria-label={`${group.label} group`}
        aria-disabled={pinned || undefined}
        title={pinned ? 'Kept open: it holds the open page or a match' : undefined}
        onClick={() => !pinned && onToggle(group.id)}
        className="w-full flex items-center justify-between gap-2 px-3 py-1.5 rounded text-left text-xs font-semibold text-gray-600 uppercase tracking-wide hover:bg-gray-100 focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-600"
      >
        {/* One line, left-aligned like every group; a sidebar too narrow for
            the label cuts it with an ellipsis rather than wrapping it. */}
        <span className="min-w-0 flex-1 truncate">{group.label}</span>
        <Chevron className="w-3.5 h-3.5 shrink-0" aria-hidden="true" />
      </button>
      <div id={listId} hidden={!open} className="space-y-1 mt-1">
        {group.items.map((item) => (
          <NavButton key={item.mode} mode={item.mode} icon={item.icon} label={item.label} />
        ))}
      </div>
    </div>
  );
}
