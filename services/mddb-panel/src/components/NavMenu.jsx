import { useEffect, useRef, useState } from 'react';
import { Search } from 'lucide-react';
import { useStore } from '../lib/store';
import { useFeatures } from '../lib/features';
import { visibleGroups, groupOfView } from '../lib/nav-groups';
import { useCollapsedGroups } from '../lib/nav-collapse';
import NavGroup from './NavGroup';

/**
 * The sidebar menu (2.19.0): a "Search the menu" box over the groups of
 * nav-groups.js. Only entries the server and the user allow are listed;
 * typing filters them by label and opens every group with a match, Escape
 * clears the box. Every group folds, the open page's too (2.19.2: the
 * default page is System Info, so pinning its group made "System" unfoldable);
 * moving to a page in a folded group unfolds that group. A group with nothing
 * to show is left out.
 */
export default function NavMenu({ authEnabled = false, clusterEnabled = false, automationsEnabled = true }) {
  const { viewMode } = useStore();
  const { features } = useFeatures();
  const { collapsed, toggle, expand } = useCollapsedGroups();
  const [query, setQuery] = useState('');
  const searching = query.trim() !== '';
  const groups = visibleGroups({ features, authEnabled, clusterEnabled, automationsEnabled }, query);
  const activeGroup = groupOfView(viewMode);
  const shownGroup = useRef(activeGroup);

  // Unfold the group of a page reached from elsewhere (a link, a shortcut);
  // not on mount, so a group folded by the user stays folded after a reload.
  useEffect(() => {
    if (shownGroup.current === activeGroup) return;
    shownGroup.current = activeGroup;
    if (activeGroup) expand(activeGroup);
  }, [activeGroup]); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <nav aria-label="Panel menu">
      <div className="relative mb-3">
        <Search className="w-3.5 h-3.5 text-gray-500 absolute left-2.5 top-1/2 -translate-y-1/2" aria-hidden="true" />
        <input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => e.key === 'Escape' && setQuery('')}
          placeholder="Search the menu"
          aria-label="Search the menu"
          className="w-full pl-8 pr-2 py-1.5 text-sm border border-gray-500 rounded-lg text-gray-900 placeholder-gray-500 focus:outline-none focus:ring-2 focus:ring-blue-600"
        />
      </div>
      {groups.length === 0 && (
        <p role="status" className="text-sm text-gray-600 px-3">No menu entries match “{query.trim()}”.</p>
      )}
      <div className="space-y-2">
        {groups.map((group) => {
          return (
            <NavGroup
              key={group.id}
              group={group}
              pinned={searching}
              open={searching || !collapsed.includes(group.id)}
              onToggle={toggle}
            />
          );
        })}
      </div>
    </nav>
  );
}
