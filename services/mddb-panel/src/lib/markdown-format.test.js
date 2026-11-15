// markdown-format (FE-016): the one list of format actions behind the
// toolbar buttons and the keyboard shortcuts, wrapping a selection, and the
// list indentation Tab / Shift+Tab apply.
import { describe, test, expect } from 'vitest';
import {
  formatActions, findFormatAction, actionForKey, applyFormat, selectionIsList, indentLines, INDENT,
} from './markdown-format';

describe('format actions', () => {
  test('ids are unique and the shortcut letters are B, I and K', () => {
    const ids = formatActions.map((a) => a.id);
    expect(new Set(ids).size).toBe(ids.length);
    expect(formatActions.filter((a) => a.key).map((a) => [a.id, a.key])).toEqual([['bold', 'b'], ['italic', 'i'], ['link', 'k']]);
  });

  test('findFormatAction and actionForKey look actions up', () => {
    expect(findFormatAction('quote').before).toBe('> ');
    expect(findFormatAction('nope')).toBeUndefined();
    expect(actionForKey('B').id).toBe('bold');
    expect(actionForKey('k').id).toBe('link');
    expect(actionForKey('x')).toBeUndefined();
  });
});

describe('applyFormat', () => {
  test('wraps the selection and puts the cursor after it', () => {
    expect(applyFormat('hello world', 6, 11, findFormatAction('bold'))).toEqual({ value: 'hello **world**', cursor: 13 });
  });

  test('inserts the placeholder when nothing is selected', () => {
    expect(applyFormat('ab', 1, 1, findFormatAction('link'))).toEqual({ value: 'a[link text](url)b', cursor: 11 });
  });

  test('an action without after or placeholder inserts `before` only', () => {
    const table = findFormatAction('table');
    expect(applyFormat('', 0, 0, table)).toEqual({ value: table.before, cursor: table.before.length });
  });
});

describe('selectionIsList', () => {
  const text = '- one\n  * two\n3. three\nplain\n+ four';
  test('true when every touched line is a list item', () => {
    expect(selectionIsList(text, 0, 0)).toBe(true);
    expect(selectionIsList(text, 2, 16)).toBe(true);
    expect(selectionIsList(text, text.length, text.length)).toBe(true);
    expect(selectionIsList('1) x', 0, 0)).toBe(true);
  });

  test('false when a touched line is not one', () => {
    expect(selectionIsList(text, 24, 24)).toBe(false);
    expect(selectionIsList(text, 0, 26)).toBe(false);
    expect(selectionIsList('', 0, 0)).toBe(false);
  });

  test('a selection ending right after a newline leaves the next line out', () => {
    expect(selectionIsList('- a\nplain', 0, 4)).toBe(true);
  });
});

describe('indentLines', () => {
  test('indents the cursor line and moves the cursor with it', () => {
    expect(indentLines('x\n- a\ny', 4, 4)).toEqual({ value: `x\n${INDENT}- a\ny`, start: 6, end: 6 });
  });

  test('indents every selected line', () => {
    expect(indentLines('- a\n- b', 0, 7)).toEqual({ value: '  - a\n  - b', start: 2, end: 11 });
  });

  test('outdents by at most one step, never below the line start', () => {
    expect(indentLines('    - a\n - b\n- c', 0, 15, true)).toEqual({ value: '  - a\n- b\n- c', start: 0, end: 12 });
    expect(indentLines('  - a', 1, 1, true)).toEqual({ value: '- a', start: 0, end: 0 });
  });
});
