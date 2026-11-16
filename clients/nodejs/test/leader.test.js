'use strict';

/**
 * GO-051: following the cluster leader. A fake follower refuses writes the
 * way mddbd does — FAILED_PRECONDITION with a google.rpc.ErrorInfo detail in
 * `grpc-status-details-bin` — and a fake leader accepts them. The detail is
 * encoded with protobufjs (already installed with the proto loader), so the
 * client's hand-written decoder is checked against the real wire format.
 */

const assert = require('node:assert/strict');
const { test, describe, after } = require('node:test');
const grpc = require('@grpc/grpc-js');
const protobuf = require('protobufjs');

const {
  MddbClient,
  NO_LEADER,
  NO_LEADER_RETRIES,
  NO_LEADER_WAIT_MS,
  NOT_LEADER,
  leaderInfo,
  loadService,
} = require('..');
const { annotate, fields, fromAny, fromDetails, grpcTarget } = require('../leader');

const root = protobuf.Root.fromJSON({
  nested: {
    google: {
      nested: {
        rpc: {
          nested: {
            Status: {
              fields: {
                code: { type: 'int32', id: 1 },
                message: { type: 'string', id: 2 },
                details: { rule: 'repeated', type: 'Any', id: 3 },
              },
            },
            Any: { fields: { type_url: { type: 'string', id: 1 }, value: { type: 'bytes', id: 2 } } },
            ErrorInfo: {
              fields: {
                reason: { type: 'string', id: 1 },
                domain: { type: 'string', id: 2 },
                metadata: { keyType: 'string', type: 'string', id: 3 },
              },
            },
          },
        },
      },
    },
  },
});
const Status = root.lookupType('google.rpc.Status');
const ErrorInfo = root.lookupType('google.rpc.ErrorInfo');
const Any = root.lookupType('google.rpc.Any');
const ERROR_INFO_TYPE = 'type.googleapis.com/google.rpc.ErrorInfo';

function anyOf(reason, metadata) {
  return {
    type_url: ERROR_INFO_TYPE,
    value: ErrorInfo.encode({ reason, domain: 'mddb', metadata }).finish(),
  };
}

function statusDetails(code, message, reason, metadata) {
  const noise = { type_url: 'type.googleapis.com/google.rpc.RetryInfo', value: Buffer.from([0x0a, 0x00]) };
  return Buffer.from(Status.encode({ code, message, details: [noise, anyOf(reason, metadata)] }).finish());
}

function refusal(code, message, trailer) {
  const metadata = new grpc.Metadata();
  if (trailer) metadata.set('grpc-status-details-bin', trailer);
  return { code, details: message, metadata };
}

function notLeader(leaderAddr, extra = {}) {
  const trailer = statusDetails(grpc.status.FAILED_PRECONDITION, 'not the leader', NOT_LEADER, {
    leader_addr: leaderAddr,
    leader_id: 'n2',
    term: '5',
    ...extra,
  });
  return () => refusal(grpc.status.FAILED_PRECONDITION, 'not the leader; leader is n2', trailer);
}

function noLeader(times) {
  const trailer = statusDetails(grpc.status.UNAVAILABLE, 'no leader', NO_LEADER, { term: '7' });
  return (hit) =>
    hit <= times ? refusal(grpc.status.UNAVAILABLE, 'no leader: an election is in progress', trailer) : null;
}

const servers = [];

/** Starts a fake node; `node.refuse(hit)` returns an error or null to accept. */
async function startNode(refuse = () => null) {
  const node = { refuse, adds: [], gets: 0 };
  const server = new grpc.Server();
  server.addService(loadService().service, {
    Add: (call, cb) => {
      node.adds.push(call.request.content_md);
      const err = node.refuse(node.adds.length);
      if (err) {
        cb(err);
        return;
      }
      cb(null, { key: call.request.key, content_md: call.request.content_md });
    },
    Get: (call, cb) => {
      node.gets += 1;
      cb(null, { key: call.request.key });
    },
  });
  const port = await new Promise((resolve, reject) => {
    server.bindAsync('127.0.0.1:0', grpc.ServerCredentials.createInsecure(), (err, p) =>
      err ? reject(err) : resolve(p)
    );
  });
  servers.push(server);
  node.address = `127.0.0.1:${port}`;
  return node;
}

