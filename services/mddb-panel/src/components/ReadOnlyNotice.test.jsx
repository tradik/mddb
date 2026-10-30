// @vitest-environment jsdom
// ReadOnlyNotice: the AGT-005 note on a corpus-pack collection — nothing for a
// writable one, the pack's name, version and license when known, and the
// compact padding used in the document list.
import { describe, test, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';

const { default: ReadOnlyNotice } = await import('./ReadOnlyNotice');

afterEach(cleanup);

describe('ReadOnlyNotice', () => {
  test.each([
    ['no config', undefined],
    ['a writable config', { readOnly: false }],
  ])('renders nothing for %s', (_label, config) => {
    const { container } = render(<ReadOnlyNotice config={config} />);
    expect(container.innerHTML).toBe('');
  });

  test.each([
    ['without corpus info', { readOnly: true }, 'Read-only. Writes to its documents are refused; remove the pack on the System page to write to this name.'],
    ['with a versioned pack', { readOnly: true, corpus: { name: 'gdpr', version: '1.2.0', license: 'CC-BY-4.0' } },
      'Read-only — installed from corpus pack gdpr v1.2.0 (CC-BY-4.0). Writes to its documents are refused; remove the pack on the System page to write to this name.'],
    ['with an unversioned pack', { readOnly: true, corpus: { name: 'rfc', license: 'MIT' } },
      'Read-only — installed from corpus pack rfc (MIT). Writes to its documents are refused; remove the pack on the System page to write to this name.'],
  ])('says why writes are refused %s', (_label, config, text) => {
    render(<ReadOnlyNotice config={config} />);
    const note = screen.getByRole('note');
    expect(note.textContent).toBe(text);
    expect(note.className).toContain('p-3');
  });

  test('compact uses the smaller padding', () => {
    render(<ReadOnlyNotice config={{ readOnly: true }} compact />);
    const note = screen.getByRole('note');
    expect(note.className).toContain('p-2');
    expect(note.className).not.toContain('p-3');
  });
});
