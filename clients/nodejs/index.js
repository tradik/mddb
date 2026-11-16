'use strict';

/**
 * MDDB Node.js client.
 *
 * TEST-001: this package shipped with `"main": "example.js"`, so requiring
 * `@tradik/mddb-client` executed a demo that connected to localhost and wrote
 * documents. There was no importable client at all, and therefore nothing a
 * test could exercise. The example is now an example, and this is the client.
 *
 * Every unary RPC in mddb.proto is exposed as a promise-returning method with
 * the same name — Add, Get, Search, and the seventy-odd others. They are
 * generated from the service definition rather than written out, so a new RPC
 * is usable here the moment the proto carries it.
 */

const path = require('node:path');
const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');
const { NOT_LEADER, NO_LEADER, annotate, grpcTarget, leaderInfo } = require('./leader');

const PROTO_PATH = path.join(__dirname, 'proto', 'mddb.proto');

const PROTO_OPTIONS = {
  keepCase: true,
  longs: String,
  enums: String,
  defaults: true,
  oneofs: true,
};

/** Default gRPC port of a local mddbd. */
const DEFAULT_ADDRESS = 'localhost:11024';

/**
 * GO-051: how often, and how long apart, a write refused while a cluster
 * elects its leader is tried again when `followLeader` is on.
 */
const NO_LEADER_RETRIES = 3;
const NO_LEADER_WAIT_MS = 1000;

let cachedService = null;

/** Loads the service definition once; the file does not change at runtime. */
function loadService() {
  if (!cachedService) {
    cachedService = grpc.loadPackageDefinition(
      protoLoader.loadSync(PROTO_PATH, PROTO_OPTIONS)
    ).mddb.MDDB;
  }
  return cachedService;
}

/**
 * Credentials for the channel.
 *
 * INT-011: insecure is the default because the server's own default is a
 * loopback port with no TLS. Anything reachable from another machine must pass
 * `secure: true`, or the API key travels in cleartext.
 */
function buildCredentials({ secure = false, rootCerts = null } = {}) {
  return secure
    ? grpc.credentials.createSsl(rootCerts)
    : grpc.credentials.createInsecure();
}

class MddbClient {
  /**
   * @param {string} address host:port of the mddbd gRPC listener.
   * @param {object} [options]
   * @param {boolean} [options.secure] use TLS.
   * @param {Buffer}  [options.rootCerts] CA bundle for TLS.
   * @param {string}  [options.apiKey] sent as the `x-api-key` metadata header.
   * @param {string}  [options.token] JWT, sent as `authorization: Bearer …`.
   * @param {number}  [options.deadlineMs] per-call deadline; 0 disables it.
   * @param {object}  [options.channelOptions] passed through to grpc-js.
   * @param {boolean} [options.followLeader] follow the cluster leader for
   *   writes (GO-051); off by default.
   * @param {function(string): (string|null)} [options.leaderResolver] maps
   *   the leader's HTTP URL to a gRPC target. Without one the server's
   *   leader_grpc address is used, else the URL's host with the port of
   *   `address`.
   */
  constructor(address = DEFAULT_ADDRESS, options = {}) {
    const {
      apiKey = null,
      token = null,
      deadlineMs = 30000,
      channelOptions = {},
      followLeader = false,
      leaderResolver = null,
    } = options;

    this.address = address;
    this.deadlineMs = deadlineMs;
    this.followLeader = followLeader;
    this._leaderResolver = leaderResolver;
    this._options = options;
    this._channelOptions = channelOptions;
    this._leader = null;
    this._leaderStub = null;
    this._writes = new Set();
    this._sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

    this._metadata = new grpc.Metadata();
    if (apiKey) this._metadata.set('x-api-key', apiKey);
    if (token) this._metadata.set('authorization', `Bearer ${token}`);

    this._stub = new (loadService())(
      address,
      buildCredentials(options),
      channelOptions
    );

    this._attachMethods();
  }

  /**
   * Exposes every unary RPC as a promise-returning method.
   *
   * Streaming RPCs (Export) are left alone: turning a stream into a single
   * promise would mean buffering an entire export in memory, which is the
   * thing streaming exists to avoid. Reach for `client.stub.Export(...)`.
   */
  _attachMethods() {
    const definition = loadService().service;

    for (const [name, method] of Object.entries(definition)) {
      if (method.requestStream || method.responseStream) continue;
      if (typeof this[name] !== 'undefined') continue;

      this[name] = (request = {}, callOptions = {}) =>
        this._call(name, request, callOptions);
    }
  }

  /**
   * Calls one RPC, following the leader when `followLeader` is on (GO-051).
   *
   * A refused write rejects with the gRPC error, carrying `leaderAddr`,
   * `leaderId`, `term` and `leaderReason`. With `followLeader` a NOT_LEADER
   * refusal is sent once more, to the leader, and a NO_LEADER one is retried
   * at most NO_LEADER_RETRIES times. The request is a plain object, so it can
   * be sent again unchanged.
   */
  async _call(method, request, callOptions) {
    return this._attempt(this._stubFor(method), method, request, callOptions, { redirected: false, waits: 0 });
  }

