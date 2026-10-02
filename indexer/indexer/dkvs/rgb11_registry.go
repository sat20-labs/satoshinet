package dkvs

import (
	"encoding/hex"
	"errors"
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

func validateRGB11RegistryStored(record *wire.DKVSRecord, parsed ParsedKey) error {
	if !isRGB11RegistryRecord(record, parsed) || IsTombstone(record.Flags) ||
		record.TTL != 0 || len(record.FeeProof) != 0 || len(record.PubKey) == 0 {
		return ErrInvalidRecord
	}
	if err := VerifySignature(record); err != nil {
		return err
	}
	if err := ValidatePrimaryDIDName(parsed.Segments[0]); err != nil {
		return err
	}
	base, err := NormalizeRGB11Ticker(parsed.Segments[1])
	if err != nil || base != parsed.Segments[1] {
		return ErrInvalidRecord
	}
	_, err = DecodeRGB11RegistryContracts(record.Value)
	return err
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

func (i *Indexer) validateRGB11RegistryMutationLocked(record *wire.DKVSRecord, parsed ParsedKey,
	existing *wire.DKVSRecord, height, now uint64) error {

	if err := validateRGB11RegistryMutation(record, parsed, existing); err != nil {
		return err
	}
	next, err := DecodeRGB11RegistryContracts(record.Value)
	if err != nil || len(next) == 0 {
		return ErrInvalidRecord
	}
	added := next[len(next)-1]
	records, _, _, err := i.scanLocked("/rgb11", nil, 0, true, height, now)
	if err != nil {
		return err
	}
	for _, candidate := range records {
		if candidate == nil || candidate.Key == record.Key {
			continue
		}
		ids, err := DecodeRGB11RegistryContracts(candidate.Value)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if id == added {
				return ErrInvalidRecord
			}
		}
	}
	return nil
}

func (i *Indexer) LookupRGB11Contract(contractID string) (*RGB11Registration, error) {
	if i == nil || len(contractID) != 64 || contractID != strings.ToLower(contractID) {
		return nil, ErrInvalidRecord
	}
	if _, err := hex.DecodeString(contractID); err != nil || strings.Trim(contractID, "0") == "" {
		return nil, ErrInvalidRecord
	}
	records, _, _, err := i.scan("/rgb11", nil, 0, true)
	if err != nil {
		return nil, err
	}
	var found *RGB11Registration
	for _, record := range records {
		reg, err := RGB11RegistrationFromRecord(record, contractID)
		if errors.Is(err, ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if found != nil && found.AssetName != reg.AssetName {
			return nil, ErrInvalidRecord
		}
		copyReg := *reg
		found = &copyReg
	}
	if found == nil {
		return nil, ErrRecordNotFound
	}
	return found, nil
}

func (i *Indexer) LookupRGB11AssetName(assetName string) (*RGB11Registration, error) {
	if i == nil || !strings.HasPrefix(assetName, "rgb11:f:") {
		return nil, ErrInvalidRecord
	}
	records, _, _, err := i.scan("/rgb11", nil, 0, true)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		parsed, err := ParseKey(record.Key)
		if err != nil || parsed.Namespace != RGB11RegistryNamespace || len(parsed.Segments) != 2 {
			continue
		}
		ids, err := DecodeRGB11RegistryContracts(record.Value)
		if err != nil {
			return nil, err
		}
		for index, id := range ids {
			ordinal := uint64(index + 1)
			name, err := BuildRGB11AssetName(parsed.Segments[0], parsed.Segments[1], ordinal)
			if err != nil {
				return nil, err
			}
			if name == assetName {
				return &RGB11Registration{
					ContractID: id, AssetName: name, ProviderDID: parsed.Segments[0],
					BaseTicker: parsed.Segments[1], Ordinal: ordinal,
				}, nil
			}
		}
	}
	return nil, ErrRecordNotFound
}
