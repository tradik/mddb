// Verify that every template shipping a CDN asset pins it and checks its
// integrity. Run with: node --test sri.test.mjs
//
// This guarded md-viewer.html until that file was removed — a leftover from
// the gh-pages era that .ssg.yaml never listed in static_sources, so it had
// stopped being deployed at all. The check moved rather than went with it:
// index-section-replication.html renders mermaid on a page that IS deployed,
// and a floating version pin there is the same exposure the original FE-007
// work closed.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));

// Every template, not a list someone has to remember to extend: a new page
// loading a CDN asset without SRI is exactly what this exists to catch.
const templates = readdirSync(here)
  .filter((name) => name.endsWith('.html'))
  .map((name) => ({ name, html: readFileSync(join(here, name), 'utf8') }));

// Match every <script>/<link> pointing at a public CDN.
const cdnTagRe =
  /<(?:script|link)\b[^>]*\b(?:src|href)=["']https:\/\/(?:cdn\.jsdelivr\.net|cdnjs\.cloudflare\.com)\/[^"']+["'][^>]*>/gi;

test('FE-007: every CDN resource in a template uses SRI + crossorigin', () => {
  for (const { name, html } of templates) {
    for (const tag of html.match(cdnTagRe) ?? []) {
      assert.match(tag, /integrity="sha384-[A-Za-z0-9+/=]+"/, `${name}: missing SRI hash: ${tag}`);
      assert.match(tag, /crossorigin=/, `${name}: missing crossorigin: ${tag}`);
    }
  }
});

test('FE-007: mermaid is pinned to an exact version, wherever it is loaded', () => {
  // An ESM `import` from a CDN carries no integrity attribute, so the version
  // pin is the whole protection: @11 would let any 11.x publish replace it.
  const loaders = templates.filter(({ html }) => html.includes('mermaid@'));
  assert.ok(loaders.length > 0, 'no template loads mermaid — has the check outlived its subject?');

  for (const { name, html } of loaders) {
    assert.doesNotMatch(html, /mermaid@\d+\//, `${name}: mermaid must be pinned to an exact patch version`);
    assert.match(html, /mermaid@\d+\.\d+\.\d+\//, `${name}: mermaid pin is not a full version`);
  }
});
