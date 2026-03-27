#!/bin/sh
set -e

/nakama/nakama migrate up --database.address $DATABASE_ADDRESS

exec /nakama/nakama \
  --config /nakama/data/local.yml \
  --database.address $DATABASE_ADDRESS \
  --socket.server_key "$NAKAMA_SERVER_KEY" \
  --runtime.http_key "$NAKAMA_HTTP_KEY" \
  --socket.port "${PORT:-7350}"