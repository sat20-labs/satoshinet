package dkvs

import (
	"bytes"
	"encoding/hex"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	"github.com/sat20-labs/satoshinet/wire"
)

const rgb11NamePrefix = "/contract/rgb11-name/"

const (
	RGB11NameClaimSeq = 1
	RGB11NameProofSeq = 2
)

// RGB11NameKey addresses the immutable binding for one complete short ticker.
func RGB11NameKey(ticker string) (string, error) {
	base, suffix, found := strings.Cut(ticker, "@")
	if !found || base == "" || nativeproof.RGB11BaseTicker(base) != base || len(suffix) != nativeproof.RGB11SuffixLen {
		return "", ErrInvalidKey
	}
	for _, char := range suffix {
		if !(char >= 'a' && char <= 'f' || char >= '0' && char <= '9') {
			return "", ErrInvalidKey
		}
	}
	key := rgb11NamePrefix + base + "/" + suffix
	if _, err := ParseKey(key); err != nil {
		return "", err
	}
	return key, nil
}

func RGB11NameTicker(genesisTicker, contractID string) (string, error) {
	value, err := RGB11NameValue(contractID)
	if err != nil {
		return "", err
	}
	ticker := nativeproof.RGB11ShortTicker(genesisTicker, [32]byte(value[1:]))
	if _, err := RGB11NameKey(ticker); err != nil {
		return "", err
	}
	return ticker, nil
}

// The immutable registry value contains only the Contract ID. The ticker and
// its suffix are in the signed key.
func RGB11NameValue(contractID string) ([]byte, error) {
	id, err := hex.DecodeString(contractID)
	if err != nil || len(id) != 32 || contractID != hex.EncodeToString(id) || bytes.Equal(id, make([]byte, 32)) {
		return nil, ErrInvalidRecord
	}
	return append([]byte{1}, id...), nil
}

func VerifyRGB11Name(record *wire.DKVSRecord, ticker string) (string, error) {
	if record == nil || record.Seq != RGB11NameProofSeq {
		return "", ErrInvalidRecord
	}
	return VerifyRGB11NameRecord(record, ticker)
}

// VerifyRGB11NameRecord accepts both phases for registry writes and readback.
func VerifyRGB11NameRecord(record *wire.DKVSRecord, ticker string) (string, error) {
	key, err := RGB11NameKey(ticker)
	if err != nil || record == nil || record.Key != key || len(record.Value) != 33 || record.Value[0] != 1 ||
		hex.EncodeToString(record.PubKey) != indexer.GetCoreNodePubKey() {
		return "", ErrInvalidRecord
	}
	if err := validateContractEnvelope(record); err != nil {
		return "", err
	}
	if err := VerifySignature(record); err != nil {
		return "", err
	}
	id := hex.EncodeToString(record.Value[1:])
	base, _, _ := strings.Cut(ticker, "@")
	if expected, err := RGB11NameTicker(base, id); err != nil || expected != ticker {
		return "", ErrInvalidRecord
	}
	return id, nil
}

func RGB11NameFromKey(key string) (string, bool) {
	path, found := strings.CutPrefix(key, rgb11NamePrefix)
	if !found {
		return "", false
	}
	base, suffix, _ := strings.Cut(path, "/")
	return base + "@" + suffix, true
}
