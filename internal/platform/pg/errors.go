// Package pg provides pgx pool construction, a transaction helper with retry
// on serialization/deadlock failures, and SQLSTATE classification helpers.
package pg

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	codeUniqueViolation      = "23505"
	codeCheckViolation       = "23514"
	codeInvalidText          = "22P02"
	codeSerializationFailure = "40001"
	codeDeadlockDetected     = "40P01"
)

func pgErr(err error) *pgconn.PgError {
	var e *pgconn.PgError
	if errors.As(err, &e) {
		return e
	}
	return nil
}

// UniqueViolation reports whether err is a unique violation and returns the
// violated constraint name.
func UniqueViolation(err error) (constraint string, ok bool) {
	if e := pgErr(err); e != nil && e.Code == codeUniqueViolation {
		return e.ConstraintName, true
	}
	return "", false
}

// CheckViolation reports whether err is a CHECK constraint violation and
// returns the constraint name.
func CheckViolation(err error) (constraint string, ok bool) {
	if e := pgErr(err); e != nil && e.Code == codeCheckViolation {
		return e.ConstraintName, true
	}
	return "", false
}

// Retryable reports whether the whole transaction may safely be re-run.
func Retryable(err error) bool {
	e := pgErr(err)
	return e != nil && (e.Code == codeSerializationFailure || e.Code == codeDeadlockDetected)
}

// InvalidText reports whether err is "invalid input syntax" (for example a
// malformed uuid passed as an id).
func InvalidText(err error) bool {
	e := pgErr(err)
	return e != nil && e.Code == codeInvalidText
}
