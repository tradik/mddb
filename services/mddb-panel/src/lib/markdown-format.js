/**
 * Markdown formatting actions of the document editor (FE-016). One list
 * serves the toolbar buttons and the keyboard shortcuts, so a button and its
 * key always insert the same thing. The functions here are pure: they take
 * the text and the selection and return the new text and selection.
 */

/**
 * Every formatting action: `before` and `after` wrap the selection, or
 * `placeholder` when nothing is selected. `key` is the letter of its
 * Ctrl/Cmd shortcut; `group` splits the toolbar with dividers.
 */
export const formatActions = [
  { id: 'h1', label: 'Heading 1', group: 1, before: '# ', placeholder: 'Heading 1' },
  { id: 'h2', label: 'Heading 2', group: 1, before: '## ', placeholder: 'Heading 2' },
  { id: 'h3', label: 'Heading 3', group: 1, before: '### ', placeholder: 'Heading 3' },
  { id: 'bold', label: 'Bold', group: 2, before: '**', after: '**', placeholder: 'bold text', key: 'b' },
  { id: 'italic', label: 'Italic', group: 2, before: '*', after: '*', placeholder: 'italic text', key: 'i' },
  { id: 'strike', label: 'Strikethrough', group: 2, before: '~~', after: '~~', placeholder: 'strikethrough text' },
  { id: 'code', label: 'Inline Code', group: 2, before: '`', after: '`', placeholder: 'code' },
  { id: 'link', label: 'Link', group: 3, before: '[', after: '](url)', placeholder: 'link text', key: 'k' },
  { id: 'ul', label: 'Bullet List', group: 3, before: '- ', placeholder: 'list item' },
  { id: 'ol', label: 'Numbered List', group: 3, before: '1. ', placeholder: 'list item' },
  { id: 'task', label: 'Task List', group: 3, before: '- [ ] ', placeholder: 'task item' },
  { id: 'quote', label: 'Blockquote', group: 4, before: '> ', placeholder: 'quote' },
  {
    id: 'table',
    label: 'Table',
    group: 4,
    before: '| Header 1 | Header 2 |\n| -------- | -------- |\n| Cell 1   | Cell 2   |\n',
  },
];

/** The action with this id, or undefined. */
export function findFormatAction(id) {
  return formatActions.find((a) => a.id === id);
}

/** The action whose shortcut letter is `key` (case-insensitive), or undefined. */
export function actionForKey(key) {
  const letter = String(key).toLowerCase();
  return formatActions.find((a) => a.key === letter);
}

/**
 * Applies `action` to `value` with the selection [start, end): the selected
 * text (or the action's placeholder) is wrapped in `before`/`after`.
 * Returns the new text and the cursor position after the inserted text.
 */
export function applyFormat(value, start, end, action) {
  const { before, after = '', placeholder = '' } = action;
  const text = value.substring(start, end) || placeholder;
  return {
    value: value.substring(0, start) + before + text + after + value.substring(end),
    cursor: start + before.length + text.length,
  };
}

/** One indentation step of a list item. */
export const INDENT = '  ';

const LIST_ITEM = /^\s*(?:[-*+]|\d+[.)])\s/;

/** The [first, last) bounds of the lines the selection [start, end) touches. */
function lineBounds(value, start, end) {
  const first = value.lastIndexOf('\n', start - 1) + 1;
  const lastBreak = value.indexOf('\n', end > start && value[end - 1] === '\n' ? end - 1 : end);
  return [first, lastBreak === -1 ? value.length : lastBreak];
}

/**
 * Whether every line the selection touches is a Markdown list item. Tab
 * indents only then; elsewhere it keeps moving the focus, so the editor is
 * no keyboard trap (WCAG 2.2 SC 2.1.2).
 */
export function selectionIsList(value, start, end) {
  const [first, last] = lineBounds(value, start, end);
  return value.substring(first, last).split('\n').every((line) => LIST_ITEM.test(line));
}

/**
 * Indents (or, with `outdent`, unindents) every line the selection touches
 * by one INDENT. Returns the new text and the moved selection.
 */
export function indentLines(value, start, end, outdent = false) {
  const [first, last] = lineBounds(value, start, end);
  const lines = value.substring(first, last).split('\n');
  const deltas = lines.map((line) => {
    if (!outdent) return INDENT.length;
    const lead = /^ */.exec(line)[0].length;
    return -Math.min(lead, INDENT.length);
  });
  const changed = lines.map((line, i) => (deltas[i] > 0 ? INDENT + line : line.slice(-deltas[i])));
  const total = deltas.reduce((sum, d) => sum + d, 0);
  const newStart = Math.max(first, start + deltas[0]);
  return {
    value: value.substring(0, first) + changed.join('\n') + value.substring(last),
    start: newStart,
    end: Math.max(newStart, end + total),
  };
}
