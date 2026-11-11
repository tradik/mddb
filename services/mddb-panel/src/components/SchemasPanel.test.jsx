// @vitest-environment jsdom
// Collection Schemas page: the list of collections with a schema, the JSON
// editor (inline parse errors, save, delete behind a confirmation naming the
// collection) and the dry-run validation box.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, within } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: { listSchemas: vi.fn(), getSchema: vi.fn(), setSchema: vi.fn(), deleteSchema: vi.fn(), validateMeta: vi.fn() },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: SchemasPanel } = await import('./SchemasPanel');

const initialStore = useStore.getState();
const blogSchema = '{"required":["category"]}';
const apiError = (status, text) => Object.assign(new Error(`API Error (${status}): ${text}`), { status, body: { error: text } });

async function renderPage(state = {}) {
  useStore.setState({ stats: { collections: [{ name: 'blog' }, { name: 'notes' }] }, ...state });
  render(<SchemasPanel />);
  await act(async () => {});
}

const editor = () => screen.getByLabelText(/^Schema of /);

beforeEach(() => {
  useStore.setState(initialStore, true);
  for (const fn of Object.values(mddbClient)) fn.mockReset();
  mddbClient.listSchemas.mockResolvedValue({ schemas: [{ collection: 'blog', schema: blogSchema }] });
  mddbClient.getSchema.mockImplementation(async (collection) => (collection === 'blog'
    ? { collection, schema: blogSchema, enabled: true }
    : { collection, schema: '', enabled: false }));
});
afterEach(cleanup);

describe('SchemasPanel', () => {
  test('lists the collections with a schema and edits the first collection', async () => {
    await renderPage();
    expect(screen.getByRole('button', { name: 'blog', pressed: true })).toBeTruthy();
    expect(editor().value).toBe('{\n  "required": [\n    "category"\n  ]\n}');
    expect(screen.getByText('Validation is on for this collection.')).toBeTruthy();

    fireEvent.change(screen.getByLabelText('Collection'), { target: { value: 'notes' } });
    await act(async () => {});
    expect(screen.getByText('This collection has no schema; documents are not validated.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Delete schema' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Save schema' }).disabled).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: 'blog' }));
    await act(async () => {});
    expect(screen.getByLabelText('Schema of blog')).toBeTruthy();
  });

  test('starts from the current collection; no schemas; no collections at all', async () => {
    mddbClient.listSchemas.mockResolvedValue({});
    await renderPage({ currentCollection: 'notes' });
    expect(screen.getByLabelText('Schema of notes')).toBeTruthy();
    expect(screen.getByText('No collection has a schema.')).toBeTruthy();
    cleanup();
    useStore.setState({ stats: null, currentCollection: null });
    render(<SchemasPanel />);
    await act(async () => {});
    expect(screen.queryByLabelText(/^Schema of /)).toBeNull();
  });

  test('a refused list says not permitted', async () => {
    mddbClient.listSchemas.mockRejectedValue(apiError(403, 'forbidden'));
    await renderPage();
    expect(screen.getByText('Not permitted')).toBeTruthy();
  });

  test('a schema that cannot be read shows the server error instead of the editor', async () => {
    mddbClient.getSchema.mockRejectedValue(apiError(500, 'storage closed'));
    await renderPage();
    expect(screen.getByText('API Error (500): storage closed')).toBeTruthy();
    expect(screen.queryByLabelText(/^Schema of /)).toBeNull();
  });
});

describe('SchemaEditor', () => {
  test('shows parse errors inline, refuses a non-object and saves compact JSON', async () => {
    mddbClient.setSchema.mockResolvedValue({ status: 'ok' });
    mddbClient.getSchema.mockResolvedValueOnce({ collection: 'blog', schema: '{broken', enabled: true });
    await renderPage();
    expect(editor().value).toBe('{broken');
    expect(editor().getAttribute('aria-invalid')).toBe('true');
    const save = screen.getByRole('button', { name: 'Save schema' });
    expect(save.disabled).toBe(true);

    fireEvent.change(editor(), { target: { value: '[1]' } });
    expect(screen.getByText(/A schema is a JSON object/)).toBeTruthy();
    fireEvent.change(editor(), { target: { value: '{ "required": ["title"] }' } });
    await act(async () => fireEvent.click(save));
    expect(mddbClient.setSchema).toHaveBeenCalledWith('blog', '{"required":["title"]}');
    expect(screen.getByRole('status').textContent).toMatch('Schema of blog saved');
    expect(mddbClient.listSchemas).toHaveBeenCalledTimes(2);

    mddbClient.setSchema.mockRejectedValueOnce(apiError(400, 'invalid schema: unknown rule'));
    fireEvent.change(editor(), { target: { value: '{"x":1}' } });
    expect(screen.queryByRole('status')).toBeNull();
    await act(async () => fireEvent.click(save));
    expect(screen.getByText('API Error (400): invalid schema: unknown rule')).toBeTruthy();
  });

  test('delete asks first, naming the collection, then turns validation off', async () => {
    mddbClient.deleteSchema.mockResolvedValue({ status: 'ok' });
    await renderPage();
    fireEvent.click(screen.getByRole('button', { name: 'Delete schema' }));
    const dialog = screen.getByRole('alertdialog', { name: 'Delete schema' });
    expect(within(dialog).getByText('blog')).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('alertdialog')).toBeNull();
    expect(mddbClient.deleteSchema).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Delete schema' }));
    await act(async () => fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Delete' })));
    expect(mddbClient.deleteSchema).toHaveBeenCalledWith('blog');
    expect(screen.queryByRole('alertdialog')).toBeNull();
    expect(editor().value).toBe('');
    expect(screen.getByText('Schema of blog deleted; validation is off.')).toBeTruthy();
  });
});

describe('SchemaValidateBox', () => {
  test('validates metadata and lists the errors and warnings', async () => {
    mddbClient.validateMeta
      .mockResolvedValueOnce({ valid: false, errors: ['field "category" value "x" not in allowed values'], warnings: ['unknown key "tag"'] })
      .mockResolvedValueOnce({ valid: true });
    await renderPage();
    const field = screen.getByLabelText('Metadata to check against blog');
    const validate = screen.getByRole('button', { name: 'Validate' });
    fireEvent.change(field, { target: { value: '"text"' } });
    expect(screen.getByText(/Metadata is a JSON object/)).toBeTruthy();
    expect(validate.disabled).toBe(true);
    fireEvent.change(field, { target: { value: '{"category":["x"]}' } });
    await act(async () => fireEvent.click(validate));
    expect(mddbClient.validateMeta).toHaveBeenCalledWith('blog', { category: ['x'] });
    expect(screen.getByText('Invalid: the server would refuse this document.')).toBeTruthy();
    expect(screen.getByText('Error: field "category" value "x" not in allowed values')).toBeTruthy();
    expect(screen.getByText('Warning: unknown key "tag"')).toBeTruthy();

    fireEvent.change(field, { target: { value: '' } });
    await act(async () => fireEvent.click(validate));
    expect(mddbClient.validateMeta).toHaveBeenLastCalledWith('blog', {});
    expect(screen.getByText('Valid: the server would accept this metadata.')).toBeTruthy();

    mddbClient.validateMeta.mockRejectedValueOnce(apiError(400, 'collection is required'));
    await act(async () => fireEvent.click(validate));
    expect(screen.getByText('API Error (400): collection is required')).toBeTruthy();
  });
});
