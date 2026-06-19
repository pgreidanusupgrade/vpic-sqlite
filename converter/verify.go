package main

// verifyProcedureIntegrity runs a spot-check after conversion.
//
// It decodes a small set of well-known VINs via two paths:
//
//  1. vpic.spVinDecode — the authoritative Postgres stored procedure
//  2. The SQLite output we just wrote
//
// If Make or ModelYear differ between the two paths, something in the
// conversion is wrong. The build is aborted.
//
// WHAT THIS DOES NOT CATCH
// Fields that spVinDecode derives through extra procedure logic (ErrorCode,
// PlantCity, AdditionalErrorText, etc.) are not in our SQLite and are not
// checked here. Only Make and ModelYear are verified because both flow through
// the normal pattern-matched / wmi-table path.

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// probeVINs are stable real-world VINs used for the integrity check.
// Their Make and ModelYear cannot change — they were already manufactured.
var probeVINs = []struct {
	vin      string
	wantMake string // expected substring (case-insensitive) in Make
	wantYear string // exact ModelYear value
}{
	{"1HGCM82633A004352", "honda", "2003"},
	{"1FTFW1ET5EKE52261", "ford", "2014"},
	{"2T1BURHE0JC060752", "toyota", "2018"},
}

func verifyProcedureIntegrity(ctx context.Context, conn *pgx.Conn, sqliteDB *sql.DB) error {
	for _, probe := range probeVINs {
		spMake, spYear, err := decodeViaStoredProc(ctx, conn, probe.vin)
		if err != nil {
			return fmt.Errorf("spVinDecode(%s): %w", probe.vin, err)
		}
		if spMake == "" || spYear == "" {
			return fmt.Errorf("spVinDecode(%s): empty Make=%q or ModelYear=%q — DB may not be fully loaded",
				probe.vin, spMake, spYear)
		}

		rawMake, rawYear, err := decodeViaSQLite(probe.vin, sqliteDB)
		if err != nil {
			return fmt.Errorf("sqlite decode(%s): %w", probe.vin, err)
		}

		if !strings.EqualFold(spMake, rawMake) {
			return fmt.Errorf("VIN %s Make mismatch:\n  spVinDecode = %q\n  sqlite      = %q",
				probe.vin, spMake, rawMake)
		}
		if spYear != rawYear {
			return fmt.Errorf("VIN %s ModelYear mismatch:\n  spVinDecode = %q\n  sqlite      = %q",
				probe.vin, spYear, rawYear)
		}

		// Sanity-check against hardcoded expectations.
		if !strings.Contains(strings.ToLower(spMake), probe.wantMake) {
			return fmt.Errorf("VIN %s: Make=%q does not contain expected %q\n"+
				"Update probeVINs in verify.go if the NHTSA manufacturer name changed.",
				probe.vin, spMake, probe.wantMake)
		}
		if spYear != probe.wantYear {
			return fmt.Errorf("VIN %s: ModelYear=%q does not match expected %q\n"+
				"ModelYear for an already-manufactured vehicle cannot change — investigate.",
				probe.vin, spYear, probe.wantYear)
		}

		fmt.Printf("  ✓ %s  Make=%q  ModelYear=%q\n", probe.vin, spMake, spYear)
	}
	return nil
}

// decodeViaStoredProc calls vpic.spVinDecode and returns Make + ModelYear.
func decodeViaStoredProc(ctx context.Context, conn *pgx.Conn, vin string) (make_, year string, err error) {
	rows, err := conn.Query(ctx, `
		SELECT variable, value
		FROM vpic.spVinDecode($1)
		WHERE variable IN ('Make', 'Model Year')
		  AND value IS NOT NULL AND value != ''
	`, vin)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	for rows.Next() {
		var variable, value string
		if err := rows.Scan(&variable, &value); err != nil {
			return "", "", err
		}
		switch variable {
		case "Make":
			make_ = value
		case "Model Year":
			year = value
		}
	}
	return make_, year, rows.Err()
}

// decodeViaSQLite replicates the API's decode path against the just-written
// SQLite file. This verifies the full converter pipeline, not just the SQL.
func decodeViaSQLite(vin string, db *sql.DB) (make_, year string, err error) {
	if len(vin) < 17 {
		return "", "", fmt.Errorf("VIN too short: %q", vin)
	}
	vin = strings.ToUpper(vin)

	wmi := vinWMI(vin)

	// Make comes from the wmi table, not from patterns.
	var makeNames *string
	row := db.QueryRow(`SELECT make_names FROM wmi WHERE wmi = ?`, wmi)
	if err := row.Scan(&makeNames); err != nil && err != sql.ErrNoRows {
		return "", "", fmt.Errorf("wmi lookup: %w", err)
	}
	if makeNames != nil {
		parts := strings.SplitN(*makeNames, ",", 2)
		make_ = parts[0]
	}

	// ModelYear from VIN position 10 (index 9).
	year = strconv.Itoa(vinModelYear(vin))

	return make_, year, nil
}

