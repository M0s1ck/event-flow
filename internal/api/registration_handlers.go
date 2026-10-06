package api

import (
	"database/sql"
	"errors"
	"net/http"
	"time"
)

type registrationResponse struct {
	ID            int64     `json:"id"`
	EventID       int64     `json:"event_id"`
	EventTitle    string    `json:"event_title"`
	EventStartsAt time.Time `json:"event_starts_at"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

// handleRegisterForEvent — атомарное занятие места (D-03). Тело запроса не читается (D-01).
func (s *Server) handleRegisterForEvent(w http.ResponseWriter, r *http.Request) {
	eventID, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	ctx := r.Context()
	personID := userFrom(r).ID

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		internalError(w, err)
		return
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE events SET free_seats = free_seats - 1
		WHERE id = $1 AND status = 'published' AND free_seats > 0`, eventID)
	if err != nil {
		internalError(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var published bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM events WHERE id = $1 AND status = 'published')`, eventID,
		).Scan(&published); err != nil {
			internalError(w, err)
			return
		}
		if published {
			writeError(w, http.StatusConflict, "no free seats")
		} else {
			writeError(w, http.StatusNotFound, "not found")
		}
		return
	}

	var reg registrationResponse
	err = tx.QueryRowContext(ctx, `
		INSERT INTO registrations (event_id, person_id, status)
		VALUES ($1, $2, 'active')
		RETURNING id, event_id, status, created_at`,
		eventID, personID,
	).Scan(&reg.ID, &reg.EventID, &reg.Status, &reg.CreatedAt)
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "already registered")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}

	if err := tx.QueryRowContext(ctx,
		`SELECT title, starts_at FROM events WHERE id = $1`, eventID,
	).Scan(&reg.EventTitle, &reg.EventStartsAt); err != nil {
		internalError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, reg)
}

func (s *Server) handleMyRegistrations(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT reg.id, reg.event_id, e.title, e.starts_at, reg.status, reg.created_at
		FROM registrations reg JOIN events e ON e.id = reg.event_id
		WHERE reg.person_id = $1
		ORDER BY reg.created_at DESC, reg.id DESC`, userFrom(r).ID)
	if err != nil {
		internalError(w, err)
		return
	}
	defer rows.Close()

	list := []registrationResponse{}
	for rows.Next() {
		var reg registrationResponse
		if err := rows.Scan(&reg.ID, &reg.EventID, &reg.EventTitle, &reg.EventStartsAt, &reg.Status, &reg.CreatedAt); err != nil {
			internalError(w, err)
			return
		}
		list = append(list, reg)
	}
	if err := rows.Err(); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleCancelRegistration: владелец и статус проверяются в том же UPDATE (D-02),
// место возвращается только если запись действительно была отменена сейчас (D-03).
func (s *Server) handleCancelRegistration(w http.ResponseWriter, r *http.Request) {
	regID, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	ctx := r.Context()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		internalError(w, err)
		return
	}
	defer tx.Rollback()

	var eventID int64
	err = tx.QueryRowContext(ctx, `
		UPDATE registrations SET status = 'cancelled', cancelled_at = now()
		WHERE id = $1 AND person_id = $2 AND status = 'active'
		RETURNING event_id`,
		regID, userFrom(r).ID,
	).Scan(&eventID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE events SET free_seats = free_seats + 1 WHERE id = $1`, eventID); err != nil {
		internalError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
