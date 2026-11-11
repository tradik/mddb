# MDDB Chat Widget

Embeddable, zero-dependency chat widget (~6 KB gzip) that connects to the MDDB
chat server over WebSocket. Drop one `<script>` tag onto any page.

## Usage

```html
<script src="https://your-domain.com/mddb-chat.min.js"
  data-server="wss://chat.example.com/ws"
  data-scenario="assistant"
  data-theme="light"
  data-accent="#3B82F6"
  data-position="bottom-right">
</script>
```

### Script `data-*` attributes

| Attribute           | Required | Default          | Notes |
|---------------------|----------|------------------|-------|
| `data-server`       | **yes**  | —                | WebSocket endpoint. **Must be `wss://`** (see Security). `ws://` is accepted **only** for `localhost` / `127.0.0.1` / `[::1]` during local development. |
| `data-scenario`     | no       | `assistant`      | Server-side conversation scenario. |
| `data-theme`        | no       | `light`          | `light` or `dark`. |
| `data-accent`       | no       | —                | Accent colour (any CSS colour). |
| `data-position`     | no       | `bottom-right`   | `bottom-right` or `bottom-left`. |
| `data-session-ttl`  | no       | —                | Session persistence TTL in hours. |

### Signed-in users (2.19.0)

A page whose user is signed in sets a join token signed by its backend
before the widget loads; the widget reads it again at every join, so the host
can replace it before it expires:

```html
<script>window.MDDB_CHAT_CONFIG = { identityToken: '<token from your backend>' };</script>
```

With it, `join` carries `token` and the server resumes the user's stored
conversation (see `docs/chat.md`, "Signed-in users"). Without it the widget is
anonymous, as before. The token is never stored by the widget.

Ratings carry the `message_id` of the answer they rate, taken from the
`done` frame that ended it.

## Security

- **Encrypted transport is required (FE-006).** `data-server` is validated
  before any connection: only `wss://` URLs (any host), or `ws://` to a loopback
  host for development, are accepted. A non-`wss://` URL on a public page, a
  relative path, or a non-WebSocket scheme (`http:`, `javascript:`, …) is
  rejected and the widget refuses to start (a clear error is logged). This
  prevents a tampered `data-server` attribute from redirecting the chat session
  — messages and `sessionId` — to an attacker host or sending it in plaintext.
- **Assistant message rendering is XSS-hardened (FE-003).** Markdown links only
  produce anchors for `http(s)`/`mailto`/relative URLs; `javascript:`, `data:`,
  `vbscript:`, `blob:` and friends are dropped to plain text.
- **Session storage (FE-004).** The session id and transcript live in
  `sessionStorage` (per-tab, not persisted to disk), capped at the most recent
  50 messages.

### Recommended CSP for embedding pages

Restrict where the widget may open a socket. For a host serving the widget from
`chat.example.com`:

```
Content-Security-Policy: connect-src 'self' wss://chat.example.com;
```

## Development

```bash
npm install
npm run dev      # vite dev server with the demo index.html
npm test         # node --test (Node >= 23.6 strips TS types)
npm run build    # tsc typecheck + vite bundle -> dist/mddb-chat.min.js
```

Tests run through `node --test`; `*.test.ts` files are excluded from the `tsc`
production build (they rely on Node's type-stripping).
