// Converts the NHTSA VPIC PostgreSQL database into a flat SQLite file.
//
// Strategy: enumerate every (wmi, pattern, vinschema) combination and for each
// element in that schema emit one row: (wmi, regex, element_id, attribute_id, value).
// At query time the API just filters by wmi, tests each regex against the VIN key
// string, and returns the matching rows — no stored procedures needed.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5"
	_ "modernc.org/sqlite"
)

func main() {
	pgURL := os.Getenv("DATABASE_URL")
	if pgURL == "" {
		pgURL = "postgres://vpic:vpic@localhost:5432/vpic?sslmode=disable"
	}
	outPath := os.Getenv("OUTPUT_PATH")
	if outPath == "" {
		outPath = "vpic.sqlite"
	}

	ctx := context.Background()

	log.Println("connecting to postgres...")
	conn, err := pgx.Connect(ctx, pgURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	log.Printf("opening sqlite at %s", outPath)
	db, err := sql.Open("sqlite", outPath)
	if err != nil {
		log.Fatalf("sqlite open: %v", err)
	}
	defer db.Close()

	if err := createSchema(db); err != nil {
		log.Fatalf("create schema: %v", err)
	}

	log.Println("exporting wmi lookup table...")
	if err := exportWMI(ctx, conn, db); err != nil {
		log.Fatalf("export wmi: %v", err)
	}

	log.Println("exporting pattern rules...")
	if err := exportPatterns(ctx, conn, db); err != nil {
		log.Fatalf("export patterns: %v", err)
	}

	log.Println("creating indexes...")
	if err := createIndexes(db); err != nil {
		log.Fatalf("indexes: %v", err)
	}

	log.Println("done.")
}

func createSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS wmi (
			wmi        TEXT NOT NULL,
			make_id    INTEGER,
			mfr_id     INTEGER,
			mfr_name   TEXT,
			PRIMARY KEY (wmi)
		);

		CREATE TABLE IF NOT EXISTS patterns (
			wmi          TEXT NOT NULL,
			pattern_id   INTEGER NOT NULL,
			schema_id    INTEGER NOT NULL,
			regex        TEXT NOT NULL,
			element_id   INTEGER NOT NULL,
			attribute_id INTEGER,
			value        TEXT NOT NULL,
			variable     TEXT NOT NULL,
			PRIMARY KEY (wmi, pattern_id, element_id)
		);
	`)
	return err
}

func exportWMI(ctx context.Context, conn *pgx.Conn, db *sql.DB) error {
	rows, err := conn.Query(ctx, `
		SELECT w.wmi,
		       w.vehicletypeid,
		       w.makeId,
		       m.mfrname
		FROM vpic.wmi w
		LEFT JOIN vpic.manufacturer m ON m.id = w.mfrid
		WHERE w.publicavailabilitydate <= NOW()
		   OR w.includenotpubliclyavailable = true
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO wmi(wmi, make_id, mfr_id, mfr_name) VALUES(?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for rows.Next() {
		var wmi string
		var vtID, makeID *int
		var mfrName *string
		if err := rows.Scan(&wmi, &vtID, &makeID, &mfrName); err != nil {
			return err
		}
		var makeIDVal interface{}
		if makeID != nil {
			makeIDVal = *makeID
		}
		var mfrNameVal interface{}
		if mfrName != nil {
			mfrNameVal = *mfrName
		}
		if _, err := stmt.Exec(wmi, makeIDVal, nil, mfrNameVal); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func exportPatterns(ctx context.Context, conn *pgx.Conn, db *sql.DB) error {
	// Pull every pattern with its schema, wmi, converted regex, and all element values.
	// vpic.sqlwild_to_regex converts the SQL-wildcard key string to a Go-compatible regex.
	// vpic.felementattributevalue resolves attribute IDs to human-readable strings.
	rows, err := conn.Query(ctx, `
		SELECT
		    w.wmi,
		    p.id              AS pattern_id,
		    p.vinschemaId     AS schema_id,
		    vpic.sqlwild_to_regex(p.keys) AS regex,
		    ev.elementId,
		    ev.attributeId,
		    COALESCE(
		        vpic.felementattributevalue(ev.elementId, ev.attributeId),
		        ev.textvalue,
		        ''
		    )                 AS value,
		    e.name            AS variable
		FROM vpic.pattern p
		JOIN vpic.wmi w         ON w.id = p.wmiId
		JOIN vpic.elementvalue ev ON ev.vinschemaId = p.vinschemaId
		JOIN vpic.element e      ON e.id = ev.elementId
		WHERE (w.publicavailabilitydate <= NOW() OR w.includenotpubliclyavailable = true)
		  AND COALESCE(
		        vpic.felementattributevalue(ev.elementId, ev.attributeId),
		        ev.textvalue,
		        ''
		      ) != ''
	`)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO patterns(wmi, pattern_id, schema_id, regex, element_id, attribute_id, value, variable)
		VALUES(?,?,?,?,?,?,?,?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	n := 0
	for rows.Next() {
		var wmi, regex, value, variable string
		var patternID, schemaID, elementID int
		var attributeID *int
		if err := rows.Scan(&wmi, &patternID, &schemaID, &regex, &elementID, &attributeID, &value, &variable); err != nil {
			return err
		}
		var attrVal interface{}
		if attributeID != nil {
			attrVal = *attributeID
		}
		if _, err := stmt.Exec(wmi, patternID, schemaID, regex, elementID, attrVal, value, variable); err != nil {
			return fmt.Errorf("insert row %d: %w", n, err)
		}
		n++
		if n%100000 == 0 {
			log.Printf("  %d rows...", n)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	log.Printf("  total: %d rows", n)
	return tx.Commit()
}

func createIndexes(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_patterns_wmi ON patterns(wmi);
	`)
	return err
}
