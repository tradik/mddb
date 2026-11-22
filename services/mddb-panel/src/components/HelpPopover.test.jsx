// @vitest-environment jsdom
// HelpPopover (FE-021): the panel's one help control.
import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';
import HelpPopover from './HelpPopover';

afterEach(cleanup);

function setup() {
  render(
    <div>
      <HelpPopover label="Geo search">
        <p>Radius and polygon queries.</p>
        <a href="https://example.test/docs">Docs</a>
      </HelpPopover>
      <button type="button">Elsewhere</button>
    </div>,
  );
  const trigger = screen.getByRole('button', { name: 'About Geo search' });
  const panel = document.getElementById(trigger.getAttribute('aria-controls'));
  return { trigger, panel };
}

describe('HelpPopover', () => {
  it('starts closed and names what it explains', () => {
    const { trigger, panel } = setup();
    expect(trigger.getAttribute('aria-expanded')).toBe('false');
    expect(trigger.getAttribute('title')).toBe('About Geo search');
    expect(panel.hidden).toBe(true);
  });

  it('opens and closes on its button', () => {
    const { trigger, panel } = setup();
    fireEvent.click(trigger);
    expect(trigger.getAttribute('aria-expanded')).toBe('true');
    expect(panel.hidden).toBe(false);
    expect(panel.textContent).toContain('Radius and polygon queries.');
    fireEvent.click(trigger);
    expect(panel.hidden).toBe(true);
  });

  it('closes on Escape and gives focus back to the button', () => {
    const { trigger, panel } = setup();
    fireEvent.click(trigger);
    screen.getByRole('link', { name: 'Docs' }).focus();
    fireEvent.keyDown(document, { key: 'Enter' });
    expect(panel.hidden).toBe(false);
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(panel.hidden).toBe(true);
    expect(document.activeElement).toBe(trigger);
  });

  it('closes on a click outside, not on one inside', () => {
    const { trigger, panel } = setup();
    fireEvent.click(trigger);
    fireEvent.mouseDown(panel);
    expect(panel.hidden).toBe(false);
    fireEvent.mouseDown(screen.getByRole('button', { name: 'Elsewhere' }));
    expect(panel.hidden).toBe(true);
  });

  it('closes when focus leaves it, not when it moves inside', () => {
    const { trigger, panel } = setup();
    fireEvent.click(trigger);
    fireEvent.blur(trigger, { relatedTarget: screen.getByRole('link', { name: 'Docs' }) });
    expect(panel.hidden).toBe(false);
    fireEvent.blur(trigger, { relatedTarget: null });
    expect(panel.hidden).toBe(false);
    fireEvent.blur(trigger, { relatedTarget: screen.getByRole('button', { name: 'Elsewhere' }) });
    expect(panel.hidden).toBe(true);
  });

  it('stops listening once closed', () => {
    const { trigger, panel } = setup();
    fireEvent.click(trigger);
    fireEvent.click(trigger);
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(panel.hidden).toBe(true);
    expect(document.activeElement).not.toBe(trigger);
  });
});
