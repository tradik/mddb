// Guards the blog's categories and topics. The generator ignores a category
// that docs/metadata.json does not define, so a post with a typo or a new
// category silently drops out of every category archive; these tests catch
// that before the build does not.
// Run with: node --test blog.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, '..', '..');
const blogDir = join(root, 'blog');
const partial = readFileSync(join(here, 'partials', 'blog.html'), 'utf8');
const metadata = JSON.parse(readFileSync(join(root, 'docs', 'metadata.json'), 'utf8'));

const categories = metadata.categories.map((c) => c.name);
// The fixed vocabulary of blog/README.md, plus the two single-post terms
// already published (their archives are public URLs).
const vocabulary = new Set([
  'agents', 'benchmarking', 'embeddings', 'mcp', 'operations', 'performance',
  'rag', 'release', 'retrieval', 'search', 'security', 'interoperability', 'storage',
]);

/** Front matter of every dated post as { file, categories, tags }. */
const posts = readdirSync(blogDir)
  .filter((f) => /^\d{4}-\d{2}-\d{2}-.+\.md$/.test(f))
  .map((file) => {
    const fm = readFileSync(join(blogDir, file), 'utf8').split('\n---', 1)[0];
    const list = (key) => (fm.match(new RegExp(`^${key}: \\[(.*)\\]`, 'm'))?.[1] ?? '')
      .split(',').map((s) => s.trim()).filter(Boolean);
    return { file, categories: list('categories'), tags: list('tags') };
  });

test('metadata.json defines the three blog categories with slugs', () => {
  assert.deepEqual(
    metadata.categories.map((c) => [c.name, c.slug]),
    [['Releases', 'releases'], ['Engineering', 'engineering'], ['Guides', 'guides']],
  );
});

test('every post names exactly one category that metadata.json defines', () => {
  assert.ok(posts.length > 0, 'no posts found');
  for (const p of posts) {
    assert.equal(p.categories.length, 1, `${p.file}: categories ${JSON.stringify(p.categories)}`);
    assert.ok(categories.includes(p.categories[0]), `${p.file}: unknown category ${p.categories[0]}`);
  }
});

test('every post has tags, all from the vocabulary', () => {
  for (const p of posts) {
    assert.ok(p.tags.length > 0, `${p.file}: no tags`);
    for (const t of p.tags) assert.ok(vocabulary.has(t), `${p.file}: tag "${t}" is not in the vocabulary`);
  }
});

test('the category bar links every category and the feeds', () => {
  for (const name of categories) {
    assert.match(partial, new RegExp(`termURL "category" "${name}"`), `bar misses ${name}`);
  }
  assert.match(partial, /href="\/blog\/"/);
  assert.match(partial, /href="\/feed\.xml"/);
  assert.match(partial, /href="\/rss\.xml"/);
});

test('a card keeps tags outside its main link (no nested links)', () => {
  const card = partial.slice(partial.indexOf('{{define "post_card"}}'));
  const linkEnd = card.indexOf('</a>');
  assert.ok(card.indexOf('post-tile-tags') > linkEnd, 'tag links sit inside the card link');
  assert.match(card, /<img class="post-tile-cover"[^>]*alt=""/);
});
