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

const path = require('path');
const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');

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
   */
  constructor(address = DEFAULT_ADDRESS, options = {}) {
    const {
      apiKey = null,
      token = null,
      deadlineMs = 30000,
      channelOptions = {},
    } = options;

    this.address = address;
    this.deadlineMs = deadlineMs;

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

  _call(method, request, callOptions) {
    return new Promise((resolve, reject) => {
      const options = { ...callOptions };
      if (this.deadlineMs > 0 && options.deadline === undefined) {
        options.deadline = new Date(Date.now() + this.deadlineMs);
      }

      this._stub[method](request, this._metadata, options, (err, response) => {
        if (err) {
          reject(err);
          return;
        }
        resolve(response);
      });
    });
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

  close() {
    this._stub.close();
  }
}

module.exports = {
  MddbClient,
  DEFAULT_ADDRESS,
  loadService,
  buildCredentials,
};
