package p2p

import (
	"encoding/binary"
	"encoding/hex"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func appendAuthBytes(dst, value []byte) []byte {
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], uint32(len(value)))
	dst = append(dst, size[:]...)
	return append(dst, value...)
}

// SyncAuthPayload binds a Mirror response to its complete request and page
// context. Signing remains a node/server responsibility; this package only
// defines and verifies the public protocol payload.
func SyncAuthPayload(net wire.BitcoinNet, requestCursor []byte, filters []wire.DKVSSyncFilter, msg *wire.MsgDKVSSyncResponse) []byte {
	payload := append([]byte("satsnet:dkvs:mirror:v1"), 0)
	var fixed [12]byte
	binary.LittleEndian.PutUint32(fixed[0:4], uint32(net))
	binary.LittleEndian.PutUint64(fixed[4:12], msg.SessionID)
	payload = append(payload, fixed[:]...)
	payload = appendAuthBytes(payload, requestCursor)
	for _, filter := range filters {
		payload = appendAuthBytes(payload, []byte(filter.Type))
		payload = appendAuthBytes(payload, []byte(filter.Target))
	}
	payload = appendAuthBytes(payload, msg.NextCursor)
	if msg.Done {
		payload = append(payload, 1)
	} else {
		payload = append(payload, 0)
	}
	payload = append(payload, msg.CheckpointRoot[:]...)
	for _, record := range msg.Records {
		hash := dkvs.RecordHash(record)
		payload = append(payload, hash[:]...)
	}
	return payload
}

// VerifySyncSignature verifies a Mirror response against the authenticated
// validator identity advertised by the P2P peer.
func VerifySyncSignature(net wire.BitcoinNet, validatorID string, requestCursor []byte, filters []wire.DKVSSyncFilter, msg *wire.MsgDKVSSyncResponse) bool {
	if msg == nil || len(msg.SourceSignature) == 0 {
		return false
	}
	pubKeyBytes, err := hex.DecodeString(validatorID)
	if err != nil {
		return false
	}
	pubKey, err := secp256k1.ParsePubKey(pubKeyBytes)
	if err != nil {
		return false
	}
	sig, err := ecdsa.ParseDERSignature(msg.SourceSignature)
	if err != nil {
		return false
	}
	return anchortx.VerifyMessage(pubKey, SyncAuthPayload(net, requestCursor, filters, msg), sig)
}

// KeyHash is the stable hash used to track key-based DKVS requests.
func KeyHash(key string) chainhash.Hash {
	return chainhash.DoubleHashH([]byte(key))
}
