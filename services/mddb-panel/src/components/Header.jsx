import { Database, RefreshCw, Search, Brain, Type, Layers, LogOut } from 'lucide-react';
import { useStore } from '../lib/store';
import { authManager } from '../lib/auth';
import LimitCappedNotice from './LimitCappedNotice';
import IdentityBadge from './IdentityBadge';
import ThemeSwitch from './ThemeSwitch';

export default function Header({ onRefresh }) {
  const { searchMode, setSearchMode } = useStore();
  const isAuthenticated = authManager.isAuthenticated();

  return (
    <header className="bg-surface border-b border-line flex-shrink-0">
      {/* At least 64px high; on a narrow screen the controls wrap to a second
          line instead of pushing the page wider than the window (FE-021). */}
      <div className="min-h-16 px-4 md:px-6 py-2 flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <div className="flex items-center space-x-3">
          <Database className="w-8 h-8 text-accent" />
          <div>
            <h1 className="text-xl font-bold text-fg">MDDB Panel</h1>
            <p className="text-xs text-fg-faint">AI-Native Document Database</p>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2 md:gap-4">
          {/* Search Mode Toggle */}
          <div className="flex items-center bg-surface-subtle rounded-lg p-0.5">
            <button
              onClick={() => setSearchMode('metadata')}
              className={`flex items-center space-x-1.5 px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
                searchMode === 'metadata'
                  ? 'bg-surface text-fg shadow-sm'
                  : 'text-fg-faint hover:text-fg-muted'
              }`}
            >
              <Search className="w-3.5 h-3.5" />
              <span>Metadata</span>
            </button>
            <button
              onClick={() => setSearchMode('fulltext')}
              className={`flex items-center space-x-1.5 px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
                searchMode === 'fulltext'
                  ? 'bg-surface text-fg shadow-sm'
                  : 'text-fg-faint hover:text-fg-muted'
              }`}
            >
              <Type className="w-3.5 h-3.5" />
              <span>Full-Text</span>
            </button>
            <button
              onClick={() => setSearchMode('vector')}
              className={`flex items-center space-x-1.5 px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
                searchMode === 'vector'
                  ? 'bg-surface text-fg shadow-sm'
                  : 'text-fg-faint hover:text-fg-muted'
              }`}
            >
              <Brain className="w-3.5 h-3.5" />
              <span>Vector</span>
            </button>
            <button
              onClick={() => setSearchMode('hybrid')}
              className={`flex items-center space-x-1.5 px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
                searchMode === 'hybrid'
                  ? 'bg-surface text-fg shadow-sm'
                  : 'text-fg-faint hover:text-fg-muted'
              }`}
            >
              <Layers className="w-3.5 h-3.5" />
              <span>Hybrid</span>
            </button>
          </div>

          <button
            onClick={onRefresh}
            className="flex items-center space-x-2 px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors"
          >
            <RefreshCw className="w-4 h-4" />
            <span className="text-sm font-medium">Refresh</span>
          </button>

          <ThemeSwitch />

          {isAuthenticated && <IdentityBadge />}
          {isAuthenticated && (
            <button
              onClick={() => authManager.logout()}
              className="flex items-center space-x-2 px-4 py-2 text-fg-subtle hover:text-fg hover:bg-surface-subtle rounded-lg transition-colors"
              title="Logout"
            >
              <LogOut className="w-4 h-4" />
              <span className="text-sm font-medium">Logout</span>
            </button>
          )}
        </div>
      </div>
      {/* Fixed-position, so it never changes the header's height. */}
      <LimitCappedNotice />
    </header>
  );
}
