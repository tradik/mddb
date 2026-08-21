// Guards the stylesheet layout of the template (FE-012).
//
// The template used to ship `style.css` and `styles.css` side by side — one
// the docs base sheet, one the landing sheet — names a tired reader cannot
// tell apart, so a rule meant for both pages could silently land on one. They
// are now `base.css` and `landing.css`. These tests keep the split explicit:
// every stylesheet a template links must exist, and every stylesheet on disk
// must be linked by something.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync, existsSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const cssDir = join(here, 'css');

const templates = readdirSync(here).filter((f) => f.endsWith('.html'));
const sheets = readdirSync(cssDir).filter((f) => f.endsWith('.css'));

/** Local stylesheet hrefs in a template, ignoring CDN ones. */
function localSheetHrefs(html) {
  return [...html.matchAll(/<link[^>]+rel="stylesheet"[^>]+href="([^"]+)"/g)]
    .map((m) => m[1])
    .filter((href) => !href.startsWith('http'));
}

test('every stylesheet a template links exists on disk', () => {
  for (const tpl of templates) {
    const html = readFileSync(join(here, tpl), 'utf8');
    for (const href of localSheetHrefs(html)) {
      // Site-absolute (/css/x.css) and template-relative (css/x.css) both
      // resolve to the same file in this directory.
      const file = join(cssDir, href.replace(/^\/?css\//, ''));
      assert.ok(existsSync(file), `${tpl} links ${href}, which does not exist`);
    }
  }
});

test('no stylesheet is orphaned', () => {
  const linked = new Set(
    templates.flatMap((tpl) =>
      localSheetHrefs(readFileSync(join(here, tpl), 'utf8')).map((h) =>
        h.replace(/^\/?css\//, ''),
      ),
    ),
  );
  for (const sheet of sheets) {
    assert.ok(linked.has(sheet), `css/${sheet} is not linked by any template`);
  }
});

test('the landing and docs sheets stay distinctly named', () => {
  // `style.css` next to `styles.css` is the exact confusion FE-012 removed.
  assert.ok(!sheets.includes('style.css'), 'style.css is back — use base.css');
  assert.ok(!sheets.includes('styles.css'), 'styles.css is back — use landing.css');
  assert.ok(sheets.includes('base.css') && sheets.includes('landing.css'));
});

test('docs templates and the landing page do not share a page sheet', () => {
  const base = localSheetHrefs(readFileSync(join(here, 'base.html'), 'utf8'));
  const index = localSheetHrefs(readFileSync(join(here, 'index.html'), 'utf8'));
  assert.ok(base.some((h) => h.endsWith('base.css')));
  assert.ok(index.some((h) => h.endsWith('landing.css')));
  assert.ok(!index.some((h) => h.endsWith('base.css')));
  assert.ok(!base.some((h) => h.endsWith('landing.css')));
});
