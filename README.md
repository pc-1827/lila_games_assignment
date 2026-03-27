# Tic-Tac-Toe Multiplayer (Nakama + React)

This project is a server-authoritative multiplayer Tic-Tac-Toe game.

- Backend: Nakama runtime module (Go plugin)
- Frontend: React + TypeScript (Vite)
- Database: PostgreSQL

## Setup And Installation

## Prerequisites

1. Docker and Docker Compose for local backend stack.
2. Node.js 20+ and npm for frontend.
3. Optional for image deployment: Docker Hub account.

## Local Setup

1. Start backend + database locally:

```bash
cd backend
docker compose up --build
```

2. Start frontend locally in a separate terminal:

```bash
cd frontend
npm install
npm run dev
```

3. Open the frontend URL shown by Vite (usually http://localhost:5173).

## Local Environment Notes

Frontend runtime config is read from environment variables in [frontend/src/nakama.ts](frontend/src/nakama.ts).

Defaults in code are:

1. Host: 127.0.0.1
2. Port: 7350
3. SSL: false
4. HTTP key: defaulthttpkey

If you override with `.env`, ensure host is host-only without protocol.

## Architecture And Design Decisions

## High-Level Components

1. React frontend handles authentication, lobby, room actions, and board UI.
2. Nakama Go module provides RPC endpoints and authoritative match loop.
3. PostgreSQL stores Nakama system data and player profile stats.

## Authoritative Multiplayer

The backend owns game state and applies all moves inside the match handler.

Key logic is in [backend/match_handler.go](backend/match_handler.go).

Design choices:

1. Server-authoritative moves prevent client-side cheating.
2. Tick-based loop controls timeouts and disconnect behavior.
3. Match labels include `open` and `fast` flags for room discovery.

## RPC-Centric Lobby Flow

The frontend uses Nakama RPCs to create/list/join/manage rooms.

RPC registration is in [backend/main.go](backend/main.go).
RPC implementations are in [backend/match_rpc.go](backend/match_rpc.go), [backend/profile.go](backend/profile.go), and [backend/auth_rpc.go](backend/auth_rpc.go).

## Profile Tracking

Wins/losses/draws are persisted in Nakama storage collection `profile`.

Implementation: [backend/profile.go](backend/profile.go).

## Backend Health Feedback

The frontend periodically polls `/healthcheck` and shows backend status in the footer.

Implementation: [frontend/src/nakama.ts](frontend/src/nakama.ts) and [frontend/src/App.tsx](frontend/src/App.tsx).

## Deployment Process Documentation

This section documents the stable deployment path that worked best for this project.

## Overview

1. Deploy PostgreSQL.
2. Build and push backend Docker image to Docker Hub.
3. Deploy backend on Render from Docker image.
4. Deploy frontend on Render as static site.

## Backend Image Build And Push

From repository root:

```bash
docker build -t <dockerhub-username>/lila-nakama:latest ./backend
docker push <dockerhub-username>/lila-nakama:latest
```

Backend image startup is controlled by:

1. [backend/Dockerfile](backend/Dockerfile)
2. [backend/start.sh](backend/start.sh)

`start.sh` runs migrations, then starts Nakama.

## Render Backend Service (From Existing Image)

1. Create Render Web Service from Docker image `<dockerhub-username>/lila-nakama:latest`.
2. Leave Start Command empty.
3. Leave Docker Command empty.
4. Set health check path to `/healthcheck`.
5. Add environment variables:

```text
DATABASE_ADDRESS=<user>:<password>@<host>:5432/<database>?sslmode=require
NAKAMA_SERVER_KEY=<strong-random-string>
NAKAMA_HTTP_KEY=<strong-random-string>
PORT=7350
```

Important:

1. Do not wrap env values in quotes.
2. `DATABASE_ADDRESS` must not include `postgresql://` prefix.
3. Host must be plain host only.

## Render Frontend Static Site

1. Root directory: `frontend`.
2. Build command:

```bash
npm ci && npm run build
```

3. Publish directory: `dist`.
4. Set frontend env vars:

```text
VITE_NAKAMA_HOST=<backend-service>.onrender.com
VITE_NAKAMA_PORT=443
VITE_NAKAMA_USE_SSL=true
VITE_NAKAMA_HTTP_KEY=<same value as backend NAKAMA_HTTP_KEY>
```

Important:

1. `VITE_NAKAMA_HOST` must be host-only, no `https://`, no port, no trailing slash.
2. Any env var change requires frontend redeploy.

## API And Server Configuration Details

## Backend RPC Endpoints

Registered in [backend/main.go](backend/main.go):

1. `find_match`
2. `create_room`
3. `list_rooms`
4. `join_room`
5. `surrender`
6. `get_profile`
7. `close_room`
8. `check_username`

## Match Protocol Opcodes

Defined in [frontend/src/types.ts](frontend/src/types.ts):

1. `OPCODE_START = 1`
2. `OPCODE_UPDATE = 2`
3. `OPCODE_DONE = 3`
4. `OPCODE_MOVE = 4`
5. `OPCODE_REJECTED = 5`
6. `OPCODE_OPPONENT_LEFT = 6`

## Core Backend Runtime Settings

Nakama settings file: [backend/local.yml](backend/local.yml).

Current notable settings:

1. `logger.level: DEBUG`
2. session token expiry: 2 hours
3. socket max message/request limits configured

## Frontend-Nakama Client Config

From [frontend/src/nakama.ts](frontend/src/nakama.ts):

1. Server key is currently hardcoded as `defaultkey` in client initialization.
2. Healthcheck authorization uses `VITE_NAKAMA_HTTP_KEY`.
3. Base URL for healthcheck is built from `HOST`, `PORT`, and SSL flag.

If you change server keys in production, keep frontend and backend values aligned.

## How To Test Multiplayer Functionality

## Local Multiplayer Test

1. Start backend stack and frontend (see setup section).
2. Open the app in two browser sessions:
	1. normal window
	2. incognito window
3. Create two accounts and log in from both.
4. In player A session, click Create Room.
5. In player B session, click Refresh Rooms and Join.
6. Verify both players receive match start and assigned marks.
7. Play turns alternately and verify:
	1. turn enforcement works
	2. board updates are synchronized
	3. winner/draw result appears
8. Test surrender behavior from one player.
9. Test back-to-lobby behavior:
	1. when solo player leaves unfinished room, it should disappear from room list after refresh cycle.

## Backend Availability Test

1. Stop backend temporarily.
2. Confirm footer status shows backend unavailable/starting.
3. Start backend again.
4. Confirm footer returns to online.

## Quick Troubleshooting

1. If healthcheck URL is malformed, verify `VITE_NAKAMA_HOST` has no protocol.
2. If backend cannot connect DB, verify `DATABASE_ADDRESS` format and no surrounding quotes.
3. If frontend build fails on enum syntax, verify `erasableSyntaxOnly` is false in [frontend/tsconfig.app.json](frontend/tsconfig.app.json).

## Security Checklist

1. Rotate any credentials that appeared in logs or chat.
2. Use strong random values for `NAKAMA_SERVER_KEY` and `NAKAMA_HTTP_KEY`.
3. Restrict DB/network exposure where possible.