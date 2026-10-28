// Section links: addHeadingAnchors() in js/main.js puts a "#" link on each
// h1–h3 that has an id. Exercised on a small stand-in for the DOM.
// Run with: node --test heading-anchors.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import { extractFunctionSource } from './extract-fn.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const js = readFileSync(join(here, 'js', 'main.js'), 'utf8');
// eslint-disable-next-line no-new-func
const addHeadingAnchors = new Function(`return (${extractFunctionSource(js, 'addHeadingAnchors')})`)();

function element(tag, id = '', text = '') {
  return {
    tag, id, textContent: text, children: [], attrs: {}, className: '', href: '',
    setAttribute(k, v) { this.attrs[k] = v; },
    appendChild(c) { this.children.push(c); },
    querySelector(sel) {
      return sel === '.heading-anchor' ? this.children.find((c) => c.className === 'heading-anchor') || null : null;
    },
  };
}

const doc = { createElement: (tag) => element(tag) };

function rootWith(...headings) {
  return {
    querySelectorAll(sel) {
      assert.equal(sel, 'h1[id], h2[id], h3[id]');
      return headings.filter((h) => ['h1', 'h2', 'h3'].includes(h.tag) && h.id);
    },
  };
}

test('every h1–h3 with an id gets a link to itself', () => {
  const h2 = element('h2', 'overview', ' Overview ');
  const h3 = element('h3', '1-deterministic-ids', '1. Deterministic IDs');
  addHeadingAnchors(rootWith(h2, h3), doc);
  for (const h of [h2, h3]) {
    assert.equal(h.children.length, 1);
    const a = h.children[0];
    assert.equal(a.tag, 'a');
    assert.equal(a.className, 'heading-anchor');
    assert.equal(a.href, `#${h.id}`);
    assert.equal(a.textContent, '#');
  }
  assert.equal(h2.children[0].attrs['aria-label'], 'Link to the section Overview');
});

test('headings without an id and h4 are left alone', () => {
  const noId = element('h2', '', 'Untitled');
  const h4 = element('h4', 'detail', 'Detail');
  addHeadingAnchors(rootWith(noId, h4), doc);
  assert.equal(noId.children.length, 0);
  assert.equal(h4.children.length, 0);
});

test('running it twice adds one link, not two', () => {
  const h1 = element('h1', 'intro', 'Intro');
  const root = rootWith(h1);
  addHeadingAnchors(root, doc);
  addHeadingAnchors(root, doc);
  assert.equal(h1.children.length, 1);
});
