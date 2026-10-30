// @vitest-environment jsdom
// CollectionConfigModal: the collection settings dialog — loading a configured,
// unconfigured or failing collection, every field, the S3 backend and its
// secret hint, the memory and quantization warnings, custom metadata, the
// response prompt counter, and what Save sends. PUT /v1/collection-config
// replaces the stored config, so saving must keep the fields the dialog does
// not show and drop the output-only ones (see lib/collection-config.js).
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: { getCollectionConfig: vi.fn(), setCollectionConfig: vi.fn() },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: CollectionConfigModal } = await import('./CollectionConfigModal');

const initialStore = useStore.getState();

/** A stored config with every field the dialog shows and several it does not. */
const fullConfig = {
  collection: 'docs',
  type: 'website',
  description: 'Product docs',
  icon: 'D',
  color: '#ff0000',
  customMeta: { owner: 'team-a' },
  quantization: 'int8',
  diskOnlyVectors: true,
  storageBackend: 's3',
  storageConfig: { endpoint: 'minio:9000', bucket: 'b', region: 'eu', accessKey: 'AK', prefix: 'p/', useTLS: true, secretKeySet: true },
  trackAccess: true,
  trackHot: true,
  spellCorrect: true,
  spellLang: 'en',
  maxRevisions: 5,
  encrypted: true,
  retrieval: {
    defaultSearchType: 'hybrid', topK: 8, retrievalMode: 'chunk', hybridStrategy: 'weighted',
    hybridAlpha: 0, hybridAlphaSet: true, contextTokenBudget: 2000,
    vectorAlgorithm: 'hnsw', summaryMode: 'blend', summaryWeight: 1.5, maxTopK: 30,
    oversample: 4, groupBy: 'author',
  },
  responsePrompt: 'Answer in steps.',
  fts: { enabled: true, excludeMetaKeys: ['url', 'sha'] },
  code: { languages: ['go'] },
  embedding: { model: 'nomic' },
  wordpress: { url: 'https://wp.example' },
  quota: { maxDocuments: 100 },
  readOnly: true,
  corpus: { name: 'gdpr', version: '1.0.0', license: 'CC-BY-4.0' },
  evalSet: [{ query: 'q' }],
};

/** The control that follows a label without htmlFor (inside its wrapper, if any). */
function field(label) {
  const el = screen.getByText(label, { selector: 'label' }).nextElementSibling;
  return el.matches('input, select, textarea') ? el : el.querySelector('input, select, textarea');
}

/** Renders the dialog for `collection` and waits for the form. */
async function open(data, collection = 'docs') {
  vi.mocked(mddbClient.getCollectionConfig).mockResolvedValue(data);
  const onClose = vi.fn();
  const onSave = vi.fn();
  const view = render(<CollectionConfigModal collection={collection} onClose={onClose} onSave={onSave} />);
  await screen.findByRole('button', { name: 'Save Settings' });
  return { ...view, onClose, onSave };
}

/**
 * Clicks Save and returns the payload as it goes over the wire: the client
 * JSON-encodes it, so keys left undefined are not sent.
 */
async function save() {
  fireEvent.click(screen.getByRole('button', { name: 'Save Settings' }));
  await waitFor(() => expect(mddbClient.setCollectionConfig).toHaveBeenCalledTimes(1));
  return JSON.parse(JSON.stringify(vi.mocked(mddbClient.setCollectionConfig).mock.calls[0][0]));
}

beforeEach(() => {
  useStore.setState(initialStore, true);
  vi.mocked(mddbClient.getCollectionConfig).mockReset();
  vi.mocked(mddbClient.setCollectionConfig).mockReset().mockResolvedValue({});
});
afterEach(cleanup);

