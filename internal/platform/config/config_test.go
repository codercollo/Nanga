package config

import (
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	var l Loader
	b := LoadBase(&l, "ledger", ":8081")
	if err := l.Err(); err != nil {
		t.Fatal(err)
	}
	if b.Env != "dev" || b.LogFormat != "json" || b.HTTPAddr != ":8081" || b.Service != "ledger" {
		t.Fatalf("bad defaults %+v", b)
	}
}

func TestOverridesAndPostgres(t *testing.T) {
	t.Setenv("ENV", "production")
	t.Setenv("PG_DSN", "postgres://x")
	t.Setenv("PG_MAX_CONNS", "25")
	var l Loader
	b := LoadBase(&l, "ledger", ":1")
	p := LoadPostgres(&l)
	if l.Err() != nil || b.Env != "production" || p.DSN != "postgres://x" || p.MaxConns != 25 {
		t.Fatalf("%v %+v %+v", l.Err(), b, p)
	}
}

func TestCollectsAllErrors(t *testing.T) {
	t.Setenv("ENV", "bogus")
	t.Setenv("PG_DSN", "")
	t.Setenv("PG_MAX_CONNS", "abc")
	t.Setenv("T_DUR", "nope")
	var l Loader
	LoadBase(&l, "x", ":1")
	LoadPostgres(&l)
	l.Duration("T_DUR", time.Second)
	msg := l.Err().Error()
	for _, want := range []string{"ENV", "PG_DSN is required", "PG_MAX_CONNS", "T_DUR"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in %q", want, msg)
		}
	}
}

func TestDuration(t *testing.T) {
	t.Setenv("T_DUR", "1500ms")
	var l Loader
	if d := l.Duration("T_DUR", 0); d != 1500*time.Millisecond || l.Err() != nil {
		t.Fatalf("%v %v", d, l.Err())
	}
}
