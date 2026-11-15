/**
 * Keyboard shortcuts of the panel (FE-016): which key event means which
 * command, the modifier name per platform, and the list the `?` dialog
 * shows. Pure functions; the editor and GlobalShortcuts attach them to
 * events.
 */
import { actionForKey, formatActions } from './markdown-format';

/** Whether the browser runs on macOS or iOS, where Cmd replaces Ctrl. */
export function isMacPlatform(nav = globalThis.navigator) {
  return /mac|iphone|ipad/i.test(nav?.userAgentData?.platform || nav?.platform || '');
}

/** The modifier's name in tooltips and the shortcut list. */
export function modName(isMac) {
  return isMac ? 'Cmd' : 'Ctrl';
}

/** Whether the event holds the platform's modifier (Cmd on a Mac, else Ctrl). */
function hasMod(event, isMac) {
  return isMac ? event.metaKey : event.ctrlKey;
}

/**
 * The editor command a key event in the Markdown textarea asks for: a
 * format action id ('bold', 'italic', 'link'), 'save', 'indent', 'outdent',
 * 'escape' — or null for every other key, which then types as usual.
 */
export function editorCommand(event, isMac) {
  if (hasMod(event, isMac) && !event.altKey && !event.shiftKey) {
    if (event.key.toLowerCase() === 's') return 'save';
    return actionForKey(event.key)?.id ?? null;
  }
  if (event.key === 'Tab' && !event.ctrlKey && !event.metaKey && !event.altKey) {
    return event.shiftKey ? 'outdent' : 'indent';
  }
  if (event.key === 'Escape') return 'escape';
  return null;
}

/** Whether keys typed at `el` belong to it (a form field or editable text). */
export function isTypingTarget(el) {
  if (!el) return false;
  if (el.isContentEditable) return true;
  return ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName);
}

/**
 * The panel-wide command of a key event: 'search' for `/`, 'help' for `?`,
 * else null. Keys typed into a form field are never taken.
 */
export function globalCommand(event) {
  if (event.ctrlKey || event.metaKey || event.altKey) return null;
  if (isTypingTarget(event.target)) return null;
  if (event.key === '/') return 'search';
  if (event.key === '?') return 'help';
  return null;
}

/** The tooltip of a toolbar action: its label and, if it has one, its shortcut. */
export function actionTitle(action, isMac) {
  return action.key ? `${action.label} (${modName(isMac)}+${action.key.toUpperCase()})` : action.label;
}

/** Every shortcut, as `{ keys, description }`, for the `?` dialog. */
export function shortcutList(isMac) {
  const mod = modName(isMac);
  const formats = formatActions
    .filter((a) => a.key)
    .map((a) => ({ keys: `${mod}+${a.key.toUpperCase()}`, description: `${a.label} (editor)` }));
  return [
    ...formats,
    { keys: `${mod}+S`, description: 'Save the document (editor)' },
    { keys: 'Tab / Shift+Tab', description: 'Indent / outdent the selected list items (editor)' },
    { keys: 'Esc', description: 'Leave the editor’s fullscreen mode' },
    { keys: '/', description: 'Focus the menu search' },
    { keys: '?', description: 'Show this list' },
  ];
}
