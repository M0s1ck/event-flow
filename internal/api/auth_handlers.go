package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lib/pq"

	"github.com/M0s1ck/event-flow/internal/auth"
)

type registerRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
	Name     string `json:"name"`
	Contacts string `json:"contacts"`
	Role     string `json:"role"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}
	req.Login = strings.TrimSpace(req.Login)
	req.Name = strings.TrimSpace(req.Name)

	switch {
	case utf8.RuneCountInString(req.Login) < 3 || utf8.RuneCountInString(req.Login) > 64:
		writeError(w, http.StatusBadRequest, "login must be 3..64 characters")
		return
	case utf8.RuneCountInString(req.Password) < 8 || len(req.Password) > 128:
		writeError(w, http.StatusBadRequest, "password must be 8..128 characters")
		return
	case req.Name == "" || utf8.RuneCountInString(req.Name) > 100:
		writeError(w, http.StatusBadRequest, "name must be 1..100 characters")
		return
	case utf8.RuneCountInString(req.Contacts) > 200:
		writeError(w, http.StatusBadRequest, "contacts must be at most 200 characters")
		return
	case req.Role != RoleOrganizer && req.Role != RoleParticipant:
		writeError(w, http.StatusBadRequest, "role must be organizer or participant")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		internalError(w, err)
		return
	}

	var id int64
	err = s.db.QueryRowContext(r.Context(), `
		INSERT INTO persons (login, password_hash, name, contacts, role)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		req.Login, hash, req.Name, req.Contacts, req.Role,
	).Scan(&id)
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "login is already taken")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, meResponse{ID: id, Login: req.Login, Name: req.Name, Contacts: req.Contacts, Role: req.Role})
}

type loginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()

	var (
		id          int64
		hash        string
		lockedUntil sql.NullTime
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, password_hash, locked_until FROM persons WHERE login = $1`,
		strings.TrimSpace(req.Login),
	).Scan(&id, &hash, &lockedUntil)
	if errors.Is(err, sql.ErrNoRows) {
		auth.BurnTime(req.Password)
		writeError(w, http.StatusUnauthorized, "invalid login or password")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}

	// SR-10: аккаунт временно заблокирован после серии неверных паролей.
	if lockedUntil.Valid && lockedUntil.Time.After(time.Now()) {
		writeError(w, http.StatusTooManyRequests, "too many failed attempts, try later")
		return
	}

	if !auth.CheckPassword(req.Password, hash) {
		_, err := s.db.ExecContext(ctx, `
			UPDATE persons SET
				failed_logins = CASE WHEN failed_logins + 1 >= $2 THEN 0 ELSE failed_logins + 1 END,
				locked_until  = CASE WHEN failed_logins + 1 >= $2 THEN now() + $3::interval ELSE locked_until END
			WHERE id = $1`,
			id, s.cfg.Auth.MaxFailedLogins, s.cfg.Auth.LockoutDuration.String(),
		)
		if err != nil {
			internalError(w, err)
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid login or password")
		return
	}

	token, tokenHash, err := auth.NewToken()
	if err != nil {
		internalError(w, err)
		return
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		internalError(w, err)
		return
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE persons SET failed_logins = 0, locked_until = NULL WHERE id = $1`, id); err != nil {
		internalError(w, err)
		return
	}
	expiresAt := time.Now().Add(s.cfg.Auth.SessionTTL)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, person_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, id, expiresAt); err != nil {
		internalError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		internalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expires_at": expiresAt.UTC()})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	token, _ := auth.BearerToken(r.Header.Get("Authorization"))
	if _, err := s.db.ExecContext(r.Context(),
		`DELETE FROM sessions WHERE token_hash = $1`, auth.HashToken(token)); err != nil {
		internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type meResponse struct {
	ID       int64  `json:"id"`
	Login    string `json:"login"`
	Name     string `json:"name"`
	Contacts string `json:"contacts"`
	Role     string `json:"role"`
}

func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	var me meResponse
	err := s.db.QueryRowContext(r.Context(),
		`SELECT id, login, name, contacts, role FROM persons WHERE id = $1`, userFrom(r).ID,
	).Scan(&me.ID, &me.Login, &me.Name, &me.Contacts, &me.Role)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, me)
}

// patchMeRequest намеренно содержит только name и contacts (SR-08).
type patchMeRequest struct {
	Name     *string `json:"name"`
	Contacts *string `json:"contacts"`
}

func (s *Server) handlePatchMe(w http.ResponseWriter, r *http.Request) {
	var req patchMeRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if n == "" || utf8.RuneCountInString(n) > 100 {
			writeError(w, http.StatusBadRequest, "name must be 1..100 characters")
			return
		}
		req.Name = &n
	}
	if req.Contacts != nil && utf8.RuneCountInString(*req.Contacts) > 200 {
		writeError(w, http.StatusBadRequest, "contacts must be at most 200 characters")
		return
	}

	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE persons SET
			name     = COALESCE($2, name),
			contacts = COALESCE($3, contacts)
		WHERE id = $1`,
		userFrom(r).ID, req.Name, req.Contacts,
	); err != nil {
		internalError(w, err)
		return
	}
	s.handleGetMe(w, r)
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}
