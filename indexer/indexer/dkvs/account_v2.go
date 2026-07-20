package dkvs

import (
	"encoding/hex"
	"strings"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	// VersionV2 removes the repeated public key from account-scoped records.
	// The signer is the x-only public key encoded by the record key (or, for
	// /account, by the mapping value) and signatures use BIP340 Schnorr.
	VersionV2 = uint32(2)

	accountIDV2Size = 32
)

// IsSupportedRecordVersion reports whether this implementation can validate a
// DKVS record version. Version 1 remains supported for backwards compatibility.
func IsSupportedRecordVersion(version uint32) bool {
	return version == Version || version == VersionV2
}

// AccountIDV2 returns the canonical lower-case x-only public key identifier.
// It accepts either a 32-byte x-only key or any secp256k1 public-key encoding.
func AccountIDV2(pubKey []byte) (string, error) {
	var xonly []byte
	if len(pubKey) == accountIDV2Size {
		if _, err := schnorr.ParsePubKey(pubKey); err != nil {
			return "", ErrInvalidSignature
		}
		xonly = append([]byte(nil), pubKey...)
	} else {
		parsed, err := btcec.ParsePubKey(pubKey)
		if err != nil {
			return "", ErrInvalidSignature
		}
		xonly = schnorr.SerializePubKey(parsed)
	}
	return hex.EncodeToString(xonly), nil
}

// AccountPubKeyV2 reconstructs the canonical even-y compressed public key used
// by BIP340 verification from an x-only account identifier.
func AccountPubKeyV2(accountID string) ([]byte, error) {
	raw, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(accountID)))
	if err != nil || len(raw) != accountIDV2Size {
		return nil, ErrInvalidSignature
	}
	pubKey, err := schnorr.ParsePubKey(raw)
	if err != nil {
		return nil, ErrInvalidSignature
	}
	return pubKey.SerializeCompressed(), nil
}

// PersonalKeyV2 builds an account-scoped personal key without requiring a
// repeated public key value.
func PersonalKeyV2(accountID, path string) (string, error) {
	if _, err := AccountPubKeyV2(accountID); err != nil {
		return "", err
	}
	key := "/personal/" + strings.ToLower(accountID) + "/" + normalizePath(path)
	_, err := ParseKey(key)
	return key, err
}

// AccountMappingKey returns the public address-to-account lookup key. The
// mapping value is the raw 32-byte account ID and the record is signed by that
// same account.
func AccountMappingKey(network, address string) (string, error) {
	canonical, _, err := accountNetworkParams(network)
	if err != nil {
		return "", err
	}
	address = strings.ToLower(strings.TrimSpace(address))
	key := "/account/" + canonical + "/" + address
	_, err = ParseKey(key)
	return key, err
}

func EncodeAccountMappingValue(accountID string) ([]byte, error) {
	if _, err := AccountPubKeyV2(accountID); err != nil {
		return nil, err
	}
	return hex.DecodeString(strings.ToLower(accountID))
}

func DecodeAccountMappingValue(value []byte) (string, error) {
	if len(value) != accountIDV2Size {
		return "", ErrInvalidRecord
	}
	accountID, err := AccountIDV2(value)
	if err != nil {
		return "", ErrInvalidRecord
	}
	return accountID, nil
}

func accountNetworkParams(network string) (string, *chaincfg.Params, error) {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "mainnet", "bitcoin", "bc":
		return "mainnet", &chaincfg.MainNetParams, nil
	case "testnet", "testnet3", "tb3":
		return "testnet3", &chaincfg.TestNet3Params, nil
	case "testnet4", "tb4":
		return "testnet4", &chaincfg.TestNet4Params, nil
	case "signet", "sb":
		return "signet", &chaincfg.SigNetParams, nil
	case "regtest", "bcrt":
		return "regtest", &chaincfg.RegressionNetParams, nil
	default:
		return "", nil, ErrInvalidKey
	}
}

func validAccountNetwork(network string) bool {
	_, _, err := accountNetworkParams(network)
	return err == nil
}

// NewRecordV2 creates a pubkey-free account record. The caller must sign the
// SigningHash with the private key corresponding to the account ID encoded by
// the key/value.
func NewRecordV2(key string, value []byte, opts RecordOptions) (*wire.DKVSRecord, error) {
	if _, err := ParseKey(key); err != nil {
		return nil, err
	}
	record := &wire.DKVSRecord{
		Version:      VersionV2,
		Key:          key,
		Value:        append([]byte(nil), value...),
		Seq:          opts.Seq,
		IssueTime:    opts.IssueTime,
		TTL:          opts.TTL,
		ExpiryHeight: opts.ExpiryHeight,
		FeeProof:     append([]byte(nil), opts.FeeProof...),
		Flags:        opts.Flags,
	}
	if record.IssueTime == 0 {
		record.IssueTime = currentUnixMilli()
	}
	if RecordSize(record) > wire.MaxDKVSRecordSize || len(record.Value) > MaxRecordValueSize {
		return nil, ErrRecordTooLarge
	}
	return record, nil
}

