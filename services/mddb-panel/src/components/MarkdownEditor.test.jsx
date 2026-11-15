// @vitest-environment jsdom
// MarkdownEditor: the edit / split / preview modes, fullscreen, what the
// toolbar inserts at the selection, templates, and the counts line. The
// preview renders through MarkdownView, the component the document viewer
// uses too (2.19.6), so both show the same Markdown styling. FE-016: the
// keyboard shortcuts run the same format actions as the toolbar; Ctrl/Cmd+S
// saves, Tab indents list items only, Esc leaves fullscreen. FE-020: the
// collection spelling check shows only when the editor knows the collection.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('./MarkdownView', () => ({ default: ({ children }) => <div data-testid="preview">{children}</div> }));
// The toolbar's own buttons are tested in MarkdownToolbar.test.jsx: a
// stand-in calls the two callbacks the editor hands it.
vi.mock('./MarkdownToolbar', () => ({
  default: ({ onFormat, onTemplate, isMac }) => (
    <div data-mac={String(isMac)}>
      <button onClick={() => onFormat('bold')}>format bold</button>
      <button onClick={() => onFormat('table')}>format table</button>
      <button onClick={() => onFormat('unknown')}>format unknown</button>
      <button onClick={() => onTemplate('readme')}>template readme</button>
      <button onClick={() => onTemplate('none')}>template none</button>
    </div>
  ),
}));
vi.mock('./CollectionSpelling', () => ({
  default: ({ collection, lang, text }) => <div>spelling {collection}/{lang}: {text}</div>,
}));

const { default: MarkdownEditor } = await import('./MarkdownEditor');
const { getTemplate } = await import('../lib/markdown-templates');
const { findFormatAction } = await import('../lib/markdown-format');

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const textarea = () => screen.getByRole('textbox');
const ctrl = (key, extra = {}) => fireEvent.keyDown(textarea(), { key, ctrlKey: true, ...extra });

describe('MarkdownEditor', () => {
  test('split mode shows the source and its preview with counts', () => {
    render(<MarkdownEditor value={'# Title\nsome words'} onChange={vi.fn()} />);
    expect(textarea().value).toBe('# Title\nsome words');
    expect(textarea().placeholder).toBe('Write your markdown here...');
    expect(screen.getByTestId('preview').textContent).toBe('# Title\nsome words');
    expect(screen.getByText(/18 characters/).textContent).toContain('2 lines');
    expect(screen.getByText(/18 characters/).textContent).toContain('4 words');
    expect(screen.queryByText(/^spelling/)).toBeNull();
  });

  test('edit and preview modes show one side each; an empty preview says so', () => {
    render(<MarkdownEditor value="" placeholder="Body" />);
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    expect(textarea().placeholder).toBe('Body');
    expect(screen.queryByTestId('preview')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Preview' }));
    expect(screen.queryByRole('textbox')).toBeNull();
    expect(screen.getByTestId('preview').textContent).toBe('*Nothing to preview*');
    fireEvent.click(screen.getByRole('button', { name: 'Split' }));
    expect(textarea()).toBeTruthy();
    expect(screen.getByText('0 characters').textContent).toBe('0 characters');
  });

  test('without a value the counts read zero', () => {
    render(<MarkdownEditor />);
    expect(screen.getByText('0 characters')).toBeTruthy();
  });

  test('fullscreen toggles', () => {
    const { container } = render(<MarkdownEditor value="x" />);
    fireEvent.click(screen.getByRole('button', { name: 'Fullscreen' }));
    expect(container.firstChild.className).toContain('fixed inset-0');
    expect(screen.getByTitle('Exit fullscreen (Esc)')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Exit fullscreen' }));
    expect(container.firstChild.className).toContain('relative');
  });

  test('typing reports the new value; without onChange it is ignored', () => {
    const onChange = vi.fn();
    const { unmount } = render(<MarkdownEditor value="a" onChange={onChange} />);
    fireEvent.change(textarea(), { target: { value: 'ab' } });
    expect(onChange).toHaveBeenCalledWith('ab');
    unmount();
    render(<MarkdownEditor value="a" />);
    fireEvent.change(textarea(), { target: { value: 'ab' } });
    expect(textarea().value).toBe('a');
  });

  test('the toolbar wraps the selection, or inserts its placeholder, and moves the cursor', () => {
    vi.useFakeTimers();
    const onChange = vi.fn();
    render(<MarkdownEditor value="hello world" onChange={onChange} />);
    textarea().setSelectionRange(6, 11);
    fireEvent.click(screen.getByRole('button', { name: 'format bold' }));
    expect(onChange).toHaveBeenLastCalledWith('hello **world**');
    const place = vi.spyOn(textarea(), 'setSelectionRange');
    act(() => vi.runAllTimers());
    expect(place).toHaveBeenCalledWith(13, 13);
    place.mockRestore();
    textarea().setSelectionRange(0, 0);
    fireEvent.click(screen.getByRole('button', { name: 'format bold' }));
    expect(onChange).toHaveBeenLastCalledWith('**bold text**hello world');
    fireEvent.click(screen.getByRole('button', { name: 'format table' }));
    expect(onChange).toHaveBeenLastCalledWith(`${findFormatAction('table').before}hello world`);
  });

  test('an unknown action, no onChange, or preview mode change nothing', () => {
    vi.useFakeTimers();
    const onChange = vi.fn();
    const { unmount } = render(<MarkdownEditor value="abc" onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: 'format unknown' }));
    expect(onChange).not.toHaveBeenCalled();
    unmount();
    render(<MarkdownEditor value="abc" />);
    fireEvent.click(screen.getByRole('button', { name: 'format bold' }));
    act(() => vi.runAllTimers());
    expect(textarea().value).toBe('abc');
    fireEvent.click(screen.getByRole('button', { name: 'Preview' }));
    fireEvent.click(screen.getByRole('button', { name: 'format bold' }));
    expect(screen.queryByRole('textbox')).toBeNull();
  });

  test('a template replaces the value; an unknown one, or no onChange, changes nothing', () => {
    const onChange = vi.fn();
    const { unmount } = render(<MarkdownEditor value="x" onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: 'template readme' }));
    expect(onChange).toHaveBeenCalledWith(getTemplate('readme'));
    fireEvent.click(screen.getByRole('button', { name: 'template none' }));
    expect(onChange).toHaveBeenCalledTimes(1);
    unmount();
    render(<MarkdownEditor value="x" />);
    fireEvent.click(screen.getByRole('button', { name: 'template readme' }));
    expect(textarea().value).toBe('x');
  });
});

