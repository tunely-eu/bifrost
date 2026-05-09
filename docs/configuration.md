# Configuration

Bifrost uses YAML as the runtime config format. JSON is accepted for the same schema.

There are two configuration paths:

- Docker environment variables, translated into generated config files by the image entrypoint.
- Explicit config files passed to `bifrost-server` or `bifrost-client` with `--config`.

Use environment variables for the default Docker workflow. Use explicit config files when you need fields that are intentionally not exposed as environment variables, such as a custom accept hook, listener safety flags, server guardrails, runtime timeouts, or checked-in deployment configuration.

## Docker Environment Configuration

The Docker image is configured through environment variables and fixed container paths. The entrypoint generates `/run/bifrost/server.yaml` or `/run/bifrost/client.yaml`, then starts `bifrost-server --config ...` or `bifrost-client --config ...`.

If `server` or `client` receives an explicit `--config`, the entrypoint does not generate a config file and passes the command through unchanged. Set `BIFROST_GENERATED_CONFIG_DIR` to change the generated config directory from `/run/bifrost`.

The environment interface covers the common container path only. It does not expose every server field.

## TLS Certificates

Bifrost always uses TLS between the homelab connector and the cloud relay.

With the default Docker paths:

| Runtime side | File | Purpose |
| --- | --- | --- |
| Cloud relay | `/certs/server.crt` | Certificate presented to connectors. Its SAN must match the connector's `BIFROST_CLIENT_SERVER_URL` host or `BIFROST_CLIENT_TLS_SERVER_NAME`. |
| Cloud relay | `/certs/server.key` | Private key for `/certs/server.crt`. Keep this file only on the relay. |
| Homelab connector | `/certs/ca.crt` | Optional CA certificate used to validate a private or self-signed relay certificate. For a self-signed test certificate, this can be the same public certificate as `server.crt`. If this file is absent and `BIFROST_CLIENT_TLS_CA_FILE` is unset, the connector uses the system trust store. |

For the Compose examples using `cloud.example.com`, generate a self-signed test certificate with:

```bash
mkdir -p examples/certs
openssl req -x509 -newkey rsa:4096 -sha256 -nodes \
  -days 365 \
  -keyout examples/certs/server.key \
  -out examples/certs/server.crt \
  -subj "/CN=cloud.example.com" \
  -addext "subjectAltName=DNS:cloud.example.com"
cp examples/certs/server.crt examples/certs/ca.crt
```

This creates:

- `examples/certs/server.crt`: mount on the cloud relay as `/certs/server.crt`.
- `examples/certs/server.key`: mount on the cloud relay as `/certs/server.key`.
- `examples/certs/ca.crt`: mount on the homelab connector as `/certs/ca.crt`.

For production, use a certificate issued for the real relay hostname. If it is issued by a public CA trusted by the image's system trust store, do not mount `/certs/ca.crt` on the connector. If it is issued by your own CA, mount that CA's public certificate on the connector. Do not copy `server.key` to the homelab side.

### Server Defaults

| Generated field | Docker default | Meaning |
| --- | --- | --- |
| `server.listen` | `:8443` | Address where the cloud relay accepts connector tunnels. |
| `server.tls.cert_file` | `/certs/server.crt` | TLS certificate presented by the cloud relay. |
| `server.tls.key_file` | `/certs/server.key` | TLS private key for `server.tls.cert_file`. |
| `accept_hook.command` | `/usr/local/share/bifrost/accept-json.sh` | Bundled JSON accept hook installed in the image from `docker/accept-json.sh`. |
| `accept_hook.args` | `["--clients", "/etc/bifrost/clients.json"]` | Arguments for the bundled hook. |
| `listener_policy.allowed_unix_prefixes` | `/sockets` | Allowed filesystem prefixes for Unix socket listeners returned by the hook. |

The default hook reads the generated accept request from stdin, extracts `x-bifrost-token`, looks up the token in the clients file, and returns the matching listener, connection policy, labels, and limits.

### Server Environment Variables

| Variable | Meaning |
| --- | --- |
| `BIFROST_SERVER_LISTEN` | Overrides `server.listen`. |
| `BIFROST_SERVER_TLS_CERT_FILE` | Overrides `server.tls.cert_file`. |
| `BIFROST_SERVER_TLS_KEY_FILE` | Overrides `server.tls.key_file`. |
| `BIFROST_ACCEPT_CLIENTS_FILE` | Changes the clients file path passed to the bundled default hook. It does not change the hook command. |
| `BIFROST_LISTENER_ALLOWED_UNIX_PREFIXES` | Comma-separated Unix socket prefixes for `listener_policy.allowed_unix_prefixes`. |
| `BIFROST_ADMIN_LISTEN` | Enables the admin listener at the given address. |
| `BIFROST_LOG_LEVEL` | Sets `logging.level`. |
| `BIFROST_LOG_FORMAT` | Sets `logging.format`. |

