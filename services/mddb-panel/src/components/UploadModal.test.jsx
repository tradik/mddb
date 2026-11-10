// @vitest-environment jsdom
// UploadModal (2.18.11): every file goes to /v1/upload, so the server reads
// Markdown frontmatter (lists included); extractImages (2.18.9) is an option.
import { describe, test, expect, vi, afterEach, beforeEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { uploadFile: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: UploadModal } = await import('./UploadModal');
const client = vi.mocked(mddbClient);

const mdFile = (name = 'My Notes.md') => new File(['---\ntags: [a, b]\n---\n# hi'], name, { type: 'text/markdown' });

function pick(container, files) {
  fireEvent.change(container.querySelector('input[type="file"]'), { target: { files } });
}

let onClose;
let onSuccess;
beforeEach(() => {
  onClose = vi.fn();
  onSuccess = vi.fn();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('UploadModal', () => {
  test('uploads Markdown through /v1/upload with its old key, not a client-side frontmatter parse', async () => {
    client.uploadFile.mockResolvedValue({ added: 1 });
    const { container } = render(<UploadModal collection="docs" onClose={onClose} onSuccess={onSuccess} />);
    pick(container, [mdFile(), new File(['x'], 'Report.pdf')]);
    expect(screen.getByText('2 files selected')).toBeTruthy();
    expect(screen.getByText(/1 Markdown\/text file \(stored as written/)).toBeTruthy();
    expect(screen.getByText(/1 file will be converted/)).toBeTruthy();
    expect(screen.getAllByText('auto-convert')).toHaveLength(1);

    fireEvent.click(screen.getByLabelText(/Extract images/));
    await act(async () => fireEvent.click(screen.getByRole('button', { name: /Upload/ })));

    expect(client.uploadFile).toHaveBeenCalledTimes(2);
    expect(client.uploadFile.mock.calls[0][0]).toMatchObject({ collection: 'docs', lang: 'en', key: 'My Notes', extractImages: true });
    expect(client.uploadFile.mock.calls[0][0].files.name).toBe('My Notes.md');
    expect(client.uploadFile.mock.calls[1][0]).toMatchObject({ key: 'report' });
    expect(onSuccess).toHaveBeenCalledWith('docs');
  });

  test('a new collection and language, rejected files and a failed upload', async () => {
    vi.useFakeTimers();
    client.uploadFile.mockRejectedValueOnce(new Error('boom')).mockResolvedValueOnce({ added: 1 });
    const { container } = render(<UploadModal onClose={onClose} onSuccess={onSuccess} />);
    expect(screen.getByText('Create Collection & Upload')).toBeTruthy();
    pick(container, [new File(['x'], 'virus.exe'), mdFile('a.md'), mdFile('b.md')]);
    expect(screen.getByText(/virus\.exe: Unsupported format/)).toBeTruthy();

    fireEvent.change(screen.getByPlaceholderText('Enter collection name'), { target: { value: ' kb ' } });
    fireEvent.change(screen.getByPlaceholderText('en'), { target: { value: 'pl' } });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: /Upload/ })));

    expect(client.uploadFile.mock.calls[0][0]).toMatchObject({ collection: 'kb', lang: 'pl', key: 'a', extractImages: false });
    expect(screen.getByText(/Uploaded 1\/2 files\. Errors: a: boom/)).toBeTruthy();
    expect(onSuccess).not.toHaveBeenCalled();
    await act(async () => vi.advanceTimersByTime(2000));
    expect(onSuccess).toHaveBeenCalledWith('kb');
  });

  test('blank inputs fall back to default collection and en; total failure never succeeds', async () => {
    client.uploadFile.mockRejectedValue(new Error('down'));
    const { container } = render(<UploadModal onClose={onClose} onSuccess={onSuccess} />);
    pick(container, [mdFile('a.md')]);
    fireEvent.change(screen.getByPlaceholderText('en'), { target: { value: ' ' } });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: /Upload/ })));
    expect(client.uploadFile.mock.calls[0][0]).toMatchObject({ collection: 'default', lang: 'en' });
    expect(screen.getByText(/Uploaded 0\/1 files/)).toBeTruthy();
  });

  test('an upload slower than 30 s times out', async () => {
    vi.useFakeTimers();
    client.uploadFile.mockReturnValue(new Promise(() => {}));
    const { container } = render(<UploadModal collection="docs" onClose={onClose} onSuccess={onSuccess} />);
    pick(container, [mdFile('a.md')]);
    act(() => { fireEvent.click(screen.getByRole('button', { name: /Upload/ })); });
    expect(screen.getByText('Uploading 1/1...')).toBeTruthy();
    await act(async () => vi.advanceTimersByTime(30000));
    expect(screen.getByText(/a: Upload timeout/)).toBeTruthy();
  });

  test('drag and drop, choosing again, empty picks and closing', () => {
    const { container } = render(<UploadModal collection="docs" onClose={onClose} onSuccess={onSuccess} />);
    const zone = container.querySelector('.border-dashed');
    fireEvent.dragEnter(zone);
    expect(zone.className).toContain('border-blue-500');
    fireEvent.dragLeave(zone);
    expect(zone.className).not.toContain('border-blue-500');
    fireEvent.dragOver(zone);
    fireEvent.drop(zone, { dataTransfer: { files: [mdFile('a.md')] } });
    expect(screen.getByText('1 file selected')).toBeTruthy();
    fireEvent.click(screen.getByText('Choose different files'));
    expect(screen.getByText('Drop your files here')).toBeTruthy();
    fireEvent.drop(zone, { dataTransfer: { files: [] } });
    pick(container, []);
    expect(screen.getByText('Drop your files here')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Close upload dialog' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  test('the summary counts several files of each kind', () => {
    const { container } = render(<UploadModal collection="docs" onClose={onClose} onSuccess={onSuccess} />);
    pick(container, [mdFile('a.md'), mdFile('b.md'), new File(['x'], 'c.pdf'), new File(['x'], 'd.pdf')]);
    expect(screen.getByText(/2 Markdown\/text files/)).toBeTruthy();
    expect(screen.getByText(/2 files will be converted/)).toBeTruthy();
  });

  test('submitting with nothing selected says so', async () => {
    render(<UploadModal collection="docs" onClose={onClose} onSuccess={onSuccess} />);
    await act(async () => fireEvent.submit(screen.getByRole('button', { name: /Upload/ }).closest('form')));
    expect(screen.getByText('No files selected.')).toBeTruthy();
  });
});
