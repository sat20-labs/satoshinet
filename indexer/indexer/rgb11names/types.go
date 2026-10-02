// Package rgb11names indexes SatoshiNet Primary DID bindings and RGB11
// ContractID <-> AssetName registrations. It does not index RGB balances or
// bridge proofs. Block effects are derived by the SatoshiNet indexer from
// authenticated chain operations; there is no public mutation API.
package rgb11names

import (
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	MaxDIDCharacters = 10
	MaxTickerBytes   = 1024
	MaxBlockEvents   = 4096
)

var (
	ErrInvalid      = errors.New("invalid RGB11 naming data")
	ErrNotFound     = errors.New("RGB11 naming record not found")
	ErrOwner        = errors.New("RGB11 naming owner/address mismatch")
	ErrConflict     = errors.New("RGB11 naming immutable record conflict")
	ErrOrder        = errors.New("RGB11 naming block/event order mismatch")
	ErrCorrupt      = errors.New("RGB11 naming index is inconsistent")
	ErrUnavailable  = errors.New("RGB11 naming index unavailable")
	ErrOrdinalLimit = errors.New("RGB11 naming ordinal exhausted")
)

type Cursor struct {
	Height int    `json:"height"`
	Hash   string `json:"hash"`
}

type Position struct {
	Height     int    `json:"height"`
	TxID       string `json:"txid"`
	TxIndex    uint32 `json:"tx_index"`
	EventIndex uint32 `json:"event_index"`
}

// Register contains facts extracted from an authenticated transcend.tc deploy.
// ContractID is the complete 32 bytes encoded as 64 lowercase hex characters,
// NOT a hash/fingerprint. Registration itself does not mint or credit assets.
type Register struct {
	ContractID      string `json:"contract_id"`
	BaseTicker      string `json:"base_ticker"`
	AssetType       string `json:"asset_type"`
	GenesisOutpoint string `json:"genesis_outpoint"`
	ProviderDID     string `json:"provider_did"`
}

// Exactly one effect must be populated. Position is checked against the block,
// and events must arrive in strictly increasing transaction/event order.
type Event struct {
	TxIndex    uint32    `json:"tx_index"`
	EventIndex uint32    `json:"event_index"`
	TxID       string    `json:"txid"`
	Register   *Register `json:"register,omitempty"`

	// Optional is used for auto-discovered transcend deployments. A malformed
	// or policy-ineligible naming descriptor never invalidates the underlying
	// SatoshiNet channel-contract deployment.
	Optional bool `json:"optional,omitempty"`
}

type Registration struct {
	ContractID      string   `json:"contract_id"`
	AssetName       string   `json:"asset_name"`
	BaseTicker      string   `json:"base_ticker"`
	AssetType       string   `json:"asset_type"`
	ProviderDID     string   `json:"provider_did"`
	Ordinal         uint64   `json:"ordinal"`
	GenesisOutpoint string   `json:"genesis_outpoint"`
	RegisteredAt    Position `json:"registered_at"`
}

type Query struct {
	Kind     string
	Value    string
	Provider string
	Ticker   string
}

type Counter struct {
	ProviderDID string `json:"provider_did"`
	BaseTicker  string `json:"base_ticker"`
	MaxOrdinal  uint64 `json:"max_ordinal"`
}

type Result struct {
	Cursor       Cursor        `json:"indexed_at"`
	Binding      *Binding      `json:"binding,omitempty"`
	Ownership    *Ownership    `json:"ownership,omitempty"`
	Registration *Registration `json:"registration,omitempty"`
	Counter      *Counter      `json:"counter,omitempty"`
}

func ValidateDID(did string) error {
	if len(did) == 0 || len(did) > MaxDIDCharacters*utf8.UTFMax || !utf8.ValidString(did) ||
		utf8.RuneCountInString(did) > MaxDIDCharacters || did != strings.ToLower(did) {
		return ErrInvalid
	}
	for _, r := range did {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || strings.ContainsRune("@:/\\", r) {
			return ErrInvalid
		}
	}
	return nil
}

// NormalizeTicker matches the wallet's normalization, including reserved
// ordinal suffix rejection before normalization. Counter keys use this result,
// so different raw spellings that normalize equally cannot collide.
func NormalizeTicker(raw string) (string, error) {
	if len(raw) > MaxTickerBytes || !utf8.ValidString(raw) || strings.ContainsAny(raw, "@:") {
		return "", ErrInvalid
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrInvalid
	}
	if i := strings.LastIndexByte(raw, '_'); i >= 0 && i+1 < len(raw) {
		digits := true
		for _, c := range raw[i+1:] {
			if c < '0' || c > '9' {
				digits = false
				break
			}
		}
		if digits {
			return "", ErrInvalid
		}
	}
	var out strings.Builder
	dash := false
	for _, c := range []byte(raw) {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			out.WriteByte(c)
			dash = false
		} else if out.Len() > 0 && !dash {
			out.WriteByte('-')
			dash = true
		}
	}
	name := strings.Trim(out.String(), "-")
	if name == "" {
		name = "asset"
	}
	return name, nil
}

func BuildAssetName(ticker, assetType, provider string, ordinal uint64) (string, error) {
	base, err := NormalizeTicker(ticker)
	if err != nil || ValidateDID(provider) != nil || ordinal == 0 || (assetType != "f" && assetType != "n") {
		return "", ErrInvalid
	}
	if ordinal > 1 {
		base += "_" + strconv.FormatUint(ordinal, 10)
	}
	return "rgb11:" + assetType + ":" + base + "@" + provider, nil
}

func validHash(text string) bool {
	// Reject oversized HTTP query values before decoding or allocating.
	if len(text) != 64 {
		return false
	}
	raw, err := hex.DecodeString(text)
	return err == nil && len(raw) == 32 && text == strings.ToLower(text)
}

func validContractID(text string) bool {
	return validHash(text) && text != strings.Repeat("0", 64)
}

func validOutpoint(text string) bool {
	if len(text) < 66 || len(text) > 75 || text[64] != ':' || !validHash(text[:64]) {
		return false
	}
	outpoint, err := wire.NewOutPointFromString(text)
	return err == nil && outpoint.String() == text
}

func canonicalAddress(text string, params *chaincfg.Params) (string, error) {
	if params == nil || len(text) == 0 || len(text) > 128 || text != strings.TrimSpace(text) {
		return "", ErrInvalid
	}
	address, err := btcutil.DecodeAddress(text, params)
	if err != nil || !address.IsForNet(params) {
		return "", ErrInvalid
	}
	switch address.(type) {
	case *btcutil.AddressPubKeyHash, *btcutil.AddressScriptHash, *btcutil.AddressWitnessPubKeyHash, *btcutil.AddressWitnessScriptHash, *btcutil.AddressTaproot:
		return address.EncodeAddress(), nil
	default:
		return "", ErrInvalid
	}
}
