package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"
)

var (
	db    *sql.DB
	vinRE = regexp.MustCompile(`(?i)^[A-HJ-NPR-Z0-9]{17}$`)
)

const vinChars = "ABCDEFGHJKLMNPRSTUVWXYZ0123456789"

type VINResponse struct {
	VIN     string            `json:"vin"`
	Results map[string]string `json:"results,omitempty"`
	Error   string            `json:"error,omitempty"`
}

func randomVIN() string {
	b := make([]byte, 17)
	for i := range b {
		b[i] = vinChars[rand.IntN(len(vinChars))]
	}
	return string(b)
}

func handleVIN(w http.ResponseWriter, r *http.Request) {
	vin := strings.TrimPrefix(r.URL.Path, "/vin/")
	vin = strings.ToUpper(strings.TrimSpace(vin))
	w.Header().Set("Content-Type", "application/json")

	if !vinRE.MatchString(vin) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(VINResponse{VIN: vin, Error: "invalid VIN"})
		return
	}

	results, err := decodeVIN(db, vin)
	if err != nil {
		log.Printf("decodeVIN %s: %v", vin, err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(VINResponse{VIN: vin, Error: "query failed"})
		return
	}
	json.NewEncoder(w).Encode(VINResponse{VIN: vin, Results: results})
}

func handleBench(w http.ResponseWriter, r *http.Request) {
	vin := randomVIN()
	w.Header().Set("Content-Type", "application/json")
	results, err := decodeVIN(db, vin)
	if err != nil {
		log.Printf("bench decodeVIN %s: %v", vin, err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(VINResponse{VIN: vin, Error: "query failed"})
		return
	}
	json.NewEncoder(w).Encode(VINResponse{VIN: vin, Results: results})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func main() {
	var err error
	db, err = openEmbeddedDB()
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(1) // sqlite is single-writer; reads share fine with WAL

	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}

	http.HandleFunc("/vin/", handleVIN)
	http.HandleFunc("/bench", handleBench)
	http.HandleFunc("/health", handleHealth)

	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
