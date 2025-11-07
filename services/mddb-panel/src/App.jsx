import { useEffect, useState } from 'react';
import { Database } from 'lucide-react';
import { useStore } from './lib/store';
import mddbClient from './lib/mddb-client';
import Header from './components/Header';
import Sidebar from './components/Sidebar';
import DocumentList from './components/DocumentList';
import DocumentViewer from './components/DocumentViewer';

function App() {
  const { 
    stats, 
    statsLoading, 
    statsError, 
    setStats, 
    setStatsLoading, 
    setStatsError,
    currentDocument,
    currentCollection
  } = useStore();

  useEffect(() => {
    loadStats();
  }, []);

  const loadStats = async () => {
    setStatsLoading(true);
    setStatsError(null);
    try {
      const data = await mddbClient.getStats();
      setStats(data);
    } catch (error) {
      setStatsError(error.message);
      console.error('Failed to load stats:', error);
    } finally {
      setStatsLoading(false);
    }
  };
  
  return (
    <div style={{ minHeight: '100vh', backgroundColor: '#f5f5f5' }}>
      <Header onRefresh={loadStats} />
      
      <div style={{ display: 'flex', height: 'calc(100vh - 64px)' }}>
        <div style={{ width: '300px', borderRight: '1px solid #ddd' }}>
          <Sidebar stats={stats} statsError={statsError} />
        </div>
        <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
          <div style={{ padding: '20px' }}>
            <h1 style={{ fontSize: '24px', fontWeight: 'bold' }}>MDDB Panel - Working Version</h1>
            <Database style={{ width: '24px', height: '24px' }} />
            <p>Stats loaded: {stats ? 'Yes' : 'No'}</p>
            <p>Loading: {statsLoading ? 'Yes' : 'No'}</p>
            <p>Error: {statsError || 'None'}</p>
          </div>
          <div style={{ flex: 1, display: 'flex' }}>
            <div style={{ flex: currentDocument ? 1 : 1, borderLeft: '1px solid #ddd' }}>
              <DocumentList />
            </div>
            {currentDocument && (
              <div style={{ flex: 1, borderLeft: '1px solid #ddd' }}>
                <DocumentViewer />
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

export default App;
