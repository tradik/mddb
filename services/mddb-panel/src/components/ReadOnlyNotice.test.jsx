// @vitest-environment jsdom
// ReadOnlyNotice: the AGT-005 note on a corpus-pack collection — nothing for a
// writable one, the pack's name, version and license when known, the
// compact padding used in the document list, and (FE-022) the link to the
// pack on the Corpus packs page.
import { describe, test, expect, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';

const { default: ReadOnlyNotice } = await import('./ReadOnlyNotice');
const { useStore } = await import('../lib/store');
const { useCorpusIntent } = await import('../lib/corpus-intent');

afterEach(cleanup);

const tail = 'Writes to its documents are refused; to write to this name, remove the pack on the Corpus packs page.';

describe('ReadOnlyNotice', () => {
  test.each([
    ['no config', undefined],
    ['a writable config', { readOnly: false }],
  ])('renders nothing for %s', (_label, config) => {
    const { container } = render(<ReadOnlyNotice config={config} />);
    expect(container.innerHTML).toBe('');
  });

  test.each([
    ['without corpus info', { readOnly: true }, `Read-only. ${tail}`],
    ['with a versioned pack', { readOnly: true, corpus: { name: 'gdpr', version: '1.2.0', license: 'CC-BY-4.0' } },
      `Read-only — installed from corpus pack gdpr v1.2.0 (CC-BY-4.0). ${tail}`],
    ['with an unversioned pack', { readOnly: true, corpus: { name: 'rfc', license: 'MIT' } },
      `Read-only — installed from corpus pack rfc (MIT). ${tail}`],
  ])('says why writes are refused %s', (_label, config, text) => {
    render(<ReadOnlyNotice config={config} />);
    const note = screen.getByRole('note');
    expect(note.textContent).toBe(text);
    expect(note.className).toContain('p-3');
  });

  test.each([
    [{ readOnly: true, corpus: { name: 'gdpr', license: 'MIT' } }, { pack: 'gdpr' }],
    [{ readOnly: true }, {}],
  ])('the link opens the Corpus packs page at the pack (%o)', (config, intent) => {
    render(<ReadOnlyNotice config={config} />);
    fireEvent.click(screen.getByRole('button', { name: 'Corpus packs' }));
    expect(useStore.getState().viewMode).toBe('corpus');
    expect(useCorpusIntent.getState().intent).toEqual(intent);
  });

  test('compact uses the smaller padding', () => {
    render(<ReadOnlyNotice config={{ readOnly: true }} compact />);
    const note = screen.getByRole('note');
    expect(note.className).toContain('p-2');
    expect(note.className).not.toContain('p-3');
  });
});
