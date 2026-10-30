package main

import (
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
)

// The configuration allowlist (OPS-025): every MDDB_* variable the server
// reads, classified once, here, by what its value may reveal. GET /v1/config
// and the diagnose bundle both render values through it, so the two cannot
// disagree about what is a secret. A variable missing from the list fails
// TestEveryConfigKeyIsClassified instead of leaking: the bundle shows an
// unclassified variable's name and never its value.

// configClass is what a configuration value may reveal.
type configClass uint8

const (
	// cfgPublic values are switches, numbers, modes and model names: shown.
	cfgPublic configClass = iota + 1
	// cfgSecret values are keys, passwords, tokens and headers: only
	// whether they are set.
	cfgSecret
	// cfgListen values are listen addresses: the port is shown, the
	// interface address only to /v1/config.
	cfgListen
	// cfgHidden values are paths, hosts and free text (a server
	// description, synonym lists): /v1/config shows them, the bundle only
	// whether they are set.
	cfgHidden
	// cfgURL values are URLs: /v1/config drops credentials and the query,
	// the bundle keeps only the scheme.
	cfgURL
	// cfgList values are lists that name hosts or origins (trusted proxies,
	// CORS origins): the bundle counts the entries.
	cfgList
)

// configAudience is who a rendered value is for.
type configAudience uint8

const (
	// audienceEndpoint is GET /v1/config: an authenticated operator of
	// this server.
	audienceEndpoint configAudience = iota
	// audienceBundle is the diagnose bundle: a stranger reading an issue.
	audienceBundle
)

// Rendered placeholders.
const (
	cfgSet          = "<set>"
	cfgUnset        = "<unset>"
	cfgUnclassified = "<unclassified>"
)

