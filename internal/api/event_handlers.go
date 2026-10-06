package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// eventResponse — публичное представление мероприятия: из данных организатора только имя (SR-11).
type eventResponse struct {
	ID            int64     `json:"id"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	StartsAt      time.Time `json:"starts_at"`
	Capacity      int       `json:"capacity"`
	FreeSeats     int       `json:"free_seats"`
	Status        string    `json:"status"`
	OrganizerName string    `json:"organizer_name"`
}

const eventSelect = `
	SELECT e.id, e.title, e.description, e.starts_at, e.capacity, e.free_seats, e.status, p.name
	FROM events e JOIN persons p ON p.id = e.organizer_id`

func scanEvent(row interface{ Scan(...any) error }) (eventResponse, error) {
	var e eventResponse
	err := row.Scan(&e.ID, &e.Title, &e.Description, &e.StartsAt, &e.Capacity, &e.FreeSeats, &e.Status, &e.OrganizerName)
	return e, err
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(),
		eventSelect+` WHERE e.status = 'published' ORDER BY e.starts_at, e.id`)
	if err != nil {
		internalError(w, err)
		return
	}
	defer rows.Close()

	events := []eventResponse{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			internalError(w, err)
			return
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	e, err := scanEvent(s.db.QueryRowContext(r.Context(), eventSelect+` WHERE e.id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

type createEventRequest struct {
	Title       string    `json:"title"`
	Description string    `json:"description"`
	StartsAt    time.Time `json:"starts_at"`
	Capacity    int       `json:"capacity"`
}

func (s *Server) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	var req createEventRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}
	req.Title = strings.TrimSpace(req.Title)

	switch {
	case req.Title == "" || utf8.RuneCountInString(req.Title) > 200:
		writeError(w, http.StatusBadRequest, "title must be 1..200 characters")
		return
	case utf8.RuneCountInString(req.Description) > 5000:
		writeError(w, http.StatusBadRequest, "description must be at most 5000 characters")
		return
	case req.StartsAt.IsZero() || req.StartsAt.Before(time.Now()):
		writeError(w, http.StatusBadRequest, "starts_at must be a future RFC 3339 time")
		return
	case req.Capacity < 1 || req.Capacity > 100_000:
		writeError(w, http.StatusBadRequest, "capacity must be 1..100000")
		return
	}

	// Организатор — всегда текущий пользователь (D-01).
	var id int64
	err := s.db.QueryRowContext(r.Context(), `
		INSERT INTO events (organizer_id, title, description, starts_at, capacity, free_seats)
		VALUES ($1, $2, $3, $4, $5, $5) RETURNING id`,
		userFrom(r).ID, req.Title, req.Description, req.StartsAt, req.Capacity,
	).Scan(&id)
	if err != nil {
		internalError(w, err)
		return
	}

	e, err := scanEvent(s.db.QueryRowContext(r.Context(), eventSelect+` WHERE e.id = $1`, id))
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

type participantResponse struct {
	Name         string    `json:"name"`
	Contacts     string    `json:"contacts"`
	RegisteredAt time.Time `json:"registered_at"`
}

// handleParticipants отдаёт список только организатору этого мероприятия (D-02, SR-09).
func (s *Server) handleParticipants(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	var exists bool
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT EXISTS (SELECT 1 FROM events WHERE id = $1 AND organizer_id = $2)`,
		id, userFrom(r).ID,
	).Scan(&exists); err != nil {
		internalError(w, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT p.name, p.contacts, reg.created_at
		FROM registrations reg JOIN persons p ON p.id = reg.person_id
		WHERE reg.event_id = $1 AND reg.status = 'active'
		ORDER BY reg.created_at, reg.id`, id)
	if err != nil {
		internalError(w, err)
		return
	}
	defer rows.Close()

	list := []participantResponse{}
	for rows.Next() {
		var p participantResponse
		if err := rows.Scan(&p.Name, &p.Contacts, &p.RegisteredAt); err != nil {
			internalError(w, err)
			return
		}
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
