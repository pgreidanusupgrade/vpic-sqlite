#!/usr/bin/env python3
"""
Fetch N unique real VINs from randomvin.com and print them to stdout.

Usage:
    python3 scripts/fetch_vins.py [count]   # default 1000
    python3 scripts/fetch_vins.py 500 > vins.txt
"""
import urllib.request
import concurrent.futures
import time
import sys

URL     = "https://randomvin.com/getvin.php?type=real"
HEADERS = {"User-Agent": "Mozilla/5.0"}
WORKERS = 10

total = int(sys.argv[1]) if len(sys.argv) > 1 else 1000


def fetch_one(_):
    try:
        req = urllib.request.Request(URL, headers=HEADERS)
        with urllib.request.urlopen(req, timeout=10) as r:
            vin = r.read().decode().strip()
            if len(vin) == 17 and vin.isalnum():
                return vin
    except Exception:
        pass
    return None


vins = set()
attempts = 0

with concurrent.futures.ThreadPoolExecutor(max_workers=WORKERS) as ex:
    while len(vins) < total:
        batch = min(total - len(vins) + 20, 50)
        results = list(ex.map(fetch_one, range(batch)))
        attempts += batch
        for v in results:
            if v:
                vins.add(v)
        sys.stderr.write(f"\r{len(vins)}/{total} ({attempts} attempts)   ")
        sys.stderr.flush()
        if len(vins) < total:
            time.sleep(0.1)

sys.stderr.write("\n")
for v in sorted(vins)[:total]:
    print(v)
