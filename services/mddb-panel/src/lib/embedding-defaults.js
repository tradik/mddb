/**
 * Per-provider defaults of the embedding configuration form (#415).
 *
 * `apiUrl` is the value the form fills in (empty where the server's default
 * is right); `urlHint` and `keyHint` are only placeholders.
 *
 * switchProvider() is what the form does when the provider changes: a field
 * still holding the previous provider's default (or nothing) takes the new
 * provider's default, and a value the user typed is kept. Before #415 only
 * empty fields were filled, so OpenAI → Ollama kept text-embedding-3-small
 * and 1536 dimensions and saved an Ollama configuration with an OpenAI model.
 */
export const PROVIDER_DEFAULTS = {
  openai: { model: 'text-embedding-3-small', dimensions: '1536', apiUrl: '', urlHint: 'https://api.openai.com/v1', keyHint: 'sk-...' },
  ollama: { model: 'nomic-embed-text', dimensions: '768', apiUrl: 'http://localhost:11434', urlHint: 'http://localhost:11434' },
  cohere: { model: 'embed-english-v3.0', dimensions: '1024', apiUrl: 'https://api.cohere.ai/v1', urlHint: 'https://api.cohere.ai/v1', keyHint: 'cohere_api_key...' },
  voyage: { model: 'voyage-3', dimensions: '1024', apiUrl: 'https://api.voyageai.com/v1', urlHint: 'https://api.voyageai.com/v1', keyHint: 'pa-...' },
  // No model and no network: hashed words and n-grams (SRCH-014).
  lexical: { model: 'lexical-ngram-v1', dimensions: '512', apiUrl: '' },
};

/** The fields switchProvider() resets. */
const SWITCHED = ['model', 'dimensions', 'apiUrl'];

/** Defaults of a provider; an unknown name gets empty ones. */
export const defaultsFor = (provider) => PROVIDER_DEFAULTS[provider] || { model: '', dimensions: '', apiUrl: '' };

/** Whether the provider needs an API key. */
export const needsApiKey = (provider) => Boolean(defaultsFor(provider).keyHint);

/** The form's first values for a new configuration of `provider`. */
export function initialFields(provider) {
  const d = defaultsFor(provider);
  return { model: d.model, dimensions: d.dimensions, apiUrl: d.apiUrl };
}

/** The fields after switching from `from` to `to`; typed values stay. */
export function switchProvider(fields, from, to) {
  const before = defaultsFor(from);
  const after = defaultsFor(to);
  const next = { ...fields };
  for (const key of SWITCHED) {
    const value = String(fields[key] ?? '');
    if (value === '' || value === String(before[key])) next[key] = after[key];
  }
  return next;
}
