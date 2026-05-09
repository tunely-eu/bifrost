FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bifrost-server ./cmd/bifrost-server \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bifrost-client ./cmd/bifrost-client \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bifrostctl ./cmd/bifrostctl

FROM alpine:3.21
RUN apk add --no-cache ca-certificates jq
COPY --from=build /out/bifrost-server /usr/local/bin/bifrost-server
COPY --from=build /out/bifrost-client /usr/local/bin/bifrost-client
COPY --from=build /out/bifrostctl /usr/local/bin/bifrostctl
COPY docker/bifrost-entrypoint /usr/local/bin/bifrost-entrypoint
COPY docker/accept-json.sh /usr/local/share/bifrost/accept-json.sh
RUN chmod +x /usr/local/bin/bifrost-entrypoint /usr/local/share/bifrost/accept-json.sh
ENTRYPOINT ["bifrost-entrypoint"]
