#!/usr/bin/env python3
"""
Query the NHTSA VPIC single-VIN decode endpoint for a list of VINs and write
a JSON golden fixture used by the Go test suite to verify SQLite decoder output.

Uses the single-VIN endpoint (not batch) so Variable names match the SQLite
patterns table's `variable` column exactly — no camelCase mapping needed.

Rate-limited: MAX_WORKERS concurrent requests, MIN_DELAY_SECS between each
individual request, so we stay well under NHTSA's limits.

Usage:
    python3 scripts/fetch_nhtsa_golden.py vins.txt api/testdata/nhtsa_golden.json

Output schema per VIN:
    {
      "VIN": {
        "Make":        "HONDA",
        "Model Year":  "2003",
        "Model":       "Accord",
        "Body Class":  "Sedan/Saloon",
        ...all non-empty NHTSA variables...
      }
    }
"""
import json
import sys
import time
import threading
import urllib.request
import urllib.error
from collections import OrderedDict

BASE_URL     = "https://vpic.nhtsa.dot.gov/api/vehicles/DecodeVin/{}?format=json"
MAX_WORKERS  = 8       # concurrent requests
MIN_DELAY    = 0.15    # seconds between each request per worker (≈ 50 req/s max)
SKIP_VALUES  = {"", "Not Applicable", "0", "None", "N/A"}

if len(sys.argv) != 3:
    print(f"Usage: {sys.argv[0]} <vin-file> <output-json>", file=sys.stderr)
    sys.exit(1)

vin_file, out_file = sys.argv[1], sys.argv[2]

with open(vin_file) as f:
    vins = [l.strip() for l in f if l.strip()]

total = len(vins)
print(f"Loaded {total} VINs from {vin_file}", file=sys.stderr)

results = {}
errors  = []
lock    = threading.Lock()
counter = [0]

def fetch_vin(vin):
    url = BASE_URL.format(vin)
    req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
    for attempt in range(3):
        try:
            with urllib.request.urlopen(req, timeout=20) as r:
                body = json.load(r)
            break
        except urllib.error.HTTPError as e:
            if e.code == 429:
                wait = 10 * (attempt + 1)
                print(f"\n  rate-limited on {vin}, waiting {wait}s", file=sys.stderr)
                time.sleep(wait)
            else:
                raise
        except Exception:
            if attempt == 2:
                raise
            time.sleep(2)

    fields = {}
    for item in body.get("Results", []):
        var = (item.get("Variable") or "").strip()
        val = (item.get("Value")    or "").strip()
        if var and val and val not in SKIP_VALUES:
            fields[var] = val
    return fields

def worker(chunk):
    for vin in chunk:
        try:
            fields = fetch_vin(vin)
            with lock:
                results[vin] = fields
                counter[0] += 1
                n = counter[0]
            if n % 50 == 0 or n == total:
                sys.stderr.write(f"\r  {n}/{total} VINs decoded   ")
                sys.stderr.flush()
        except Exception as e:
            with lock:
                errors.append(vin)
                counter[0] += 1
                sys.stderr.write(f"\n  ERROR {vin}: {e}\n")
                sys.stderr.flush()
        time.sleep(MIN_DELAY)

# Split VINs across workers
chunks = [vins[i::MAX_WORKERS] for i in range(MAX_WORKERS)]
threads = [threading.Thread(target=worker, args=(c,)) for c in chunks]
for t in threads:
    t.start()
for t in threads:
    t.join()

sys.stderr.write(f"\nDone. {len(results)} decoded, {len(errors)} errors.\n")
if errors:
    sys.stderr.write(f"Failed VINs: {errors}\n")

# Sort by VIN for stable diffs
ordered = OrderedDict(sorted(results.items()))

with open(out_file, "w") as f:
    json.dump(ordered, f, indent=2, sort_keys=True)

print(f"Written: {out_file}", file=sys.stderr)
