package Ext

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func ComputeHMAC(torrentID int, ts int64, pageToken string) string {
	data := fmt.Sprintf("%d|%d|%s", torrentID, ts, pageToken)
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}
