# Contributing

Thanks for helping improve Bifrost. The project is pre-1.0, so small, focused pull requests with clear tests are the easiest to review.

## Development Setup

Prerequisites:

- Go 1.25 or newer
- `make`
- `jq`
- `openssl`
- Docker, if you want to run the container demo

Clone the repository, then run:

```bash
go mod download
make test
```

The devcontainer includes the same basic toolchain and runs `make test` after creation.

## Checks

Before opening a pull request, run:

```bash
make fmt
make fmt-check
make vet
make test
make race
make entrypoint-test
make build
```

For Docker changes, also run:

```bash
make docker-build
```

## Pull Requests

- Keep changes scoped to one behavior or maintenance task.
- Add or update tests when behavior changes.
- Update `README.md` or `docs/` when user-facing behavior, configuration, protocol, or security expectations change.
- Call out compatibility impact for config schema, admission decisions, protocol, listener behavior, and Docker entrypoint changes.
- Do not commit local binaries, demo certificates, logs, credentials, or machine-specific editor files.

## Security

Do not open public issues for suspected vulnerabilities. Follow [SECURITY.md](SECURITY.md).
