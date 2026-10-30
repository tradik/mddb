/**
 * The search surface, tracing and proxy settings of GET /v1/config (2.18.10),
 * as the Config and System pages show them. A server before 2.18.10 sends
 * none of these fields: each row then says it was not reported.
 */

const NOT_REPORTED = 'Not reported by this server';

/** `describe(section)` for a reported section, NOT_REPORTED for a missing one. */
function reported(section, describe) {
  return section ? describe(section) : NOT_REPORTED;
}

/** "On<detail>" when a switch is on, "Off" otherwise. */
function onOff(enabled, detail = '') {
  return enabled ? `On${detail}` : 'Off';
}

/** The trusted proxy count, or what an empty list means. */
function proxyText(proxies) {
  return proxies ? `${proxies} configured` : 'None: forwarding headers are ignored';
}

/** The rows the card shows, in order: [label, value]. */
export function edgeRows(config) {
  const { esCompat, searchMaxLimit, telemetry, clientAddressing } = config;
  return [
    ['Elasticsearch subset', reported(esCompat, (es) => onOff(es.enabled, `, under ${es.prefix}`))],
    ['Search result cap', reported(searchMaxLimit, (max) => `${max.toLocaleString('en-US')} results per search`)],
    ['OpenTelemetry traces', reported(telemetry, (t) => onOff(t.enabled, `, to ${t.endpoint}`))],
    ['Trusted proxies', reported(clientAddressing, (ca) => proxyText(ca.trustedProxies))],
    ['Client IP header', reported(clientAddressing, (ca) => ca.header)],
    ['PROXY protocol', reported(clientAddressing, (ca) => onOff(ca.proxyProtocol))],
  ];
}
