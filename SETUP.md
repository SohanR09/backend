# Setup guide

How to install, run, test and understand the **Geofence backend** (Go + PostgreSQL) and the **Fenceline frontend** (React). Start with the Docker route if you only want to see it working.

**Quick start (Docker, about 5 minutes):**

```bash
cd backend
cp .env.example .env              # then open .env and set your own DB_PASSWORD
docker compose up --build         # wait for "geofence API listening on :8080"
curl http://localhost:8080/health
cd ../frontend && cp .env.example .env.local   # set VITE_API_URL=http://localhost:8080
npm install && npm run dev        # open http://localhost:5173
```

Contents

1. [Prerequisites](#1-prerequisites)
2. [Local setup (no Docker)](#2-local-setup-no-docker)
3. [Run with Docker Compose](#3-run-with-docker-compose)
4. [API testing guide](#4-api-testing-guide)
5. [Frontend usage guide](#5-frontend-usage-guide)
6. [Architecture overview](#6-architecture-overview)
7. [Production notes](#7-production-notes)
8. [Troubleshooting](#8-troubleshooting)

This file assumes the two projects sit side by side:

```
geofence/
  backend/     <- this repository (SETUP.md lives here)
  frontend/    <- the Fenceline React app
```

The full API reference is in [`README.md`](README.md). This guide covers getting everything running.

---

## 1. Prerequisites

| Tool | Version | Needed for |
|---|---|---|
| Docker Desktop (or Docker Engine + Compose v2) | any recent | Running the API and PostgreSQL together (easiest) |
| Go | 1.24 or newer | Running the API without Docker, running the tests |
| PostgreSQL | 16 recommended | Running the API without Docker (Docker Compose supplies its own) |
| Node.js | 20.19+ or 22.12+ | The frontend (Vite 8 requires this) |
| npm | ships with Node | Installing frontend packages |
| curl | any | Trying the API from a terminal |

### Dependencies

**Backend.** One third-party Go module: `github.com/jackc/pgx/v5` (PostgreSQL driver). The WebSocket server is implemented in the project, so there is no WebSocket library. `go.sum` is not committed, so the first `go mod tidy` (or the Docker build) downloads the driver and creates it. That step needs internet access.

**Frontend.** React 19, React Router 7, Redux Toolkit, Leaflet and react-leaflet for the map, Tailwind CSS 4, Vite 8 and TypeScript. `npm install` fetches all of them. The map tiles are loaded at runtime from OpenStreetMap, so the browser needs internet access to draw the map background.

---

## 2. Local setup (no Docker)

Use this when you want to edit code and restart quickly. You need Go and a reachable PostgreSQL.

### 2.1 Database

Create an empty database, or let the API create it for you. With the default `DB_AUTO_CREATE=true` the API creates the database on first start if the user has the `CREATEDB` right (the default `postgres` user does). The API creates all tables itself, so there is no migration step.

### 2.2 Start the API

Run these inside the `backend` folder.

**macOS / Linux (bash)**

```bash
go mod tidy                      # first time only: downloads the pgx driver, writes go.sum
export DB_HOST=localhost DB_PORT=5432 DB_USER=postgres DB_PASSWORD='your-password' DB_NAME=geofence
go run ./cmd/server
```

**Windows (PowerShell)**

```powershell
go mod tidy
$env:DB_HOST="localhost"; $env:DB_PORT="5432"; $env:DB_USER="postgres"; $env:DB_PASSWORD="your-password"; $env:DB_NAME="geofence"
go run ./cmd/server
```

Expected log lines:

```
connected to PostgreSQL at postgres://postgres:xxxxx@localhost:5432/geofence?sslmode=disable
geofence API listening on :8080 (auth=false, rate limit=50/s burst 100)
```

Check it:

```bash
curl http://localhost:8080/health
# {"status":"ok","time_ns":"..."}
```

You can pass one connection string instead of the `DB_*` parts:

```bash
export DATABASE_URL='postgres://user:pass@localhost:5432/geofence?sslmode=disable'
```

### 2.3 Environment variables

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | HTTP port |
| `DATABASE_URL` | empty | Full connection URL. When set, it overrides every `DB_*` value |
| `DB_HOST` / `DB_PORT` | `localhost` / `5432` | Database address |
| `DB_USER` / `DB_PASSWORD` | `postgres` / none | Credentials |
| `DB_NAME` | `geofence` | Database name |
| `DB_SSLMODE` | `disable` | Use `require` for hosted databases |
| `DB_AUTO_CREATE` | `true` | Create the database on start when it is missing |
| `DB_CONNECT_RETRIES` | `30` | Connection attempts, 2 seconds apart |
| `API_KEY` | empty | When set, every request except `/health` needs this key |
| `RATE_LIMIT_PER_SEC` / `RATE_LIMIT_BURST` | `50` / `100` | Limit on `POST /vehicles/location` per client. `0` disables it |
| `TRUST_PROXY` | `false` | Use `X-Forwarded-For` to identify clients (set `true` behind a reverse proxy) |
| `ALLOWED_ORIGIN` | `*` | Value of the CORS `Access-Control-Allow-Origin` header |

### 2.4 Run the tests

```bash
go test ./...                                   # unit tests, no database needed
```

The PostgreSQL contract tests are behind a build tag and **wipe the target database**, so point them at a throwaway one:

```bash
export TEST_DATABASE_URL='postgres://postgres:pw@localhost:5432/geofence_test?sslmode=disable'   # create geofence_test first
go test -tags integration ./internal/store/
```

### 2.5 Start the frontend

See [section 5.1](#51-install-and-start). The short version:

```bash
cd ../frontend
cp .env.example .env.local        # then set VITE_API_URL=http://localhost:8080
npm install
npm run dev                       # http://localhost:5173
```

---

## 3. Run with Docker Compose

`docker-compose.yml` starts two containers:

| Service | Image | What it does |
|---|---|---|
| `db` | `postgres:16-alpine` | Stores everything in the named volume `pgdata`, so data survives restarts and rebuilds |
| `api` | built from `Dockerfile` | The Go API on port 8080. Waits until `db` is healthy |

Nothing else needs to be installed.

### 3.1 First start

Run these inside the `backend` folder (the one that contains `docker-compose.yml`).

```bash
cp .env.example .env              # PowerShell: Copy-Item .env.example .env
# edit .env and set DB_PASSWORD to your own value (compose refuses to start while it is empty)
docker compose up --build
```

The first build takes a few minutes. Wait for:

```
api-1  | connected to PostgreSQL at postgres://geofence:xxxxx@db:5432/geofence?sslmode=disable
api-1  | geofence API listening on :8080 ...
```

Then:

```bash
curl http://localhost:8080/health
```

`.env` is git-ignored. Do not commit it.

### 3.2 Settings that compose reads from `.env`

`DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_HOST_PORT` (host port for the database, default 5432), `PORT` (host port for the API, default 8080), `API_KEY`, `RATE_LIMIT_PER_SEC`, `RATE_LIMIT_BURST`, `TRUST_PROXY`, `ALLOWED_ORIGIN`.

PostgreSQL reads the user, password and database name only when the volume is first created. Changing them later does not change an existing database. Run `docker compose down -v` to start again from an empty one (this deletes the data).

### 3.3 Everyday commands

| Task | Command |
|---|---|
| Start in the background | `docker compose up --build -d` |
| Watch API logs | `docker compose logs -f api` |
| Stop, keep data | `docker compose down` |
| Stop and delete all data | `docker compose down -v` |
| Open a SQL shell | `docker compose exec db psql -U geofence -d geofence` |
| List the tables | `docker compose exec db psql -U geofence -d geofence -c "\dt"` |
| Back up | `docker compose exec -T db pg_dump -U geofence geofence > backup.sql` |
| Restore | `docker compose exec -T db psql -U geofence -d geofence < backup.sql` |

The database is published on `127.0.0.1` only, so tools such as DBeaver or pgAdmin on your own machine can connect, but other machines on your network cannot.

### 3.4 Run the frontend against the Docker backend

Compose does not start the frontend. Run it separately (section 5.1) with `VITE_API_URL=http://localhost:8080`.

---

## 4. API testing guide

Start the API first (section 2 or 3). The examples use bash and `curl`. On Windows PowerShell, call `curl.exe` (plain `curl` is an alias of another command) or use the PowerShell versions in [`README.md`](README.md#c-run-each-route-by-hand-powershell).

If you set `API_KEY`, add `-H "X-API-Key: your-key"` to every request except `/health`.

### 4.1 Quick checks

| Method | What |
|---|---|
| Postman | Import both files from `docs/postman/`, pick the **Geofence API - local** environment, then run the collection. It runs 42 requests with 125 assertions |
| PowerShell | `powershell -ExecutionPolicy Bypass -File .\scripts\smoke-test.ps1` |
| Unit tests | `go test ./...` |

### 4.2 A complete walkthrough with curl

Run these in one terminal, in order. Later commands reuse shell variables set by earlier ones. This needs `jq` for pulling ids out of responses (`sudo apt install jq`, `brew install jq`); if you do not have it, copy the ids by hand.

```bash
BASE=http://localhost:8080
# If API_KEY is set, add -H "X-API-Key: your-key" to every curl call below (except /health).
ts() { date -u -d "$1 minutes ago" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v-"$1"M +%Y-%m-%dT%H:%M:%SZ; }
```

**1. Create a geofence.** Coordinates are `[latitude, longitude]`, the ring must be closed (first point equals last) and have at least 4 points.

```bash
GEO=$(curl -s -X POST $BASE/geofences -H "Content-Type: application/json" -d '{
  "name": "Downtown Restricted Zone",
  "description": "No entry",
  "category": "restricted_zone",
  "coordinates": [[37.7749,-122.4194],[37.7849,-122.4194],[37.7849,-122.4094],[37.7749,-122.4094],[37.7749,-122.4194]]
}')
echo "$GEO"                        # 201 {"id":"geo_...","name":"...","status":"active",...}
GEO_ID=$(echo "$GEO" | jq -r .id)
```

Categories: `delivery_zone`, `restricted_zone`, `toll_zone`, `customer_area`.

**2. List geofences.**

```bash
curl -s "$BASE/geofences"
curl -s "$BASE/geofences?category=restricted_zone"
```

**3. Register a vehicle.** All four fields are required. `vehicle_number` must be unique.

```bash
VEH=$(curl -s -X POST $BASE/vehicles -H "Content-Type: application/json" -d '{
  "vehicle_number": "KA-01-AB-1234", "driver_name": "John Doe", "vehicle_type": "truck", "phone": "+1234567890"
}')
echo "$VEH"                        # 201; a second run returns 409 because the number is taken
VEH_ID=$(echo "$VEH" | jq -r .id)
curl -s $BASE/vehicles
```

**4. Create an alert rule.** Leave out `vehicle_id` to apply the rule to every vehicle. `event_type` is `entry`, `exit` or `both`. Without a rule, entries and exits are still recorded in the history, but no alert is pushed.

```bash
ALERT=$(curl -s -X POST $BASE/alerts/configure -H "Content-Type: application/json" \
  -d "{\"geofence_id\":\"$GEO_ID\",\"event_type\":\"both\"}")
echo "$ALERT"
ALERT_ID=$(echo "$ALERT" | jq -r .alert_id)
curl -s "$BASE/alerts?geofence_id=$GEO_ID"
```

**5. Listen for live alerts** in a second terminal (needs `websocat`, or use the browser console trick below).

```bash
websocat ws://localhost:8080/ws/alerts      # with a key: ws://localhost:8080/ws/alerts?api_key=your-key
```

No tools to install: open any browser tab, press F12, and paste this in the console.

```js
const ws = new WebSocket("ws://localhost:8080/ws/alerts");
ws.onmessage = e => console.log(JSON.parse(e.data));
```

**6. Move the vehicle: outside, inside, outside.** The second call is an entry and the third an exit, so two alerts appear in the listener.

```bash
move() { curl -s -X POST $BASE/vehicles/location -H "Content-Type: application/json" \
  -d "{\"vehicle_id\":\"$VEH_ID\",\"latitude\":$1,\"longitude\":$2,\"timestamp\":\"$(ts $3)\"}"; echo; }

move 37.70   -122.50   5     # outside  -> "current_geofences": []
move 37.7799 -122.4144 4     # inside   -> entry, lists the zone
move 37.70   -122.50   3     # outside  -> exit
```

Timestamps are ISO 8601. A report older than the vehicle's newest one is stored but does not change its state.

**7. Read the results.**

```bash
curl -s $BASE/vehicles/location/$VEH_ID                          # one vehicle: position and zones it is in
curl -s $BASE/vehicles/locations                                 # every vehicle that has reported
curl -s "$BASE/violations/history?vehicle_id=$VEH_ID&limit=10"   # entry/exit log, newest first
curl -s "$BASE/alerts/events?limit=10"                           # alerts that were pushed
```

History filters: `vehicle_id`, `geofence_id`, `start_date`, `end_date` (ISO 8601, URL-encode the colons if you type them by hand), `limit` (default 50, max 500) and `offset`. The response includes `total_count`.

**8. Delete the rule.**

```bash
curl -s -X DELETE $BASE/alerts/$ALERT_ID
```

### 4.3 Error cases worth trying

```bash
curl -si -X POST $BASE/geofences -H "Content-Type: application/json" -d '{"name":"x","category":"nope","coordinates":[]}'   # 400
curl -si $BASE/vehicles/location/veh_does_not_exist                                                                       # 404
curl -si $BASE/no/such/route                                                                                              # 404
```

Every response, including errors, carries `time_ns` (handler time in nanoseconds), and errors have the shape `{"error": "...", "time_ns": "..."}`.

| Status | Meaning |
|---|---|
| 400 | Validation failed, malformed JSON or a bad query parameter |
| 401 | `API_KEY` is set and the key is missing or wrong |
| 404 | Unknown vehicle, geofence, alert or route |
| 409 | Duplicate `vehicle_number` |
| 413 | Body larger than 1 MiB |
| 429 | Location updates too fast. The `Retry-After` header says how long to wait |
| 500 | Unexpected server or database error |

---

## 5. Frontend usage guide

Fenceline is a single-page app for working with the API: draw zones, register vehicles, move them on a map, set alert rules, watch live alerts and browse the history.

### 5.1 Install and start

```bash
cd frontend
cp .env.example .env.local
```

Open `.env.local` and set the API address. The example file points at a hosted backend, so change it for local work:

```
VITE_API_URL=http://localhost:8080
# VITE_API_KEY=your-key        # only when the backend has API_KEY set
```

```bash
npm install
npm run dev                    # http://localhost:5173 with hot reload
```

| Command | What it does |
|---|---|
| `npm run dev` | Dev server on port 5173 |
| `npm run build` | Type-check, then build to `dist/` |
| `npm start` | Serve `dist/` (uses `$PORT`, default 4173). Run `npm run build` first |
| `npm run typecheck` | TypeScript check only |

You can also set the address at runtime, without rebuilding, by editing `public/config.js` (or `dist/config.js` after a build): `API_URL`, `API_KEY` and `TILE_URL` (map tile template, empty means OpenStreetMap). A value in `config.js` wins over `.env.local`. The file is public, so an API key placed there is a deterrent, not a secret.

The backend must allow the site's origin. The default `ALLOWED_ORIGIN=*` does.

### 5.2 A first session

Follow this order and you will see every feature work.

1. **Zones** (`/zones`). Click corners on the map to outline an area, name it, pick a category and save. The list on the side shows saved zones, and clicking one flies the map to it.
2. **Vehicles** (`/vehicles`). Add a vehicle with its number, driver, type and phone. A vehicle appears on the map only after its first reported position.
3. **Alert rules** (`/rules`). Choose a zone, optionally one vehicle (blank means all), and whether to alert on entry, exit or both. Delete a rule with its **Delete** button (the app asks you to confirm).
4. **Live map** (`/`). Check that the sidebar shows **Receiving live alerts** (the WebSocket is connected). Under **Move a vehicle**, pick the vehicle, then click the map. The page sends that point to the API as the vehicle's new position. A message tells you which zones it is now inside. Click inside the zone, then outside again.
5. **Alert feed.** The entry and the exit appear in the feed as they happen and the vehicle marker pulses. Click an alert to fly the map to where it happened.
6. **History** (`/history`). Filter by vehicle, zone and date range, then page through the entry and exit log, 25 rows at a time.

### 5.3 Good to know

- This frontend is the only thing that reports positions in the demo: it posts the point you click, stamped with the current time. In real use, a GPS device or another service sends the same `POST /vehicles/location` request.
- The live map refreshes vehicle positions every 10 seconds and again whenever an alert arrives.
- If the connection drops, the sidebar shows **Reconnecting**. The app retries with growing delays (1 s up to 15 s) and, once reconnected, loads the alerts it missed.
- Zones cannot be edited or deleted, because the backend has no endpoints for that.
- Without a matching alert rule no alert is raised, but the entry and exit are still stored and show in History.

### 5.4 Deploying the frontend

`npm run build` produces a static site in `dist/`. The app uses real URLs such as `/zones`, so the host must serve `index.html` for any path or a page refresh returns 404. This is already set up for Vercel (`vercel.json`) and for Netlify and Cloudflare Pages (`public/_redirects`). On a Node host, run `npm start`. On nginx add `location / { try_files $uri /index.html; }`.

After deploying, set `API_URL` in `dist/config.js`, and set `ALLOWED_ORIGIN` on the backend to the frontend's address.

---

## 6. Architecture overview

```
                    ┌───────────────────────────── Browser ─────────────────────────────┐
                    │  React pages  ──►  Redux store  ──►  src/api.ts   (fetch)         │
                    │                        ▲             src/socket.ts (WebSocket)    │
                    └────────────────────────┼───────────────────┬───────────────┬──────┘
                                             │ alerts            │ REST / JSON   │ WS /ws/alerts
                                             │                   ▼               │
 GPS device / other client ─── POST /vehicles/location ──►  ┌──────────── Go API (:8080) ────────────┐
                                                            │ middleware: recover, CORS, log, API    │
                                                            │ key, body limit, rate limit            │
                                                            │ handlers  ──►  store.Store interface   │
                                                            │ alert worker (queue) ──► WebSocket hub │
                                                            └───────────────────┬────────────────────┘
                                                                                │ SQL
                                                                                ▼
                                                                  PostgreSQL (volume "pgdata")
```

### 6.1 Backend layout

```
backend/
  cmd/server/main.go        wires config, database, hub and API; graceful shutdown
  internal/
    config/                 environment settings (config.go) and the connection-string builder (dsn.go)
    database/               connection pool with retry, creates the database on first start
    model/                  domain types: Geofence, Vehicle, Location, AlertRule, Violation, AlertEvent
    geo/                    polygon validation, bounding box, point-in-polygon
    store/                  Store interface; postgres.go (production), memory.go (tests only),
                            migrations/schema.sql (embedded in the binary, applied at start)
    ws/                     WebSocket hub, written from the RFC: handshake, framing, ping/pong, broadcast
    api/                    handlers, middleware, rate limiter, response helpers, alert delivery worker
  docs/postman/             Postman collection and environment
  scripts/smoke-test.ps1    PowerShell test of every route
  Dockerfile, docker-compose.yml, .env.example
```

Dependencies point inward: `api` depends on the `store.Store` interface, not on PostgreSQL, so the tests run it against the in-memory store. `config` and `database` are used only by `main.go`.

### 6.2 What happens on a position update

1. A client sends `POST /vehicles/location`. The request passes through recover, CORS, logging, API key check and the 1 MiB body limit. This route is also rate limited.
2. The handler validates the body and loads the vehicle.
3. PostgreSQL finds the geofences containing the point: a GiST-indexed bounding-box lookup, then an exact point-in-polygon test (a point on an edge counts as inside).
4. The new location is saved. If it is the newest for that vehicle, the stored set of zones the vehicle is inside is replaced in the same step (under a row lock, so concurrent updates cannot corrupt it).
5. Comparing the old and new set gives the entries and exits. Each one is written to `violations`.
6. If an active alert rule matches, the event is put on an in-memory queue. This never blocks the response.
7. The response returns `current_geofences`.
8. In the background, a worker saves the event to `alert_events` and hands it to the WebSocket hub, which pushes it to every connected client. Slow clients are dropped instead of holding others up.

### 6.3 Data stored

| Table | Holds |
|---|---|
| `geofences` | Polygon plus an indexed bounding box and category |
| `vehicles` | Registered vehicles |
| `vehicle_locations` | Every reported position (full history) |
| `vehicle_geofence_state` | The zones each vehicle is inside right now |
| `alert_rules` | Which entries and exits should raise alerts |
| `violations` | Every entry and exit |
| `alert_events` | Alerts that were pushed, used to catch up after a reconnect |

The schema is applied automatically at start-up inside one transaction guarded by an advisory lock, so several instances starting together do not clash.

### 6.4 Frontend layout and flow

```
frontend/src/
  main.tsx        entry: store, router, WebSocket
  App.tsx         routes: / /zones /vehicles /rules /history
  config.ts       reads public/config.js and VITE_* variables
  api.ts          the only place that makes HTTP calls
  socket.ts       WebSocket client with reconnect and backoff
  store/          Redux slices: geofences, vehicles, alerts, history, ui
  pages/          Live, Zones, Vehicles, Rules, History
  components/     Shell (layout), MapView (Leaflet), Feed, Toasts, UI parts
```

Components never call `fetch`. They dispatch Redux thunks, the thunks call `api.ts`, and the store is the single source of truth. The WebSocket is opened once, outside React, and every alert it receives goes into the store, pulses the vehicle marker and refreshes positions.

### 6.5 Design choices and limits

- One PostgreSQL database is the only state, so the API can be restarted at any time.
- WebSocket broadcasts reach only clients connected to the instance that handled the update. With several API instances you would need a shared bus such as PostgreSQL `LISTEN/NOTIFY` or Redis.
- Polygons are treated as flat in latitude and longitude. This is fine for city-sized zones, but not for polygons that cross the antimeridian or enclose a pole.
- Authentication is one optional shared `API_KEY`. There are no user accounts.
- The Docker build runs `go mod tidy`, so building the image needs internet access.

---

## 7. Production notes

The defaults are meant for local use. Before exposing the system to other people:

| Area | What to do |
|---|---|
| Database password | Replace the sample `DB_PASSWORD` in `.env.example` with a strong, unique value. Never commit `.env` |
| API key | Set `API_KEY`. The frontend's `config.js` is a public file, so a key placed there can be read by anyone who opens the site. Treat it as a deterrent, not a secret |
| CORS | Set `ALLOWED_ORIGIN` to the frontend's exact address instead of `*` |
| HTTPS | Put the API and the frontend behind a reverse proxy or host that provides HTTPS. Browsers then use `wss://` for the alert stream automatically, because the frontend derives it from the API address |
| Proxy headers | Set `TRUST_PROXY=true` only when a proxy you control sits in front of the API. Otherwise clients can fake `X-Forwarded-For` and dodge the rate limit |
| Hosted PostgreSQL | Use `DB_SSLMODE=require` (or a `DATABASE_URL` with `sslmode=require`) |
| Database exposure | Compose publishes PostgreSQL on `127.0.0.1` only. Keep it that way, or remove the `ports` entry from the `db` service |
| Backups | Schedule `pg_dump` (section 3.3) and keep copies outside the Docker host. The `pgdata` volume is the only copy of the data |
| Scaling | WebSocket alerts reach only the clients connected to the instance that handled the update, so run a single API instance until a shared message bus is added |
| Dependencies | Commit `go.sum` after your first `go mod tidy` so builds are repeatable |

---

## 8. Troubleshooting

| Symptom | Likely cause and fix |
|---|---|
| `password authentication failed` in the API log | The volume was created earlier with a different password. Restore the old one in `.env`, or run `docker compose down -v` (deletes data) |
| `port is already allocated` on 5432 | Another PostgreSQL uses that port. Set `DB_HOST_PORT=5433` in `.env` |
| API keeps printing `waiting for PostgreSQL` | The database is still starting or is unhealthy. Check `docker compose logs db`. Without Docker, check `DB_HOST`, `DB_PORT` and the password |
| `missing go.sum entry` when building | Run `go mod tidy` once, with internet access |
| `database ... does not exist and could not be created` | The user lacks `CREATEDB`. Create the database by hand, or use the `postgres` user |
| `401 missing or invalid API key` | `API_KEY` is set. Send `X-API-Key`, or `?api_key=` on the WebSocket |
| `429 rate limit exceeded` | Too many location updates. Wait for `Retry-After`, or raise `RATE_LIMIT_PER_SEC` |
| Frontend says "The API address is not configured" | Set `VITE_API_URL` in `.env.local` or `API_URL` in `public/config.js`, then restart `npm run dev` |
| Frontend says "Cannot reach the API" | The backend is down, the URL is wrong, or `ALLOWED_ORIGIN` does not include the site |
| Sidebar shows **Alerts offline** | No API address, so no WebSocket |
| Map is grey with no background | The browser cannot reach the OpenStreetMap tile server |
| Refreshing `/zones` gives 404 after deploying | The host has no single-page-app fallback (section 5.4) |
| A vehicle is not on the map | It has not reported a position yet. Use **Move a vehicle** on the Live page |
