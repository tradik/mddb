// @vitest-environment jsdom
// AuditLogPanel: the audit trail viewer — loading, the empty state, rows keyed
// by event, the dropped-events badge, the filter bar, the presets other pages
// send, and the three ways a request fails (403, 404, anything else).
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { listAuditEvents: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: AuditLogPanel } = await import('./AuditLogPanel');

const initialStore = useStore.getState();

// 2026-10-30 12:00:00 UTC in nanoseconds.
const tsNanos = '1793361600000000000';

const okEvent = {
  ts: tsNanos,
  actor: 'admin',
  action: 'auth.login',
  result: 'ok',
  ip: '10.0.0.1',
  resource: '/v1/login',
  userAgent: 'curl/8',
  detail: 'password',
};

/** The rendered table rows, header excluded. */
function bodyRows(container) {
  return [...container.querySelectorAll('tbody tr')];
}

/** The text of each cell in a row. */
function cells(row) {
  return [...row.querySelectorAll('td')].map((td) => td.textContent);
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  vi.mocked(mddbClient.listAuditEvents).mockReset();
});
afterEach(cleanup);

describe('AuditLogPanel', () => {
  test('shows Loading while the first request is in flight', () => {
    vi.mocked(mddbClient.listAuditEvents).mockReturnValue(new Promise(() => {}));
    render(<AuditLogPanel />);
    expect(screen.getByText('Loading…')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Refresh' }).disabled).toBe(true);
  });

  test('says so when no event matches', async () => {
    vi.mocked(mddbClient.listAuditEvents).mockResolvedValue({});
    const { container } = render(<AuditLogPanel />);
    expect(await screen.findByText('No audit events match the current filter.')).toBeTruthy();
    expect(container.textContent).not.toContain('dropped');
    expect(mddbClient.listAuditEvents).toHaveBeenCalledWith({ actor: '', action: '', result: '', limit: 200 });
  });

  test('renders one row per event, with placeholders for missing fields', async () => {
    vi.mocked(mddbClient.listAuditEvents).mockResolvedValue({
      events: [
        okEvent,
        { ts: '2', actor: 'eve', action: 'doc.delete', result: 'fail' },
        { ts: 0, result: 'denied' },
        { ts: 'not-a-number' },
      ],
      dropped: 1234,
    });
    const { container } = render(<AuditLogPanel />);
    await screen.findByText('auth.login');

    const rows = bodyRows(container);
    expect(rows).toHaveLength(4);
    expect(cells(rows[0])).toEqual([
      '2026-10-30 12:00:00.000', 'admin', 'auth.login', 'OK', '10.0.0.1', '/v1/login', 'curl/8', 'password',
    ]);
    expect(rows[0].querySelector('span').className).toContain('bg-green-100');
    expect(rows[1].querySelector('span').className).toContain('bg-red-100');
    expect(cells(rows[2])).toEqual(['', '-', '-', 'DENIED', '-', '-', '-', '-']);
    expect(rows[2].querySelector('span').className).toContain('bg-gray-100');
    expect(cells(rows[3])[0]).toBe('not-a-number');
    expect(cells(rows[3])[3]).toBe('-');
    expect(screen.getByText(/1,234 dropped/)).toBeTruthy();
  });

  test('keys rows by event: same-nanosecond events by different actions do not collide', async () => {
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {});
    vi.mocked(mddbClient.listAuditEvents).mockResolvedValue({
      events: [okEvent, { ...okEvent, action: 'doc.add' }, { ...okEvent, actor: 'eve' }],
    });
    const { container } = render(<AuditLogPanel />);
    await screen.findByText('doc.add');
    expect(bodyRows(container)).toHaveLength(3);
    const keyWarnings = errors.mock.calls.filter((args) => String(args[0]).includes('same key'));
    expect(keyWarnings).toEqual([]);
  });

  test('sends the filter bar on Apply', async () => {
    vi.mocked(mddbClient.listAuditEvents).mockResolvedValue({ events: [] });
    const { container } = render(<AuditLogPanel />);
    await screen.findByText('No audit events match the current filter.');

    fireEvent.change(screen.getByPlaceholderText('actor'), { target: { value: 'bob' } });
    fireEvent.change(screen.getByPlaceholderText('action'), { target: { value: 'auth.login' } });
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'fail' } });
    const [from, to] = container.querySelectorAll('input[type="datetime-local"]');
    fireEvent.change(from, { target: { value: '2026-10-01T00:00' } });
    fireEvent.change(to, { target: { value: '2026-10-02T00:00' } });
    fireEvent.change(screen.getByTitle('limit'), { target: { value: '50' } });
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    await screen.findByText('No audit events match the current filter.');

    expect(mddbClient.listAuditEvents).toHaveBeenLastCalledWith({
      actor: 'bob',
      action: 'auth.login',
      result: 'fail',
      limit: 50,
      from: new Date('2026-10-01T00:00').toISOString(),
      to: new Date('2026-10-02T00:00').toISOString(),
    });

    // A cleared limit falls back to the default.
    fireEvent.change(screen.getByTitle('limit'), { target: { value: '' } });
    expect(screen.getByTitle('limit').value).toBe('200');
  });

  test('applies a preset from another page once, then clears it', async () => {
    vi.mocked(mddbClient.listAuditEvents).mockResolvedValue({ events: [] });
    useStore.setState({ auditFilterPreset: { actor: 'eve', action: 'auth.login', result: 'fail' } });
    render(<AuditLogPanel />);
    await screen.findByText('No audit events match the current filter.');
    expect(screen.getByPlaceholderText('actor').value).toBe('eve');
    expect(screen.getByPlaceholderText('action').value).toBe('auth.login');
    expect(screen.getByRole('combobox').value).toBe('fail');
    expect(useStore.getState().auditFilterPreset).toBeNull();

    // A partial preset leaves the other filters as they are.
    await act(async () => useStore.setState({ auditFilterPreset: { result: 'ok' } }));
    expect(screen.getByPlaceholderText('actor').value).toBe('eve');
    expect(screen.getByRole('combobox').value).toBe('ok');
  });

  test.each([
    ['API Error (403): no'],
    ['Forbidden'],
  ])('shows the admin-only notice on %s', async (message) => {
    vi.mocked(mddbClient.listAuditEvents).mockRejectedValue(new Error(message));
    render(<AuditLogPanel />);
    expect(await screen.findByText('Admin only')).toBeTruthy();
    expect(screen.queryByText('Audit Log')).toBeNull();
  });

  test('explains a 404 as the audit log being off', async () => {
    vi.mocked(mddbClient.listAuditEvents).mockRejectedValue(new Error('API Error (404): not found'));
    render(<AuditLogPanel />);
    expect(await screen.findByText(/MDDB_AUDIT_ENABLED=true/)).toBeTruthy();
  });

  test('shows any other failure as it came', async () => {
    vi.mocked(mddbClient.listAuditEvents).mockRejectedValue(new Error('API Error (500): down'));
    render(<AuditLogPanel />);
    expect(await screen.findByText('API Error (500): down')).toBeTruthy();
  });
});
