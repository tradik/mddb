// @vitest-environment jsdom
// ImagePreview (2.18.9): an image document's picture with alt text, caption,
// page and parent; nothing for other documents; a missing image; cleanup.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { getImage: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: ImagePreview } = await import('./ImagePreview');
const { isImageDocument } = await import('../lib/image-meta');
const client = vi.mocked(mddbClient);

beforeEach(() => {
  client.getImage.mockReset();
  URL.createObjectURL = vi.fn(() => 'blob:img');
  URL.revokeObjectURL = vi.fn();
});
afterEach(cleanup);

const meta = { kind: ['image'], altText: ['A seal'], caption: ['The seal'], page: ['3'], parent: ['manual'] };

describe('isImageDocument', () => {
  test.each([
    [{ kind: ['image'] }, true],
    [{ kind: 'image' }, true],
    [{ kind: ['doc'] }, false],
    [{}, false],
    [undefined, false],
  ])('%o → %s', (m, want) => expect(isImageDocument(m)).toBe(want));
});

describe('ImagePreview', () => {
  test('renders nothing for a document that is not an image', () => {
    const { container } = render(<ImagePreview collection="docs" docKey="a" lang="en" meta={{ kind: ['doc'] }} />);
    expect(container.innerHTML).toBe('');
    expect(client.getImage).not.toHaveBeenCalled();
  });

  test('shows the image with alt, caption, page and parent, and revokes the URL', async () => {
    client.getImage.mockResolvedValue({ blob: new Blob(['x']) });
    const { unmount } = render(<ImagePreview collection="docs" docKey="manual/images/seal.png" lang="en" meta={meta} />);
    expect(screen.getByText('Loading image…')).toBeTruthy();
    const img = await screen.findByAltText('A seal');
    expect(img.getAttribute('src')).toBe('blob:img');
    expect(client.getImage).toHaveBeenCalledWith({ collection: 'docs', key: 'manual/images/seal.png', lang: 'en' });
    expect(screen.getByText('The seal')).toBeTruthy();
    expect(screen.getByText('Page 3')).toBeTruthy();
    expect(screen.getByText('manual')).toBeTruthy();
    unmount();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:img');
  });

  test('an image without alt text or caption', async () => {
    client.getImage.mockResolvedValue({ blob: new Blob(['x']) });
    const { container } = render(<ImagePreview collection="docs" docKey="k" meta={{ kind: 'image' }} />);
    await vi.waitFor(() => expect(container.querySelector('img')).toBeTruthy());
    expect(container.querySelector('img').getAttribute('alt')).toBe('');
    expect(container.querySelector('figcaption')).toBeNull();
  });

  test('a 404 says no image is stored', async () => {
    client.getImage.mockRejectedValue(new Error('HTTP 404: not found'));
    render(<ImagePreview collection="docs" docKey="k" meta={meta} />);
    expect((await screen.findByRole('alert')).textContent).toBe('No image stored for this key.');
  });

  test('another error is shown as it is', async () => {
    client.getImage.mockRejectedValue(new Error('Forbidden'));
    render(<ImagePreview collection="docs" docKey="k" meta={meta} />);
    expect((await screen.findByRole('alert')).textContent).toBe('Forbidden');
  });

  test('ignores an answer that arrives after unmount', async () => {
    let resolve;
    let reject;
    client.getImage
      .mockReturnValueOnce(new Promise((r) => { resolve = r; }))
      .mockReturnValueOnce(new Promise((_, r) => { reject = r; }));
    const first = render(<ImagePreview collection="docs" docKey="k" meta={meta} />);
    first.unmount();
    resolve({ blob: new Blob(['x']) });
    const second = render(<ImagePreview collection="docs" docKey="k2" meta={meta} />);
    second.unmount();
    reject(new Error('late'));
    await Promise.resolve();
    expect(URL.createObjectURL).not.toHaveBeenCalled();
  });
});

describe('ImagePreview switching documents', () => {
  test('hides the previous picture while the next one loads', async () => {
    client.getImage
      .mockResolvedValueOnce({ blob: new Blob(['a']) })
      .mockReturnValueOnce(new Promise(() => {}));
    const { rerender } = render(<ImagePreview collection="docs" docKey="a" meta={meta} />);
    await screen.findByAltText('A seal');
    rerender(<ImagePreview collection="docs" docKey="b" meta={meta} />);
    expect(screen.queryByAltText('A seal')).toBeNull();
    expect(screen.getByText('Loading image…')).toBeTruthy();
  });
});
