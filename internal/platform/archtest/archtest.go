// Package archtest enforces the import boundaries from project-structure.md
// §3 as an ordinary Go test, so they are checked by `make test` even where
// golangci-lint (depguard) is not run. Rules:
//
//  1. domain imports only the standard library, platform/money, or its own domain packages.
//  2. app never imports its adapters, gen, or drivers (pgx, redis, amqp, gin, grpc).
//  3. a module never imports another module's packages.
//  4. platform never imports business modules.
//  5. nothing under internal/ imports cmd/.
//
// Test files (*_test.go) are exempt: they may wire adapters to use cases.
package archtest

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Modules are the business modules/deployables under internal/.
var Modules = []string{"gateway", "identity", "ledger", "payments", "mpesa", "worker"}

var drivers = []string{
	"github.com/jackc/pgx", "github.com/redis/go-redis", "github.com/go-redis/redis",
	"github.com/rabbitmq/amqp091-go", "github.com/streadway/amqp",
	"github.com/gin-gonic/gin", "google.golang.org/grpc",
}

// Violation is one broken rule.
type Violation struct {
	File   string
	Import string
	Rule   string
}

func (v Violation) String() string { return fmt.Sprintf("%s imports %s: %s", v.File, v.Import, v.Rule) }

func isModule(s string) bool {
	for _, m := range Modules {
		if m == s {
			return true
		}
	}
	return false
}

// ModulePath reads the module path from root/go.mod.
func ModulePath(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("archtest: no module line in %s/go.mod", root)
}

// Check scans root/internal and returns every violation.
func Check(root string) ([]Violation, error) {
	mod, err := ModulePath(root)
	if err != nil {
		return nil, err
	}
	internal := filepath.Join(root, "internal")
	var out []Violation
	err = filepath.WalkDir(internal, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(internal, path)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		f, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		for _, imp := range f.Imports {
			ip, _ := strconv.Unquote(imp.Path.Value)
			if rule := violates(mod, parts, ip); rule != "" {
				out = append(out, Violation{File: "internal/" + filepath.ToSlash(rel), Import: ip, Rule: rule})
			}
		}
		return nil
	})
	return out, err
}

func violates(mod string, parts []string, imp string) string {
	top := parts[0]
	internalPrefix := mod + "/internal/"
	if strings.HasPrefix(imp, mod+"/cmd/") {
		return "internal code must not import cmd (rule 5)"
	}
	if top == "platform" {
		if rest, ok := strings.CutPrefix(imp, internalPrefix); ok && isModule(strings.SplitN(rest, "/", 2)[0]) {
			return "platform must not import business modules (rule 4)"
		}
		return ""
	}
	if !isModule(top) {
		return ""
	}
	if rest, ok := strings.CutPrefix(imp, internalPrefix); ok {
		if other := strings.SplitN(rest, "/", 2)[0]; isModule(other) && other != top {
			return "module must not import another module (rule 3)"
		}
	}
	if len(parts) < 2 {
		return ""
	}
	switch parts[1] {
	case "domain":
		isStd := !strings.Contains(strings.SplitN(imp, "/", 2)[0], ".")
		own := mod + "/internal/" + top + "/domain"
		if !(isStd || imp == mod+"/internal/platform/money" || imp == own || strings.HasPrefix(imp, own+"/")) {
			return "domain may import only stdlib, platform/money, own domain (rule 1)"
		}
	case "app":
		if imp == mod+"/internal/"+top+"/adapter" || strings.HasPrefix(imp, mod+"/internal/"+top+"/adapter/") || strings.HasPrefix(imp, mod+"/gen") {
			return "app must not import adapter or gen (rule 2)"
		}
		for _, d := range drivers {
			if strings.HasPrefix(imp, d) {
				return "app must not import drivers (rule 2)"
			}
		}
	}
	return ""
}
