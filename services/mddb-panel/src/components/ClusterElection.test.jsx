// @vitest-environment jsdom
// ClusterElection (GO-051): the leader election part of the Cluster tab —
// term, leader, lease, failover, the nodes table, the rejected-writes file,
// and promote / step down behind a confirmation that shows a refusal.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { promoteNode: vi.fn(), demoteNode: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: ClusterElection } = await import('./ClusterElection');

const follower = {
  node_id: 'n1',
  state: 'follower',
  term: 5,
  voted_for: 'n2',
  leader_id: 'n2',
  leader_addr: 'http://10.0.0.2:11023',
  lease_valid: false,
  writable: false,
  auto_failover: true,
  majority: 2,
  ack_mode: 'quorum',
  last_rejected_file: '/data/rejected-writes-n1-term4-20261116T101500Z.jsonl',
  nodes: [
    { id: 'n1', grpc_addr: '10.0.0.1:11024', http_addr: 'http://10.0.0.1:11023', state: 'follower', term: 5, last_lsn: 1234, last_term: 5, last_seen: '2026-11-16T10:15:00Z', reachable: true, self: true },
    { id: 'n2', state: 'leader', reachable: false, last_seen: 'not a date' },
    { id: 'n3' },
  ],
};

const leader = { node_id: 'n2', state: 'leader', term: 6, leader_id: 'n2', lease_valid: true, writable: true, auto_failover: false };

const refused = (message) => Object.assign(new Error(`API Error (409): ${message}`), { status: 409, body: { error: message } });

let onChanged;
beforeEach(() => {
  vi.mocked(mddbClient.promoteNode).mockReset();
  vi.mocked(mddbClient.demoteNode).mockReset();
  onChanged = vi.fn(async () => {});
});
afterEach(cleanup);

describe('ClusterElection', () => {
  test('a follower: term, leader, settings, rejected writes and the nodes', () => {
    render(<ClusterElection cluster={follower} term={4} onChanged={onChanged} />);
    expect(screen.getByText('Leader Election')).toBeTruthy();
    expect(screen.getByText('n2 (http://10.0.0.2:11023)')).toBeTruthy();
    expect(screen.getByText('not held')).toBeTruthy();
    expect(screen.getByText('on')).toBeTruthy();
    expect(screen.getByText('quorum')).toBeTruthy();
    expect(screen.getByText('· majority 2')).toBeTruthy();
    expect(screen.getByText(follower.last_rejected_file)).toBeTruthy();
    expect(screen.getByText(/kept in this file for manual review/)).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Step down' })).toBeNull();

    const rows = within(screen.getByRole('table')).getAllByRole('row');
    expect(rows).toHaveLength(4);
    expect(rows[1].textContent).toContain('(this node)');
    expect(rows[1].textContent).toContain('gRPC 10.0.0.1:11024');
    expect(rows[1].textContent).toContain('1,234 / 5');
    expect(rows[1].textContent).toContain('yes');
    expect(rows[1].lastChild.textContent).not.toBe('-');
    expect(rows[2].textContent).toContain('no');
    expect(rows[2].lastChild.textContent).toBe('-'); // an unreadable time
    expect(rows[3].textContent).toContain('unknown');
    expect(rows[3].textContent).toContain('0 / -');
    expect(rows[3].lastChild.textContent).toBe('-'); // never seen
  });

  test('a leader with what the server left out', () => {
    render(<ClusterElection cluster={{ node_id: 'n9' }} onChanged={onChanged} />);
    expect(screen.getByText('none known')).toBeTruthy();
    expect(screen.getByText('unknown')).toBeTruthy();
    expect(screen.getByText('async')).toBeTruthy();
    expect(screen.getByText('· majority -')).toBeTruthy();
    expect(screen.getByText('off')).toBeTruthy();
    expect(screen.queryByRole('table')).toBeNull();
    expect(screen.queryByText('Rejected writes')).toBeNull();
  });

  test('the term falls back to the status, and a leader without an address', () => {
    render(<ClusterElection cluster={{ ...leader, term: undefined }} term={7} onChanged={onChanged} />);
    expect(screen.getByText('7')).toBeTruthy();
    expect(screen.getAllByText('n2').length).toBe(2); // this node and the leader
    expect(screen.getByText('valid')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Promote this node' })).toBeNull();
  });

  test('promote with automatic failover: no force, and the status reloads', async () => {
    vi.mocked(mddbClient.promoteNode).mockResolvedValue({ node_id: 'n1', state: 'leader', term: 6 });
    render(<ClusterElection cluster={follower} onChanged={onChanged} />);
    fireEvent.click(screen.getByRole('button', { name: 'Promote this node' }));
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).queryByLabelText(/Force/)).toBeNull();
    expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Cancel' }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Promote' }));
    expect((await screen.findByRole('status')).textContent).toBe('n1 is now leader in term 6.');
    expect(mddbClient.promoteNode).toHaveBeenCalledWith({ force: false });
    expect(onChanged).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  test('promote without automatic failover offers force, and a refusal shows', async () => {
    vi.mocked(mddbClient.promoteNode).mockRejectedValueOnce(refused('node n3 has a later log'));
    render(<ClusterElection cluster={{ ...follower, auto_failover: false }} onChanged={onChanged} />);
    fireEvent.click(screen.getByRole('button', { name: 'Promote this node' }));
    const box = screen.getByLabelText('Force, skipping the log check');
    expect(screen.getByText(/Force only when the other nodes are lost/)).toBeTruthy();
    fireEvent.click(box);
    fireEvent.click(screen.getByRole('button', { name: 'Promote' }));
    expect((await screen.findByRole('alert')).textContent).toContain('node n3 has a later log');
    expect(mddbClient.promoteNode).toHaveBeenCalledWith({ force: true });
    expect(onChanged).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Promote this node' }));
    expect(screen.getByLabelText('Force, skipping the log check').checked).toBe(false); // reset
    expect(screen.queryByRole('alert')).toBeNull();
  });

  test('step down: confirmed, refused, cancelled with Escape', async () => {
    vi.mocked(mddbClient.demoteNode)
      .mockRejectedValueOnce(Object.assign(new Error('API Error (400): not in a cluster'), { status: 400 }))
      .mockResolvedValueOnce({ node_id: 'n2', state: 'follower', term: 6 });
    render(<ClusterElection cluster={leader} onChanged={onChanged} />);
    const opener = screen.getByRole('button', { name: 'Step down' });
    opener.focus();
    fireEvent.click(opener);
    expect(screen.getByText(/stops taking writes/)).toBeTruthy();
    fireEvent.keyDown(document, { key: 'Enter' });
    expect(screen.getByRole('dialog')).toBeTruthy();
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(document.activeElement).toBe(opener);

    fireEvent.click(opener);
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Step down' }));
    expect((await screen.findByRole('alert')).textContent).toBe('API Error (400): not in a cluster');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Step down' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(screen.getByRole('status').textContent).toBe('n2 is now follower in term 6.');
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  test('the confirm button is busy while the call runs', async () => {
    let resolve;
    vi.mocked(mddbClient.demoteNode).mockReturnValue(new Promise((r) => { resolve = r; }));
    render(<ClusterElection cluster={leader} onChanged={onChanged} />);
    fireEvent.click(screen.getByRole('button', { name: 'Step down' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Step down' }));
    expect(screen.getByRole('button', { name: 'Working…' }).disabled).toBe(true);
    resolve({ node_id: 'n2', state: 'follower', term: 7 });
    expect(await screen.findByText('n2 is now follower in term 7.')).toBeTruthy();
  });
});
