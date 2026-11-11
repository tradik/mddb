// @vitest-environment jsdom
// FeaturesPanel (2.19.0): categories, and "Turn on all" / "Turn off all" for
// every module or one category — the confirmation, the order, a locked module
// skipped, a failure reported while the rest still run.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: { listFeatures: vi.fn(), setFeature: vi.fn() },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { resetFeatures } = await import('../lib/features');
const { default: FeaturesPanel } = await import('./FeaturesPanel');

const mod = (name, on, extra = {}) => ({ name, enabled: on, configured: on, default: true, requires: [], ...extra });
const list = [
  mod('geo', true),
  mod('crossSearch', false),
  mod('chat', false),
  mod('chatExternalMcp', false, { requires: ['chat'], default: false }),
  mod('temporal', false, { lockedBy: 'MDDB_FEATURES_FORCE_OFF' }),
  mod('webhooks', true),
];

async function renderPanel(features = list) {
  vi.mocked(mddbClient.listFeatures).mockResolvedValue({ features, version: 7 });
  render(<FeaturesPanel />);
  await screen.findByText('version 7');
  await screen.findByRole('region', { name: 'Modules' });
}

/** Clicks a bulk button, then the confirmation's button. */
async function bulk(name, confirmLabel) {
  fireEvent.click(screen.getByRole('button', { name }));
  const dialog = await screen.findByRole('alertdialog');
  fireEvent.click(within(dialog).getByRole('button', { name: confirmLabel }));
  return dialog;
}

beforeEach(() => {
  resetFeatures();
  vi.mocked(mddbClient.setFeature).mockReset().mockResolvedValue({ version: 8 });
});
afterEach(cleanup);

describe('categories', () => {
  test('modules sit under their category headings, each with its own list', async () => {
    await renderPanel();
    expect(screen.getAllByRole('heading', { level: 3 }).map((h) => h.textContent)).toEqual([
      'Search', 'Data', 'Chat', 'Integrations',
    ]);
    const chatList = screen.getByRole('list', { name: 'Chat modules' });
    expect(within(chatList).getAllByRole('switch')).toHaveLength(2);
  });
});

describe('turn on all / turn off all', () => {
  test('turn on all asks first, names what changes and the locked module, then switches in dependency order', async () => {
    await renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'Turn on all' }));
    const dialog = await screen.findByRole('alertdialog');
    expect(dialog.textContent).toContain('Cross search, Chat, Chat: external MCP servers will be turned on.');
    expect(dialog.textContent).toContain('Skipped, forced off by the operator: Temporal analytics.');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Turn on' }));
    await screen.findByText('3 modules turned on.');
    expect(vi.mocked(mddbClient.setFeature).mock.calls).toEqual([
      ['crossSearch', true], ['chat', true], ['chatExternalMcp', true],
    ]);
    expect(mddbClient.listFeatures).toHaveBeenCalledTimes(2);
  });

  test('turn off all in one category touches only that category and says stored data stays', async () => {
    await renderPanel();
    const dialog = await bulk('Turn off all in Search', 'Turn off');
    expect(dialog.textContent).toContain('Geo search will be turned off.');
    expect(dialog.textContent).toContain('Stored data stays.');
    await screen.findByText('1 module turned off.');
    expect(vi.mocked(mddbClient.setFeature).mock.calls).toEqual([['geo', false]]);
  });

  test('a failed module is reported and the rest still run', async () => {
    vi.mocked(mddbClient.setFeature).mockImplementation(async (name) => {
      if (name === 'geo') throw Object.assign(new Error('Forbidden'), { status: 403 });
      return {};
    });
    await renderPanel();
    await bulk('Turn off all', 'Turn off');
    await screen.findByText('1 module turned off.');
    expect(screen.getByRole('alert').textContent).toBe('Forbidden');
    expect(vi.mocked(mddbClient.setFeature).mock.calls.map(([name]) => name)).toEqual(['webhooks', 'geo']);
  });

  test('cancel changes nothing; a category with nothing to change has its button disabled', async () => {
    await renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'Turn on all' }));
    const dialog = await screen.findByRole('alertdialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    expect(mddbClient.setFeature).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Turn on all in Data' }).disabled).toBe(true);
    expect(screen.getByRole('button', { name: 'Turn off all in Data' }).disabled).toBe(true);
  });
});
