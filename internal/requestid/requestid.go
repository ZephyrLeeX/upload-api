package requestid

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func New() string {
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Sprintf("%s-unavailable", time.Now().UTC().Format("20060102T150405.000000000Z"))
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(random[:])
}
