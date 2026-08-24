import express from 'express';
import { createProxyMiddleware } from 'http-proxy-middleware';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dirname = dirname(fileURLToPath(import.meta.url));

const PORT = process.env.PORT || 3000;
const MDDB_SERVER = process.env.MDDB_SERVER || 'http://localhost:11023';

const app = express();

// Proxy /v1/* requests to mddbd
app.use(createProxyMiddleware({
  target: MDDB_SERVER,
  changeOrigin: true,
  pathFilter: '/v1/**',
  on: {
    error: (err, _req, res) => {
      console.error(`Proxy error: ${err.message}`);
      if (!res.headersSent) {
        res.status(502).json({ error: 'backend unavailable' });
      }
    },
  },
}));

// Serve static files from dist/
app.use(express.static(join(__dirname, 'dist')));

// SPA fallback — serve index.html for all non-API routes.
// FE-008: Express 5 (path-to-regexp v8) requires route patterns to start with
// '/'. The leading-slash-less '{*path}' failed to match deep links (e.g. a
// refresh on /documents/123 returned 404 instead of the SPA). '/{*path}' is the
// correct optional catch-all and also matches '/'.
//
// Deliberately not rate-limited, which CodeQL flags (js/missing-rate-limiting).
// The path sent is a constant — nothing a caller supplies reaches the
// filesystem — so this is not a steerable file access, and the panel is
// documented to run behind a reverse proxy or cloudflared.
//
// The stronger reason is the one SEC-014 turned up on mddb-chat: a limiter
// here would key on the address it sees, and behind a proxy that is the
// proxy. Every visitor would share one bucket and the first noisy one would
// lock out the rest. Rate limiting belongs where the real client address is
// visible, which is the proxy — adding it here would repeat a bug this
// release just fixed.
app.get('/{*path}', (_req, res) => {
  res.sendFile(join(__dirname, 'dist', 'index.html'));
});

app.listen(PORT, '0.0.0.0', () => {
  console.log(`mddb-panel listening on :${PORT}`);
  console.log(`  proxy: /v1/* -> ${MDDB_SERVER}`);
});
