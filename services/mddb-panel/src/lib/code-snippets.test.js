// Run with: npm test (node --test).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { jsonToPHP, jsonToPython } from '../lib/code-snippets.js';

// CodeQL js/incomplete-sanitization: both generators escaped the quote and not
// the backslash. These snippets are shown to a user to copy and run, so a value
// that breaks out of the string literal is code the user pastes into a shell.

test('a trailing backslash no longer escapes the closing quote', () => {
  // The old output was 'a\' — the backslash escaped the quote, so the string
  // never closed and everything after it was read as code.
  assert.equal(jsonToPHP('a\\'), "'a\\\\'");
  assert.equal(jsonToPython('a\\'), '"a\\\\"');
});

test('a value cannot close the literal and append code', () => {
  // A backslash, then a quote, then shell text. Under the old escaping the
  // backslash was left alone and the quote became \', so PHP saw
  //   'a\\' ; rm -rf /'
  // — the doubled backslash ended the escape, the quote closed the string, and
  // the rest was code in a snippet the user was invited to copy and run.
  const hostile = "a\\' ; rm -rf /";

  assert.equal(jsonToPHP(hostile), "'a\\\\\\' ; rm -rf /'");
  assert.equal(jsonToPython(hostile), '"a\\\\\' ; rm -rf /"');
});

test('quotes are still escaped', () => {
  assert.equal(jsonToPHP("it's"), "'it\\'s'");
  assert.equal(jsonToPython('say "hi"'), '"say \\"hi\\""');
});

test('a quote the other language does not use is left alone', () => {
  // PHP single-quoted strings do not treat " specially, and Python's
  // double-quoted ones do not treat ' specially. Escaping either would put a
  // stray backslash into the snippet.
  assert.equal(jsonToPHP('say "hi"'), "'say \"hi\"'");
  assert.equal(jsonToPython("it's"), '"it\'s"');
});

test('windows paths survive intact', () => {
  assert.equal(jsonToPHP('C:\\Users\\ada'), "'C:\\\\Users\\\\ada'");
  assert.equal(jsonToPython('C:\\Users\\ada'), '"C:\\\\Users\\\\ada"');
});

test('non-strings are unaffected', () => {
  assert.equal(jsonToPHP(null), 'null');
  assert.equal(jsonToPHP(true), 'true');
  assert.equal(jsonToPHP(42), '42');
  assert.equal(jsonToPython(null), 'None');
  assert.equal(jsonToPython(true), 'True');
  assert.equal(jsonToPython(42), '42');
});
