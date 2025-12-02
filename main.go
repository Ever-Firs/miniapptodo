package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type Task struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Done      bool   `json:"done"`
	CreatedAt string `json:"created_at"`
}

type CreateTask struct {
	Name string `json:"name"`
}

var db *sql.DB

func main() {
	connStr := "user=postgres password=secret host=localhost port=5432 database=postgres sslmode=disable"
	var err error
	db, err = sql.Open("pgx", connStr)
	if err != nil {
		log.Fatal("❌ Не удалось создать подключение к БД:", err)
	}
	defer db.Close()

	if err = db.Ping(); err != nil {
		log.Fatal("❌ Не удалось подключиться к PostgreSQL:", err)
	}
	log.Println("✅ Успешное подключение к базе данных!")

	r := chi.NewRouter()
	r.Use(middleware.Logger)

	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

			// ОБЯЗАТЕЛЬНО: обработка preflight
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	r.Get("/task", getTask)
	r.Post("/task", postTask)
	r.Patch("/task/{id}", updateTask)

	http.ListenAndServe(":8080", r)
}

func getTask(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`
	SELECT id, name, Done, created_at FROM tasks
	`)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		var createdAt string
		if err := rows.Scan(&t.ID, &t.Name, &t.Done, &t.CreatedAt); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		t.CreatedAt = createdAt
		tasks = append(tasks, t)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

func postTask(w http.ResponseWriter, r *http.Request) {
	var createT CreateTask

	if err := json.NewDecoder(r.Body).Decode(&createT); err != nil {
		http.Error(w, "неверный JSON", http.StatusBadRequest)
		return
	}

	if createT.Name == "" {
		http.Error(w, "текст не может быть пустым", http.StatusBadRequest)
		return
	}

	var t Task
	err := db.QueryRow(
		"INSERT INTO tasks (name) VALUES ($1) RETURNING id, name, done, created_at",
		createT.Name,
	).Scan(&t.ID, &t.Name, &t.Done, &t.CreatedAt)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(t)
}

func updateTask(w http.ResponseWriter, r *http.Request) {
	strid := chi.URLParam(r, "id")
	id, err := strconv.Atoi(strid)

	log.Printf("Получен запрос на обновление задачи ID: %s", chi.URLParam(r, "id"))

	if err != nil {
		http.Error(w, "неверный ID задачи", http.StatusBadRequest)
		return
	}

	var input struct {
		Done bool `json:"done"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "неверный JSON", http.StatusBadRequest)
		return
	}

	var t Task

	err = db.QueryRow(`
	UPDATE tasks SET done = $1 WHERE id = $2 RETURNING id, name, done, created_at
	`, input.Done, id).Scan(&t.ID, &t.Name, &t.Done, &t.CreatedAt)
	if err == sql.ErrNoRows {
		http.Error(w, "задача не найдена", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(t)
}
