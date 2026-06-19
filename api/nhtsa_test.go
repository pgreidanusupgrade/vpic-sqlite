package main

// Cross-check tests: compare our decoder against the NHTSA VPIC REST API.
//
// Offline mode (default): loads nhtsaProbeVINs from testdata/nhtsa_golden.json
// and compares all decoded fields. Runs in CI without network access.
//
// Live mode: set NHTSA_LIVE_TEST=1 to call the real API. Use this when the
// VPIC database is refreshed to verify our output still matches upstream.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// nhtsaProbeVINs is the curated set of VINs checked by TestNHTSAGoldenComparison.
// All must be present in testdata/nhtsa_golden.json.
var nhtsaProbeVINs = []string{
	// original probes
	"1HGCM82633A004352", // 2003 Honda Accord
	"1FTFW1ET5EKE52261", // 2014 Ford F-150
	"2T1BURHE0JC060752", // 2018 Toyota Corolla
	// added probes
	"7SAYGAEE9NF432848", // 2022 Tesla Model Y (BEV)
	"1GC4YUEY5LF152163", // 2020 Chevrolet Silverado 3500 (diesel)
	"1GKS2GKCXLR292005", // 2020 GMC Yukon XL
}

type nhtsaAPIResponse struct {
	Results []struct {
		Variable string `json:"Variable"`
		Value    string `json:"Value"`
	} `json:"Results"`
}

func fetchNHTSAAllFields(vin string) (map[string]string, error) {
	url := "https://vpic.nhtsa.dot.gov/api/vehicles/DecodeVin/" + vin + "?format=json"
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	var body nhtsaAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}

	skip := map[string]bool{"": true, "Not Applicable": true, "0": true, "None": true, "N/A": true}
	fields := make(map[string]string)
	for _, item := range body.Results {
		if item.Variable == "" || skip[item.Value] {
			continue
		}
		fields[item.Variable] = item.Value
	}
	return fields, nil
}

// checkNHTSAFields applies the same hard/soft logic as TestGoldenVINs to a
// single VIN and its NHTSA golden data. Shared by offline and live tests.
func checkNHTSAFields(t *testing.T, vin string, want map[string]string) {
	t.Helper()
	res, err := decodeVIN(vin)
	if err != nil {
		t.Fatalf("decodeVIN: %v", err)
	}

	// Log the full decoded output so "pull full data into the test output" is satisfied.
	t.Logf("decoded: Make=%q ModelYear=%q VehicleType=%q Attributes=%v",
		res.Make, res.ModelYear, res.VehicleType, res.Attributes)

	// ── ModelYear ────────────────────────────────────────────────────────────
	if nhtsaYear := want["Model Year"]; nhtsaYear != "" {
		y, _ := strconv.Atoi(nhtsaYear)
		if y < 2010 {
			t.Logf("SKIP ModelYear pre-2010 (30-year cycle ambiguity): NHTSA=%q", nhtsaYear)
		} else if res.ModelYear != nhtsaYear {
			t.Errorf("ModelYear: NHTSA=%q got=%q", nhtsaYear, res.ModelYear)
		}
	}

	// ── Make ─────────────────────────────────────────────────────────────────
	nhtsaMake := strings.ToUpper(want["Make"])
	if nhtsaMake != "" {
		gotMake := strings.ToUpper(res.Make)
		if gotMake != nhtsaMake {
			found := false
			for _, m := range wmiMakeNames(vinWMI(vin)) {
				if m == nhtsaMake {
					found = true
					break
				}
			}
			if found {
				t.Logf("SKIP Make WMI-multi-brand: NHTSA=%q primary=%q", nhtsaMake, gotMake)
			} else if sameFamily(gotMake, nhtsaMake) {
				t.Logf("SKIP Make brand-family: NHTSA=%q primary=%q", nhtsaMake, gotMake)
			} else {
				t.Errorf("Make: NHTSA=%q got=%q", nhtsaMake, gotMake)
			}
		}
	}

	// ── VehicleType ───────────────────────────────────────────────────────────
	if nhtsaVT := want["Vehicle Type"]; nhtsaVT != "" && res.VehicleType != "" {
		if !strings.EqualFold(res.VehicleType, nhtsaVT) {
			t.Errorf("VehicleType: NHTSA=%q got=%q", nhtsaVT, res.VehicleType)
		}
	}

	// ── All other attributes (soft — log only) ────────────────────────────────
	norm := func(s string) string {
		return strings.Join(strings.Fields(strings.ToLower(s)), " ")
	}
	for varName, nhtsaVal := range want {
		switch varName {
		case "Make", "Model Year", "Vehicle Type",
			"Manufacturer", "Manufacturer Name", "Manufacturer Id",
			"Plant City", "Plant Country", "Plant State", "Plant Company Name",
			"Note", "Destination Market",
			"Error Code", "Error Text", "Additional Error Text",
			"Possible Values", "Suggested VIN",
			"Vehicle Descriptor", "Active Safety System Note":
			continue
		}
		gotVal, present := res.Attributes[varName]
		if !present {
			continue
		}
		if norm(gotVal) != norm(nhtsaVal) {
			t.Logf("INFO attr mismatch %q: NHTSA=%q got=%q", varName, nhtsaVal, gotVal)
		}
	}
}

// TestNHTSAGoldenComparison verifies our decoder matches stored NHTSA results
// for the probe VINs. Loads all fields from testdata/nhtsa_golden.json.
// Fully offline — no network required.
func TestNHTSAGoldenComparison(t *testing.T) {
	fixture := loadGoldenFixture(t)
	loadTestData(t)

	for _, vin := range nhtsaProbeVINs {
		vin := vin
		want, ok := fixture[vin]
		if !ok {
			t.Errorf("probe VIN %s missing from testdata/nhtsa_golden.json — run scripts/fetch_nhtsa_golden.py", vin)
			continue
		}
		t.Run(vin, func(t *testing.T) {
			checkNHTSAFields(t, vin, want)
		})
	}
}

// TestNHTSALiveComparison calls the real NHTSA API and compares all fields.
// Only runs when NHTSA_LIVE_TEST=1 — requires internet access.
//
//	NHTSA_LIVE_TEST=1 go test -v -run TestNHTSALiveComparison
func TestNHTSALiveComparison(t *testing.T) {
	if os.Getenv("NHTSA_LIVE_TEST") != "1" {
		t.Skip("set NHTSA_LIVE_TEST=1 to run live API comparison")
	}
	loadTestData(t)

	for _, vin := range nhtsaProbeVINs {
		vin := vin
		t.Run(vin, func(t *testing.T) {
			fields, err := fetchNHTSAAllFields(vin)
			if err != nil {
				t.Fatalf("NHTSA API: %v", err)
			}
			if fields["Make"] == "" {
				t.Fatalf("NHTSA returned empty Make for %s", vin)
			}
			checkNHTSAFields(t, vin, fields)
		})
	}
}
