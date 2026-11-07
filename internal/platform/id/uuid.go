// Package id generates time-ordered UUIDv7 identifiers.
package id

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// NewV7 returns a UUIDv7 string for the current time.
func NewV7() string { return NewV7At(time.Now()) }

// NewV7At returns a UUIDv7 string for the given time.
func NewV7At(t time.Time) string {
	var u [16]byte
	ms := uint64(t.UnixMilli())
	u[0], u[1], u[2] = byte(ms>>40), byte(ms>>32), byte(ms>>24)
	u[3], u[4], u[5] = byte(ms>>16), byte(ms>>8), byte(ms)
	if _, err := rand.Read(u[6:]); err != nil {
		panic("id: crypto/rand failed: " + err.Error())
	}
	u[6] = (u[6] & 0x0f) | 0x70
	u[8] = (u[8] & 0x3f) | 0x80
	var out [36]byte
	hex.Encode(out[0:8], u[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], u[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], u[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], u[8:10])
	out[23] = '-'
	hex.Encode(out[24:], u[10:16])
	return string(out[:])
}

// Generator implements the ledger's IDGenerator port.
type Generator struct{}

// NewID returns a new UUIDv7.
func (Generator) NewID() string { return NewV7() }
