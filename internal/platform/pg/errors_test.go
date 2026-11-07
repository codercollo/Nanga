package pg

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassification(t *testing.T) {
	uniq := fmt.Errorf("wrap: %w", &pgconn.PgError{Code: "23505", ConstraintName: "journals_idempotency_key_key"})
	if c, ok := UniqueViolation(uniq); !ok || c != "journals_idempotency_key_key" {
		t.Fatalf("unique: %q %v", c, ok)
	}
	chk := &pgconn.PgError{Code: "23514", ConstraintName: "accounts_non_negative"}
	if c, ok := CheckViolation(chk); !ok || c != "accounts_non_negative" {
		t.Fatalf("check: %q %v", c, ok)
	}
	for _, code := range []string{"40001", "40P01"} {
		if !Retryable(&pgconn.PgError{Code: code}) {
			t.Errorf("%s should be retryable", code)
		}
	}
	if Retryable(uniq) || Retryable(errors.New("x")) {
		t.Fatal("non-retryable misclassified")
	}
	if !InvalidText(&pgconn.PgError{Code: "22P02"}) || InvalidText(errors.New("x")) {
		t.Fatal("InvalidText misclassified")
	}
	if _, ok := UniqueViolation(errors.New("x")); ok {
		t.Fatal("plain error is not unique violation")
	}
}