describe('CollectionConfigModal loading', () => {
  test('shows a spinner while the config loads', () => {
    vi.mocked(mddbClient.getCollectionConfig).mockReturnValue(new Promise(() => {}));
    const { container } = render(<CollectionConfigModal collection="docs" onClose={vi.fn()} onSave={vi.fn()} />);
    expect(container.querySelector('.animate-spin')).not.toBeNull();
    expect(screen.queryByRole('button', { name: 'Save Settings' })).toBeNull();
    expect(screen.getByText('docs')).toBeTruthy();
  });

  test('reports a load failure', async () => {
    vi.mocked(mddbClient.getCollectionConfig).mockRejectedValue(new Error('API Error (500): boom'));
    render(<CollectionConfigModal collection="docs" onClose={vi.fn()} onSave={vi.fn()} />);
    expect(await screen.findByText('API Error (500): boom')).toBeTruthy();
  });

  test('loads again when the collection changes', async () => {
    const { rerender, onClose, onSave } = await open({ configured: false });
    rerender(<CollectionConfigModal collection="other" onClose={onClose} onSave={onSave} />);
    await waitFor(() => expect(mddbClient.getCollectionConfig).toHaveBeenLastCalledWith('other'));
    expect(mddbClient.getCollectionConfig).toHaveBeenCalledTimes(2);
  });

  test.each([
    ['unconfigured', { configured: false }],
    ['configured without a config', { configured: true }],
    ['configured with an empty config', { configured: true, config: {} }],
  ])('an %s collection shows the defaults and saves only the basics', async (_label, data) => {
    await open(data);
    expect(field('Type').value).toBe('default');
    expect(field('Description').value).toBe('');
    expect(field('Icon (emoji)').value).toBe('');
    expect(field('Color').value).toBe('#3b82f6');
    expect(field('Storage Backend').value).toBe('boltdb');
    expect(screen.getByText('Embedded key-value store, persisted to disk')).toBeTruthy();
    expect(field('Vector Quantization').value).toBe('float32');
    expect(screen.getByText('Full precision, highest accuracy')).toBeTruthy();
    expect(screen.getByText('Unlimited (default) — every update retained')).toBeTruthy();
    expect(screen.getByText('No custom metadata defined')).toBeTruthy();
    expect(screen.getByText('0 / 4096 characters')).toBeTruthy();
    expect(screen.queryByRole('note')).toBeNull();
    expect(screen.queryByText('S3 / MinIO Settings')).toBeNull();
    expect(screen.queryByText('Override spell language (optional)')).toBeNull();

    const payload = await save();
    expect(payload).toEqual({ collection: 'docs', type: 'default', description: '', icon: '', color: '#3b82f6' });
    expect(payload).not.toHaveProperty('quota');
    expect(payload).not.toHaveProperty('retrieval');
  });
});

