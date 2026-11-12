// embedding-defaults (#415): switching the provider in the embedding form.
import { describe, test, expect } from 'vitest';
import { PROVIDER_DEFAULTS, defaultsFor, needsApiKey, initialFields, switchProvider } from './embedding-defaults';

describe('switchProvider', () => {
  test('OpenAI to Ollama replaces the OpenAI model, dimensions and URL with Ollama defaults', () => {
    expect(switchProvider(initialFields('openai'), 'openai', 'ollama')).toEqual({
      model: 'nomic-embed-text', dimensions: '768', apiUrl: 'http://localhost:11434',
    });
  });

  test('values the user typed are kept; defaults and empty fields follow the provider', () => {
    const typed = { model: 'mxbai-embed-large', dimensions: '', apiUrl: 'http://ollama:11434', name: 'x' };
    expect(switchProvider(typed, 'ollama', 'openai')).toEqual({
      model: 'mxbai-embed-large', dimensions: '1536', apiUrl: 'http://ollama:11434', name: 'x',
    });
  });

  test('a numeric dimension equal to the old default is replaced; Ollama back to OpenAI clears the URL', () => {
    expect(switchProvider({ model: 'nomic-embed-text', dimensions: 768, apiUrl: 'http://localhost:11434' }, 'ollama', 'openai'))
      .toEqual({ model: 'text-embedding-3-small', dimensions: '1536', apiUrl: '' });
  });

  test('a missing field and an unknown provider are handled', () => {
    expect(switchProvider({}, 'nope', 'lexical')).toEqual({ model: 'lexical-ngram-v1', dimensions: '512', apiUrl: '' });
    expect(switchProvider({ model: 'm' }, 'openai', 'nope')).toEqual({ model: 'm', dimensions: '', apiUrl: '' });
  });
});

describe('defaults', () => {
  test('every provider has a model and dimensions; only hosted ones need a key', () => {
    for (const d of Object.values(PROVIDER_DEFAULTS)) expect(d.model && d.dimensions).toBeTruthy();
    expect(['openai', 'ollama', 'cohere', 'voyage', 'lexical'].filter(needsApiKey)).toEqual(['openai', 'cohere', 'voyage']);
    expect(defaultsFor('nope')).toEqual({ model: '', dimensions: '', apiUrl: '' });
    expect(initialFields('voyage')).toEqual({ model: 'voyage-3', dimensions: '1024', apiUrl: 'https://api.voyageai.com/v1' });
  });
});
