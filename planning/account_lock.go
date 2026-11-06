package planning

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
)

var accountLocks sync.Map

// AccountIdentity uses the Hyperliquid owner wallet, not the replaceable agent.
// Binance API keys are account identifiers; different keys for one Binance
// account require operator coordination because its account UID is not exposed here.
func AccountIdentity(venue, key, wallet string) string {
	identity := venue + ":" + key
	if venue == "hyperliquid" {
		identity = venue + ":" + strings.ToLower(wallet)
	}
	h := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(h[:])
}
func LockAccount(identity string) func() {
	m, _ := accountLocks.LoadOrStore(identity, &sync.Mutex{})
	lock := m.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}
