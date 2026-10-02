package rgb11names

import (
	"fmt"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	sncommon "github.com/sat20-labs/satoshinet/indexer/common"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/txscript"
)

const primaryDIDBindDomain = "satoshinet-primary-did-bind-v1"

type ParsedPrimaryDIDBind struct {
	DID     string
	Address string
	PubKey  []byte
}

func PrimaryDIDBindSigningPayload(did string, params *chaincfg.Params) ([]byte, error) {
	if ValidateDID(did) != nil || params == nil {
		return nil, ErrInvalid
	}
	return []byte(primaryDIDBindDomain + "|" + params.Name + "|" + did), nil
}

// EncodePrimaryDIDBindPayload encodes the payload later wrapped by the generic
// SatoshiNet OP_RETURN envelope. The caller signs PrimaryDIDBindSigningPayload
// with the same key whose P2TR address owns the Ordinals DID.
func EncodePrimaryDIDBindPayload(did string, pubKey, signature []byte) ([]byte, error) {
	if ValidateDID(did) != nil || len(pubKey) == 0 || len(signature) == 0 {
		return nil, ErrInvalid
	}
	return txscript.NewScriptBuilder().
		AddData([]byte(did)).
		AddData(pubKey).
		AddData(signature).
		Script()
}

// ParsePrimaryDIDBindPayload verifies address control. L1 DID ownership is
// deliberately checked by the caller through the existing L1 DID resolver.
func ParsePrimaryDIDBindPayload(data []byte, params *chaincfg.Params) (*ParsedPrimaryDIDBind, error) {
	if params == nil {
		return nil, ErrInvalid
	}
	tokenizer := txscript.MakeScriptTokenizer(0, data)
	next := func(label string) ([]byte, error) {
		if !tokenizer.Next() || tokenizer.Err() != nil || tokenizer.Data() == nil {
			return nil, fmt.Errorf("%w: missing %s", ErrInvalid, label)
		}
		return append([]byte(nil), tokenizer.Data()...), nil
	}
	didRaw, err := next("did")
	if err != nil {
		return nil, err
	}
	did := strings.TrimSpace(string(didRaw))
	if did != string(didRaw) || ValidateDID(did) != nil {
		return nil, ErrInvalid
	}
	pubKey, err := next("pubkey")
	if err != nil {
		return nil, err
	}
	sigRaw, err := next("signature")
	if err != nil {
		return nil, err
	}
	if tokenizer.Next() || tokenizer.Err() != nil {
		return nil, fmt.Errorf("%w: trailing primary DID bind fields", ErrInvalid)
	}
	pk, err := secp256k1.ParsePubKey(pubKey)
	if err != nil {
		return nil, ErrInvalid
	}
	sig, err := ecdsa.ParseDERSignature(sigRaw)
	if err != nil {
		return nil, ErrInvalid
	}
	signingPayload, err := PrimaryDIDBindSigningPayload(did, params)
	if err != nil || !sncommon.VerifyMessage(pk, signingPayload, sig) {
		return nil, ErrOwner
	}
	address, err := dkvs.P2TRAddressFromPubKeyBytes(pubKey, params)
	if err != nil {
		return nil, ErrInvalid
	}
	return &ParsedPrimaryDIDBind{DID: did, Address: address, PubKey: pubKey}, nil
}
