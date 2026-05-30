package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
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

	// Prepare log file
	logFile, err := os.OpenFile("api.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0644)
	if err != nil {
		log.Fatal("Failed to load or create log file: ", err)
	}
	logger := slog.New(
		slog.NewJSONHandler(logFile, nil),
	)

	// Load enviroment variables(gets .env by default)
	err = godotenv.Load()
	if err != nil {
		log.Fatal("Failed to load env variables: ", err)
	}

	// Open SQLite database(If there isn't one, it creates it.)
	db, err := sql.Open("sqlite3", "./data.db")
	if err != nil {
		log.Fatal("Failed load or create db: ", err)
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
			http.Error(w, `{"Unable to get happiness friend. Please contact javim in Slack."}`, http.StatusInternalServerError)
			logger.Error("Unable to get happiness friend. Problem with getHappinessFriend.", "error", err)
			return
		}

		if HappinessFriendEntry.EntryID != 0 {
			message := "Your happiness friend is " + HappinessFriendEntry.Name + "! " + "Their slack id is: " + HappinessFriendEntry.SlackID + " and the last time they logged a happiness level of " + strconv.Itoa(HappinessFriendEntry.HappinessLevel) + " was at: " + HappinessFriendEntry.Timestamp.String() + " The note they added to their latest entry was: " + HappinessFriendEntry.Note
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"message": message,
			})
			return
		} else {
			http.Error(w, `{"message": "Nobody with that happiness level was found."}`, http.StatusNotFound)
			return
		}
	})

	/// GET PROFILE
	mux.HandleFunc("GET /profile", func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("Authorization")
		slackID := r.URL.Query().Get("slackID")
		if apiKey == "" {
			http.Error(w, `{"message":"apiKey missing."}`, http.StatusBadRequest)
			return
		}

		if apiKey == os.Getenv("REVIEW_KEY") {
			if slackID == "" {
				entry, averageHappiness, numberOfEntries, err := getProfile(db, "reviewerID")
				if err != nil {
					http.Error(w, `{"message": "Something went wrong. Please contact javim in slack."}`, http.StatusInternalServerError)
					logger.Error("Problem with getProfile().", "error", err)
					return
				}

				if numberOfEntries == 0 {
					http.Error(w, `{"message": "No profile found. Hi reviewer! You first have to create an anonymous entry(/newEntry without slackID). Then, you'll be able to view the anonymous reviewr profile."}`, http.StatusNotFound)
					return
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{
					"Name":                 entry.Name,
					"SlackID":              entry.SlackID,
					"LatestHappinessLevel": strconv.Itoa(entry.HappinessLevel),
					"LatestNote":           entry.Note,
					"LatestEntryTimestamp": entry.Timestamp.String(),
					"AverageHappiness":     strconv.FormatFloat(averageHappiness, 'f', -1, 64),
					"NumberOfEntries":      strconv.Itoa(numberOfEntries),
				})
				return
			} else {
				entry, averageHappiness, numberOfEntries, err := getProfile(db, slackID)
				if err != nil {
					http.Error(w, `{"message": "Something went wrong. Something went wrong. Please contact javim in slack."}`, http.StatusInternalServerError)
					logger.Error("Problem with getProfile().", "error", err)
					return
				}

				if numberOfEntries == 0 {
					http.Error(w, `{"message": "No profile found. ¿Have you created any entries?"}`, http.StatusNotFound)
					logger.Error("Problem with getProfile().", "error", err)
					return
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{
					"Name":                 entry.Name,
					"SlackID":              entry.SlackID,
					"LatestHappinessLevel": strconv.Itoa(entry.HappinessLevel),
					"LatestNote":           entry.Note,
					"LatestEntryTimestamp": entry.Timestamp.String(),
					"AverageHappiness":     strconv.FormatFloat(averageHappiness, 'f', -1, 64),
					"NumberOfEntries":      strconv.Itoa(numberOfEntries),
				})
				return
			}
		}

		realID, err := getDBSlackID(db, apiKey)
		if err != nil {
			http.Error(w, `{"error": "Auth error. Please contact javim in slack."}`, http.StatusInternalServerError)
			logger.Error("Problem with getDBSlackID().", "error", err)
			return
		}

		if slackID == "" {
			http.Error(w, `{"message": "You must include a slackID unless you have a review key."}`, http.StatusBadRequest)
			return
		} else if slackID == realID {
			entry, averageHappiness, numberOfEntries, err := getProfile(db, slackID)
			if err != nil {
				http.Error(w, `{"message": "Something went wrong. Something went wrong. Please contact javim in slack."}`, http.StatusInternalServerError)
				logger.Error("Problem with getProfile().", "error", err)
				return
			}

			if numberOfEntries == 0 {
				http.Error(w, `{"message": "No profile found. ¿Have you created any entries?"}`, http.StatusNotFound)
				logger.Error("Problem with getProfile().", "error", err)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"Name":                 entry.Name,
				"SlackID":              entry.SlackID,
				"LatestHappinessLevel": strconv.Itoa(entry.HappinessLevel),
				"LatestNote":           entry.Note,
				"LatestEntryTimestamp": entry.Timestamp.String(),
				"AverageHappiness":     strconv.FormatFloat(averageHappiness, 'f', -1, 64),
				"NumberOfEntries":      strconv.Itoa(numberOfEntries),
			})
			return
		} else {
			http.Error(w, `{"message": "The provided API key doesn't match the provided SlackID. ¿Did you register? DM me to do so."}`, http.StatusUnauthorized)
			return
		}

	})

	/// GET STATS
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		message, err := getStats(db)
		if err != nil {
			http.Error(w, "Unable to get stats. Please contact javim in slack.", http.StatusInternalServerError)
			logger.Error("Problem with getStats()", "error", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": message,
		})
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
		} else if utf8.RuneCountInString(entry.Note) > 300 ||
			utf8.RuneCountInString(entry.Note) < 10 {
			http.Error(w, `{"error": "Invalid note length. Max:300/Min:10 chars."}`, http.StatusBadRequest)
			return
		}

		realID, getDBSlackIDerr := getDBSlackID(db, entry.APIKey)

		if entry.APIKey == os.Getenv("REVIEW_KEY") {
			if entry.SlackID == "" {
				err := newEntry(db, "anonymousReviewer", "reviewerID", entry.HappinessLevel, entry.Note, time.Now())
				if err != nil {
					http.Error(w, `{"error": "DB failure. Please contact javim in slack."}`, http.StatusInternalServerError)
					logger.Error("Problem with newEntry.", "error", err)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{
					"message": "Your happiness level has been logged anonymously(since you didn't include a SlackID, this is only allowed for reviewers)!",
				})
				logger.Info("A reviewer created a new Entry!", "Note", entry.Note, "HappinesLevel", entry.HappinessLevel)
				return
			} else {
				userInfo, err := slackApi.GetUserInfo(entry.SlackID)
				if err != nil {
					http.Error(w, `{"error": "Unable to get slack username from id. Try without SlackID or contact javim in slack."}`, http.StatusInternalServerError)
					logger.Error("Unable to get slack username from id. Problem with slackApi.GetUserInfo().", "error", err)
					return
				}

				name := userInfo.Profile.DisplayName
				if name == "" {
					name = userInfo.RealName
				}
				if name == "" {
					name = userInfo.Name
				}

				err = newEntry(db, name, entry.SlackID, entry.HappinessLevel, entry.Note, time.Now())
				if err != nil {
					http.Error(w, `{"error": "DB failure. Please contact javim in slack."}`, http.StatusInternalServerError)
					logger.Error("Problem with newEntry.", "error", err)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{
					"message": name + ", your happiness level has been logged!",
				})
				logger.Info("A reviewer created a new Entry!", "name", name, "slackid:", entry.SlackID, "note", entry.Note, "happinesslevel", entry.HappinessLevel)
				return
			}
		} else if getDBSlackIDerr != nil {
			http.Error(w, `{"error": "Auth error. Please contact javim in slack."}`, http.StatusInternalServerError)
			logger.Error("Problem with getDBSlackID().", "error", getDBSlackIDerr)
			return
		} else if entry.SlackID == "" {
			http.Error(w, `{"error": "You must include a SlackID unless you have a review key."}`, http.StatusBadRequest)
			return
		} else if entry.SlackID == realID {
			userInfo, err := slackApi.GetUserInfo(entry.SlackID)
			if err != nil {
				http.Error(w, `{"error": "Unable to get slack username from id. Does the slack account exist? Please contact javim in slack."}`, http.StatusInternalServerError)
				logger.Error("Problem with slackApi.GetUserInfo().", "error:", err)
				return
			}

			name := userInfo.Profile.DisplayName
			if name == "" {
				name = userInfo.RealName
			}
			if name == "" {
				name = userInfo.Name
			}

			err = newEntry(db, name, entry.SlackID, entry.HappinessLevel, entry.Note, time.Now())
			if err != nil {
				http.Error(w, `{"error": "DB failure. Please contact javim in slack."}`, http.StatusInternalServerError)
				logger.Error("Problem with newEntry.", "error", err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"message": name + ", your happiness level has been logged!",
			})
			return
		} else {
			http.Error(w, `{"error": "The provided API key doesn't match the provided SlackID. ¿Did you register? DM me to do so."}`, http.StatusUnauthorized)
			return
		}
	})

	/// POST NEW USER
	mux.HandleFunc("POST /newUser", func(w http.ResponseWriter, r *http.Request) {
		var user POSTNewUser
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&user); err != nil {
			http.Error(w, `{"error": "Invalid JSON or unknown fields provided"}`, http.StatusBadRequest)
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
				logger.Error("Failed to generate API key.", "error", err)
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

			err = newUser(db, generatedAPIKey, user.SlackID, time.Now())
			if err != nil {
				http.Error(w, `{"error": "DB failure."}`, http.StatusInternalServerError)
				logger.Error("Problem with newUser().", "error", err)
				return
			}

			message := "User resgistered! API Key: " + generatedAPIKey + " SlackID: " + user.SlackID + " Slack Name: " + name
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"message": message,
			})
			logger.Info("User resgistered!", "SlackID", user.SlackID, "Slack Name", name)
			return
		} else {
			http.Error(w, `{"error": "Invalid management key"}`, http.StatusUnauthorized)
			logger.Info("Somebody tried to create a new user without a management key", "ip", r.RemoteAddr)
			return
		}

	})

	fmt.Println("Server running on http://localhost:8080")
	http.ListenAndServe(":8080", mux)
}