describe('CollectionConfigModal with a stored config', () => {
  test('shows every stored value and the read-only notice', async () => {
    await open({ configured: true, config: fullConfig });
    expect(screen.getByRole('note').textContent).toContain('installed from corpus pack gdpr v1.0.0 (CC-BY-4.0)');
    expect(field('Type').value).toBe('website');
    expect(field('Description').value).toBe('Product docs');
    expect(field('Icon (emoji)').value).toBe('D');
    expect(field('Color').value).toBe('#ff0000');
    expect(field('Storage Backend').value).toBe('s3');
    expect(field('Endpoint *').value).toBe('minio:9000');
    expect(field('Bucket *').value).toBe('b');
    expect(field('Region').value).toBe('eu');
    expect(field('Prefix').value).toBe('p/');
    expect(field('Access Key').value).toBe('AK');
    expect(field('Secret Key').value).toBe('');
    expect(field('Secret Key').placeholder).toBe('Stored — leave blank to keep');
    expect(screen.getByText('A secret is stored. Leave blank to keep it, or type a new one to replace it.')).toBeTruthy();
    expect(screen.getByLabelText('Use TLS (HTTPS)').checked).toBe(true);
    expect(field('Vector Quantization').value).toBe('int8');
    expect(screen.getByText('vector-reindex --force')).toBeTruthy();
    expect(screen.getByLabelText('Track document access events').checked).toBe(true);
    expect(screen.getByLabelText('Maintain hot-docs leaderboard').checked).toBe(true);
    expect(screen.getByLabelText('Auto-correct FTS queries').checked).toBe(true);
    expect(field('Override spell language (optional)').value).toBe('en');
    expect(screen.getByText('Keep last 5 revisions per document')).toBeTruthy();
    expect(screen.getByLabelText('Default search type').value).toBe('hybrid');
    expect(screen.getByLabelText('Results per search (topK)').value).toBe('8');
    expect(screen.getByLabelText('Granularity').value).toBe('chunk');
    expect(screen.getByLabelText('Hybrid fusion').value).toBe('weighted');
    expect(field(/Keyword ↔ semantic balance/).value).toBe('0');
    expect(screen.getByText(/Keyword ↔ semantic balance/).textContent).toContain('(0)');
    expect(screen.getByLabelText('Context budget (tokens)').value).toBe('2000');
    expect(screen.getByLabelText('Vector index').value).toBe('hnsw');
    expect(screen.getByLabelText('Meta keys kept out of full-text search').value).toBe('url, sha');
    expect(screen.getByLabelText('Max documents').value).toBe('100');
    expect(screen.getByLabelText('Answer Formatting').value).toBe('Answer in steps.');
    expect(screen.getByLabelText('Encrypt this collection (AES-256-GCM)').checked).toBe(true);
    expect(screen.getByPlaceholderText('Key').value).toBe('owner');
    expect(screen.getByPlaceholderText('Value').value).toBe('team-a');
  });

  test('saving unchanged keeps hidden fields and drops output-only ones', async () => {
    await open({ configured: true, config: fullConfig });
    const payload = await save();
    expect(payload).toEqual({
      collection: 'docs',
      type: 'website',
      description: 'Product docs',
      icon: 'D',
      color: '#ff0000',
      customMeta: { owner: 'team-a' },
      quantization: 'int8',
      diskOnlyVectors: true,
      storageBackend: 's3',
      storageConfig: { endpoint: 'minio:9000', bucket: 'b', region: 'eu', accessKey: 'AK', secretKey: '', prefix: 'p/', useTLS: true },
      trackAccess: true,
      trackHot: true,
      spellCorrect: true,
      spellLang: 'en',
      maxRevisions: 5,
      encrypted: true,
      retrieval: {
        oversample: 4, groupBy: 'author',
        defaultSearchType: 'hybrid', topK: 8, retrievalMode: 'chunk', hybridStrategy: 'weighted',
        hybridAlpha: 0, hybridAlphaSet: true, contextTokenBudget: 2000,
        vectorAlgorithm: 'hnsw', summaryMode: 'blend', summaryWeight: 1.5, maxTopK: 30,
      },
      responsePrompt: 'Answer in steps.',
      fts: { enabled: true, excludeMetaKeys: ['url', 'sha'] },
      code: { languages: ['go'] },
      embedding: { model: 'nomic' },
      wordpress: { url: 'https://wp.example' },
    });
    for (const key of ['readOnly', 'corpus', 'evalSet', 'quota']) expect(payload).not.toHaveProperty(key);
    expect(payload.storageConfig).not.toHaveProperty('secretKeySet');
  });

  test('switching to float32 drops diskOnlyVectors', async () => {
    await open({ configured: true, config: fullConfig });
    fireEvent.change(field('Vector Quantization'), { target: { value: 'float32' } });
    expect(screen.queryByText('vector-reindex --force')).toBeNull();
    const payload = await save();
    expect(payload).not.toHaveProperty('quantization');
    expect(payload).not.toHaveProperty('diskOnlyVectors');
  });

  test('a non-S3 backend drops the stored storage config', async () => {
    await open({ configured: true, config: { ...fullConfig, storageBackend: 'boltdb' } });
    expect(screen.queryByText('S3 / MinIO Settings')).toBeNull();
    const payload = await save();
    expect(payload).not.toHaveProperty('storageConfig');
    expect(payload).not.toHaveProperty('storageBackend');
  });

  test('an unset hybrid alpha stays empty, a set one without value reads 0', async () => {
    await open({ configured: true, config: { retrieval: { hybridAlphaSet: true } } });
    expect(field(/Keyword ↔ semantic balance/).value).toBe('0');
    cleanup();
    vi.mocked(mddbClient.getCollectionConfig).mockReset();
    await open({ configured: true, config: { retrieval: { hybridAlpha: 0.7 } } });
    expect(field(/Keyword ↔ semantic balance/).value).toBe('');
    const payload = await save();
    expect(payload).not.toHaveProperty('retrieval');
  });

  test('changing a quota limit sends it, clearing every limit sends {}', async () => {
    await open({ configured: true, config: fullConfig });
    fireEvent.change(screen.getByLabelText('Max documents'), { target: { value: '' } });
    const payload = await save();
    expect(payload.quota).toEqual({});
  });

  test('shows the stored quota used, from the usage the server sends with it', async () => {
    await open({ configured: true, config: fullConfig, usage: { documents: 40, bytes: 1024 } });
    const bar = screen.getByRole('progressbar', { name: 'Documents' });
    expect(bar.getAttribute('aria-valuetext')).toBe('40 / 100 documents (40%)');
    expect(screen.getByText('40 / 100 documents')).toBeTruthy();
  });

  test('without usage (no quota, or an older server) there is no usage section', async () => {
    await open({ configured: true, config: fullConfig });
    expect(screen.queryByRole('heading', { name: 'Quota usage' })).toBeNull();
  });

  test('clearing the advanced settings removes them but keeps other stored keys', async () => {
    await open({ configured: true, config: fullConfig });
    fireEvent.change(screen.getByLabelText('Vector index'), { target: { value: '' } });
    fireEvent.change(screen.getByLabelText('Document summaries'), { target: { value: '' } });
    fireEvent.change(screen.getByLabelText('Summary weight'), { target: { value: '' } });
    fireEvent.change(screen.getByLabelText('Result cap (maxTopK)'), { target: { value: '' } });
    fireEvent.change(screen.getByLabelText('Meta keys kept out of full-text search'), { target: { value: '' } });
    fireEvent.change(screen.getByLabelText('Max bytes'), { target: { value: '512' } });
    const payload = await save();
    expect(payload.retrieval).not.toHaveProperty('vectorAlgorithm');
    expect(payload.retrieval).not.toHaveProperty('summaryMode');
    expect(payload.retrieval).not.toHaveProperty('summaryWeight');
    expect(payload.retrieval).not.toHaveProperty('maxTopK');
    expect(payload.retrieval.oversample).toBe(4);
    expect(payload.fts).toEqual({ enabled: true });
    expect(payload.quota).toEqual({ maxDocuments: 100, maxBytes: 512 });
  });
});

