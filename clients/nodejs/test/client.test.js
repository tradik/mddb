'use strict';

/**
 * TEST-001. The package had no tests and no importable client — requiring it
 * ran a demo against localhost. These start a real gRPC server in-process and
 * check what the client puts on the wire.
 */

const assert = require('node:assert/strict');
const { test, describe, before, after } = require('node:test');
const grpc = require('@grpc/grpc-js');

const { MddbClient, DEFAULT_ADDRESS, loadService, buildCredentials } = require('..');

/** Starts an in-process gRPC server implementing the handlers it is given. */
async function startServer(handlers) {
  const server = new grpc.Server();
  server.addService(loadService().service, handlers);

  const port = await new Promise((resolve, reject) => {
    server.bindAsync('127.0.0.1:0', grpc.ServerCredentials.createInsecure(), (err, p) =>
      err ? reject(err) : resolve(p)
    );
  });

  return { server, address: `127.0.0.1:${port}` };
}

describe('MddbClient', () => {
  let server;
  let address;
  let lastCall;

  before(async () => {
    const started = await startServer({
      Add: (call, cb) => {
        lastCall = { method: 'Add', request: call.request, metadata: call.metadata.getMap() };
        cb(null, {
          id: `${call.request.collection}|${call.request.key}|${call.request.lang}`,
          key: call.request.key,
          lang: call.request.lang,
          content_md: call.request.content_md,
          added_at: '1700000000',
        });
      },
      Get: (call, cb) => {
        lastCall = { method: 'Get', request: call.request, metadata: call.metadata.getMap() };
        if (call.request.key === 'missing') {
          cb({ code: grpc.status.NOT_FOUND, message: 'document not found' });
          return;
        }
        cb(null, { id: 'blog|post|en', key: 'post', content_md: '# Hello' });
      },
      Search: (call, cb) => {
        lastCall = { method: 'Search', request: call.request, metadata: call.metadata.getMap() };
        cb(null, { documents: [{ key: 'a' }, { key: 'b' }], total: 2 });
      },
      Stats: (call, cb) => {
        lastCall = { method: 'Stats', request: call.request, metadata: call.metadata.getMap() };
        cb(null, { total_documents: 42 });
      },
    });
    server = started.server;
    address = started.address;
  });

  after(() => {
    server.forceShutdown();
  });

  test('every unary RPC is exposed as a promise-returning method', () => {
    const client = new MddbClient(address);
    try {
      const definition = loadService().service;
      const unary = Object.entries(definition).filter(
        ([, m]) => !m.requestStream && !m.responseStream
      );

      assert.ok(unary.length > 50, `only ${unary.length} unary RPCs were found`);
      for (const [name] of unary) {
        assert.equal(typeof client[name], 'function', `${name} is not exposed`);
      }
    } finally {
      client.close();
    }
  });

  test('a streaming RPC is not promisified', () => {
    const client = new MddbClient(address);
    try {
      // Export streams; wrapping it in a promise would buffer the whole
      // export in memory, which is what streaming exists to avoid.
      assert.equal(client.Export, undefined);
      assert.equal(typeof client.stub.Export, 'function');
    } finally {
      client.close();
    }
  });

  test('Add sends the document and resolves with the server reply', async () => {
    const client = new MddbClient(address);
    try {
      const doc = await client.Add({
        collection: 'blog',
        key: 'post',
        lang: 'en',
        content_md: '# Hello',
        meta: { tag: { values: ['go', 'grpc'] } },
      });

      assert.equal(doc.id, 'blog|post|en');
      assert.equal(lastCall.method, 'Add');
      assert.equal(lastCall.request.collection, 'blog');
      assert.equal(lastCall.request.content_md, '# Hello');
      assert.deepEqual(lastCall.request.meta.tag.values, ['go', 'grpc']);
    } finally {
      client.close();
    }
  });

  test('a server error rejects rather than resolving with undefined', async () => {
    const client = new MddbClient(address);
    try {
      await assert.rejects(
        () => client.Get({ collection: 'blog', key: 'missing', lang: 'en' }),
        (err) => {
          assert.equal(err.code, grpc.status.NOT_FOUND);
          assert.match(err.message, /not found/);
          return true;
        }
      );
    } finally {
      client.close();
    }
  });

  test('an API key travels as metadata', async () => {
    const client = new MddbClient(address, { apiKey: 'mddb_secret' });
    try {
      await client.Stats({});
      assert.equal(lastCall.metadata['x-api-key'], 'mddb_secret');
      assert.equal(lastCall.metadata.authorization, undefined);
    } finally {
      client.close();
    }
  });

  test('a token travels as a bearer header', async () => {
    const client = new MddbClient(address, { token: 'jwt.value' });
    try {
      await client.Stats({});
      assert.equal(lastCall.metadata.authorization, 'Bearer jwt.value');
    } finally {
      client.close();
    }
  });

  test('no credentials means no credential metadata', async () => {
    const client = new MddbClient(address);
    try {
      await client.Stats({});
      assert.equal(lastCall.metadata['x-api-key'], undefined);
      assert.equal(lastCall.metadata.authorization, undefined);
    } finally {
      client.close();
    }
  });

  test('Search returns the documents the server sent', async () => {
    const client = new MddbClient(address);
    try {
      const res = await client.Search({ collection: 'blog', limit: 10 });
      assert.equal(res.total, 2);
      assert.deepEqual(res.documents.map((d) => d.key), ['a', 'b']);
      assert.equal(lastCall.request.limit, 10);
    } finally {
      client.close();
    }
  });

  test('waitForReady resolves against a live server', async () => {
    const client = new MddbClient(address);
    try {
      await client.waitForReady(3000);
    } finally {
      client.close();
    }
  });

  test('waitForReady rejects when nothing is listening', async () => {
    // Port 1 is reserved; nothing binds it.
    const client = new MddbClient('127.0.0.1:1');
    try {
      await assert.rejects(() => client.waitForReady(500));
    } finally {
      client.close();
    }
  });

  test('a call to an unreachable server rejects instead of hanging', async () => {
    const client = new MddbClient('127.0.0.1:1', { deadlineMs: 500 });
    try {
      await assert.rejects(() => client.Stats({}));
    } finally {
      client.close();
    }
  });

  test('a per-call deadline can be overridden', async () => {
    const client = new MddbClient(address, { deadlineMs: 0 });
    try {
      // deadlineMs: 0 disables the default; the call must still work.
      const res = await client.Stats({});
      assert.equal(res.total_documents, 42);
    } finally {
      client.close();
    }
  });
});

describe('configuration', () => {
  test('the default address is the local gRPC port', () => {
    assert.equal(DEFAULT_ADDRESS, 'localhost:11024');
  });

  test('credentials default to insecure and can be made secure', () => {
    // INT-011: insecure is for a loopback server only, and the default must
    // stay visible in a test rather than being an unstated assumption.
    const insecure = buildCredentials({});
    const secure = buildCredentials({ secure: true });

    assert.equal(insecure._isSecure(), false);
    assert.equal(secure._isSecure(), true);
  });

  test('the service definition loads once and carries every RPC', () => {
    const first = loadService();
    const second = loadService();

    assert.equal(first, second, 'the proto was reloaded instead of cached');
    assert.ok(Object.keys(first.service).length > 50);
    for (const rpc of ['Add', 'Get', 'Search', 'Export', 'FTS', 'VectorSearch']) {
      assert.ok(first.service[rpc], `${rpc} is missing from the service definition`);
    }
  });
});
