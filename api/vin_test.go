package main

import (
	"database/sql"
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	info, err := os.Stat("vpic.sqlite")
	if err != nil || info.Size() == 0 {
		t.Skip("vpic.sqlite not generated yet — run 'make convert' first")
	}
	tdb, err := sql.Open("sqlite", "vpic.sqlite?mode=ro&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { tdb.Close() })
	return tdb
}

// ---------------------------------------------------------------------------
// Invalid VIN format tests — no DB required
// ---------------------------------------------------------------------------

// invalidVINs are VINs that must be rejected with HTTP 400.
// Organised by failure category so regressions are easy to locate.
var invalidVINs = []struct {
	vin    string
	reason string
}{
	// contains I (forbidden in VIN)
	{"1HGCM826I3A004352", "contains I at pos 9"},
	{"IXXXXXXXXXXXXXXXX", "starts with I"},
	{"XXXXXXXXXXXXXXXXI", "ends with I"},
	{"1HGIM82633A00435I", "multiple I"},
	{"AIBCDEFGHJKLMNPRS", "I near start"},
	{"ABCDEFGHIJKLMNPRS", "I in middle"},
	{"ABCDEFGHJKLMNIPRS", "I further in"},
	{"ABCDEFGHJKLMNIPR5", "I late, digit at end"},
	{"1HGCM826I3A00435I", "two I chars"},
	{"IIIIIIIIIIIIIIIII", "all I"},

	// contains O (forbidden in VIN)
	{"1HGCM826O3A004352", "contains O at pos 9"},
	{"OXXXXXXXXXXXXXXXX", "starts with O"},
	{"XXXXXXXXXXXXXXXOX", "O near end"},
	{"ABCDEFGHJKLMNOPRS", "O in range N-P"},
	{"1HGCM82603O004352", "O at pos 11"},
	{"ABCOEFGHJKLMNPRS5", "O replacing C"},
	{"1HGOM82633A004352", "O in WMI"},
	{"1HGCM826O3A00435O", "two O chars"},
	{"OOOOOOOOOOOOOOOO5", "all O except last"},
	{"OOOOOOOOOOOOOOOOO", "all O"},

	// contains Q (forbidden in VIN)
	{"1HGCM826Q3A004352", "contains Q at pos 9"},
	{"QXXXXXXXXXXXXXXXX", "starts with Q"},
	{"1HGCM82633A00435Q", "Q at end"},
	{"ABCDEFGHJKLMNPQRS", "Q in range P-R"},
	{"1HGQM82633A004352", "Q in WMI"},
	{"ABCDEFGHJKLQMNPRS", "Q mid-VIN"},
	{"1HGCM82QQ3A004352", "two Q chars"},
	{"1QGCM82633A004352", "Q as second char"},
	{"QQQQQQQQQQQQQQQQQ", "all Q"},
	{"1HGCM826Q3A0043Q2", "Q appears twice"},

	// wrong length
	{"1HGCM82633A00435", "16 chars (too short by 1)"},
	{"1HGCM82633A0043", "15 chars"},
	{"1HGCM82633A004", "14 chars"},
	{"1HGCM82633", "10 chars"},
	{"1HG", "3 chars (WMI only)"},
	{"", "empty string"},
	{"1HGCM82633A0043521", "18 chars (too long by 1)"},
	{"1HGCM82633A00435210", "19 chars"},
	{"1HGCM82633A004352100", "20 chars"},
	{"11111111111111111111", "20 chars all digits"},

	// spaces and whitespace
	{"1HGCM82633A00435 ", "trailing space"},
	{" 1HGCM82633A004352", "leading space"},
	{"1HGCM826 3A004352", "internal space"},
	{"1HGCM82633A0 4352", "internal space mid"},
	{"1 HGCM82633A04352", "space after first char"},

	// special / non-alphanumeric characters
	{"1HGCM826-3A004352", "hyphen in VIN"},
	{"1HGCM826.3A004352", "period in VIN"},
	{"1HGCM826/3A004352", "slash in VIN"},
	{"1HGCM826#3A004352", "hash in VIN"},
	{"1HGCM826*3A004352", "asterisk in VIN"},
	{"1HGCM826!3A004352", "exclamation mark"},
	{"1HGCM826@3A004352", "at sign"},
	{"1HGCM826$3A004352", "dollar sign"},
	{"1HGCM82633A0043_2", "underscore"},
	{"1HGCM82633A00435\t", "tab character"},
}

// TestInvalidVINFormat tests the regex directly — no DB or HTTP layer needed.
// This catches malformed VINs before they ever hit the decode path.
func TestInvalidVINFormat(t *testing.T) {
	for _, tc := range invalidVINs {
		tc := tc
		t.Run(tc.reason, func(t *testing.T) {
			if vinRE.MatchString(tc.vin) {
				t.Errorf("vin %q (%s): expected regex rejection but it matched", tc.vin, tc.reason)
			}
		})
	}
}

// TestInvalidVINHTTP tests the HTTP handler returns 400 for VINs that are
// also safe to embed in a URL path (no raw spaces/tabs/slashes).
func TestInvalidVINHTTP(t *testing.T) {
	urlSafeInvalid := []struct {
		vin    string
		reason string
	}{
		{"1HGCM826I3A004352", "contains I"},
		{"1HGCM826O3A004352", "contains O"},
		{"1HGCM826Q3A004352", "contains Q"},
		{"1HGCM82633A00435", "16 chars"},
		{"1HGCM82633A0043521", "18 chars"},
		{"IIIIIIIIIIIIIIIII", "all I"},
		{"OOOOOOOOOOOOOOOOO", "all O"},
		{"QQQQQQQQQQQQQQQQQ", "all Q"},
	}
	for _, tc := range urlSafeInvalid {
		tc := tc
		t.Run(tc.reason, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/vin/"+tc.vin, nil)
			w := httptest.NewRecorder()
			handleVIN(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("vin %q (%s): want 400, got %d", tc.vin, tc.reason, w.Code)
			}
			var resp VINResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Error == "" {
				t.Errorf("vin %q: expected non-empty error field in response", tc.vin)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Known-VIN integration tests — require vpic.sqlite
// ---------------------------------------------------------------------------

// vinTestCase is a VIN with its expected VPIC field values.
type vinTestCase struct {
	vin       string
	makeSub   string // expected substring in the Make field (case-insensitive)
	modelYear string // exact expected ModelYear value
}

// yearChar maps a year character (VIN position 10) to the model year string.
// Only includes years from 2000 onward; for modern manufacturer WMIs the
// 1980s interpretation is irrelevant.
var yearChars = []struct {
	char string
	year string
}{
	{"Y", "2000"},
	{"1", "2001"},
	{"2", "2002"},
	{"3", "2003"},
	{"4", "2004"},
	{"5", "2005"},
	{"6", "2006"},
	{"7", "2007"},
	{"8", "2008"},
	{"9", "2009"},
	{"A", "2010"},
	{"B", "2011"},
	{"C", "2012"},
	{"D", "2013"},
	{"E", "2014"},
	{"F", "2015"},
	{"G", "2016"},
	{"H", "2017"},
	{"J", "2018"},
	{"K", "2019"},
	{"L", "2020"},
	{"M", "2021"},
	{"N", "2022"},
	{"P", "2023"},
	{"R", "2024"},
}

// knownVINSources are real manufacturer WMIs + a VDS prefix.
// Each entry produces 25 VINs (one per model year 2000-2024).
// The VDS value is representative; VPIC will decode at least Make + ModelYear
// for any syntactically valid VIN from a registered WMI.
var knownVINSources = []struct {
	wmi     string
	vds     string // 5 chars: no I, O, Q
	makeSub string // expected substring in Make (case-insensitive)
}{
	{"1HG", "CM826", "honda"},
	{"2HG", "FG128", "honda"},
	{"1FA", "6P8TH", "ford"},
	{"1FT", "FW1ET", "ford"},
	{"1FM", "5K8AT", "ford"},
	{"1G1", "YY22G", "chevrolet"},
	{"1GC", "4YREY", "chevrolet"},
	{"4T1", "BF3EK", "toyota"},
	{"2T1", "BURHE", "toyota"},
	{"JTD", "KARFU", "toyota"},
	{"WBA", "JA910", "bmw"},
	{"1N4", "AL3AP", "nissan"},
	{"JN1", "AZ4EH", "nissan"},
	{"5NP", "EH4CF", "hyundai"},
	{"KMH", "CM3AC", "hyundai"},
	{"5XY", "K3DB3", "hyundai"}, // 5XY is shared Hyundai/Kia; primary make_id in NHTSA DB is Hyundai
	{"KNA", "D5DH3", "kia"},
	{"WVW", "AU7LA", "volkswagen"},
	{"WP0", "AD2A6", "porsche"},
	{"5YJ", "SA1E2", "tesla"},
}

// buildKnownVINs generates the 500-entry test table from knownVINSources × yearChars.
func buildKnownVINs() []vinTestCase {
	cases := make([]vinTestCase, 0, len(knownVINSources)*len(yearChars))
	for _, src := range knownVINSources {
		for _, y := range yearChars {
			// Structure: WMI(3) + VDS(5) + check(1='0') + yearChar(1) + plant(1='A') + seq(6)
			vin := src.wmi + src.vds + "0" + y.char + "A" + "000001"
			cases = append(cases, vinTestCase{
				vin:       vin,
				makeSub:   src.makeSub,
				modelYear: y.year,
			})
		}
	}
	// Shuffle so test order is not always WMI-grouped × year-sequential.
	// This catches ordering-dependent cache bugs and makes failures easier to spot.
	rand.Shuffle(len(cases), func(i, j int) { cases[i], cases[j] = cases[j], cases[i] })
	return cases
}

func TestKnownVINDecoding(t *testing.T) {
	tdb := openTestDB(t)
	db = tdb // set package-level var used by decodeVIN

	cases := buildKnownVINs()
	if len(cases) != 500 {
		t.Fatalf("expected 500 test cases, got %d", len(cases))
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.vin, func(t *testing.T) {
			res, err := decodeVIN(tdb, tc.vin)
			if err != nil {
				t.Fatalf("decodeVIN: %v", err)
			}
			if res.Make == "" && len(res.Attributes) == 0 {
				t.Errorf("no results returned (WMI may not be in DB)")
				return
			}

			// ModelYear is deterministic from position 10 — always assert it.
			if res.ModelYear != tc.modelYear {
				t.Errorf("ModelYear: want %q, got %q", tc.modelYear, res.ModelYear)
			}

			// Make must contain the expected manufacturer substring.
			if !strings.Contains(strings.ToLower(res.Make), tc.makeSub) {
				t.Errorf("Make: want substring %q, got %q", tc.makeSub, res.Make)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Specific well-known VINs with fuller expected values
// ---------------------------------------------------------------------------

var specificVINTests = []struct {
	vin       string
	wantMake  string
	wantYear  string
}{
	{"1HGCM82633A004352", "HONDA", "2003"},
	{"1FTFW1ET5EKE52261", "FORD", "2014"},
	{"2T1BURHE0JC060752", "TOYOTA", "2018"},
}

func TestSpecificVINs(t *testing.T) {
	tdb := openTestDB(t)
	db = tdb

	for _, tc := range specificVINTests {
		tc := tc
		t.Run(tc.vin, func(t *testing.T) {
			res, err := decodeVIN(tdb, tc.vin)
			if err != nil {
				t.Fatalf("decodeVIN: %v", err)
			}
			if res.Make == "" {
				t.Fatal("Make is empty")
			}
			if !strings.EqualFold(res.Make, tc.wantMake) {
				t.Errorf("Make: want %q, got %q", tc.wantMake, res.Make)
			}
			if res.ModelYear != tc.wantYear {
				t.Errorf("ModelYear: want %q, got %q", tc.wantYear, res.ModelYear)
			}
		})
	}
}