describe('CollectionConfigModal editing', () => {
  test('every field edited reaches the payload', async () => {
    await open({ configured: false });
    fireEvent.change(field('Type'), { target: { value: 'audio' } });
    fireEvent.change(field('Description'), { target: { value: 'Podcasts' } });
    fireEvent.change(field('Icon (emoji)'), { target: { value: 'P' } });
    expect(screen.getByText('P', { selector: 'span' })).toBeTruthy();
    const [colorPicker, colorText] = screen.getByText('Color', { selector: 'label' }).nextElementSibling.querySelectorAll('input');
    fireEvent.change(colorPicker, { target: { value: '#00ff00' } });
    expect(colorText.value).toBe('#00ff00');
    fireEvent.change(colorText, { target: { value: '#123456' } });

    fireEvent.change(field('Storage Backend'), { target: { value: 's3' } });
    expect(screen.getByText('S3-compatible object storage')).toBeTruthy();
    expect(screen.getByText('No secret stored yet.')).toBeTruthy();
    expect(field('Secret Key').placeholder).toBe('wJalrXUtnFEMI/K7MDENG...');
    fireEvent.change(field('Endpoint *'), { target: { value: 's3.example' } });
    fireEvent.change(field('Bucket *'), { target: { value: 'bk' } });
    fireEvent.change(field('Region'), { target: { value: 'us-east-1' } });
    fireEvent.change(field('Prefix'), { target: { value: 'mddb/' } });
    fireEvent.change(field('Access Key'), { target: { value: 'AKIA' } });
    fireEvent.change(field('Secret Key'), { target: { value: 'secret' } });
    fireEvent.click(screen.getByLabelText('Use TLS (HTTPS)'));

    fireEvent.change(field('Vector Quantization'), { target: { value: 'int4' } });
    expect(screen.getByText('~2-3% recall drop, best for large collections')).toBeTruthy();
    fireEvent.click(screen.getByLabelText('Track document access events'));
    fireEvent.click(screen.getByLabelText('Maintain hot-docs leaderboard'));
    fireEvent.click(screen.getByLabelText('Auto-correct FTS queries'));
    fireEvent.change(field('Override spell language (optional)'), { target: { value: 'de' } });
    fireEvent.change(field('Revision History Retention'), { target: { value: '3' } });
    expect(screen.getByText('Keep last 3 revisions per document')).toBeTruthy();

    fireEvent.change(screen.getByLabelText('Default search type'), { target: { value: 'vector' } });
    fireEvent.change(screen.getByLabelText('Results per search (topK)'), { target: { value: '12' } });
    fireEvent.change(screen.getByLabelText('Granularity'), { target: { value: 'graph' } });
    fireEvent.change(screen.getByLabelText('Hybrid fusion'), { target: { value: 'alpha' } });
    fireEvent.change(field(/Keyword ↔ semantic balance/), { target: { value: '0.3' } });
    expect(screen.getByText(/Keyword ↔ semantic balance/).textContent).toContain('(0.3)');
    fireEvent.change(screen.getByLabelText('Context budget (tokens)'), { target: { value: '4000' } });
    fireEvent.change(screen.getByLabelText('Vector index'), { target: { value: 'auto' } });
    fireEvent.change(screen.getByLabelText('Document summaries'), { target: { value: 'expand' } });
    fireEvent.change(screen.getByLabelText('Summary weight'), { target: { value: '2.5' } });
    fireEvent.change(screen.getByLabelText('Result cap (maxTopK)'), { target: { value: '25' } });
    fireEvent.change(screen.getByLabelText('Meta keys kept out of full-text search'), { target: { value: 'url, license' } });
    fireEvent.change(screen.getByLabelText('Max vectors'), { target: { value: '9' } });
    fireEvent.change(screen.getByLabelText('Answer Formatting'), { target: { value: '  Use bullets.  ' } });
    fireEvent.click(screen.getByLabelText('Encrypt this collection (AES-256-GCM)'));

    const payload = await save();
    expect(payload).toEqual({
      collection: 'docs',
      type: 'audio',
      description: 'Podcasts',
      icon: 'P',
      color: '#123456',
      quantization: 'int4',
      storageBackend: 's3',
      storageConfig: { endpoint: 's3.example', bucket: 'bk', region: 'us-east-1', accessKey: 'AKIA', secretKey: 'secret', prefix: 'mddb/', useTLS: true },
      trackAccess: true,
      trackHot: true,
      spellCorrect: true,
      spellLang: 'de',
      maxRevisions: 3,
      encrypted: true,
      retrieval: {
        defaultSearchType: 'vector', topK: 12, retrievalMode: 'graph', hybridStrategy: 'alpha',
        hybridAlpha: 0.3, hybridAlphaSet: true, contextTokenBudget: 4000,
        vectorAlgorithm: 'auto', summaryMode: 'expand', summaryWeight: 2.5, maxTopK: 25,
      },
      fts: { excludeMetaKeys: ['url', 'license'] },
      quota: { maxVectors: 9 },
      responsePrompt: 'Use bullets.',
    });
  });

  test('the memory backend warns that data is lost on restart', async () => {
    await open({ configured: false });
    expect(screen.queryByText(/will be lost when the server restarts/)).toBeNull();
    fireEvent.change(field('Storage Backend'), { target: { value: 'memory' } });
    expect(screen.getByText(/will be lost when the server restarts/)).toBeTruthy();
    expect(screen.getByText('Fast but data is lost on restart')).toBeTruthy();
    const payload = await save();
    expect(payload.storageBackend).toBe('memory');
    expect(payload).not.toHaveProperty('storageConfig');
  });

  test('reciprocal rank fusion hides the alpha balance', async () => {
    await open({ configured: false });
    expect(screen.getByText(/Keyword ↔ semantic balance/)).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Hybrid fusion'), { target: { value: 'rrf' } });
    expect(screen.queryByText(/Keyword ↔ semantic balance/)).toBeNull();
  });

  test('custom metadata entries are added, edited, removed, and blank keys skipped', async () => {
    await open({ configured: false });
    const add = screen.getByRole('button', { name: 'Add' });
    fireEvent.click(add);
    fireEvent.click(add);
    fireEvent.click(add);
    expect(screen.queryByText('No custom metadata defined')).toBeNull();
    const keys = screen.getAllByPlaceholderText('Key');
    const values = screen.getAllByPlaceholderText('Value');
    fireEvent.change(keys[0], { target: { value: ' lang ' } });
    fireEvent.change(values[0], { target: { value: 'en' } });
    fireEvent.change(keys[1], { target: { value: 'drop' } });
    fireEvent.change(values[2], { target: { value: 'no key' } });
    // Remove the second row ("drop"): each row ends with its trash button.
    fireEvent.click(keys[1].parentElement.querySelector('button'));
    expect(screen.getAllByPlaceholderText('Key').map((i) => i.value)).toEqual([' lang ', '']);

    const payload = await save();
    expect(payload.customMeta).toEqual({ lang: 'en' });
  });

  test('removing every entry shows the empty state', async () => {
    await open({ configured: true, config: { customMeta: { a: '1' } } });
    fireEvent.click(screen.getByPlaceholderText('Key').parentElement.querySelector('button'));
    expect(screen.getByText('No custom metadata defined')).toBeTruthy();
  });

  test('a long response prompt warns, and input is capped at 4096 characters', async () => {
    await open({ configured: false });
    const prompt = screen.getByLabelText('Answer Formatting');
    fireEvent.change(prompt, { target: { value: 'x'.repeat(3801) } });
    const counter = screen.getByText(/3801 \/ 4096 characters/);
    expect(counter.textContent).toContain('competes with the answer for context');
    expect(counter.className).toContain('text-amber-600');
    fireEvent.change(prompt, { target: { value: 'y'.repeat(5000) } });
    expect(prompt.value).toHaveLength(4096);
    fireEvent.change(prompt, { target: { value: 'short' } });
    expect(screen.getByText('5 / 4096 characters').className).toContain('text-gray-400');
  });
});

