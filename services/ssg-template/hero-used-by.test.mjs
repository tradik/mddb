// Guards the homepage hero (calls to action + product video) and the
// rotating "Powers" strip. The inline scripts are pulled out of the templates
// and run against the smallest fake DOM they touch.
// Run with: node --test hero-used-by.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { extractFunctionSource } from './extract-fn.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const read = (name) => readFileSync(join(here, name), 'utf8');
// eslint-disable-next-line no-new-func
const load = (src, name) => new Function(`return (${extractFunctionSource(src, name)})`)();

const hero = read('index-section-hero.html');
const usedBy = read('index-section-used-by.html');
const rotate = read('index-section-rotate.html');
const startRotation = load(rotate, 'startRotation');

/** A fake element: classList, attributes, listeners, optional query results. */
function fakeEl(attrs = {}) {
  const classes = new Set();
  const listeners = {};
  return {
    hidden: true,
    attrs: { ...attrs },
    classList: {
      add: (c) => classes.add(c),
      remove: (c) => classes.delete(c),
      contains: (c) => classes.has(c),
    },
    getAttribute(k) { return this.attrs[k] ?? null; },
    setAttribute(k, v) { this.attrs[k] = v; },
    removeAttribute(k) { delete this.attrs[k]; },
    addEventListener: (type, fn) => { listeners[type] = fn; },
    fire: (type, ev = { preventDefault() {} }) => listeners[type](ev),
  };
}

// ----- hero -----

const playHeroVideo = load(hero, 'playHeroVideo');

test('the hero keeps its download and GitHub calls to action', () => {
  assert.match(hero, /href="#download"/);
  assert.match(hero, /href="https:\/\/github\.com\/tradik\/mddb"/);
});

test('the video sits under the calls to action as a poster linking to YouTube', () => {
  assert.ok(hero.indexOf('class="hero-actions"') < hero.indexOf('class="hero-video"'));
  const link = hero.match(/<a class="hero-video-play"[\s\S]*?>/)?.[0] ?? '';
  assert.match(link, /href="https:\/\/www\.youtube\.com\/watch\?v=Idg6NyNYQnU"/);
  assert.match(link, /aria-label="Play video:[^"]+"/);
  assert.match(hero, /<img src="https:\/\/i\.ytimg\.com\/vi\/Idg6NyNYQnU\/[^"]+"\s+alt="[^"]+"/);
  assert.doesNotMatch(hero, /<iframe/, 'no player before the visitor asks for it');
});

function videoSetup(withParent = true) {
  const attrs = {
    'data-video-id': 'Idg6NyNYQnU',
    'data-video-title': 'MDDB video',
  };
  const link = { getAttribute: (k) => attrs[k] ?? null, parentNode: null };
  const replaced = [];
  if (withParent) {
    link.parentNode = { replaceChild: (n, o) => replaced.push([n, o]) };
  }
  let focused = false;
  const doc = { createElement: () => ({ focus() { focused = true; } }) };
  return { link, doc, replaced, focused: () => focused };
}

test('a click swaps the poster for the no-cookie player, autoplaying', () => {
  const v = videoSetup();
  const frame = playHeroVideo(v.link, v.doc);
  assert.equal(frame.src, 'https://www.youtube-nocookie.com/embed/Idg6NyNYQnU?autoplay=1&rel=0');
  assert.equal(frame.title, 'MDDB video');
  assert.match(frame.allow, /autoplay/);
  assert.equal(frame.allowFullscreen, true);
  assert.equal(frame.referrerPolicy, 'strict-origin-when-cross-origin');
  assert.deepEqual(v.replaced, [[frame, v.link]]);
  assert.ok(v.focused(), 'focus moves to the player for keyboard users');
});

test('no poster, or one already replaced, does nothing', () => {
  assert.equal(playHeroVideo(null, videoSetup().doc), null);
  const v = videoSetup(false);
  assert.equal(playHeroVideo(v.link, v.doc), null);
});

// ----- "Powers" strip -----

test('the "Powers" strip names both production deployments', () => {
  assert.match(usedBy, /href="https:\/\/daemonports\.org\/"/);
  assert.match(usedBy, /href="https:\/\/versions\.tradik\.com\/"/);
  assert.match(usedBy, /220\+ packages/);
});

function stripSetup(count, reducedMotion = false) {
  const items = Array.from({ length: count }, () => fakeEl());
  const list = fakeEl();
  list.querySelectorAll = () => items;
  let tick = null;
  const win = {
    matchMedia: () => ({ matches: reducedMotion }),
    setInterval: (fn) => { tick = fn; return 42; },
  };
  const id = startRotation(list, '.used-by-text', win, 7000);
  const active = () => items.findIndex((i) => i.classList.contains('is-active'));
  return { id, list, strip: list, tick: () => tick(), active };
}

test('rotation shows one entry at a time and wraps around', () => {
  const s = stripSetup(2);
  assert.equal(s.id, 42);
  assert.ok(s.list.classList.contains('is-rotating'));
  assert.equal(s.active(), 0);
  s.tick();
  assert.equal(s.active(), 1);
  s.tick();
  assert.equal(s.active(), 0);
});

test('rotation holds while hovered or focused', () => {
  const s = stripSetup(2);
  for (const [on, off] of [['mouseenter', 'mouseleave'], ['focusin', 'focusout']]) {
    s.strip.fire(on);
    s.tick();
    assert.equal(s.active(), 0, `${on} did not hold`);
    s.strip.fire(off);
    s.tick();
    assert.equal(s.active(), 1);
    s.tick();
  }
});

test('no rotation with a single entry, reduced motion or no element', () => {
  for (const s of [stripSetup(1), stripSetup(2, true)]) {
    assert.equal(s.id, null);
    assert.ok(!s.list.classList.contains('is-rotating'));
  }
  assert.equal(startRotation(null, '.x', { matchMedia: () => ({ matches: false }) }, 1), null);
});

const addRotatorWords = load(rotate, 'addRotatorWords');

test('both rotators are started, and the page loads the script once', () => {
  assert.match(rotate, /startRotation\(heroRotator, '\.hero-rotator-word'/);
  assert.match(rotate, /startRotation\(document\.querySelector\('\.used-by-list'\), '\.used-by-text'/);
  const index = read('index.html');
  assert.equal(index.split('{{template "index_rotate" .}}').length, 2);
});

test('the hero heading in HTML is "code intelligence"; the rest comes from data-more', () => {
  const h1 = hero.match(/<h1>([\s\S]*?)<\/h1>/)[1];
  assert.match(h1, /data-more="content spatial visual temporal"><span class="hero-rotator-word">code<\/span><\/span> intelligence$/);
});

test('addRotatorWords appends one word span per data-more entry', () => {
  const appended = [];
  const root = { getAttribute: () => 'content  spatial', appendChild: (n) => appended.push(n) };
  const doc = { createElement: () => ({}) };
  assert.equal(addRotatorWords(root, doc), 2);
  assert.deepEqual(appended.map((n) => [n.className, n.textContent]),
    [['hero-rotator-word', 'content'], ['hero-rotator-word', 'spatial']]);
  assert.equal(addRotatorWords(null, doc), 0);
  assert.equal(addRotatorWords({ getAttribute: () => null }, doc), 0);
});
