// corpus-packs (FE-022): grouping the listed collections into packs, the
// build bodies, the file names and the plain-words refusals.
import { describe, test, expect } from 'vitest';
import { formatBytes, groupPacks, buildRequest, rebuildRequest, packFileName, explainPackError } from './corpus-packs';

describe('corpus-packs', () => {
  test.each([[0, ''], [undefined, ''], [512, '512 B'], [1536, '1.5 KB'], [5 * 1024 ** 3, '5 GB'], [3 * 1024 ** 5, '3072 TB']])('formatBytes(%s)', (n, text) => {
    expect(formatBytes(n)).toBe(text);
  });

  test('groupPacks puts a pack of several back together, sorted', () => {
    const packs = groupPacks([
      { collection: 'z', readOnly: true, corpus: { name: 'zz', license: 'MIT' } },
      { collection: 'notes', readOnly: false, corpus: { name: 'kb', license: 'MIT', packLicense: 'CC0-1.0', docCount: 2, bytes: 5, vectorsDisabled: true, sources: [{ repo: 'r' }] } },
      { collection: 'docs', readOnly: true, corpus: { name: 'kb', license: 'CC0-1.0', packLicense: 'CC0-1.0', docCount: 3, bytes: 7 } },
      { collection: 'legacy', readOnly: true, corpus: {} },
    ]);
    expect(packs.map((p) => p.name)).toEqual(['kb', 'legacy', 'zz']);
    const [kb, legacy, zz] = packs;
    expect(kb).toMatchObject({ license: 'CC0-1.0', several: true, readOnly: false, vectorsDisabled: true, docCount: 5, bytes: 12, sources: [{ repo: 'r' }] });
    expect(kb.collections.map((c) => c.name)).toEqual(['notes', 'docs']);
    expect(legacy).toMatchObject({ docCount: 0, several: false, version: '', description: '' });
    expect(zz.collections[0]).toEqual({ name: 'z', license: 'MIT', docCount: 0, sources: [] });
    expect(groupPacks()).toEqual([]);
  });

  test('buildRequest: one collection, several, and sources', () => {
    expect(buildRequest({ collections: ['a'], license: ' MIT ', version: '', sources: [{ repo: ' ' }] })).toEqual({ collection: 'a', license: 'MIT' });
    expect(buildRequest({
      collections: ['a', 'b', 'c'], name: ' kb ', license: 'MIT', licenses: { b: 'Apache-2.0', c: 'MIT' },
      sources: [{ repo: 'https://r', commit: '', license: '' }],
    })).toEqual({ name: 'kb', collections: ['a', { name: 'b', license: 'Apache-2.0' }, 'c'], license: 'MIT', sources: [{ repo: 'https://r', license: 'MIT' }] });
  });

  test('rebuildRequest makes an installed pack again', () => {
    const [single, multi] = [
      groupPacks([{ collection: 'rfc', corpus: { name: 'rfc', license: 'MIT', version: '1', sources: [{ repo: 'r', license: 'MIT' }] } }])[0],
      groupPacks([{ collection: 'a', corpus: { name: 'kb', license: 'MIT', packLicense: 'MIT', description: 'd' } }])[0],
    ];
    expect(rebuildRequest(single)).toEqual({ collection: 'rfc', license: 'MIT', version: '1', sources: [{ repo: 'r', license: 'MIT' }] });
    expect(rebuildRequest(multi)).toEqual({ name: 'kb', license: 'MIT', description: 'd', collections: [{ name: 'a', license: 'MIT' }] });
  });

  test('packFileName', () => {
    expect(packFileName({ collection: 'a' })).toBe('a.mddbpack');
    expect(packFileName({ name: 'kb', version: '2' })).toBe('kb-2.mddbpack');
  });

  test.each([
    [{ status: 409 }, 'already on this server'],
    [{ message: 'a pack named "kb" is already installed' }, 'already on this server'],
    [{ body: { error: 'source "x" has no licence' } }, 'under which licence'],
    [{ message: 'unsupported pack format 3' }, 'not a pack this server can read'],
    [{ message: 'something else' }, null],
    [undefined, null],
  ])('explainPackError(%o)', (err, text) => {
    const got = explainPackError(err);
    if (text === null) expect(got).toBeNull();
    else expect(got).toContain(text);
  });
});