describe('CollectionConfigModal actions', () => {
  test('a successful save calls onSave then onClose', async () => {
    const { onSave, onClose } = await open({ configured: false });
    await save();
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave.mock.invocationCallOrder[0]).toBeLessThan(onClose.mock.invocationCallOrder[0]);
  });

  test('a failed save shows the error and keeps the dialog open', async () => {
    vi.mocked(mddbClient.setCollectionConfig).mockRejectedValue(new Error('API Error (403): admin on "*" required'));
    const { onSave, onClose } = await open({ configured: false });
    fireEvent.click(screen.getByRole('button', { name: 'Save Settings' }));
    expect(await screen.findByText('API Error (403): admin on "*" required')).toBeTruthy();
    expect(onSave).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Save Settings' }).disabled).toBe(false);
  });

  test('Save is disabled with a spinner while saving', async () => {
    vi.mocked(mddbClient.setCollectionConfig).mockReturnValue(new Promise(() => {}));
    const { container } = await open({ configured: false });
    fireEvent.click(screen.getByRole('button', { name: 'Save Settings' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save Settings' }).disabled).toBe(true));
    expect(container.querySelector('button .animate-spin')).not.toBeNull();
  });

  test('Cancel and the close button call onClose', async () => {
    const { onClose, container } = await open({ configured: false });
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    fireEvent.click(container.querySelector('h2').parentElement.nextElementSibling);
    expect(onClose).toHaveBeenCalledTimes(2);
    expect(mddbClient.setCollectionConfig).not.toHaveBeenCalled();
  });
});
