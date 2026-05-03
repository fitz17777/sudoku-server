# Sudoku

A full-featured, multiplayer Sudoku web application built with Go, HTMX, and WebSockets. Supports solo play, real-time multiplayer (collaborative, competitive, and side-by-side modes), per-puzzle and per-difficulty leaderboards, and mobile-responsive design.

---

## Features

- **Solo play** — five difficulty levels (Easy → Master), pause/resume, live score, mistake hearts
- **Multiplayer** — three modes over WebSocket:
  - **Collaborative** — both players fill the same board, share a single score and mistake counter
  - **Competitive** — race to fill cells; cells are colour-coded by who solved them
  - **Side-by-side** — each player has their own board; opponent's live score is shown as you play
- **Notes** — pencil-mode candidates per cell, auto-cleared when a digit is confirmed
- **Leaderboards** — per-difficulty best-score boards and per-puzzle boards
- **Scoring** — 100 pts × time multiplier × difficulty multiplier, per correct cell fill
- **OIDC login** — any OpenID Connect provider (tested with Keycloak)
- **Docker-first** — multi-stage Dockerfile produces a distroless ~8 MB image; `docker compose up` starts everything
- **Redis-backed** — all game state, puzzles, scores, and sessions stored in Redis with AOF persistence

---

## Quick Start

### Prerequisites

| Tool | Version |
|------|---------|
| Go | 1.22+ |
| Docker + Docker Compose | v2+ |
| Redis | 7+ (or use the bundled Docker service) |
| An OIDC provider | Keycloak, Auth0, Google, etc. |

### 1. Clone the repo

```bash
git clone https://github.com/fitz17777/sudoku-server.git
cd sudoku-server
```

### 2. Create your `.env`

```bash
cp .env.example .env
```

Open `.env` and fill in every value:

```dotenv
PORT=8080

# Internal bind IP (leave blank / 0.0.0.0 for local dev)
INTERNAL_BIND_IP=0.0.0.0

# Public base URL — must match the redirect URI registered in your OIDC provider
BASE_URL=https://sudoku.example.com

# Redis password — must match docker-compose redis command
REDIS_PASSWORD=change-me-strong-password

# OIDC (see section below)
OIDC_ISSUER=https://auth.example.com/realms/myrealm
OIDC_CLIENT_ID=sudoku
OIDC_CLIENT_SECRET=your-keycloak-client-secret
OIDC_REDIRECT_URL=https://sudoku.example.com/callback

# Session signing secret — generate with: openssl rand -hex 32
SESSION_SECRET=change-me-to-a-64-char-hex-secret

# Force puzzle regeneration on next start (useful after code changes to the generator)
PUZZLE_REGENERATE=false
```

### 3. Start with Docker Compose

```bash
docker compose up --build
```

The app starts on `http://localhost:8080`. It seeds 125 puzzles (5 difficulties × 25 each) on first boot and caches them in Redis — this takes a few seconds. The `/healthz` endpoint returns `503` until seeding is complete and `200 ok` once ready.

---

## Running Locally Without Docker

```bash
# Start Redis (or point REDIS_ADDR at an existing instance)
docker run -d --name redis -p 6379:6379 redis:7-alpine \
  redis-server --requirepass mysecret

export REDIS_ADDR=localhost:6379
export REDIS_PASSWORD=mysecret
# ... set remaining env vars ...

go run ./cmd/server
```

---

## OIDC / Keycloak Setup

The app uses OpenID Connect for authentication. Any compliant provider works; Keycloak is the primary tested target.

### Keycloak

1. Create a **realm** (e.g. `myrealm`).
2. Create a **client** with:
   - Client ID: `sudoku`
   - Client authentication: **On** (confidential)
   - Valid redirect URIs: `https://sudoku.example.com/callback`
   - Web origins: `https://sudoku.example.com`
3. Copy the client secret to `OIDC_CLIENT_SECRET`.
4. Set `OIDC_ISSUER` to `https://auth.example.com/realms/myrealm` — this must exactly match the `iss` claim in tokens.
5. Set `OIDC_REDIRECT_URL` to `https://sudoku.example.com/callback`.

> **Important:** The `iss` claim Keycloak puts in tokens equals its own configured hostname URL. If the app can't reach Keycloak at that URL, discovery will fail. The easiest fix is to ensure `OIDC_ISSUER` is the public HTTPS URL and that DNS resolves it from inside the app container.

### Other providers (Auth0, Google, etc.)

Set `OIDC_ISSUER` to the provider's issuer URL (check the `/.well-known/openid-configuration` endpoint), set `OIDC_CLIENT_ID` and `OIDC_CLIENT_SECRET` from the provider dashboard, and set `OIDC_REDIRECT_URL` to match the redirect URI registered there.

