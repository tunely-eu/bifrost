# Security Policy

Bifrost is pre-1.0. Security fixes are accepted on the `main` branch.

## Reporting Vulnerabilities

Please do not disclose suspected vulnerabilities in public issues, pull requests, or discussions.

Use GitHub private vulnerability reporting if it is enabled for the repository. If private reporting is not available, open a minimal public issue asking for a private maintainer contact and do not include exploit details.

Useful reports include:

- Affected version or commit
- Environment and configuration
- Steps to reproduce
- Expected and observed behavior
- Security impact
- Suggested fix, if known

## Scope

Security-sensitive areas include TLS setup, ALPN negotiation, accept hook input and output handling, listener validation, header normalization and redaction, resource limits, stream lifecycle, Docker entrypoint config generation, and any behavior that can expose a listener unintentionally.

For the runtime security model, see [docs/security.md](docs/security.md).
