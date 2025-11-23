# MDDB MCP Configuration

MDDB MCP uses a layered configuration model:

1. Built-in defaults
2. Optional YAML file (default: `config.yaml` or path from `MDDB_MCP_CONFIG`)
3. Environment variables (highest priority)

## YAML structure

```yaml
mcp:
  listenAddress: "0.0.0.0:9000"

mddb:
  grpcAddress: "localhost:11024"
  restBaseUrl: "http://localhost:11023"
  # grpc_only | rest_only | grpc_with_rest_fallback | rest_with_grpc_fallback
  transportMode: "grpc_with_rest_fallback"
  timeoutSeconds: 2
  maxRetries: 1
```

## Environment variables (override YAML)

- `MCP_LISTEN_ADDRESS` – MCP listen address, e.g. `0.0.0.0:9000`
- `MDDB_GRPC_ADDRESS` – gRPC address of MDDB, e.g. `mddb:11024`
- `MDDB_REST_BASE_URL` – HTTP base URL of MDDB, e.g. `http://mddb:11023`
- `MDDB_TRANSPORT_MODE` – one of:
  - `grpc_only`
  - `rest_only`
  - `grpc_with_rest_fallback`
  - `rest_with_grpc_fallback`
- `MDDB_TIMEOUT_SECONDS` – request timeout in seconds (int)
- `MDDB_MAX_RETRIES` – max retries for fallback logic (int)

Environment variables always take precedence over values from YAML.
