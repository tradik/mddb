// @vitest-environment jsdom
// WebhooksPanel: loading the list (array or {webhooks} shape, load errors),
// the rows (events, collection or "all", created date), delete behind a
// confirmation, and the New Webhook form — validation, event toggles, the
// collection filter from the store, saving, a refused save and closing.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: { listWebhooks: vi.fn(), deleteWebhook: vi.fn(), registerWebhook: vi.fn() },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: WebhooksPanel } = await import('./WebhooksPanel');

const initialStore = useStore.getState();
const hook = {
  id: 'w1', url: 'https://example.com/a', events: ['doc.added', 'ops.panic_recovered'],
  collection: 'blog', createdAt: '2026-11-01T10:00:00Z',
};
const bare = { id: 'w2', url: 'https://example.com/b' };

async function renderPanel() {
  render(<WebhooksPanel />);
  await act(async () => {});
}

async function openForm() {
  await renderPanel();
  fireEvent.click(screen.getByRole('button', { name: 'New Webhook' }));
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  for (const fn of Object.values(mddbClient)) fn.mockReset();
  mddbClient.listWebhooks.mockResolvedValue([]);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('WebhooksPanel list', () => {
  test('shows Loading… then the empty state', async () => {
    let finish;
    mddbClient.listWebhooks.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    render(<WebhooksPanel />);
    expect(screen.getByText('Loading…')).toBeTruthy();
    await act(async () => finish([]));
    expect(screen.getByText('No webhooks registered yet.')).toBeTruthy();
  });

  test('renders rows from a plain array with events, collection and date', async () => {
    mddbClient.listWebhooks.mockResolvedValue([hook, bare]);
    await renderPanel();
    expect(screen.getByText('https://example.com/a')).toBeTruthy();
    expect(screen.getByText('ops.panic_recovered')).toBeTruthy();
    expect(screen.getByText('blog')).toBeTruthy();
    expect(screen.getByText('all')).toBeTruthy();
    expect(screen.getAllByText(/Created/)).toHaveLength(1);
  });

  test('accepts the {webhooks} shape and a response without it', async () => {
    mddbClient.listWebhooks.mockResolvedValue({ webhooks: [bare] });
    await renderPanel();
    expect(screen.getByText('https://example.com/b')).toBeTruthy();
    cleanup();
    mddbClient.listWebhooks.mockResolvedValue({});
    await renderPanel();
    expect(screen.getByText('No webhooks registered yet.')).toBeTruthy();
  });

  test('a load error is shown', async () => {
    mddbClient.listWebhooks.mockRejectedValue(new Error('forbidden'));
    await renderPanel();
    expect(screen.getByText('forbidden')).toBeTruthy();
  });

  test('works when the store has no collection configs', async () => {
    useStore.setState({ collectionConfigs: null });
    await openForm();
    expect(screen.getAllByRole('option')).toHaveLength(1);
  });
});

describe('WebhooksPanel delete', () => {
  test('declining the confirmation keeps the webhook', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    mddbClient.listWebhooks.mockResolvedValue([hook]);
    await renderPanel();
    await act(async () => fireEvent.click(screen.getByTitle('Delete')));
    expect(window.confirm).toHaveBeenCalledWith('Delete this webhook?');
    expect(mddbClient.deleteWebhook).not.toHaveBeenCalled();
  });

  test('confirmed delete removes it and reloads', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    mddbClient.listWebhooks.mockResolvedValueOnce([hook]).mockResolvedValueOnce([]);
    mddbClient.deleteWebhook.mockResolvedValue({});
    await renderPanel();
    await act(async () => fireEvent.click(screen.getByTitle('Delete')));
    expect(mddbClient.deleteWebhook).toHaveBeenCalledWith('w1');
    expect(mddbClient.listWebhooks).toHaveBeenCalledTimes(2);
    expect(screen.getByText('No webhooks registered yet.')).toBeTruthy();
  });

  test('a refused delete shows the error', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    mddbClient.listWebhooks.mockResolvedValue([hook]);
    mddbClient.deleteWebhook.mockRejectedValue(new Error('not found'));
    await renderPanel();
    await act(async () => fireEvent.click(screen.getByTitle('Delete')));
    expect(screen.getByText('not found')).toBeTruthy();
  });
});

describe('WebhookForm', () => {
  test('requires a URL and at least one event', async () => {
    await openForm();
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(screen.getByText('URL is required')).toBeTruthy();
    fireEvent.change(screen.getByLabelText('URL'), { target: { value: '   ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(screen.getByText('URL is required')).toBeTruthy();
    fireEvent.change(screen.getByLabelText('URL'), { target: { value: 'https://h' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(screen.getByText('Select at least one event')).toBeTruthy();
    expect(mddbClient.registerWebhook).not.toHaveBeenCalled();
  });

  test('toggles events, lists sorted collections and saves, then reloads', async () => {
    useStore.setState({ collectionConfigs: { zeta: {}, alpha: {} } });
    let finish;
    mddbClient.registerWebhook.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    await openForm();
    expect(screen.getAllByRole('option').map((o) => o.textContent)).toEqual(['All collections', 'alpha', 'zeta']);

    fireEvent.change(screen.getByLabelText('URL'), { target: { value: ' https://h/x ' } });
    fireEvent.click(screen.getByLabelText('doc.added'));
    fireEvent.click(screen.getByLabelText('ops.disk_usage_high'));
    fireEvent.click(screen.getByLabelText('doc.updated'));
    fireEvent.click(screen.getByLabelText('doc.updated'));
    expect(screen.getByLabelText('doc.updated').checked).toBe(false);
    fireEvent.change(screen.getByLabelText('Collection filter'), { target: { value: 'zeta' } });

    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(screen.getByRole('button', { name: 'Saving…' }).disabled).toBe(true);
    await act(async () => finish({}));
    expect(mddbClient.registerWebhook).toHaveBeenCalledWith({
      url: 'https://h/x', events: ['doc.added', 'ops.disk_usage_high'], collection: 'zeta',
    });
    expect(screen.queryByLabelText('URL')).toBeNull();
    expect(mddbClient.listWebhooks).toHaveBeenCalledTimes(2);
  });

  test('all collections sends no collection; a refused save shows the error', async () => {
    mddbClient.registerWebhook.mockRejectedValue(new Error('bad url'));
    await openForm();
    fireEvent.change(screen.getByLabelText('URL'), { target: { value: 'https://h' } });
    fireEvent.click(screen.getByLabelText('security.auth_failure_burst'));
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Save' })));
    expect(mddbClient.registerWebhook).toHaveBeenCalledWith({
      url: 'https://h', events: ['security.auth_failure_burst'], collection: undefined,
    });
    expect(screen.getByText('bad url')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Save' }).disabled).toBe(false);
  });

  test('Cancel and the X button close the form', async () => {
    await openForm();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByLabelText('URL')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'New Webhook' }));
    const heading = screen.getByRole('heading', { name: 'New Webhook' });
    fireEvent.click(heading.parentElement.querySelector('button'));
    expect(screen.queryByLabelText('URL')).toBeNull();
  });
});
