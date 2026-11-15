// @vitest-environment jsdom
// NewDocumentModal: key/language inputs, adding and removing metadata (button
// and Enter key), the markdown content with its default, the busy state while
// addDocument runs, the optional onSuccess callback and a refused create.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { addDocument: vi.fn() } }));
vi.mock('./MarkdownEditor', () => ({
  default: ({ value, onChange }) => (
    <textarea aria-label="markdown" value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: NewDocumentModal } = await import('./NewDocumentModal');

beforeEach(() => {
  mddbClient.addDocument.mockReset();
});
afterEach(cleanup);

function setup(props = {}) {
  const onClose = vi.fn();
  const onSuccess = vi.fn();
  render(<NewDocumentModal collection="blog" onClose={onClose} onSuccess={onSuccess} {...props} />);
  return {
    onClose,
    onSuccess,
    keyInput: screen.getByPlaceholderText('e.g., hello-world'),
    create: screen.getByRole('button', { name: 'Create Document' }),
  };
}

describe('NewDocumentModal', () => {
  test('names the collection; Create stays disabled until a key is typed', () => {
    const { keyInput, create } = setup();
    expect(screen.getByText('Create a new document in blog')).toBeTruthy();
    expect(create.disabled).toBe(true);
    fireEvent.change(keyInput, { target: { value: 'hello' } });
    expect(create.disabled).toBe(false);
  });

  test('metadata: needs both key and value, adds with the button or Enter, removes', () => {
    setup();
    const metaKey = screen.getByPlaceholderText('Key (e.g., author)');
    const metaValue = screen.getByPlaceholderText('Value');
    const add = screen.getByRole('button', { name: 'Add' });

    fireEvent.click(add);
    fireEvent.change(metaKey, { target: { value: 'author' } });
    fireEvent.click(add);
    expect(screen.queryByRole('button', { name: 'Remove' })).toBeNull();

    fireEvent.change(metaValue, { target: { value: 'Ann' } });
    fireEvent.click(add);
    expect(screen.getByDisplayValue('author')).toBeTruthy();
    expect(screen.getByDisplayValue('Ann')).toBeTruthy();
    expect(metaKey.value).toBe('');
    expect(metaValue.value).toBe('');

    fireEvent.change(metaKey, { target: { value: 'tag' } });
    fireEvent.change(metaValue, { target: { value: 'go' } });
    fireEvent.keyPress(metaValue, { key: 'a', code: 'KeyA', charCode: 97 });
    expect(screen.getAllByRole('button', { name: 'Remove' })).toHaveLength(1);
    fireEvent.keyPress(metaValue, { key: 'Enter', code: 'Enter', charCode: 13 });
    expect(screen.getAllByRole('button', { name: 'Remove' })).toHaveLength(2);

    fireEvent.click(screen.getAllByRole('button', { name: 'Remove' })[0]);
    expect(screen.queryByDisplayValue('author')).toBeNull();
    expect(screen.getByDisplayValue('tag')).toBeTruthy();
  });

  test('creates with the default content, shows the busy state, then calls onSuccess and onClose', async () => {
    let finish;
    mddbClient.addDocument.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    const { keyInput, onClose, onSuccess } = setup();
    fireEvent.change(keyInput, { target: { value: 'hello' } });
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'pl_PL' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create Document' }));
    expect(screen.getByRole('button', { name: 'Creating...' }).disabled).toBe(true);
    await act(async () => finish({}));
    expect(mddbClient.addDocument).toHaveBeenCalledWith({
      collection: 'blog', key: 'hello', lang: 'pl_PL', meta: {},
      contentMd: '# New Document\n\nStart writing here...',
    });
    expect(onSuccess).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  test('sends typed content and metadata; works without onSuccess', async () => {
    mddbClient.addDocument.mockResolvedValue({});
    const onClose = vi.fn();
    render(<NewDocumentModal collection="docs" onClose={onClose} />);
    fireEvent.change(screen.getByPlaceholderText('e.g., hello-world'), { target: { value: 'k' } });
    fireEvent.change(screen.getByPlaceholderText('Key (e.g., author)'), { target: { value: 'author' } });
    fireEvent.change(screen.getByPlaceholderText('Value'), { target: { value: 'Ann' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    fireEvent.change(screen.getByLabelText('markdown'), { target: { value: '# Hi' } });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Create Document' })));
    expect(mddbClient.addDocument).toHaveBeenCalledWith({
      collection: 'docs', key: 'k', lang: 'en_US', meta: { author: ['Ann'] }, contentMd: '# Hi',
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  test('a refused create shows the error and re-enables the button', async () => {
    mddbClient.addDocument.mockRejectedValue(new Error('document already exists'));
    const { keyInput, onClose, onSuccess } = setup();
    fireEvent.change(keyInput, { target: { value: 'hello' } });
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Create Document' })));
    expect(screen.getByText('Error creating document')).toBeTruthy();
    expect(screen.getByText('document already exists')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Create Document' }).disabled).toBe(false);
    expect(onSuccess).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });

  test('Cancel and the X button close without saving', () => {
    const { onClose } = setup();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    fireEvent.click(screen.getAllByRole('button')[0]);
    expect(onClose).toHaveBeenCalledTimes(2);
    expect(mddbClient.addDocument).not.toHaveBeenCalled();
  });
});
