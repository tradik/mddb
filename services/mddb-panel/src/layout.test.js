// FE-021: one scroll. The rules that keep the document itself from ever
// scrolling: a shell exactly the visible window high that clips, a <body>
// that does not grow past it, and page panes that scroll their own page.
// jsdom computes no Tailwind layout, so this reads the rules where they live;
// docs/PANEL.md ("One scroll") says how the result was measured in a browser.
import { describe, test, expect } from 'vitest';
import { readFileSync } from 'node:fs';

const read = (path) => readFileSync(new URL(path, import.meta.url), 'utf8');
const css = read('./index.css');
const app = read('./App.jsx');

/** The declarations of the first `selector { … }` rule in index.css. */
function rule(selector) {
  const match = css.match(new RegExp(`(^|\\n)${selector.replace('#', '\\#')} \\{([^}]*)\\}`));
  return match ? match[2] : '';
}

/** The className of the first element whose className contains `marker`. */
function classesWith(marker) {
  return app.match(new RegExp(`className="([^"]*${marker.replace(/[*:]/g, '\\$&')}[^"]*)"`))?.[1] ?? '';
}

describe('one scroll', () => {
  test('<body> and #root never grow past the window', () => {
    const body = rule('body');
    expect(body).toContain('margin: 0');
    expect(body).not.toMatch(/min-height|height:\s*100vh|display:\s*flex/);
    expect(rule('#root')).toContain('min-width: 0');
  });

  test('the shell is the visible window high and clips instead of scrolling', () => {
    const shell = classesWith('h-dvh').split(' ');
    expect(shell).toEqual(expect.arrayContaining(['h-dvh', 'flex', 'flex-col', 'overflow-hidden']));
    expect(app.match(/h-dvh|h-screen|100vh/g)).toEqual(['h-dvh']);
  });

  test('each page pane of <main> is the one scroll container of its page', () => {
    const main = classesWith('*:overflow-y-auto').split(' ');
    expect(app).toMatch(/<main className="[^"]*\*:overflow-y-auto/);
    expect(main).toEqual(expect.arrayContaining(['flex-1', 'min-w-0', 'overflow-hidden', '*:min-h-0', '*:overflow-y-auto']));
  });

  test('on a phone the sidebar overlays the page instead of squeezing it', () => {
    expect(classesWith('max-md:absolute').split(' ')).toEqual(expect.arrayContaining(['max-md:absolute', 'max-md:z-30']));
    expect(classesWith('cursor-col-resize')).toContain('max-md:hidden');
  });
});
