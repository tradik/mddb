import { listeners } from './setup';

describe('background service worker wiring', () => {
  beforeEach(() => {
    jest.resetModules();
  });

  it('registers lifecycle and message listeners', async () => {
    await import('../src/background');
    expect(listeners.installed.length).toBe(1);
    expect(listeners.startup.length).toBe(1);
    expect(listeners.alarms.length).toBe(1);
    expect(listeners.storageChanged.length).toBe(1);
    expect(listeners.runtimeMessage.length).toBe(1);
  });

  it('reschedules on install and refreshes the badge', async () => {
    await chrome.storage.local.set({
      settings: {
        serverUrl: '',
        apiKey: '',
        panelUrl: '',
        refreshIntervalSeconds: 60,
      },
    });
    await import('../src/background');
    await Promise.all(listeners.installed.map((l) => l()));
    expect(chrome.action.setBadgeText).toHaveBeenCalled();
  });

  it('does not schedule an alarm when refresh is 0', async () => {
    await chrome.storage.local.set({
      settings: { serverUrl: '', apiKey: '', panelUrl: '', refreshIntervalSeconds: 0 },
    });
    await import('../src/background');
    await Promise.all(listeners.startup.map((l) => l()));
    expect(chrome.alarms.create).not.toHaveBeenCalled();
  });

  it('only refreshes for the mddb alarm', async () => {
    await import('../src/background');
    (chrome.action.setBadgeText as jest.Mock).mockClear();
    await Promise.all(listeners.alarms.map((l) => l({ name: 'other' })));
    expect(chrome.action.setBadgeText).not.toHaveBeenCalled();
    await Promise.all(listeners.alarms.map((l) => l({ name: 'mddb-refresh' })));
    expect(chrome.action.setBadgeText).toHaveBeenCalled();
  });

  it('refreshes on settings change', async () => {
    await import('../src/background');
    (chrome.action.setBadgeText as jest.Mock).mockClear();
    await Promise.all(
      listeners.storageChanged.map((l) => l({ settings: { newValue: {} } }, 'local')),
    );
    expect(chrome.action.setBadgeText).toHaveBeenCalled();
  });

  it('ignores storage changes for unrelated keys', async () => {
    await import('../src/background');
    (chrome.action.setBadgeText as jest.Mock).mockClear();
    await Promise.all(listeners.storageChanged.map((l) => l({ other: { newValue: {} } }, 'local')));
    expect(chrome.action.setBadgeText).not.toHaveBeenCalled();
  });

  it('ignores storage changes outside local area', async () => {
    await import('../src/background');
    (chrome.action.setBadgeText as jest.Mock).mockClear();
    await Promise.all(
      listeners.storageChanged.map((l) => l({ settings: { newValue: {} } }, 'sync')),
    );
    expect(chrome.action.setBadgeText).not.toHaveBeenCalled();
  });

  it('handles refresh messages from an internal sender (popup)', async () => {
    await import('../src/background');
    const sendResponse = jest.fn();
    const sender = { id: chrome.runtime.id }; // no tab => popup/internal page
    const result = listeners.runtimeMessage[0]({ type: 'mddb:refresh' }, sender, sendResponse);
    expect(result).toBe(true);
    await new Promise((r) => setTimeout(r, 0));
    expect(sendResponse).toHaveBeenCalled();
  });

  it('answers a refresh message even when the refresh fails', async () => {
    jest.doMock('../src/refresh', () => ({
      refreshStats: () => Promise.reject(new Error('storage unavailable')),
    }));
    await import('../src/background');
    const sendResponse = jest.fn();
    const sender = { id: chrome.runtime.id };
    listeners.runtimeMessage[0]({ type: 'mddb:refresh' }, sender, sendResponse);
    await new Promise((r) => setTimeout(r, 0));
    expect(sendResponse).toHaveBeenCalledWith({ status: null });
    jest.dontMock('../src/refresh');
  });

  it('returns false for unknown messages', async () => {
    await import('../src/background');
    const sendResponse = jest.fn();
    const sender = { id: chrome.runtime.id };
    const result = listeners.runtimeMessage[0]({ type: 'other' }, sender, sendResponse);
    expect(result).toBe(false);
  });

  it('INT-008: rejects refresh from a foreign extension id', async () => {
    await import('../src/background');
    (chrome.action.setBadgeText as jest.Mock).mockClear();
    const sendResponse = jest.fn();
    const result = listeners.runtimeMessage[0](
      { type: 'mddb:refresh' },
      { id: 'some-other-extension' },
      sendResponse,
    );
    expect(result).toBe(false);
    expect(sendResponse).not.toHaveBeenCalled();
  });

  it('INT-008: rejects refresh that carries a sender.tab (content script)', async () => {
    await import('../src/background');
    const sendResponse = jest.fn();
    const result = listeners.runtimeMessage[0](
      { type: 'mddb:refresh' },
      { id: chrome.runtime.id, tab: { id: 7 } },
      sendResponse,
    );
    expect(result).toBe(false);
    expect(sendResponse).not.toHaveBeenCalled();
  });
});
