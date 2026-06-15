# Passive Latency Observations

Bifrost exposes endpoint-keyed passive session latency state for embedders that
need a bounded tunnel-health signal without inspecting application traffic.

The observation key is the Bifrost `endpoint_key`. The observation payload is:

```json
{
  "endpoint_key": "home",
  "latency_ms": 18,
  "observed_at": "2026-06-15T10:15:30Z",
  "state": "ok"
}
```

`state` is controlled:

- `ok`: a fresh passive mux/session control observation exists.
- `unknown`: no passive observation exists for the endpoint.
- `stale`: the latest passive observation exists but is older than the
  configured freshness window.

`latency_ms` and `observed_at` are present only when an observation exists.
Consumers must treat `unknown` as absence of evidence, not as zero latency.

## Source

Latency is derived from Bifrost-owned tunnel control traffic. The server records
the round-trip time returned by the yamux keepalive ping used to maintain the
session. This does not inspect proxied bytes and does not measure target
application response time.

The passive latency surface must not be populated from:

- HTTP requests, paths, headers, cookies, bodies, status codes, or content
  types;
- target application health checks or published-service response times;
- diagnostic actions or connection-test probes;
- remote address analytics or participant identifiers.

## Data Boundary

Passive latency observations contain only endpoint key, latency milliseconds,
observation time, and state. They do not carry connector tokens, token hashes,
TLS private keys, hello header values, remote addresses, SNI names, route
hostnames, application payloads, or participant data.

The standalone Prometheus metrics endpoint does not emit passive latency
samples. Embedders that export observations elsewhere are responsible for
keeping labels low-cardinality and secret-free.

