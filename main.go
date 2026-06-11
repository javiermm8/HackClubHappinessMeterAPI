package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/joho/godotenv"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"golang.org/x/time/rate"

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
	ManagementKey string
	SlackID       string
}

type clientEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Rate limit variables
var clients = make(map[string]*clientEntry)
var mu sync.Mutex

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
			"message": "Service is running.",
		})
	})

	/// GET DOCS
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})

	/// GET HAPPINESS FRIEND
	mux.HandleFunc("GET /happinessFriend", func(w http.ResponseWriter, r *http.Request) {
		rawHappinessLevel := r.URL.Query().Get("happinessLevel")
		happinessLevel, err := strconv.Atoi(rawHappinessLevel)
		// Verify happiness level is ok
		if err != nil {
			http.Error(w, "Invalid happiness level.", http.StatusBadRequest)
			return
		}
		if happinessLevel <= 0 || happinessLevel > 10 {
			http.Error(w, "Invalid happiness level. Max:10/Min:1", http.StatusBadRequest)
			return
		}

		// Get friend from db(the actual search is managed by sqlite)
		HappinessFriendEntry, err := getHappinessFriend(db, happinessLevel)
		if err != nil {
			http.Error(w, "Unable to get happiness friend. Please contact javim on Slack.", http.StatusInternalServerError)
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
			http.Error(w, "No users found with that happiness level.", http.StatusNotFound)
			return
		}
	})

	/// GET PROFILE
	mux.HandleFunc("GET /profile", func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("Authorization")
		slackID := r.URL.Query().Get("slackID")
		if apiKey == "" {
			http.Error(w, "apiKey missing.", http.StatusBadRequest)
			return
		}

		hash := sha256.New()
		_, err := hash.Write([]byte(apiKey))
		if err != nil {
			http.Error(w, "Something went wrong while creating your API Key.", http.StatusInternalServerError)
			logger.Error("Problem with almostHash.Write()", "error", err)
			return
		}

		if apiKey == os.Getenv("REVIEW_KEY") {
			if slackID == "" {
				entry, averageHappiness, numberOfEntries, err := getProfile(db, "reviewerID")
				if err != nil {
					http.Error(w, "Something went wrong. Please contact javim on slack.", http.StatusInternalServerError)
					logger.Error("Problem with getProfile().", "error", err)
					return
				}

				if numberOfEntries == 0 {
					http.Error(w, "No profile found. Hi reviewer! You first have to create an anonymous entry(/newEntry without slackID). Then, you'll be able to view the anonymous reviewer profile.", http.StatusNotFound)
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
					http.Error(w, "Something went wrong. Something went wrong. Please contact javim on slack.", http.StatusInternalServerError)
					logger.Error("Problem with getProfile().", "error", err)
					return
				}

				if numberOfEntries == 0 {
					http.Error(w, "No profile found. Have you created any entries?", http.StatusNotFound)
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

		realID, err := getDBSlackID(db, hex.EncodeToString(hash.Sum(nil)))
		if err != nil {
			http.Error(w, "Auth error. Please contact javim on slack.", http.StatusInternalServerError)
			logger.Error("Problem with getDBSlackID().", "error", err)
			return
		}

		if slackID == "" {
			http.Error(w, "You must include a slackID unless you have a review key.", http.StatusBadRequest)
			return
		} else if slackID == realID {
			entry, averageHappiness, numberOfEntries, err := getProfile(db, slackID)
			if err != nil {
				http.Error(w, "Something went wrong. Please contact javim on Slack.", http.StatusInternalServerError)
				logger.Error("Problem with getProfile().", "error", err)
				return
			}

			if numberOfEntries == 0 {
				http.Error(w, "No profile found. Have you created any entries?", http.StatusNotFound)
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
			http.Error(w, "The provided API key doesn't match the provided SlackID. Did you register? DM me to do so.", http.StatusUnauthorized)
			return
		}

	})

	/// GET STATS
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		message, err := getStats(db)
		if err != nil {
			http.Error(w, "Unable to get stats. Please contact javim on Slack.", http.StatusInternalServerError)
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

		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

		var entry POSTNewEntry
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&entry); err != nil {
			http.Error(w, "Invalid JSON or unknown fields provided", http.StatusBadRequest)
			return
		} else if entry.APIKey == "" ||
			entry.HappinessLevel <= 0 ||
			entry.HappinessLevel > 10 ||
			entry.Note == "" {
			http.Error(w, "Missing parameters or invalid values", http.StatusBadRequest)
			return
		} else if utf8.RuneCountInString(entry.Note) > 300 ||
			utf8.RuneCountInString(entry.Note) < 10 {
			http.Error(w, "Invalid note length. Max:300/Min:10 chars.", http.StatusBadRequest)
			return
		}

		hash := sha256.New()
		_, err := hash.Write([]byte(entry.APIKey))
		if err != nil {
			http.Error(w, "Something went wrong while creating your API Key.", http.StatusInternalServerError)
			logger.Error("Problem with almostHash.Write()", "error", err)
			return
		}

		realID, getDBSlackIDerr := getDBSlackID(db, hex.EncodeToString(hash.Sum(nil)))

		if entry.APIKey == os.Getenv("REVIEW_KEY") {
			if entry.SlackID == "" {
				err := newEntry(db, "anonymousReviewer", "reviewerID", entry.HappinessLevel, entry.Note, time.Now())
				if err != nil {
					http.Error(w, "DB failure. Please contact javim on Slack.", http.StatusInternalServerError)
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
					http.Error(w, "Unable to get slack username from id. Try without SlackID or contact javim on Slack.", http.StatusInternalServerError)
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
					http.Error(w, "DB failure. Please contact javim on Slack.", http.StatusInternalServerError)
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
			http.Error(w, "Auth error. Please contact javim on Slack.", http.StatusInternalServerError)
			logger.Error("Problem with getDBSlackID().", "error", getDBSlackIDerr)
			return
		} else if entry.SlackID == "" {
			http.Error(w, "You must include a SlackID unless you have a review key.", http.StatusBadRequest)
			return
		} else if entry.SlackID == realID {
			userInfo, err := slackApi.GetUserInfo(entry.SlackID)
			if err != nil {
				http.Error(w, "Unable to get slack username from id. Does the slack account exist? Please contact javim on Slack.", http.StatusInternalServerError)
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
				http.Error(w, "DB failure. Please contact javim on Slack.", http.StatusInternalServerError)
				logger.Error("Problem with newEntry.", "error", err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"message": name + ", your happiness level has been logged!",
			})
			return
		} else {
			http.Error(w, "The provided API key doesn't match the provided SlackID. Did you register? DM me to do so.", http.StatusUnauthorized)
			return
		}
	})

	/// POST NEW USER
	mux.HandleFunc("POST /newUser", func(w http.ResponseWriter, r *http.Request) {

		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

		var user POSTNewUser
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&user); err != nil {
			http.Error(w, "Invalid JSON or unknown fields provided", http.StatusBadRequest)
			return
		} else if user.SlackID == "" {
			http.Error(w, "You must include a SlackID", http.StatusBadRequest)
			return
		}

		if user.ManagementKey == os.Getenv("MANAGEMENT_KEY") {
			// Generate new API Key(the one I have to send to the user)
			bytes := make([]byte, 32)
			if _, err := rand.Read(bytes); err != nil {
				http.Error(w, "Failed to generate API key", http.StatusInternalServerError)
				logger.Error("Failed to generate API key.", "error", err)
				return
			}
			generatedAPIKey := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(bytes)
			hash := sha256.New()
			_, err := hash.Write([]byte(generatedAPIKey))
			if err != nil {
				http.Error(w, "Something went wrong while creating your API Key.", http.StatusInternalServerError)
				logger.Error("Problem with almostHash.Write()", "error", err)
				return
			}

			userInfo, err := slackApi.GetUserInfo(user.SlackID)
			if err != nil {
				http.Error(w, "Unable to get slack username from id. Maybe a typo?", http.StatusInternalServerError)
				return
			}

			name := userInfo.Profile.DisplayName
			if name == "" {
				name = userInfo.RealName
			}
			if name == "" {
				name = userInfo.Name
			}

			alreadyExists, err := newUser(db, hex.EncodeToString(hash.Sum(nil)), user.SlackID, time.Now())
			if err != nil {
				http.Error(w, "DB failure.", http.StatusInternalServerError)
				logger.Error("Problem with newUser().", "error", err)
				return
			} else if alreadyExists == true {
				http.Error(w, "User already exists.", http.StatusBadRequest)
				return
			}

			logger.Info("User registered!", "SlackID", user.SlackID, "Slack Name", name)
			message := "User registered! API Key: " + generatedAPIKey + " SlackID: " + user.SlackID + " Slack Name: " + name
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"message": message,
			})
			return
		} else {
			http.Error(w, "Invalid management key", http.StatusUnauthorized)
			logger.Info("Somebody tried to create a new user without a management key", "ip", r.RemoteAddr)
			return
		}

	})

	/// SLACK BOT RECEIVE MESSAGES
	mux.HandleFunc("/slackEvents", func(w http.ResponseWriter, r *http.Request) {

		r.Body = http.MaxBytesReader(w, r.Body, 2048*1024)

		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		secretsVerifier, err := slack.NewSecretsVerifier(r.Header, os.Getenv("SLACK_BOT_SIGNING_SECRET"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, err := secretsVerifier.Write(body); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if err := secretsVerifier.Ensure(); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		eventsAPIEvent, err := slackevents.ParseEvent(json.RawMessage(body), slackevents.OptionNoVerifyToken())
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// IDE wants me to use a switch, which I have no idea how to use. If else works, so it stays that way.
		if eventsAPIEvent.Type == slackevents.URLVerification {
			var challenge *slackevents.ChallengeResponse

			err := json.Unmarshal([]byte(body), &challenge)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text")
			w.Write([]byte(challenge.Challenge))
		} else if eventsAPIEvent.Type == slackevents.CallbackEvent {
			innerEvent := eventsAPIEvent.InnerEvent
			ev, ok := innerEvent.Data.(*slackevents.MessageEvent)

			if ok == false {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			if ev.User == os.Getenv("BOT_ID") {
				return
			}

			logger.Info("The bot received a message", "SlackID", ev.User, "Message", ev.Text)

			if ok == true && ev.Text == "Register" {
				bytes := make([]byte, 32)
				if _, err := rand.Read(bytes); err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					logger.Error("Failed to generate API key.", "error", err)
					return
				}
				generatedAPIKey := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(bytes)
				hash := sha256.New()
				_, err := hash.Write([]byte(generatedAPIKey))
				if err != nil {
					http.Error(w, "Something went wrong while creating your API Key.", http.StatusInternalServerError)
					logger.Error("Problem with almostHash.Write()", "error", err)
					return
				}

				userInfo, err := slackApi.GetUserInfo(ev.User)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				name := userInfo.Profile.DisplayName
				if name == "" {
					name = userInfo.RealName
				}
				if name == "" {
					name = userInfo.Name
				}

				alreadyExists, err := newUser(db, hex.EncodeToString(hash.Sum(nil)), ev.User, time.Now())
				if err != nil {
					_, _, err = slackApi.PostMessage(
						ev.Channel, slack.MsgOptionText("Something went wrong. Please contact javim on Slack.", false),
					)
					if err != nil {
						w.WriteHeader(http.StatusInternalServerError)
						logger.Info("Failed to send message", "SlackID", ev.User)
					}
					logger.Error("Problem with newUser().", "error", err)
					return
				} else if alreadyExists == true {
					_, _, err = slackApi.PostMessage(
						ev.Channel, slack.MsgOptionText("User already exists.", false),
					)
					if err != nil {
						w.WriteHeader(http.StatusInternalServerError)
						logger.Info("Failed to send message", "SlackID", ev.User)
					}
					return
				}

				logger.Info("User registered by Slack Bot!", "SlackID", ev.User, "Slack Name", name)
				message := "Hi " + name + "! Your API Key is: " + generatedAPIKey + " Keep it safe! SlackID: " + ev.User + " Slack Name: " + name

				_, _, err = slackApi.PostMessage(
					ev.Channel, slack.MsgOptionText(message, false),
				)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					logger.Info("Failed to send message", "SlackID", ev.User)
				}
				return
			} else {
				return
			}
		}

	})

	fmt.Println("Server running on http://127.0.0.1:8081")
	err = http.ListenAndServe(":8081", corsMiddleware(mux))
	if err != nil {
		log.Fatal(err)
	}

	//gorutine that cleans clients after 10 minutes
	go func() {
		//same as for true
		for {
			time.Sleep(10 * time.Minute)

			mu.Lock()
			for ip, c := range clients {
				if time.Since(c.lastSeen) > 30*time.Minute {
					delete(clients, ip)
				}
			}
			mu.Unlock()
		}
	}()
}

