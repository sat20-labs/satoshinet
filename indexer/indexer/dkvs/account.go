package dkvs

import (
	"encoding/binary"
	"encoding/hex"
	"strings"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/wire"
)

const accountIDSize = 32

// CanonicalAccountID returns the lower-case x-only public key used as the
// account identifier for account-scoped DKVS records.
func CanonicalAccountID(pubKey []byte) (string, error) {
	var xonly []byte
	if len(pubKey) == accountIDSize {
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

// AccountPubKey reconstructs the canonical even-y compressed public key used
// by BIP340 verification from an x-only account identifier.
func AccountPubKey(accountID string) ([]byte, error) {
	raw, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(accountID)))
	if err != nil || len(raw) != accountIDSize {
		return nil, ErrInvalidSignature
	}
	pubKey, err := schnorr.ParsePubKey(raw)
	if err != nil {
		return nil, ErrInvalidSignature
	}
	return pubKey.SerializeCompressed(), nil
}

// AccountPersonalKey builds a personal key whose signer is derived from the
// x-only account identifier encoded by the key.
func AccountPersonalKey(accountID, path string) (string, error) {
	if _, err := AccountPubKey(accountID); err != nil {
		return "", err
	}
	key := "/personal/" + strings.ToLower(accountID) + "/" + normalizePath(path)
	_, err := ParseKey(key)
	return key, err
}

// AccountMappingKey returns the public address-to-account lookup key. Its
// value is an AccountServiceDescriptor signed by that same account.
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

const (
	AccountServiceDescriptorVersion = uint8(1)

	// AccountServiceCapabilityRGB11Direct advertises direct RGB11 transfers to
	// the root account's address. Standard invoice transfers do not depend on it.
	AccountServiceCapabilityRGB11Direct = uint64(1 << 0)

	accountServiceDescriptorFixedSize = 1 + accountIDSize + 33 + 8
	maxAccountServiceDescriptorSize   = 1024
	maxAccountServiceExtensionSize    = 512
)

// AccountServiceExtension is a protocol-defined extension of the one free
// account service record. Types must be non-zero and strictly increasing so
// the encoding has exactly one canonical form. Unknown types are preserved by
// the decoder for forward compatibility.
type AccountServiceExtension struct {
	Type  uint16
	Value []byte
}

// AccountServiceDescriptor is the bounded, versioned value stored at the one
// free /account/<network>/<root-address> key. It is not a general-purpose free
// blob: additions require a protocol-defined capability bit or extension type.
type AccountServiceDescriptor struct {
	Version      uint8
	AccountID    string
	CoreNodeID   string
	Capabilities uint64
	Extensions   []AccountServiceExtension
}

// EncodeAccountServiceDescriptor builds the single free account control value
// used for address discovery, Account -> CoreNode routing and small protocol
// capabilities. The account ID remains the record signer; CoreNode acceptance
// of the binding remains a separate local operation.
func EncodeAccountServiceDescriptor(descriptor AccountServiceDescriptor) ([]byte, error) {
	if descriptor.Version == 0 {
		descriptor.Version = AccountServiceDescriptorVersion
	}
	if descriptor.Version != AccountServiceDescriptorVersion {
		return nil, ErrInvalidRecord
	}
	accountID := strings.ToLower(strings.TrimSpace(descriptor.AccountID))
	account, err := hex.DecodeString(accountID)
	if err != nil || len(account) != accountIDSize {
		return nil, ErrInvalidRecord
	}
	if _, err := AccountPubKey(accountID); err != nil {
		return nil, err
	}
	coreNodeID := strings.ToLower(strings.TrimSpace(descriptor.CoreNodeID))
	core, err := hex.DecodeString(coreNodeID)
	if err != nil || len(core) != 33 {
		return nil, ErrInvalidRecord
	}
	if _, err := btcec.ParsePubKey(core); err != nil {
		return nil, ErrInvalidRecord
	}
	size := accountServiceDescriptorFixedSize
	previousType := uint16(0)
	for _, extension := range descriptor.Extensions {
		if extension.Type == 0 || extension.Type <= previousType || len(extension.Value) == 0 ||
			len(extension.Value) > maxAccountServiceExtensionSize {
			return nil, ErrInvalidRecord
		}
		size += 4 + len(extension.Value)
		if size > maxAccountServiceDescriptorSize {
			return nil, ErrInvalidRecord
		}
		previousType = extension.Type
	}
	value := make([]byte, size)
	value[0] = descriptor.Version
	copy(value[1:1+accountIDSize], account)
	copy(value[1+accountIDSize:1+accountIDSize+33], core)
	binary.BigEndian.PutUint64(value[1+accountIDSize+33:accountServiceDescriptorFixedSize], descriptor.Capabilities)
	offset := accountServiceDescriptorFixedSize
	for _, extension := range descriptor.Extensions {
		binary.BigEndian.PutUint16(value[offset:offset+2], extension.Type)
		binary.BigEndian.PutUint16(value[offset+2:offset+4], uint16(len(extension.Value)))
		copy(value[offset+4:], extension.Value)
		offset += 4 + len(extension.Value)
	}
	return value, nil
}

