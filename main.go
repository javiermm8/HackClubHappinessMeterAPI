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
	"unicode/utf8"

	"github.com/joho/godotenv"
	"github.com/slack-go/slack"

	_ "github.com/mattn/go-sqlite3"
)

// Structs for db
type HappinessEntry struct {
	EntryID        int
	Name           string
	SlackID        string
	HappinessLevel int
	Note           string
	Timestamp      time.Time
}
type Auth struct {
	EntryID   int
	APIKey    string
	SlackID   string
	Timestamp time.Time
}

// Structs for POST requests
type POSTNewEntry struct {
	APIKey         string
	SlackID        string
	HappinessLevel int
	Note           string
}
type POSTNewUser struct {
	ManagmentKey string
	SlackID      string
}

func main() {

	// Load enviroment variables(gets .env by default)
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

	// Start http server
	mux := http.NewServeMux()

	/// GET STATUS
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": "All good!",
		})
	})

	/// GET HAPPINESS FRIEND
	mux.HandleFunc("GET /happinessFriend", func(w http.ResponseWriter, r *http.Request) {
		rawHappinessLevel := r.URL.Query().Get("happinessLevel")
		happinessLevel, err := strconv.Atoi(rawHappinessLevel)
		// Verify happiness level is ok
		if err != nil {
			http.Error(w, `{"error": "Invalid happiness level."}`, http.StatusBadRequest)
			return
		}
		if happinessLevel < 0 || happinessLevel > 10 {
			http.Error(w, `{"error": "Invalid happiness level. Max:10/Min:0"}`, http.StatusBadRequest)
			return
		}

		// Get friend from db(the actual search is managed by sqlite)
		HappinessFriendEntry, err := getHappinessFriend(db, happinessLevel)
		if err != nil {
			http.Error(w, `{"Unable to get happiness friend. DB error. Please contact javim in Slack."}`, http.StatusInternalServerError)
		}

		if HappinessFriendEntry.EntryID != 0 {
			message := "Your happiness friend is " + HappinessFriendEntry.Name + "! " + "Their slack id is: " + HappinessFriendEntry.SlackID + " and the last time they logged a happiness level of " + strconv.Itoa(HappinessFriendEntry.HappinessLevel) + " was at: " + HappinessFriendEntry.Timestamp.String()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"message": message,
			})
		} else {
			http.Error(w, `{"Nobody with that happiness level was found."}`, http.StatusNotFound)
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
			entry.HappinessLevel > 10 ||
			entry.Note == "" {
			http.Error(w, `{"error": "missing parameters or invalid values"}`, http.StatusBadRequest)
			return
		} else if utf8.RuneCountInString(entry.Note) >= 300 ||
			utf8.RuneCountInString(entry.Note) < 10 {
			http.Error(w, `{"error": "Invalid note length. Max:300/Min:10 chars."}`, http.StatusBadRequest)
			return
		}

		realID, getDBSlackIDerr := getDBSlackID(db, entry.APIKey)

		if entry.APIKey == os.Getenv("REVIEW_KEY") {
			if entry.SlackID == "" {
				err := newEntry(db, "anonymousReviewer", entry.SlackID, entry.HappinessLevel, entry.Note, time.Now())
				if err != nil {
					http.Error(w, `{"error": "DB failure. Please contact javim in slack."}`, http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{
					"message": "Your happiness level has been logged anonymously(since you didn't include a SlackID, this is only allowed for reviewers)!",
				})
			} else {
				userInfo, err := slackApi.GetUserInfo(entry.SlackID)
				if err != nil {
					http.Error(w, `{"error": "Unable to get slack username from id. Try without SlackID or contact javim in slack."}`, http.StatusInternalServerError)
					return
				}

				name := userInfo.Profile.DisplayName
				if name == "" {
					name = userInfo.RealName
				}
				if name == "" {
					name = userInfo.Name
				}

				err1 := newEntry(db, name, entry.SlackID, entry.HappinessLevel, entry.Note, time.Now())
				if err1 != nil {
					http.Error(w, `{"error": "DB failure. Please contact javim in slack."}`, http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{
					"message": name + ", your happiness level has been logged!",
				})
			}
		} else if getDBSlackIDerr != nil {
			http.Error(w, `{"error": "Auth error. Please contact javim in slack."}`, http.StatusInternalServerError)
			return
		} else if entry.SlackID == "" {
			http.Error(w, `{"error": "You must include a SlackID unless you have a review key."}`, http.StatusBadRequest)
			return
		} else if entry.SlackID == realID {
			userInfo, err := slackApi.GetUserInfo(entry.SlackID)
			if err != nil {
				http.Error(w, `{"error": "Unable to get slack username from id. Please contact javim in slack."}`, http.StatusInternalServerError)
				return
			}

			name := userInfo.Profile.DisplayName
			if name == "" {
				name = userInfo.RealName
			}
			if name == "" {
				name = userInfo.Name
			}

			err1 := newEntry(db, name, entry.SlackID, entry.HappinessLevel, entry.Note, time.Now())
			if err1 != nil {
				http.Error(w, `{"error": "DB failure. Please contact javim in slack."}`, http.StatusInternalServerError)
				return
			}
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
		} else if user.SlackID == "" {
			http.Error(w, `{"error": "You must inclued a SlackID"}`, http.StatusBadRequest)
			return
		}

		if user.ManagmentKey == os.Getenv("MANAGEMENT_KEY") {
			// Generate new API Key(the one I have to send to the user)
			bytes := make([]byte, 32)
			if _, err := rand.Read(bytes); err != nil {
				http.Error(w, `{"error": "Failed to generate API key"}`, http.StatusInternalServerError)
				return
			}
			generatedAPIKey := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(bytes)

			userInfo, err := slackApi.GetUserInfo(user.SlackID)
			if err != nil {
				http.Error(w, `{"error": "Unable to get slack username from id. Maybe a typo?"}`, http.StatusInternalServerError)
				return
			}

			name := userInfo.Profile.DisplayName
			if name == "" {
				name = userInfo.RealName
			}
			if name == "" {
				name = userInfo.Name
			}

			err1 := newUser(db, generatedAPIKey, user.SlackID, time.Now())
			if err1 != nil {
				http.Error(w, `{"error": "DB failure."}`, http.StatusInternalServerError)
			}

			message := "User resgistered! API Key: " + generatedAPIKey + " " + "SlackID: " + user.SlackID + " " + "Slack Name: " + name
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
	note TEXT,
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
		fmt.Println("Failed to create data db.")
		log.Fatal(err)
	}
	_, err = db.Exec(SQLcreateTableAuth)
	if err != nil {
		fmt.Println("Failed to create auth db.")
		log.Fatal(err)
	}

	fmt.Println("I don't know if there was a table, but there is one now.")

}

func newEntry(
	db *sql.DB,
	Name string,
	SlackID string,
	HappinessLevel int,
	Note string,
	Timestamp time.Time,
) (error error) {
	SQLnewEntry := `
	INSERT INTO data
	(name, slackID, happinessLevel, note, timestamp)
	VALUES (?, ?, ?, ?, ?)
	`
	output, err := db.Exec(
		SQLnewEntry,
		Name,
		SlackID,
		HappinessLevel,
		Note,
		Timestamp,
	)
	if err != nil {
		return err
	}

	id, err := output.LastInsertId()
	if err != nil {
		return err
	}

	fmt.Println("Entry added! Entry ID:", id)
	return nil
}

func newUser(db *sql.DB, APIKey string, SlackID string, Timestamp time.Time) (error error) {
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
		return err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	fmt.Println("User added! User ID:", id)
	return nil
}

func getHappinessFriend(db *sql.DB, happinessLevel int) (*HappinessEntry, error) {
	row, err := db.Query(`
		SELECT
			entryID,
			name,
			slackID,
			happinessLevel,
			note,
			timestamp
		FROM data
		WHERE happinessLevel = ?
		ORDER BY timestamp DESC
		LIMIT 1;
	`, happinessLevel)
	if err != nil {
		return nil, err
	}
	defer row.Close()

	var entry HappinessEntry
	for row.Next() {
		err = row.Scan(
			&entry.EntryID,
			&entry.Name,
			&entry.SlackID,
			&entry.HappinessLevel,
			&entry.Note,
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

func getDBSlackID(db *sql.DB, APIKey string) (SlackID string, error error) {
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
		return "", err
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
				return "", nil
			}
			return "", err
		}
	}

	return user.SlackID, nil

}
