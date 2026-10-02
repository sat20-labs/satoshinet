package rgb11names

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	TranscendTemplateName        = "transcend.tc"
	RGB11RegistrationDescriptor = "rgb11-reg-v1"
)

// ParseTranscendRegistration extracts the minimal RGB11 descriptor appended to
// transcend.tc content. ContractID and asset type are already encoded in the
// contract asset name and are never duplicated in the descriptor.
func ParseTranscendRegistration(contractPath string, content []byte) (*Register, error) {
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
	if asset == nil || asset.Protocol != "rgb11" || (asset.Type != "f" && asset.Type != "n") ||
		!validContractID(asset.Ticker) {
		return nil, ErrNotFound
	}
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
	tickerRaw, err := nextData("base ticker")
	if err != nil {
		return nil, err
	}
	outpointRaw, err := nextData("genesis outpoint")
	if err != nil {
		return nil, err
	}
	providerRaw, err := nextData("provider DID")
	if err != nil {
		return nil, err
	}
	if tokenizer.Next() || tokenizer.Err() != nil {
		return nil, fmt.Errorf("%w: trailing RGB11 registration fields", ErrInvalid)
	}
	if asset.String()+"_"+TranscendTemplateName != contractPath {
		return nil, ErrInvalid
	}
	base, err := NormalizeTicker(string(tickerRaw))
	if err != nil {
		return nil, err
	}
	outpoint := string(outpointRaw)
	if !validOutpoint(outpoint) || ValidateDID(string(providerRaw)) != nil {
		return nil, ErrInvalid
	}
	return &Register{
		ContractID: asset.Ticker, BaseTicker: base, AssetType: asset.Type,
		GenesisOutpoint: outpoint, ProviderDID: string(providerRaw),
	}, nil
}

// EncodeTranscendRegistrationSuffix is the canonical test fixture. Production
// construction lives in sat20wallet.
func EncodeTranscendRegistrationSuffix(ticker, outpoint, providerDID string) ([]byte, error) {
	if _, err := NormalizeTicker(ticker); err != nil || !validOutpoint(outpoint) || ValidateDID(providerDID) != nil {
		return nil, ErrInvalid
	}
	return txscript.NewScriptBuilder().
		AddData([]byte(RGB11RegistrationDescriptor)).
		AddData([]byte(ticker)).
		AddData([]byte(outpoint)).
		AddData([]byte(providerDID)).
		Script()
}
