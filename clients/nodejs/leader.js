'use strict';

/**
 * Cluster leader information carried by a refused write (GO-051).
 *
 * A node of an mddbd cluster that is not the leader refuses a write with gRPC
 * status FAILED_PRECONDITION ("not the leader ...") and a google.rpc.ErrorInfo
 * detail {reason: "NOT_LEADER", domain: "mddb", metadata: {leader_addr,
 * leader_id, term, leader_grpc}}. While an election runs it answers UNAVAILABLE with reason
 * "NO_LEADER". leader_addr is the leader's HTTP base URL, leader_grpc (when
 * the server sends it) its gRPC address.
 *
 * The detail travels in the `grpc-status-details-bin` trailer as an encoded
 * google.rpc.Status. It is decoded here by hand — three small messages — so
 * the client needs no extra dependency for it.
 */

const grpc = require('@grpc/grpc-js');

const NOT_LEADER = 'NOT_LEADER';
const NO_LEADER = 'NO_LEADER';

const DETAILS_KEY = 'grpc-status-details-bin';
const ERROR_INFO_TYPE = 'type.googleapis.com/google.rpc.ErrorInfo';
const NOT_LEADER_MESSAGE = 'not the leader';

/** Default gRPC port, used when the configured address names none. */
const DEFAULT_GRPC_PORT = '11024';

// Protobuf wire types.
const VARINT = 0;
const I64 = 1;
const LEN = 2;
const I32 = 5;

/**
 * Returns the leader information of a refused write, or null.
 *
 * @param {Error} err an error a call rejected with.
 * @returns {{reason: string, leaderAddr: string, leaderId: string, term: (number|null), leaderGrpc: string}|null}
 */
function leaderInfo(err) {
  if (!err || typeof err.code !== 'number') return null;
  const info = fromDetails(trailer(err));
  if (info) return info;
  // A proxy may drop the trailer; the status itself still says enough.
  const details = typeof err.details === 'string' ? err.details : '';
  if (err.code === grpc.status.FAILED_PRECONDITION && details.startsWith(NOT_LEADER_MESSAGE)) {
    return { reason: NOT_LEADER, leaderAddr: '', leaderId: '', term: null, leaderGrpc: '' };
  }
  return null;
}

/**
 * Sets `leaderAddr`, `leaderGrpc`, `leaderId`, `term` and `leaderReason` on a refused
 * write. The error stays the one gRPC produced, so `err.code` checks keep
 * working.
 */
function annotate(err) {
  const info = leaderInfo(err);
  if (info) {
    err.leaderAddr = info.leaderAddr;
    err.leaderId = info.leaderId;
    err.leaderGrpc = info.leaderGrpc;
    err.term = info.term;
    err.leaderReason = info.reason;
  }
  return info;
}

/**
 * Maps a leader's HTTP base URL to a gRPC target: the URL's host with the
 * port of `homeAddress`, the configured node, since the nodes of a cluster
 * normally share their gRPC port. Only an absolute http/https URL counts;
 * anything else gives null.
 */
function grpcTarget(leaderAddr, homeAddress) {
  let url;
  try {
    url = new URL(leaderAddr);
  } catch {
    return null;
  }
  if ((url.protocol !== 'http:' && url.protocol !== 'https:') || !url.hostname) return null;
  const port = String(homeAddress).split(':').pop();
  return `${url.hostname}:${/^\d+$/.test(port) ? port : DEFAULT_GRPC_PORT}`;
}

function trailer(err) {
  if (!err.metadata || typeof err.metadata.get !== 'function') return null;
  const values = err.metadata.get(DETAILS_KEY);
  return values.length > 0 && Buffer.isBuffer(values[0]) ? values[0] : null;
}

function fromDetails(data) {
  if (!data || data.length === 0) return null;
  try {
    for (const [num, wire, value] of fields(data)) {
      if (num !== 3 || wire !== LEN) continue;
      const info = fromAny(value);
      if (info) return info;
    }
  } catch {
    return null;
  }
  return null;
}

/** Decodes a google.protobuf.Any; only an ErrorInfo with a known reason counts. */
function fromAny(data) {
  const any = lenFields(data);
  if ((any.get(1) || Buffer.alloc(0)).toString() !== ERROR_INFO_TYPE) return null;
  let reason = '';
  const metadata = {};
  for (const [num, wire, value] of fields(any.get(2) || Buffer.alloc(0))) {
    if (wire !== LEN) continue;
    if (num === 1) reason = value.toString();
    if (num === 3) {
      const entry = lenFields(value);
      metadata[(entry.get(1) || '').toString()] = (entry.get(2) || '').toString();
    }
  }
  if (reason !== NOT_LEADER && reason !== NO_LEADER) return null;
  const term = metadata.term || '';
  return {
    reason,
    leaderAddr: metadata.leader_addr || '',
    leaderId: metadata.leader_id || '',
    term: /^\d+$/.test(term) ? Number(term) : null,
    leaderGrpc: metadata.leader_grpc || '',
  };
}

/** The length-delimited fields of a message, by number (last one wins). */
function lenFields(data) {
  const out = new Map();
  for (const [num, wire, value] of fields(data)) {
    if (wire === LEN) out.set(num, value);
  }
  return out;
}

/** Yields [field number, wire type, value] of an encoded message. */
function* fields(data) {
  let pos = 0;
  while (pos < data.length) {
    let key;
    [key, pos] = varint(data, pos);
    const wire = key & 7;
    let value;
    if (wire === VARINT) {
      [value, pos] = varint(data, pos);
    } else if (wire === LEN) {
      let size;
      [size, pos] = varint(data, pos);
      value = data.subarray(pos, pos + size);
      pos += size;
    } else if (wire === I64 || wire === I32) {
      const width = wire === I64 ? 8 : 4;
      value = data.subarray(pos, pos + width);
      pos += width;
    } else {
      throw new Error(`unsupported wire type ${wire}`);
    }
    if (pos > data.length) throw new Error('truncated message');
    yield [Math.floor(key / 8), wire, value];
  }
}

function varint(data, start) {
  let result = 0;
  let scale = 1;
  let pos = start;
  for (;;) {
    if (pos >= data.length) throw new Error('truncated varint');
    const byte = data[pos++];
    result += (byte & 0x7f) * scale;
    if ((byte & 0x80) === 0) return [result, pos];
    scale *= 128;
  }
}

module.exports = {
  NOT_LEADER,
  NO_LEADER,
  leaderInfo,
  annotate,
  grpcTarget,
  // exported for the tests
  fields,
  fromAny,
  fromDetails,
};
