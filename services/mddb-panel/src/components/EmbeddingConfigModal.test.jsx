// @vitest-environment jsdom
// EmbeddingConfigModal (#415): switching the provider replaces the previous
// provider's defaults, keeps typed values, and saves what the form shows.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';
import EmbeddingConfigModal from './EmbeddingConfigModal';

afterEach(cleanup);

const value = (label) => screen.getByLabelText(label).value;
const type = (label, text) => fireEvent.change(screen.getByLabelText(label), { target: { value: text } });

function renderNew(onSave = vi.fn().mockResolvedValue(undefined), onClose = vi.fn()) {
  render(<EmbeddingConfigModal onSave={onSave} onClose={onClose} />);
  return { onSave, onClose };
}

describe('a new configuration', () => {
  test('starts as OpenAI and switching to Ollama does not keep the OpenAI model (the #415 bug)', () => {
    renderNew();
    expect([value(/^Model/), value(/^Dimensions/), value(/^API URL/)]).toEqual(['text-embedding-3-small', '1536', '']);
    expect(screen.getByLabelText(/^API Key/)).toBeTruthy();
    type(/^Provider/, 'ollama');
    expect([value(/^Model/), value(/^Dimensions/), value(/^API URL/)]).toEqual(['nomic-embed-text', '768', 'http://localhost:11434']);
    expect(screen.queryByLabelText(/^API Key/)).toBeNull();
    expect(screen.getByText(/not localhost when mddbd runs in a container/)).toBeTruthy();
  });

  test('a model the user typed survives a provider change', () => {
    renderNew();
    type(/^Provider/, 'ollama');
    type(/^Model/, 'mxbai-embed-large');
    type(/^Provider/, 'cohere');
    expect([value(/^Model/), value(/^Dimensions/), value(/^API URL/)]).toEqual(['mxbai-embed-large', '1024', 'https://api.cohere.ai/v1']);
  });

  test('lexical has no key and no URL and explains itself', () => {
    renderNew();
    type(/^Provider/, 'lexical');
    expect(screen.queryByLabelText(/^API Key/)).toBeNull();
    expect(screen.queryByLabelText(/^API URL/)).toBeNull();
    expect(screen.getByRole('note').textContent).toContain('not meaning');
  });

  test('create sends the fields, the number of dimensions and the key only for a hosted provider', async () => {
    const { onSave } = renderNew();
    type(/^ID/, 'ollama-nomic');
    type(/^Name/, 'Ollama Nomic');
    type(/^API Key/, 'sk-secret');
    type(/^Provider/, 'ollama');
    fireEvent.click(screen.getByLabelText('Set as default embedding model'));
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0]).toEqual({
      id: 'ollama-nomic', name: 'Ollama Nomic', provider: 'ollama', model: 'nomic-embed-text', dimensions: 768,
      apiKey: '', apiUrl: 'http://localhost:11434', isDefault: true,
    });
  });

  test('a failed save shows the error and enables the form again; Cancel and Close call onClose', async () => {
    const onSave = vi.fn().mockRejectedValue(new Error('provider unreachable'));
    const { onClose } = renderNew(onSave);
    type(/^ID/, 'a');
    type(/^Name/, 'A');
    type(/^API Key/, 'k');
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    expect((await screen.findByRole('alert')).textContent).toBe('provider unreachable');
    expect(screen.getByRole('button', { name: 'Create' }).disabled).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  test('the button says Saving while the save runs', async () => {
    let finish;
    const onSave = vi.fn(() => new Promise((resolve) => { finish = resolve; }));
    renderNew(onSave);
    type(/^ID/, 'a');
    type(/^Name/, 'A');
    type(/^API Key/, 'k');
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    expect(await screen.findByRole('button', { name: 'Saving...' })).toBeTruthy();
    finish();
  });
});

describe('an existing configuration', () => {
  test('shows its values, locks the provider, has no ID field and updates without an id', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<EmbeddingConfigModal onSave={onSave} onClose={vi.fn()} config={{
      id: 'v', name: 'Voyage', provider: 'voyage', model: 'voyage-3-large', dimensions: 2048, apiKey: 'pa-1', apiUrl: '', isDefault: false,
    }} />);
    expect(screen.getByRole('heading').textContent).toBe('Edit Embedding Model');
    expect(screen.queryByLabelText(/^ID/)).toBeNull();
    expect(screen.getByLabelText(/^Provider/).disabled).toBe(true);
    expect(screen.getByLabelText(/^API URL/).placeholder).toBe('https://api.voyageai.com/v1');
    fireEvent.click(screen.getByRole('button', { name: 'Update' }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0]).toMatchObject({ id: undefined, provider: 'voyage', model: 'voyage-3-large', dimensions: 2048, apiKey: 'pa-1' });
  });

  test('a configuration with missing fields still opens', () => {
    render(<EmbeddingConfigModal onSave={vi.fn()} onClose={vi.fn()} config={{ provider: 'lexical' }} />);
    expect([value(/^Name/), value(/^Model/), value(/^Dimensions/)]).toEqual(['', '', '']);
  });
});
