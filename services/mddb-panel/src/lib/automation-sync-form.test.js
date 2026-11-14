// automation-sync-form (2.19.5, #421): a git repository as a synced source —
// its payload (globs as lists, the forge only for a self-hosted host, the
// secrets by name), reading it back into the form, and its one-line source.
import { describe, test, expect } from 'vitest';
import { EMPTY_SYNC, buildSync, needsForge, shortCommit, syncSourceText, syncToForm } from './automation-sync-form';

const git = {
  ...EMPTY_SYNC,
  source: 'git',
  collection: 'docs',
  url: ' https://git.example.com/acme/docs.git ',
  ref: ' latest-release ',
  include: 'docs/*.md\n*.go, README.md\n\n',
  exclude: 'testdata/**',
  forge: 'gitea',
  tokenSecretRef: 'forge-token',
  hookSecretRef: 'hook-key',
  adopt: true,
  prune: true,
};

describe('git sync payload', () => {
  test('globs become lists, the forge goes with a self-hosted host, secrets go by name', () => {
    expect(buildSync(git)).toEqual({
      source: 'git', collection: 'docs', lang: 'en', prune: true,
      url: 'https://git.example.com/acme/docs.git', ref: 'latest-release', forge: 'gitea',
      tokenSecretRef: 'forge-token', hookSecretRef: 'hook-key', adopt: true,
      include: ['docs/*.md', '*.go', 'README.md'], exclude: ['testdata/**'],
    });
  });

  test('a known host sends no forge; blanks are left out', () => {
    const out = buildSync({ ...EMPTY_SYNC, source: 'git', collection: 'd', url: 'https://github.com/a/b', forge: 'gitea' });
    expect(out).toEqual({ source: 'git', collection: 'd', lang: 'en', url: 'https://github.com/a/b' });
  });

  test('a stored git sync reads back into the form and round-trips', () => {
    const form = syncToForm(buildSync(git));
    expect(form).toMatchObject({
      ref: 'latest-release', include: 'docs/*.md\n*.go\nREADME.md', exclude: 'testdata/**',
      forge: 'gitea', tokenSecretRef: 'forge-token', hookSecretRef: 'hook-key', adopt: true,
    });
    expect(buildSync(form)).toEqual(buildSync(git));
    expect(syncToForm({ source: 'git', collection: 'd', lang: 'en' })).toMatchObject({
      ref: '', include: '', exclude: '', forge: '', tokenSecretRef: '', hookSecretRef: '', adopt: false,
    });
  });
});

describe('git helpers', () => {
  test.each([
    ['https://github.com/a/b', false],
    ['https://GitLab.com/a/b', false],
    ['https://codeberg.org/a/b', false],
    ['https://git.example.com/a/b', true],
    ['not a url yet', false],
  ])('needsForge(%s) is %s', (url, want) => {
    expect(needsForge(url)).toBe(want);
  });

  test('the source line names the repository and its ref', () => {
    expect(syncSourceText({ source: 'git', url: 'https://github.com/a/b', ref: 'v1' })).toBe('https://github.com/a/b @ v1');
    expect(syncSourceText({ source: 'git', url: 'https://github.com/a/b' })).toBe('https://github.com/a/b @ default branch');
  });

  test('a commit is shown by its first seven characters', () => {
    expect(shortCommit('1a2b3c4d5e6f')).toBe('1a2b3c4');
    expect(shortCommit(undefined)).toBe('');
  });
});
