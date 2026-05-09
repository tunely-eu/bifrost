# Architecture

Bifrost is split into a small data-plane runtime and an external admission decision point. The tunnel core is responsible for transport, listeners, stream multiplexing, and limits. The accept hook decides whether a client is allowed and which server-side listener that client should own.

## Runtime Roles

- `bifrost-client` runs near the private service and connects outbound to a reachable server.
- `bifrost-server` accepts the tunnel, asks the accept hook for a decision, opens a server-side listener, and forwards accepted ingress connections over the tunnel.
- `bifrostctl` provides local utility commands such as config example generation and validation.

## Data Path

```text
external caller
  -> server-side listener
  -> bifrost-server
  -> TLS + ALPN bifrost/1 + yamux
  -> bifrost-client
  -> configured local TCP target
```

Every accepted ingress connection becomes one yamux stream. The client forwards each stream to the configured local TCP target and copies bytes in both directions until either side closes.

Bifrost does not inspect application payloads. HTTP routing, SNI routing, TLS termination for exposed services, DNS, ACME, authentication, and tenancy logic live outside the tunnel core.

## Control Flow

1. The client establishes TLS and offers ALPN `bifrost/1`.
2. The client sends a newline-terminated JSON hello with protocol version `1` and generic headers.
3. The server validates the transport and hello shape.
4. The server calls the configured accept hook with remote address, normalized headers, protocol metadata, and timestamp.
5. The hook returns an allow or deny decision.
6. On allow, the hook must provide an `endpoint_key`, listener spec, connection policy, and optional labels and limits.
7. The server validates the decision against listener policy and guardrails.
8. The server registers the session, opens the listener, sends `{"accepted":true}`, and starts yamux.
9. The listener accepts ingress connections and maps them to yamux streams.
10. On disconnect, replacement, or shutdown, the server closes the listener and removes the session.

## Endpoint Ownership

`endpoint_key` is the server-side identity for a tunnel session. It lets the hook decide how reconnects and competing clients behave:

- `reject_if_exists`: reject a new session when the key is already active.
- `replace_existing`: close the existing session and let the new one take over.
- `allow_parallel`: allow multiple sessions for the same key up to `max_parallel`.

This keeps reconnection behavior explicit and reviewable in hook output instead of hiding it in server defaults.

## Extension Boundaries

The accept hook is the primary extension point. It can be a shell script, a compiled binary, or a wrapper around an internal control plane. The hook can read any backing store it wants as long as it writes a valid decision JSON object to stdout.

Metrics and logging are intentionally narrow internal interfaces. The current runtime exposes readiness and Prometheus-style metrics through the optional admin listener; richer collectors can be added without changing the transport contract.
