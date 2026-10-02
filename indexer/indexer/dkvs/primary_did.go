package dkvs

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sat20-labs/satoshinet/wire"
)

const (
	PrimaryDIDPersonalPath  = "primary_did"
	MaxPrimaryDIDCharacters = 10
)

func AccountPrimaryDIDKey(accountID string) (string, error) {
	return AccountPersonalKey(accountID, PrimaryDIDPersonalPath)
}

func ValidatePrimaryDIDName(did string) error {
	if len(did) == 0 || len(did) > MaxPrimaryDIDCharacters*utf8.UTFMax ||
		!utf8.ValidString(did) || utf8.RuneCountInString(did) > MaxPrimaryDIDCharacters ||
		did != strings.ToLower(did) || did != strings.TrimSpace(did) {
		return ErrInvalidRecord
	}
	for _, r := range did {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) ||
			strings.ContainsRune("@:/\\", r) {
			return ErrInvalidRecord
		}
	}
	return nil
}

func isPrimaryDIDRecord(record *wire.DKVSRecord, parsed ParsedKey) bool {
	return record != nil && parsed.Namespace == "personal" &&
		len(parsed.Segments) == 2 && parsed.Segments[1] == PrimaryDIDPersonalPath
}

func validatePrimaryDIDRecordWith(record *wire.DKVSRecord, parsed ParsedKey, resolver DIDResolver) error {
	if !isPrimaryDIDRecord(record, parsed) {
		return nil
	}
	pubKey := record.PubKey
	if len(pubKey) == 0 {
		var err error
		pubKey, err = AccountPubKey(parsed.Segments[0])
		if err != nil {
			return ErrPermissionDenied
		}
	} else if parsed.Segments[0] != AccountID(pubKey) {
		return ErrPermissionDenied
	}
	if IsTombstone(record.Flags) {
		return nil
	}
	did := string(record.Value)
	if err := ValidatePrimaryDIDName(did); err != nil {
		return err
	}
	if resolver == nil {
		return ErrDIDResolverUnavailable
	}
	identity, err := resolver.ResolveName(did)
	if err != nil {
		return err
	}
	if !identity.Active || identity.CanonicalName != did {
		return ErrPermissionDenied
	}
	return identity.CanSign(pubKey)
}
