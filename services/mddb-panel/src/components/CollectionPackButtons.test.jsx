// @vitest-environment jsdom
// CollectionPackButtons (FE-022): the collection list's ways into the Corpus
// packs page — the header button, "Create pack from this collection", and
// the read-only label of a collection installed from a pack.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';

const { useStore } = await import('../lib/store');
const { useCorpusIntent } = await import('../lib/corpus-intent');
const { CorpusPacksButton, CollectionPackButton } = await import('./CollectionPackButtons');

const initialStore = useStore.getState();
const row = vi.fn();

beforeEach(() => {
  useStore.setState(initialStore, true);
  useCorpusIntent.setState({ intent: null });
  row.mockReset();
});
afterEach(cleanup);

describe('CollectionPackButtons', () => {
  test('the header button opens the page', () => {
    render(<CorpusPacksButton />);
    fireEvent.click(screen.getByRole('button', { name: 'Corpus packs: install or create a pack' }));
    expect(useStore.getState().viewMode).toBe('corpus');
    expect(useCorpusIntent.getState().intent).toEqual({});
  });

  test('a written collection offers a pack of itself, without opening the row', () => {
    render(<div onClick={row}><CollectionPackButton collection="docs" /></div>);
    fireEvent.click(screen.getByRole('button', { name: 'Create pack from docs' }));
    expect(useCorpusIntent.getState().intent).toEqual({ build: ['docs'] });
    expect(row).not.toHaveBeenCalled();
  });

  test.each([
    [{ name: 'kb', version: '2' }, 'From pack kb v2, read-only'],
    [{ name: 'rfc' }, 'From pack rfc, read-only'],
  ])('a collection from a pack says so and opens it (%o)', (corpus, label) => {
    useStore.setState({ collectionConfigs: { docs: { readOnly: true, corpus } } });
    render(<div onClick={row}><CollectionPackButton collection="docs" /></div>);
    fireEvent.click(screen.getByRole('button', { name: label }));
    expect(useCorpusIntent.getState().intent).toEqual({ pack: corpus.name });
    expect(row).not.toHaveBeenCalled();
  });
});
