package dkvs

import (
	"encoding/hex"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/sat20-labs/satoshinet/wire"
)

const (
	RGB11RegistryVersion       = byte(1)
	RGB11RegistryNamespace     = "rgb11"
	RGB11RegistryContractBytes = 32
)

type RGB11Registration struct {
	ContractID  string `json:"contract_id"`
	AssetName   string `json:"asset_name"`
	ProviderDID string `json:"provider_did"`
	BaseTicker  string `json:"base_ticker"`
	Ordinal     uint64 `json:"ordinal"`
}

func NormalizeRGB11Ticker(raw string) (string, error) {
	if !utf8.ValidString(raw) || strings.ContainsAny(raw, "@:") {
		return "", ErrInvalidRecord
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrInvalidRecord
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
			return "", ErrInvalidRecord
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
	ticker := strings.Trim(out.String(), "-")
	if ticker == "" {
		return "", ErrInvalidRecord
	}
	return ticker, nil
}

func RGB11RegistryKey(providerDID, ticker string) (string, error) {
	if err := ValidatePrimaryDIDName(providerDID); err != nil {
		return "", err
	}
	base, err := NormalizeRGB11Ticker(ticker)
	if err != nil {
		return "", err
	}
	key := "/rgb11/" + providerDID + "/" + base
	if _, err := ParseKey(key); err != nil {
		return "", err
	}
	return key, nil
}

func EncodeRGB11RegistryContracts(contractIDs []string) ([]byte, error) {
	if len(contractIDs) == 0 {
		return nil, ErrInvalidRecord
	}
	value := make([]byte, 1, 1+len(contractIDs)*RGB11RegistryContractBytes)
	value[0] = RGB11RegistryVersion
	seen := make(map[string]struct{}, len(contractIDs))
	for _, id := range contractIDs {
		if len(id) != 64 || id != strings.ToLower(id) {
			return nil, ErrInvalidRecord
		}
		raw, err := hex.DecodeString(id)
		if err != nil || len(raw) != RGB11RegistryContractBytes || strings.Trim(id, "0") == "" {
			return nil, ErrInvalidRecord
		}
		if _, ok := seen[id]; ok {
			return nil, ErrInvalidRecord
		}
		seen[id] = struct{}{}
		value = append(value, raw...)
	}
	if len(value) > wire.MaxDKVSRecordSize {
		return nil, ErrInvalidRecord
	}
	return value, nil
}

func DecodeRGB11RegistryContracts(value []byte) ([]string, error) {
	if len(value) < 1+RGB11RegistryContractBytes || value[0] != RGB11RegistryVersion ||
		(len(value)-1)%RGB11RegistryContractBytes != 0 {
		return nil, ErrInvalidRecord
	}
	count := (len(value) - 1) / RGB11RegistryContractBytes
	ids := make([]string, 0, count)
	seen := make(map[string]struct{}, count)
	for offset := 1; offset < len(value); offset += RGB11RegistryContractBytes {
		id := hex.EncodeToString(value[offset : offset+RGB11RegistryContractBytes])
		if strings.Trim(id, "0") == "" {
			return nil, ErrInvalidRecord
		}
		if _, ok := seen[id]; ok {
			return nil, ErrInvalidRecord
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func BuildRGB11AssetName(providerDID, ticker string, ordinal uint64) (string, error) {
	if ordinal == 0 || ValidatePrimaryDIDName(providerDID) != nil {
		return "", ErrInvalidRecord
	}
	base, err := NormalizeRGB11Ticker(ticker)
	if err != nil {
		return "", err
	}
	if ordinal > 1 {
		base += "_" + strconv.FormatUint(ordinal, 10)
	}
	return "rgb11:f:" + base + "@" + providerDID, nil
}

func RGB11RegistrationFromRecord(record *wire.DKVSRecord, contractID string) (*RGB11Registration, error) {
	if record == nil {
		return nil, ErrRecordNotFound
	}
	parsed, err := ParseKey(record.Key)
	if err != nil || parsed.Namespace != RGB11RegistryNamespace || len(parsed.Segments) != 2 {
		return nil, ErrInvalidRecord
	}
	ids, err := DecodeRGB11RegistryContracts(record.Value)
	if err != nil {
		return nil, err
	}
	for index, id := range ids {
		if id != contractID {
			continue
		}
		ordinal := uint64(index + 1)
		name, err := BuildRGB11AssetName(parsed.Segments[0], parsed.Segments[1], ordinal)
		if err != nil {
			return nil, err
		}
		return &RGB11Registration{
			ContractID: id, AssetName: name, ProviderDID: parsed.Segments[0],
			BaseTicker: parsed.Segments[1], Ordinal: ordinal,
		}, nil
	}
	return nil, ErrRecordNotFound
}

func isRGB11RegistryRecord(record *wire.DKVSRecord, parsed ParsedKey) bool {
	return record != nil && parsed.Namespace == RGB11RegistryNamespace && len(parsed.Segments) == 2
}

func validateRGB11RegistryPermissionWith(record *wire.DKVSRecord, parsed ParsedKey, resolver DIDResolver) error {
	if !isRGB11RegistryRecord(record, parsed) || IsTombstone(record.Flags) || record.TTL != 0 {
		return ErrInvalidRecord
	}
	if err := ValidatePrimaryDIDName(parsed.Segments[0]); err != nil {
		return err
	}
	base, err := NormalizeRGB11Ticker(parsed.Segments[1])
	if err != nil || base != parsed.Segments[1] {
		return ErrInvalidRecord
	}
	if _, err := DecodeRGB11RegistryContracts(record.Value); err != nil {
		return err
	}
	if resolver == nil {
		return ErrDIDResolverUnavailable
	}
	identity, err := resolver.ResolveName(parsed.Segments[0])
	if err != nil {
		return err
	}
	if !identity.Active || identity.CanonicalName != parsed.Segments[0] {
		return ErrPermissionDenied
	}
	return identity.CanSign(record.PubKey)
}

func validateRGB11RegistryMutation(record *wire.DKVSRecord, parsed ParsedKey, existing *wire.DKVSRecord) error {
	if !isRGB11RegistryRecord(record, parsed) {
		return ErrInvalidRecord
	}
	next, err := DecodeRGB11RegistryContracts(record.Value)
	if err != nil {
		return err
	}
	if existing == nil {
		if len(next) != 1 {
			return ErrInvalidRecord
		}
		return nil
	}
	current, err := DecodeRGB11RegistryContracts(existing.Value)
	if err != nil || len(next) != len(current)+1 {
		return ErrInvalidRecord
	}
	for i := range current {
		if next[i] != current[i] {
			return ErrInvalidRecord
		}
	}
	return nil
}
