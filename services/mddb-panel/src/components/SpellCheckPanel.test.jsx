// @vitest-environment jsdom
// Spell Checker: suggestions for typed text, cleaning it up
// (/v1/spell-cleanup), and the custom dictionary (load, add, remove), with
// errors as the server words them.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { spellSuggest: vi.fn(), spellCleanup: vi.fn(), spellDictionary: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: SpellCheckPanel } = await import('./SpellCheckPanel');

const initialStore = useStore.getState();
const apiError = (status, text) => Object.assign(new Error(`API Error (${status}): ${text}`), { status, body: null });
const click = (name) => act(async () => fireEvent.click(screen.getByRole('button', { name })));
const typeText = (value) => fireEvent.change(screen.getByPlaceholderText(/Type text with typos/), { target: { value } });

beforeEach(() => {
  useStore.setState(initialStore, true);
  for (const fn of Object.values(mddbClient)) fn.mockReset();
});
afterEach(cleanup);

describe('SpellCheckPanel test tab', () => {
  test('suggests corrections with Ctrl+Enter or the button, and says when none are needed', async () => {
    useStore.setState({ currentCollection: 'kb' });
    mddbClient.spellSuggest
      .mockResolvedValueOnce({ originalText: 'teh db', suggestedText: 'the db', tokenSuggestions: [{ original: 'teh', corrected: 'the', confidence: 0.91 }] })
      .mockResolvedValueOnce({ originalText: 'ok', suggestedText: 'ok' })
      .mockRejectedValueOnce(apiError(503, 'spell index still loading'));
    render(<SpellCheckPanel />);
    expect(screen.getByText('· kb')).toBeTruthy();
    const box = screen.getByPlaceholderText(/Type text with typos/);
    fireEvent.keyDown(box, { key: 'Enter', ctrlKey: true });
    expect(mddbClient.spellSuggest).not.toHaveBeenCalled();
    typeText('teh db');
    fireEvent.change(screen.getAllByRole('combobox')[0], { target: { value: 'pl' } });
    await act(async () => fireEvent.keyDown(box, { key: 'Enter', ctrlKey: true }));
    expect(mddbClient.spellSuggest).toHaveBeenCalledWith({ collection: 'kb', text: 'teh db', lang: 'pl', maxSuggestions: 10 });
    expect(screen.getByText('91% confidence')).toBeTruthy();
    fireEvent.keyDown(box, { key: 'Enter' });
    await click('Check Spelling');
    expect(screen.getByText(/No corrections needed/)).toBeTruthy();
    await click('Check Spelling');
    expect(screen.getByText('API Error (503): spell index still loading')).toBeTruthy();
  });

  test('cleans up the text, or explains a server without spell correction', async () => {
    mddbClient.spellCleanup
      .mockResolvedValueOnce({ original: 'teh', cleaned: 'the', correctionsApplied: 1 })
      .mockRejectedValueOnce(apiError(404, '404 page not found'))
      .mockRejectedValueOnce(apiError(400, 'lang is required'));
    render(<SpellCheckPanel />);
    expect(screen.getByRole('button', { name: 'Clean up text' }).disabled).toBe(true);
    typeText('teh');
    await click('Clean up text');
    expect(mddbClient.spellCleanup).toHaveBeenCalledWith({ collection: '', text: 'teh', lang: 'en' });
    expect(screen.getByRole('status').textContent).toBe('Cleaned text (1 corrections)the');
    await click('Clean up text');
    expect(screen.getByRole('alert').textContent).toBe('Spell correction is off on this server: start it with MDDB_SPELL=true.');
    await click('Clean up text');
    expect(screen.getByRole('alert').textContent).toBe('API Error (400): lang is required');
    mddbClient.spellSuggest.mockResolvedValueOnce({ originalText: 'teh', suggestedText: 'the', tokenSuggestions: [] });
    await click('Check Spelling');
    expect(mddbClient.spellSuggest.mock.lastCall[0].collection).toBe('');
  });
});

describe('SpellCheckPanel dictionary tab', () => {
  test('loads, adds and removes words, scoped to the collection', async () => {
    useStore.setState({ currentCollection: 'kb' });
    mddbClient.spellDictionary.mockImplementation(async (method) => (method === 'GET' ? { words: ['mddb'] } : {}));
    render(<SpellCheckPanel />);
    await click('Custom Dictionary');
    expect(mddbClient.spellDictionary).toHaveBeenCalledWith('GET', { collection: 'kb', lang: 'en' });
    expect(screen.getByText('mddb')).toBeTruthy();
    expect(screen.getByText(/Scoped to collection "kb"/)).toBeTruthy();

    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'de' } });
    await click('Load');
    expect(mddbClient.spellDictionary).toHaveBeenLastCalledWith('GET', { collection: 'kb', lang: 'de' });

    const input = screen.getByPlaceholderText('Add a custom word...');
    await act(async () => fireEvent.keyDown(input, { key: 'Enter' }));
    fireEvent.change(input, { target: { value: ' bbolt ' } });
    fireEvent.keyDown(input, { key: 'a' });
    await act(async () => fireEvent.keyDown(input, { key: 'Enter' }));
    expect(mddbClient.spellDictionary).toHaveBeenCalledWith('PUT', { collection: 'kb', lang: 'de', words: ['bbolt'] });
    expect(input.value).toBe('');

    await act(async () => fireEvent.click(screen.getByTitle('Remove word')));
    expect(mddbClient.spellDictionary).toHaveBeenCalledWith('DELETE', { collection: 'kb', lang: 'de', words: ['mddb'] });
    expect(screen.getByText(/No custom words yet/)).toBeTruthy();
    await click('Test Spell Checker');
    expect(screen.getByRole('button', { name: 'Check Spelling' })).toBeTruthy();
  });

  test('global scope, a pending load and failures', async () => {
    mddbClient.spellDictionary
      .mockResolvedValueOnce({})
      .mockRejectedValueOnce(new Error('API Error (400): bad word'))
      .mockResolvedValueOnce({ words: ['x'] })
      .mockRejectedValueOnce(new Error('API Error (500): cannot remove'))
      .mockRejectedValueOnce(new Error('API Error (500): cannot load'))
      .mockReturnValueOnce(new Promise(() => {}));
    render(<SpellCheckPanel />);
    await click('Custom Dictionary');
    expect(screen.getByText(/Global \(all collections\)/)).toBeTruthy();
    fireEvent.change(screen.getByPlaceholderText('Add a custom word...'), { target: { value: 'w' } });
    await click('Add');
    expect(screen.getByText('API Error (400): bad word')).toBeTruthy();
    await click('Load');
    await act(async () => fireEvent.click(screen.getByTitle('Remove word')));
    expect(screen.getByText('API Error (500): cannot remove')).toBeTruthy();
    await click('Load');
    expect(screen.getByText('API Error (500): cannot load')).toBeTruthy();
    await click('Load');
    expect(document.querySelector('.animate-spin')).not.toBeNull();
  });
});