after(() => {
  for (const server of servers) server.forceShutdown();
});

const to = (target) => () => target;
const add = (client, body = '# hi') =>
  client.Add({ collection: 'blog', key: 'k', lang: 'en', content_md: body });

describe('followLeader off', () => {
  test('a refused write carries the leader on the error', async () => {
    const leader = await startNode();
    const follower = await startNode(notLeader('http://10.0.0.2:11023'));
    const client = new MddbClient(follower.address, { leaderResolver: to(leader.address) });
    try {
      await assert.rejects(add(client), (err) => {
        assert.equal(err.code, grpc.status.FAILED_PRECONDITION);
        assert.equal(err.leaderAddr, 'http://10.0.0.2:11023');
        assert.equal(err.leaderId, 'n2');
        assert.equal(err.term, 5);
        assert.equal(err.leaderReason, NOT_LEADER);
        assert.deepEqual(leaderInfo(err), {
          reason: NOT_LEADER,
          leaderAddr: 'http://10.0.0.2:11023',
          leaderId: 'n2',
          term: 5,
          leaderGrpc: '',
        });
        return true;
      });
      assert.deepEqual(leader.adds, []);
      assert.equal(client.leader, null);
    } finally {
      client.close();
    }
  });

  test('no leader is not retried', async () => {
    const node = await startNode(noLeader(100));
    const client = new MddbClient(node.address);
    try {
      await assert.rejects(add(client), (err) => err.term === 7 && err.leaderReason === NO_LEADER);
      assert.equal(node.adds.length, 1);
    } finally {
      client.close();
    }
  });
});

