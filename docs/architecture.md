# Architecture

Bifrost is split into a small data-plane runtime and an admission decision point. The tunnel core is responsible for TLS, protocol negotiation, listeners, stream multiplexing, limits, metrics, and shutdown. The admission provider decides whether a connector is allowed and which server-side listener that connector should own.

Standalone `bifrost-server` builds a static admission provider from top-level `clients[]` configuration. Embedded integrations can provide their own implementation of the Go `AcceptProvider` interface.

## Runtime Roles

- `bifrost-client` runs near the private service and connects outbound to a reachable server.
- `bifrost-server` accepts the tunnel, asks the configured provider for an admission decision, opens a server-side listener, and forwards accepted ingress connections over the tunnel.
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
4. The server calls the configured admission provider with remote address, normalized headers, protocol metadata, and timestamp.
5. The provider returns an allow or deny decision.
6. On allow, the decision must provide an `endpoint_key`, listener spec, connection policy, and optional labels and limits.
7. The server validates the decision against listener policy and guardrails.
8. The server registers the session, opens the listener, sends `{"accepted":true}`, and starts yamux.
9. The listener accepts ingress connections and maps them to yamux streams.
10. On disconnect, replacement, or shutdown, the server closes the listener and removes the session.

## Endpoint Ownership

`endpoint_key` is the server-side identity for a tunnel session. It makes reconnects and competing clients explicit:

- `reject_if_exists`: reject a new session when the key is already active.
- `replace_existing`: close the existing session and let the new one take over.
- `allow_parallel`: allow multiple sessions for the same key up to `max_parallel`.

This keeps reconnect behavior reviewable in configuration or provider output instead of hiding it in server defaults.

## Extension Boundaries

`AcceptProvider` is the primary extension point. The standalone binaries use a static provider created from `clients[]`; embedded products can supply a provider backed by their own control plane, database, policy engine, or tenant model.

The provider returns Bifrost's native admission decision. The tunnel core still validates listener specs and applies guardrails after every decision, so product-specific policy cannot silently expand beyond configured runtime ceilings.

Metrics and logging are intentionally narrow internal interfaces. The runtime emits typed observer events for readiness, sessions, streams, rejects, and copied bytes. The standalone binaries aggregate those events for the optional admin `/metrics` endpoint, while embedded users can attach their own observer without changing the transport contract.
