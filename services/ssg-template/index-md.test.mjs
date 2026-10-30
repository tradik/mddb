// FE-010 — the inline chat markdown renderer md() in index.html had an
// over-escaped code-fence regex (\\w / \\n / [\\s\\S]), so triple-backtick code
// blocks never rendered. These tests extract md() from the HTML and exercise it.
// Run with: node --test index-md.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import { extractFunctionSource } from './extract-fn.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const html = readFileSync(join(here, 'index.html'), 'utf8');

// Extract `function md(...) { ... }` and build a callable.
// eslint-disable-next-line no-new-func
const md = new Function(`return (${extractFunctionSource(html, 'md')})`)();

test('FE-010: a multi-line ```code``` block renders as <pre><code>', () => {
  const out = md('intro\n```js\nconst x = 1;\nconst y = 2;\n```\nouttro');
  assert.match(out, /<pre><code>[\s\S]*const x = 1;[\s\S]*<\/code><\/pre>/);
  assert.ok(!out.includes('```'), `raw fences leaked: ${out}`);
});

test('FE-010: HTML inside a code block stays escaped (no XSS regression)', () => {
  const out = md('```\n<script>alert(1)</script>\n```');
  assert.ok(out.includes('&lt;script&gt;'), `script not escaped: ${out}`);
  assert.ok(!out.includes('<script>'), 'raw <script> leaked');
});

test('FE-010: inline markdown still works', () => {
  assert.match(md('**bold**'), /<strong>bold<\/strong>/);
  assert.match(md('`code`'), /<code>code<\/code>/);
});

// The inline chat widget connects to ws://localhost:11030 — an mddb-chat on the
// visitor's machine. It must start only where the page is served locally; on
// the public site it was a bubble stuck on "Connecting..." for everyone.
test('chat widget starts only on a local host', () => {
  const m = html.match(/if \(!(\/\^\(localhost[^\n]*?\$\/)\.test\(window\.location\.hostname\)\) return;/);
  assert.ok(m, 'local-host guard not found before the chat widget');
  assert.ok(html.indexOf(m[0]) < html.indexOf("const CHAT_SERVER = 'ws://localhost:11030/ws'"),
    'the guard must run before the widget is built');
  // eslint-disable-next-line no-new-func
  const re = new Function(`return ${m[1]}`)();
  for (const host of ['localhost', '127.0.0.1', '[::1]']) assert.ok(re.test(host), host);
  for (const host of ['mddb.tradik.com', 'localhost.example.com', 'evil-localhost']) assert.ok(!re.test(host), host);
});