func DecodeAccountServiceDescriptor(value []byte) (*AccountServiceDescriptor, error) {
	if len(value) < accountServiceDescriptorFixedSize || len(value) > maxAccountServiceDescriptorSize ||
		value[0] != AccountServiceDescriptorVersion {
		return nil, ErrInvalidRecord
	}
	accountID, err := CanonicalAccountID(value[1 : 1+accountIDSize])
	if err != nil {
		return nil, ErrInvalidRecord
	}
	core := value[1+accountIDSize : 1+accountIDSize+33]
	if _, err := btcec.ParsePubKey(core); err != nil {
		return nil, ErrInvalidRecord
	}
	descriptor := &AccountServiceDescriptor{
		Version:      value[0],
		AccountID:    accountID,
		CoreNodeID:   hex.EncodeToString(core),
		Capabilities: binary.BigEndian.Uint64(value[1+accountIDSize+33 : accountServiceDescriptorFixedSize]),
	}
	previousType := uint16(0)
	for offset := accountServiceDescriptorFixedSize; offset < len(value); {
		if len(value)-offset < 4 {
			return nil, ErrInvalidRecord
		}
		typ := binary.BigEndian.Uint16(value[offset : offset+2])
		length := int(binary.BigEndian.Uint16(value[offset+2 : offset+4]))
		offset += 4
		if typ == 0 || typ <= previousType || length == 0 || length > maxAccountServiceExtensionSize ||
			length > len(value)-offset {
			return nil, ErrInvalidRecord
		}
		descriptor.Extensions = append(descriptor.Extensions, AccountServiceExtension{
			Type: typ, Value: append([]byte(nil), value[offset:offset+length]...),
		})
		offset += length
		previousType = typ
	}
	return descriptor, nil
}

