#!/bin/sh
set -eu

if [ -z ${DATABASE_ADDRESS:-} ]; then
  echo "DATABASE_ADDRESS is required"
  exit 1
fi

/nakama/nakama migrate up --database.address $DATABASE_ADDRESS

exec /nakama/nakama \
  --config /nakama/data/local.yml \
  --database.address $DATABASE_ADDRESS \
  --socket.server_key "${NAKAMA_SERVER_KEY:-defaultkey}" \
  --runtime.http_key "${NAKAMA_HTTP_KEY:-defaulthttpkey}" \
  --socket.port "${PORT:-7350}"