// RecordSignerAccountID obtains the v2 signer without consulting record.PubKey.
func RecordSignerAccountID(record *wire.DKVSRecord, parsed ParsedKey) (string, error) {
	if record == nil || record.Version != VersionV2 {
		return "", ErrInvalidRecord
	}
	if parsed.Namespace == "" {
		var err error
		parsed, err = ParseKey(record.Key)
		if err != nil {
			return "", err
		}
	}
	var accountID string
	switch parsed.Namespace {
	case "account":
		if IsTombstone(record.Flags) {
			return "", ErrPermissionDenied
		}
		var err error
		accountID, err = DecodeAccountMappingValue(record.Value)
		if err != nil {
			return "", err
		}
	case "personal", "blob":
		accountID = parsed.Segments[0]
	case "mail":
		if len(parsed.Segments) != 4 {
			return "", ErrInvalidKey
		}
		if parsed.Segments[1] == "share" || IsTombstone(record.Flags) {
			accountID = parsed.Segments[0]
		} else if parsed.Segments[1] == "msg" {
			accountID = parsed.Segments[2]
		} else {
			return "", ErrInvalidKey
		}
	default:
		return "", ErrPermissionDenied
	}
	if _, err := AccountPubKeyV2(accountID); err != nil {
		return "", ErrPermissionDenied
	}
	return strings.ToLower(accountID), nil
}

// RecordSignerPubKey returns the effective compressed signer key for both DKVS
// versions. Callers such as fee verification must use this instead of reading
// record.PubKey directly.
func RecordSignerPubKey(record *wire.DKVSRecord) ([]byte, error) {
	if record == nil {
		return nil, ErrInvalidRecord
	}
	if record.Version == Version {
		if len(record.PubKey) == 0 {
			return nil, ErrInvalidSignature
		}
		return append([]byte(nil), record.PubKey...), nil
	}
	if record.Version != VersionV2 || len(record.PubKey) != 0 {
		return nil, ErrInvalidSignature
	}
	parsed, err := ParseKey(record.Key)
	if err != nil {
		return nil, err
	}
	accountID, err := RecordSignerAccountID(record, parsed)
	if err != nil {
		return nil, err
	}
	return AccountPubKeyV2(accountID)
}

// ValidateRecordIdentity checks the identity-to-key binding that is not covered
// by the cryptographic signature alone. Version 1 permissions remain unchanged.
func ValidateRecordIdentity(record *wire.DKVSRecord, parsed ParsedKey) error {
	if record == nil || record.Version == Version {
		return nil
	}
	if record.Version != VersionV2 || len(record.PubKey) != 0 {
		return ErrInvalidRecord
	}
	accountID, err := RecordSignerAccountID(record, parsed)
	if err != nil {
		return err
	}
	switch parsed.Namespace {
	case "account":
		if len(parsed.Segments) != 2 || IsTombstone(record.Flags) {
			return ErrInvalidRecord
		}
		_, params, err := accountNetworkParams(parsed.Segments[0])
		if err != nil {
			return err
		}
		pubKey, err := AccountPubKeyV2(accountID)
		if err != nil {
			return err
		}
		address, err := P2TRAddressFromPubKeyBytes(pubKey, params)
		if err != nil || !strings.EqualFold(address, parsed.Segments[1]) {
			return ErrPermissionDenied
		}
	case "personal", "blob":
		if parsed.Segments[0] != accountID {
			return ErrPermissionDenied
		}
	case "mail":
		if parsed.Segments[1] == "share" {
			if parsed.Segments[0] != accountID {
				return ErrPermissionDenied
			}
		} else if IsTombstone(record.Flags) {
			if parsed.Segments[0] != accountID {
				return ErrPermissionDenied
			}
		} else if parsed.Segments[1] != "msg" || parsed.Segments[2] != accountID {
			return ErrPermissionDenied
		}
	default:
		return ErrPermissionDenied
	}
	return nil
}

func verifySignatureV2(record *wire.DKVSRecord) error {
	if record == nil || record.Version != VersionV2 || len(record.PubKey) != 0 || len(record.Signature) == 0 {
		return ErrInvalidSignature
	}
	pubKeyBytes, err := RecordSignerPubKey(record)
	if err != nil {
		return err
	}
	pubKey, err := btcec.ParsePubKey(pubKeyBytes)
	if err != nil {
		return ErrInvalidSignature
	}
	sig, err := schnorr.ParseSignature(record.Signature)
	if err != nil {
		return ErrInvalidSignature
	}
	hash := SigningHash(record)
	if !sig.Verify(hash[:], pubKey) {
		return ErrInvalidSignature
	}
	return nil
}
