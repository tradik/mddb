// editor-shortcuts (FE-016): Ctrl on Windows/Linux, Cmd on macOS; the editor
// keys, the panel-wide `/` and `?` that never fire inside a form field, the
// toolbar tooltips and the list the `?` dialog shows.
import { describe, test, expect } from 'vitest';
import {
  isMacPlatform, modName, editorCommand, isTypingTarget, globalCommand, actionTitle, shortcutList,
} from './editor-shortcuts';
import { findFormatAction } from './markdown-format';

const key = (k, mods = {}) => ({ key: k, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...mods });

describe('platform', () => {
  test('macOS and iOS use Cmd, everything else Ctrl', () => {
    expect(isMacPlatform({ platform: 'MacIntel' })).toBe(true);
    expect(isMacPlatform({ userAgentData: { platform: 'macOS' }, platform: 'Linux' })).toBe(true);
    expect(isMacPlatform({ platform: 'iPhone' })).toBe(true);
    expect(isMacPlatform({ platform: 'Win32' })).toBe(false);
    expect(isMacPlatform({})).toBe(false);
    expect(isMacPlatform(undefined)).toBe(false);
    expect(typeof isMacPlatform()).toBe('boolean');
    expect(modName(true)).toBe('Cmd');
    expect(modName(false)).toBe('Ctrl');
  });
});

describe('editorCommand', () => {
  test('Ctrl+B / I / K / S off a Mac', () => {
    expect(editorCommand(key('b', { ctrlKey: true }), false)).toBe('bold');
    expect(editorCommand(key('I', { ctrlKey: true }), false)).toBe('italic');
    expect(editorCommand(key('k', { ctrlKey: true }), false)).toBe('link');
    expect(editorCommand(key('s', { ctrlKey: true }), false)).toBe('save');
    expect(editorCommand(key('x', { ctrlKey: true }), false)).toBeNull();
  });

  test('Cmd on a Mac; the other modifier does nothing there', () => {
    expect(editorCommand(key('b', { metaKey: true }), true)).toBe('bold');
    expect(editorCommand(key('S', { metaKey: true }), true)).toBe('save');
    expect(editorCommand(key('b', { ctrlKey: true }), true)).toBeNull();
    expect(editorCommand(key('b', { metaKey: true }), false)).toBeNull();
  });

  test('Alt or Shift with the modifier is not a shortcut', () => {
    expect(editorCommand(key('b', { ctrlKey: true, altKey: true }), false)).toBeNull();
    expect(editorCommand(key('s', { ctrlKey: true, shiftKey: true }), false)).toBeNull();
  });

  test('Tab, Shift+Tab and Escape', () => {
    expect(editorCommand(key('Tab'), false)).toBe('indent');
    expect(editorCommand(key('Tab', { shiftKey: true }), false)).toBe('outdent');
    expect(editorCommand(key('Tab', { altKey: true }), false)).toBeNull();
    expect(editorCommand(key('Tab', { metaKey: true }), false)).toBeNull();
    expect(editorCommand(key('Escape'), false)).toBe('escape');
    expect(editorCommand(key('a'), false)).toBeNull();
  });
});

describe('global keys', () => {
  const el = (tagName, extra = {}) => ({ tagName, isContentEditable: false, ...extra });

  test('isTypingTarget: form fields and editable text', () => {
    expect(isTypingTarget(null)).toBe(false);
    expect(isTypingTarget(el('INPUT'))).toBe(true);
    expect(isTypingTarget(el('TEXTAREA'))).toBe(true);
    expect(isTypingTarget(el('SELECT'))).toBe(true);
    expect(isTypingTarget(el('DIV', { isContentEditable: true }))).toBe(true);
    expect(isTypingTarget(el('BUTTON'))).toBe(false);
  });

  test('`/` searches and `?` helps, outside form fields and without modifiers', () => {
    const body = el('BODY');
    expect(globalCommand(key('/', { target: body }))).toBe('search');
    expect(globalCommand(key('?', { target: body, shiftKey: true }))).toBe('help');
    expect(globalCommand(key('/', { target: el('INPUT') }))).toBeNull();
    expect(globalCommand(key('/', { target: body, ctrlKey: true }))).toBeNull();
    expect(globalCommand(key('/', { target: body, metaKey: true }))).toBeNull();
    expect(globalCommand(key('?', { target: body, altKey: true }))).toBeNull();
    expect(globalCommand(key('a', { target: body }))).toBeNull();
  });
});

describe('what the user is shown', () => {
  test('a toolbar tooltip names the shortcut when there is one', () => {
    expect(actionTitle(findFormatAction('bold'), false)).toBe('Bold (Ctrl+B)');
    expect(actionTitle(findFormatAction('link'), true)).toBe('Link (Cmd+K)');
    expect(actionTitle(findFormatAction('quote'), false)).toBe('Blockquote');
  });

  test('the shortcut list covers every key, with the platform modifier', () => {
    const keys = shortcutList(true).map((s) => s.keys);
    expect(keys).toEqual(['Cmd+B', 'Cmd+I', 'Cmd+K', 'Cmd+S', 'Tab / Shift+Tab', 'Esc', '/', '?']);
    expect(shortcutList(false)[0]).toEqual({ keys: 'Ctrl+B', description: 'Bold (editor)' });
  });
});
