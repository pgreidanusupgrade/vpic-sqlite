package main

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

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

// vinKey builds the key string the NHTSA pattern matching uses:
// positions 4-8 (VDS) concatenated with "|" and positions 10-17 (VIS).
func vinKey(vin string) string {
	vin = strings.ToUpper(vin)
	if len(vin) < 9 {
		return vin[3:]
	}
	return vin[3:8] + "|" + vin[9:]
}

// decodeVIN looks up patterns for the VIN's WMI (first 6 chars) and returns
// a map of variable→value for every element whose pattern matches.
func decodeVIN(db *sql.DB, vin string) (map[string]string, error) {
	if len(vin) < 6 {
		return nil, fmt.Errorf("VIN too short")
	}
	wmi := strings.ToUpper(vin[:6])
	key := vinKey(vin)

	// 1. Fetch all patterns for this WMI grouped by schema_id.
	type schemaPatterns struct {
		schemaID int
		rows     []patternRow
	}

	rows, err := db.Query(`
		SELECT schema_id, regex, variable, value
		FROM patterns
		WHERE wmi = ?
		ORDER BY schema_id, pattern_id
	`, wmi)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Group rows by schema_id; compile regexes once per (wmi,schema_id).
	type schemaKey struct {
		wmi      string
		schemaID int
	}
	bySchema := map[int][]patternRow{}
	for rows.Next() {
		var schemaID int
		var r patternRow
		if err := rows.Scan(&schemaID, &r.regex, &r.variable, &r.value); err != nil {
			return nil, err
		}
		bySchema[schemaID] = append(bySchema[schemaID], r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// For each schema: try to match. Use the first schema whose patterns all compile
	// and at least one matches the key string.
	out := map[string]string{}
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
				out[p.variable] = p.value
				matched = true
			}
		}
		if matched {
			break
		}
	}

	return out, nil
}
