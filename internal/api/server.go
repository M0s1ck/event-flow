// Package api — HTTP-обработчики event-flow.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/M0s1ck/event-flow/internal/auth"
	"github.com/M0s1ck/event-flow/internal/config"
)

const (
	RoleOrganizer   = "organizer"
	RoleParticipant = "participant"
)

type Server struct {
	db     *sql.DB
	cfg    config.Config
	router chi.Router
}

func NewServer(db *sql.DB, cfg config.Config) *Server {
	s := &Server{db: db, cfg: cfg}
	s.router = s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Server) routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/health", s.handleHealth)

	r.Route("/auth", func(r chi.Router) {
		r.Post("/register", s.handleRegister)
		r.Post("/login", s.handleLogin)
		r.With(s.requireUser).Post("/logout", s.handleLogout)
	})

	r.Route("/me", func(r chi.Router) {
		r.Use(s.requireUser)
		r.Get("/", s.handleGetMe)
		r.Patch("/", s.handlePatchMe)
	})

	r.Route("/events", func(r chi.Router) {
		r.Get("/", s.handleListEvents)
		r.Get("/{id}", s.handleGetEvent)
		r.With(s.requireUser, requireRole(RoleOrganizer)).Post("/", s.handleCreateEvent)
		r.With(s.requireUser, requireRole(RoleOrganizer)).Get("/{id}/participants", s.handleParticipants)
		r.With(s.requireUser, requireRole(RoleParticipant)).Post("/{id}/registrations", s.handleRegisterForEvent)
	})

	r.Route("/registrations", func(r chi.Router) {
		r.Use(s.requireUser)
		r.Get("/my", s.handleMyRegistrations)
		r.Delete("/{id}", s.handleCancelRegistration)
	})

	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.db.PingContext(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "error", "db": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "db": "ok"})
}

// --- текущий пользователь (D-01) ---

type user struct {
	ID   int64
	Role string
}

type ctxKey struct{}

// userFrom — единственный способ узнать, кто делает запрос.
func userFrom(r *http.Request) user {
	return r.Context().Value(ctxKey{}).(user)
}

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := auth.BearerToken(r.Header.Get("Authorization"))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		var u user
		err = s.db.QueryRowContext(r.Context(), `
			SELECT p.id, p.role
			FROM sessions s JOIN persons p ON p.id = s.person_id
			WHERE s.token_hash = $1 AND s.expires_at > now()`,
			auth.HashToken(token),
		).Scan(&u.ID, &u.Role)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if err != nil {
			internalError(w, err)
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}

// requireRole ставится после requireUser.
func requireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if userFrom(r).Role != role {
				writeError(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// --- общие помощники ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func internalError(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

// decodeJSON отклоняет неизвестные поля (SR-08) и слишком большие тела.
func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.HTTP.MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}