describe('followLeader on', () => {
  test('a write follows the leader and remembers it; reads stay home', async () => {
    const leader = await startNode();
    const follower = await startNode(notLeader('http://10.0.0.2:11023'));
    const client = new MddbClient(follower.address, {
      followLeader: true,
      leaderResolver: to(leader.address),
    });
    try {
      assert.equal((await add(client, 'first')).content_md, 'first');
      assert.equal(client.leader, leader.address);
      assert.deepEqual(follower.adds, ['first']);
      assert.deepEqual(leader.adds, ['first']);

      await add(client, 'second');
      assert.deepEqual(follower.adds, ['first']);
      assert.deepEqual(leader.adds, ['first', 'second']);

      await client.Get({ collection: 'blog', key: 'k' });
      assert.equal(follower.gets, 1);
      assert.equal(leader.gets, 0);

      // A new leader named by the old one replaces it.
      const newer = await startNode();
      leader.refuse = notLeader('http://10.0.0.3:11023');
      client._leaderResolver = to(newer.address);
      await add(client, 'third');
      assert.equal(client.leader, newer.address);
      assert.deepEqual(newer.adds, ['third']);
    } finally {
      client.close();
    }
  });

  test('only one redirect per call', async () => {
    const second = await startNode(notLeader('http://10.0.0.3:11023'));
    const first = await startNode(notLeader('http://10.0.0.2:11023'));
    const client = new MddbClient(first.address, { followLeader: true, leaderResolver: to(second.address) });
    try {
      await assert.rejects(add(client), (err) => err.leaderAddr === 'http://10.0.0.3:11023');
      assert.equal(first.adds.length, 1);
      assert.equal(second.adds.length, 1);
    } finally {
      client.close();
    }
  });

  for (const addr of ['', 'ftp://10.0.0.2', '10.0.0.2:11023']) {
    test(`an unusable leader address (${JSON.stringify(addr)}) is not followed`, async () => {
      const follower = await startNode(notLeader(addr));
      const client = new MddbClient(follower.address, { followLeader: true });
      try {
        await assert.rejects(add(client), (err) => err.code === grpc.status.FAILED_PRECONDITION);
        assert.equal(follower.adds.length, 1);
        assert.equal(client.leader, null);
      } finally {
        client.close();
      }
    });
  }

  test('no leader is retried a bounded number of times', async () => {
    const node = await startNode(noLeader(2));
    const client = new MddbClient(node.address, { followLeader: true });
    const waits = [];
    client._sleep = async (ms) => waits.push(ms);
    try {
      await add(client);
      assert.deepEqual(waits, [NO_LEADER_WAIT_MS, NO_LEADER_WAIT_MS]);
      assert.equal(node.adds.length, 3);

      node.refuse = noLeader(100);
      node.adds.length = 0;
      await assert.rejects(add(client), (err) => err.leaderReason === NO_LEADER);
      assert.equal(node.adds.length, NO_LEADER_RETRIES + 1);
    } finally {
      client.close();
    }
  });

  test('the default election wait sleeps', async () => {
    const client = new MddbClient('127.0.0.1:1');
    const started = Date.now();
    await client._sleep(5);
    assert.ok(Date.now() - started >= 4);
    client.close();
  });

  test('an unreachable leader is forgotten', async () => {
    const follower = await startNode(notLeader('http://10.0.0.2:11023'));
    const client = new MddbClient(follower.address, {
      followLeader: true,
      leaderResolver: to('127.0.0.1:1'),
      deadlineMs: 2000,
    });
    try {
      await assert.rejects(add(client), (err) => err.code === grpc.status.UNAVAILABLE);
      assert.equal(client.leader, null);

      // Any other error of the leader keeps it, as does a failure at home.
      const stub = client._follow('Add', { leaderAddr: 'http://x' });
      client._forgetLeader(stub, { code: grpc.status.INTERNAL });
      client._forgetLeader(client.stub, { code: grpc.status.UNAVAILABLE });
      assert.equal(client.leader, '127.0.0.1:1');
    } finally {
      client.close();
    }
  });

  test('an ordinary error is rejected unchanged', async () => {
    const node = await startNode(() => ({ code: grpc.status.INVALID_ARGUMENT, details: 'bad' }));
    const client = new MddbClient(node.address, { followLeader: true });
    try {
      await assert.rejects(add(client), (err) => err.code === grpc.status.INVALID_ARGUMENT && !('leaderAddr' in err));
    } finally {
      client.close();
    }
  });

  test('a method a subclass defines is not replaced by the RPC of that name', () => {
    class Custom extends MddbClient {
      Stats() {
        return 'custom';
      }
    }
    const client = new Custom('127.0.0.1:1');
    assert.equal(client.Stats(), 'custom');
    client.close();
  });

  test("the server's gRPC address is preferred", async () => {
    const leader = await startNode();
    const follower = await startNode(notLeader('http://10.0.0.2:11023', { leader_grpc: leader.address }));
    const client = new MddbClient(follower.address, { followLeader: true });
    try {
      await add(client);
      assert.equal(client.leader, leader.address);
      assert.deepEqual(leader.adds, ['# hi']);
    } finally {
      client.close();
    }
  });

  test("a resolver wins over the server's gRPC address", async () => {
    const leader = await startNode();
    const follower = await startNode(notLeader('http://10.0.0.2:11023', { leader_grpc: '127.0.0.1:1' }));
    const client = new MddbClient(follower.address, { followLeader: true, leaderResolver: to(leader.address) });
    try {
      await add(client);
      assert.deepEqual(leader.adds, ['# hi']);
    } finally {
      client.close();
    }
  });

  test('without a gRPC address the configured port is used', () => {
    const client = new MddbClient('node1:12000', { followLeader: true });
    assert.equal(client._leaderTarget({ leaderAddr: 'http://10.0.0.2:11023', leaderGrpc: '' }), '10.0.0.2:12000');
    assert.equal(client._leaderTarget({ leaderAddr: 'http://x', leaderGrpc: 'x:9' }), 'x:9');
    assert.equal(client._leaderTarget({ leaderAddr: '', leaderGrpc: '' }), null);
    client.close();

    const resolved = new MddbClient('node1:12000', { followLeader: true, leaderResolver: to('r:1') });
    assert.equal(resolved._leaderTarget({ leaderAddr: '', leaderGrpc: 'x:9' }), null);
    resolved.close();
  });
});

