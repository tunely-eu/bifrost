# Security Policy

Bifrost is pre-1.0. Security fixes are accepted on the `main` branch.

## Reporting Vulnerabilities

Please do not disclose suspected vulnerabilities in public issues, pull requests, or discussions.

Use GitHub private vulnerability reporting for this repository. Open a private vulnerability report from the repository's **Security** tab and include enough detail for maintainers to reproduce and assess the issue.

Useful reports include:

- Affected version or commit
- Environment and configuration
- Steps to reproduce
- Expected and observed behavior
- Security impact
- Suggested fix, if known

## Scope

Security-sensitive areas include TLS setup, ALPN negotiation, admission input and output handling, listener validation, header normalization and redaction, resource limits, stream lifecycle, Docker entrypoint config generation, and any behavior that can expose a listener unintentionally.

For the runtime security model, see [docs/security.md](docs/security.md).
