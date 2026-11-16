// @vitest-environment jsdom
// ClusterPlacement (GO-052): the collection placement part of the Cluster
// tab — this node's group and the meta group, the groups, which group holds
// each collection with a Move action, the moves, and polling while one runs.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getPlacement: vi.fn(), moveCollection: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: ClusterPlacement, MOVE_POLL_INTERVAL } = await import('./ClusterPlacement');

const groups = [
  { name: 'meta', meta: true, leader: 'a1', leaderHttp: 'http://a1:11023', nodes: [{ id: 'a1', http: 'http://a1:11023', grpc: 'a1:11024' }], collections: 10 },
  { name: 'g2', leader: '', nodes: [{ id: 'b1' }, { id: 'b2', http: 'http://b2:11023' }], collections: 1200 },
  { name: 'g3' },
];

const copying = {
  collection: 'blog', from: 'g1', to: 'g2', phase: 'copying', startedAt: '2026-11-16T10:00:00Z', finishedAt: '', keysCopied: 1234, sourceLsn: 999, appliedLsn: 950, error: '', durationMs: 0,
};
const failed = {
  collection: 'docs', from: 'g2', to: 'g3', phase: 'failed', startedAt: 'bad', keysCopied: 0, sourceLsn: 0, error: 'target group has no leader', durationMs: 0,
};
const done = { collection: 'wiki', from: 'meta', to: 'g2', phase: 'done', startedAt: '2026-11-16T09:00:00Z', keysCopied: 5, sourceLsn: 10, appliedLsn: 12, durationMs: 1500 };

const base = {
  enabled: true, nodeId: 'b1', group: 'g2', metaGroup: 'meta', version: 1731800000000000000, term: 3, route: 'forward', groups,
};

const placement = (extra) => ({ ...base, ...extra });

const refused = (status, message) => Object.assign(new Error(`API Error (${status}): ${message}`), { status, body: { error: message } });

