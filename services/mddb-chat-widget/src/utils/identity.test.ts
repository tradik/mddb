// #405: the identity token comes from window.MDDB_CHAT_CONFIG and nowhere else.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { identityTokenFrom } from './identity.ts';

test('a string token is used as it is', () => {
  assert.equal(identityTokenFrom({ identityToken: 'v1.a.b' }), 'v1.a.b');
});

test('no config, no token, or a token that is not a string means anonymous', () => {
  for (const config of [undefined, null, 'v1.a.b', {}, { identityToken: '' }, { identityToken: 42 }]) {
    assert.equal(identityTokenFrom(config), undefined, JSON.stringify(config));
  }
});
