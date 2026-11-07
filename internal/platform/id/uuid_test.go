package id

import (
	"regexp"
	"testing"
	"time"
)

var re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestFormat(t *testing.T) {
	for i := 0; i < 100; i++ {
		if s := NewV7(); !re.MatchString(s) {
			t.Fatalf("bad uuid %q", s)
		}
	}
}

func TestUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 20000; i++ {
		s := NewV7()
		if seen[s] {
			t.Fatalf("duplicate %s", s)
		}
		seen[s] = true
	}
}

func TestTimeOrdered(t *testing.T) {
	base := time.Now()
	a, b := NewV7At(base), NewV7At(base.Add(5*time.Millisecond))
	if !(a < b) {
		t.Fatalf("not ordered: %s %s", a, b)
	}
}
