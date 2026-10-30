// @vitest-environment jsdom
// CronRunNow (2.18.11, #377): the Run now button of a sync or backup rule —
// the job id it reports, a refusal, the busy state — and the last result line.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { runAutomation: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { LastRunResult, RunNowButton } = await import('./CronRunNow');
const client = vi.mocked(mddbClient);

afterEach(cleanup);

const sync = { id: 's1', name: 'Docs', type: 'cron', sync: { source: 'sitemap' } };

describe('RunNowButton', () => {
  test('starts the run and shows its job id', async () => {
    client.runAutomation.mockResolvedValue({ jobId: 'sync-s1-20261030T120000Z' });
    render(<RunNowButton rule={sync} />);
    fireEvent.click(screen.getByRole('button', { name: 'Run Docs now' }));
    expect((await screen.findByRole('status')).textContent).toContain('Started: job sync-s1-20261030T120000Z');
    expect(client.runAutomation).toHaveBeenCalledWith('s1');
    expect(screen.getByRole('button', { name: 'Run Docs now' }).disabled).toBe(false);
  });

  test('shows why the server refused, and is busy while it asks', async () => {
    let refuse;
    client.runAutomation.mockReturnValue(new Promise((_, reject) => { refuse = reject; }));
    render(<RunNowButton rule={sync} />);
    const button = screen.getByRole('button', { name: 'Run Docs now' });
    fireEvent.click(button);
    expect(button.disabled).toBe(true);
    refuse(new Error('the previous run of this rule is still going'));
    expect((await screen.findByRole('status')).textContent).toContain('still going');
    expect(screen.getByRole('status').className).toContain('text-red-700');
  });
});

describe('LastRunResult', () => {
  test('nothing before the first run', () => {
    const { container } = render(<LastRunResult rule={sync} />);
    expect(container.innerHTML).toBe('');
  });

  test('a failed sync in red with its counts and error', () => {
    const rule = { ...sync, lastResult: { status: 'error', added: 2, failed: 1, error: '1 of 3 sources failed' } };
    render(<LastRunResult rule={rule} />);
    const line = screen.getByText(/Last run:/);
    expect(line.textContent).toContain('Last run: error · +2 ~0 =0 −0 ✕1 · 1 of 3 sources failed');
    expect(line.className).toContain('text-red-700');
  });

  test('a successful backup without counts', () => {
    render(<LastRunResult rule={{ id: 'b1', backup: {}, lastResult: { status: 'success' } }} />);
    const line = screen.getByText(/Last run:/);
    expect(line.textContent).toContain('Last run: success');
    expect(line.className).toContain('text-gray-600');
  });
});
