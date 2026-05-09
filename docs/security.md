# Security

Bifrost is designed to keep the tunnel core small and fail closed around the security-sensitive boundaries: transport setup, hook execution, listener creation, and resource control.

## Built-In Protections

- TLS is required for every client-server tunnel.
- ALPN must be `bifrost/1`.
- The client hello is size-limited.
- Header names are validated and normalized before hook execution.
- Header values are redacted in logs by default.
- Hook errors, hook timeouts, invalid JSON, invalid listeners, and missing `endpoint_key` fail closed.
- TCP listeners bind only to localhost unless `allow_public_tcp` is explicitly enabled.
- Unix sockets are restricted to configured path prefixes.
- Sessions, headers, streams, idle time, and bandwidth are bounded by server guardrails and hook plan limits.

## Trust Boundaries

The client-server tunnel is protected by TLS. The server-side listener and the client-side local target are separate trust boundaries:

- If the server-side listener should speak HTTPS, terminate TLS in a proxy or service in front of that listener.
- If the client-side local target requires authentication or encryption, provide it at the target service layer.
- Treat accept hook input as untrusted. Validate headers and remote metadata before mapping them to listeners or limits.

## Listener Safety

Unix socket listeners must live under configured prefixes. This prevents hook decisions from placing sockets in arbitrary filesystem locations.

TCP listeners are localhost-only by default. Public TCP listeners require `listener_policy.allow_public_tcp: true` and should be paired with explicit network controls.

## Resource Control

Accept decisions can set per-session limits, but the server enforces global guardrails before opening a listener. This prevents a hook bug or bad client definition from expanding beyond configured ceilings.

Important limits include:

- maximum active sessions
- maximum streams per session
- maximum bandwidth per session
- minimum and maximum stream idle timeout
- maximum header count and header bytes

## Known Non-Goals

Bifrost does not defend against volumetric DDoS.

Bifrost does not provide application-layer authentication for exposed services.

Bifrost does not manage certificates, DNS records, SNI routing, accounts, tenants, or billing.

Those concerns belong to surrounding infrastructure, the accept hook, a reverse proxy, or the target application.
