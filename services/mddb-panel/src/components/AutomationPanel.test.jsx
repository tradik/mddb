// @vitest-environment jsdom
// AutomationPanel: the automation rules page — the rules table and its type
// filter tabs, the add form for webhooks, triggers and cron rules (webhook,
// backup and sync actions), inline edit, enable/disable, delete with confirm,
// test trigger results, errors, Refresh and the Logs tab (hidden when the
// server turns automation logs off).
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, within, waitFor, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    listAutomation: vi.fn(),
    createAutomation: vi.fn(),
    updateAutomation: vi.fn(),
    deleteAutomation: vi.fn(),
    testAutomation: vi.fn(),
    listAutomationLogs: vi.fn(),
  },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: AutomationPanel } = await import('./AutomationPanel');

const initialStore = useStore.getState();
const client = vi.mocked(mddbClient);
const MIB = 1024 * 1024;

const slack = { id: 'w1', type: 'webhook', name: 'Slack', url: 'https://hooks/slack', enabled: true };
const ping = { id: 'w2', type: 'webhook', name: 'Ping', url: 'https://ping', method: 'GET', enabled: false };
const news = {
  id: 't1', type: 'trigger', name: 'News', collection: 'news', query: 'breaking', sentimentEnabled: true,
  sentimentMin: -0.5, sentimentMax: 0.5, conditionLogic: 'or', events: ['insert', 'delete'], enabled: true,
};
const bare = { id: 't2', type: 'trigger', name: 'Bare', query: 'q', sentimentEnabled: true, events: [], enabled: false };
const nightly = { id: 'c1', type: 'cron', name: 'Nightly', schedule: '0 0 3 * * *', backup: { target: 'local', keep: 7 }, enabled: true };
const mirror = {
  id: 'c2', type: 'cron', name: 'Mirror', schedule: '0 0 * * * *', enabled: true,
  sync: {
    source: 's3', collection: 'docs', lang: 'en', bucket: 'bk', prefix: 'docs/', maxBytes: 5 * MIB,
    storageConfig: { endpoint: 'minio', secretKey: '', secretKeySet: true, useTLS: true },
  },
};
const odd = { id: 'x1', type: 'mystery', name: 'Odd', enabled: true };
const rules = [slack, ping, news, bare, nightly, mirror, odd];

/** listAutomation as the server answers it: filtered by type when given one. */
const serve = (all) => async (type) => (type ? all.filter((r) => r.type === type) : all);

/** The table row of the rule called `name`. */
const rowOf = (name) => screen.getByText(name, { selector: 'div' }).closest('tr');

/** Renders the panel and waits for the rules table. */
async function renderLoaded() {
  const view = render(<AutomationPanel />);
  await screen.findByText('Rules (7)');
  await screen.findByText('All (7)');
  return view;
}

/** Opens the add form and picks a rule type and name. */
function openAddForm(type, name) {
  fireEvent.click(screen.getByRole('button', { name: 'Add Rule' }));
  fireEvent.change(screen.getByDisplayValue('Webhook'), { target: { value: type } });
  fireEvent.change(screen.getByPlaceholderText('My automation rule'), { target: { value: name } });
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  for (const fn of Object.values(client)) fn.mockReset();
  client.listAutomation.mockImplementation(serve(rules));
  client.createAutomation.mockResolvedValue({});
  client.updateAutomation.mockResolvedValue({});
  client.deleteAutomation.mockResolvedValue({});
  client.listAutomationLogs.mockResolvedValue({ logs: [] });
});
afterEach(cleanup);

