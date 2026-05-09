#!/bin/sh
set -eu

fail() {
  echo "entrypoint-test: $*" >&2
  exit 1
}

assert_eq() {
  expected="$1"
  actual="$2"
  label="$3"
  if [ "$actual" != "$expected" ]; then
    fail "$label: expected '$expected', got '$actual'"
  fi
}

assert_file_contains() {
  file="$1"
  needle="$2"
  label="$3"
  if ! grep -F -- "$needle" "$file" >/dev/null; then
    fail "$label: expected '$needle' in $file"
  fi
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    fail "$1 is required"
  fi
}

script_dir="$(CDPATH= cd "$(dirname "$0")" && pwd)"
repo_root="$(CDPATH= cd "$script_dir/.." && pwd)"
entrypoint="$repo_root/docker/bifrost-entrypoint"

require_command jq

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT INT HUP TERM

bin_dir="$work_dir/bin"
mkdir -p "$bin_dir"

make_stub() {
  name="$1"
  cat > "$bin_dir/$name" <<'EOF'
#!/bin/sh
set -eu
{
  printf '%s\n' "$0"
  for arg in "$@"; do
    printf '%s\n' "$arg"
  done
} > "$BIFROST_STUB_LOG"
EOF
  chmod +x "$bin_dir/$name"
}

make_stub bifrost-server
make_stub bifrost-client
make_stub bifrostctl

PATH="$bin_dir:$PATH"
export PATH

run_entrypoint() {
  log_file="$work_dir/stub.log"
  rm -f "$log_file"
  BIFROST_STUB_LOG="$log_file" "$entrypoint" "$@"
}

test_server_generates_config() {
  config_dir="$work_dir/server-config"
  BIFROST_GENERATED_CONFIG_DIR="$config_dir" \
    BIFROST_SERVER_LISTEN=":9443" \
    BIFROST_SERVER_TLS_CERT_FILE="/tls/server.crt" \
    BIFROST_SERVER_TLS_KEY_FILE="/tls/server.key" \
    BIFROST_ACCEPT_CLIENTS_FILE="/data/clients.json" \
    BIFROST_LISTENER_ALLOWED_UNIX_PREFIXES="/sock,/tmp/bifrost" \
    BIFROST_ADMIN_LISTEN="127.0.0.1:9000" \
    BIFROST_LOG_LEVEL="debug" \
    BIFROST_LOG_FORMAT="json" \
    run_entrypoint server --flag value

  server_config="$config_dir/server.yaml"
  [ -f "$server_config" ] || fail "server config was not generated"

  assert_file_contains "$work_dir/stub.log" "bifrost-server" "server command"
  assert_file_contains "$work_dir/stub.log" "--config" "server config arg"
  assert_file_contains "$work_dir/stub.log" "$server_config" "server config path"
  assert_file_contains "$work_dir/stub.log" "--flag" "server forwarded arg"
  assert_file_contains "$work_dir/stub.log" "value" "server forwarded value"

  assert_eq ":9443" "$(jq -r '.server.listen' "$server_config")" "server listen"
  assert_eq "/tls/server.crt" "$(jq -r '.server.tls.cert_file' "$server_config")" "server cert file"
  assert_eq "/tls/server.key" "$(jq -r '.server.tls.key_file' "$server_config")" "server key file"
  assert_eq "/usr/local/share/bifrost/accept-json.sh" "$(jq -r '.accept_hook.command' "$server_config")" "server hook"
  assert_eq "/data/clients.json" "$(jq -r '.accept_hook.args[1]' "$server_config")" "server clients file"
  assert_eq "/sock" "$(jq -r '.listener_policy.allowed_unix_prefixes[0]' "$server_config")" "server first unix prefix"
  assert_eq "/tmp/bifrost" "$(jq -r '.listener_policy.allowed_unix_prefixes[1]' "$server_config")" "server second unix prefix"
  assert_eq "127.0.0.1:9000" "$(jq -r '.admin.listen' "$server_config")" "server admin listen"
  assert_eq "debug" "$(jq -r '.logging.level' "$server_config")" "server log level"
  assert_eq "json" "$(jq -r '.logging.format' "$server_config")" "server log format"
}

test_client_generates_config() {
  config_dir="$work_dir/client-config"
  BIFROST_GENERATED_CONFIG_DIR="$config_dir" \
    BIFROST_CLIENT_SERVER_URL="https://bifrost.example.test:8443" \
    BIFROST_CLIENT_TARGET_ADDRESS="127.0.0.1:8080" \
    BIFROST_CLIENT_TOKEN="secret-token" \
    BIFROST_CLIENT_HEADERS_JSON='{"X-Trace":"abc123"}' \
    BIFROST_CLIENT_TLS_CA_FILE="/tls/ca.crt" \
    BIFROST_CLIENT_TLS_SERVER_NAME="bifrost.example.test" \
    BIFROST_CLIENT_TLS_INSECURE_SKIP_VERIFY="yes" \
    BIFROST_ADMIN_LISTEN="127.0.0.1:9100" \
    run_entrypoint client --once

  client_config="$config_dir/client.yaml"
  [ -f "$client_config" ] || fail "client config was not generated"

  assert_file_contains "$work_dir/stub.log" "bifrost-client" "client command"
  assert_file_contains "$work_dir/stub.log" "$client_config" "client config path"
  assert_file_contains "$work_dir/stub.log" "--once" "client forwarded arg"

  assert_eq "https://bifrost.example.test:8443" "$(jq -r '.client.server_url' "$client_config")" "client server url"
  assert_eq "127.0.0.1:8080" "$(jq -r '.client.target.address' "$client_config")" "client target address"
  assert_eq "tcp" "$(jq -r '.client.target.type' "$client_config")" "client target type"
  assert_eq "secret-token" "$(jq -r '.client.headers["X-Bifrost-Token"]' "$client_config")" "client token header"
  assert_eq "abc123" "$(jq -r '.client.headers["X-Trace"]' "$client_config")" "client extra header"
  assert_eq "/tls/ca.crt" "$(jq -r '.client.tls.ca_file' "$client_config")" "client ca file"
  assert_eq "bifrost.example.test" "$(jq -r '.client.tls.server_name' "$client_config")" "client server name"
  assert_eq "true" "$(jq -r '.client.tls.insecure_skip_verify' "$client_config")" "client insecure skip verify"
  assert_eq "127.0.0.1:9100" "$(jq -r '.admin.listen' "$client_config")" "client admin listen"
}

