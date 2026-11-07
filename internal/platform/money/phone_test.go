package money

import "testing"

func TestNormalizeKE(t *testing.T) {
	good := []string{"0712345678", "712345678", "254712345678", "+254712345678", "+254 712-345-678", "00254712345678"}
	for _, in := range good {
		got, err := NormalizeKE(in)
		if err != nil || got != "254712345678" {
			t.Errorf("%q: got %q %v", in, got, err)
		}
	}
	if got, err := NormalizeKE("0112345678"); err != nil || got != "254112345678" {
		t.Errorf("01xx: %q %v", got, err)
	}
	for _, in := range []string{"", "071234567", "07123456789", "0212345678", "+255712345678", "abc", "0712 345 67x", "7+12345678"} {
		if _, err := NormalizeKE(in); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
}

func TestMask(t *testing.T) {
	if got := Mask("254712345678"); got != "2547****678" {
		t.Fatalf("got %q", got)
	}
	if Mask("123") != "****" {
		t.Fatal("short input must be fully masked")
	}
}
