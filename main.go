package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
	"github.com/slack-go/slack"

	_ "github.com/mattn/go-sqlite3"
)

type HappinessEntry struct {
	EntryID        int
	Name           string
	SlackID        string
	HappinessLevel int
	Timestamp      time.Time
}

type Auth struct {
	EntryID   int
	APIKey    string
	SlackID   string
	Timestamp time.Time
}

type POSTNewEntry struct {
	APIKey         string
	SlackID        string
	HappinessLevel int
}

type POSTNewUser struct {
	ManagmentKey string
	SlackID      string
}

func main() {

	// Enviroment variables
	err := godotenv.Load()
	if err != nil {
		log.Fatal(err)
	}

	// Open SQLite database(If there isn't one, it creates it.)
	db, err := sql.Open("sqlite3", "./data.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	createTable(db)

	// Slack bot
	slackApi := slack.New(os.Getenv("BOT_TOKEN"))

	fmt.Println(os.Getenv("BOT_TOKEN"), os.Getenv("REVIEW_KEY"), os.Getenv("MANAGEMENT_KEY"))

	mux := http.NewServeMux()

	/// GET STATUS

	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": "All good!",
		})
	})

	/// GET HAPPINESS NEIGHBOUR

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

	/// POST NEW ENTRY

	mux.HandleFunc("POST /newEntry", func(w http.ResponseWriter, r *http.Request) {
		var entry POSTNewEntry
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&entry); err != nil {
			http.Error(w, `{"error": "invalid JSON or unknown fields provided"}`, http.StatusBadRequest)
			return
		} else if entry.APIKey == "" ||
			entry.HappinessLevel <= 0 ||
			entry.HappinessLevel > 10 {
			http.Error(w, `{"error": "missing parameters or invalid values"}`, http.StatusBadRequest)
			return
		}

		realID := getDBSlackID(db, entry.APIKey)

		if entry.APIKey == os.Getenv("REVIEW_KEY") {
			if entry.SlackID == "" {
				newEntry(db, "anonymousReviewer", entry.SlackID, entry.HappinessLevel, time.Now())
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{
					"message": "Your happiness level has been logged anonymously(since you didn't include a SlackID, this is only allowed for reviewers)!",
				})
			} else {
				userInfo, err := slackApi.GetUserInfo(entry.SlackID)
				if err != nil {
					http.Error(w, `{"error": "Unable to get slack username from id. Try without SlackID."}`, http.StatusInternalServerError)
					return
				}

				name := userInfo.Profile.DisplayName
				if name == "" {
					name = userInfo.RealName
				}
				if name == "" {
					name = userInfo.Name
				}

				newEntry(db, name, entry.SlackID, entry.HappinessLevel, time.Now())
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{
					"message": name + ", your happiness level has been logged!",
				})
			}
		} else if entry.SlackID == realID {
			userInfo, err := slackApi.GetUserInfo(entry.SlackID)
			if err != nil {
				http.Error(w, `{"error": "Unable to get slack username from id, please DM me about it."}`, http.StatusInternalServerError)
				return
			}

			name := userInfo.Profile.DisplayName
			if name == "" {
				name = userInfo.RealName
			}
			if name == "" {
				name = userInfo.Name
			}

			newEntry(db, name, entry.SlackID, entry.HappinessLevel, time.Now())
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"message": name + ", your happiness level has been logged!",
			})
		} else {
			http.Error(w, `{"error": "The API key you provided doesn't match the provided SlackID. ¿Did you register? DM me to do so."}`, http.StatusBadRequest)
			return
		}
	})

	/// POST NEW USER

	mux.HandleFunc("POST /newUser", func(w http.ResponseWriter, r *http.Request) {
		var user POSTNewUser
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&user); err != nil {
			http.Error(w, `{"error": "invalid JSON or unknown fields provided"}`, http.StatusBadRequest)
			return
		}

		if user.ManagmentKey == os.Getenv("MANAGEMENT_KEY") {
			// Generate new API Key(the one I have to send to the user)
			bytes := make([]byte, 32)
			if _, err := rand.Read(bytes); err != nil {
				log.Fatal("failed to generate API key: %w", err)
			}
			generatedAPIKey := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(bytes)

			// Create the db entry
			newUser(db, generatedAPIKey, user.SlackID, time.Now())

			userInfo, _ := slackApi.GetUserInfo(user.SlackID)
			name := userInfo.Profile.DisplayName
			if name == "" {
				name = userInfo.RealName
			}
			if name == "" {
				name = userInfo.Name
			}

			// Send info back
			message := "User resgistered! API Key: " + generatedAPIKey + " " + "SlackID: " + user.SlackID + "Slack Name: " + name
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"message": message,
			})
		} else {
			http.Error(w, `{"error": "invalid management key"}`, http.StatusBadRequest)
			return
		}

	})

	fmt.Println("Server running on http://localhost:8080")
	http.ListenAndServe(":8080", mux)
}

func createTable(db *sql.DB) {

	// SQL sintaxt to create the table(if it doesn't already exist) with it's necessary colums.
	SQLcreateTableData := `
	CREATE TABLE IF NOT EXISTS data (
	entryID INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT,
	slackID TEXT,
	happinessLevel INTEGER NOT NULL,
	timestamp DATETIME NOT NULL
	);
	`
	SQLcreateTableAuth := `
	CREATE TABLE IF NOT EXISTS auth (
	entryID INTEGER PRIMARY KEY AUTOINCREMENT,
	APIKey TEXT NOT NULL,
	slackID TEXT NOT NULL,
	timestamp DATETIME NOT NULL
	);
	`

	// Error handleing in case db.Exec(SQLcreateTable) fails.
	_, err := db.Exec(SQLcreateTableData)
	if err != nil {
		log.Fatal(err)
	}

	_, err = db.Exec(SQLcreateTableAuth)
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

func newUser(db *sql.DB, APIKey string, SlackID string, Timestamp time.Time) {
	SQLNewUser := `
	INSERT INTO auth
	(APIKey, slackID, timestamp)
	VALUES (?, ?, ?)
	`
	result, err := db.Exec(
		SQLNewUser,
		APIKey,
		SlackID,
		Timestamp,
	)
	if err != nil {
		log.Fatal(nil)
	}

	id, err := result.LastInsertId()
	if err != nil {
		log.Fatal(nil)
	}

	fmt.Println("User added! User ID:", id)
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
		err = row.Scan(
			&entry.EntryID,
			&entry.Name,
			&entry.SlackID,
			&entry.HappinessLevel,
			&entry.Timestamp,
		)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, nil
			}
			return nil, err
		}
	}

	return &entry, nil
}

func getDBSlackID(db *sql.DB, APIKey string) (SlackID string) {
	row, err := db.Query(`
		SELECT
			entryID,
			APIKey,
			slackID,
			timestamp
		FROM auth
		WHERE APIKey = ?
		LIMIT 1;
	`, APIKey)
	if err != nil {
		log.Fatal(err)
	}
	defer row.Close()

	var user Auth

	for row.Next() {
		err = row.Scan(
			&user.EntryID,
			&user.APIKey,
			&user.SlackID,
			&user.Timestamp,
		)
		if err != nil {
			if err == sql.ErrNoRows {
				log.Fatal(err)
				return
			}
			log.Fatal(err)
			return
		}
	}

	return user.SlackID

}
