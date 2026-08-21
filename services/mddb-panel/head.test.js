// FE-009: the repo convention is that every index.html carries current meta
// tags and a GTM snippet with a placeholder ID. These assert the panel's head
// keeps them, and — more importantly — that the analytics snippet stays inert
// until someone configures a container, so a build without VITE_GTM_ID never
// calls out to a container that does not exist.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const html = readFileSync(join(dirname(fileURLToPath(import.meta.url)), 'index.html'), 'utf8');

test('describes itself for link previews', () => {
  assert.match(html, /<meta\s+name="description"\s+content="[^"]{40,}"/s);
  assert.match(html, /property="og:title"/);
  assert.match(html, /property="og:description"/);
  assert.match(html, /property="og:type"/);
  assert.match(html, /name="twitter:card"/);
});

test('stays out of search indexes', () => {
  // The panel is behind a login; an indexed admin URL helps nobody.
  assert.match(html, /<meta\s+name="robots"\s+content="noindex/);
});

test('carries a GTM snippet with a build-time placeholder', () => {
  assert.match(html, /googletagmanager\.com\/gtm\.js/);
  assert.ok(html.includes('%VITE_GTM_ID%'), 'the GTM id must come from VITE_GTM_ID');
});

test('the GTM snippet is inert while the id is unconfigured', () => {
  // Run the snippet with the placeholder left unreplaced and assert it makes
  // no attempt to load anything.
  const snippet = html.match(/<script>\s*(\(function \(w, d, s, l, i\)[\s\S]*?)<\/script>/)[1];
  const created = [];
  const fakeDoc = {
    getElementsByTagName: () => [{ parentNode: { insertBefore: () => {} } }],
    createElement: () => {
      const el = {};
      created.push(el);
      return el;
    },
  };
  const w = {};
  new Function('window', 'document', `${snippet}`)(w, fakeDoc);

  assert.equal(created.length, 0, 'no script element should be created');
  assert.equal(w.dataLayer, undefined, 'dataLayer should not be initialised');
});

test('the GTM snippet loads once a real id is substituted', () => {
  const snippet = html
    .match(/<script>\s*(\(function \(w, d, s, l, i\)[\s\S]*?)<\/script>/)[1]
    .replace('%VITE_GTM_ID%', 'GTM-REAL123');
  const created = [];
  const fakeDoc = {
    getElementsByTagName: () => [{ parentNode: { insertBefore: () => {} } }],
    createElement: () => {
      const el = {};
      created.push(el);
      return el;
    },
  };
  const w = {};
  new Function('window', 'document', `${snippet}`)(w, fakeDoc);

  assert.equal(created.length, 1);
  assert.match(created[0].src, /gtm\.js\?id=GTM-REAL123/);
  assert.equal(w.dataLayer.length, 1);
});
