FROM mcr.microsoft.com/devcontainers/go:1-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        iproute2 \
        jq \
        make \
        netcat-openbsd \
        openssl \
        socat \
        tcpdump \
    && rm -rf /var/lib/apt/lists/*

RUN go install mvdan.cc/gofumpt@latest \
    && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
