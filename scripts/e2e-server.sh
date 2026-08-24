#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
database="money_e2e_$$"
backend_port=8200
idp_port=5758
admin_url="postgres://postgres:postgres@127.0.0.1:5556/postgres"
database_url="postgres://postgres:postgres@127.0.0.1:5556/$database"
bin_dir="$(mktemp -d)"
pids=()

cleanup() {
  trap - EXIT INT TERM
  if ((${#pids[@]})); then
    kill "${pids[@]}" 2>/dev/null || true
    wait "${pids[@]}" 2>/dev/null || true
  fi
  psql "$admin_url" -c "drop database if exists $database with (force)" >/dev/null
  rm -rf "$bin_dir"
}
trap cleanup EXIT INT TERM

(
  cd "$root/backend"
  go build -o "$bin_dir/money" .
  go build -o "$bin_dir/devidp" ./cmd/devidp
)

psql "$admin_url" -v ON_ERROR_STOP=1 -c "create database $database" >/dev/null
openssl genrsa -out "$bin_dir/enablebanking.pem" 2048 >/dev/null 2>&1

DEVIDP_ADDR="127.0.0.1:$idp_port" \
DEVIDP_ISSUER="http://127.0.0.1:$idp_port" \
"$bin_dir/devidp" >"$bin_dir/devidp.log" 2>&1 &
idp_pid=$!
pids+=("$idp_pid")

for _ in {1..100}; do
  if (echo >/dev/tcp/127.0.0.1/"$idp_port") 2>/dev/null; then
    break
  fi
  if ! kill -0 "$idp_pid" 2>/dev/null; then
    cat "$bin_dir/devidp.log" >&2
    exit 1
  fi
  sleep 0.1
done

PORT="$backend_port" \
BACKEND_URL="http://127.0.0.1:$backend_port" \
FRONT_URL= \
DEV_VITE_URL="http://127.0.0.1:3001" \
DB_URL="$database_url" \
IMPORT_STORE=postgres \
DISABLE_RATE_SYNC=1 \
DEMO_MODE=1 \
OIDC_ISSUER="http://127.0.0.1:$idp_port" \
OIDC_CLIENT_ID=money \
OIDC_CLIENT_SECRET=money \
OIDC_REDIRECT_URL= \
ENABLEBANKING_APP_ID=e2e \
ENABLEBANKING_PRIVATE_KEY="$bin_dir/enablebanking.pem" \
ENABLEBANKING_ALLOWED_OIDC_SUBJECTS=e2e-bank-auth,e2e-bank-connections \
IS_PROD=0 \
"$bin_dir/money" >"$bin_dir/backend.log" 2>&1 &
backend_pid=$!
pids+=("$backend_pid")

set +e
wait "$backend_pid"
status=$?
set -e
cat "$bin_dir/backend.log" >&2
exit "$status"
