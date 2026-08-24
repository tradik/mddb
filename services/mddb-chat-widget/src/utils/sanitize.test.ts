// Run with: node --test src/utils/sanitize.test.ts (Node >= 23.6 strips types).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sanitizeInput } from './sanitize.ts';

// The regex this replaced — kept so the tests can show what changed rather
// than assert the new behaviour in a vacuum.
const oldRegexStrip = (s: string) => s.replace(/<[^>]*>/g, '');

test('CodeQL js/incomplete-multi-character-sanitization: an unterminated tag no longer survives', () => {
  // The regex needs a closing > to match anything, so these came out exactly
  // as they went in — which is what "may still contain <script" meant. The
  // fragment is later concatenated with other markup, which supplies the >.
  for (const input of [
    '<script',
    '<script src=x',
    'hi <img src=x onerror=alert(1)',
    '<div',
  ]) {
    assert.equal(
      oldRegexStrip(input),
      input,
      `the old regex was expected to leave ${input} untouched`,
    );
    assert.ok(
      !sanitizeInput(input, 2000).includes('<'),
      `${input} still carries a < after sanitising`,
    );
  }
});

test('closed tags are stripped, their text is kept', () => {
  assert.equal(sanitizeInput('<b>hi</b>', 2000), 'hi');
  assert.equal(sanitizeInput('<img src=x onerror=alert(1)>', 2000), '');
  assert.equal(
    sanitizeInput('<scr<script>ipt>alert(1)</script>', 2000),
    'ipt>alert(1)',
  );
});

test('a stray less-than eats the rest, deliberately', () => {
  // The consequence of scanning to the end when no > closes the tag. It is
  // the same trade the server makes, and the two agreeing matters more than
  // preserving an unmatched <.
  assert.equal(sanitizeInput('a<b', 2000), 'a');
  assert.equal(sanitizeInput('2 < 3 is true', 2000), '2');
});

test('ordinary text is untouched', () => {
  assert.equal(sanitizeInput('  how do I index a collection?  ', 2000), 'how do I index a collection?');
  assert.equal(sanitizeInput('a > b', 2000), 'a > b');
});

test('length is capped after stripping, not before', () => {
  // Truncating first would leave a half-removed tag behind.
  assert.equal(sanitizeInput('<b>abcdef</b>', 3), 'abc');
  assert.equal(sanitizeInput('x'.repeat(50), 10), 'x'.repeat(10));
});

test('empty input stays empty', () => {
  assert.equal(sanitizeInput('', 2000), '');
  assert.equal(sanitizeInput('   ', 2000), '');
});
