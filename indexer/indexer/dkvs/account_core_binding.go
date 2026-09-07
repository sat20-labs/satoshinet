package dkvs

import "github.com/sat20-labs/satoshinet/wire"

// isAccountMappingBindingControlRecord recognizes the one free account
// control KV. It is globally replicated, has no fee proof or lifetime, and
// simultaneously maps the root address and binds the root account to a
// service CoreNode.
func isAccountMappingBindingControlRecord(record *wire.DKVSRecord, parsed ParsedKey) bool {
	if record == nil || parsed.Namespace != "account" || len(parsed.Segments) != 2 ||
		len(record.PubKey) != 0 || record.TTL != 0 || record.Flags != 0 || len(record.FeeProof) != 0 {
		return false
	}
	_, err := DecodeAccountServiceDescriptor(record.Value)
	return err == nil
}

func IsAccountMappingBindingKey(key string) bool {
	parsed, err := ParseKey(key)
	return err == nil && parsed.Namespace == "account" && len(parsed.Segments) == 2
}

// ValidateAccountMappingBindingRecord verifies the root-owned mapping/binding
// record. CoreNode acceptance remains a separate local fact: publishing a
// signed KV cannot force a CoreNode to serve the account.
func ValidateAccountMappingBindingRecord(record *wire.DKVSRecord) (network, address string,
	descriptor *AccountServiceDescriptor, err error) {
	if record == nil {
		return "", "", nil, ErrInvalidRecord
	}
	parsed, err := ParseKey(record.Key)
	if err != nil || !isAccountMappingBindingControlRecord(record, parsed) {
		return "", "", nil, ErrInvalidRecord
	}
	if err := VerifySignature(record); err != nil {
		return "", "", nil, err
	}
	if err := ValidateRecordIdentity(record, parsed); err != nil {
		return "", "", nil, err
	}
	descriptor, err = DecodeAccountServiceDescriptor(record.Value)
	if err != nil {
		return "", "", nil, err
	}
	return parsed.Segments[0], parsed.Segments[1], descriptor, nil
}
