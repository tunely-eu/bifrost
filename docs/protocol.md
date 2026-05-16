# Protocol

The native Bifrost transport is:

```text
TLS + ALPN "bifrost/1" + JSON hello + yamux
```

TLS is mandatory. The client must offer ALPN `bifrost/1`; the server rejects connections that negotiate anything else. Application traffic starts only after the server accepts the client and both sides switch to yamux over the established TLS connection.

## Handshake

1. TCP connection is established to `bifrost-server`.
2. TLS handshake completes.
3. ALPN must negotiate `bifrost/1`.
4. Client sends one newline-terminated JSON hello.
5. Server validates the hello and asks the configured admission provider for a decision.
6. Server replies with an accept or reject JSON response.
7. On accept, yamux starts on the same TLS connection.

## Client Hello

The client sends one newline-terminated JSON object:

```json
{
  "protocol_version": "1",
  "headers": {
    "x-bifrost-token": "dev-secret"
  }
}
```

`protocol_version` is currently `1`.

Header names are syntactically validated and normalized to lower-case before they are passed to the admission provider. Header values are opaque to Bifrost; the tunnel runtime does not interpret them beyond size and count limits.

## Server Response

Accepted:

```json
{"accepted":true}
```

Rejected:

```json
{"accepted":false,"reason":"invalid token"}
```

Rejected connections do not start yamux. The reason is intended for diagnostics and should not contain secrets.

## Stream Mapping

After accept, each server-side listener connection opens one yamux stream to the client. The client dials its configured local TCP target for every stream and copies bytes in both directions.

Stream concurrency, bandwidth, and idle lifetime come from the admission decision, then are checked against server guardrails before the listener is opened.

## Keepalive

The server configures yamux keepalive globally under `runtime`:

```yaml
runtime:
  tunnel_keepalive_interval: "30s"
  tunnel_keepalive_timeout: "10s"
```

The server sends periodic yamux pings. If the client does not respond within `tunnel_keepalive_timeout`, yamux closes the session and Bifrost removes the session and its server-side listener.

## Compatibility Notes

The protocol is pre-1.0. Changes to ALPN, hello fields, response fields, or stream semantics should be treated as compatibility-sensitive and documented in release notes.
