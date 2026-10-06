// Package config собирает настройки сервиса из переменных окружения.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTP     HTTP
	Postgres Postgres
	Auth     Auth
	Swagger  Swagger
}

type Swagger struct {
	Enabled bool // SWAGGER_ENABLED — отдавать /swagger/ и /swagger/openapi.yaml
}

type HTTP struct {
	Addr              string        // HTTP_ADDR
	ReadHeaderTimeout time.Duration // HTTP_READ_HEADER_TIMEOUT
	ReadTimeout       time.Duration // HTTP_READ_TIMEOUT
	WriteTimeout      time.Duration // HTTP_WRITE_TIMEOUT
	IdleTimeout       time.Duration // HTTP_IDLE_TIMEOUT
	ShutdownTimeout   time.Duration // HTTP_SHUTDOWN_TIMEOUT
	MaxBodyBytes      int64         // HTTP_MAX_BODY_BYTES
}

type Postgres struct {
	DSN             string        // DATABASE_URL
	ConnectTimeout  time.Duration // DB_CONNECT_TIMEOUT — сколько ждать готовности БД при старте
	MaxOpenConns    int           // DB_MAX_OPEN_CONNS
	MaxIdleConns    int           // DB_MAX_IDLE_CONNS
	ConnMaxLifetime time.Duration // DB_CONN_MAX_LIFETIME
}

type Auth struct {
	SessionTTL      time.Duration // AUTH_SESSION_TTL
	MaxFailedLogins int           // AUTH_MAX_FAILED_LOGINS (SR-10)
	LockoutDuration time.Duration // AUTH_LOCKOUT_DURATION (SR-10)
}

// Load читает переменные окружения; для незаданных берутся значения по умолчанию.
func Load() (Config, error) {
	l := loader{}
	cfg := Config{
		HTTP: HTTP{
			Addr:              l.str("HTTP_ADDR", ":8080"),
			ReadHeaderTimeout: l.dur("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
			ReadTimeout:       l.dur("HTTP_READ_TIMEOUT", 10*time.Second),
			WriteTimeout:      l.dur("HTTP_WRITE_TIMEOUT", 15*time.Second),
			IdleTimeout:       l.dur("HTTP_IDLE_TIMEOUT", 60*time.Second),
			ShutdownTimeout:   l.dur("HTTP_SHUTDOWN_TIMEOUT", 10*time.Second),
			MaxBodyBytes:      int64(l.int("HTTP_MAX_BODY_BYTES", 1<<20)),
		},
		Postgres: Postgres{
			DSN:             l.str("DATABASE_URL", "postgres://eventflow:eventflow@localhost:5432/eventflow?sslmode=disable"),
			ConnectTimeout:  l.dur("DB_CONNECT_TIMEOUT", 30*time.Second),
			MaxOpenConns:    l.int("DB_MAX_OPEN_CONNS", 20),
			MaxIdleConns:    l.int("DB_MAX_IDLE_CONNS", 5),
			ConnMaxLifetime: l.dur("DB_CONN_MAX_LIFETIME", 30*time.Minute),
		},
		Auth: Auth{
			SessionTTL:      l.dur("AUTH_SESSION_TTL", 24*time.Hour),
			MaxFailedLogins: l.int("AUTH_MAX_FAILED_LOGINS", 5),
			LockoutDuration: l.dur("AUTH_LOCKOUT_DURATION", 15*time.Minute),
		},
		Swagger: Swagger{
			Enabled: l.bool("SWAGGER_ENABLED", true),
		},
	}
	if err := errors.Join(l.errs...); err != nil {
		return Config{}, err
	}
	return cfg, cfg.validate()
}

func (c Config) validate() error {
	var errs []error
	if c.HTTP.Addr == "" {
		errs = append(errs, errors.New("HTTP_ADDR must not be empty"))
	}
	if c.HTTP.MaxBodyBytes <= 0 {
		errs = append(errs, errors.New("HTTP_MAX_BODY_BYTES must be positive"))
	}
	if c.Postgres.DSN == "" {
		errs = append(errs, errors.New("DATABASE_URL must not be empty"))
	}
	if c.Postgres.MaxOpenConns <= 0 {
		errs = append(errs, errors.New("DB_MAX_OPEN_CONNS must be positive"))
	}
	if c.Auth.SessionTTL <= 0 {
		errs = append(errs, errors.New("AUTH_SESSION_TTL must be positive"))
	}
	if c.Auth.MaxFailedLogins <= 0 {
		errs = append(errs, errors.New("AUTH_MAX_FAILED_LOGINS must be positive"))
	}
	if c.Auth.LockoutDuration <= 0 {
		errs = append(errs, errors.New("AUTH_LOCKOUT_DURATION must be positive"))
	}
	return errors.Join(errs...)
}

type loader struct{ errs []error }

func (l *loader) str(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func (l *loader) int(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: %w", key, err))
		return def
	}
	return n
}

func (l *loader) dur(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: %w", key, err))
		return def
	}
	return d
}

func (l *loader) bool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: %w", key, err))
		return def
	}
	return b
}
