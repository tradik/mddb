// @vitest-environment jsdom
// MarkdownEditor: the edit / split / preview modes, fullscreen, what the
// toolbar inserts at the selection, templates, and the counts line. The
// preview renders through MarkdownView, the component the document viewer
// uses too (2.19.6), so both show the same Markdown styling.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('./MarkdownView', () => ({ default: ({ children }) => <div data-testid="preview">{children}</div> }));
// The toolbar's own buttons are not under test: a stand-in calls the two
// callbacks the editor hands it.
vi.mock('./MarkdownToolbar', () => ({
  default: ({ onInsert, onTemplate }) => (
    <div>
      <button onClick={() => onInsert('**', '**', 'bold text')}>insert bold</button>
      <button onClick={() => onInsert('---')}>insert rule</button>
      <button onClick={() => onTemplate('readme')}>template readme</button>
      <button onClick={() => onTemplate('none')}>template none</button>
    </div>
  ),
}));

const { default: MarkdownEditor } = await import('./MarkdownEditor');
const { getTemplate } = await import('../lib/markdown-templates');

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const textarea = () => screen.getByRole('textbox');

describe('MarkdownEditor', () => {
  test('split mode shows the source and its preview with counts', () => {
    render(<MarkdownEditor value={'# Title\nsome words'} onChange={vi.fn()} />);
    expect(textarea().value).toBe('# Title\nsome words');
    expect(textarea().placeholder).toBe('Write your markdown here...');
    expect(screen.getByTestId('preview').textContent).toBe('# Title\nsome words');
    expect(screen.getByText(/18 characters/).textContent).toContain('2 lines');
    expect(screen.getByText(/18 characters/).textContent).toContain('4 words');
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
    fireEvent.click(screen.getByTitle('Fullscreen'));
    expect(container.firstChild.className).toContain('fixed inset-0');
    fireEvent.click(screen.getByTitle('Exit fullscreen'));
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
    fireEvent.click(screen.getByRole('button', { name: 'insert bold' }));
    expect(onChange).toHaveBeenLastCalledWith('hello **world**');
    const place = vi.spyOn(textarea(), 'setSelectionRange');
    act(() => vi.runAllTimers());
    expect(place).toHaveBeenCalledWith(13, 13);
    place.mockRestore();
    textarea().setSelectionRange(0, 0);
    fireEvent.click(screen.getByRole('button', { name: 'insert bold' }));
    expect(onChange).toHaveBeenLastCalledWith('**bold text**hello world');
    fireEvent.click(screen.getByRole('button', { name: 'insert rule' }));
    expect(onChange).toHaveBeenLastCalledWith('---hello world');
  });

  test('inserting without onChange only moves the cursor; in preview mode it does nothing', () => {
    vi.useFakeTimers();
    render(<MarkdownEditor value="abc" />);
    fireEvent.click(screen.getByRole('button', { name: 'insert rule' }));
    act(() => vi.runAllTimers());
    expect(textarea().value).toBe('abc');
    fireEvent.click(screen.getByRole('button', { name: 'Preview' }));
    fireEvent.click(screen.getByRole('button', { name: 'insert rule' }));
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
