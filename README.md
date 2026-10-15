# Geofencing & Real-time Alert API (Go + PostgreSQL)

Backend for a geofencing and vehicle tracking system: define polygon geofences, register vehicles, post
locations, detect entry/exit events and push real-time alerts over WebSocket.

- **Go 1.24** API, **PostgreSQL** for all storage (runs in Docker, data persisted in the `pgdata` volume)
- Every data route reads/writes PostgreSQL: geofences, vehicles, locations, alert rules, entry/exit history, delivered alerts
- Point-in-polygon: GiST-indexed bounding-box lookup in Postgres, then exact ray-casting test (edge/vertex = inside)
- WebSocket alerts at `/ws/alerts` (own RFC 6455 implementation, no extra library)
- Postman collection with 125 automated assertions in [`docs/postman/`](docs/postman)

---

## 1. Run it with Docker (API + PostgreSQL, data in a Docker volume)

`docker compose up` starts two containers: the API and a `postgres:16-alpine` database. All data is stored in the
named Docker volume **`pgdata`**, so it survives `docker compose down`, rebuilds and restarts. Nothing needs to be
installed on your machine except Docker. The API creates all tables itself on start-up.

### Step 1: create your `.env`

No credentials are shipped in this project. Copy the template and set your own database password:

```powershell
Copy-Item .env.example .env
notepad .env        # set DB_PASSWORD (required); DB_USER and DB_NAME can stay as they are
```

`.env` is git-ignored and excluded from the Docker image. Do not commit it or share it. Postgres reads the user,
password and database name only the **first** time the volume is created; changing them later has no effect on
existing data (use `docker compose down -v` to start from scratch). `docker compose` refuses to start while
`DB_PASSWORD` is empty.

### Step 2: start everything

In PowerShell, **inside the `backend` folder** (the one containing `docker-compose.yml`):

```powershell
docker compose up --build
```

First start takes a few minutes (downloads Go and Postgres, builds the API). Wait for:

```
api-1  | connected to PostgreSQL at postgres://geofence:xxxxx@db:5432/geofence?sslmode=disable
api-1  | geofence API listening on :8080 ...
```

```powershell
Invoke-RestMethod http://localhost:8080/health
```

See the stored data (pgAdmin, DBeaver, or psql). The database is published on `127.0.0.1:5432` only:

```powershell
docker compose exec db psql -U geofence -d geofence -c "\dt"
docker compose exec db psql -U geofence -d geofence -c "select * from violations order by recorded_at desc limit 5;"
```

| Task | Command |
|---|---|
| Run in background | `docker compose up --build -d` |
| Watch API logs | `docker compose logs -f api` |
| Stop (data stays in the volume) | `docker compose down` |
| Use another API port | `$env:PORT=9000; docker compose up --build` |
| Use another host port for the database | `$env:DB_HOST_PORT=5433; docker compose up --build` |
| Delete all data | `docker compose down -v` |
| List the volume | `docker volume ls` (named `<folder>_pgdata`, e.g. `backend_pgdata`) |

### Backing up and restoring the volume

```powershell
# backup to a file
docker compose exec -T db pg_dump -U geofence geofence > geofence-backup.sql
# restore into a running stack
Get-Content geofence-backup.sql | docker compose exec -T db psql -U geofence -d geofence
```

### If something goes wrong

Check `docker compose logs api` and `docker compose logs db`:

| Message | Cause and fix |
|---|---|
| `password authentication failed` | The volume was created earlier with a different password. Either put the old password back in `.env` or run `docker compose down -v` (deletes data) |
| `port is already allocated` (5432) | Another PostgreSQL is using 5432. Set `DB_HOST_PORT=5433` in `.env` |
| API keeps waiting for PostgreSQL | The `db` container is still starting or unhealthy: `docker compose logs db` |

### Other ways to run

