# Security

Bifrost is designed to keep the tunnel core small and fail closed around the security-sensitive boundaries: transport setup, connector admission, listener creation, and resource control.

## Built-In Protections

- TLS is required for every client-server tunnel.
- ALPN must be `bifrost/1`.
- The client hello is size-limited.
- Header names are validated and normalized before admission decisions.
- Header values are redacted in logs by default.
- Admission errors, invalid client definitions, invalid listeners, and missing `endpoint_key` fail closed.
- Sessions, headers, streams, idle time, and bandwidth are bounded by server guardrails and plan limits.

## Trust Boundaries

The client-server tunnel is protected by TLS. The server-side listener and the client-side local target are separate trust boundaries:

- If the server-side listener should speak HTTPS, terminate TLS in a proxy or service in front of that listener.
- If the client-side local target requires authentication or encryption, provide it at the target service layer.
- Treat connector hello headers as untrusted. Validate headers and remote metadata before mapping them to endpoints, listeners, or limits.

## Listener Safety

Unix socket listeners should use deployment-owned runtime directories and restrictive file modes.

TCP listeners should bind to loopback unless surrounding network controls are intentional.

## Resource Control

Accept decisions can set per-session limits, but the server enforces global guardrails before opening a listener or stream. This prevents a bad client definition or custom `AcceptProvider` from expanding beyond configured ceilings.

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

Those concerns belong to surrounding infrastructure, a product-specific `AcceptProvider`, a reverse proxy, or the target application.
