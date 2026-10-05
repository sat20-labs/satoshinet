package p2p

import (
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

// Realtime P2P notifications carry only the signed KV operation. Source
// validity is checked by Handler; no generation or source ownership is stored.
func (h Handler) applyCurrentNotification(record *wire.DKVSRecord) (bool, error) {
	if !h.TrustedSource || h.validatorID() == "" {
		return false, dkvs.ErrPermissionDenied
	}
	return h.Store.AcceptDKVSCurrentRecord(record)
}
