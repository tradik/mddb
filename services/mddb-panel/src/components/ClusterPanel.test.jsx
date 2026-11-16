// @vitest-environment jsdom
// ClusterPanel: shows what /v1/replication/status reports (GO-050) — the
// server's health verdict and its reason, the position and log, the binlog
// against its limit, lag in time and entries, and the followers a leader knows.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    getReplicationStatus: vi.fn(), promoteNode: vi.fn(), demoteNode: vi.fn(), getPlacement: vi.fn(async () => ({ enabled: false })),
  },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: ClusterPanel } = await import('./ClusterPanel');

const leader = {
  node_id: 'leader-1',
  role: 'leader',
  current_lsn: 45230,
  log_id: '3f9a0c51d2e4b788',
  binlog_oldest_lsn: 40000,
  binlog_size_bytes: 1024 * 1024,
  binlog_max_size_bytes: 4 * 1024 * 1024,
  binlog_sync: 'commit',
  healthy: true,
  uptime_seconds: 3700,
  followers: [
    { follower_id: 'f-1', address: '10.0.0.2:5000', confirmed_lsn: 45228, lag_ms: 12, lag_entries: 2, lag_exact: true, last_seen_at: 1709500000, connected: true, status: 'healthy' },
    { follower_id: 'f-2', confirmed_lsn: 10, lag_ms: 90000, lag_entries: 45220, lag_exact: false, connected: false, status: 'disconnected' },
  ],
};

const follower = {
  node_id: 'follower-1',
  role: 'follower',
  current_lsn: 45228,
  leader_addr: 'leader:11024',
  binlog_max_size_bytes: 1024,
  connected: false,
  replication_lag_ms: 0,
  lag_entries: 0,
  last_applied_at: 1709500000,
  healthy: false,
  last_error: 'replication stopped, resynchronising from a snapshot: unknown binlog entry type 99',
  followers: [],
};

beforeEach(() => {
  vi.mocked(mddbClient.getReplicationStatus).mockReset();
  vi.mocked(mddbClient.getPlacement).mockResolvedValue({ enabled: false });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('ClusterPanel', () => {
  test('a leader: health, position, binlog and its followers', async () => {
    vi.mocked(mddbClient.getReplicationStatus).mockResolvedValue(leader);
    render(<ClusterPanel />);
    expect((await screen.findByRole('status')).textContent).toBe('healthy');
    expect(screen.getByText('45,230')).toBeTruthy();
    expect(screen.getByText('Log 3f9a0c51d2e4b788')).toBeTruthy();
    expect(screen.getByText('25% of 4 MB · sync commit')).toBeTruthy();
    expect(screen.getByText('Oldest LSN: 40,000')).toBeTruthy();
    expect(screen.getByText('Furthest Follower Lag')).toBeTruthy();
    expect(screen.getAllByText('≥ 1.5m').length).toBe(2); // the card and the table
    expect(screen.getByText('Followers (2)')).toBeTruthy();
    expect(screen.getByText('disconnected')).toBeTruthy();
    expect(screen.getByText('2 entries behind')).toBeTruthy();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  test('an unhealthy follower says why, and keeps a lag history', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.mocked(mddbClient.getReplicationStatus).mockResolvedValue(follower);
    render(<ClusterPanel />);
    expect((await screen.findByRole('status')).textContent).toBe('unhealthy');
    expect(screen.getByRole('alert').textContent).toContain('unknown binlog entry type 99');
    expect(screen.getByText(/not connected/)).toBeTruthy();
    expect(screen.getByText('Applied LSN')).toBeTruthy();
    expect(screen.getByText('Replication Lag')).toBeTruthy();
    expect(screen.getByText('0% of 1 KB · sync interval')).toBeTruthy();
    vi.mocked(mddbClient.getReplicationStatus).mockResolvedValue({
      ...follower, healthy: true, last_error: undefined, connected: true, leader_addr: undefined, replication_lag_ms: 2500, lag_entries: 1, last_applied_at: 0,
    });
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(await screen.findByText(/Lag History/)).toBeTruthy();
    expect(screen.getByText(/stream open/)).toBeTruthy();
    expect(screen.getByText('1 entry behind')).toBeTruthy();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  test('without cluster mode there is no leader election section', async () => {
    vi.mocked(mddbClient.getReplicationStatus).mockResolvedValue({ ...leader, term: 3 });
    render(<ClusterPanel />);
    await screen.findByRole('status');
    expect(screen.queryByText('Leader Election')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Step down' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Promote this node' })).toBeNull();
  });

  test('in a cluster: the election, and a promote reloads the status (GO-051)', async () => {
    const cluster = { node_id: 'n1', state: 'follower', term: 5, leader_id: 'n2', auto_failover: true, nodes: [{ id: 'n1', self: true, reachable: true }] };
    vi.mocked(mddbClient.getReplicationStatus).mockResolvedValue({ ...follower, term: 5, cluster });
    vi.mocked(mddbClient.promoteNode).mockResolvedValue({ node_id: 'n1', state: 'leader', term: 6 });
    render(<ClusterPanel />);
    expect(await screen.findByText('Leader Election')).toBeTruthy();
    expect(screen.getByText('(this node)')).toBeTruthy();
    vi.mocked(mddbClient.getReplicationStatus).mockResolvedValue({ ...follower, role: 'leader', term: 6, cluster: { ...cluster, state: 'leader', term: 6, leader_id: 'n1' } });
    const calls = vi.mocked(mddbClient.getReplicationStatus).mock.calls.length;
    fireEvent.click(screen.getByRole('button', { name: 'Promote this node' }));
    fireEvent.click(screen.getByRole('button', { name: 'Promote' }));
    expect(await screen.findByText('n1 is now leader in term 6.')).toBeTruthy();
    expect(await screen.findByRole('button', { name: 'Step down' })).toBeTruthy();
    expect(vi.mocked(mddbClient.getReplicationStatus).mock.calls.length).toBeGreaterThan(calls);
  });

  test('a standalone node', async () => {
    vi.mocked(mddbClient.getReplicationStatus).mockResolvedValue({ role: 'standalone', healthy: true, current_lsn: 0 });
    render(<ClusterPanel />);
    expect(await screen.findByText('Standalone Mode')).toBeTruthy();
    expect(screen.getByRole('status').textContent).toBe('standalone');
    expect(screen.getByText('-', { selector: 'div.text-2xl' })).toBeTruthy();
    expect(await screen.findByText(/Collection placement is not configured/)).toBeTruthy(); // GO-052
  });

  test('a failure to load, retried, and one after a status was shown', async () => {
    vi.mocked(mddbClient.getReplicationStatus).mockRejectedValueOnce(new Error('API Error (500): down'));
    render(<ClusterPanel />);
    expect(await screen.findByText('API Error (500): down')).toBeTruthy();
    vi.mocked(mddbClient.getReplicationStatus).mockResolvedValue({ healthy: true, followers: [{ follower_id: 'f-9' }] });
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect((await screen.findByRole('status')).textContent).toBe('healthy');
    expect(screen.getAllByText('unknown').length).toBe(2); // a role and a follower status the server left out
    expect(screen.getByText('f-9')).toBeTruthy();
    vi.mocked(mddbClient.getReplicationStatus).mockRejectedValue(new Error('gone'));
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Refresh' }).disabled).toBe(false));
    expect(screen.getByRole('status').textContent).toBe('healthy'); // the last status stays
  });
});