### Client Environment Variables

| Variable | Required | Meaning |
| --- | --- | --- |
| `BIFROST_CLIENT_SERVER_URL` | yes | Cloud relay address in `host:port` form. |
| `BIFROST_CLIENT_TARGET_ADDRESS` | yes | Local TCP target reached by the connector for every accepted tunnel stream. |
| `BIFROST_CLIENT_TOKEN` | yes | Token sent as `X-Bifrost-Token` in the client hello. |
| `BIFROST_CLIENT_TLS_CA_FILE` | no | CA certificate file used to validate the cloud relay certificate. If unset, generated Docker config uses `/certs/ca.crt` only when that file exists; otherwise the client uses the system trust store. |
| `BIFROST_CLIENT_TLS_SERVER_NAME` | no | TLS server name override when the connection host and certificate name differ. |
| `BIFROST_CLIENT_TLS_INSECURE_SKIP_VERIFY` | no | Development-only TLS verification bypass. |
| `BIFROST_CLIENT_HEADERS_JSON` | no | Extra hello headers as a JSON object with string values. Must not include `X-Bifrost-Token`. |
| `BIFROST_ADMIN_LISTEN` | no | Enables the admin listener at the given address. |
| `BIFROST_LOG_LEVEL` | no | Sets `logging.level`. |
| `BIFROST_LOG_FORMAT` | no | Sets `logging.format`. |

## Explicit Config Files

The binaries read config files directly:

```bash
bifrost-server --config path/to/server.yaml
bifrost-client --config path/to/client.yaml
```

Docker can also use explicit config files. Mount the file into the container and pass `server --config /path/in/container.yaml` or `client --config /path/in/container.yaml`; the entrypoint will skip generated config for that role.

Validate configs before deployment:

```bash
go run ./cmd/bifrostctl config validate --server --file path/to/server.yaml
go run ./cmd/bifrostctl config validate --client --file path/to/client.yaml
```

You can print starter configs with:

```bash
go run ./cmd/bifrostctl config example --server
go run ./cmd/bifrostctl config example --client
```

## Server Parameters

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `server.listen` | string | File config: `127.0.0.1:8443`; Docker env path: `:8443` | Address where the cloud relay accepts connector tunnels. |
| `server.tls.cert_file` | string | Docker env path: `/certs/server.crt` | TLS certificate file for the relay. Required in explicit config. |
| `server.tls.key_file` | string | Docker env path: `/certs/server.key` | TLS private key file for the relay. Required in explicit config. |
| `accept_hook.command` | string | Docker env path: `/usr/local/share/bifrost/accept-json.sh` | Program executed for each connector handshake to allow or deny the connection. Required in explicit config. |
| `accept_hook.args` | list of strings | empty | Arguments passed to `accept_hook.command`. |
| `listener_policy.allowed_unix_prefixes` | list of strings | File config: `/run/bifrost`, `/tmp/bifrost`; Docker env path: `/sockets` | Unix socket listener paths returned by the hook must be under one of these prefixes. |
| `listener_policy.allow_public_tcp` | bool | `false` | Allows hook decisions to create TCP listeners on non-localhost addresses. Keep disabled unless surrounding network controls are intentional. |
| `listener_policy.create_parent_dirs` | bool | `true` | Creates parent directories for Unix socket listeners when needed. |
| `guardrails.max_sessions` | int | `1000` | Maximum active tunnel sessions on the relay. |
| `guardrails.max_streams_per_session` | int | `512` | Upper bound for `limits.max_streams` returned by hooks. |
| `guardrails.max_bandwidth_bps_per_session` | int | `100000000` | Upper bound for `limits.max_bandwidth_bps` returned by hooks. |
| `guardrails.min_stream_idle_timeout` | duration | `30s` | Lower bound for `limits.stream_idle_timeout_seconds` returned by hooks. |
| `guardrails.max_stream_idle_timeout` | duration | `1h` | Upper bound for `limits.stream_idle_timeout_seconds` returned by hooks. |
| `guardrails.max_headers` | int | `32` | Maximum number of hello headers accepted from a connector. |
| `guardrails.max_header_bytes` | int | `8192` | Maximum combined header bytes accepted from a connector. |
| `runtime.handshake_timeout` | duration | `10s` | Deadline for TLS and protocol handshake work. |
| `runtime.hook_timeout` | duration | `2s` | Maximum runtime for one accept hook invocation. |
| `runtime.hook_max_stdout_bytes` | int | `65536` | Maximum stdout bytes accepted from the hook. |
| `runtime.stream_copy_buffer_bytes` | int | `32768` | Buffer size used while copying stream bytes. |
| `runtime.tunnel_keepalive_interval` | duration | `30s` | Yamux keepalive ping interval. |
| `runtime.tunnel_keepalive_timeout` | duration | `10s` | Yamux keepalive timeout before the session is closed. |
| `logging.level` | string | `info` | Log level. |
| `logging.format` | string | `text` | Log format. |
| `admin.listen` | string | disabled | Optional admin listener address for `/readyz` and `/metrics`. |