beforeEach(() => {
  vi.mocked(mddbClient.getPlacement).mockReset();
  vi.mocked(mddbClient.moveCollection).mockReset();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('ClusterPlacement', () => {
  test('not configured: a note and nothing else', async () => {
    vi.mocked(mddbClient.getPlacement).mockResolvedValue({ enabled: false });
    render(<ClusterPlacement />);
    expect(await screen.findByText(/Collection placement is not configured/)).toBeTruthy();
    expect(screen.getByText('MDDB_PLACEMENT_GROUPS')).toBeTruthy();
    expect(screen.queryByRole('table')).toBeNull();
  });

  test('a server without the route counts as not configured', async () => {
    vi.mocked(mddbClient.getPlacement).mockRejectedValue(refused(404, 'not found'));
    render(<ClusterPlacement />);
    expect(await screen.findByText(/Collection placement is not configured/)).toBeTruthy();
  });

  test('a failure to load shows, and a later failure keeps the view', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.mocked(mddbClient.getPlacement).mockRejectedValueOnce(new Error('API Error (500): down'));
    const first = render(<ClusterPlacement />);
    expect((await screen.findByRole('alert')).textContent).toBe('API Error (500): down');
    first.unmount();

    vi.mocked(mddbClient.getPlacement).mockResolvedValueOnce(placement({ collections: [{ name: 'blog', group: 'g2', movingTo: 'g3' }], moves: [copying] }));
    vi.mocked(mddbClient.getPlacement).mockRejectedValueOnce(new Error('gone'));
    render(<ClusterPlacement />);
    expect(await screen.findByText('Collection Placement')).toBeTruthy();
    await act(async () => { await vi.advanceTimersByTimeAsync(MOVE_POLL_INTERVAL); });
    expect(screen.getByRole('alert').textContent).toBe('gone');
    expect(screen.getByText('Collection Placement')).toBeTruthy();
  });

  test('groups, collections and moves', async () => {
    vi.mocked(mddbClient.getPlacement).mockResolvedValue(placement({
      collections: [{ name: 'blog', group: 'g2', movingTo: '' }, { name: 'docs', group: 'g3' }],
      moves: [copying, failed, done],
    }));
    render(<ClusterPlacement />);
    const groupTable = await screen.findByRole('table', { name: 'Placement groups' });
    const rows = within(groupTable).getAllByRole('row');
    expect(rows).toHaveLength(4);
    expect(rows[1].textContent).toContain('meta');
    expect(within(rows[1]).getByText('meta', { selector: 'span' })).toBeTruthy();
    expect(rows[1].textContent).toContain('http://a1:11023');
    expect(rows[1].textContent).toContain('10');
    expect(rows[2].textContent).toContain('(this node)');
    expect(rows[2].textContent).toContain('unknown');
    expect(rows[2].textContent).toContain('1,200');
    expect(rows[2].textContent).toContain('b2 http://b2:11023');
    expect(rows[2].textContent).toContain('b1 -');
    expect(rows[3].textContent).toContain('0');

    expect(screen.getAllByText('b1').length).toBeGreaterThan(0);
    expect(screen.getByText('3 / forward')).toBeTruthy();

    const items = screen.getAllByRole('listitem');
    expect(items).toHaveLength(3);
    expect(items[0].textContent).toContain('g1 → g2');
    expect(items[0].textContent).toContain('copying');
    expect(items[0].textContent).toContain('Keys copied 1,234');
    expect(items[0].textContent).toContain('950 / 999 (95%)');
    expect(items[0].textContent).toContain('started');
    expect(items[1].textContent).toContain('target group has no leader');
    expect(items[1].textContent).toContain('Applied / source LSN -');
    expect(items[1].textContent).toContain('· -');
    expect(items[2].textContent).toContain('12 / 10 (100%)');
    expect(items[2].textContent).toContain('took 1.5s');

    // blog is mid-move (a running move), docs is not.
    expect(screen.getByRole('button', { name: 'Move blog' }).disabled).toBe(true);
    expect(screen.getByRole('button', { name: 'Move docs' }).disabled).toBe(false);
  });

  test('what the server left out', async () => {
    vi.mocked(mddbClient.getPlacement).mockResolvedValue({ enabled: true, groups: [{ name: 'only' }], collections: [{ name: 'solo', group: 'only' }] });
    render(<ClusterPlacement />);
    expect(await screen.findByText('- / -')).toBeTruthy();
    expect(screen.getAllByText('-').length).toBe(3);
    expect(screen.queryByText('Moves')).toBeNull();
    expect(screen.getByRole('button', { name: 'Move solo' }).disabled).toBe(true); // nowhere to go
  });

  test('no groups or collections yet', async () => {
    vi.mocked(mddbClient.getPlacement).mockResolvedValue({ enabled: true, moves: [{ collection: 'x' }] });
    render(<ClusterPlacement />);
    expect(await screen.findByText('No collections placed yet.')).toBeTruthy();
    expect(screen.getByText('unknown')).toBeTruthy(); // a move without a phase
    expect(screen.getByText('Keys copied 0')).toBeTruthy();
  });

  test('a collection the map marks as moving cannot be moved', async () => {
    vi.mocked(mddbClient.getPlacement).mockResolvedValue(placement({ collections: [{ name: 'blog', group: 'g2', movingTo: 'g3' }] }));
    render(<ClusterPlacement />);
    expect(await screen.findByText('moving to g3')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Move blog' }).disabled).toBe(true);
  });

  test('a move: target chosen, body sent, the view reloads', async () => {
    vi.mocked(mddbClient.getPlacement).mockResolvedValue(placement({ collections: [{ name: 'blog', group: 'g2' }] }));
    vi.mocked(mddbClient.moveCollection).mockResolvedValue({ move: { ...copying, from: 'g2', to: 'g3' } });
    render(<ClusterPlacement />);
    fireEvent.click(await screen.findByRole('button', { name: 'Move blog' }));
    const dialog = screen.getByRole('dialog');
    const select = within(dialog).getByLabelText('Target group');
    expect([...select.options].map((o) => o.value)).toEqual(['meta', 'g3']);
    expect(select.value).toBe('meta');
    fireEvent.change(select, { target: { value: 'g3' } });
    const calls = vi.mocked(mddbClient.getPlacement).mock.calls.length;
    fireEvent.click(within(dialog).getByRole('button', { name: 'Move' }));
    expect((await screen.findByRole('status')).textContent).toBe('Moving blog to g3.');
    expect(mddbClient.moveCollection).toHaveBeenCalledWith({ collection: 'blog', group: 'g3' });
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(vi.mocked(mddbClient.getPlacement).mock.calls.length).toBeGreaterThan(calls);
  });

  test('an answer without the move still says what started', async () => {
    vi.mocked(mddbClient.getPlacement).mockResolvedValue(placement({ collections: [{ name: 'blog', group: 'g2' }] }));
    vi.mocked(mddbClient.moveCollection).mockResolvedValue({});
    render(<ClusterPlacement />);
    fireEvent.click(await screen.findByRole('button', { name: 'Move blog' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Move' }));
    expect((await screen.findByRole('status')).textContent).toBe('Moving blog to meta.');
  });

  test.each([
    [400, 'unknown group "g9"'],
    [409, 'collection blog is already moving'],
  ])('a refused move (%i) stays in the dialog', async (status, message) => {
    vi.mocked(mddbClient.getPlacement).mockResolvedValue(placement({ collections: [{ name: 'blog', group: 'g2' }] }));
    vi.mocked(mddbClient.moveCollection).mockRejectedValue(refused(status, message));
    render(<ClusterPlacement />);
    fireEvent.click(await screen.findByRole('button', { name: 'Move blog' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Move' }));
    expect((await screen.findByRole('alert')).textContent).toContain(message);
    expect(screen.getByRole('dialog')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Move blog' }));
    expect(screen.queryByRole('alert')).toBeNull(); // reset on open
  });

  test('polls every 2 s while a move runs, stops when it ends and on unmount', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const getPlacement = vi.mocked(mddbClient.getPlacement);
    getPlacement.mockResolvedValue(placement({ collections: [{ name: 'blog', group: 'g1' }], moves: [copying] }));
    const view = render(<ClusterPlacement />);
    expect(await screen.findByText('copying')).toBeTruthy();
    expect(getPlacement).toHaveBeenCalledTimes(1);

    await act(async () => { await vi.advanceTimersByTimeAsync(MOVE_POLL_INTERVAL); });
    expect(getPlacement).toHaveBeenCalledTimes(2);

    getPlacement.mockResolvedValue(placement({ collections: [{ name: 'blog', group: 'g2' }], moves: [{ ...copying, phase: 'done' }] }));
    await act(async () => { await vi.advanceTimersByTimeAsync(MOVE_POLL_INTERVAL); });
    expect(getPlacement).toHaveBeenCalledTimes(3);
    expect(screen.getByText('done')).toBeTruthy();
    await act(async () => { await vi.advanceTimersByTimeAsync(MOVE_POLL_INTERVAL * 3); });
    expect(getPlacement).toHaveBeenCalledTimes(3); // nothing runs: no polling

    getPlacement.mockResolvedValue(placement({ moves: [{ ...copying, phase: 'fenced' }] }));
    view.unmount();
    render(<ClusterPlacement />);
    expect(await screen.findByText('fenced')).toBeTruthy();
    expect(getPlacement).toHaveBeenCalledTimes(4);
    cleanup();
    await act(async () => { await vi.advanceTimersByTimeAsync(MOVE_POLL_INTERVAL * 3); });
    expect(getPlacement).toHaveBeenCalledTimes(4); // unmounted: no polling
  });

  test('a catching-up move badge, and the phases each have a colour', async () => {
    vi.mocked(mddbClient.getPlacement).mockResolvedValue(placement({ moves: [{ ...copying, phase: 'catching-up' }] }));
    render(<ClusterPlacement />);
    const badge = await screen.findByText('catching-up');
    expect(badge.className).toContain('bg-info-muted');
  });
});