func accountNetworkParams(network string) (string, *chaincfg.Params, error) {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "mainnet", "bitcoin", "bc":
		return "mainnet", &chaincfg.MainNetParams, nil
	case "testnet3", "tb3":
		return "testnet3", &chaincfg.TestNetParams, nil
	case "testnet", "testnet4", "tb4":
		return "testnet4", &chaincfg.TestNetParams, nil
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

func isAccountScopedNamespace(namespace string) bool {
	switch namespace {
	case "account", "personal", "mail", "blob":
		return true
	default:
		return false
	}
}

// NewAccountRecord creates a pubkey-free version-1 account record. The caller
// signs SigningHash with the private key corresponding to the account ID
// encoded by the record key or, for /account, by the mapping value.
func NewAccountRecord(key string, value []byte, opts RecordOptions) (*wire.DKVSRecord, error) {
	if _, err := ParseKey(key); err != nil {
		return nil, err
	}
	record := &wire.DKVSRecord{
		Version:     Version,
		Key:         key,
		Value:       append([]byte(nil), value...),
		Seq:         opts.Seq,
		IssueHeight: opts.IssueHeight,
		TTL:         opts.TTL,
		FeeProof:    append([]byte(nil), opts.FeeProof...),
		Flags:       opts.Flags,
	}
	parsed, err := ParseKey(key)
	if err != nil {
		return nil, err
	}
	if err := validateRecordSizeForParsed(record, parsed); err != nil {
		return nil, err
	}
	return record, nil
}

// RecordSignerAccountID derives the signer for a pubkey-free account record.
func RecordSignerAccountID(record *wire.DKVSRecord, parsed ParsedKey) (string, error) {
	if record == nil || record.Version != Version || len(record.PubKey) != 0 {
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
		descriptor, decodeErr := DecodeAccountServiceDescriptor(record.Value)
		err = decodeErr
		if err != nil {
			return "", err
		}
		accountID = descriptor.AccountID
	case "personal", "blob":
		accountID = parsed.Segments[0]
	case "mail":
		if len(parsed.Segments) < 2 {
			return "", ErrInvalidKey
		}
		if IsTombstone(record.Flags) || parsed.Segments[1] == "share" {
			accountID = parsed.Segments[0]
		} else if len(parsed.Segments) == 4 && parsed.Segments[1] == "msg" {
			accountID = parsed.Segments[2]
		} else {
			return "", ErrPermissionDenied
		}
	default:
		return "", ErrPermissionDenied
	}
	if _, err := AccountPubKey(accountID); err != nil {
		return "", ErrPermissionDenied
	}
	return strings.ToLower(accountID), nil
}

// RecordSignerPubKey returns the effective signer key. Account-scoped records
// derive it from the key/value; other DKVS namespaces may still carry the
// authorized key selected by their resolver.
func RecordSignerPubKey(record *wire.DKVSRecord) ([]byte, error) {
	if record == nil || record.Version != Version {
		return nil, ErrInvalidRecord
	}
	if len(record.PubKey) != 0 {
		return append([]byte(nil), record.PubKey...), nil
	}
	parsed, err := ParseKey(record.Key)
	if err != nil {
		return nil, err
	}
	accountID, err := RecordSignerAccountID(record, parsed)
	if err != nil {
		return nil, err
	}
	return AccountPubKey(accountID)
}

// ValidateRecordIdentity checks account identity-to-key binding. Records that
// carry a resolver-selected key are validated by the existing namespace policy.
func ValidateRecordIdentity(record *wire.DKVSRecord, parsed ParsedKey) error {
	if record == nil || record.Version != Version {
		return ErrInvalidRecord
	}
	// The outer mailbox record of a MessageManager delivery has no author key;
	// the inner encrypted message carries the sender signature. This form is
	// only creatable through PutInternalMailbox and remains valid for reads.
	if isInternalMailboxRecord(record) {
		return nil
	}
	accountScoped := isAccountScopedNamespace(parsed.Namespace)
	if accountScoped && len(record.PubKey) != 0 {
		return ErrInvalidRecord
	}
	if !accountScoped {
		if len(record.PubKey) == 0 {
			return ErrInvalidRecord
		}
		return nil
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
		pubKey, err := AccountPubKey(accountID)
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
		if parsed.Segments[1] == "share" || IsTombstone(record.Flags) {
			if parsed.Segments[0] != accountID {
				return ErrPermissionDenied
			}
		} else if len(parsed.Segments) != 4 || parsed.Segments[1] != "msg" || parsed.Segments[2] != accountID {
			return ErrPermissionDenied
		}
	default:
		return ErrPermissionDenied
	}
	return nil
}

func verifyAccountSignature(record *wire.DKVSRecord) error {
	if record == nil || record.Version != Version || len(record.PubKey) != 0 || len(record.Signature) == 0 {
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
