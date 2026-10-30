/**
 * The search surface, tracing and proxy settings of GET /v1/config (2.18.10),
 * as the Config and System pages show them. A server before 2.18.10 sends
 * none of these fields: each row then says it was not reported.
 */

const NOT_REPORTED = 'Not reported by this server';

/** The rows the card shows, in order: [label, value]. */
export function edgeRows(config) {
  const { esCompat, searchMaxLimit, telemetry, clientAddressing: ca } = config;
  const proxies = ca?.trustedProxies;
  return [
    ['Elasticsearch subset', esCompat ? (esCompat.enabled ? `On, under ${esCompat.prefix}` : 'Off') : NOT_REPORTED],
    ['Search result cap', searchMaxLimit ? `${searchMaxLimit.toLocaleString('en-US')} results per search` : NOT_REPORTED],
    ['OpenTelemetry traces', telemetry ? (telemetry.enabled ? `On, to ${telemetry.endpoint}` : 'Off') : NOT_REPORTED],
    ['Trusted proxies', ca ? (proxies ? `${proxies} configured` : 'None: forwarding headers are ignored') : NOT_REPORTED],
    ['Client IP header', ca ? ca.header : NOT_REPORTED],
    ['PROXY protocol', ca ? (ca.proxyProtocol ? 'On' : 'Off') : NOT_REPORTED],
  ];
}
