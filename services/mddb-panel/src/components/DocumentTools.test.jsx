// @vitest-environment jsdom
// Document tools in the viewer: set or clear a TTL (/v1/set-ttl) and
// zero-shot classification of the document or other text (/v1/classify).
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { setTTL: vi.fn(), classify: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: DocumentTools } = await import('./DocumentTools');

const doc = { collection: 'kb', key: 'a', lang: 'en', contentMd: '# A' };
const apiError = (status, text) => Object.assign(new Error(`API Error (${status}): ${text}`), { status, body: { error: text } });
const click = (name) => act(async () => fireEvent.click(screen.getByRole('button', { name })));

beforeEach(() => {
  mddbClient.setTTL.mockReset();
  mddbClient.classify.mockReset();
  useStore.setState({ currentDocument: null });
});
afterEach(cleanup);

describe('TtlControl', () => {
  test('sets an expiry, shows it, and removes it', async () => {
    mddbClient.setTTL.mockResolvedValueOnce({ ...doc, expiresAt: 2000 }).mockResolvedValueOnce({ ...doc });
    const { rerender } = render(<DocumentTools document={doc} />);
    expect(screen.getByText('Does not expire.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Remove expiry' })).toBeNull();
    fireEvent.change(screen.getByLabelText('Expire after (seconds)'), { target: { value: '3600' } });
    await click('Set expiry');
    expect(mddbClient.setTTL).toHaveBeenCalledWith({ collection: 'kb', key: 'a', lang: 'en', ttl: 3600 });
    const updated = useStore.getState().currentDocument;
    expect(updated).toEqual({ ...doc, expiresAt: 2000 });

    rerender(<DocumentTools document={updated} />);
    expect(screen.getByText(`Expires ${new Date(2000000).toLocaleString()}.`)).toBeTruthy();
    await click('Remove expiry');
    expect(mddbClient.setTTL).toHaveBeenLastCalledWith({ collection: 'kb', key: 'a', lang: 'en', ttl: 0 });
    expect(useStore.getState().currentDocument.expiresAt).toBeUndefined();
  });

  test('a non-positive TTL cannot be set; refusals as worded', async () => {
    mddbClient.setTTL.mockRejectedValueOnce(apiError(403, 'collection "kb" is read-only'));
    render(<DocumentTools document={doc} />);
    fireEvent.change(screen.getByLabelText('Expire after (seconds)'), { target: { value: '0' } });
    expect(screen.getByRole('button', { name: 'Set expiry' }).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Expire after (seconds)'), { target: { value: '5' } });
    await click('Set expiry');
    expect(screen.getByText('Not permitted')).toBeTruthy();
  });
});

describe('ClassifyBox', () => {
  test('classifies the document, then free text, and lists the scores', async () => {
    mddbClient.classify
      .mockResolvedValueOnce({ results: [{ label: 'devops', score: 0.0738 }], model: 'lexical-ngram-v1', dimensions: 512 })
      .mockResolvedValueOnce({ results: null, model: 'm', dimensions: 8 });
    render(<DocumentTools document={doc} />);
    const button = screen.getByRole('button', { name: 'Classify' });
    expect(button.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Labels (comma-separated)'), { target: { value: ' devops, , cooking ' } });
    await click('Classify');
    expect(mddbClient.classify).toHaveBeenCalledWith({ collection: 'kb', key: 'a', lang: 'en', labels: ['devops', 'cooking'] });
    const table = screen.getByRole('table', { name: 'Label scores' });
    expect(within(table).getByText('0.074')).toBeTruthy();
    expect(screen.getByText('Model lexical-ngram-v1, 512 dimensions.')).toBeTruthy();

    fireEvent.click(screen.getByLabelText('Other text'));
    expect(button.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Text to classify'), { target: { value: 'blue green deploy' } });
    await click('Classify');
    expect(mddbClient.classify).toHaveBeenLastCalledWith({ text: 'blue green deploy', labels: ['devops', 'cooking'] });
    expect(screen.getByText('No label scored above the threshold.')).toBeTruthy();
    fireEvent.click(screen.getByLabelText('This document'));
    expect(screen.queryByLabelText('Text to classify')).toBeNull();
  });

  test('a server without embeddings says so', async () => {
    mddbClient.classify.mockRejectedValueOnce(apiError(503, 'embedding provider not configured'));
    render(<DocumentTools document={doc} />);
    fireEvent.change(screen.getByLabelText('Labels (comma-separated)'), { target: { value: 'a' } });
    await click('Classify');
    expect(screen.getByRole('alert').textContent).toBe('API Error (503): embedding provider not configured');
  });
});
