// FE-009: the dev preview page must carry the meta tags and GTM placeholder
// the repo expects of every index.html — and the analytics snippet must stay
// inert until a container is configured, so a build without VITE_GTM_ID makes
// no request to a container that does not exist.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const html = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), '..', 'index.html'),
  'utf8',
);

/** Pull the GTM loader out of the page so it can be exercised directly. */
function gtmSnippet(id?: string): string {
  const match = html.match(/<script>\s*(\(function \(w, d, s, l, i\)[\s\S]*?)<\/script>/);
  assert.ok(match, 'the GTM snippet should be present');
  return id ? match[1].replace('%VITE_GTM_ID%', id) : match[1];
}

function runSnippet(source: string) {
  const created: Array<Record<string, unknown>> = [];
  const fakeDoc = {
    getElementsByTagName: () => [{ parentNode: { insertBefore: () => {} } }],
    createElement: () => {
      const el: Record<string, unknown> = {};
      created.push(el);
      return el;
    },
  };
  const w: Record<string, unknown> = {};
  new Function('window', 'document', source)(w, fakeDoc);
  return { created, w };
}

test('describes itself for link previews', () => {
  assert.match(html, /<meta\s+name="description"\s+content="[^"]{40,}"/s);
  assert.match(html, /property="og:title"/);
  assert.match(html, /property="og:description"/);
  assert.match(html, /property="og:type"/);
  assert.match(html, /name="twitter:card"/);
});

test('stays out of search indexes', () => {
  assert.match(html, /<meta\s+name="robots"\s+content="noindex/);
});

test('carries a GTM snippet with a build-time placeholder', () => {
  assert.match(html, /googletagmanager\.com\/gtm\.js/);
  assert.ok(html.includes('%VITE_GTM_ID%'), 'the GTM id must come from VITE_GTM_ID');
});

test('the GTM snippet is inert while the id is unconfigured', () => {
  const { created, w } = runSnippet(gtmSnippet());
  assert.equal(created.length, 0, 'no script element should be created');
  assert.equal(w.dataLayer, undefined, 'dataLayer should not be initialised');
});

test('the GTM snippet loads once a real id is substituted', () => {
  const { created, w } = runSnippet(gtmSnippet('GTM-REAL123'));
  assert.equal(created.length, 1);
  assert.match(String(created[0].src), /gtm\.js\?id=GTM-REAL123/);
  assert.equal((w.dataLayer as unknown[]).length, 1);
});
