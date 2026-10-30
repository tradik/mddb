// #376 — the download card linked only the amd64 .deb/.rpm, although every
// release publishes linux-arm64 ones too. These tests derive the asset names
// from .github/workflows/release.yml (the server build matrix and its file
// names) and check the card against them both ways: every link names an
// asset the release publishes, and every published mddbd asset has a link.
// Run with: node --test download.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const card = readFileSync(join(here, 'index-section-download.html'), 'utf8');
const releaseYml = readFileSync(join(here, '..', '..', '.github', 'workflows', 'release.yml'), 'utf8');

const versionVar = '{{$.Vars.mddbVersion}}';
const downloadPrefix = `https://github.com/tradik/mddb/releases/download/v${versionVar}/`;

/**
 * The build-server job's matrix: each platform and whether it builds deb/rpm.
 */
function serverPlatforms() {
  const job = releaseYml.indexOf('\n  build-server:\n');
  assert.ok(job >= 0, 'release.yml: no build-server job; the guard is stale');
  const matrix = releaseYml.slice(job, releaseYml.indexOf('\n    steps:', job));
  const entries = [...matrix.matchAll(/platform: (\S+)\s+package_formats: (\S+)/g)];
  assert.ok(entries.length > 0, 'release.yml: no server build matrix found; the guard is stale');
  return entries.map(([, platform, formats]) => ({ platform, packages: formats === 'deb,rpm' }));
}

/** The mddbd asset names one release publishes, with the version as the template variable. */
function publishedServerAssets() {
  // VERSION is the tag (v2.18.10) and names the tarball; VERSION_NUM drops the
  // "v" and names the packages. Fail loudly if release.yml renames either.
  for (const naming of [
    'tar czf mddbd-${VERSION}-${{ matrix.platform }}.tar.gz',
    'dist/mddbd-${VERSION_NUM}-${{ matrix.platform }}.deb',
    'dist/mddbd-${VERSION_NUM}-${{ matrix.platform }}.rpm',
  ]) {
    assert.ok(releaseYml.includes(naming), `release.yml no longer names assets as "${naming}"; update this guard`);
  }
  const names = [];
  for (const { platform, packages } of serverPlatforms()) {
    names.push(`mddbd-v${versionVar}-${platform}.tar.gz`);
    if (packages) {
      names.push(`mddbd-${versionVar}-${platform}.deb`, `mddbd-${versionVar}-${platform}.rpm`);
    }
  }
  return names;
}

/** The release asset names the card links to. */
function linkedAssets() {
  return [...card.matchAll(/href="https:\/\/github\.com\/tradik\/mddb\/releases\/download\/([^"]+)"/g)].map(
    ([, rest]) => {
      const href = `https://github.com/tradik/mddb/releases/download/${rest}`;
      assert.ok(href.startsWith(downloadPrefix), `${href} does not use the v${versionVar} release tag`);
      return href.slice(downloadPrefix.length);
    },
  );
}

test('#376: every download link names an asset the release publishes', () => {
  const published = new Set(publishedServerAssets());
  const linked = linkedAssets();
  assert.ok(linked.length > 0, 'the download card has no release links');
  for (const name of linked) {
    assert.ok(published.has(name), `${name} is not an asset release.yml publishes`);
  }
});

test('#376: every published mddbd asset has a download link, linux-arm64 .deb/.rpm included', () => {
  const linked = new Set(linkedAssets());
  for (const name of publishedServerAssets()) {
    assert.ok(linked.has(name), `the download card has no link to ${name}`);
  }
});

test('#376: no download link is listed twice', () => {
  const linked = linkedAssets();
  assert.equal(new Set(linked).size, linked.length, `duplicate links: ${linked}`);
});