describe('AutomationPanel: rules table', () => {
  test('shows the spinner while rules load', () => {
    client.listAutomation.mockReturnValue(new Promise(() => {}));
    render(<AutomationPanel />);
    expect(screen.getByText('Loading automation rules...')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Refresh' }).disabled).toBe(true);
  });

  test('lists every rule with its summary, badge, status and counts per type', async () => {
    const { container } = await renderLoaded();
    expect(client.listAutomation).toHaveBeenCalledWith(undefined);
    expect(client.listAutomation).toHaveBeenCalledWith();
    for (const tab of ['All (7)', 'Webhooks (2)', 'Triggers (2)', 'Crons (2)']) {
      expect(screen.getByRole('button', { name: tab })).toBeTruthy();
    }
    expect(screen.getByRole('button', { name: 'All (7)' }).className).toContain('bg-blue-100');

    expect(rowOf('Slack').textContent).toContain('POST https://hooks/slack');
    expect(rowOf('Ping').textContent).toContain('GET https://ping');
    expect(within(rowOf('News')).getByText('news')).toBeTruthy();
    expect(within(rowOf('News')).getByText('"breaking"')).toBeTruthy();
    expect(within(rowOf('News')).getByText('sentiment [-0.5, 0.5]')).toBeTruthy();
    expect(within(rowOf('News')).getByText('or')).toBeTruthy();
    expect(within(rowOf('News')).getByText('ON INSERT, DELETE')).toBeTruthy();
    expect(within(rowOf('Bare')).getByText('sentiment [, ]')).toBeTruthy();
    expect(within(rowOf('Bare')).getByText('and')).toBeTruthy();
    expect(rowOf('Bare').textContent).not.toContain('ON ');
    expect(within(rowOf('Nightly')).getByText('0 0 3 * * *')).toBeTruthy();
    expect(within(rowOf('Nightly')).getByText('backup → MDDB_BACKUP_DIR, keep 7')).toBeTruthy();
    expect(within(rowOf('Mirror')).getByText('sync s3 s3://bk/docs/ → docs')).toBeTruthy();

    expect(within(rowOf('Slack')).getByText('Webhook').className).toContain('bg-blue-100');
    expect(within(rowOf('News')).getByText('Trigger').className).toContain('bg-amber-100');
    expect(within(rowOf('Nightly')).getByText('Cron').className).toContain('bg-purple-100');
    // An unknown type gets neither icon nor badge.
    expect(rowOf('Odd').querySelector('td').innerHTML).toBe('');
    expect(rowOf('Odd').querySelectorAll('td')[2].innerHTML).toBe('');

    expect(within(rowOf('Slack')).getByTitle('Click to disable').textContent).toBe('Enabled');
    expect(within(rowOf('Ping')).getByTitle('Click to enable').textContent).toBe('Disabled');
    expect(within(rowOf('News')).getByTitle('Test trigger')).toBeTruthy();
    expect(within(rowOf('Slack')).queryByTitle('Test trigger')).toBeNull();
    expect(container.querySelectorAll('tbody tr')).toHaveLength(7);
  });

  test.each([
    ['Webhooks (2)', 'webhook', ['Slack', 'Ping']],
    ['Triggers (2)', 'trigger', ['News', 'Bare']],
    ['Crons (2)', 'cron', ['Nightly', 'Mirror']],
  ])('the %s tab loads only that type', async (tab, type, names) => {
    const { container } = await renderLoaded();
    fireEvent.click(screen.getByRole('button', { name: tab }));
    await screen.findByText('Rules (2)');
    expect(client.listAutomation).toHaveBeenLastCalledWith(type);
    const shown = [...container.querySelectorAll('tbody tr td:nth-child(2) > div:first-child')].map((d) => d.textContent);
    expect(shown).toEqual(names);
    expect(screen.getByRole('button', { name: tab }).className).toContain('bg-blue-100');
    expect(screen.getByRole('button', { name: 'All (7)' }).className).not.toContain('bg-blue-100');
  });

  test.each([
    ['an object with rules', { rules: [slack] }, 'Rules (1)', 'All (1)'],
    ['an object without rules', {}, 'No automation rules found.', 'All (0)'],
  ])('reads a reply that is %s', async (_label, reply, text, tab) => {
    client.listAutomation.mockResolvedValue(reply);
    render(<AutomationPanel />);
    expect(await screen.findByText(text)).toBeTruthy();
    expect(await screen.findByRole('button', { name: tab })).toBeTruthy();
  });

  test('a failed load shows the error and logs both failures', async () => {
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {});
    client.listAutomation.mockRejectedValue(new Error('forbidden'));
    render(<AutomationPanel />);
    expect(await screen.findByText('forbidden')).toBeTruthy();
    await waitFor(() => expect(errors).toHaveBeenCalledTimes(2));
    expect(errors.mock.calls.map((c) => c[0])).toEqual(
      expect.arrayContaining(['Failed to load automation rules:', 'Failed to load dropdown data:']),
    );
    expect(screen.getByText('No automation rules found.')).toBeTruthy();
  });

  test('Refresh reloads the rules', async () => {
    await renderLoaded();
    client.listAutomation.mockResolvedValue([slack]);
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    expect(await screen.findByText('Rules (1)')).toBeTruthy();
  });
});

