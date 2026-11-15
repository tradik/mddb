// @vitest-environment jsdom
// TemplatePicker (FE-018): built-in templates, the custom ones once loaded,
// picking either, and the note when `_templates` cannot be read.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { search: vi.fn() } }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { templates } = await import('../lib/markdown-templates');
const { default: TemplatePicker } = await import('./TemplatePicker');

beforeEach(() => {
  mddbClient.search.mockReset();
});
afterEach(cleanup);

async function renderPicker(onPick = vi.fn()) {
  await act(async () => {
    render(<TemplatePicker onPick={onPick} />);
  });
  return { onPick, select: screen.getByLabelText('Start from template') };
}

describe('TemplatePicker', () => {
  test('says it is loading the custom templates until they arrive', async () => {
    let resolve;
    mddbClient.search.mockReturnValue(new Promise((r) => { resolve = r; }));
    const onPick = vi.fn();
    render(<TemplatePicker onPick={onPick} />);
    expect(screen.getByRole('option', { name: 'Loading custom templates…' }).disabled).toBe(true);
    // A built-in template can be picked before the custom ones arrive.
    fireEvent.change(screen.getByLabelText('Start from template'), { target: { value: 'builtin:api' } });
    expect(onPick).toHaveBeenCalledWith({ contentMd: templates.api, meta: {} });
    await act(async () => resolve({ documents: [] }));
    expect(screen.queryByRole('option', { name: 'Loading custom templates…' })).toBeNull();
    expect(screen.queryByRole('group', { name: 'Custom (_templates)' })).toBeNull();
  });

  test('lists built-in and custom templates; picking one hands over content and metadata', async () => {
    mddbClient.search.mockResolvedValue({ documents: [{ key: 'meeting', lang: 'en', contentMd: '# Meeting', meta: { type: ['notes'] } }] });
    const { onPick, select } = await renderPicker();
    expect(screen.getByRole('group', { name: 'Built-in' })).toBeTruthy();
    expect(screen.getByRole('group', { name: 'Custom (_templates)' })).toBeTruthy();

    fireEvent.change(select, { target: { value: 'custom:meeting:en' } });
    expect(onPick).toHaveBeenLastCalledWith({ contentMd: '# Meeting', meta: { type: ['notes'] } });
    expect(select.value).toBe('custom:meeting:en');

    fireEvent.change(select, { target: { value: 'builtin:readme' } });
    expect(onPick).toHaveBeenLastCalledWith({ contentMd: templates.readme, meta: {} });

    fireEvent.change(select, { target: { value: '' } });
    expect(onPick).toHaveBeenCalledTimes(2);
  });

  test('without read permission only the built-in ones are offered, with a note', async () => {
    mddbClient.search.mockRejectedValue(Object.assign(new Error('API Error (403): forbidden'), { status: 403 }));
    await renderPicker();
    expect(screen.getByRole('status').textContent).toBe('Custom templates are not shown: you have no read permission on _templates.');
    expect(screen.getAllByRole('option')).toHaveLength(6);
  });

  test('another failure is named', async () => {
    mddbClient.search.mockRejectedValue(new Error('Network down'));
    const { onPick, select } = await renderPicker();
    expect(screen.getByRole('status').textContent).toBe('Custom templates could not be loaded: Network down');
    fireEvent.change(select, { target: { value: 'builtin:blog' } });
    expect(onPick).toHaveBeenCalledWith({ contentMd: templates.blog, meta: {} });
  });

  test('an answer after unmounting is ignored', async () => {
    let resolve;
    mddbClient.search.mockReturnValue(new Promise((r) => { resolve = r; }));
    const { unmount } = render(<TemplatePicker onPick={() => {}} />);
    unmount();
    await act(async () => resolve({ documents: [] }));
    expect(mddbClient.search).toHaveBeenCalledTimes(1);
  });
});