// Functions to deal with the database
func createTable(db *sql.DB) {
	// SQL syntaxt to create the table(if it doesn't already exist) with it's necessary colums.
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

	fmt.Println("Database tables are ready.")
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

func newUser(db *sql.DB, APIKey string, SlackID string, Timestamp time.Time) (alreadyExists bool, error error) {
	row, err := db.Query(`
		SELECT
			entryID,
			slackID
		FROM auth
		WHERE slackID = ?;
	`, SlackID)
	if err != nil {
		return false, err
	}
	defer row.Close()

	var user Auth

	for row.Next() {
		row.Scan(
			&user.EntryID,
			&user.SlackID,
		)
	}
	if user.SlackID != "" {
		return true, nil
	}

	SQLNewUser := `
	INSERT INTO auth
	(APIKey, slackID, timestamp)
	VALUES (?, ?, ?)
	`
	_, err = db.Exec(
		SQLNewUser,
		APIKey,
		SlackID,
		Timestamp,
	)
	if err != nil {
		return false, err
	}
	return false, nil
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

// CORS MIDDLEWARE
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, ngrok-skip-browser-warning")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		mu.Lock()

		c, exists := clients[ip]
		if exists == false {
			c = &clientEntry{
				limiter: rate.NewLimiter(5, 10),
			}
			clients[ip] = c
		}

		c.lastSeen = time.Now()
		limiter := c.limiter
		mu.Unlock()

		if limiter.Allow() == false {
			http.Error(w, "Too many requests.", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
