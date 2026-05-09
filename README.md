# Bifrost

Bifrost is a secure, self-hosted reverse tunnel written in Go. It exposes selected local TCP services through outbound connections, without requiring inbound connectivity to the private network.

Use it when a service sits behind NAT, DS-Lite, CGNAT, or a firewall, and you want a small tunnel runtime that leaves admission, listener selection, and resource policy under your control.

```text
public caller
  -> cloud relay listener
  -> bifrost cloud runtime
  -> TLS + ALPN bifrost/1 + yamux
  -> homelab connector
  -> private local service
```

Bifrost forwards byte streams. It does not include HTTP routing, DNS, ACME, SNI routing, account management, billing, dashboards, or application authentication.

## Why Bifrost

- Outbound-first connectivity for services that cannot accept public inbound traffic.
- Mandatory TLS transport with ALPN protocol selection.
- Multiplexed streams over one client-server tunnel.
- External accept hook for admission decisions, listener placement, labels, and per-session limits.
- Localhost-only TCP listeners by default and prefix-restricted Unix sockets.
- Guardrails for sessions, headers, stream counts, idle time, and bandwidth.
- Same Docker image for cloud relay, homelab connector, and control commands.

## Status

Bifrost is early and experimental. The transport, hook contract, Docker entrypoint, and config schema are usable for development, demos, and review, but should be treated as pre-1.0.

## Example: Homelab Service Through A Cloud VM

The example Compose files show a common split deployment:

- A cloud VM accepts the tunnel and exposes an HTTP entrypoint.
- A homelab machine connects outbound to the cloud VM.
- Requests arriving at the cloud side are forwarded to a private HTTP service in the homelab.

The files are intentionally deployment-oriented rather than a fully local demo. They assume the cloud relay is reachable as `cloud.example.com:8443` and the Bifrost image is available as `ghcr.io/tunely-eu/bifrost:latest`.

The cloud relay needs a TLS certificate and private key mounted at `/certs/server.crt` and `/certs/server.key`. The homelab connector needs a CA file at `/certs/ca.crt` that can validate the relay certificate. For a self-signed test certificate:

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

In a split deployment, keep `server.key` only on the cloud relay. The homelab side only needs the CA file.

Cloud side:

```bash
docker compose -f examples/cloud.compose.yml up
```

Homelab side:

```bash
docker compose -f examples/homelab.compose.yml up
```

For a real deployment, replace `cloud.example.com` with your relay hostname and mount certificates that match that name.

## Docker Runtime

The Docker image is the default runtime interface. Use the same image for each runtime role:

- Cloud relay: `command: ["server"]`
- Homelab connector: `command: ["client"]`
- Control command: `command: ["ctl", "..."]`

The entrypoint generates `/run/bifrost/server.yaml` or `/run/bifrost/client.yaml` from environment variables, then starts the selected binary with `--config`.

For the default Docker path, you do not need to maintain `server.yaml` or `client.yaml` files. Use explicit config files only when you want settings that are intentionally not exposed as environment variables, such as a custom accept hook, listener safety flags, server guardrails, runtime timeouts, or checked-in deployment configuration.

### Default Container Paths

Mount these paths when using the generated Docker configuration:

- `/certs/server.crt`: server TLS certificate
- `/certs/server.key`: server TLS private key
- `/certs/ca.crt`: client CA certificate
- `/etc/bifrost/clients.json`: client definitions for the bundled JSON accept hook
- `/sockets`: allowed Unix socket directory for server-side listeners

The cloud relay listens on `:8443` by default. The bundled accept hook reads `/etc/bifrost/clients.json` and returns the listener, connection policy, labels, and limits for a matching connector token.

### Required Connector Environment

Homelab connector containers need:

- `BIFROST_CLIENT_SERVER_URL`: cloud relay address, for example `cloud.example.com:8443`
- `BIFROST_CLIENT_TARGET_ADDRESS`: local TCP target in the private network, for example `home-dashboard:8080`
- `BIFROST_CLIENT_TOKEN`: token sent as `X-Bifrost-Token`

