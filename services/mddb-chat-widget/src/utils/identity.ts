/**
 * The host's join token (#405) from `window.MDDB_CHAT_CONFIG.identityToken`:
 * a non-empty string, or `undefined` for an anonymous widget.
 *
 * The widget reads it again at every join, so a host that refreshes the
 * short-lived token in `window.MDDB_CHAT_CONFIG` before it expires has the
 * new one used on the next reconnect.
 */
export function identityTokenFrom(config: unknown): string | undefined {
  if (typeof config !== 'object' || config === null) return undefined;
  const token = (config as { identityToken?: unknown }).identityToken;
  return typeof token === 'string' && token !== '' ? token : undefined;
}
