import { ChatWidget, type WidgetOptions } from './widget';
import { validateServerUrl } from './utils/serverUrl';

function init(): void {
  // Find the script tag that loaded us
  const scripts = document.querySelectorAll('script[data-server]');
  const script = scripts[scripts.length - 1] as HTMLScriptElement | undefined;

  if (!script) {
    console.error('[mddb-chat] Missing data-server attribute on script tag');
    return;
  }

  // FE-006: validate the WebSocket endpoint before connecting. Reject anything
  // that isn't wss:// (or ws:// to localhost for dev) so the chat session can't
  // be redirected to an attacker host or sent in plaintext on a public page.
  const server = validateServerUrl(script.dataset.server ?? '');
  if (!server) {
    console.error(
      '[mddb-chat] data-server must be an absolute wss:// URL (ws:// is allowed only for localhost)',
    );
    return;
  }

  const sessionTtlAttr = script.dataset.sessionTtl;
  const options: WidgetOptions = {
    server,
    scenario: script.dataset.scenario || 'assistant',
    theme: (script.dataset.theme as 'light' | 'dark') || 'light',
    accent: script.dataset.accent || undefined,
    position:
      (script.dataset.position as 'bottom-right' | 'bottom-left') ||
      'bottom-right',
    sessionTtlHours: sessionTtlAttr ? parseInt(sessionTtlAttr, 10) : undefined,
  };

  // Create host element
  const host = document.createElement('div');
  host.id = 'mddb-chat-widget';
  document.body.appendChild(host);

  // Initialize widget
  const widget = new ChatWidget(host, options);

  // Expose globally for programmatic control
  (window as any).__mddbChat = widget;
}

// Auto-init when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', init);
} else {
  init();
}