  /**
   * Sends the RPC on stub and, when the refusal is one `followLeader` acts
   * on, sends it again as `_retry` decides. The retries are sequential by
   * design: each one depends on how the one before it failed. `tries` is
   * what has been spent: the one redirect and the NO_LEADER waits.
   */
  async _attempt(stub, method, request, callOptions, tries) {
    try {
      return await this._once(stub, method, request, callOptions);
    } catch (err) {
      const next = await this._retry(err, stub, method, tries);
      return this._attempt(next.stub, method, request, callOptions, next.tries);
    }
  }

  /**
   * Decides what follows a failed attempt. It rethrows err when nothing
   * does; otherwise it returns the stub and the tries of the next attempt —
   * the leader's stub once for NOT_LEADER, the same stub after a wait for
   * NO_LEADER.
   */
  async _retry(err, stub, method, { redirected, waits }) {
    const info = annotate(err);
    if (!this.followLeader) throw err;
    if (!info) {
      this._forgetLeader(stub, err);
      throw err;
    }
    if (info.reason === NOT_LEADER && !redirected) {
      const leaderStub = this._follow(method, info);
      if (!leaderStub) throw err;
      return { stub: leaderStub, tries: { redirected: true, waits } };
    }
    if (info.reason !== NO_LEADER || waits >= NO_LEADER_RETRIES) throw err;
    await this._sleep(NO_LEADER_WAIT_MS);
    return { stub, tries: { redirected, waits: waits + 1 } };
  }

  /** One attempt of an RPC on stub; each attempt gets its own deadline. */
  _once(stub, method, request, callOptions) {
    return new Promise((resolve, reject) => {
      const options = { ...callOptions };
      if (this.deadlineMs > 0 && options.deadline === undefined) {
        options.deadline = new Date(Date.now() + this.deadlineMs);
      }

      stub[method](request, this._metadata, options, (err, response) => {
        if (err) {
          reject(err);
          return;
        }
        resolve(response);
      });
    });
  }

  /**
   * The leader's stub for an RPC known to be a write — one that was
   * redirected before — else the configured node's. Reads stay home.
   */
  _stubFor(method) {
    if (this.followLeader && this._leaderStub && this._writes.has(method)) {
      return this._leaderStub;
    }
    return this._stub;
  }

  /**
   * Remembers the leader a refusal named and returns its stub, or null when
   * the address is not an http(s) URL or the resolver has no target for it.
   */
  _follow(method, info) {
    const target = this._leaderTarget(info);
    if (!target) return null;
    this._writes.add(method);
    if (target !== this._leader) {
      if (this._leaderStub) this._leaderStub.close();
      this._leader = target;
      this._leaderStub = new (loadService())(
        target,
        buildCredentials(this._options),
        this._channelOptions
      );
    }
    return this._leaderStub;
  }

  /**
   * The gRPC target of the leader a refusal named: a caller's
   * `leaderResolver` wins, then the server's leader_grpc, then the HTTP
   * URL's host with the port of `address`.
   */
  _leaderTarget(info) {
    if (this._leaderResolver) {
      return info.leaderAddr ? this._leaderResolver(info.leaderAddr) : null;
    }
    return info.leaderGrpc || grpcTarget(info.leaderAddr, this.address);
  }

  /**
   * Drops a remembered leader that cannot be reached, so the next write asks
   * the configured node again, which names the current leader.
   */
  _forgetLeader(stub, err) {
    if (err.code !== grpc.status.UNAVAILABLE || stub !== this._leaderStub) return;
    this._leaderStub.close();
    this._leader = null;
    this._leaderStub = null;
  }

  /** The gRPC target of the leader this client follows, or null. */
  get leader() {
    return this._leader;
  }

  /** The generated stub, for streaming calls and anything not wrapped here. */
  get stub() {
    return this._stub;
  }

  /**
   * Waits until the channel is connected.
   *
   * Without this the first call is what discovers an unreachable server, and
   * it reports the failure as a deadline rather than a refused connection.
   */
  waitForReady(timeoutMs = 5000) {
    return new Promise((resolve, reject) => {
      this._stub.waitForReady(new Date(Date.now() + timeoutMs), (err) =>
        err ? reject(err) : resolve()
      );
    });
  }

  /** Closes the channel, and the followed leader's when there is one. */
  close() {
    this._stub.close();
    if (this._leaderStub) this._leaderStub.close();
  }
}

module.exports = {
  MddbClient,
  DEFAULT_ADDRESS,
  NO_LEADER_RETRIES,
  NO_LEADER_WAIT_MS,
  NOT_LEADER,
  NO_LEADER,
  leaderInfo,
  loadService,
  buildCredentials,
};
