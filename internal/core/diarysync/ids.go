package diarysync

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

var seq atomic.Uint64

func newID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		n := seq.Add(1)
		return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), n)
	}
	n := seq.Add(1)
	return fmt.Sprintf("%s_%s%02x", prefix, hex.EncodeToString(b[:]), byte(n))
}
