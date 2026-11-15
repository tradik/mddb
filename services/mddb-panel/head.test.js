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
  // Anchored to the full URL, scheme included. The bare
  // /googletagmanager\.com\/gtm\.js/ this replaces accepted
  // https://evil-googletagmanager.com/gtm.js and an http:// downgrade of the
  // real host — both of which are exactly what a test pinning where the page
  // loads a script from exists to catch.
  assert.match(html, /'https:\/\/www\.googletagmanager\.com\/gtm\.js\?id='/);
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

// FE-015: the inline theme script sets the `dark` class before the first
// paint, from the same localStorage key src/lib/theme.js writes.
const themeSnippet = html.match(/<script>\s*(\(function \(w, d\)[\s\S]*?)<\/script>/)[1];

/** Runs the theme snippet; returns whether it set the `dark` class. */
function runThemeSnippet({ stored = null, prefersDark = false, storageThrows = false, mediaThrows = false }) {
  let dark = null;
  const w = {
    localStorage: {
      getItem: (key) => {
        if (storageThrows) throw new Error('blocked');
        return key === 'mddb.theme' ? stored : null;
      },
    },
    matchMedia: (query) => {
      if (mediaThrows) throw new Error('none');
      return { matches: query === '(prefers-color-scheme: dark)' && prefersDark };
    },
  };
  const d = { documentElement: { classList: { toggle: (name, on) => { if (name === 'dark') dark = on; } } } };
  new Function('window', 'document', themeSnippet)(w, d);
  return dark;
}

test('the theme snippet uses the key of src/lib/theme.js', async () => {
  const { THEME_KEY } = await import('./src/lib/theme.js');
  assert.ok(themeSnippet.includes(`'${THEME_KEY}'`));
});

test('the theme snippet follows the stored choice, then the system', () => {
  assert.equal(runThemeSnippet({ stored: 'dark' }), true);
  assert.equal(runThemeSnippet({ stored: 'light', prefersDark: true }), false);
  assert.equal(runThemeSnippet({ stored: 'system', prefersDark: true }), true);
  assert.equal(runThemeSnippet({}), false);
});

test('the theme snippet stays light when storage or matchMedia throw', () => {
  assert.equal(runThemeSnippet({ storageThrows: true, prefersDark: true }), true);
  assert.equal(runThemeSnippet({ storageThrows: true, mediaThrows: true }), false);
});

test('the theme snippet runs before the app script', () => {
  assert.ok(html.indexOf(themeSnippet) < html.indexOf('/src/main.jsx'));
});