test_client_omits_empty_ca_file() {
  config_dir="$work_dir/client-system-trust-config"
  BIFROST_GENERATED_CONFIG_DIR="$config_dir" \
    BIFROST_CLIENT_SERVER_URL="bifrost.example.test:8443" \
    BIFROST_CLIENT_TARGET_ADDRESS="127.0.0.1:8080" \
    BIFROST_CLIENT_TOKEN="secret-token" \
    BIFROST_CLIENT_TLS_CA_FILE="" \
    run_entrypoint client

  client_config="$config_dir/client.yaml"
  [ -f "$client_config" ] || fail "client config was not generated"

  assert_eq "false" "$(jq -r '.client.tls | has("ca_file")' "$client_config")" "client ca file omitted"
  assert_eq "false" "$(jq -r '.client.tls.insecure_skip_verify' "$client_config")" "client insecure skip verify default"
}

test_client_omits_missing_default_ca_file() {
  if [ -f /certs/ca.crt ]; then
    echo "entrypoint-test: skipping missing default CA test because /certs/ca.crt exists" >&2
    return
  fi

  config_dir="$work_dir/client-missing-default-ca-config"
  BIFROST_GENERATED_CONFIG_DIR="$config_dir" \
    BIFROST_CLIENT_SERVER_URL="bifrost.example.test:8443" \
    BIFROST_CLIENT_TARGET_ADDRESS="127.0.0.1:8080" \
    BIFROST_CLIENT_TOKEN="secret-token" \
    run_entrypoint client

  client_config="$config_dir/client.yaml"
  [ -f "$client_config" ] || fail "client config was not generated"

  assert_eq "false" "$(jq -r '.client.tls | has("ca_file")' "$client_config")" "missing default client ca file omitted"
}

test_explicit_config_passthrough() {
  config_dir="$work_dir/passthrough-config"
  mkdir -p "$config_dir"

  BIFROST_GENERATED_CONFIG_DIR="$config_dir" run_entrypoint server --config /etc/bifrost/server.yaml --verbose
  [ ! -e "$config_dir/server.yaml" ] || fail "server config should not be generated with explicit --config"
  assert_file_contains "$work_dir/stub.log" "bifrost-server" "server passthrough command"
  assert_file_contains "$work_dir/stub.log" "/etc/bifrost/server.yaml" "server passthrough config"
  assert_file_contains "$work_dir/stub.log" "--verbose" "server passthrough arg"

  BIFROST_GENERATED_CONFIG_DIR="$config_dir" run_entrypoint client --config=/etc/bifrost/client.yaml --once
  [ ! -e "$config_dir/client.yaml" ] || fail "client config should not be generated with explicit --config"
  assert_file_contains "$work_dir/stub.log" "bifrost-client" "client passthrough command"
  assert_file_contains "$work_dir/stub.log" "--config=/etc/bifrost/client.yaml" "client passthrough config"
  assert_file_contains "$work_dir/stub.log" "--once" "client passthrough arg"
}

test_ctl_passthrough() {
  run_entrypoint ctl status --json
  assert_file_contains "$work_dir/stub.log" "bifrostctl" "ctl command"
  assert_file_contains "$work_dir/stub.log" "status" "ctl arg"
  assert_file_contains "$work_dir/stub.log" "--json" "ctl flag"
}

test_rejects_invalid_input() {
  if BIFROST_GENERATED_CONFIG_DIR="$work_dir/invalid" run_entrypoint client >/dev/null 2>"$work_dir/err.log"; then
    fail "client without required env should fail"
  fi
  assert_file_contains "$work_dir/err.log" "BIFROST_CLIENT_SERVER_URL is required" "client required env error"

  if BIFROST_CLIENT_SERVER_URL="https://example.test" \
    BIFROST_GENERATED_CONFIG_DIR="$work_dir/invalid-headers" \
    BIFROST_CLIENT_TARGET_ADDRESS="127.0.0.1:8080" \
    BIFROST_CLIENT_TOKEN="secret-token" \
    BIFROST_CLIENT_HEADERS_JSON='{"X-Bifrost-Token":"bad"}' \
    run_entrypoint client >/dev/null 2>"$work_dir/err.log"; then
    fail "client headers should reject X-Bifrost-Token"
  fi
  assert_file_contains "$work_dir/err.log" "must not contain X-Bifrost-Token" "client reserved header error"

  if run_entrypoint gateway >/dev/null 2>"$work_dir/err.log"; then
    fail "unknown role should fail"
  fi
  assert_file_contains "$work_dir/err.log" "unknown role gateway" "unknown role error"
}

test_server_generates_config
test_client_generates_config
test_client_omits_empty_ca_file
test_client_omits_missing_default_ca_file
test_explicit_config_passthrough
test_ctl_passthrough
test_rejects_invalid_input

echo "entrypoint-test: ok"
