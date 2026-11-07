// Package config loads and validates environment configuration. All problems
// are collected so startup fails once with a complete list.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Loader reads env vars and accumulates validation errors.
type Loader struct{ errs []error }

func (l *Loader) fail(format string, a ...any) { l.errs = append(l.errs, fmt.Errorf(format, a...)) }

// Err returns all accumulated errors, or nil.
func (l *Loader) Err() error { return errors.Join(l.errs...) }

// Str returns the variable or def when unset/empty.
func (l *Loader) Str(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Required returns the variable and records an error when unset.
func (l *Loader) Required(key string) string {
	v := os.Getenv(key)
	if v == "" {
		l.fail("%s is required", key)
	}
	return v
}

// Int parses an integer variable.
func (l *Loader) Int(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.fail("%s: %q is not an integer", key, v)
		return def
	}
	return n
}

// Duration parses a Go duration variable such as "5s".
func (l *Loader) Duration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.fail("%s: %q is not a duration", key, v)
		return def
	}
	return d
}

// OneOf returns the variable, requiring it to be one of allowed.
func (l *Loader) OneOf(key, def string, allowed ...string) string {
	v := l.Str(key, def)
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	l.fail("%s: %q must be one of %s", key, v, strings.Join(allowed, "|"))
	return def
}

// Base is configuration shared by every deployable.
type Base struct {
	Service   string
	Env       string // dev | staging | production
	LogLevel  string // debug | info | warn | error
	LogFormat string // json | text
	HTTPAddr  string
}

// LoadBase reads ENV, LOG_LEVEL, LOG_FORMAT, HTTP_ADDR.
func LoadBase(l *Loader, service, defaultAddr string) Base {
	return Base{
		Service:   service,
		Env:       l.OneOf("ENV", "dev", "dev", "staging", "production"),
		LogLevel:  l.OneOf("LOG_LEVEL", "info", "debug", "info", "warn", "error"),
		LogFormat: l.OneOf("LOG_FORMAT", "json", "json", "text"),
		HTTPAddr:  l.Str("HTTP_ADDR", defaultAddr),
	}
}

// Postgres is connection configuration for a service's database.
type Postgres struct {
	DSN      string
	MaxConns int
}

// LoadPostgres reads PG_DSN (required) and PG_MAX_CONNS.
func LoadPostgres(l *Loader) Postgres {
	p := Postgres{DSN: l.Required("PG_DSN"), MaxConns: l.Int("PG_MAX_CONNS", 10)}
	if p.MaxConns < 1 {
		l.fail("PG_MAX_CONNS must be >= 1")
	}
	return p
}
