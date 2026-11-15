// @vitest-environment jsdom
// MarkdownToolbar: one button per format action of markdown-format.js, each
// calling onFormat with its id and naming its shortcut in the tooltip
// (Ctrl or Cmd per platform, FE-016); dividers between groups; the
// templates menu only with onTemplate.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import MarkdownToolbar from './MarkdownToolbar';
import { formatActions } from '../lib/markdown-format';

afterEach(cleanup);

describe('MarkdownToolbar', () => {
  test('every action has a button that calls onFormat with its id', () => {
    const onFormat = vi.fn();
    render(<MarkdownToolbar onFormat={onFormat} />);
    for (const action of formatActions) {
      fireEvent.click(screen.getByRole('button', { name: new RegExp(`^${action.label}`) }));
      expect(onFormat).toHaveBeenLastCalledWith(action.id);
    }
    expect(onFormat).toHaveBeenCalledTimes(formatActions.length);
  });

  test('tooltips name the shortcut: Ctrl by default, Cmd on a Mac', () => {
    const { unmount } = render(<MarkdownToolbar />);
    expect(screen.getByTitle('Bold (Ctrl+B)')).toBeTruthy();
    expect(screen.getByTitle('Heading 1')).toBeTruthy();
    // Without onFormat a click does nothing (and does not throw).
    fireEvent.click(screen.getByTitle('Bold (Ctrl+B)'));
    unmount();
    render(<MarkdownToolbar isMac />);
    expect(screen.getByRole('button', { name: 'Link (Cmd+K)' })).toBeTruthy();
  });

  test('a divider separates each group; no templates menu without onTemplate', () => {
    const { container } = render(<MarkdownToolbar />);
    const groups = new Set(formatActions.map((a) => a.group)).size;
    expect(container.querySelectorAll('.w-px').length).toBe(groups - 1);
    expect(screen.queryByTitle('Templates')).toBeNull();
  });

  test('the templates menu calls onTemplate with each template', () => {
    const onTemplate = vi.fn();
    render(<MarkdownToolbar onTemplate={onTemplate} />);
    expect(screen.getByTitle('Templates')).toBeTruthy();
    for (const [label, id] of [['Blog Post', 'blog'], ['Documentation', 'documentation'], ['README', 'readme'], ['API Documentation', 'api'], ['Changelog', 'changelog']]) {
      fireEvent.click(screen.getByRole('button', { name: (name) => name.split(' ').slice(1).join(' ') === label }));
      expect(onTemplate).toHaveBeenLastCalledWith(id);
    }
  });
});
