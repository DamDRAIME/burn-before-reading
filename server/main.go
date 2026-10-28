package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

type Message struct {
	ID          int       `json:"id"`
	Sender      string    `json:"sender"`
	Content     string    `json:"content"`
	Status      string    `json:"status"`
	NAttempts   int       `json:"n_attempts"`
	CreatedAt   time.Time `json:"created_at"`
	LastUpdated time.Time `json:"last_updated"`
}

type App struct {
	DB *sql.DB
}

func main() {
	dbUser := os.Getenv("DB_USER")
	dbPassword := os.Getenv("DB_PASSWORD")
	dbHost := os.Getenv("DB_HOST")
	dbPort := os.Getenv("DB_PORT")
	dbName := os.Getenv("DB_NAME")

	if dbUser == "" || dbPassword == "" || dbHost == "" || dbPort == "" || dbName == "" {
		log.Fatal("Database configuration environment variables are not set")
	}

	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", dbHost, dbPort, dbUser, dbPassword, dbName)

	var db *sql.DB
	var err error

	for i := 0; i < 5; i++ {
		db, err = sql.Open("postgres", connStr)
		if err == nil {
			err = db.Ping()
			if err == nil {
				log.Printf("Attempt %d: Database ping failed: %v", i+1, err)
				break
			}
		}
		log.Printf("Attempt %d: Failed to connect to database: %v", i+1, err)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		log.Fatalf("Could not connect to the database: %v", err)
	}
	defer db.Close()

	log.Println("Connected to the database successfully.")

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id SERIAL PRIMARY KEY,
			sender VARCHAR(100) NOT NULL,
			content TEXT NOT NULL,
			status VARCHAR(50) NOT NULL,
			n_attempts INT NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_updated TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		log.Fatalf("Could not create messages table: %v", err)
	}
	log.Println("Messages table is ready.")

	app := &App{DB: db}

	mux := http.NewServeMux()
	mux.HandleFunc("/messages", app.handleMessages)
	mux.HandleFunc("/messages/", app.handleMessageByID)

	port := ":8080"
	log.Printf("Server is running on port %s", port)
	if err := http.ListenAndServe(port, mux); err != nil {
		log.Fatalf("Could not start server: %v", err)
	}
}

func (app *App) handleMessages(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		app.getMessages(w, r)
	case http.MethodPost:
		app.createMessage(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *App) getMessages(w http.ResponseWriter, r *http.Request) {
	rows, err := app.DB.Query("SELECT id, sender, content, status, n_attempts, created_at, last_updated FROM messages")
	if err != nil {
		http.Error(w, "Failed to fetch messages", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Sender, &m.Content, &m.Status, &m.NAttempts, &m.CreatedAt, &m.LastUpdated); err != nil {
			http.Error(w, "Failed to scan message", http.StatusInternalServerError)
			return
		}
		messages = append(messages, m)
	}

	if messages == nil {
		messages = []Message{}
	}

	json.NewEncoder(w).Encode(messages)
}

func (app *App) createMessage(w http.ResponseWriter, r *http.Request) {
	var m Message
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	if m.Sender == "" || m.Content == "" {
		http.Error(w, "Invalid message data", http.StatusBadRequest)
		return
	}

	err := app.DB.QueryRow("INSERT INTO messages (sender, content, status) VALUES ($1, $2, $3) RETURNING id, created_at, last_updated", m.Sender, m.Content, m.Status).Scan(&m.ID, &m.CreatedAt, &m.LastUpdated)
	if err != nil {
		http.Error(w, "Failed to create message", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(m)
}

func (app *App) handleMessageByID(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	idStr := strings.TrimPrefix(r.URL.Path, "/messages/")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid message ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		var m Message
		err := app.DB.QueryRow("SELECT id, sender, content, status, n_attempts, created_at, last_updated FROM messages WHERE id = $1", id).Scan(&m.ID, &m.Sender, &m.Content, &m.Status, &m.NAttempts, &m.CreatedAt, &m.LastUpdated)
		if err == sql.ErrNoRows {
			http.Error(w, "Message not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, "Failed to fetch message", http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(m)

	case http.MethodPut:
		var m Message
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			http.Error(w, "Invalid request payload", http.StatusBadRequest)
			return
		}

		if m.Sender == "" || m.Content == "" {
			http.Error(w, "Invalid message data", http.StatusBadRequest)
			return
		}

		result, err := app.DB.Exec("UPDATE messages SET sender = $1, content = $2, status = $3 WHERE id = $4", m.Sender, m.Content, m.Status, id)
		if err != nil {
			http.Error(w, "Failed to update message", http.StatusInternalServerError)
			return
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			http.Error(w, "Failed to retrieve update result", http.StatusInternalServerError)
			return
		}

		if rowsAffected == 0 {
			http.Error(w, "Message not found", http.StatusNotFound)
			return
		}

		m.ID = id
		json.NewEncoder(w).Encode(m)

	case http.MethodDelete:
		result, err := app.DB.Exec("DELETE FROM messages WHERE id = $1", id)
		if err != nil {
			http.Error(w, "Failed to delete message", http.StatusInternalServerError)
			return
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			http.Error(w, "Failed to retrieve delete result", http.StatusInternalServerError)
			return
		}

		if rowsAffected == 0 {
			http.Error(w, "Message not found", http.StatusNotFound)
			return
		}

		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
