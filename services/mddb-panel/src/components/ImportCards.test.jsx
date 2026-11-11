// @vitest-environment jsdom
// Import page, single-source side: one Markdown document from a URL
// (/v1/import-url) and a Wikipedia dump (/v1/import-wiki), with the server's
// refusals shown as worded and "stop waiting" for a long dump.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { importURL: vi.fn(), importWiki: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: UrlImportCard } = await import('./UrlImportCard');
const { default: WikiImportCard } = await import('./WikiImportCard');

const initialStore = useStore.getState();
const apiError = (status, text) => Object.assign(new Error(`API Error (${status}): ${text}`), { status, body: { error: text } });
const change = (label, value) => fireEvent.change(screen.getByLabelText(label), { target: { value } });

beforeEach(() => {
  useStore.setState(initialStore, true);
  useStore.setState({ stats: { collections: [{ name: 'blog' }] } });
  mddbClient.importURL.mockReset();
  mddbClient.importWiki.mockReset();
});
afterEach(cleanup);

describe('UrlImportCard', () => {
  test('imports with only what is set and reports the stored key', async () => {
    useStore.setState({ currentCollection: 'blog' });
    mddbClient.importURL.mockResolvedValueOnce({ key: 'post', lang: 'en' });
    render(<UrlImportCard />);
    const submit = screen.getByRole('button', { name: 'Import' });
    expect(submit.disabled).toBe(true);
    change('URL of a Markdown document', ' https://example.com/post.md ');
    await act(async () => fireEvent.click(submit));
    expect(mddbClient.importURL).toHaveBeenCalledWith({ collection: 'blog', url: 'https://example.com/post.md', lang: 'en', key: undefined, ttl: 0 });
    expect(screen.getByRole('status').textContent).toBe('Imported post (en) into blog.');

    mddbClient.importURL.mockResolvedValueOnce({ key: 'k', lang: 'pl' });
    change('Language', '');
    change('Key (optional)', 'k');
    change('Expires after (seconds, optional)', '3600');
    await act(async () => fireEvent.click(submit));
    expect(mddbClient.importURL).toHaveBeenLastCalledWith({ collection: 'blog', url: 'https://example.com/post.md', lang: 'en', key: 'k', ttl: 3600 });
  });

  test('a refused address is shown as the server worded it', async () => {
    mddbClient.importURL.mockRejectedValueOnce(apiError(400, 'destination address is not allowed (private/loopback/link-local)'));
    render(<UrlImportCard />);
    change('Collection', 'notes');
    change('URL of a Markdown document', 'http://127.0.0.1/x.md');
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Import' })));
    expect(screen.getByRole('alert').textContent).toBe('API Error (400): destination address is not allowed (private/loopback/link-local)');
    expect(mddbClient.importURL.mock.lastCall[0].collection).toBe('notes');
  });
});

describe('WikiImportCard', () => {
  const file = new File(['<mediawiki/>'], 'dump.xml');

  test('uploads the dump with its options and reports the counts', async () => {
    mddbClient.importWiki.mockResolvedValueOnce({ imported: 1, skipped: 1, failed: 0, collection: 'wikipedia', durationMs: 9 });
    render(<WikiImportCard />);
    const submit = screen.getByRole('button', { name: 'Import dump' });
    expect(submit.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Dump file (.xml or .xml.bz2)'), { target: { files: [file] } });
    change('Namespaces (default 0)', '0,14');
    change('Max pages (optional)', '10');
    fireEvent.click(screen.getByLabelText('Skip redirect pages'));
    fireEvent.click(screen.getByLabelText('Skip full-text indexing (run an FTS reindex afterwards)'));
    await act(async () => fireEvent.click(submit));
    expect(mddbClient.importWiki).toHaveBeenCalledWith(expect.objectContaining({
      file, collection: 'wikipedia', lang: 'en', namespaces: '0,14', skipRedirects: false, skipFts: true, maxPages: 10,
    }));
    expect(screen.getByRole('status').textContent).toBe('Imported 1 pages into wikipedia, skipped 1, failed 0 (0 s).');

    fireEvent.change(screen.getByLabelText('Dump file (.xml or .xml.bz2)'), { target: { files: [] } });
    expect(submit.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Dump file (.xml or .xml.bz2)'), { target: { files: [file] } });
    change('Collection', ' ');
    expect(submit.disabled).toBe(true);
  });

  test('stop waiting aborts quietly; a server error is shown', async () => {
    let signal;
    mddbClient.importWiki.mockImplementationOnce((args) => new Promise((_, reject) => {
      signal = args.signal;
      signal.addEventListener('abort', () => reject(Object.assign(new Error('aborted'), { name: 'AbortError' })));
    }));
    render(<WikiImportCard />);
    fireEvent.change(screen.getByLabelText('Dump file (.xml or .xml.bz2)'), { target: { files: [file] } });
    change('Max pages (optional)', 'x');
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Import dump' })));
    expect(mddbClient.importWiki.mock.lastCall[0].maxPages).toBe(0);
    expect(screen.getByText('The server goes on importing if you stop waiting.')).toBeTruthy();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Stop waiting' })));
    expect(signal.aborted).toBe(true);
    expect(screen.queryByRole('alert')).toBeNull();

    mddbClient.importWiki.mockRejectedValueOnce(apiError(400, 'missing collection'));
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Import dump' })));
    expect(screen.getByRole('alert').textContent).toBe('API Error (400): missing collection');
  });
});