// Functions to deal with the databases

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
	_, err := db.Exec(
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
	return nil
}

func newUser(db *sql.DB, APIKey string, SlackID string, Timestamp time.Time) (error error) {
	SQLNewUser := `
	INSERT INTO auth
	(APIKey, slackID, timestamp)
	VALUES (?, ?, ?)
	`
	_, err := db.Exec(
		SQLNewUser,
		APIKey,
		SlackID,
		Timestamp,
	)
	if err != nil {
		return err
	}
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
		LIMIT 2;
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

func getProfile(db *sql.DB, slackID string) (*HappinessEntry, float64, int, error) {
	row, err := db.Query(`
		SELECT
			entryID,
			name,
			slackID,
			happinessLevel,
			note,
			timestamp
		FROM data
		WHERE slackID = ?
		ORDER BY timestamp ASC;
	`, slackID)
	if err != nil {
		return nil, 0, 0, err
	}
	defer row.Close()

	var entry HappinessEntry
	var totalHappiness int
	var numberOfEntries int

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
				return nil, 0, 0, nil
			}
			return nil, 0, 0, err
		}

		numberOfEntries += 1

		totalHappiness = totalHappiness + entry.HappinessLevel
	}

	var averageHappiness float64 = float64(totalHappiness) / float64(numberOfEntries)

	return &entry, averageHappiness, numberOfEntries, nil
}

func getStats(db *sql.DB) (message string, error error) {
	row, err := db.Query(`
		SELECT
			entryID
		FROM data;
	`)
	if err != nil {
		return "", err
	}
	defer row.Close()

	var entry HappinessEntry
	var numberOfEntries int

	for row.Next() {
		err = row.Scan(
			&entry.EntryID,
		)
		if err != nil {
			if err == sql.ErrNoRows {
				return "", nil
			}
			return "", err
		}

		numberOfEntries += 1
	}

	row1, err := db.Query(`
		SELECT
			entryID
		FROM auth;
	`)
	if err != nil {
		return "", err
	}
	defer row1.Close()

	var entry1 Auth
	var numberOfEntries1 int

	for row1.Next() {
		err = row1.Scan(
			&entry1.EntryID,
		)
		if err != nil {
			if err == sql.ErrNoRows {
				return "", nil
			}
			return "", err
		}

		numberOfEntries1 += 1
	}

	message = "Total number of entries: " + strconv.Itoa(numberOfEntries) + " Total number of users: " + strconv.Itoa(numberOfEntries1)

	return message, nil
}
