// FE-015: the colour tokens of src/index.css meet WCAG 2.2 AA in both modes.
// The test reads the light values from the @theme block and the dark ones
// from the `:root.dark` block, so a token edited in the CSS is measured here
// — and docs/PANEL.md's table is regenerated from the same numbers.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const css = readFileSync(join(dirname(fileURLToPath(import.meta.url)), 'index.css'), 'utf8');

/** The `--color-*: #rrggbb` declarations inside the block that starts at `marker`. */
function tokens(marker) {
  const start = css.indexOf(marker);
  assert.ok(start >= 0, `index.css has no ${marker} block`);
  const block = css.slice(start, css.indexOf('\n}', start));
  return Object.fromEntries([...block.matchAll(/--color-([\w-]+):\s*(#[0-9a-f]{6})/gi)].map((m) => [m[1], m[2]]));
}

const light = tokens('@theme static {');
const dark = { ...light, ...tokens(':root.dark {') };

/** Relative luminance of a #rrggbb colour (WCAG 2.2). */
function luminance(hex) {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

/** The WCAG contrast ratio of two #rrggbb colours. */
function contrast(a, b) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

const textTokens = ['fg', 'fg-soft', 'fg-muted', 'fg-subtle', 'fg-faint'];
const surfaces = ['surface', 'surface-muted', 'surface-subtle', 'surface-strong'];
const roles = ['danger', 'success', 'warning', 'info', 'accent', 'note'];

/** Every [foreground, background, minimum] pair the panel puts on screen. */
const pairs = [
  ...textTokens.flatMap((fg) => surfaces.map((bg) => [fg, bg, 4.5])),
  ...roles.flatMap((role) => [role, `${role}-strong`].flatMap((fg) =>
    ['surface', 'surface-muted', 'surface-subtle', `${role}-soft`, `${role}-muted`].map((bg) => [fg, bg, 4.5]))),
  // Input, button and checkbox borders, and the focus ring: SC 1.4.11.
  ...['surface', 'surface-muted'].map((bg) => ['line-strong', bg, 3]),
  ...['surface', 'surface-muted', 'surface-subtle'].map((bg) => ['focus', bg, 3]),
];

for (const [mode, palette] of [['light', light], ['dark', dark]]) {
  test(`${mode}: every token pair meets WCAG 2.2 AA`, () => {
    const failing = pairs
      .map(([fg, bg, min]) => ({ fg, bg, min, ratio: contrast(palette[fg], palette[bg]) }))
      .filter(({ ratio, min }) => !(ratio >= min))
      .map(({ fg, bg, ratio, min }) => `${fg} on ${bg}: ${ratio.toFixed(2)} < ${min}`);
    assert.deepEqual(failing, []);
  });
}

test('the dark block redefines every light token but the primary scale', () => {
  const own = tokens(':root.dark {');
  const missing = Object.keys(light).filter((name) => !name.startsWith('primary-') && !(name in own));
  assert.deepEqual(missing, []);
});

test('white text on the solid buttons reaches 4.5:1', () => {
  // Buttons keep their palette colour in both modes, with white text.
  assert.ok(contrast('#ffffff', light['primary-600']) >= 4.5);
  assert.ok(contrast('#ffffff', '#0369a1') >= 4.5); // primary-700
});

test('contrast is symmetric and spans 1 to 21', () => {
  assert.equal(contrast('#000000', '#ffffff'), 21);
  assert.equal(contrast('#ffffff', '#000000'), 21);
  assert.equal(contrast('#777777', '#777777'), 1);
});

if (process.env.PRINT_CONTRAST) {
  for (const [fg, bg] of pairs) {
    console.log(`${fg} on ${bg}: light ${contrast(light[fg], light[bg]).toFixed(1)} dark ${contrast(dark[fg], dark[bg]).toFixed(1)}`);
  }
}