describe('the decoder', () => {
  test('without a detail the message still identifies a refusal', () => {
    const err = { code: grpc.status.FAILED_PRECONDITION, details: 'not the leader' };
    assert.deepEqual(annotate(err), { reason: NOT_LEADER, leaderAddr: '', leaderId: '', term: null, leaderGrpc: '' });
    assert.equal(err.leaderAddr, '');
    assert.equal(leaderInfo({ code: grpc.status.FAILED_PRECONDITION }), null);
    assert.equal(leaderInfo({ code: grpc.status.UNAVAILABLE, details: 'not the leader' }), null);
    assert.equal(leaderInfo(new Error('x')), null);
    assert.equal(leaderInfo(null), null);
    assert.equal(annotate(new Error('x')), null);
  });

  test('other trailers are ignored', () => {
    const metadata = new grpc.Metadata();
    metadata.set('other-bin', Buffer.from('x'));
    assert.equal(leaderInfo({ code: grpc.status.ABORTED, details: 'x', metadata }), null);
    assert.equal(leaderInfo({ code: grpc.status.ABORTED, details: 'x', metadata: {} }), null);
  });

  test('what cannot be read is rejected', () => {
    assert.equal(fromDetails(Buffer.alloc(0)), null);
    assert.equal(fromDetails(null), null);
    assert.equal(fromDetails(Buffer.from([0x1a, 0x05, 0x61, 0x62])), null); // truncated length
    assert.equal(fromDetails(Buffer.from([0x80])), null); // truncated varint
    assert.equal(fromDetails(Buffer.from([0x0b])), null); // group wire type
    assert.equal(fromDetails(Buffer.from([0x08, 0x05, 0x1a, 0x00])), null); // an empty Any
    // Fixed-width fields are skipped.
    const fixed = Buffer.concat([Buffer.from([0x09]), Buffer.alloc(8), Buffer.from([0x15]), Buffer.alloc(4)]);
    assert.deepEqual([...fields(fixed)].map(([n]) => n), [1, 2]);
    // A multi-byte varint decodes.
    assert.deepEqual([...fields(Buffer.from([0x08, 0xac, 0x02]))][0][2], 300);
  });

  test('a known reason is required', () => {
    assert.equal(fromAny(Any.encode(anyOf('QUOTA', { leader_addr: 'http://x' })).finish()), null);
    const odd = Any.encode(anyOf(NO_LEADER, { term: 'soon' })).finish();
    // A varint field inside ErrorInfo is skipped.
    const info = Buffer.concat([Buffer.from(ErrorInfo.encode({ reason: NO_LEADER }).finish()), Buffer.from([0x20, 0x01])]);
    const withVarint = Any.encode({ type_url: ERROR_INFO_TYPE, value: info }).finish();
    assert.deepEqual(fromAny(odd), { reason: NO_LEADER, leaderAddr: '', leaderId: '', term: null, leaderGrpc: '' });
    assert.deepEqual(fromAny(withVarint), { reason: NO_LEADER, leaderAddr: '', leaderId: '', term: null, leaderGrpc: '' });
    // An Any without a value, and a map entry without key or value.
    assert.equal(fromAny(Any.encode({ type_url: ERROR_INFO_TYPE }).finish()), null);
    const bareEntry = Buffer.from([0x0a, 0x09, ...Buffer.from(NO_LEADER), 0x1a, 0x00]);
    assert.equal(fromAny(Any.encode({ type_url: ERROR_INFO_TYPE, value: bareEntry }).finish()).reason, NO_LEADER);
    assert.equal(fromAny(Buffer.alloc(0)), null);
  });

  test('grpcTarget', () => {
    assert.equal(grpcTarget('https://leader.internal:11023/', 'node1:11024'), 'leader.internal:11024');
    assert.equal(grpcTarget('http://[::1]:11023', 'dns:///node1:9000'), '[::1]:9000');
    assert.equal(grpcTarget('http://10.0.0.2', 'node1'), '10.0.0.2:11024');
    assert.equal(grpcTarget('http://', 'node1:1'), null);
    assert.equal(grpcTarget('mailto:x@y', 'node1:1'), null);
  });
});
