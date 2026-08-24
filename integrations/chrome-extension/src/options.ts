import { getHealth, MddbApiError } from './client';
import { normalizeServerUrl } from './url';
import { DEFAULT_REFRESH_SECONDS, Settings, loadSettings, saveSettings } from './storage';
import { MAX_REFRESH_SECONDS, MIN_REFRESH_SECONDS } from './constants';

interface OptionsElements {
  form: HTMLFormElement;
  url: HTMLInputElement;
  apiKey: HTMLInputElement;
  panel: HTMLInputElement;
  refresh: HTMLInputElement;
  testButton: HTMLButtonElement;
  status: HTMLElement;
}

function $<T extends HTMLElement>(id: string): T {
  const el = document.getElementById(id) as T | null;
  if (!el) throw new Error(`Missing element #${id}`);
  return el;
}

export function getElements(): OptionsElements {
  return {
    form: $('settings-form'),
    url: $('mddb-url'),
    apiKey: $('api-key'),
    panel: $('panel-url'),
    refresh: $('refresh-interval'),
    testButton: $('test-button'),
    status: $('status-message'),
  };
}

function setStatus(el: HTMLElement, text: string, tone: 'ok' | 'err' | 'neutral'): void {
  el.textContent = text;
  el.classList.remove('status-message--ok', 'status-message--err');
  if (tone === 'ok') el.classList.add('status-message--ok');
  if (tone === 'err') el.classList.add('status-message--err');
}

export function parseRefresh(raw: string): number {
  if (!raw.trim()) return DEFAULT_REFRESH_SECONDS;
  const n = Number(raw);
  if (!Number.isFinite(n)) return DEFAULT_REFRESH_SECONDS;
  if (n <= 0) return 0;
  if (n < MIN_REFRESH_SECONDS) return MIN_REFRESH_SECONDS;
  if (n > MAX_REFRESH_SECONDS) return MAX_REFRESH_SECONDS;
  return Math.round(n);
}

export function buildSettings(els: OptionsElements): Settings {
  const serverUrl = normalizeServerUrl(els.url.value);
  const panelRaw = els.panel.value.trim();
  const panelUrl = panelRaw ? normalizeServerUrl(panelRaw) : '';
  return {
    serverUrl,
    apiKey: els.apiKey.value.trim(),
    panelUrl,
    refreshIntervalSeconds: parseRefresh(els.refresh.value),
  };
}

function originPattern(serverUrl: string): string {
  return new URL(serverUrl).origin + '/*';
}

async function requestHostPermission(serverUrl: string): Promise<boolean> {
  try {
    const granted = await chrome.permissions.request({ origins: [originPattern(serverUrl)] });
    return Boolean(granted);
  } catch {
    return false;
  }
}

/**
 * Drop the host permission for a server the extension no longer points at
 * (INT-015). Without this, every address the user ever saved stays granted,
 * because `optional_host_permissions` covers all of http/https and nothing
 * revokes the previous grant. Failure is non-fatal: the new settings are
 * already saved and the stale grant is a privacy wart, not a broken state.
 */
async function revokeStaleHostPermission(previousUrl: string, nextUrl: string): Promise<void> {
  let previous: string;
  let next: string;
  try {
    previous = originPattern(previousUrl);
    next = originPattern(nextUrl);
  } catch {
    return; // an unparseable stored URL grants nothing to revoke
  }
  if (previous === next) return;
  try {
    await chrome.permissions.remove({ origins: [previous] });
  } catch {
    /* revocation is best-effort */
  }
}

export async function init(els = getElements()): Promise<void> {
  const settings = await loadSettings();
  // Tracks the origin the extension currently holds a grant for, so a second
  // save in the same session revokes the first save's origin rather than the
  // one loaded at startup (INT-015).
  let grantedFor = settings.serverUrl;
  els.url.value = settings.serverUrl;
  els.apiKey.value = settings.apiKey;
  els.panel.value = settings.panelUrl;
  els.refresh.value = String(settings.refreshIntervalSeconds);

  els.form.addEventListener('submit', async (event) => {
    event.preventDefault();
    try {
      const next = buildSettings(els);
      const granted = await requestHostPermission(next.serverUrl);
      if (!granted) {
        setStatus(
          els.status,
          'Permission denied. The extension needs access to the server origin to query stats.',
          'err',
        );
        return;
      }
      await saveSettings(next);
      await revokeStaleHostPermission(grantedFor, next.serverUrl);
      grantedFor = next.serverUrl;
      setStatus(els.status, 'Settings saved.', 'ok');
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Failed to save settings';
      setStatus(els.status, msg, 'err');
    }
  });

  els.testButton.addEventListener('click', async () => {
    try {
      const next = buildSettings(els);
      setStatus(els.status, 'Testing…', 'neutral');
      const health = await getHealth({
        baseUrl: next.serverUrl,
        apiKey: next.apiKey || undefined,
      });
      setStatus(
        els.status,
        `OK — server status "${health.status}" (mode: ${health.mode ?? 'unknown'}).`,
        'ok',
      );
    } catch (err) {
      const detail =
        err instanceof MddbApiError
          ? err.status === 401 || err.status === 403
            ? 'authentication failed — check your API key'
            : err.status > 0
              ? `server returned ${err.status}`
              : err.message
          : err instanceof Error
            ? err.message
            : 'unknown error';
      setStatus(els.status, `Failed: ${detail}`, 'err');
    }
  });
}
