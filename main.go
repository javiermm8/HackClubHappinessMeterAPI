package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type HappinessEntry struct {
	EntryID        int
	Name           string
	SlackID        string
	HappinessLevel int
	Timestamp      time.Time
}

type POSTRequest struct {
	Name           string
	SlackID        string
	HappinessLevel int
}

func main() {

	// Open SQLite database(If there isn't one, it creates it.)
	db, err := sql.Open("sqlite3", "./data.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	createTable(db)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": "All good!",
		})
	})

	mux.HandleFunc("GET /happinessNeighbour", func(w http.ResponseWriter, r *http.Request) {
		rawHappinessLevel := r.URL.Query().Get("happinessLevel")
		happinessLevel, err := strconv.Atoi(rawHappinessLevel)
		if err != nil {
			log.Printf("Invalid happiness level: %v", err)

			http.Error(w, `{"error": "Invalid happiness level"}`, http.StatusBadRequest)
			return
		}

		HappinessNeighbourEntry, err := getHappinessNeighbour(db, happinessLevel)

		if HappinessNeighbourEntry != nil {
			message := "Your happiness neighbour is " + HappinessNeighbourEntry.Name + "! " + "Their slack id is: " + HappinessNeighbourEntry.SlackID + " and the last time they logged a happiness level of " + strconv.Itoa(HappinessNeighbourEntry.HappinessLevel) + " was at: " + HappinessNeighbourEntry.Timestamp.String()

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"message": message,
			})
		}

	})

	mux.HandleFunc("POST /newEntry", func(w http.ResponseWriter, r *http.Request) {
		var entry POSTRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&entry); err != nil {
			http.Error(w, `{"error": "invalid JSON or unknown fields provided"}`, http.StatusBadRequest)
			return
		}

		newEntry(db, entry.Name, entry.SlackID, entry.HappinessLevel, time.Now())

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Your happiness level has been logged!",
		})
	})

	fmt.Println("Server running on http://localhost:8080")
	http.ListenAndServe(":8080", mux)
}

func createTable(db *sql.DB) {

	// SQL sintaxt to create the table(if it doesn't already exist) with it's necessary colums.
	SQLcreateTable := `
	CREATE TABLE IF NOT EXISTS data (
	entryID INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT,
	slackID TEXT,
	happinessLevel INTEGER NOT NULL,
	timestamp DATETIME NOT NULL
	);
	`
	// Error handleing in case db.Exec(SQLcreateTable) fails.
	_, err := db.Exec(SQLcreateTable)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("I don't know if there was a table, but there is one now.")

}

func newEntry(
	db *sql.DB,
	Name string,
	SlackID string,
	HappinessLevel int,
	Timestamp time.Time,
) {
	SQLnewEntry := `
	INSERT INTO data
	(name, slackID, happinessLevel, timestamp)
	VALUES (?, ?, ?, ?)
	`
	output, err := db.Exec(
		SQLnewEntry,
		Name,
		SlackID,
		HappinessLevel,
		Timestamp,
	)
	if err != nil {
		log.Fatal(nil)
	}

	id, err := output.LastInsertId()
	if err != nil {
		log.Fatal(nil)
	}

	fmt.Println("Entry added! Entry ID:", id)
}

func getHappinessNeighbour(db *sql.DB, happinessLevel int) (*HappinessEntry, error) {
	row, err := db.Query(`
		SELECT
			entryID,
			name,
			slackID,
			happinessLevel,
			timestamp
		FROM data
		WHERE happinessLevel = ?
		ORDER BY timestamp DESC
		LIMIT 1;
	`, happinessLevel)
	if err != nil {
		log.Fatal(err)
	}
	defer row.Close()

	var entry HappinessEntry

	for row.Next() {
		err1 := row.Scan(
			&entry.EntryID,
			&entry.Name,
			&entry.SlackID,
			&entry.HappinessLevel,
			&entry.Timestamp,
		)
		if err1 != nil {
			if err1 == sql.ErrNoRows {
				return nil, nil
			}
			return nil, err1
		}
	}

	return &entry, nil
}