describe('keyboard shortcuts (FE-016)', () => {
  test('Ctrl+B, Ctrl+I and Ctrl+K format like the toolbar', () => {
    const onChange = vi.fn();
    render(<MarkdownEditor value="word" onChange={onChange} />);
    expect(screen.getByRole('button', { name: 'format bold' }).parentElement.dataset.mac).toBe('false');
    textarea().setSelectionRange(0, 4);
    ctrl('b');
    expect(onChange).toHaveBeenLastCalledWith('**word**');
    ctrl('i');
    expect(onChange).toHaveBeenLastCalledWith('*word*');
    ctrl('k');
    expect(onChange).toHaveBeenLastCalledWith('[word](url)');
  });

  test('Ctrl+S calls onSave and keeps the browser from saving the page', () => {
    const onSave = vi.fn();
    render(<MarkdownEditor value="x" onSave={onSave} />);
    const event = new KeyboardEvent('keydown', { key: 's', ctrlKey: true, bubbles: true, cancelable: true });
    textarea().dispatchEvent(event);
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(event.defaultPrevented).toBe(true);
  });

  test('Ctrl+S without onSave is still taken from the browser', () => {
    render(<MarkdownEditor value="x" />);
    const event = new KeyboardEvent('keydown', { key: 's', ctrlKey: true, bubbles: true, cancelable: true });
    textarea().dispatchEvent(event);
    expect(event.defaultPrevented).toBe(true);
  });

  test('other keys type as usual', () => {
    const onChange = vi.fn();
    render(<MarkdownEditor value="x" onChange={onChange} />);
    const event = new KeyboardEvent('keydown', { key: 'a', bubbles: true, cancelable: true });
    textarea().dispatchEvent(event);
    expect(event.defaultPrevented).toBe(false);
    expect(onChange).not.toHaveBeenCalled();
  });

  test('Tab and Shift+Tab indent list items and keep the selection', () => {
    vi.useFakeTimers();
    const onChange = vi.fn();
    const { rerender } = render(<MarkdownEditor value="- a" onChange={onChange} />);
    textarea().setSelectionRange(3, 3);
    fireEvent.keyDown(textarea(), { key: 'Tab' });
    expect(onChange).toHaveBeenLastCalledWith('  - a');
    rerender(<MarkdownEditor value="  - a" onChange={onChange} />);
    const place = vi.spyOn(textarea(), 'setSelectionRange');
    act(() => vi.runAllTimers());
    expect(place).toHaveBeenCalledWith(5, 5);
    textarea().setSelectionRange(5, 5);
    fireEvent.keyDown(textarea(), { key: 'Tab', shiftKey: true });
    expect(onChange).toHaveBeenLastCalledWith('- a');
  });

  test('outside a list Tab moves the focus as usual (no keyboard trap)', () => {
    const onChange = vi.fn();
    render(<MarkdownEditor value="plain" onChange={onChange} />);
    const event = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
    textarea().dispatchEvent(event);
    expect(event.defaultPrevented).toBe(false);
    expect(onChange).not.toHaveBeenCalled();
  });

  test('Esc leaves fullscreen; outside fullscreen it is left alone', () => {
    const { container } = render(<MarkdownEditor value="x" />);
    const outside = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    textarea().dispatchEvent(outside);
    expect(outside.defaultPrevented).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Fullscreen' }));
    fireEvent.keyDown(textarea(), { key: 'Escape' });
    expect(container.firstChild.className).toContain('relative');
  });
});

describe('collection spelling (FE-020)', () => {
  test('shown with the collection and language when given', () => {
    render(<MarkdownEditor value="Teh text" spelling={{ collection: 'blog', lang: 'en' }} />);
    expect(screen.getByText('spelling blog/en: Teh text')).toBeTruthy();
  });

  test('an empty editor checks empty text', () => {
    render(<MarkdownEditor spelling={{ collection: 'blog', lang: 'en' }} />);
    expect(screen.getByText(/^spelling blog\/en:/).textContent).toBe('spelling blog/en: ');
  });
});