// configKeys classifies every configuration variable the server reads.
var configKeys = map[string]configClass{
	// Secrets: whether set, never what.
	"MDDB_AUTH_ADMIN_PASSWORD": cfgSecret, "MDDB_AUTH_ADMIN_USERNAME": cfgSecret, "MDDB_AUTH_JWT_SECRET": cfgSecret,
	"MDDB_EMBEDDING_API_KEY": cfgSecret, "MDDB_ENCRYPTION_KEY": cfgSecret, "MDDB_ENCRYPTION_KEYS_PREVIOUS": cfgSecret,
	"MDDB_ENCRYPTION_KEY_ID": cfgSecret, "MDDB_MCP_API_KEYS": cfgSecret, "MDDB_MCP_OAUTH_SECRET": cfgSecret,
	"MDDB_REPLICATION_SECRET": cfgSecret, "MDDB_OTEL_HEADERS": cfgSecret, "MDDB_AUDIT_EXPORT_WEBHOOK_HEADER": cfgSecret,

	// Listen addresses: the port.
	"MDDB_ADDR": cfgListen, "MDDB_HTTP_ADDR": cfgListen, "MDDB_GRPC_ADDR": cfgListen, "MDDB_MCP_ADDR": cfgListen,
	"MDDB_HTTP3_ADDR": cfgListen,

	// URLs.
	"MDDB_EMBEDDING_API_URL": cfgURL, "MDDB_AUDIT_EXPORT_WEBHOOK_URL": cfgURL, "MDDB_OTEL_ENDPOINT": cfgURL,
	"MDDB_MCP_OAUTH_ISSUER": cfgURL, "MDDB_MCP_SERVER_HOMEPAGE": cfgURL,

	// Lists of hosts and origins: counted. Trusted proxies (#366) name the
	// operator's internal network, so the bundle says how many, not which.
	"MDDB_TRUSTED_PROXIES": cfgList, "MDDB_CORS_ORIGIN": cfgList, "MDDB_CORS_ORIGINS": cfgList,
	"MDDB_OUTBOUND_ALLOWLIST": cfgList, "MDDB_MCP_OAUTH_REDIRECT_URIS": cfgList,

	// Paths, hosts and free text.
	"MDDB_PATH": cfgHidden, "MDDB_BACKUP_DIR": cfgHidden, "MDDB_BINLOG_PATH": cfgHidden, "MDDB_CONFIG": cfgHidden,
	"MDDB_GEO_DATA_DIR": cfgHidden, "MDDB_MCP_CONFIG": cfgHidden, "MDDB_MCP_INSTRUCTIONS_FILE": cfgHidden,
	"MDDB_TLS_CERT": cfgHidden, "MDDB_TLS_KEY": cfgHidden, "MDDB_TLS_CLIENT_CA": cfgHidden,
	"MDDB_INTERNAL_VERIFY_DB": cfgHidden, "MDDB_REPLICATION_LEADER_ADDR": cfgHidden,
	"MDDB_AUDIT_EXPORT_SYSLOG_ADDR": cfgHidden, "MDDB_MCP_DOMAIN": cfgHidden, "MDDB_MCP_INSTRUCTIONS": cfgHidden,
	"MDDB_MCP_SERVER_DESCRIPTION": cfgHidden, "MDDB_MCP_SERVER_NAME": cfgHidden, "MDDB_MCP_SERVER_VENDOR": cfgHidden,
	"MDDB_NODE_ID": cfgHidden, "MDDB_OTEL_SERVICE_NAME": cfgHidden, "MDDB_FTS_SYNONYMS": cfgHidden,

	// Switches, numbers, modes, model and header names.
	"MDDB_ACCESS_LOG": cfgPublic, "MDDB_API_MODE": cfgPublic, "MDDB_AUDIT_ENABLED": cfgPublic,
	"MDDB_AUDIT_EXPORT_BUFFER": cfgPublic, "MDDB_AUDIT_EXPORT_SYSLOG_FACILITY": cfgPublic,
	"MDDB_AUDIT_EXPORT_WEBHOOK_INSECURE_TLS": cfgPublic, "MDDB_AUDIT_RETENTION_DAYS": cfgPublic,
	"MDDB_AUTH_ENABLED": cfgPublic, "MDDB_AUTH_JWT_EXPIRY": cfgPublic, "MDDB_AUTOMATIONS": cfgPublic,
	"MDDB_AUTOMATION_LOGS": cfgPublic, "MDDB_AUTOMATION_LOGS_TTL": cfgPublic, "MDDB_BACKUP_VERIFY": cfgPublic,
	"MDDB_BINLOG_ENABLED": cfgPublic, "MDDB_CLIENT_IP_HEADER": cfgPublic, "MDDB_CODE_MAX_SYMBOLS": cfgPublic,
	"MDDB_COMPRESSION_ENABLED": cfgPublic, "MDDB_COMPRESSION_MEDIUM_THRESHOLD": cfgPublic,
	"MDDB_COMPRESSION_SMALL_THRESHOLD": cfgPublic, "MDDB_CORPUS_MAX_BYTES": cfgPublic, "MDDB_CRONS": cfgPublic,
	"MDDB_DISK_MIN_FREE": cfgPublic, "MDDB_DISK_RESERVE_BYTES": cfgPublic, "MDDB_EMBEDDING_AUTODETECT": cfgPublic,
	"MDDB_EMBEDDING_CACHE_SIZE": cfgPublic, "MDDB_EMBEDDING_CACHE_TTL": cfgPublic,
	"MDDB_EMBEDDING_CHUNK_ENABLED": cfgPublic, "MDDB_EMBEDDING_CHUNK_MODE": cfgPublic,
	"MDDB_EMBEDDING_CHUNK_SIZE": cfgPublic, "MDDB_EMBEDDING_DIMENSIONS": cfgPublic,
	"MDDB_EMBEDDING_IMAGE_MODEL": cfgPublic, "MDDB_EMBEDDING_MODEL": cfgPublic, "MDDB_EMBEDDING_PROVIDER": cfgPublic,
	"MDDB_EMBEDDING_QUEUE_SIZE": cfgPublic, "MDDB_EMBEDDING_QUEUE_WAIT": cfgPublic, "MDDB_ES_COMPAT": cfgPublic,
	"MDDB_EXTREME": cfgPublic, "MDDB_FREELIST_BACKGROUND": cfgPublic, "MDDB_FREELIST_SYNC": cfgPublic,
	"MDDB_FREELIST_SYNC_THRESHOLD": cfgPublic, "MDDB_FTS_DEFAULT_LANG": cfgPublic, "MDDB_FTS_STEMMING": cfgPublic,
	"MDDB_GRAPHQL_ENABLED": cfgPublic, "MDDB_GRAPHQL_PLAYGROUND": cfgPublic, "MDDB_GRPC_ENABLED": cfgPublic,
	"MDDB_GRPC_MODE": cfgPublic, "MDDB_GRPC_PORT": cfgPublic, "MDDB_HEALTH_STALL_LIMIT": cfgPublic,
	"MDDB_HTTP3_ENABLED": cfgPublic, "MDDB_HTTP3_MODE": cfgPublic, "MDDB_HTTP3_PORT": cfgPublic,
	"MDDB_HTTP_ENABLED": cfgPublic, "MDDB_HTTP_POOL_IDLE_TIMEOUT": cfgPublic, "MDDB_HTTP_POOL_MAX_IDLE": cfgPublic,
	"MDDB_HTTP_POOL_MAX_PER_HOST": cfgPublic, "MDDB_HTTP_PORT": cfgPublic, "MDDB_HTTP_READ_TIMEOUT": cfgPublic,
	"MDDB_HTTP_WRITE_TIMEOUT": cfgPublic, "MDDB_INCIDENT_AUTH_COOLDOWN_SEC": cfgPublic,
	"MDDB_INCIDENT_AUTH_THRESHOLD": cfgPublic, "MDDB_INCIDENT_AUTH_WINDOW_SEC": cfgPublic,
	"MDDB_INCIDENT_DISK_COOLDOWN_SEC": cfgPublic, "MDDB_INCIDENT_DISK_INTERVAL_SEC": cfgPublic,
	"MDDB_INCIDENT_DISK_THRESHOLD_PCT": cfgPublic, "MDDB_INCIDENT_LAG_COOLDOWN_SEC": cfgPublic,
	"MDDB_INCIDENT_LAG_INTERVAL_SEC": cfgPublic, "MDDB_INCIDENT_LAG_THRESHOLD_MS": cfgPublic,
	"MDDB_LOG_FORMAT": cfgPublic, "MDDB_LOG_LEVEL": cfgPublic, "MDDB_MAX_BODY_BYTES": cfgPublic,
	"MDDB_MCP_API_KEY_CACHE_TTL": cfgPublic, "MDDB_MCP_API_KEY_ENABLED": cfgPublic,
	"MDDB_MCP_BUILTIN_TOOLS": cfgPublic, "MDDB_MCP_CONFIRM_DESTRUCTIVE": cfgPublic, "MDDB_MCP_ENABLED": cfgPublic,
	"MDDB_MCP_LOGGING_ENABLED": cfgPublic, "MDDB_MCP_LOGGING_LEVEL": cfgPublic, "MDDB_MCP_MODE": cfgPublic,
	"MDDB_MCP_OAUTH_ACCESS_TTL": cfgPublic, "MDDB_MCP_OAUTH_ENABLED": cfgPublic, "MDDB_MCP_OAUTH_REFRESH_TTL": cfgPublic,
	"MDDB_MCP_PORT": cfgPublic, "MDDB_MCP_PROTOCOL_VERSION": cfgPublic, "MDDB_MCP_RATE_LIMIT_BURST": cfgPublic,
	"MDDB_MCP_RATE_LIMIT_BY": cfgPublic, "MDDB_MCP_RATE_LIMIT_ENABLED": cfgPublic,
	"MDDB_MCP_RATE_LIMIT_REQUESTS": cfgPublic, "MDDB_MCP_RATE_LIMIT_WINDOW": cfgPublic, "MDDB_MCP_STDIO": cfgPublic,
	"MDDB_MCP_TOOLSETS": cfgPublic, "MDDB_METRICS": cfgPublic, "MDDB_METRICS_PUBLIC": cfgPublic, "MDDB_MODE": cfgPublic,
	"MDDB_OTEL_QUEUE_SIZE": cfgPublic, "MDDB_OTEL_SAMPLE_RATIO": cfgPublic, "MDDB_OUTBOUND_ALLOW_PRIVATE": cfgPublic,
	"MDDB_PANEL_MODE": cfgPublic, "MDDB_PPROF_ENABLED": cfgPublic, "MDDB_PREWARM": cfgPublic,
	"MDDB_PRODUCTION": cfgPublic, "MDDB_PROXY_PROTOCOL": cfgPublic, "MDDB_RATE_LIMIT_BURST": cfgPublic,
	"MDDB_RATE_LIMIT_BY": cfgPublic, "MDDB_RATE_LIMIT_ENABLED": cfgPublic, "MDDB_RATE_LIMIT_REQUESTS": cfgPublic,
	"MDDB_RATE_LIMIT_WINDOW": cfgPublic, "MDDB_REPLICATION_ROLE": cfgPublic, "MDDB_SEARCH_CACHE_SIZE": cfgPublic,
	"MDDB_SEARCH_MAX_CONCURRENT": cfgPublic, "MDDB_SEARCH_MAX_LIMIT": cfgPublic,
	"MDDB_SEARCH_QUEUE_TIMEOUT_MS": cfgPublic, "MDDB_SEARCH_STATS": cfgPublic, "MDDB_SHUTDOWN_TIMEOUT_SEC": cfgPublic,
	"MDDB_SPELL": cfgPublic, "MDDB_SSE_ENABLED": cfgPublic, "MDDB_SSE_MAX_CLIENTS": cfgPublic,
	"MDDB_SSE_MAX_PER_IP": cfgPublic, "MDDB_STATS_TTL": cfgPublic, "MDDB_STATS_WAIT": cfgPublic,
	"MDDB_STORAGE_LAYOUT": cfgPublic, "MDDB_TEMPORAL": cfgPublic, "MDDB_TLS_CLIENT_AUTH": cfgPublic,
	"MDDB_TLS_ENABLED": cfgPublic, "MDDB_TLS_INSECURE_OK": cfgPublic, "MDDB_TRIGGERS": cfgPublic,
	"MDDB_UPDATE_CHECK": cfgPublic, "MDDB_VECTOR_BQ_RERANK_FACTOR": cfgPublic,
	"MDDB_VECTOR_DEFAULT_ALGORITHM": cfgPublic, "MDDB_VECTOR_PARALLEL_MIN_SIZE": cfgPublic,
	"MDDB_VECTOR_PARALLEL_WORKERS": cfgPublic, "MDDB_VECTOR_RAM_LIMIT_BYTES": cfgPublic, "MDDB_VERIFY_TIMEOUT": cfgPublic,
	"MDDB_WIKI_MAX_DECOMPRESSED_BYTES": cfgPublic, "MDDB_WIKI_MAX_PAGES": cfgPublic,
}