Duration values accept Go duration strings such as `10s`, `2m`, and `1h`. Numeric JSON duration values are interpreted as seconds.

## Client Parameters

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `client.server_url` | string | none | Cloud relay address in `host:port` form. Required. |
| `client.headers` | map of string to string | empty | Generic hello headers sent to the relay before hook execution. |
| `client.target.type` | string | `tcp` | Target type. Only `tcp` is currently supported. |
| `client.target.address` | string | none | Local TCP target address reached for every accepted tunnel stream. Required. |
| `client.tls.ca_file` | string | system trust store | Optional CA certificate file for validating the relay certificate. Generated Docker config uses mounted `/certs/ca.crt` only when it exists. |
| `client.tls.server_name` | string | host from `client.server_url` | TLS server name override. |
| `client.tls.insecure_skip_verify` | bool | `false` | Development-only TLS verification bypass. |
| `logging.level` | string | `info` | Log level. |
| `logging.format` | string | `text` | Log format. |
| `admin.listen` | string | disabled | Optional admin listener address for `/readyz` and `/metrics`. |

Reconnect backoff is internal client behavior and is not currently user-configurable.

## Default Hook Clients File

The bundled `docker/accept-json.sh` hook expects a JSON file with a top-level `clients` array. Each item describes one accepted token and the listener plan returned when that token connects.

| Field | Type | Meaning |
| --- | --- | --- |
| `clients[].token` | string | Token matched against the connector's `x-bifrost-token` header. |
| `clients[].endpoint_key` | string | Stable identity for session replacement, rejection, or parallelism decisions. |
| `clients[].listener.type` | string | Listener type returned to the relay. Supported values are `unix` and `tcp`. |
| `clients[].listener.path` | string | Unix socket path when `listener.type` is `unix`. Must be under an allowed prefix. |
| `clients[].listener.mode` | string | Optional Unix socket file mode, for example `0600`. |
| `clients[].listener.address` | string | TCP listen address when `listener.type` is `tcp`. Public addresses require `listener_policy.allow_public_tcp`. |
| `clients[].connection_policy.mode` | string | Session ownership mode: `reject_if_exists`, `replace_existing`, or `allow_parallel`. |
| `clients[].connection_policy.max_parallel` | int | Maximum active sessions for `allow_parallel`. |
| `clients[].limits.max_streams` | int | Maximum concurrent streams for the session. Checked against server guardrails. |
| `clients[].limits.max_bandwidth_bps` | int | Session bandwidth limit in bytes per second. Checked against server guardrails. |
| `clients[].limits.stream_idle_timeout_seconds` | int | Stream idle timeout in seconds. Checked against server guardrails. |
| `clients[].labels` | map of string to string | Optional metadata for logs, metrics, or external hook implementations. |

Missing hook limits use plan defaults before guardrail checks: `max_streams` defaults to `100`, `max_bandwidth_bps` defaults to `25000000`, and `stream_idle_timeout_seconds` defaults to `300`.

## Custom Accept Hooks

The accept hook receives one JSON request on stdin and must write one JSON decision object to stdout. Hook failures, timeouts, invalid JSON, invalid listeners, and missing `endpoint_key` fail closed.

Use a custom hook when token lookup in `clients.json` is not enough, for example when decisions should come from a database, provisioning system, or internal policy service.

In Docker, a custom hook requires an explicit server config because the generated config always points at the bundled hook. Mount the hook and config file into the container, then pass `server --config /path/to/server.yaml`.

## Admin Endpoints

When `admin.listen` is set, Bifrost exposes:

- `/readyz`: readiness status.
- `/metrics`: Prometheus-style text metrics.

Bind the admin listener to a private interface, loopback address, or protected network segment.
