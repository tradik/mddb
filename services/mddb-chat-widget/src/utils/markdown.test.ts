// Run with: node --test src/utils/markdown.test.ts (Node >= 23.6 strips types).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { renderMarkdown, isSafeUrl, replaceLinks } from './markdown.ts';

test('FE-003: javascript: link renders as plain text, not an anchor', () => {
  const html = renderMarkdown('[click](javascript:alert(document.cookie))');
  assert.ok(!html.includes('<a '), `unexpected anchor: ${html}`);
  assert.ok(!html.toLowerCase().includes('javascript:'), `javascript: leaked: ${html}`);
  assert.ok(html.includes('click'), 'link label should survive as text');
});

test('FE-003: unsafe scheme is rejected regardless of case/whitespace', () => {
  for (const raw of [
    'JAVASCRIPT:alert(1)',
    ' javascript:alert(1)',
    'java\tscript:alert(1)',
    'java\nscript:alert(1)',
    'data:text/html,<script>1</script>',
    'vbscript:msgbox(1)',
    'blob:https://x/y',
    'file:///etc/passwd',
  ]) {
    assert.equal(isSafeUrl(raw), false, `should reject: ${JSON.stringify(raw)}`);
  }
});

test('FE-003: safe schemes and relative references are allowed', () => {
  for (const raw of [
    'https://ok.example/path?q=1',
    'http://ok.example',
    'HTTPS://OK.EXAMPLE',
    'mailto:a@b.c',
    '/relative/path',
    './sibling',
    '../parent',
    '#anchor',
  ]) {
    assert.equal(isSafeUrl(raw), true, `should allow: ${JSON.stringify(raw)}`);
  }
});

test('FE-003: safe link renders an anchor with noopener noreferrer', () => {
  const html = renderMarkdown('[docs](https://example.com)');
  assert.match(html, /<a href="https:\/\/example\.com" target="_blank" rel="noopener noreferrer">docs<\/a>/);
});

test('FE-003: relative link is preserved as an anchor', () => {
  const html = renderMarkdown('[home](/start)');
  assert.match(html, /<a href="\/start"[^>]*>home<\/a>/);
});

test('escapeHtml still neutralises inline HTML in message text', () => {
  const html = renderMarkdown('<img src=x onerror=alert(1)>');
  assert.ok(!html.includes('<img'), `raw tag leaked: ${html}`);
  assert.ok(html.includes('&lt;img'), 'angle brackets should be escaped');
});

test('basic markdown (bold, code) still renders', () => {
  assert.match(renderMarkdown('**hi**'), /<strong>hi<\/strong>/);
  assert.match(renderMarkdown('`x`'), /<code>x<\/code>/);
});

// The scanner replaced /\[([^\]]+)\]\(([^)]+)\)/g (Sonar S8786: backtracking).
// It must match exactly what the regex matched.
test('replaceLinks matches what the link regex matched', () => {
  const re = /\[([^\]]+)\]\(([^)]+)\)/g;
  const render = (label: string, url: string) => `<${label}|${url}>`;
  const alphabet = ['[', ']', '(', ')', 'a', 'b', ' ', '\n'];
  let seed = 7;
  const rnd = () => (seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648;
  for (let n = 0; n < 5000; n++) {
    let s = '';
    const len = Math.floor(rnd() * 24);
    for (let k = 0; k < len; k++) s += alphabet[Math.floor(rnd() * alphabet.length)];
    const want = s.replace(re, (_m, label, url) => render(label, url));
    assert.equal(replaceLinks(s, render), want, JSON.stringify(s));
  }
});

test('replaceLinks is linear on input the regex backtracked on', () => {
  const evil = '['.repeat(50000) + ']' + 'x'.repeat(50000);
  const start = Date.now();
  assert.equal(replaceLinks(evil, () => 'L'), evil);
  assert.equal(replaceLinks('[a](' + 'b'.repeat(50000), () => 'L'), '[a](' + 'b'.repeat(50000));
  assert.ok(Date.now() - start < 500, `took ${Date.now() - start} ms`);
});
