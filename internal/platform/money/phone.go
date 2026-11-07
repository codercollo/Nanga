package money

import (
	"errors"
	"strings"
)

// ErrInvalidPhone is returned for numbers that are not Kenyan mobile numbers.
var ErrInvalidPhone = errors.New("money: invalid Kenyan mobile number")

// NormalizeKE converts common Kenyan mobile formats (0712345678, 712345678,
// 254712345678, +254 712 345 678, 00254712345678) to E.164 without symbols
// (254712345678), the form used for lookups, indexing and Daraja.
// Mobile numbers are 9 national digits starting with 7 or 1.
func NormalizeKE(s string) (string, error) {
	var b strings.Builder
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' && i == 0:
		case r == ' ' || r == '-' || r == '(' || r == ')':
		default:
			return "", ErrInvalidPhone
		}
	}
	d := b.String()
	switch {
	case strings.HasPrefix(d, "00254"):
		d = d[5:]
	case strings.HasPrefix(d, "254"):
		d = d[3:]
	case strings.HasPrefix(d, "0"):
		d = d[1:]
	}
	if len(d) != 9 || (d[0] != '7' && d[0] != '1') {
		return "", ErrInvalidPhone
	}
	return "254" + d, nil
}

// Mask hides the middle of a normalised number for logs and UI
// (254712345678 -> 2547****678).
func Mask(msisdn string) string {
	if len(msisdn) < 8 {
		return "****"
	}
	return msisdn[:4] + "****" + msisdn[len(msisdn)-3:]
}
