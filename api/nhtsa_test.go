package main

// Cross-check tests: compare our SQLite decoder against the NHTSA VPIC REST API.
//
// Offline mode (default): uses nhtsaGolden, a hardcoded snapshot of NHTSA
// results for the probe VINs. Runs in CI without network access.
//
// Live mode: set NHTSA_LIVE_TEST=1 to call the real API. Use this when the
// VPIC database is refreshed to verify our output still matches upstream.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// nhtsaGolden stores pre-fetched NHTSA DecodeVin results for probe VINs.
// Captured from https://vpic.nhtsa.dot.gov/api/vehicles/DecodeVin/{VIN}?format=json
// Update this map after each VPIC database refresh if values change.
var nhtsaGolden = map[string]struct {
	Make      string
	ModelYear string
}{
	"1HGCM82633A004352": {Make: "HONDA",  ModelYear: "2003"},
	"1FTFW1ET5EKE52261": {Make: "FORD",   ModelYear: "2014"},
	"2T1BURHE0JC060752": {Make: "TOYOTA", ModelYear: "2018"},
}

// nhtsaAPIResponse is the relevant subset of the NHTSA DecodeVin JSON envelope.
type nhtsaAPIResponse struct {
	Results []struct {
		Variable string `json:"Variable"`
		Value    string `json:"Value"`
	} `json:"Results"`
}

// fetchNHTSA calls the NHTSA VPIC REST API and extracts Make + ModelYear.
func fetchNHTSA(vin string) (make_, year string, err error) {
	url := "https://vpic.nhtsa.dot.gov/api/vehicles/DecodeVin/" + vin + "?format=json"
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	var body nhtsaAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", fmt.Errorf("decode json: %w", err)
	}

	for _, item := range body.Results {
		if item.Value == "" || item.Value == "Not Applicable" {
			continue
		}
		switch item.Variable {
		case "Make":
			make_ = strings.ToUpper(item.Value)
		case "Model Year":
			year = item.Value
		}
	}
	return make_, year, nil
}

// TestNHTSAGoldenComparison verifies our SQLite decoder matches stored NHTSA results.
// This is the offline version — no network required.
func TestNHTSAGoldenComparison(t *testing.T) {
	tdb := openTestDB(t)
	db = tdb

	for vin, golden := range nhtsaGolden {
		vin, golden := vin, golden
		t.Run(vin, func(t *testing.T) {
			res, err := decodeVIN(tdb, vin)
			if err != nil {
				t.Fatalf("decodeVIN: %v", err)
			}

			gotMake := strings.ToUpper(res.Make)
			if gotMake != golden.Make {
				t.Errorf("Make: NHTSA golden=%q, SQLite=%q", golden.Make, gotMake)
			}
			if res.ModelYear != golden.ModelYear {
				t.Errorf("ModelYear: NHTSA golden=%q, SQLite=%q", golden.ModelYear, res.ModelYear)
			}
		})
	}
}

// TestNHTSALiveComparison calls the real NHTSA API and compares results.
// Only runs when NHTSA_LIVE_TEST=1 is set — requires internet access.
//
// Run this after a VPIC database refresh to verify our converter is still correct:
//
//	NHTSA_LIVE_TEST=1 go test -v -run TestNHTSALiveComparison
func TestNHTSALiveComparison(t *testing.T) {
	if os.Getenv("NHTSA_LIVE_TEST") != "1" {
		t.Skip("set NHTSA_LIVE_TEST=1 to run live API comparison")
	}
	tdb := openTestDB(t)
	db = tdb

	vins := []string{
		"1HGCM82633A004352",
		"1FTFW1ET5EKE52261",
		"2T1BURHE0JC060752",
		// Additional VINs for broader live coverage.
		"WBA3A5G59DNP26082", // BMW 3-series
		"JTEBU5JR5G5375843", // Toyota 4Runner
		"5NPE24AF8FH213670", // Hyundai Sonata
	}

	for _, vin := range vins {
		vin := vin
		t.Run(vin, func(t *testing.T) {
			nhtsa_make, nhtsa_year, err := fetchNHTSA(vin)
			if err != nil {
				t.Fatalf("NHTSA API: %v", err)
			}
			if nhtsa_make == "" {
				t.Fatalf("NHTSA returned empty Make for %s — VIN may not be in NHTSA DB", vin)
			}

			res, err := decodeVIN(tdb, vin)
			if err != nil {
				t.Fatalf("decodeVIN: %v", err)
			}

			gotMake := strings.ToUpper(res.Make)
			if gotMake != nhtsa_make {
				t.Errorf("Make mismatch: NHTSA=%q SQLite=%q", nhtsa_make, gotMake)
			}
			if nhtsa_year != "" && res.ModelYear != nhtsa_year {
				t.Errorf("ModelYear mismatch: NHTSA=%q SQLite=%q", nhtsa_year, res.ModelYear)
			}

			t.Logf("✓ NHTSA Make=%q Year=%q | SQLite Make=%q Year=%q", nhtsa_make, nhtsa_year, res.Make, res.ModelYear)
		})
	}
}
