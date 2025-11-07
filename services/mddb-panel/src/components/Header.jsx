import { Database, RefreshCw } from 'lucide-react';

export default function Header({ onRefresh }) {
  return (
    <header className="bg-white border-b border-gray-200 h-16">
      <div className="h-full px-6 flex items-center justify-between">
        <div className="flex items-center space-x-3">
          <Database className="w-8 h-8 text-primary-600" />
          <div>
            <h1 className="text-xl font-bold text-gray-900">MDDB Panel</h1>
            <p className="text-xs text-gray-500">Markdown Database Admin</p>
          </div>
        </div>
        
        <div className="flex items-center space-x-4">
          <button
            onClick={onRefresh}
            className="flex items-center space-x-2 px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors"
          >
            <RefreshCw className="w-4 h-4" />
            <span className="text-sm font-medium">Refresh</span>
          </button>
        </div>
      </div>
    </header>
  );
}
