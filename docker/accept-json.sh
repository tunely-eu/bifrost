#!/bin/sh
set -eu

clients_file=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --clients)
      clients_file="${2:-}"
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

if [ -z "$clients_file" ]; then
  echo "--clients is required" >&2
  exit 2
fi

request="$(cat)"
token="$(printf '%s' "$request" | jq -r '.headers["x-bifrost-token"] // empty')"

if [ -z "$token" ]; then
  printf '{"allow":false,"reason":"missing token"}\n'
  exit 0
fi

decision="$(jq -c --arg token "$token" '
  .clients[]?
  | select(.token == $token)
  | {
      allow: true,
      reason: "accepted",
      endpoint_key: .endpoint_key,
      listener: .listener,
      connection_policy: (.connection_policy // {"mode":"reject_if_exists"}),
      limits: (.limits // {}),
      labels: (.labels // {})
    }
' "$clients_file" | head -n 1)"

if [ -z "$decision" ]; then
  printf '{"allow":false,"reason":"unknown token"}\n'
  exit 0
fi

printf '%s\n' "$decision"