The connector validates the cloud relay certificate with `/certs/ca.crt`. The certificate SAN must match the host in `BIFROST_CLIENT_SERVER_URL`, unless `BIFROST_CLIENT_TLS_SERVER_NAME` is set explicitly.

## Accept Hook

The accept hook is the control point between the tunnel runtime and your environment. For each connector connection, the cloud relay sends one JSON request to the hook and expects one JSON decision back.

```text
bifrost-server -> hook stdin:  Accept Request JSON
hook stdout    -> server:      Accept Decision JSON
```

Accept request:

```json
{
  "remote_addr": "203.0.113.10:51234",
  "headers": {
    "x-bifrost-token": "dev-secret"
  },
  "protocol_version": "1",
  "transport": "tls",
  "alpn": "bifrost/1",
  "timestamp": "2026-05-02T12:00:00Z"
}
```

Accept decision:

```json
{
  "allow": true,
  "reason": "accepted",
  "endpoint_key": "homelab-dashboard",
  "listener": {
    "type": "unix",
    "path": "/sockets/homelab-dashboard.sock",
    "mode": "0600"
  },
  "connection_policy": {
    "mode": "replace_existing",
    "max_parallel": 1
  },
  "limits": {
    "max_streams": 100,
    "max_bandwidth_bps": 25000000,
    "stream_idle_timeout_seconds": 300
  },
  "labels": {
    "site": "homelab",
    "service": "dashboard"
  }
}
```

Reject decision:

```json
{"allow":false,"reason":"invalid token"}
```

The Docker image uses the bundled `docker/accept-json.sh` hook by default. Inside the image it is installed at `/usr/local/share/bifrost/accept-json.sh`; the generated server config calls it with `--clients /etc/bifrost/clients.json`.

The default hook uses `jq` and a `clients.json` file. It is intentionally simple so the hook contract is easy to inspect, replace, or port to another control plane.

To use a custom hook in Docker, mount your hook and an explicit server config, then start the server with `server --config /path/to/server.yaml` so the entrypoint does not generate one. The available config fields are documented in [Configuration](docs/configuration.md).

## Security Model

- Client-server transport is always TLS.
- ALPN must be `bifrost/1`.
- The client hello is JSON and size-limited.
- Header names are validated and normalized before hook execution.
- Header values are generic runtime metadata and are redacted in logs.
- Hook failures, hook timeouts, invalid hook output, invalid listener specs, and missing `endpoint_key` fail closed.
- TCP listeners bind only to localhost unless public TCP listeners are explicitly enabled.
- Unix sockets are restricted to configured path prefixes.
- Streams, sessions, buffers, idle time, and bandwidth have bounded defaults.

TLS protects the tunnel between `bifrost-client` and `bifrost-server`. Bifrost does not automatically apply TLS to the server-side listener or to the client-side local target; it forwards the bytes it receives.

## What Bifrost Is Not

Bifrost is not a VPN. It exposes selected stream listeners, not an IP network.

Bifrost is not a hosted tunnel product. It has no hosted control plane, account system, billing logic, or managed edge.

Bifrost is not a reverse proxy. Routing, SNI, certificates, and DNS live outside Bifrost. A proxy such as Caddy can sit in front of a Bifrost Unix socket when HTTP routing is needed.

## Local Development

Build and test locally:

```bash
make test
make entrypoint-test
make build
```

The binaries are written to `bin/`.

Generate starter configs:

```bash
go run ./cmd/bifrostctl config example --server
go run ./cmd/bifrostctl config example --client
```

Validate your own configs:

```bash
go run ./cmd/bifrostctl config validate --server --file path/to/server.yaml
go run ./cmd/bifrostctl config validate --client --file path/to/client.yaml
```

## Documentation

- [Architecture](docs/architecture.md)
- [Protocol](docs/protocol.md)
- [Configuration](docs/configuration.md)
- [Security](docs/security.md)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, review expectations, and pull request guidelines.

Please report suspected vulnerabilities through the process in [SECURITY.md](SECURITY.md).

## License

MIT License. See [LICENSE](LICENSE).
