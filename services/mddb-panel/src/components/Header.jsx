import { Database, RefreshCw, Search, Brain, Type, Layers, LogOut } from 'lucide-react';
import { useStore } from '../lib/store';
import { authManager } from '../lib/auth';
import LimitCappedNotice from './LimitCappedNotice';

export default function Header({ onRefresh }) {
  const { searchMode, setSearchMode } = useStore();
  const isAuthenticated = authManager.isAuthenticated();

  return (
    <header className="bg-white border-b border-gray-200 h-16">
      <div className="h-full px-6 flex items-center justify-between">
        <div className="flex items-center space-x-3">
          <Database className="w-8 h-8 text-primary-600" />
          <div>
            <h1 className="text-xl font-bold text-gray-900">MDDB Panel</h1>
            <p className="text-xs text-gray-500">AI-Native Document Database</p>
          </div>
        </div>

        <div className="flex items-center space-x-4">
          {/* Search Mode Toggle */}
          <div className="flex items-center bg-gray-100 rounded-lg p-0.5">
            <button
              onClick={() => setSearchMode('metadata')}
              className={`flex items-center space-x-1.5 px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
                searchMode === 'metadata'
                  ? 'bg-white text-gray-900 shadow-sm'
                  : 'text-gray-500 hover:text-gray-700'
              }`}
            >
              <Search className="w-3.5 h-3.5" />
              <span>Metadata</span>
            </button>
            <button
              onClick={() => setSearchMode('fulltext')}
              className={`flex items-center space-x-1.5 px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
                searchMode === 'fulltext'
                  ? 'bg-white text-gray-900 shadow-sm'
                  : 'text-gray-500 hover:text-gray-700'
              }`}
            >
              <Type className="w-3.5 h-3.5" />
              <span>Full-Text</span>
            </button>
            <button
              onClick={() => setSearchMode('vector')}
              className={`flex items-center space-x-1.5 px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
                searchMode === 'vector'
                  ? 'bg-white text-gray-900 shadow-sm'
                  : 'text-gray-500 hover:text-gray-700'
              }`}
            >
              <Brain className="w-3.5 h-3.5" />
              <span>Vector</span>
            </button>
            <button
              onClick={() => setSearchMode('hybrid')}
              className={`flex items-center space-x-1.5 px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
                searchMode === 'hybrid'
                  ? 'bg-white text-gray-900 shadow-sm'
                  : 'text-gray-500 hover:text-gray-700'
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

          {isAuthenticated && (
            <button
              onClick={() => authManager.logout()}
              className="flex items-center space-x-2 px-4 py-2 text-gray-600 hover:text-gray-900 hover:bg-gray-100 rounded-lg transition-colors"
              title="Logout"
            >
              <LogOut className="w-4 h-4" />
              <span className="text-sm font-medium">Logout</span>
            </button>
          )}
        </div>
      </div>
      {/* Fixed-position, so the 64px header height the layout relies on stays. */}
      <LimitCappedNotice />
    </header>
  );
}
