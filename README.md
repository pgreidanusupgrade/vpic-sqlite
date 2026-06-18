# vpic-sqlite

Single-container VIN decoder. The NHTSA VPIC database is converted to SQLite and
embedded directly in the Go binary — no database container at runtime.

## Monthly Update Workflow

No manual download needed. Just run:

```bash
make convert   # downloads latest NHTSA release, builds postgres, runs converter → api/vpic.sqlite
make build     # builds the API image with vpic.sqlite embedded
make run       # starts on :8080
```

Or all at once:
```bash
make all
```

Requires **podman** and **podman compose**.

## Endpoints

- `GET /vin/{VIN}` — decode a real 17-character VIN
- `GET /bench` — decode a random VIN (for cache-free load testing)
- `GET /health` — liveness probe

## Architecture

```
NHTSA vpic.nhtsa.dot.gov (downloaded at db image build time)
    ↓ (db/Dockerfile — postgres:16-alpine, data loaded into image)
postgres vpic DB
    ↓ (converter — Go binary connecting via TCP)
api/vpic.sqlite         ← flat table: (wmi, regex, variable, value)
    ↓ (//go:embed)
api binary              ← single static binary, extracts DB to tmpfs on start
    ↓
:8080
```

The converter materialises every (WMI × pattern × element) combination so the API
does only two operations per request:
1. SQLite SELECT by `wmi` (indexed)
2. Regex match of each returned pattern against the VIN key string in Go

No stored procedures, no Postgres at runtime.