describe('AutomationPanel: add form', () => {
  test('Create stays disabled and does nothing until the rule has a name', async () => {
    const { container } = await renderLoaded();
    fireEvent.click(screen.getByRole('button', { name: 'Add Rule' }));
    expect(screen.getByText('Add Automation Rule')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Create' }).disabled).toBe(true);
    fireEvent.change(screen.getByPlaceholderText('My automation rule'), { target: { value: '   ' } });
    fireEvent.submit(container.querySelector('form'));
    expect(client.createAutomation).not.toHaveBeenCalled();
  });

  test('creates a webhook with method, headers and enabled off', async () => {
    await renderLoaded();
    openAddForm('webhook', 'Hook');
    expect(screen.getByText('Template Variables')).toBeTruthy();
    fireEvent.change(screen.getByPlaceholderText('https://example.com/hook'), { target: { value: 'https://h/{{doc.id}}' } });
    fireEvent.change(screen.getByDisplayValue('POST'), { target: { value: 'PUT' } });
    fireEvent.change(screen.getByPlaceholderText('{"Content-Type": "application/json"}'), { target: { value: '{"A":"1"}' } });
    fireEvent.click(screen.getByRole('checkbox', { name: 'Enabled' }));
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(screen.queryByText('Add Automation Rule')).toBeNull());
    expect(client.createAutomation).toHaveBeenCalledWith({
      type: 'webhook', name: 'Hook', enabled: false, url: 'https://h/{{doc.id}}', method: 'PUT', headers: { A: '1' },
    });
    await waitFor(() => expect(client.listAutomation).toHaveBeenCalledTimes(4));
  });

  test('creates a trigger with events, condition, sentiment and a webhook', async () => {
    useStore.setState({ stats: { collections: [{ name: 'docs' }, { name: 'news' }] } });
    const { container } = await renderLoaded();
    openAddForm('trigger', 'Breaking');
    expect(screen.getByRole('option', { name: 'Slack (POST https://hooks/slack)' })).toBeTruthy();
    expect(screen.getByRole('option', { name: 'Ping (GET https://ping)' })).toBeTruthy();

    fireEvent.click(screen.getByRole('checkbox', { name: 'INSERT (new doc)' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'DELETE (doc removed)' }));
    fireEvent.change(screen.getByDisplayValue('Select collection'), { target: { value: 'news' } });
    fireEvent.change(screen.getByDisplayValue('Full-Text Search'), { target: { value: 'hybrid' } });
    fireEvent.change(screen.getByPlaceholderText('e.g. breaking news'), { target: { value: 'quake' } });
    fireEvent.change(container.querySelector('input[type="range"]'), { target: { value: '70' } });
    expect(screen.getByText(/Score ≥ 70%/)).toBeTruthy();
    expect(screen.queryByText('Combine conditions:')).toBeNull();

    fireEvent.click(screen.getByRole('checkbox', { name: 'Sentiment condition' }));
    const [, min, max] = container.querySelectorAll('input[type="range"]');
    fireEvent.change(min, { target: { value: '-30' } });
    fireEvent.change(max, { target: { value: '80' } });
    expect(screen.getByText(/Min score: -0.3/)).toBeTruthy();
    expect(screen.getByText(/Max score: 0.8/)).toBeTruthy();
    expect(screen.getByText('Combine conditions:')).toBeTruthy();
    fireEvent.click(screen.getByRole('radio', { name: 'OR (either must match)' }));
    fireEvent.click(screen.getByRole('radio', { name: 'AND (both must match)' }));
    expect(screen.getByRole('radio', { name: 'AND (both must match)' }).checked).toBe(true);
    fireEvent.click(screen.getByRole('radio', { name: 'OR (either must match)' }));
    fireEvent.change(screen.getByDisplayValue('Select webhook'), { target: { value: 'w1' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(client.createAutomation).toHaveBeenCalledTimes(1));
    expect(client.createAutomation).toHaveBeenCalledWith({
      type: 'trigger', name: 'Breaking', enabled: true, collection: 'news', searchType: 'hybrid', query: 'quake',
      threshold: 70, events: ['update', 'delete'], webhookId: 'w1', sentimentEnabled: true,
      sentimentMin: -0.3, sentimentMax: 0.8, conditionLogic: 'or',
    });
    await waitFor(() => expect(screen.queryByText('Add Automation Rule')).toBeNull());
  });

  test('creates a cron rule that backs up to S3', async () => {
    await renderLoaded();
    openAddForm('cron', 'Offsite');
    fireEvent.change(screen.getByLabelText(/^Schedule/), { target: { value: '0 0 3 * * *' } });
    fireEvent.change(screen.getByLabelText('Action'), { target: { value: 'backup' } });
    fireEvent.change(screen.getByLabelText('Backup target'), { target: { value: 's3' } });
    fireEvent.change(screen.getByLabelText('Bucket'), { target: { value: 'bk' } });
    fireEvent.change(screen.getByLabelText(/^Keep newest/), { target: { value: '5' } });
    fireEvent.change(screen.getByLabelText(/^Secret key/), { target: { value: 'sk' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(client.createAutomation).toHaveBeenCalledTimes(1));
    expect(client.createAutomation).toHaveBeenCalledWith({
      type: 'cron', name: 'Offsite', enabled: true, schedule: '0 0 3 * * *',
      backup: { target: 's3', bucket: 'bk', keep: 5, collections: ['*'], storageConfig: { secretKey: 'sk', useTLS: true } },
    });
  });

  test('creates a cron rule that syncs a URL list into a collection', async () => {
    useStore.setState({ stats: { collections: [{ name: 'docs' }] } });
    await renderLoaded();
    openAddForm('cron', 'Pages');
    fireEvent.change(screen.getByLabelText(/^Schedule/), { target: { value: '0 */30 * * * *' } });
    fireEvent.change(screen.getByLabelText('Action'), { target: { value: 'sync' } });
    fireEvent.change(screen.getByLabelText('Source'), { target: { value: 'urls' } });
    fireEvent.change(screen.getByLabelText('Into collection'), { target: { value: 'docs' } });
    fireEvent.change(screen.getByLabelText('URLs (one per line)'), { target: { value: 'https://a\n\nhttps://b\n' } });
    fireEvent.change(screen.getByLabelText('Max MiB per document'), { target: { value: '2' } });
    fireEvent.click(screen.getByLabelText(/^Prune/));
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(client.createAutomation).toHaveBeenCalledTimes(1));
    expect(client.createAutomation).toHaveBeenCalledWith({
      type: 'cron', name: 'Pages', enabled: true, schedule: '0 */30 * * * *',
      sync: { source: 'urls', collection: 'docs', lang: 'en', urls: ['https://a', 'https://b'], maxBytes: 2 * MIB, prune: true },
    });
  });

  test('a failed create keeps the form open and shows the error', async () => {
    let fail;
    client.createAutomation.mockReturnValue(new Promise((_resolve, reject) => { fail = reject; }));
    await renderLoaded();
    openAddForm('cron', 'Broken');
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Create' }).disabled).toBe(true));

    await act(async () => fail(new Error('invalid cron expression')));
    expect(screen.getByText('invalid cron expression')).toBeTruthy();
    expect(screen.getByText('Add Automation Rule')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Create' }).disabled).toBe(false);
  });

  test('Cancel and Add Rule close the form; Cancel clears it', async () => {
    await renderLoaded();
    openAddForm('trigger', 'Draft');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByText('Add Automation Rule')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Add Rule' }));
    expect(screen.getByPlaceholderText('My automation rule').value).toBe('');
    expect(screen.getByDisplayValue('Webhook')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Add Rule' }));
    expect(screen.queryByText('Add Automation Rule')).toBeNull();
  });
});

describe('AutomationPanel: edit, toggle, delete, test', () => {
  test('edits a sync rule inline and keeps the stored S3 secret', async () => {
    let finish;
    client.updateAutomation.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    await renderLoaded();
    fireEvent.click(within(rowOf('Mirror')).getByTitle('Edit'));

    expect(screen.getByText('A secret is stored. Leave blank to keep it.')).toBeTruthy();
    expect(screen.getByDisplayValue('Cron')).toBeTruthy();
    fireEvent.change(screen.getByDisplayValue('Mirror'), { target: { value: 'Mirror 2' } });
    fireEvent.click(screen.getByRole('checkbox', { name: 'Enabled' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Enabled' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save' }).disabled).toBe(true));

    expect(client.updateAutomation).toHaveBeenCalledWith('c2', {
      type: 'cron', name: 'Mirror 2', enabled: true, schedule: '0 0 * * * *',
      sync: {
        source: 's3', collection: 'docs', lang: 'en', bucket: 'bk', prefix: 'docs/', maxBytes: 5 * MIB,
        storageConfig: { endpoint: 'minio', useTLS: true },
      },
    });
    await act(async () => finish({}));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Save' })).toBeNull());
    expect(rowOf('Mirror')).toBeTruthy();
  });

  test('changing the type in the edit row starts that type from scratch', async () => {
    await renderLoaded();
    fireEvent.click(within(rowOf('Nightly')).getByTitle('Edit'));
    fireEvent.change(screen.getByDisplayValue('Cron'), { target: { value: 'webhook' } });
    fireEvent.change(screen.getByPlaceholderText('https://example.com/hook'), { target: { value: 'https://new' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(client.updateAutomation).toHaveBeenCalledTimes(1));
    expect(client.updateAutomation).toHaveBeenCalledWith('c1', {
      type: 'webhook', name: 'Nightly', enabled: true, url: 'https://new', method: 'POST',
    });
  });

  test('Cancel leaves the edit row without saving', async () => {
    await renderLoaded();
    fireEvent.click(within(rowOf('Slack')).getByTitle('Edit'));
    expect(screen.getByDisplayValue('https://hooks/slack')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull();
    expect(rowOf('Slack')).toBeTruthy();
    expect(client.updateAutomation).not.toHaveBeenCalled();
  });

  test('a failed save keeps the edit row and shows the error', async () => {
    client.updateAutomation.mockRejectedValue(new Error('rule name taken'));
    await renderLoaded();
    fireEvent.click(within(rowOf('News')).getByTitle('Edit'));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('rule name taken')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Save' })).toBeTruthy();
    expect(client.updateAutomation.mock.calls[0][1]).toMatchObject({ type: 'trigger', name: 'News', conditionLogic: 'or' });
  });

  test('the status button flips enabled and reloads', async () => {
    let finish;
    client.updateAutomation.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    await renderLoaded();
    fireEvent.click(within(rowOf('Slack')).getByTitle('Click to disable'));
    await waitFor(() => expect(within(rowOf('Slack')).getByTitle('Click to disable').disabled).toBe(true));
    expect(client.updateAutomation).toHaveBeenCalledWith('w1', { ...slack, enabled: false });

    await act(async () => finish({}));
    await waitFor(() => expect(within(rowOf('Slack')).getByTitle('Click to disable').disabled).toBe(false));
    expect(client.listAutomation).toHaveBeenCalledTimes(3);
  });

  test('a failed toggle shows the error', async () => {
    client.updateAutomation.mockRejectedValue(new Error('read-only'));
    await renderLoaded();
    fireEvent.click(within(rowOf('Ping')).getByTitle('Click to enable'));
    expect(await screen.findByText('read-only')).toBeTruthy();
    expect(client.updateAutomation).toHaveBeenCalledWith('w2', { ...ping, enabled: true });
  });

  test('Delete asks first and does nothing when declined', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    await renderLoaded();
    fireEvent.click(within(rowOf('Odd')).getByTitle('Delete'));
    expect(confirm).toHaveBeenCalledWith('Delete automation rule "Odd"?');
    expect(client.deleteAutomation).not.toHaveBeenCalled();
  });

  test('Delete removes the rule once confirmed and reloads', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    let finish;
    client.deleteAutomation.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    await renderLoaded();
    fireEvent.click(within(rowOf('Odd')).getByTitle('Delete'));
    await waitFor(() => expect(within(rowOf('Odd')).getByTitle('Delete').disabled).toBe(true));
    expect(client.deleteAutomation).toHaveBeenCalledWith('x1');

    client.listAutomation.mockImplementation(serve(rules.filter((r) => r !== odd)));
    await act(async () => finish({}));
    expect(await screen.findByText('Rules (6)')).toBeTruthy();
    expect(await screen.findByRole('button', { name: 'All (6)' })).toBeTruthy();
  });

  test('a failed delete shows the error', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    client.deleteAutomation.mockRejectedValue(new Error('not found'));
    await renderLoaded();
    fireEvent.click(within(rowOf('Slack')).getByTitle('Delete'));
    expect(await screen.findByText('not found')).toBeTruthy();
    expect(within(rowOf('Slack')).getByTitle('Delete').disabled).toBe(false);
  });

  test('Test trigger shows the result until dismissed', async () => {
    let finish;
    client.testAutomation.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    await renderLoaded();
    fireEvent.click(within(rowOf('News')).getByTitle('Test trigger'));
    await waitFor(() => expect(within(rowOf('News')).getByTitle('Test trigger').disabled).toBe(true));
    expect(client.testAutomation).toHaveBeenCalledWith('t1');

    await act(async () => finish({ matched: 2, fired: true }));
    expect(screen.getByText('Test Results')).toBeTruthy();
    expect(screen.getByText(/"matched": 2/).textContent).toBe(JSON.stringify({ matched: 2, fired: true }, null, 2));
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByText('Test Results')).toBeNull();
  });

  test('a failed test shows the error and no results', async () => {
    client.testAutomation.mockRejectedValue(new Error('no webhook'));
    await renderLoaded();
    fireEvent.click(within(rowOf('Bare')).getByTitle('Test trigger'));
    expect(await screen.findByText('no webhook')).toBeTruthy();
    expect(screen.queryByText('Test Results')).toBeNull();
  });
});

describe('AutomationPanel: logs tab', () => {
  test.each([
    ['no server config yet', null],
    ['logs enabled', { automationLogsEnabled: true }],
  ])('with %s the Logs tab opens the run log', async (_label, config) => {
    useStore.setState({ config });
    await renderLoaded();
    fireEvent.click(screen.getByRole('button', { name: 'Logs' }));
    expect(await screen.findByText('No automation logs found.')).toBeTruthy();
    expect(screen.queryByText('Rules (7)')).toBeNull();
    expect(screen.getByRole('button', { name: 'Logs' }).className).toContain('border-blue-600');

    fireEvent.click(screen.getByRole('button', { name: 'Rules' }));
    expect(screen.getByText('Rules (7)')).toBeTruthy();
    expect(screen.queryByText('No automation logs found.')).toBeNull();
  });

  test('hides the Logs tab when the server turns automation logs off', async () => {
    useStore.setState({ config: { automationLogsEnabled: false } });
    await renderLoaded();
    expect(screen.queryByRole('button', { name: 'Logs' })).toBeNull();
    expect(client.listAutomationLogs).not.toHaveBeenCalled();
  });
});
