package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type Task struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Done      bool   `json:"done"`
	CreatedAt string `json:"created_at"`
	UserID    int    `json:"-"`
}

type CreateTask struct {
	Name string `json:"name"`
}

var db *sql.DB

const jwtSecret = "очень-секретный-ключ-для-девелопа"

type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
}

type Claims struct {
	UserID int `json:"user_id"`
	jwt.RegisteredClaims
}

type RegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Пропускаем CORS preflight
		if r.Method == "OPTIONS" {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "требуется авторизация", http.StatusUnauthorized)
			return
		}

		// Ожидаем формат: "Bearer <token>"
		var tokenStr string
		_, err := fmt.Sscanf(authHeader, "Bearer %s", &tokenStr)
		if err != nil {
			http.Error(w, "неверный формат токена", http.StatusUnauthorized)
			return
		}

		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
			return []byte(jwtSecret), nil
		})

		if err != nil || !token.Valid {
			http.Error(w, "недействительный токен", http.StatusUnauthorized)
			return
		}

		// Передаём userID дальше через контекст (опционально)
		ctx := context.WithValue(r.Context(), "userID", claims.UserID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

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

	// В функции main(), замените CORS middleware на:
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Accept")
			w.Header().Set("Access-Control-Expose-Headers", "Authorization")
			w.Header().Set("Access-Control-Max-Age", "86400") // 24 часа

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	r.Post("/register", registerUser)
	r.Post("/login", loginUser)
	r.Route("/task", func(r chi.Router) {
		r.Use(AuthMiddleware)
		r.Get("/", getTask)
		r.Post("/", postTask)
		r.Patch("/{id}", updateTask)
		r.Delete("/{id}", deleteTask)
	})

	http.ListenAndServe(":8080", r)
}

func registerUser(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "неверный JSON", http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" {
		http.Error(w, "логин и пароль обязательны", http.StatusBadRequest)
		return
	}

	_, err := db.Exec(`
	INSERT INTO users (username, password) VALUES ($1, $2)
	`, req.Username, req.Password)
	if err != nil {
		http.Error(w, "пользователь уже существует", http.StatusConflict)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message": "Пользователь создан"})
}

func loginUser(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "неверный JSON", http.StatusBadRequest)
		return
	}

	var userID int
	var storedPassword string

	err := db.QueryRow(`
	SELECT id, password FROM users WHERE username = $1
	`, req.Username).Scan(&userID, &storedPassword)
	if err != nil {
		http.Error(w, "неверный логин или пароль", http.StatusUnauthorized)
		return
	}

	if storedPassword != req.Password {
		http.Error(w, "неверный логин или пароль", http.StatusUnauthorized)
		return
	}

	expirationTime := time.Now().Add(24 * time.Hour)
	claims := &Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(jwtSecret))
	if err != nil {
		http.Error(w, "ошибка генерации токена", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": tokenString})
}

func getTask(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(int)
	if !ok {
		http.Error(w, "пользователь не авторизован", http.StatusUnauthorized)
		return
	}

	rows, err := db.Query(`
	SELECT id, name, Done, created_at FROM tasks WHERE user_id = $1
	`, userID)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Name, &t.Done, &t.CreatedAt); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		tasks = append(tasks, t)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

func postTask(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(int)
	if !ok {
		http.Error(w, "пользователь не авторизован", http.StatusUnauthorized)
		return
	}

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
	err := db.QueryRow(`
	INSERT INTO tasks (name, user_id) VALUES ($1, $2) RETURNING id, name, done, created_at
	`, createT.Name, userID).Scan(&t.ID, &t.Name, &t.Done, &t.CreatedAt)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(t)
}

func updateTask(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(int)
	if !ok {
		http.Error(w, "пользователь не авторизован", http.StatusUnauthorized)
		return
	}

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
	UPDATE tasks SET done = $1 WHERE id = $2 AND user_id = $3 RETURNING id, name, done, created_at
	`, input.Done, id, userID).Scan(&t.ID, &t.Name, &t.Done, &t.CreatedAt)
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

func deleteTask(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("userID").(int)
	if !ok {
		http.Error(w, "пользователь не авторизован", http.StatusUnauthorized)
		return
	}

	strid := chi.URLParam(r, "id")
	id, err := strconv.Atoi(strid)

	if err != nil {
		http.Error(w, "неверный ID задачи", http.StatusBadRequest)
	}

	_, err = db.Exec(`
	DELETE FROM tasks WHERE id = $1 AND user_id = $2
	`, id, userID)
	if err != nil {
		http.Error(w, "ошибка", 500)
		return
	}

	w.WriteHeader(http.StatusNoContent)

}