---

## Production Deployment

The included `docker-compose.yml` is designed to sit behind a **reverse proxy** (e.g. Traefik or nginx) that terminates TLS.

Key settings:

| Setting | Purpose |
|---------|---------|
| `INTERNAL_BIND_IP` | Bind the app to a private/internal IP so port 8080 is not exposed to the public internet |
| `read_only: true` | The app container's filesystem is read-only (all data goes to Redis) |
| `cap_drop: ALL` | No Linux capabilities |
| `no-new-privileges: true` | Process cannot gain new privileges |
| Redis has no ports mapping | Redis is only reachable from within the Docker bridge network |

**Firewall recommendation:** Allow port 8080 only from the Traefik VM's internal IP:

```bash
ufw allow from <traefik-vm-ip> to any port 8080
```

---

## Project Structure

```
.
├── cmd/server/          # main package — router, server setup, graceful shutdown
├── internal/
│   ├── auth/            # OIDC login, session cookie middleware
│   ├── config/          # env-based config loading
│   ├── game/            # puzzle types, scoring formula, Sudoku generator/seeder
│   ├── handler/         # HTTP handlers: game, multiplayer, leaderboard, auth, WS
│   ├── hub/             # WebSocket hub: Hub, Room, Client, message types
│   ├── redis/           # Redis data layer: puzzles, games, rooms, scores, sessions
│   └── templates/       # Template renderer + custom Go template functions
├── web/
│   ├── static/          # JS (QR code library)
│   └── templates/
│       ├── layout/      # base.html (CSS, JS utilities, nav)
│       ├── pages/       # full-page templates
│       └── partials/    # HTMX OOB swap fragments
├── docker-compose.yml
├── Dockerfile
├── .env.example
└── go.mod
```

---

## Multiplayer Architecture

Multiplayer uses a single **WebSocket hub** (`internal/hub`) running in a dedicated goroutine — no mutexes needed for game state. Each connected client has a read and write goroutine that fan messages into and out of a central `inbound` channel.

```
Browser ──WS──► Client.readPump ──► hub.inbound ──► hub.Run() ──► Room.dispatch()
Browser ◄─WS── Client.writePump ◄── client.send ◄── Room.broadcast()
```

Room state is persisted to Redis after every cell fill so reconnecting players rejoin seamlessly.

### Game modes

| Mode | Board | Scoring |
|------|-------|---------|
| Collaborative | Shared — both players fill the same cells | Shared score and mistakes |
| Competitive | Shared — cells are colour-coded by solver | Per-player score |
| Side-by-side | Each player has their own board | Per-player, opponent score shown live |

---

## Scoring

```
pts = 100 × time_multiplier × difficulty_multiplier
```

| Factor | Formula |
|--------|---------|
| `time_multiplier` | Decays linearly from 1.0 → 0.1 over the difficulty's time limit |
| `difficulty_multiplier` | Easy 1.0 × Medium 1.2 × Hard 1.5 × Expert 2.0 × Master 3.0 |

Leaderboards track each player's **best single-game score** per difficulty (not cumulative).

---

## Puzzle Generation

On startup, the seeder generates 25 puzzles per difficulty (125 total) using a backtracking solver + clue-removal loop, then stores them in Redis. Puzzles are only regenerated if `PUZZLE_REGENERATE=true` is set or if fewer than 25 exist for a given difficulty. Subsequent restarts reuse the cached puzzles instantly.

---

## Development Tips

**Rebuild templates without restarting:** Templates are embedded at compile time via `embed.FS`. During development, run `go run ./cmd/server` directly instead of Docker — you can recompile quickly without container rebuilds.

**Force puzzle regeneration:**

```bash
PUZZLE_REGENERATE=true go run ./cmd/server
```

**Clear leaderboard data (e.g. after scoring algorithm changes):**

```bash
redis-cli -a "$REDIS_PASSWORD" DEL diff_score:easy diff_score:medium diff_score:hard diff_score:expert diff_score:master
redis-cli -a "$REDIS_PASSWORD" DEL diff_time:easy  diff_time:medium  diff_time:hard  diff_time:expert  diff_time:master
```

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.22, chi router, gorilla/websocket |
| Frontend | HTMX, vanilla JS (no build step), HTML5 |
| Auth | go-oidc v3, oauth2 |
| Storage | Redis 7 (sorted sets, hashes, strings) |
| Container | Docker multi-stage, distroless runtime image |
| TLS | Terminated externally by Traefik (or any reverse proxy) |

---

## License

MIT
