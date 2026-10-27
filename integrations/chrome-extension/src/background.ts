import { refreshStats } from './refresh';
import { loadSettings, SETTINGS_KEY } from './storage';
import { MIN_REFRESH_SECONDS, REFRESH_ALARM_NAME } from './constants';

const badgeDeps = {
  setBadgeText: (text: string) => chrome.action.setBadgeText({ text }),
  setBadgeColor: (color: string) => chrome.action.setBadgeBackgroundColor({ color }),
  setBadgeTitle: (title: string) => chrome.action.setTitle({ title }),
};

async function reschedule(): Promise<void> {
  const settings = await loadSettings();
  await chrome.alarms.clear(REFRESH_ALARM_NAME);
  if (settings.refreshIntervalSeconds <= 0) return;
  const minutes = Math.max(MIN_REFRESH_SECONDS, settings.refreshIntervalSeconds) / 60;
  await chrome.alarms.create(REFRESH_ALARM_NAME, {
    periodInMinutes: minutes,
    delayInMinutes: minutes,
  });
}

chrome.runtime.onInstalled.addListener(async () => {
  await reschedule();
  await refreshStats(badgeDeps);
});

chrome.runtime.onStartup.addListener(async () => {
  await reschedule();
  await refreshStats(badgeDeps);
});

chrome.alarms.onAlarm.addListener(async (alarm) => {
  if (alarm.name === REFRESH_ALARM_NAME) {
    await refreshStats(badgeDeps);
  }
});

chrome.storage.onChanged.addListener(async (changes, area) => {
  if (area !== 'local') return;
  if (changes[SETTINGS_KEY]) {
    await reschedule();
    await refreshStats(badgeDeps);
  }
});

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  // INT-008: only accept messages from this extension's own trusted surfaces
  // (popup / options / internal pages). Reject a different sender.id, and reject
  // messages carrying sender.tab — those come from a content script, so a web
  // page could otherwise coax a refresh (forced traffic to the configured MDDB
  // server with its auth header).
  const isInternal = sender.id === chrome.runtime.id && !sender.tab;
  if (!isInternal) {
    return false;
  }
  if (message && message.type === 'mddb:refresh') {
    refreshStats(badgeDeps).then(
      (status) => sendResponse({ status }),
      () => sendResponse({ status: null }), // the popup shows "unknown" rather than waiting forever
    );
    return true;
  }
  return false;
});
