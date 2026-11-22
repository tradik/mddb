import { useEffect } from 'react';
import { Server, Cpu, HardDrive, Clock, Package, Network } from 'lucide-react';
import { useStore } from '../lib/store';
import mddbClient from '../lib/mddb-client';
import GainCard from './GainCard';
import { openCorpusPage } from '../lib/corpus-intent';
import DiagnoseCard from './DiagnoseCard';
import ServerEdgeCard from './ServerEdgeCard';
import StorageCard from './StorageCard';

export default function SystemInfo() {
  const {
    systemInfo,
    systemInfoLoading,
    systemInfoError,
    setSystemInfo,
    setSystemInfoLoading,
    setSystemInfoError,
    setViewMode,
  } = useStore();

  useEffect(() => {
    loadSystemInfo();
  }, []);

  const loadSystemInfo = async () => {
    setSystemInfoLoading(true);
    setSystemInfoError(null);
    try {
      const data = await mddbClient.getSystemInfo();
      setSystemInfo(data);
    } catch (error) {
      setSystemInfoError(error.message);
      console.error('Failed to load system info:', error);
    } finally {
      setSystemInfoLoading(false);
    }
  };

  const formatBytes = (bytes) => {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return Math.round(bytes / Math.pow(k, i) * 100) / 100 + ' ' + sizes[i];
  };

  const formatUptime = (seconds) => {
    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);

    if (days > 0) return `${days}d ${hours}h ${minutes}m`;
    if (hours > 0) return `${hours}h ${minutes}m`;
    return `${minutes}m`;
  };

  // Only called once systemInfo is loaded (see the early returns below).
  const getMemoryPercentage = () => {
    return Math.round((systemInfo.memoryUsed / systemInfo.memorySystem) * 100);
  };

  if (systemInfoLoading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-primary-600"></div>
      </div>
    );
  }

  if (systemInfoError) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center">
          <div className="text-danger font-medium mb-2">Failed to load system info</div>
          <div className="text-sm text-fg-faint">{systemInfoError}</div>
          <button
            onClick={loadSystemInfo}
            className="mt-4 px-4 py-2 bg-primary-600 text-white rounded hover:bg-primary-700"
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  if (!systemInfo) return null;

  return (
    <div className="h-full overflow-y-auto bg-surface-muted p-6">
      <div className="max-w-4xl mx-auto">
        <div className="mb-6">
          <h1 className="text-2xl font-bold text-fg mb-2">System Information</h1>
          <p className="text-fg-subtle">Server hardware and runtime details</p>
        </div>

        {/* Server Info Card */}
        <div className="bg-surface rounded-lg shadow mb-6 p-6">
          <div className="flex items-center mb-4">
            <Server className="w-5 h-5 text-accent mr-2" />
            <h2 className="text-lg font-semibold text-fg">Server</h2>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <div className="text-sm text-fg-subtle">Hostname</div>
              <div className="font-medium text-fg">{systemInfo.hostname}</div>
            </div>
            <div>
              <div className="text-sm text-fg-subtle">Uptime</div>
              <div className="font-medium text-fg">{formatUptime(systemInfo.uptimeSeconds)}</div>
            </div>
          </div>
        </div>

        {/* Hardware Card */}
        <div className="bg-surface rounded-lg shadow mb-6 p-6">
          <div className="flex items-center mb-4">
            <Cpu className="w-5 h-5 text-accent mr-2" />
            <h2 className="text-lg font-semibold text-fg">Hardware</h2>
          </div>
          <div className="space-y-4">
            <div>
              <div className="flex justify-between text-sm mb-1">
                <span className="text-fg-subtle">CPU Usage ({systemInfo.numCPU} cores)</span>
                <span className="font-medium text-fg">{Math.round(systemInfo.cpuUsagePercent || 0)}%</span>
              </div>
              <div className="w-full bg-surface-strong rounded-full h-2">
                <div
                  className={`h-2 rounded-full transition-all ${
                    (systemInfo.cpuUsagePercent || 0) > 80 ? 'bg-red-500' :
                    (systemInfo.cpuUsagePercent || 0) > 50 ? 'bg-yellow-500' : 'bg-green-500'
                  }`}
                  style={{ width: `${Math.min(Math.round(systemInfo.cpuUsagePercent || 0), 100)}%` }}
                ></div>
              </div>
            </div>
            <div className="grid grid-cols-3 gap-4 pt-2 border-t">
              <div>
                <div className="text-sm text-fg-subtle">Operating System</div>
                <div className="font-medium text-fg">{systemInfo.os} ({systemInfo.arch})</div>
              </div>
              <div>
                <div className="text-sm text-fg-subtle">CPU Cores</div>
                <div className="font-medium text-fg">{systemInfo.numCPU}</div>
              </div>
              <div>
                <div className="text-sm text-fg-subtle">Goroutines</div>
                <div className="font-medium text-fg">{systemInfo.numGoroutines.toLocaleString()}</div>
              </div>
            </div>
          </div>
        </div>

        {/* Memory Card */}
        <div className="bg-surface rounded-lg shadow mb-6 p-6">
          <div className="flex items-center mb-4">
            <HardDrive className="w-5 h-5 text-accent mr-2" />
            <h2 className="text-lg font-semibold text-fg">Memory</h2>
          </div>
          <div className="space-y-4">
            <div>
              <div className="flex justify-between text-sm mb-1">
                <span className="text-fg-subtle">Heap Memory Usage</span>
                <span className="font-medium text-fg">{getMemoryPercentage()}%</span>
              </div>
              <div className="w-full bg-surface-strong rounded-full h-2">
                <div
                  className="bg-primary-600 h-2 rounded-full transition-all"
                  style={{ width: `${getMemoryPercentage()}%` }}
                ></div>
              </div>
              <div className="flex justify-between text-xs text-fg-faint mt-1">
                <span>{formatBytes(systemInfo.memoryUsed)} used</span>
                <span>{formatBytes(systemInfo.memorySystem)} system</span>
              </div>
            </div>
            <div className="grid grid-cols-3 gap-4 pt-2 border-t">
              <div>
                <div className="text-sm text-fg-subtle">Total Allocated</div>
                <div className="font-medium text-fg">{formatBytes(systemInfo.memoryTotal)}</div>
              </div>
              <div>
                <div className="text-sm text-fg-subtle">In Use</div>
                <div className="font-medium text-fg">{formatBytes(systemInfo.memoryUsed)}</div>
              </div>
              <div>
                <div className="text-sm text-fg-subtle">Heap In Use</div>
                <div className="font-medium text-fg">{formatBytes(systemInfo.memoryHeap)}</div>
              </div>
            </div>
          </div>
        </div>

        {/* Network Card */}
        {systemInfo.network && systemInfo.network.length > 0 && (
          <div className="bg-surface rounded-lg shadow mb-6 p-6">
            <div className="flex items-center mb-4">
              <Network className="w-5 h-5 text-accent mr-2" />
              <h2 className="text-lg font-semibold text-fg">Network</h2>
            </div>
            <div className="space-y-3">
              {systemInfo.network.map((iface, idx) => (
                <div key={idx} className={idx > 0 ? 'pt-3 border-t' : ''}>
                  <div className="flex items-center justify-between mb-1">
                    <div className="font-medium text-fg">{iface.name}</div>
                    <div className="text-xs text-fg-faint">{iface.flags}</div>
                  </div>
                  <div className="space-y-0.5">
                    {iface.addresses.map((addr, i) => (
                      <div key={i} className="text-sm text-fg-subtle font-mono">{addr}</div>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* Software Card */}
        <div className="bg-surface rounded-lg shadow p-6">
          <div className="flex items-center mb-4">
            <Package className="w-5 h-5 text-accent mr-2" />
            <h2 className="text-lg font-semibold text-fg">Software</h2>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <div className="text-sm text-fg-subtle">Go Version</div>
              <div className="font-medium text-fg">{systemInfo.goVersion}</div>
            </div>
            <div>
              <div className="text-sm text-fg-subtle">MDDB Version</div>
              <div className="font-medium text-fg">{systemInfo.version}</div>
            </div>
          </div>
        </div>

        {/* Search surface, tracing and proxies, from GET /v1/config (2.18.10) */}
        <div className="mt-6">
          <ServerEdgeCard />
        </div>

        <GainCard />

        {/* 2.18.2: where the bytes of the database file are */}
        <StorageCard />

        {/* FE-022: corpus packs have their own page, beside the collections */}
        <p className="mb-6 text-sm text-fg-soft">
          Corpus packs are on their own page: <button type="button" onClick={() => openCorpusPage()} className="underline text-info-strong hover:text-fg">Corpus packs</button>.
        </p>

        <DiagnoseCard />

        {/* 2.19.11: backups and collection upkeep have pages of their own. */}
        <p className="text-sm text-fg-muted">
          Backups now live on{' '}
          <button type="button" onClick={() => setViewMode('backups')} className="text-info-strong underline hover:no-underline">Backup &amp; Restore</button>,
          and checksum, export, revision clean-up and indexing status on{' '}
          <button type="button" onClick={() => setViewMode('maintenance')} className="text-info-strong underline hover:no-underline">Collection Maintenance</button>.
        </p>

        {/* Refresh Button */}
        <div className="mt-6 flex justify-end">
          <button
            onClick={loadSystemInfo}
            disabled={systemInfoLoading}
            className="flex items-center px-4 py-2 bg-primary-600 text-white rounded hover:bg-primary-700 disabled:opacity-50"
          >
            <Clock className="w-4 h-4 mr-2" />
            Refresh
          </button>
        </div>
      </div>
    </div>
  );
}
