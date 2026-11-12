// @vitest-environment jsdom
// EmbeddingModelsPanel: the list, default/delete/edit, the empty states and
// importing the environment's configuration (whose provider is the real
// provider name since #415).
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    vectorStats: vi.fn(), listEmbeddingConfigs: vi.fn(), createEmbeddingConfig: vi.fn(),
    updateEmbeddingConfig: vi.fn(), deleteEmbeddingConfig: vi.fn(), setDefaultEmbeddingConfig: vi.fn(),
  },
}));

const { default: client } = await import('../lib/mddb-client');
const { default: EmbeddingModelsPanel } = await import('./EmbeddingModelsPanel');

const openai = { id: 'openai-small', name: 'OpenAI Small', provider: 'openai', model: 'text-embedding-3-small', dimensions: 1536, apiUrl: '', isDefault: true };
const ollama = { id: 'ollama-nomic', name: 'Ollama Nomic', provider: 'ollama', model: 'nomic-embed-text', dimensions: 768, apiUrl: 'http://ollama:11434', isDefault: false };
const envStats = { enabled: true, provider: 'ollama', model: 'nomic-embed-text', dimensions: 768 };

async function renderPanel({ configs = [openai, ollama], stats = { enabled: false } } = {}) {
  vi.mocked(client.listEmbeddingConfigs).mockResolvedValue({ configs });
  vi.mocked(client.vectorStats).mockResolvedValue(stats);
  render(<EmbeddingModelsPanel />);
  await screen.findByRole('heading', { name: 'Embedding Models' });
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.spyOn(window, 'alert').mockImplementation(() => {});
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(cleanup);

describe('the list', () => {
  test('shows each configuration, marks the default and falls back to "Default" for an empty URL', async () => {
    await renderPanel();
    expect(screen.getByText('OpenAI Small')).toBeTruthy();
    expect(screen.getByText('http://ollama:11434')).toBeTruthy();
    expect(screen.getAllByText('Default')).toHaveLength(2); // the badge and the empty URL
    expect(screen.getAllByTitle('Set as default')).toHaveLength(1);
    expect(screen.getAllByTitle('Delete')[0].disabled).toBe(true);
  });

  test('set as default reloads, and a failure is reported', async () => {
    await renderPanel();
    vi.mocked(client.setDefaultEmbeddingConfig).mockResolvedValueOnce({});
    fireEvent.click(screen.getByTitle('Set as default'));
    await waitFor(() => expect(client.listEmbeddingConfigs).toHaveBeenCalledTimes(2));
    expect(client.setDefaultEmbeddingConfig).toHaveBeenCalledWith('ollama-nomic');

    vi.mocked(client.setDefaultEmbeddingConfig).mockRejectedValueOnce(new Error('nope'));
    fireEvent.click(await screen.findByTitle('Set as default'));
    await waitFor(() => expect(window.alert).toHaveBeenCalledWith('Failed to set default: nope'));
  });

  test('delete asks first; cancel keeps it, confirm deletes, a failure is reported', async () => {
    await renderPanel();
    const del = () => screen.getAllByTitle('Delete')[1];
    vi.mocked(window.confirm).mockReturnValueOnce(false);
    fireEvent.click(del());
    expect(client.deleteEmbeddingConfig).not.toHaveBeenCalled();

    vi.mocked(client.deleteEmbeddingConfig).mockResolvedValueOnce({});
    fireEvent.click(del());
    await waitFor(() => expect(client.deleteEmbeddingConfig).toHaveBeenCalledWith('ollama-nomic'));
    await waitFor(() => expect(client.listEmbeddingConfigs).toHaveBeenCalledTimes(2));

    vi.mocked(client.deleteEmbeddingConfig).mockRejectedValueOnce(new Error('in use'));
    fireEvent.click(await screen.findAllByTitle('Delete').then((b) => b[1]));
    await waitFor(() => expect(window.alert).toHaveBeenCalledWith('Failed to delete: in use'));
  });

  test('edit opens the form with the configuration, saves by its id, and closes', async () => {
    await renderPanel();
    fireEvent.click(screen.getAllByTitle('Edit')[1]);
    expect(screen.getByRole('heading', { name: 'Edit Embedding Model' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();

    vi.mocked(client.updateEmbeddingConfig).mockResolvedValueOnce({});
    fireEvent.click(screen.getAllByTitle('Edit')[1]);
    fireEvent.click(screen.getByRole('button', { name: 'Update' }));
    await waitFor(() => expect(client.updateEmbeddingConfig).toHaveBeenCalledWith('ollama-nomic', expect.objectContaining({ model: 'nomic-embed-text' })));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  test('add opens an empty form, creates and closes; Cancel closes without saving', async () => {
    await renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'Add Model' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(client.createEmbeddingConfig).not.toHaveBeenCalled();

    vi.mocked(client.createEmbeddingConfig).mockResolvedValueOnce({});
    fireEvent.click(screen.getByRole('button', { name: 'Add Model' }));
    fireEvent.change(screen.getByLabelText(/^ID/), { target: { value: 'x' } });
    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: 'X' } });
    fireEvent.change(screen.getByLabelText(/^API Key/), { target: { value: 'k' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(client.createEmbeddingConfig).toHaveBeenCalledWith(expect.objectContaining({ id: 'x', provider: 'openai' })));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });
});

describe('empty states and errors', () => {
  test('nothing configured anywhere offers the first model', async () => {
    await renderPanel({ configs: [] });
    expect(screen.getByText('No embedding models configured')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Add Your First Model' }));
    expect(screen.getByRole('dialog')).toBeTruthy();
  });

  test('a provider from the environment can be imported under its provider name, or a new one added', async () => {
    await renderPanel({ configs: [], stats: envStats });
    expect(screen.getByText('Import Current Configuration')).toBeTruthy();
    vi.mocked(client.createEmbeddingConfig).mockResolvedValueOnce({});
    fireEvent.click(screen.getByRole('button', { name: 'Import Current Config' }));
    await waitFor(() => expect(window.alert).toHaveBeenCalledWith('Successfully imported current configuration!'));
    expect(client.createEmbeddingConfig).toHaveBeenCalledWith({
      id: 'ollama-default', name: 'ollama - nomic-embed-text', provider: 'ollama', model: 'nomic-embed-text',
      dimensions: 768, apiKey: '', apiUrl: '', isDefault: true,
    });

    vi.mocked(client.createEmbeddingConfig).mockRejectedValueOnce(new Error('exists'));
    fireEvent.click(await screen.findByRole('button', { name: 'Import Current Config' }));
    await waitFor(() => expect(window.alert).toHaveBeenCalledWith('Failed to import configuration: exists'));

    fireEvent.click(screen.getByRole('button', { name: 'Add New Model' }));
    expect(screen.getByRole('dialog')).toBeTruthy();
  });

  test('a failed list is shown, and missing vector stats are not an error', async () => {
    vi.mocked(client.listEmbeddingConfigs).mockRejectedValue(new Error('forbidden'));
    vi.mocked(client.vectorStats).mockRejectedValue(new Error('down'));
    render(<EmbeddingModelsPanel />);
    expect(await screen.findByText('forbidden')).toBeTruthy();
    expect(screen.getByText('No embedding models configured')).toBeTruthy();
  });

  test('a list without configs is treated as empty', async () => {
    vi.mocked(client.listEmbeddingConfigs).mockResolvedValue({});
    vi.mocked(client.vectorStats).mockResolvedValue({ enabled: false });
    render(<EmbeddingModelsPanel />);
    expect(await screen.findByText('No embedding models configured')).toBeTruthy();
  });
});
