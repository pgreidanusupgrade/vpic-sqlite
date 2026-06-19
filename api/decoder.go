package main

import (
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// decodeResult holds all decoded attributes for a VIN.
type decodeResult struct {
	WMI         string            `json:"wmi"`
	Make        string            `json:"make,omitempty"`
	MakeName    string            `json:"make_name,omitempty"`
	Manufacturer string           `json:"manufacturer,omitempty"`
	VehicleType string            `json:"vehicle_type,omitempty"`
	ModelYear   string            `json:"model_year,omitempty"`
	Attributes  map[string]string `json:"attributes"`
}

// patternRow is one row from the patterns table.
type patternRow struct {
	regex    string
	compiled *regexp.Regexp
	variable string
	value    string
}

// schemaCache caches compiled regex sets per (wmi, schema_id) to avoid
// recompiling on every request for the same VIN prefix.
var schemaCache sync.Map // key: "wmi:schema_id" → []patternRow

// vinWMI implements vpic.fVinWMI: first 3 chars, extended to 6 if pos 3 is '9'.
// Small-volume manufacturers use a 6-char WMI (VIN[0:3] + VIN[11:14]).
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

// vinKey builds the key string the NHTSA pattern matching uses:
// positions 4-8 (VDS) concatenated with "|" and positions 10-17 (VIS).
// Source: vpic.spvindecode_core: SUBSTRING(var_vin,4,5) || '|' || SUBSTRING(var_vin,10,8)
func vinKey(vin string) string {
	if len(vin) < 9 {
		return vin[3:]
	}
	return vin[3:8] + "|" + vin[9:]
}

// vinModelYear implements vpic.fVinModelYear2 without the vehicle-type disambiguation.
// Returns 0 if the year character at VIN[9] is invalid.
//
// The year character at position 10 (index 9) of the VIN encodes model year.
// Letters repeat every 30 years (A=1980/2010, B=1981/2011, ...). Digits 1-9
// encode 2001-2009 (first cycle) / 2031-2039 (second cycle).
// If the decoded year is more than 2 years in the future, subtract 30.
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

// decodeVIN decodes a VIN and returns structured results.
// Make/VehicleType come from the wmi table. ModelYear is computed from VIN[9].
// All other attributes come from pattern matching.
func decodeVIN(db *sql.DB, vin string) (*decodeResult, error) {
	vin = strings.ToUpper(vin)
	if len(vin) < 3 {
		return nil, fmt.Errorf("VIN too short")
	}

	wmi := vinWMI(vin)
	key := vinKey(vin)

	res := &decodeResult{
		WMI:        wmi,
		Attributes: map[string]string{},
	}

	// 1. Look up WMI-level attributes (Make, Manufacturer, VehicleType).
	var makeNames, mfrName, vehicleType *string
	var makeID, mfrID, vehicleTypeID *int
	row := db.QueryRow(`
		SELECT make_id, make_names, mfr_id, mfr_name, vehicle_type_id, vehicle_type
		FROM wmi WHERE wmi = ?
	`, wmi)
	if err := row.Scan(&makeID, &makeNames, &mfrID, &mfrName, &vehicleTypeID, &vehicleType); err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("wmi lookup: %w", err)
	}
	if makeNames != nil {
		parts := strings.SplitN(*makeNames, ",", 2)
		res.Make = parts[0]
		if len(parts) == 1 {
			res.MakeName = parts[0]
		} else {
			res.MakeName = *makeNames
		}
	}
	if mfrName != nil {
		res.Manufacturer = *mfrName
	}
	if vehicleType != nil {
		res.VehicleType = *vehicleType
	}

	// 2. ModelYear from VIN position 10.
	if y := vinModelYear(vin); y > 0 {
		res.ModelYear = strconv.Itoa(y)
	}

	// 3. Pattern-based attributes.
	type schemaKey struct {
		wmi      string
		schemaID int
	}

	dbRows, err := db.Query(`
		SELECT schema_id, regex, variable, value
		FROM patterns
		WHERE wmi = ?
		ORDER BY schema_id, pattern_id
	`, wmi)
	if err != nil {
		return nil, err
	}
	defer dbRows.Close()

	bySchema := map[int][]patternRow{}
	for dbRows.Next() {
		var schemaID int
		var r patternRow
		if err := dbRows.Scan(&schemaID, &r.regex, &r.variable, &r.value); err != nil {
			return nil, err
		}
		bySchema[schemaID] = append(bySchema[schemaID], r)
	}
	if err := dbRows.Err(); err != nil {
		return nil, err
	}

	for schemaID, pats := range bySchema {
		sk := schemaKey{wmi, schemaID}
		var compiled []patternRow
		if cached, ok := schemaCache.Load(sk); ok {
			compiled = cached.([]patternRow)
		} else {
			compiled = make([]patternRow, 0, len(pats))
			for _, p := range pats {
				re, err := regexp.Compile("(?i)" + p.regex)
				if err != nil {
					continue
				}
				p.compiled = re
				compiled = append(compiled, p)
			}
			schemaCache.Store(sk, compiled)
		}

		matched := false
		for _, p := range compiled {
			if p.compiled.MatchString(key) {
				res.Attributes[p.variable] = p.value
				matched = true
			}
		}
		if matched {
			break
		}
	}

	return res, nil
}
