// @vitest-environment jsdom
// FeatureRow (FE-021): the server's plain title and summary on the card, the
// technical detail behind the help control; an older server's description
// stands in for the summary.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';
import FeatureRow from './FeatureRow';

afterEach(cleanup);

const webSearch = {
  name: 'webSearch', title: 'Web search endpoint', summary: 'Lets a chat front end search your collections.',
  description: 'POST /v1/web-search searches the readable collections.', enabled: false, configured: false,
  default: false, requires: ['chat'],
};

function renderRow(feature) {
  const onToggle = vi.fn();
  render(<ul><FeatureRow feature={feature} busy={false} onToggle={onToggle} /></ul>);
  return onToggle;
}

describe('FeatureRow', () => {
  test('shows the title and summary; the help holds the detail, the setting name, the default and the docs', () => {
    const onToggle = renderRow(webSearch);
    expect(screen.getByRole('switch', { name: 'Web search endpoint' }).checked).toBe(false);
    expect(screen.getByText('Lets a chat front end search your collections.')).toBeTruthy();
    const help = screen.getByRole('button', { name: 'About Web search endpoint' });
    const panel = document.getElementById(help.getAttribute('aria-controls'));
    expect(panel.hidden).toBe(true);
    fireEvent.click(help);
    expect(panel.textContent).toContain('POST /v1/web-search searches the readable collections.');
    expect(panel.textContent).toContain('Setting name webSearch. A new server starts with it off.');
    expect(panel.textContent).toContain('Needs Chat assistants.');
    const docs = screen.getByRole('link', { name: 'About this module in the docs' });
    expect(docs.getAttribute('href')).toContain('docs/FEATURES.md#optional-modules');
    fireEvent.click(screen.getByRole('switch', { name: 'Web search endpoint' }));
    expect(onToggle).toHaveBeenCalledWith('webSearch', true);
  });

  test("an older server's description stands in for the summary, and the panel's name for the title", () => {
    renderRow({ name: 'geo', description: 'Geo search: radius and polygons', enabled: true, default: true });
    expect(screen.getByRole('switch', { name: 'Geo search' }).checked).toBe(true);
    expect(screen.getByText('Geo search: radius and polygons')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'About Geo search' }));
    expect(screen.queryByText('Needs', { exact: false })).toBeNull();
    expect(screen.getByText('Setting name', { exact: false }).textContent).toContain('starts with it on');
  });

  test('a module with no text at all still shows its switch', () => {
    renderRow({ name: 'futureThing', enabled: false });
    expect(screen.getByRole('switch', { name: 'futureThing' })).toBeTruthy();
    expect(document.getElementById('feature-futureThing-desc').textContent).toBe('');
  });
});
