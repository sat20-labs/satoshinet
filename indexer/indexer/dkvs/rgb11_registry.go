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

func RGB11RegistryKey(providerDID, ticker string, ordinal uint64) (string, error) {
	if err := ValidatePrimaryDIDName(providerDID); err != nil || !validSegment(providerDID) || ordinal == 0 {
		return "", ErrInvalidRecord
	}
	base, err := NormalizeRGB11Ticker(ticker)
	if err != nil {
		return "", err
	}
	key := "/rgb11/" + providerDID + "/" + base + "/" + strconv.FormatUint(ordinal, 10)
	if _, err := ParseKey(key); err != nil {
		return "", err
	}
	return key, nil
}

func RGB11RegistryPrefix(providerDID, ticker string) (string, error) {
	if err := ValidatePrimaryDIDName(providerDID); err != nil || !validSegment(providerDID) {
		return "", ErrInvalidRecord
	}
	base, err := NormalizeRGB11Ticker(ticker)
	if err != nil {
		return "", err
	}
	return "/rgb11/" + providerDID + "/" + base, nil
}

func parseRGB11RegistryKey(parsed ParsedKey) (provider, ticker string, ordinal uint64, err error) {
	if parsed.Namespace != RGB11RegistryNamespace || len(parsed.Segments) != 3 ||
		ValidatePrimaryDIDName(parsed.Segments[0]) != nil || !validSegment(parsed.Segments[0]) {
		return "", "", 0, ErrInvalidKey
	}
	ticker, err = NormalizeRGB11Ticker(parsed.Segments[1])
	if err != nil || ticker != parsed.Segments[1] {
		return "", "", 0, ErrInvalidKey
	}
	ordinal, err = strconv.ParseUint(parsed.Segments[2], 10, 64)
	if err != nil || ordinal == 0 || strconv.FormatUint(ordinal, 10) != parsed.Segments[2] {
		return "", "", 0, ErrInvalidKey
	}
	return parsed.Segments[0], ticker, ordinal, nil
}

func EncodeRGB11ContractID(contractID string) ([]byte, error) {
	if len(contractID) != RGB11RegistryContractBytes*2 ||
		contractID != strings.ToLower(contractID) || strings.Trim(contractID, "0") == "" {
		return nil, ErrInvalidRecord
	}
	raw, err := hex.DecodeString(contractID)
	if err != nil || len(raw) != RGB11RegistryContractBytes {
		return nil, ErrInvalidRecord
	}
	return raw, nil
}

