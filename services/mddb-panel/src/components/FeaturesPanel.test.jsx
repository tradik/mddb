// @vitest-environment jsdom
// FeaturesPanel + FeatureRow (GO-049): one switch per module, the toggle,
// a locked module, a blocked dependant, the 409/403 messages and a list that
// cannot be loaded.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: { listFeatures: vi.fn(), setFeature: vi.fn() },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { resetFeatures } = await import('../lib/features');
const { default: FeaturesPanel } = await import('./FeaturesPanel');

const geo = {
  name: 'geo', description: 'Geo search over document coordinates.', enabled: true, configured: true,
  default: true, requires: [], lockedBy: '', blockedBy: [], updatedAt: '2026-11-11T10:00:00Z', updatedBy: 'admin',
};
const chat = { name: 'chat', enabled: false, configured: false, default: true, requires: [], blockedBy: [] };
const chatExternalMcp = {
  name: 'chatExternalMcp', enabled: false, configured: true, default: false, requires: ['chat'], blockedBy: ['chat'], updatedBy: 'ops',
};
const temporal = { name: 'temporal', enabled: false, configured: true, default: true, lockedBy: 'MDDB_FEATURES_FORCE_OFF' };
const images = { name: 'images', enabled: true, default: false, updatedAt: '2026-11-11T10:00:00Z' };

async function renderWith(features = [geo, chat, chatExternalMcp, temporal, images], version = 3) {
  vi.mocked(mddbClient.listFeatures).mockResolvedValue({ features, version });
  render(<FeaturesPanel />);
  await screen.findByText(`version ${version}`);
  await screen.findByRole('list', { name: 'Modules' });
}

const toggle = (name) => screen.getByRole('switch', { name });

beforeEach(() => {
  resetFeatures();
  vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(cleanup);

describe('the list', () => {
  test('one switch per module with its state, default, requirements and history', async () => {
    await renderWith();
    expect(toggle('Geo search').checked).toBe(true);
    expect(toggle('Chat').checked).toBe(false);
    expect(screen.getByText('Geo search over document coordinates.')).toBeTruthy();
    expect(screen.getByText(/Changed 2026-11-11 \d\d:\d\d by admin/)).toBeTruthy();
    expect(screen.getByText('Changed by ops')).toBeTruthy();
    expect(screen.getByText(/^Changed 2026-11-11 \d\d:\d\d$/)).toBeTruthy();
    expect(screen.getByText(/requires chat/)).toBeTruthy();
    expect(screen.getAllByText('Running')).toHaveLength(2);
    // images has no `configured`: the switch follows `enabled`.
    expect(toggle('Image documents').checked).toBe(true);
  });

  test('a blocked dependant keeps its stored setting and says what it waits for', async () => {
    await renderWith();
    expect(toggle('Chat: external MCP servers').checked).toBe(true);
    expect(screen.getByText('Off until Chat is turned on.')).toBeTruthy();
  });

  test('a locked module cannot be switched and names the lock', async () => {
    await renderWith();
    const locked = toggle('Temporal analytics');
    expect(locked.disabled).toBe(true);
    expect(locked.checked).toBe(false);
    expect(screen.getByText(/Forced off by MDDB_FEATURES_FORCE_OFF/)).toBeTruthy();
  });

  test('shows a loading line until the list arrives', async () => {
    vi.mocked(mddbClient.listFeatures).mockReturnValue(new Promise(() => {}));
    render(<FeaturesPanel />);
    expect(screen.getByText('Loading modules…')).toBeTruthy();
  });
});

describe('switching', () => {
  test('a toggle calls setFeature, reports it and reloads the list', async () => {
    await renderWith();
    vi.mocked(mddbClient.setFeature).mockResolvedValue({ feature: { ...geo, enabled: false }, version: 4 });
    vi.mocked(mddbClient.listFeatures).mockResolvedValue({ features: [{ ...geo, enabled: false, configured: false }], version: 4 });
    fireEvent.click(toggle('Geo search'));
    expect(mddbClient.setFeature).toHaveBeenCalledWith('geo', false);
    expect((await screen.findByRole('status')).textContent).toBe('Geo search turned off.');
    await screen.findByText('version 4');
    expect(toggle('Geo search').checked).toBe(false);
  });

  test('turning a module on says so', async () => {
    await renderWith();
    vi.mocked(mddbClient.setFeature).mockResolvedValue({});
    fireEvent.click(toggle('Chat'));
    expect(mddbClient.setFeature).toHaveBeenCalledWith('chat', true);
    expect((await screen.findByRole('status')).textContent).toBe('Chat turned on.');
  });

  test('a 409 names the lock', async () => {
    await renderWith();
    vi.mocked(mddbClient.setFeature).mockRejectedValue(Object.assign(new Error('API Error (409): feature locked'), {
      status: 409, body: { error: 'feature locked', feature: 'geo', lockedBy: 'MDDB_FEATURES_FORCE_OFF' },
    }));
    fireEvent.click(toggle('Geo search'));
    expect((await screen.findByRole('alert')).textContent).toBe(
      "Geo search is locked: forced off by MDDB_FEATURES_FORCE_OFF. Change the server's environment and restart it to turn it on.",
    );
    expect(toggle('Geo search').disabled).toBe(false);
  });

  test('a 409 without a body still explains itself', async () => {
    await renderWith();
    vi.mocked(mddbClient.setFeature).mockRejectedValue(Object.assign(new Error('x'), { status: 409 }));
    fireEvent.click(toggle('Geo search'));
    expect((await screen.findByRole('alert')).textContent).toMatch(/forced off by the operator/);
  });

  test('a 403 is shown as the server sent it', async () => {
    await renderWith();
    vi.mocked(mddbClient.setFeature).mockRejectedValue(Object.assign(new Error('API Error (403): forbidden'), { status: 403 }));
    fireEvent.click(toggle('Geo search'));
    expect((await screen.findByRole('alert')).textContent).toBe('API Error (403): forbidden');
  });
});

describe('a list that cannot be loaded', () => {
  test('an older server without the endpoint', async () => {
    vi.mocked(mddbClient.listFeatures).mockRejectedValue(Object.assign(new Error('API Error (404): not found'), { status: 404 }));
    render(<FeaturesPanel />);
    expect((await screen.findByRole('alert')).textContent).toMatch(/does not report module switches/);
  });

  test('any other failure, then Reload', async () => {
    vi.mocked(mddbClient.listFeatures).mockRejectedValue(new Error('network down'));
    render(<FeaturesPanel />);
    expect((await screen.findByRole('alert')).textContent).toBe(
      'Could not load the module list: network down. Until it loads, every module is treated as on.',
    );
    vi.mocked(mddbClient.listFeatures).mockResolvedValue({ features: [geo], version: 5 });
    fireEvent.click(screen.getByRole('button', { name: 'Reload' }));
    await screen.findByText('version 5');
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
    expect(toggle('Geo search')).toBeTruthy();
  });
});
