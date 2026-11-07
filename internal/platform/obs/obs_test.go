package obs

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoggerFields(t *testing.T) {
	var buf bytes.Buffer
	NewLogger(&buf, "info", "json", "ledger", "dev").Info("hi")
	out := buf.String()
	if !strings.Contains(out, `"service":"ledger"`) || !strings.Contains(out, `"env":"dev"`) {
		t.Fatalf("missing fields: %s", out)
	}
}

func TestLoggerLevel(t *testing.T) {
	var buf bytes.Buffer
	NewLogger(&buf, "warn", "text", "s", "e").Info("hidden")
	if buf.Len() != 0 {
		t.Fatalf("info should be filtered: %s", buf.String())
	}
}

func get(h http.Handler, path string) int {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec.Code
}

func TestHealth(t *testing.T) {
	ok := Check{"db", func(context.Context) error { return nil }}
	bad := Check{"redis", func(context.Context) error { return errors.New("down") }}
	if c := get(HealthMux(bad), "/healthz"); c != 200 {
		t.Fatalf("healthz %d", c)
	}
	if c := get(HealthMux(ok), "/readyz"); c != 200 {
		t.Fatalf("readyz ok %d", c)
	}
	if c := get(HealthMux(ok, bad), "/readyz"); c != 503 {
		t.Fatalf("readyz bad %d", c)
	}
}