// renderConfig renders the value of configuration variable key for aud. set
// says whether the variable has a value at all; an unknown key is shown as
// unclassified, never with its value.
func renderConfig(key, value string, set bool, aud configAudience) string {
	class, known := configKeys[key]
	switch {
	case !known:
		return cfgUnclassified
	case !set && aud == audienceEndpoint:
		return value // /v1/config has always shown an empty value as empty
	case !set:
		return cfgUnset
	case class == cfgPublic:
		return value
	case class == cfgSecret:
		return cfgSet
	case aud == audienceEndpoint && class == cfgURL:
		return endpointURL(value)
	case aud == audienceEndpoint:
		return value
	case class == cfgListen:
		return listenPort(value)
	case class == cfgURL:
		return bundleURL(value)
	case class == cfgList:
		return "<" + strconv.Itoa(listCount(value)) + " entries>"
	default: // cfgHidden
		return cfgSet
	}
}

// configValue renders a value the server already resolved (a default, or one
// from the config file) under key's class.
func configValue(key, value string, aud configAudience) string {
	return renderConfig(key, value, value != "", aud)
}

// endpointURL drops a URL's credentials, query and fragment, which
// /v1/config has no reason to show; what does not parse is withheld.
func endpointURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return cfgSet
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

// bundleURL keeps only a URL's scheme: the host may be internal.
func bundleURL(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" {
		return "<set: " + u.Scheme + ">"
	}
	return cfgSet
}

// listenPort keeps the port of a listen address ("10.0.0.5:11023" →
// ":11023"); a Unix socket or an address without a port is only set.
func listenPort(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return ":" + port
	}
	return cfgSet
}

// listCount counts the entries of a comma- or space-separated list.
func listCount(v string) int {
	return len(strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }))
}

// envSnapshot is env.json: every MDDB_* variable in the environment, its
// value rendered for the bundle.
func envSnapshot() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		name, value, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "MDDB_") {
			out[name] = renderConfig(name, value, true, audienceBundle)
		}
	}
	return out
}

// configSettings is every classified variable that is set, rendered for
// aud, sorted by name in the JSON encoding.
func configSettings(aud configAudience) map[string]string {
	keys := make([]string, 0, len(configKeys))
	for k := range configKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]string{}
	for _, k := range keys {
		if v, set := os.LookupEnv(k); set {
			out[k] = renderConfig(k, v, true, aud)
		}
	}
	return out
}
