package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// The real repository must satisfy every rule.
func TestRepositoryRespectsBoundaries(t *testing.T) {
	vs, err := Check(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		t.Error(v)
	}
}

func write(t *testing.T, root, rel, src string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The checker itself must detect each kind of violation.
func TestCheckerDetectsViolations(t *testing.T) {
	root := t.TempDir()
	const mod = "example.com/x"
	write(t, root, "go.mod", "module "+mod+"\n\ngo 1.22\n")
	write(t, root, "internal/ledger/domain/a.go", `package domain
import ("fmt"; _ "github.com/jackc/pgx/v5"; _ "`+mod+`/internal/platform/money"; _ "`+mod+`/internal/platform/obs")
var _ = fmt.Sprint`)
	write(t, root, "internal/ledger/app/b.go", `package app
import (_ "`+mod+`/internal/ledger/adapter/postgres"; _ "github.com/jackc/pgx/v5/pgxpool"; _ "`+mod+`/gen/ledger")`)
	write(t, root, "internal/ledger/adapter/c.go", `package adapter
import _ "`+mod+`/internal/payments/domain"`)
	write(t, root, "internal/platform/money/d.go", `package money
import _ "`+mod+`/internal/ledger/domain"`)
	write(t, root, "internal/identity/app/e.go", `package app
import _ "`+mod+`/cmd/identity"`)
	// exempt: test files may wire adapters
	write(t, root, "internal/ledger/app/b_test.go", `package app_test
import _ "`+mod+`/internal/ledger/adapter/postgres"`)
	// clean files produce nothing
	write(t, root, "internal/ledger/domain/ok.go", `package domain
import _ "`+mod+`/internal/platform/money"`)

	vs, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, v := range vs {
		got[v.File+"|"+v.Import] = true
	}
	want := []string{
		"internal/ledger/domain/a.go|github.com/jackc/pgx/v5",
		"internal/ledger/domain/a.go|" + mod + "/internal/platform/obs",
		"internal/ledger/app/b.go|" + mod + "/internal/ledger/adapter/postgres",
		"internal/ledger/app/b.go|github.com/jackc/pgx/v5/pgxpool",
		"internal/ledger/app/b.go|" + mod + "/gen/ledger",
		"internal/ledger/adapter/c.go|" + mod + "/internal/payments/domain",
		"internal/platform/money/d.go|" + mod + "/internal/ledger/domain",
		"internal/identity/app/e.go|" + mod + "/cmd/identity",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missed violation: %s", w)
		}
	}
	if len(vs) != len(want) {
		var b strings.Builder
		for _, v := range vs {
			b.WriteString("\n  " + v.String())
		}
		t.Errorf("want %d violations, got %d:%s", len(want), len(vs), b.String())
	}
}
