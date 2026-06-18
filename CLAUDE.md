# vpic-sqlite — Claude Code Reference

## What this repo does

Converts the NHTSA VPIC PostgreSQL database into a flat SQLite file embedded
in a Go binary. The result is a single-container VIN decoder with no runtime
database dependency.

## Monthly update workflow

```bash
make convert   # db/Dockerfile downloads latest NHTSA release; converter writes api/vpic.sqlite
make build     # bakes api/vpic.sqlite into the Go binary via //go:embed
make run       # podman compose up → :8080
```

No manual download step. `db/Dockerfile` fetches directly from
`https://vpic.nhtsa.dot.gov/downloads/vPICList_full_YYYY_MM.plain.zip` (or lite if full is unavailable) at build time. URL is auto-computed from the build date.

Uses **podman**, not docker. All Makefile targets use `podman`/`podman compose`.

## Key technical facts — do not change these without re-verifying

### VIN key string format

Source: `vpic.spvindecode_core` stored procedure (inspectable via
`SELECT prosrc FROM pg_proc WHERE proname = 'spvindecode_core'`):

```sql
var_keys = SUBSTRING(var_vin, 4, 5)          -- positions 4-8 (VDS)
if length > 9:
    var_keys = var_keys || '|' || SUBSTRING(var_vin, 10, 8)  -- positions 10-17 (VIS start)
```

In Go (0-indexed): `vin[3:8] + "|" + vin[9:]`

This key string is what gets matched against the SQL-wildcard patterns in
`vpic.pattern.keys`. If NHTSA ever changes this formula, the converter
and `api/decoder.go:vinKey()` both need updating.

### Pattern-to-regex conversion

The DB function `vpic.sqlwild_to_regex(keys)` converts a SQL-wildcard key
string (e.g. `"BB___*"`) to a Go-compatible regex (e.g. `"^BB....*"`).
The converter stores the *already-converted regex* in SQLite, so the Go API
never needs to call this function at runtime.

The output is Go-regexp-compatible with `(?i)` prepended for case-insensitivity.

### Attribute value resolution

`vpic.felementattributevalue(elementId, attributeId)` resolves an attribute ID
to a human-readable string (e.g. attribute 29 on element 5 → `"HONDA"`).
The converter calls this at export time and stores the resolved string.
No resolution is needed at query time.

### SQLite schema

```sql
-- Manufacturer WMI codes (first 6 chars of VIN)
wmi(wmi TEXT PK, make_id INT, mfr_id INT, mfr_name TEXT)

-- One row per (wmi × pattern × element). Regex already converted.
patterns(wmi TEXT, pattern_id INT, schema_id INT, regex TEXT,
         element_id INT, attribute_id INT, value TEXT, variable TEXT,
         PRIMARY KEY (wmi, pattern_id, element_id))

INDEX idx_patterns_wmi ON patterns(wmi)
```

### Decode algorithm (api/decoder.go)

1. Extract WMI = first 6 chars of VIN
2. `SELECT * FROM patterns WHERE wmi = ?` (indexed — fast)
3. Group rows by `schema_id`
4. For each schema: compile regexes (cached in `sync.Map` per `(wmi, schema_id)`),
   test each against `vinKey(vin)`, collect matching element values
5. Stop at the first schema that produces at least one match

## Procedure integrity check

`converter/verify.go` runs automatically at the end of `make convert`.
It decodes three known VINs via two independent paths:
- **Path A**: `vpic.spVinDecode()` — the authoritative stored procedure
- **Path B**: the raw-table JOIN the converter uses for the SQLite export

If `Make` or `ModelYear` differ between paths, **the build aborts** with a
clear error message. Do not ship the sqlite file until this passes.

### When the integrity check fails

First, find out what actually changed in the stored procedure:

```sql
-- Inspect the stored procedure source
SELECT prosrc FROM pg_proc p
JOIN pg_namespace n ON n.oid = p.pronamespace
WHERE p.proname IN ('spvindecode_core', 'spvindecode', 'sqlwild_to_regex', 'felementattributevalue')
  AND n.nspname = 'vpic';
```

Common causes and fixes:

| Symptom | Likely cause | Fix |
|---|---|---|
| Make mismatch | `felementattributevalue` logic changed | Inspect the function; update the JOIN in `exportPatterns` |
| ModelYear mismatch | Year-element ID changed | Check `vpic.element` table for the ModelYear element |
| Key mismatch (no results) | `spvindecode_core` key formula changed | Update `vinKey()` in `api/decoder.go` and the raw-table query in `verify.go` |
| Regex mismatch | `sqlwild_to_regex` output format changed | Update `sqlWildToGoRegex()` in `verify.go` and the regex compilation in `api/decoder.go` |

### Confirming it is just data, not procedure changes

Before running `make convert` on a new monthly release, do a quick sanity check:

```bash
# Start the new Postgres image
podman run -d --name vpic-check -e POSTGRES_DB=vpic -e POSTGRES_USER=vpic \
  -e POSTGRES_PASSWORD=vpic -p 5432:5432 vpic-db
until podman exec vpic-check pg_isready -U vpic -d vpic; do sleep 1; done

# Diff the stored procedure source against the previous release
psql postgres://vpic:vpic@localhost:5432/vpic -c \
  "SELECT proname, md5(prosrc) FROM pg_proc p \
   JOIN pg_namespace n ON n.oid = p.pronamespace \
   WHERE n.nspname = 'vpic' ORDER BY proname;"
```

If all MD5 hashes match the previous release, the procedures are unchanged
and the new data can be loaded safely. If any hash changed, read that
function's source and determine whether the converter needs updating before
proceeding.

## Related files

| File | Purpose |
|---|---|
| `db/Dockerfile` | Downloads latest NHTSA VPIC lite zip, loads SQL into postgres at image build time |
| `converter/main.go` | Postgres → SQLite export pipeline |
| `converter/verify.go` | Procedure integrity check (runs at end of `make convert`) |
| `api/decoder.go` | SQLite query + regex matching at serve time |
| `api/db.go` | Extracts embedded sqlite bytes to tmpfile, opens read-only |
| `api/embed.go` | `//go:embed vpic.sqlite` declaration |
| `api/vin_test.go` | 50 invalid format tests + 500 known-VIN integration tests |
