// @vitest-environment jsdom
// ClusterPanel: shows what /v1/replication/status reports (GO-050) — the
// server's health verdict and its reason, the position and log, the binlog
// against its limit, lag in time and entries, and the followers a leader knows.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getReplicationStatus: vi.fn() } }));

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

  test('a standalone node', async () => {
    vi.mocked(mddbClient.getReplicationStatus).mockResolvedValue({ role: 'standalone', healthy: true, current_lsn: 0 });
    render(<ClusterPanel />);
    expect(await screen.findByText('Standalone Mode')).toBeTruthy();
    expect(screen.getByRole('status').textContent).toBe('standalone');
    expect(screen.getByText('-', { selector: 'div.text-2xl' })).toBeTruthy();
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