func DecodeRGB11ContractID(value []byte) (string, error) {
	if len(value) != RGB11RegistryContractBytes {
		return "", ErrInvalidRecord
	}
	id := hex.EncodeToString(value)
	if strings.Trim(id, "0") == "" {
		return "", ErrInvalidRecord
	}
	return id, nil
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

func RGB11RegistrationFromRecord(record *wire.DKVSRecord) (*RGB11Registration, error) {
	if record == nil {
		return nil, ErrRecordNotFound
	}
	parsed, err := ParseKey(record.Key)
	if err != nil {
		return nil, err
	}
	provider, ticker, ordinal, err := parseRGB11RegistryKey(parsed)
	if err != nil {
		return nil, err
	}
	contractID, err := DecodeRGB11ContractID(record.Value)
	if err != nil {
		return nil, err
	}
	name, err := BuildRGB11AssetName(provider, ticker, ordinal)
	if err != nil {
		return nil, err
	}
	return &RGB11Registration{
		ContractID: contractID, AssetName: name, ProviderDID: provider,
		BaseTicker: ticker, Ordinal: ordinal,
	}, nil
}

func isRGB11RegistryRecord(record *wire.DKVSRecord, parsed ParsedKey) bool {
	if record == nil || parsed.Namespace != RGB11RegistryNamespace {
		return false
	}
	_, _, _, err := parseRGB11RegistryKey(parsed)
	return err == nil
}

func validateRGB11RegistryStored(record *wire.DKVSRecord, parsed ParsedKey) error {
	if !isRGB11RegistryRecord(record, parsed) || IsTombstone(record.Flags) ||
		record.Seq != 1 || record.TTL != 0 || len(record.FeeProof) != 0 || len(record.PubKey) == 0 {
		return ErrInvalidRecord
	}
	if err := VerifySignature(record); err != nil {
		return err
	}
	_, err := DecodeRGB11ContractID(record.Value)
	return err
}

func validateRGB11RegistryPermissionWith(record *wire.DKVSRecord, parsed ParsedKey, system SystemVerifier) error {
	if err := validateRGB11RegistryStored(record, parsed); err != nil {
		return err
	}
	if system == nil {
		return ErrPermissionDenied
	}
	return system.CanWriteSystem(record.Key, record.PubKey)
}

func (i *Indexer) validateRGB11RegistryInsertLocked(record *wire.DKVSRecord, parsed ParsedKey,
	height, now uint64) error {

	provider, ticker, ordinal, err := parseRGB11RegistryKey(parsed)
	if err != nil {
		return err
	}
	if _, err := DecodeRGB11ContractID(record.Value); err != nil {
		return err
	}
	prefix, _ := RGB11RegistryPrefix(provider, ticker)
	records, _, _, err := i.scanLocked(prefix, nil, 0, true, height, now)
	if err != nil {
		return err
	}
	if uint64(len(records))+1 != ordinal {
		return ErrInvalidSequence
	}
	added, _ := DecodeRGB11ContractID(record.Value)
	all, _, _, err := i.scanLocked("/rgb11", nil, 0, true, height, now)
	if err != nil {
		return err
	}
	for _, candidate := range all {
		if candidate == nil || candidate.Key == record.Key {
			continue
		}
		id, err := DecodeRGB11ContractID(candidate.Value)
		if err != nil {
			return err
		}
		if id == added {
			return ErrInvalidRecord
		}
	}
	return nil
}

func validateRGB11PathRecords(path string, records []*wire.DKVSRecord) error {
	prefix, err := ParsePrefix(path)
	if err != nil || prefix.Namespace != RGB11RegistryNamespace || len(prefix.Segments) != 2 {
		return ErrInvalidSnapshot
	}
	provider := prefix.Segments[0]
	ticker, err := NormalizeRGB11Ticker(prefix.Segments[1])
	if err != nil || ticker != prefix.Segments[1] {
		return ErrInvalidSnapshot
	}
	seenContracts := make(map[string]struct{}, len(records))
	seenOrdinals := make(map[uint64]struct{}, len(records))
	for _, record := range records {
		if record == nil {
			return ErrInvalidSnapshot
		}
		parsed, err := ParseKey(record.Key)
		if err != nil {
			return ErrInvalidSnapshot
		}
		gotProvider, gotTicker, ordinal, err := parseRGB11RegistryKey(parsed)
		if err != nil || gotProvider != provider || gotTicker != ticker {
			return ErrInvalidSnapshot
		}
		if _, duplicate := seenOrdinals[ordinal]; duplicate {
			return ErrInvalidSnapshot
		}
		seenOrdinals[ordinal] = struct{}{}
		contractID, err := DecodeRGB11ContractID(record.Value)
		if err != nil {
			return ErrInvalidSnapshot
		}
		if _, duplicate := seenContracts[contractID]; duplicate {
			return ErrInvalidSnapshot
		}
		seenContracts[contractID] = struct{}{}
	}
	for ordinal := uint64(1); ordinal <= uint64(len(records)); ordinal++ {
		if _, ok := seenOrdinals[ordinal]; !ok {
			return ErrInvalidSnapshot
		}
	}
	return nil
}

func (i *Indexer) LookupRGB11Contract(contractID string) (*RGB11Registration, error) {
	if i == nil {
		return nil, ErrInvalidRecord
	}
	if _, err := EncodeRGB11ContractID(contractID); err != nil {
		return nil, err
	}
	records, _, _, err := i.scan("/rgb11", nil, 0, true)
	if err != nil {
		return nil, err
	}
	var found *RGB11Registration
	for _, record := range records {
		reg, err := RGB11RegistrationFromRecord(record)
		if err != nil {
			return nil, err
		}
		if reg.ContractID != contractID {
			continue
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
		reg, err := RGB11RegistrationFromRecord(record)
		if err != nil {
			return nil, err
		}
		if reg.AssetName == assetName {
			return reg, nil
		}
	}
	return nil, ErrRecordNotFound
}

func (i *Indexer) RGB11RegistryCount(providerDID, ticker string) (uint64, error) {
	if i == nil {
		return 0, ErrInvalidRecord
	}
	prefix, err := RGB11RegistryPrefix(providerDID, ticker)
	if err != nil {
		return 0, err
	}
	records, _, _, err := i.scan(prefix, nil, 0, true)
	if err != nil {
		return 0, err
	}
	return uint64(len(records)), nil
}

func IsRGB11RegistryNotFound(err error) bool {
	return errors.Is(err, ErrRecordNotFound)
}