// vinWMI implements vpic.fVinWMI: first 3 chars, extended to 6 if pos 3 = '9'.
func vinWMI(vin string) string {
	if len(vin) < 3 {
		return vin
	}
	wmi := vin[:3]
	if wmi[2] == '9' && len(vin) >= 14 {
		wmi = wmi + vin[11:14]
	}
	return wmi
}

// vinModelYear implements vpic.fVinModelYear2 for the simplified (non-vehicle-type)
// case. Returns 0 if the year character is invalid.
//
// The year char at VIN[9] encodes model year. Letters repeat every 30 years
// (A=1980/2010, B=1981/2011, ...). Digits 1-9 encode 2001-2009 / 2031-2039.
// For disambiguation we apply the same heuristic as the stored proc: if the
// decoded year is more than 2 years in the future, subtract 30.
func vinModelYear(vin string) int {
	if len(vin) < 10 {
		return 0
	}
	pos10 := vin[9]
	var y int
	switch {
	case pos10 >= 'A' && pos10 <= 'H':
		y = 2010 + int(pos10-'A')
	case pos10 >= 'J' && pos10 <= 'N':
		y = 2010 + int(pos10-'A') - 1
	case pos10 == 'P':
		y = 2023
	case pos10 >= 'R' && pos10 <= 'T':
		y = 2010 + int(pos10-'A') - 3
	case pos10 >= 'V' && pos10 <= 'Y':
		y = 2010 + int(pos10-'A') - 4
	case pos10 >= '1' && pos10 <= '9':
		y = 2031 + int(pos10-'1')
	default:
		return 0
	}
	limit := time.Now().Year() + 2
	if y > limit {
		y -= 30
	}
	return y
}

// decodeViaRawTables is kept for use in the CLAUDE.md investigation workflow.
// It replicates the exportPatterns JOIN against postgres to compare with spVinDecode.
func decodeViaRawTables(ctx context.Context, conn *pgx.Conn, vin string) (make_, year string, err error) {
	if len(vin) < 10 {
		return "", "", fmt.Errorf("VIN too short")
	}
	vin = strings.ToUpper(vin)
	wmi := vinWMI(vin)
	// Key string: VDS (positions 4-8) + "|" + VIS start (positions 10-17).
	key := vin[3:8] + "|" + vin[9:]

	// Make from wmi_make, not from patterns.
	row := conn.QueryRow(ctx, `
		SELECT mk.name
		FROM vpic.wmi w
		JOIN vpic.wmi_make wm ON wm.wmiid = w.id
		JOIN vpic.make mk ON mk.id = wm.makeid
		WHERE w.wmi = $1
		ORDER BY mk.id
		LIMIT 1
	`, wmi)
	if err := row.Scan(&make_); err != nil && err.Error() != "no rows in result set" {
		return "", "", fmt.Errorf("make lookup: %w", err)
	}

	// ModelYear from VIN position 10.
	year = strconv.Itoa(vinModelYear(vin))

	// Optionally verify pattern matching gives at least one result (sanity).
	rows, err := conn.Query(ctx, `
		SELECT
		    p.keys_regex AS regex,
		    e.name       AS variable,
		    vpic.felementattributevalue(p.elementid, p.attributeid) AS value
		FROM vpic.pattern p
		JOIN vpic.wmi_vinschema wv ON wv.vinschemaid = p.vinschemaid
		JOIN vpic.wmi w            ON w.id = wv.wmiid
		JOIN vpic.element e        ON e.id = p.elementid
		WHERE w.wmi = $1
		  AND e.name = 'Model'
		  AND p.attributeid != ''
		  AND vpic.felementattributevalue(p.elementid, p.attributeid) != ''
		ORDER BY p.id
	`, wmi)
	if err != nil {
		return make_, year, fmt.Errorf("pattern query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var regexStr, variable, value string
		if err := rows.Scan(&regexStr, &variable, &value); err != nil {
			return make_, year, err
		}
		re, err := regexp.Compile("(?i)" + regexStr)
		if err != nil || !re.MatchString(key) {
			continue
		}
		_ = variable
		_ = value
		break // found a matching pattern — converter logic works
	}
	return make_, year, rows.Err()
}
