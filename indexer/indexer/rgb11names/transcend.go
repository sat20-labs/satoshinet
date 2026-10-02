package rgb11names

import (
	"fmt"
	"strings"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	TranscendTemplateName        = "transcend.tc"
	RGB11RegistrationDescriptor = "rgb11-reg-v1"
)

// ParseTranscendRegistration extracts the optional RGB11 descriptor appended
// to a legacy transcend.tc contract payload. The descriptor never contains the
// final SatoshiNet name or ordinal; those are assigned by the naming registry.
func ParseTranscendRegistration(contractPath string, content []byte, params *chaincfg.Params) (*Register, error) {
	tokenizer := txscript.MakeScriptTokenizer(0, content)
	nextData := func(label string) ([]byte, error) {
		if !tokenizer.Next() || tokenizer.Err() != nil || tokenizer.Data() == nil {
			return nil, fmt.Errorf("%w: missing %s", ErrInvalid, label)
		}
		return append([]byte(nil), tokenizer.Data()...), nil
	}
	templateRaw, err := nextData("contract template")
	if err != nil {
		return nil, err
	}
	if string(templateRaw) != TranscendTemplateName {
		return nil, ErrNotFound
	}
	assetRaw, err := nextData("contract asset")
	if err != nil {
		return nil, err
	}
	asset := wire.NewAssetNameFromString(string(assetRaw))
	if asset == nil || asset.Protocol != "rgb11" || (asset.Type != "f" && asset.Type != "n") {
		return nil, ErrNotFound
	}
	// start/end block are part of ContractBase but not naming identity.
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return nil, fmt.Errorf("%w: missing start block", ErrInvalid)
	}
	_ = tokenizer.ExtractInt64()
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return nil, fmt.Errorf("%w: missing end block", ErrInvalid)
	}
	_ = tokenizer.ExtractInt64()

	magic, err := nextData("RGB11 registration marker")
	if err != nil {
		return nil, ErrNotFound
	}
	if string(magic) != RGB11RegistrationDescriptor {
		return nil, ErrNotFound
	}
	contractIDRaw, err := nextData("contract id")
	if err != nil {
		return nil, err
	}
	tickerRaw, err := nextData("base ticker")
	if err != nil {
		return nil, err
	}
	outpointRaw, err := nextData("genesis outpoint")
	if err != nil {
		return nil, err
	}
	addressRaw, err := nextData("genesis address")
	if err != nil {
		return nil, err
	}
	if tokenizer.Next() || tokenizer.Err() != nil {
		return nil, fmt.Errorf("%w: trailing RGB11 registration fields", ErrInvalid)
	}
	contractID := string(contractIDRaw)
	if !validContractID(contractID) || asset.Ticker != contractID {
		return nil, ErrInvalid
	}
	if asset.String()+"_"+TranscendTemplateName != contractPath {
		return nil, ErrInvalid
	}
	base, err := NormalizeTicker(string(tickerRaw))
	if err != nil {
		return nil, err
	}
	outpoint := string(outpointRaw)
	if !validOutpoint(outpoint) {
		return nil, ErrInvalid
	}
	address, err := canonicalAddress(string(addressRaw), params)
	if err != nil {
		return nil, err
	}
	return &Register{
		ContractID: contractID, BaseTicker: base, AssetType: asset.Type,
		GenesisOutpoint: outpoint, GenesisAddress: address,
	}, nil
}

// EncodeTranscendRegistrationSuffix is kept in the indexer package as the
// canonical wire fixture for tests and SDK cross-checks. Production contract
// construction lives in sat20wallet.
func EncodeTranscendRegistrationSuffix(contractID, ticker, outpoint, address string) ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(RGB11RegistrationDescriptor)).
		AddData([]byte(contractID)).
		AddData([]byte(ticker)).
		AddData([]byte(outpoint)).
		AddData([]byte(strings.TrimSpace(address))).
		Script()
}
