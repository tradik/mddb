// @vitest-environment jsdom
// GlobalShortcuts and ShortcutsDialog (FE-016): `/` focuses the menu search,
// `?` opens the shortcut list; neither fires inside a form field. The dialog
// takes the focus, keeps Tab on Close, closes on Escape or Close and gives
// the focus back.
import { describe, test, expect, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';
import GlobalShortcuts from './GlobalShortcuts';

afterEach(cleanup);

const press = (key, target = document.body, extra = {}) => {
  const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...extra });
  act(() => { target.dispatchEvent(event); });
  return event;
};

describe('GlobalShortcuts', () => {
  test('`/` focuses the menu search', () => {
    render(
      <>
        <GlobalShortcuts />
        <input type="search" aria-label="Search the menu" data-shortcut="search" />
      </>,
    );
    const event = press('/');
    expect(event.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(screen.getByRole('searchbox'));
  });

  test('`/` with no search box on the page types as usual', () => {
    render(<GlobalShortcuts />);
    expect(press('/').defaultPrevented).toBe(false);
  });

  test('keys typed into a form field are left alone', () => {
    render(
      <>
        <GlobalShortcuts />
        <textarea aria-label="notes" />
      </>,
    );
    expect(press('?', screen.getByRole('textbox')).defaultPrevented).toBe(false);
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(press('x').defaultPrevented).toBe(false);
  });

  test('`?` opens the list; Escape closes it and the focus goes back', () => {
    render(
      <>
        <GlobalShortcuts />
        <button type="button">opener</button>
      </>,
    );
    const opener = screen.getByRole('button', { name: 'opener' });
    opener.focus();
    expect(press('?', opener, { shiftKey: true }).defaultPrevented).toBe(true);
    const dialog = screen.getByRole('dialog', { name: 'Keyboard shortcuts' });
    expect(dialog.textContent).toContain('Ctrl+B');
    expect(dialog.textContent).toContain('Focus the menu search');
    const close = screen.getByRole('button', { name: 'Close' });
    expect(document.activeElement).toBe(close);
    const tab = press('Tab', close);
    expect(tab.defaultPrevented).toBe(true);
    fireEvent.keyDown(close, { key: 'a' });
    fireEvent.keyDown(close, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(document.activeElement).toBe(opener);
  });

  test('Close closes the list', () => {
    render(<GlobalShortcuts />);
    press('?');
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});
