# testdata

## nhtsa_golden.json

Golden fixture: NHTSA VPIC single-VIN API results for 1000 real VINs sourced from randomvin.com.

**Schema:**
```json
{
  "VIN17CHARS": {
    "Make":       "UPPERCASE MAKE NAME",
    "Model Year": "YYYY",
    "Model":      "Accord",
    "Body Class": "Sedan/Saloon",
    "Drive Type": "FWD/Front-Wheel Drive",
    "...":        "all non-empty NHTSA Variable/Value pairs"
  }
}
```

Field names are the raw NHTSA `Variable` strings — they match the SQLite
`patterns.variable` column exactly, so no mapping layer is needed in tests.

**How it was generated:**
1. 1000 VINs fetched from `https://randomvin.com/getvin.php?type=real`
2. NHTSA single-VIN endpoint queried: `GET /api/vehicles/DecodeVin/{VIN}?format=json`
   - 8 concurrent workers, 0.15s per-worker delay (~50 req/s max)
   - All non-empty, non-"Not Applicable" Variable/Value pairs stored
3. Results saved here as the authoritative expected output

**Refresh procedure** (run after each NHTSA VPIC database release):
```bash
# Fetch fresh VINs
python3 scripts/fetch_vins.py 1000 > /tmp/vins.txt

# Re-query NHTSA (all fields) and overwrite fixture
python3 scripts/fetch_nhtsa_golden.py /tmp/vins.txt api/testdata/nhtsa_golden.json
```