- **Without Docker** (needs Go 1.24+): see [section 6](#6-development-without-docker-optional). Use `DB_HOST=localhost`.

### PostgreSQL variables

| Variable | Default | Meaning |
|---|---|---|
| `DB_HOST` | `localhost` | Database host as seen by the API (set to `db` automatically by `docker-compose.yml`) |
| `DB_PORT` | `5432` | Database port |
| `DB_USER` | `postgres` | Database user |
| `DB_PASSWORD` | _(none)_ | Database password (set it in `.env`) |
| `DB_NAME` | `geofence` | Database name (created automatically if missing) |
| `DB_SSLMODE` | `disable` | Use `require` for hosted databases |
| `DB_AUTO_CREATE` | `true` | Create the database on start-up if it does not exist (needs `CREATEDB`) |
| `DATABASE_URL` | _(empty)_ | Full connection URL; if set it overrides all `DB_*` values, e.g. `postgres://user:pass@host:5432/dbname?sslmode=require`. URL-encode special characters in the password |
| `DB_CONNECT_RETRIES` | `30` | How many times (2 s apart) the API waits for the database at start-up |

Tables (created automatically): `geofences` (polygon + GiST-indexed bounding box), `vehicles`, `vehicle_locations`
(full location history), `vehicle_geofence_state` (which zones each vehicle is in now), `alert_rules`, `violations`
(entry/exit history), `alert_events` (alerts that were pushed).

### API variables

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | API port on your PC |
| `API_KEY` | _(empty)_ | If set, every request except `GET /health` must carry the key (see [Auth](#auth)) |
| `RATE_LIMIT_PER_SEC` | `50` | Sustained `POST /vehicles/location` rate per client (`0` disables) |
| `RATE_LIMIT_BURST` | `100` | Burst size for the limiter |
| `TRUST_PROXY` | `false` | Use `X-Forwarded-For` for rate limiting (set `true` behind a reverse proxy) |
| `ALLOWED_ORIGIN` | `*` | `Access-Control-Allow-Origin` value |

---

## 2. Test it

Three ways, pick any. Start the API first (section 1).

### A. One-command PowerShell smoke test (all routes)

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\smoke-test.ps1
# with an API key:   ... -File .\scripts\smoke-test.ps1 -ApiKey secret
# other port:        ... -File .\scripts\smoke-test.ps1 -BaseUrl http://localhost:9000
```

Prints `PASS`/`FAIL` for every check and ends with a summary. It can be run repeatedly.

### B. Postman

1. Postman → **Import** → select both files from the `docs/postman` folder:
   `Geofence-API.postman_collection.json` and `Geofence-Local.postman_environment.json`.
2. Top-right environment dropdown → **Geofence API - local**.
3. Open the collection → **Run** → **Run Geofence & Real-time Alert API**. It runs 42 requests in order, saves ids
   between requests, and checks 125 assertions (status codes, response shape, `time_ns`, entry/exit logic, filters,
   pagination, error cases). It is safe to run repeatedly.
4. Variables: `baseUrl` (default `http://localhost:8080`) and `apiKey` (leave empty unless you set `API_KEY`).

**Real-time alerts in Postman:** *New → WebSocket*, URL `ws://localhost:8080/ws/alerts`, click **Connect**, then run
folder *4. Location updates and real-time alerts*; alerts appear in the WebSocket window.

### C. Run each route by hand (PowerShell)

Run these **in order in one PowerShell window** (later commands use variables set by earlier ones).
Full request/response details for each route are in [section 4](#4-routes).

```powershell
$base = "http://localhost:8080"
$h = @{}                      # with an API key use:  $h = @{ "X-API-Key" = "secret" }
function Ts([int]$minAgo) { (Get-Date).ToUniversalTime().AddMinutes(-$minAgo).ToString("yyyy-MM-ddTHH:mm:ssZ") }
```

**Live alerts without installing anything:** open any browser tab, press `F12` → Console and paste

```js
const ws = new WebSocket("ws://localhost:8080/ws/alerts");
ws.onmessage = e => console.log(JSON.parse(e.data));
```

Then run the commands below; entry/exit alerts print in the console immediately.

```powershell
# 1. POST /geofences
$geo = Invoke-RestMethod -Method Post -Uri "$base/geofences" -Headers $h -ContentType "application/json" -Body @'
{ "name": "Downtown Restricted Zone", "description": "No entry", "category": "restricted_zone",
  "coordinates": [[37.7749,-122.4194],[37.7849,-122.4194],[37.7849,-122.4094],[37.7749,-122.4094],[37.7749,-122.4194]] }
'@
$geo; $geoId = $geo.id

# 2. GET /geofences (all, then filtered)
Invoke-RestMethod "$base/geofences" -Headers $h | ConvertTo-Json -Depth 6
Invoke-RestMethod "$base/geofences?category=restricted_zone" -Headers $h | ConvertTo-Json -Depth 6

# 3. POST /vehicles   (vehicle_number must be unique: change it if you re-run)
$veh = Invoke-RestMethod -Method Post -Uri "$base/vehicles" -Headers $h -ContentType "application/json" -Body @'
{ "vehicle_number": "KA-01-AB-1234", "driver_name": "John Doe", "vehicle_type": "truck", "phone": "+1234567890" }
'@
$veh; $vehId = $veh.id

# 4. GET /vehicles
Invoke-RestMethod "$base/vehicles" -Headers $h | ConvertTo-Json -Depth 6

# 7. POST /alerts/configure   (omit vehicle_id = all vehicles; event_type: entry | exit | both)
$alert = Invoke-RestMethod -Method Post -Uri "$base/alerts/configure" -Headers $h -ContentType "application/json" `
  -Body (@{ geofence_id = $geoId; event_type = "both" } | ConvertTo-Json)
$alert; $alertId = $alert.alert_id

# 8. GET /alerts   (optional ?geofence_id= and ?vehicle_id=)
Invoke-RestMethod "$base/alerts?geofence_id=$geoId" -Headers $h | ConvertTo-Json -Depth 6

# 5. POST /vehicles/location : outside, then INSIDE the zone (entry alert), then outside again (exit alert)
function Send-Location($lat, $lon, $minAgo) {
  Invoke-RestMethod -Method Post -Uri "$base/vehicles/location" -Headers $h -ContentType "application/json" `
    -Body (@{ vehicle_id = $vehId; latitude = $lat; longitude = $lon; timestamp = (Ts $minAgo) } | ConvertTo-Json) |
    ConvertTo-Json -Depth 6
}
Send-Location 37.70   -122.50   5     # outside
Send-Location 37.7799 -122.4144 4     # inside -> entry
# 6. GET /vehicles/location/{vehicle_id}
Invoke-RestMethod "$base/vehicles/location/$vehId" -Headers $h | ConvertTo-Json -Depth 6
Send-Location 37.70   -122.50   3     # outside -> exit

# 9. GET /violations/history   (filters: vehicle_id, geofence_id, start_date, end_date, limit, offset)
Invoke-RestMethod "$base/violations/history?vehicle_id=$vehId&limit=100" -Headers $h | ConvertTo-Json -Depth 6
Invoke-RestMethod "$base/violations/history?geofence_id=$geoId&limit=1&offset=1" -Headers $h | ConvertTo-Json -Depth 6
$from = [uri]::EscapeDataString((Ts 60)); $to = [uri]::EscapeDataString((Ts -60))
Invoke-RestMethod "$base/violations/history?start_date=$from&end_date=$to" -Headers $h | ConvertTo-Json -Depth 6

# Extra routes
Invoke-RestMethod "$base/health"
Invoke-RestMethod "$base/vehicles/locations" -Headers $h | ConvertTo-Json -Depth 6
Invoke-RestMethod "$base/alerts/events?limit=10" -Headers $h | ConvertTo-Json -Depth 6
Invoke-RestMethod -Method Delete -Uri "$base/alerts/$alertId" -Headers $h
```

**Seeing error responses:** `Invoke-RestMethod` throws on 4xx. To print the JSON error body (Windows PowerShell 5.1):

```powershell
try { Invoke-RestMethod -Method Post -Uri "$base/geofences" -ContentType "application/json" -Body '{"name":"x","category":"nope","coordinates":[]}' }
catch { $_.Exception.Response.StatusCode.value__; (New-Object IO.StreamReader($_.Exception.Response.GetResponseStream())).ReadToEnd() }
```

**curl on Windows:** use `curl.exe` (plain `curl` in PowerShell is an alias of `Invoke-WebRequest`) and put JSON in a file
to avoid quote problems: `curl.exe -X POST http://localhost:8080/vehicles -H "Content-Type: application/json" --data-binary "@vehicle.json"`

---

## 3. Conventions

**Every JSON response includes `time_ns`**: handler execution time in nanoseconds as a string (`"time_ns": "1234567"`),
also on errors.

**Errors** use one shape:

```json
{ "error": "category must be one of: delivery_zone, restricted_zone, toll_zone, customer_area", "time_ns": "41230" }
```

| Status | When |
|---|---|
| `400` | Validation failed, malformed JSON, bad query parameter |
| `401` | `API_KEY` is set and the key is missing/wrong |
| `404` | Unknown vehicle / geofence / alert, or unknown route |
| `409` | Duplicate `vehicle_number` |
| `413` | Request body larger than 1 MiB |
| `429` | Location-update rate limit exceeded (`Retry-After` header set) |
| `500` | Unexpected server/database error |

Coordinates are always `[latitude, longitude]`. Timestamps are ISO 8601 (`2025-01-15T10:35:00Z`).
IDs are prefixed: `geo_…`, `veh_…`, `alert_…`, `viol_…`, `evt_…`.

### Auth

Only enforced when `API_KEY` is set. Send the key as `X-API-Key: <key>`, or `Authorization: Bearer <key>`, or (WebSocket only,
because browsers can't set headers) `ws://localhost:8080/ws/alerts?api_key=<key>`. `OPTIONS` preflights and `GET /health` never need it.
PowerShell: `-Headers @{ "X-API-Key" = "secret" }`.

---

## 4. Routes

| # | Method | Path | Purpose |
|---|---|---|---|
| 1 | POST | `/geofences` | Create a geofence |
| 2 | GET | `/geofences` | List geofences (filter by category) |
| 3 | POST | `/vehicles` | Register a vehicle |
| 4 | GET | `/vehicles` | List vehicles |
| 5 | POST | `/vehicles/location` | Update location, detect entry/exit, trigger alerts |
| 6 | GET | `/vehicles/location/{vehicle_id}` | Current location + geofences of one vehicle |
| 7 | POST | `/alerts/configure` | Create an alert rule |
| 8 | GET | `/alerts` | List alert rules |
| 9 | GET | `/violations/history` | Entry/exit event history |
| WS | GET | `/ws/alerts` | Real-time alert stream (WebSocket) |
| + | GET | `/health` | Liveness check |
| + | GET | `/vehicles/locations` | Latest location of every vehicle |
| + | DELETE | `/alerts/{alert_id}` | Delete an alert rule |
| + | GET | `/alerts/events` | Recently delivered alerts |

Rows marked `+` are extras beyond the required endpoints; the required ones follow the spec exactly.
Commands below assume the variables from section 2C (`$base`, `$h`, `$geoId`, `$vehId`).

### 1. `POST /geofences`

| Field | Type | Required | Notes |
|---|---|---|---|
| `name` | string | yes | Non-empty |
| `description` | string | no | |
| `coordinates` | `[[lat, lon], …]` | yes | ≥ 4 points, first == last (closed), lat ∈ [-90, 90], lon ∈ [-180, 180], max 10 000 points |
| `category` | string | yes | `delivery_zone` \| `restricted_zone` \| `toll_zone` \| `customer_area` |

```powershell
Invoke-RestMethod -Method Post -Uri "$base/geofences" -Headers $h -ContentType "application/json" -Body @'
{ "name": "Downtown Delivery Zone", "description": "Main delivery area for downtown customers", "category": "delivery_zone",
  "coordinates": [[37.7749,-122.4194],[37.7849,-122.4194],[37.7849,-122.4094],[37.7749,-122.4094],[37.7749,-122.4194]] }
'@
```

**Response `201`**: `{ "id": "geo_6d51fbb33110", "name": "Downtown Delivery Zone", "status": "active", "time_ns": "191405" }`

**Errors:** `400` (missing name, bad category, polygon not closed, fewer than 4 points, lat/lon out of range).

### 2. `GET /geofences`

| Query | Required | Notes |
|---|---|---|
| `category` | no | One of the four categories, otherwise `400` |
| `limit`, `offset` | no | Optional pagination (max `limit` 500); the response then also has `total_count` |

```powershell
Invoke-RestMethod "$base/geofences?category=delivery_zone" -Headers $h | ConvertTo-Json -Depth 6
```

**Response `200`**

```json
{
  "geofences": [
    {
      "id": "geo_6d51fbb33110",
      "name": "Downtown Delivery Zone",
      "description": "Main delivery area for downtown customers",
      "coordinates": [[37.7749,-122.4194],[37.7849,-122.4194],[37.7849,-122.4094],[37.7749,-122.4094],[37.7749,-122.4194]],
      "category": "delivery_zone",
      "created_at": "2025-01-15T10:30:00Z"
    }
  ],
  "time_ns": "987654"
}
```

### 3. `POST /vehicles`

All fields required and non-empty: `vehicle_number` (unique), `driver_name`, `vehicle_type` (e.g. `truck`, `car`, `van`, `motorcycle`), `phone`.

```powershell
Invoke-RestMethod -Method Post -Uri "$base/vehicles" -Headers $h -ContentType "application/json" -Body @'
{ "vehicle_number": "KA-01-AB-1234", "driver_name": "John Doe", "vehicle_type": "truck", "phone": "+1234567890" }
'@
```

**Response `201`**: `{ "id": "veh_889af1660bff", "vehicle_number": "KA-01-AB-1234", "status": "active", "time_ns": "126441" }`

**Errors:** `400` (missing field), `409` (vehicle number already registered).

### 4. `GET /vehicles`

Optional `limit` / `offset` (adds `total_count`).

```powershell
Invoke-RestMethod "$base/vehicles" -Headers $h | ConvertTo-Json -Depth 6
```

**Response `200`**

```json
{
  "vehicles": [
    { "id": "veh_889af1660bff", "vehicle_number": "KA-01-AB-1234", "driver_name": "John Doe", "vehicle_type": "truck",
      "phone": "+1234567890", "status": "active", "created_at": "2025-01-15T09:00:00Z" }
  ],
  "time_ns": "876543"
}
```

### 5. `POST /vehicles/location`

Stores the position, finds every geofence containing it, compares with the geofences the vehicle was in before to detect
**entry** and **exit**, records them in the history, and queues a real-time alert for each event that matches an alert rule.
Delivery is asynchronous and never blocks the response. Rate limited per client (`RATE_LIMIT_*`).

| Field | Type | Required | Notes |
|---|---|---|---|
| `vehicle_id` | string | yes | Must exist (`404` otherwise) |
| `latitude` | number | yes | -90 … 90 |
| `longitude` | number | yes | -180 … 180 |
| `timestamp` | string | yes | ISO 8601, e.g. `2025-01-15T10:35:00Z` |

```powershell
Invoke-RestMethod -Method Post -Uri "$base/vehicles/location" -Headers $h -ContentType "application/json" `
  -Body (@{ vehicle_id = $vehId; latitude = 37.7799; longitude = -122.4144; timestamp = (Ts 0) } | ConvertTo-Json)
```

**Response `200`**

```json
{
  "vehicle_id": "veh_889af1660bff",
  "location_updated": true,
  "current_geofences": [
    { "geofence_id": "geo_6d51fbb33110", "geofence_name": "Downtown Delivery Zone", "status": "inside" }
  ],
  "time_ns": "85397"
}
```

`current_geofences` is `[]` when the vehicle is outside every geofence.

Behaviour notes:
- The first location ever reported for a vehicle that is already inside a geofence counts as an **entry**.
- A report whose `timestamp` is **older** than the vehicle's latest one is stored but does **not** change its current state
  or create events (protects against out-of-order delivery); the response then shows the real current geofences.
- Updates for the same vehicle are serialised with a row lock in Postgres, so concurrent updates cannot corrupt the state.
- Every entry/exit appears in `/violations/history`; only those matching an alert rule are pushed over WebSocket.

**Errors:** `400` (missing/invalid fields, bad timestamp), `404` (unknown vehicle), `429` (rate limited).

### 6. `GET /vehicles/location/{vehicle_id}`

```powershell
Invoke-RestMethod "$base/vehicles/location/$vehId" -Headers $h | ConvertTo-Json -Depth 6
```

**Response `200`**

```json
{
  "vehicle_id": "veh_889af1660bff",
  "vehicle_number": "KA-01-AB-1234",
  "current_location": { "latitude": 37.7799, "longitude": -122.4144, "timestamp": "2025-01-15T10:35:00Z" },
  "current_geofences": [
    { "geofence_id": "geo_6d51fbb33110", "geofence_name": "Downtown Delivery Zone", "category": "delivery_zone" }
  ],
  "time_ns": "6005"
}
```

**Errors:** `404` if the vehicle doesn't exist, or exists but has never reported a location.

### 7. `POST /alerts/configure`

| Field | Type | Required | Notes |
|---|---|---|---|
| `geofence_id` | string | yes | Must exist (`404` otherwise) |
| `vehicle_id` | string | no | Omit to apply to **all** vehicles. Must exist if given |
| `event_type` | string | yes | `entry` \| `exit` \| `both` |

```powershell
Invoke-RestMethod -Method Post -Uri "$base/alerts/configure" -Headers $h -ContentType "application/json" `
  -Body (@{ geofence_id = $geoId; vehicle_id = $vehId; event_type = "entry" } | ConvertTo-Json)
```

**Response `201`**

```json
{ "alert_id": "alert_a68f8c4b0ffe", "geofence_id": "geo_6d51fbb33110", "vehicle_id": "veh_889af1660bff",
  "event_type": "entry", "status": "active", "time_ns": "1567890" }
```

`vehicle_id` is `null` for an all-vehicles rule. If several rules match one event, a single alert is sent.

**Errors:** `400` (missing `geofence_id`, bad `event_type`), `404` (unknown geofence or vehicle).

### 8. `GET /alerts`

| Query | Required | Notes |
|---|---|---|
| `geofence_id` | no | Filter by geofence |
| `vehicle_id` | no | Filter by vehicle |
| `limit`, `offset` | no | Optional pagination (adds `total_count`) |

```powershell
Invoke-RestMethod "$base/alerts?geofence_id=$geoId" -Headers $h | ConvertTo-Json -Depth 6
```

**Response `200`**

```json
{
  "alerts": [
    { "alert_id": "alert_a68f8c4b0ffe", "geofence_id": "geo_6d51fbb33110", "geofence_name": "Downtown Delivery Zone",
      "vehicle_id": "veh_889af1660bff", "vehicle_number": "KA-01-AB-1234", "event_type": "entry",
      "status": "active", "created_at": "2025-01-15T09:15:00Z" }
  ],
  "time_ns": "654321"
}
```

For all-vehicles rules `vehicle_id` and `vehicle_number` are `null`.

### 9. `GET /violations/history`

Entry/exit events, newest first.

| Query | Required | Notes |
|---|---|---|
| `vehicle_id` | no | |
| `geofence_id` | no | |
| `start_date` | no | ISO 8601, inclusive |
| `end_date` | no | ISO 8601, inclusive; must not be before `start_date` |
| `limit` | no | Default `50`, max `500` (larger values are clamped) |
| `offset` | no | Default `0`, for paging through `total_count` |

```powershell
Invoke-RestMethod "$base/violations/history?vehicle_id=$vehId&limit=100" -Headers $h | ConvertTo-Json -Depth 6
```

**Response `200`**

```json
{
  "violations": [
    { "id": "viol_4f5492354286", "vehicle_id": "veh_889af1660bff", "vehicle_number": "KA-01-AB-1234",
      "geofence_id": "geo_6d51fbb33110", "geofence_name": "Downtown Delivery Zone", "event_type": "entry",
      "latitude": 37.7799, "longitude": -122.4144, "timestamp": "2025-01-15T10:35:00Z" }
  ],
  "total_count": 245,
  "time_ns": "3456789"
}
```

`total_count` is the number of matches ignoring `limit`/`offset`. **Errors:** `400` (bad limit/offset/date).

### WebSocket `/ws/alerts`

The server pushes one JSON text message per alert. Multiple clients are supported, the server pings every 25 s, and slow
clients are dropped instead of blocking others. With an API key: `ws://localhost:8080/ws/alerts?api_key=secret`.
Try it in the browser console (section 2C) or in Postman (section 2B).

```json
{
  "event_id": "evt_af1f282da2da",
  "event_type": "entry",
  "timestamp": "2025-01-15T10:35:00Z",
  "vehicle": { "vehicle_id": "veh_889af1660bff", "vehicle_number": "KA-01-AB-1234", "driver_name": "John Doe" },
  "geofence": { "geofence_id": "geo_6d51fbb33110", "geofence_name": "Downtown Delivery Zone", "category": "delivery_zone" },
  "location": { "latitude": 37.7799, "longitude": -122.4144 }
}
```

`event_type` is `entry` or `exit`. A client that reconnects can catch up with `GET /alerts/events`.

### Extra routes

- **`GET /health`** → `{ "status": "ok", "time_ns": "…" }`
- **`GET /vehicles/locations`** → `{ "locations": [ { "vehicle_id", "vehicle_number", "driver_name", "current_location", "current_geofences" } ], "time_ns": "…" }` (only vehicles that have reported a location)
- **`DELETE /alerts/{alert_id}`** → `{ "alert_id": "…", "deleted": true, "time_ns": "…" }`, `404` if unknown
- **`GET /alerts/events?limit=50`** → `{ "events": [ <same shape as a WebSocket message>, … ], "time_ns": "…" }`, newest first (default 50, max 500)

---

## 5. Project layout

Everything below lives in the `backend` folder; the repository root only contains `backend/` and `frontend/`.

```
backend/
  cmd/server/main.go                entrypoint: wires config, database, WebSocket hub and API; graceful shutdown
  internal/config/                  environment settings (config.go) and DSN builder (dsn.go) (+ tests)
  internal/database/                PostgreSQL connection pool with retry, first-start database creation (+ tests)
  internal/model/                   domain types and enums
  internal/geo/                     polygon validation, bounding box, point-in-polygon (+ tests)
  internal/store/                   Store interface; postgres.go (production); memory.go (tests only);
                                    migrations/schema.sql (embedded into the binary)
  internal/ws/                      WebSocket hub: handshake, framing, ping/pong, broadcast (+ tests)
  internal/api/                     handlers, time_ns wrapper, middleware (CORS, auth, rate limit), async alert delivery (+ tests)
  docs/postman/                     Postman collection + environment
  scripts/smoke-test.ps1            PowerShell test of every route
  Dockerfile
  docker-compose.yml                API + PostgreSQL containers, data in the "pgdata" Docker volume (settings in .env)
  .env / .env.example               settings read by docker compose
  README.md                         this file
frontend/                           not built yet
```

## 6. Development without Docker (optional)

Needs Go 1.24+ and a reachable PostgreSQL.

```powershell
# run these inside the backend folder
go mod tidy                                   # one time: downloads the pgx driver and writes go.sum
$env:DB_HOST="localhost"; $env:DB_PORT="5432"; $env:DB_USER="geofence"; $env:DB_PASSWORD="<your password>"; $env:DB_NAME="geofence"
go run ./cmd/server
go test ./...                                 # unit tests (no database needed)
$env:TEST_DATABASE_URL="postgres://geofence:<your password>@localhost:5432/geofence_test?sslmode=disable"   # create geofence_test first
go test -tags integration ./internal/store/   # PostgreSQL contract + concurrency tests (WIPES that database)
```

## 7. Known limitations

- Geofences cannot be updated or deleted (not in the spec).
- Polygons are treated as planar in lat/lon space: fine for city-scale zones, not for polygons crossing the antimeridian or enclosing a pole.
- No authentication beyond the optional shared `API_KEY`; no per-user accounts.
- With several API instances, WebSocket broadcasts only reach clients connected to the instance that handled the update
  (a Redis/Postgres `LISTEN/NOTIFY` bus would fix that).
- Not yet exercised: `docker compose` itself (see the delivery notes), and `scripts/smoke-test.ps1` on a real Windows PowerShell.
