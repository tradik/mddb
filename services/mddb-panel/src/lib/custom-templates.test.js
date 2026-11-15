// custom-templates (FE-018): built-in templates plus the team's own from
// the `_templates` collection.
import { describe, test, expect, vi } from 'vitest';

vi.mock('./mddb-client', () => ({ default: { search: vi.fn(), addDocument: vi.fn() } }));

const { default: mddbClient } = await import('./mddb-client');
const { templates, templateLabels, getTemplate, getTemplateList } = await import('./markdown-templates');
const {
  templateCollection, templateLimit, builtinTemplates, toTemplate, loadCustomTemplates, saveTemplate,
} = await import('./custom-templates');

describe('markdown-templates', () => {
  test('every built-in template has a label, and lookups work', () => {
    expect(Object.keys(templateLabels).sort()).toEqual(getTemplateList().sort());
    expect(getTemplate('readme')).toBe(templates.readme);
    expect(getTemplate('nope')).toBe('');
  });
});

describe('builtinTemplates', () => {
  test('one picker entry per built-in template, without metadata', () => {
    const list = builtinTemplates();
    expect(list.map((t) => t.id)).toEqual(['builtin:blog', 'builtin:documentation', 'builtin:readme', 'builtin:api', 'builtin:changelog']);
    expect(list[2]).toEqual({ id: 'builtin:readme', name: 'README', contentMd: templates.readme, meta: {} });
  });
});

describe('toTemplate', () => {
  test('a template document becomes an entry; metadata values become lists', () => {
    expect(toTemplate({ key: 'meeting', lang: 'en', contentMd: '# M', meta: { tags: ['x'], owner: 'ann', n: 2 } })).toEqual({
      id: 'custom:meeting:en', name: 'meeting', lang: 'en', contentMd: '# M', meta: { tags: ['x'], owner: ['ann'], n: ['2'] },
    });
    expect(toTemplate({ key: 'empty', lang: 'pl' })).toEqual({ id: 'custom:empty:pl', name: 'empty', lang: 'pl', contentMd: '', meta: {} });
  });
});

describe('loadCustomTemplates', () => {
  test('reads _templates by key', async () => {
    mddbClient.search.mockResolvedValue({ documents: [{ key: 'a', lang: 'en', contentMd: 'x' }], totalCount: 1 });
    const result = await loadCustomTemplates();
    expect(mddbClient.search).toHaveBeenCalledWith({ collection: '_templates', sort: 'key', asc: true, limit: templateLimit });
    expect(result).toEqual({ templates: [toTemplate({ key: 'a', lang: 'en', contentMd: 'x' })], error: null });
  });

  test('a refused or failed read gives no templates and the error', async () => {
    const error = Object.assign(new Error('API Error (403): forbidden'), { status: 403 });
    mddbClient.search.mockRejectedValue(error);
    expect(await loadCustomTemplates()).toEqual({ templates: [], error });
  });
});

describe('saveTemplate', () => {
  const document = { collection: 'docs', key: 'guide', lang: 'en', meta: { tags: ['a'] }, contentMd: '# G' };

  test('writes the document to _templates under the trimmed name, with its metadata', async () => {
    mddbClient.addDocument.mockResolvedValue({});
    await saveTemplate({ name: '  guide-tpl ', document, includeMeta: true });
    expect(mddbClient.addDocument).toHaveBeenCalledWith({ collection: templateCollection, key: 'guide-tpl', lang: 'en', meta: { tags: ['a'] }, contentMd: '# G' });
  });

  test('without metadata when asked, and with empty fallbacks', async () => {
    await saveTemplate({ name: 'x', document, includeMeta: false });
    expect(mddbClient.addDocument).toHaveBeenLastCalledWith(expect.objectContaining({ meta: {} }));
    await saveTemplate({ name: 'y', document: { lang: 'en' }, includeMeta: true });
    expect(mddbClient.addDocument).toHaveBeenLastCalledWith({ collection: '_templates', key: 'y', lang: 'en', meta: {}, contentMd: '' });
  });
});
