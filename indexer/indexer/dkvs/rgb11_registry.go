package dkvs

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	RGB11RegistryNamespace     = "rgb11"
	RGB11RegistryContractBytes = 32
	RGB11RegistryValueBytes    = 1 + RGB11RegistryContractBytes
)

type RGB11Registration struct {
	ContractID  string `json:"contract_id"`
	AssetName   string `json:"asset_name"`
	AssetType   string `json:"asset_type"`
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
		return "asset", nil
	}
	return ticker, nil
}

func RGB11RegistryKey(providerDID, ticker string, ordinal uint64) (string, error) {
	if err := ValidatePrimaryDIDName(providerDID); err != nil || ordinal == 0 {
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
	if err := ValidatePrimaryDIDName(providerDID); err != nil {
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
		ValidatePrimaryDIDName(parsed.Segments[0]) != nil {
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

func validRGB11AssetType(assetType string) bool {
	return assetType == indexercommon.ASSET_TYPE_FT || assetType == indexercommon.ASSET_TYPE_NFT
}

func EncodeRGB11RegistryValue(assetType, contractID string) ([]byte, error) {
	if !validRGB11AssetType(assetType) || len(contractID) != RGB11RegistryContractBytes*2 ||
		contractID != strings.ToLower(contractID) || strings.Trim(contractID, "0") == "" {
		return nil, ErrInvalidRecord
	}
	raw, err := hex.DecodeString(contractID)
	if err != nil || len(raw) != RGB11RegistryContractBytes {
		return nil, ErrInvalidRecord
	}
	value := make([]byte, RGB11RegistryValueBytes)
	value[0] = assetType[0]
	copy(value[1:], raw)
	return value, nil
}

func DecodeRGB11RegistryValue(value []byte) (assetType, contractID string, err error) {
	if len(value) != RGB11RegistryValueBytes {
		return "", "", ErrInvalidRecord
	}
	assetType = string(value[:1])
	if !validRGB11AssetType(assetType) {
		return "", "", ErrInvalidRecord
	}
	contractID = hex.EncodeToString(value[1:])
	if strings.Trim(contractID, "0") == "" {
		return "", "", ErrInvalidRecord
	}
	return assetType, contractID, nil
}

func BuildRGB11AssetName(providerDID, ticker, assetType string, ordinal uint64) (string, error) {
	if ordinal == 0 || ValidatePrimaryDIDName(providerDID) != nil || !validRGB11AssetType(assetType) {
		return "", ErrInvalidRecord
	}
	base, err := NormalizeRGB11Ticker(ticker)
	if err != nil {
		return "", err
	}
	if ordinal > 1 {
		base += "_" + strconv.FormatUint(ordinal, 10)
	}
	return "rgb11:" + assetType + ":" + base + "@" + providerDID, nil
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
	assetType, contractID, err := DecodeRGB11RegistryValue(record.Value)
	if err != nil {
		return nil, err
	}
	name, err := BuildRGB11AssetName(provider, ticker, assetType, ordinal)
	if err != nil {
		return nil, err
	}
	return &RGB11Registration{
		ContractID: contractID, AssetName: name, AssetType: assetType,
		ProviderDID: provider, BaseTicker: ticker, Ordinal: ordinal,
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
	_, _, err := DecodeRGB11RegistryValue(record.Value)
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

func (i *Indexer) validateRGB11IncomingGlobalLocked(records []*wire.DKVSRecord,
	replacedKeys map[string]struct{}, height, now uint64) error {

	incomingByContract := make(map[string]string)
	incomingByKey := make(map[string]*wire.DKVSRecord)
	for _, record := range records {
		if record == nil {
			continue
		}
		parsed, err := ParseKey(record.Key)
		if err != nil || parsed.Namespace != RGB11RegistryNamespace {
			continue
		}
		_, contractID, err := DecodeRGB11RegistryValue(record.Value)
		if err != nil {
			return ErrInvalidRecord
		}
		if key, duplicate := incomingByContract[contractID]; duplicate && key != record.Key {
			return ErrInvalidRecord
		}
		if old, duplicate := incomingByKey[record.Key]; duplicate && !bytes.Equal(old.Value, record.Value) {
			return ErrWriteConflict
		}
		incomingByContract[contractID] = record.Key
		incomingByKey[record.Key] = record
	}
	if len(incomingByContract) == 0 && len(replacedKeys) == 0 {
		return nil
	}

	existing, _, _, err := i.scanLocked("/rgb11", nil, 0, true, height, now)
	if err != nil {
		return err
	}
	finalByKey := make(map[string]*wire.DKVSRecord, len(existing)+len(incomingByKey))
	for key, record := range incomingByKey {
		finalByKey[key] = record
	}
	for _, record := range existing {
		if record == nil {
			continue
		}
		parsed, err := ParseKey(record.Key)
		if err != nil || parsed.Namespace != RGB11RegistryNamespace {
			continue
		}
		_, contractID, err := DecodeRGB11RegistryValue(record.Value)
		if err != nil {
			return ErrInvalidRecord
		}
		if incoming, present := incomingByKey[record.Key]; present {
			// The complete business identity is type + ContractID. A valid
			// replacement signature or newer issue height cannot rename it.
			if !bytes.Equal(incoming.Value, record.Value) {
				return ErrWriteConflict
			}
		} else if _, replaced := replacedKeys[record.Key]; replaced {
			return ErrInvalidRecord
		}
		if incomingKey, duplicate := incomingByContract[contractID]; duplicate && incomingKey != record.Key {
			return ErrInvalidRecord
		}
		finalByKey[record.Key] = record
	}

	// Validate the post-commit view, not merely the incoming batch. A partial
	// merge containing ordinal 2 is valid when ordinal 1 already exists. A
	// self-consistent snapshot/root must not authorize an ordinal gap.
	paths := make(map[string][]*wire.DKVSRecord)
	for _, record := range finalByKey {
		parsed, err := ParseKey(record.Key)
		if err != nil {
			return err
		}
		provider, ticker, _, err := parseRGB11RegistryKey(parsed)
		if err != nil {
			return err
		}
		path := "/rgb11/" + provider + "/" + ticker
		paths[path] = append(paths[path], record)
	}
	for path, pathRecords := range paths {
		if err := validateRGB11PathRecords(path, pathRecords); err != nil {
			return err
		}
	}
	return nil
}

func (i *Indexer) validateRGB11RegistryInsertLocked(record *wire.DKVSRecord, parsed ParsedKey,
	height, now uint64) error {

	provider, ticker, ordinal, err := parseRGB11RegistryKey(parsed)
	if err != nil {
		return err
	}
	if _, _, err := DecodeRGB11RegistryValue(record.Value); err != nil {
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
	_, added, _ := DecodeRGB11RegistryValue(record.Value)
	all, _, _, err := i.scanLocked("/rgb11", nil, 0, true, height, now)
	if err != nil {
		return err
	}
	for _, candidate := range all {
		if candidate == nil || candidate.Key == record.Key {
			continue
		}
		_, id, err := DecodeRGB11RegistryValue(candidate.Value)
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
		_, contractID, err := DecodeRGB11RegistryValue(record.Value)
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
	if i == nil || len(contractID) != RGB11RegistryContractBytes*2 ||
		contractID != strings.ToLower(contractID) {
		return nil, ErrInvalidRecord
	}
	if raw, err := hex.DecodeString(contractID); err != nil || len(raw) != RGB11RegistryContractBytes ||
		strings.Trim(contractID, "0") == "" {
		return nil, ErrInvalidRecord
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
	if i == nil || (!strings.HasPrefix(assetName, "rgb11:"+indexercommon.ASSET_TYPE_FT+":") &&
		!strings.HasPrefix(assetName, "rgb11:"+indexercommon.ASSET_TYPE_NFT+":")) {
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
