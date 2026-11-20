// The docs deploy pins ssg twice: the action (`uses: spagu/ssg@<sha> # vX`)
// and the binary it downloads (`version: 'vX'`). Dependabot bumps only the
// first. When they drifted (action v1.9.6, binary v1.8.82) the site built
// locally on the newer ssg and failed in CI on a config key the old binary
// did not know. This keeps the two equal.
// Run with: node --test ssg-pin.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const workflow = readFileSync(join(here, '..', '..', '.github', 'workflows', 'deploy-docs.yml'), 'utf8');

test('the ssg action and the ssg binary in deploy-docs.yml are the same version', () => {
  const action = workflow.match(/uses: spagu\/ssg@[0-9a-f]{40} # (v[\d.]+)/)?.[1];
  const binary = workflow.match(/^\s+version: '(v[\d.]+)'/m)?.[1];
  assert.ok(action, 'no pinned spagu/ssg action');
  assert.ok(binary, 'no ssg version input');
  assert.equal(binary, action, `action ${action} downloads binary ${binary}`);
});
