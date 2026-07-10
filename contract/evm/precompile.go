package evm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	scommon "github.com/sat20-labs/indexer/common"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

var (
	// AssetPrecompileAddress is the SatoshiNet native asset interface inside EVM.
	AssetPrecompileAddress = gethcommon.HexToAddress("0x0000000000000000000000000000000000534e01")
	// TriggerPrecompileAddress is the SatoshiNet contract trigger registry interface inside EVM.
	TriggerPrecompileAddress = gethcommon.HexToAddress("0x0000000000000000000000000000000000534e02")

	assetBalanceOfSelector         = methodSelector("balanceOf(address,string)")
	assetTransferAssetSelector     = methodSelector("transferAsset(string,string,string,bytes)")
	assetTransferAssetsSelector    = methodSelector("transferAssets(string[],string[],string[],bytes[])")
	assetFundingAssetSelector      = methodSelector("fundingAssetAmount(string)")
	assetFundingSatsSelector       = methodSelector("fundingSats()")
	assetClaimFundingSelector      = methodSelector("claimFundingAsset(string,string)")
	assetCallerAddressSelector     = methodSelector("callerAddress()")
	assetCompareAmountSelector     = methodSelector("compareAmount(string,string)")
	assetAddAmountSelector         = methodSelector("addAmount(string,string)")
	assetSubAmountSelector         = methodSelector("subAmount(string,string)")
	assetMulAmountSelector         = methodSelector("mulAmount(string,string)")
	assetDivAmountSelector         = methodSelector("divAmount(string,string)")
	assetUintToAmountSelector      = methodSelector("uintToAmount(uint256)")
	assetAmountToUintFloorSelector = methodSelector("amountToUintFloor(string)")
	assetAmountToUintCeilSelector  = methodSelector("amountToUintCeil(string)")
	triggerRegisterHeightSelector  = methodSelector("registerHeightTrigger(string,uint256,uint256,bytes)")
)

const evmAmountMaxPrecision = scommon.MAX_PRECISION - 1

type AssetBalanceReader interface {
	AssetBalance(owner EVMAddress, assetName string) (*scommon.Decimal, error)
}

type FundingAmountReader interface {
	FundingAssetAmount(assetName string) (*scommon.Decimal, error)
	ClaimFundingAsset(assetName string, amount *scommon.Decimal) error
}

type UTXOAssetView struct {
	UTXOs []UTXO
}

type FundingAssetView struct {
	Outputs       []contractframework.ContractOutput
	GasAssetName  string
	GasFeeReserve *scommon.Decimal
	claimed       map[string]*scommon.Decimal
}

type fundingAssetSnapshot map[string]*scommon.Decimal

func NewFundingAssetView(outputs []contractframework.ContractOutput, gasAssetName string,
	gasFeeReserve *scommon.Decimal) *FundingAssetView {

	cp := make([]contractframework.ContractOutput, len(outputs))
	copy(cp, outputs)
	return &FundingAssetView{
		Outputs:       cp,
		GasAssetName:  gasAssetName,
		GasFeeReserve: contractframework.CloneDecimal(gasFeeReserve),
		claimed:       make(map[string]*scommon.Decimal),
	}
}

type ContractUTXOAssetView struct {
	Prefix   string
	Provider ContractUTXOProvider
}

type FundingOverlayAssetBalanceView struct {
	Base     AssetBalanceReader
	Funding  FundingAmountReader
	Contract EVMAddress
}

func NewUTXOAssetView(utxos []UTXO) UTXOAssetView {
	cp := make([]UTXO, len(utxos))
	copy(cp, utxos)
	return UTXOAssetView{UTXOs: cp}
}

func NewContractUTXOAssetView(prefix string, provider ContractUTXOProvider) ContractUTXOAssetView {
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	return ContractUTXOAssetView{Prefix: prefix, Provider: provider}
}

func NewFundingOverlayAssetBalanceView(base AssetBalanceReader, funding FundingAmountReader,
	contract EVMAddress) FundingOverlayAssetBalanceView {

	return FundingOverlayAssetBalanceView{
		Base:     base,
		Funding:  funding,
		Contract: contract,
	}
}

func (v UTXOAssetView) AssetBalance(owner EVMAddress, assetName string) (*scommon.Decimal, error) {
	if assetName == "" {
		return nil, ErrInvalidAsset
	}
	total := zeroDecimal()
	for _, u := range v.UTXOs {
		if ContractAddressHash(u.Contract) != owner {
			continue
		}
		amount, err := u.AssetAmount(assetName)
		if err != nil {
			return nil, err
		}
		total = total.AddAlignPrecision(amount)
	}
	return total, nil
}

func (v ContractUTXOAssetView) AssetBalance(owner EVMAddress, assetName string) (*scommon.Decimal, error) {
	if assetName == "" {
		return nil, ErrInvalidAsset
	}
	if v.Provider == nil {
		return nil, errors.New("contract UTXO provider is not configured")
	}
	contractAddr, err := NewContractAddress(v.Prefix, AddressVersionV1, ContractTypeEVM, owner)
	if err != nil {
		return nil, err
	}
	utxos, err := v.Provider(contractAddr)
	if err != nil {
		return nil, err
	}
	return contractframework.SumUTXOAssetAmount(utxos, assetName)
}

func (v FundingOverlayAssetBalanceView) AssetBalance(owner EVMAddress, assetName string) (*scommon.Decimal, error) {
	if assetName == "" {
		return nil, ErrInvalidAsset
	}
	total := zeroDecimal()
	if v.Base != nil {
		base, err := v.Base.AssetBalance(owner, assetName)
		if err != nil {
			return nil, err
		}
		if base != nil {
			total = total.AddAlignPrecision(base)
		}
	}
	if owner != v.Contract || v.Funding == nil {
		return total, nil
	}
	funding, err := v.Funding.FundingAssetAmount(assetName)
	if err != nil {
		return nil, err
	}
	if funding != nil {
		total = total.AddAlignPrecision(funding)
	}
	return total, nil
}

func (v *FundingAssetView) FundingAssetAmount(assetName string) (*scommon.Decimal, error) {
	if v == nil {
		return nil, errors.New("funding reader is not configured")
	}
	if assetName == "" {
		return nil, ErrInvalidAsset
	}
	total := zeroDecimal()
	if assetName == SatoshiAssetName {
		for _, output := range v.Outputs {
			if plain := output.PlainValue(); plain > 0 {
				total = total.AddAlignPrecision(scommon.NewDefaultDecimal(plain))
			}
		}
	} else {
		for _, output := range v.Outputs {
			for _, asset := range output.TxAssets() {
				if asset.Name.String() == assetName && asset.Amount.Sign() > 0 {
					total = total.AddAlignPrecision(asset.Amount.Clone())
				}
			}
		}
	}
	if assetName != v.GasAssetName {
		return total, nil
	}
	reserve := contractframework.CloneDecimal(v.GasFeeReserve)
	if reserve.Sign() < 0 {
		return nil, errors.New("gas fee reserve is negative")
	}
	if total.Cmp(reserve) <= 0 {
		return zeroDecimal(), nil
	}
	return total.SubAlignPrecision(reserve), nil
}

func (v *FundingAssetView) ClaimFundingAsset(assetName string, amount *scommon.Decimal) error {
	if v == nil {
		return errors.New("funding reader is not configured")
	}
	if assetName == "" {
		return ErrInvalidAsset
	}
	if amount == nil || amount.Sign() <= 0 {
		return errors.New("claim amount must be positive")
	}
	available, err := v.FundingAssetAmount(assetName)
	if err != nil {
		return err
	}
	next := amount.Clone()
	if existing := v.claimed[assetName]; existing != nil {
		next = existing.AddAlignPrecision(next)
	}
	if next.Cmp(available) > 0 {
		return fmt.Errorf("claimed funding asset %s amount %s exceeds available %s",
			assetName, next.String(), available.String())
	}
	v.claimed[assetName] = next
	return nil
}

func (v *FundingAssetView) ClaimedAssetAmount(assetName string) *scommon.Decimal {
	if v == nil || assetName == "" {
		return zeroDecimal()
	}
	if amount := v.claimed[assetName]; amount != nil {
		return amount.Clone()
	}
	return zeroDecimal()
}

func (v *FundingAssetView) Snapshot() fundingAssetSnapshot {
	if v == nil || len(v.claimed) == 0 {
		return nil
	}
	out := make(fundingAssetSnapshot, len(v.claimed))
	for name, amount := range v.claimed {
		out[name] = contractframework.CloneDecimal(amount)
	}
	return out
}

func (v *FundingAssetView) RevertTo(snapshot fundingAssetSnapshot) {
	if v == nil {
		return
	}
	v.claimed = make(map[string]*scommon.Decimal, len(snapshot))
	for name, amount := range snapshot {
		v.claimed[name] = contractframework.CloneDecimal(amount)
	}
}

type AssetPrecompile struct {
	Balances      AssetBalanceReader
	Funding       FundingAmountReader
	CallerAddress string
}

func NewAssetPrecompile(balances AssetBalanceReader, funding FundingAmountReader, callerAddress ...string) *AssetPrecompile {
	addr := ""
	if len(callerAddress) > 0 {
		addr = callerAddress[0]
	}
	return &AssetPrecompile{Balances: balances, Funding: funding, CallerAddress: addr}
}

func (p *AssetPrecompile) RequiredGas(input []byte) uint64 {
	words := uint64((len(input) + 31) / 32)
	return 700 + words*12
}

func (p *AssetPrecompile) Run(input []byte) ([]byte, error) {
	selector, args, err := splitSelector(input)
	if err != nil {
		return nil, err
	}
	switch selector {
	case assetBalanceOfSelector:
		owner, assetName, err := decodeBalanceOf(args)
		if err != nil {
			return nil, err
		}
		if p.Balances == nil {
			return nil, errors.New("asset balance reader is not configured")
		}
		balance, err := p.Balances.AssetBalance(owner, assetName)
		if err != nil {
			return nil, err
		}
		if balance == nil {
			balance = zeroDecimal()
		}
		return abiEncodeDynamicBytes([]byte(balance.String())), nil
	case assetTransferAssetSelector:
		if _, _, _, _, err := DecodeTransferAssetCall(input); err != nil {
			return nil, err
		}
		return abiEncodeBool(true), nil
	case assetTransferAssetsSelector:
		if _, err := DecodeTransferAssetsCall(input); err != nil {
			return nil, err
		}
		return abiEncodeBool(true), nil
	case assetFundingAssetSelector:
		assetName, err := abiReadString(args, 0)
		if err != nil {
			return nil, err
		}
		if p.Funding == nil {
			return nil, errors.New("funding reader is not configured")
		}
		amount, err := p.Funding.FundingAssetAmount(assetName)
		if err != nil {
			return nil, err
		}
		if amount == nil {
			amount = zeroDecimal()
		}
		return abiEncodeDynamicBytes([]byte(amount.String())), nil
	case assetFundingSatsSelector:
		if len(args) != 0 {
			return nil, errors.New("fundingSats takes no arguments")
		}
		if p.Funding == nil {
			return nil, errors.New("funding reader is not configured")
		}
		amount, err := p.Funding.FundingAssetAmount(SatoshiAssetName)
		if err != nil {
			return nil, err
		}
		value, err := contractframework.DecimalToInt64(*amount)
		if err != nil {
			return nil, err
		}
		if value < 0 {
			return nil, errors.New("funding sats is negative")
		}
		return abiEncodeUint64(uint64(value)), nil
	case assetClaimFundingSelector:
		assetName, err := abiReadString(args, 0)
		if err != nil {
			return nil, err
		}
		amountText, err := abiReadString(args, 1)
		if err != nil {
			return nil, err
		}
		amount, err := ParseDecimalAmountString(amountText)
		if err != nil {
			return nil, err
		}
		if p.Funding == nil {
			return nil, errors.New("funding reader is not configured")
		}
		if err := p.Funding.ClaimFundingAsset(assetName, amount); err != nil {
			return nil, err
		}
		return abiEncodeBool(true), nil
	case assetCallerAddressSelector:
		if len(args) != 0 {
			return nil, errors.New("callerAddress takes no arguments")
		}
		if p.CallerAddress == "" {
			return nil, errors.New("caller address is not configured")
		}
		return abiEncodeDynamicBytes([]byte(p.CallerAddress)), nil
	case assetCompareAmountSelector:
		left, right, err := decodeAmountPair(args)
		if err != nil {
			return nil, err
		}
		cmp := left.Cmp(right)
		if cmp < 0 {
			cmp = -1
		} else if cmp > 0 {
			cmp = 1
		}
		return abiEncodeInt64(int64(cmp)), nil
	case assetAddAmountSelector:
		result, err := runAmountBinaryOp(args, func(left, right *scommon.Decimal) (*scommon.Decimal, error) {
			return left.AddAlignPrecision(right), nil
		})
		if err != nil {
			return nil, err
		}
		return abiEncodeDynamicBytes([]byte(result.String())), nil
	case assetSubAmountSelector:
		result, err := runAmountBinaryOp(args, func(left, right *scommon.Decimal) (*scommon.Decimal, error) {
			result := left.SubAlignPrecision(right)
			if result.Sign() < 0 {
				return nil, errors.New("amount subtraction underflows")
			}
			return result, nil
		})
		if err != nil {
			return nil, err
		}
		return abiEncodeDynamicBytes([]byte(result.String())), nil
	case assetMulAmountSelector:
		result, err := runAmountBinaryOp(args, func(left, right *scommon.Decimal) (*scommon.Decimal, error) {
			return left.MulV2(right), nil
		})
		if err != nil {
			return nil, err
		}
		return abiEncodeDynamicBytes([]byte(result.String())), nil
	case assetDivAmountSelector:
		result, err := runAmountBinaryOp(args, func(left, right *scommon.Decimal) (*scommon.Decimal, error) {
			if right.Sign() == 0 {
				return nil, errors.New("amount division by zero")
			}
			result := left.NewPrecision(evmAmountMaxPrecision).Div(right)
			if result == nil {
				return nil, errors.New("amount division failed")
			}
			return result, nil
		})
		if err != nil {
			return nil, err
		}
		return abiEncodeDynamicBytes([]byte(result.String())), nil
	case assetUintToAmountSelector:
		value, err := abiReadUint64(args, 0)
		if err != nil {
			return nil, err
		}
		if value > math.MaxInt64 {
			return nil, errors.New("uint amount overflows int64")
		}
		return abiEncodeDynamicBytes([]byte(scommon.NewDefaultDecimal(int64(value)).String())), nil
	case assetAmountToUintFloorSelector:
		amountText, err := abiReadString(args, 0)
		if err != nil {
			return nil, err
		}
		amount, err := ParseDecimalAmountString(amountText)
		if err != nil {
			return nil, err
		}
		if amount.Sign() < 0 {
			return nil, errors.New("amount must be non-negative")
		}
		return abiEncodeUint64(uint64(amount.Floor())), nil
	case assetAmountToUintCeilSelector:
		amountText, err := abiReadString(args, 0)
		if err != nil {
			return nil, err
		}
		amount, err := ParseDecimalAmountString(amountText)
		if err != nil {
			return nil, err
		}
		if amount.Sign() < 0 {
			return nil, errors.New("amount must be non-negative")
		}
		return abiEncodeUint64(uint64(amount.Ceil())), nil
	default:
		return nil, fmt.Errorf("unknown asset precompile selector 0x%x", selector)
	}
}

func (p *AssetPrecompile) Name() string {
	return "satoshinetAsset"
}

type TriggerPrecompile struct{}

func NewTriggerPrecompile() *TriggerPrecompile {
	return &TriggerPrecompile{}
}

func (p *TriggerPrecompile) RequiredGas(input []byte) uint64 {
	words := uint64((len(input) + 31) / 32)
	return 700 + words*12
}

func (p *TriggerPrecompile) Run(input []byte) ([]byte, error) {
	if _, err := DecodeTriggerRegistrationCall(input); err != nil {
		return nil, err
	}
	return abiEncodeBool(true), nil
}

func (p *TriggerPrecompile) Name() string {
	return "satoshinetTrigger"
}

func SatoshiNetPrecompiles(balances AssetBalanceReader, funding FundingAmountReader, callerAddress string,
	rules vm.PrecompiledContracts) vm.PrecompiledContracts {

	out := make(vm.PrecompiledContracts, len(rules)+2)
	for addr, p := range rules {
		out[addr] = p
	}
	out[AssetPrecompileAddress] = NewAssetPrecompile(balances, funding, callerAddress)
	out[TriggerPrecompileAddress] = NewTriggerPrecompile()
	return out
}

func DecodeTransferAssetCall(input []byte) (assetName, to string, amount *scommon.Decimal, extraData []byte, err error) {
	selector, args, err := splitSelector(input)
	if err != nil {
		return "", "", nil, nil, err
	}
	if selector != assetTransferAssetSelector {
		return "", "", nil, nil, errors.New("not a transferAsset call")
	}
	assetName, err = abiReadString(args, 0)
	if err != nil {
		return "", "", nil, nil, err
	}
	to, err = abiReadString(args, 1)
	if err != nil {
		return "", "", nil, nil, err
	}
	amountText, err := abiReadString(args, 2)
	if err != nil {
		return "", "", nil, nil, err
	}
	amount, err = ParseDecimalAmountString(amountText)
	if err != nil {
		return "", "", nil, nil, err
	}
	if amount.Sign() < 0 {
		return "", "", nil, nil, errors.New("asset amount must be non-negative")
	}
	extraData, err = abiReadDynamicBytes(args, 3)
	if err != nil {
		return "", "", nil, nil, err
	}
	return assetName, to, amount, extraData, nil
}

type AssetTransferRequest struct {
	AssetName string
	To        string
	Amount    *scommon.Decimal
	ExtraData []byte
}

func DecodeTransferAssetsCall(input []byte) ([]AssetTransferRequest, error) {
	selector, args, err := splitSelector(input)
	if err != nil {
		return nil, err
	}
	if selector != assetTransferAssetsSelector {
		return nil, errors.New("not a transferAssets call")
	}
	assetNames, err := abiReadStringArray(args, 0)
	if err != nil {
		return nil, err
	}
	recipients, err := abiReadStringArray(args, 1)
	if err != nil {
		return nil, err
	}
	amountTexts, err := abiReadStringArray(args, 2)
	if err != nil {
		return nil, err
	}
	extraData, err := abiReadBytesArray(args, 3)
	if err != nil {
		return nil, err
	}
	if len(assetNames) == 0 {
		return nil, errors.New("transferAssets requires at least one transfer")
	}
	if len(assetNames) != len(recipients) || len(assetNames) != len(amountTexts) || len(assetNames) != len(extraData) {
		return nil, errors.New("transferAssets array length mismatch")
	}
	out := make([]AssetTransferRequest, len(assetNames))
	for i := range assetNames {
		amount, err := ParseDecimalAmountString(amountTexts[i])
		if err != nil {
			return nil, err
		}
		if amount.Sign() <= 0 {
			return nil, errors.New("transferAssets amount must be positive")
		}
		out[i] = AssetTransferRequest{
			AssetName: assetNames[i],
			To:        recipients[i],
			Amount:    amount,
			ExtraData: contractframework.CloneBytes(extraData[i]),
		}
	}
	return out, nil
}

func DecodeAssetTransferIntents(input []byte) ([]AssetTransferRequest, error) {
	if assetName, to, amount, extraData, err := DecodeTransferAssetCall(input); err == nil {
		return []AssetTransferRequest{{
			AssetName: assetName,
			To:        to,
			Amount:    amount,
			ExtraData: extraData,
		}}, nil
	}
	return DecodeTransferAssetsCall(input)
}

func EncodeBalanceOfCall(owner EVMAddress, assetName string) []byte {
	args := make([]byte, 64)
	copy(args[12:32], owner[:])
	putABIUint64(args[32:64], 64)
	args = append(args, abiEncodeDynamicBytes([]byte(assetName))...)
	return appendMethod(assetBalanceOfSelector, args)
}

func EncodeTransferAssetCall(assetName, to, amount string, extraData []byte) []byte {
	head := make([]byte, 128)
	tail := make([]byte, 0)
	putABIUint64(head[0:32], 128+uint64(len(tail)))
	tail = append(tail, abiEncodeDynamicBytes([]byte(assetName))...)
	putABIUint64(head[32:64], 128+uint64(len(tail)))
	tail = append(tail, abiEncodeDynamicBytes([]byte(to))...)
	putABIUint64(head[64:96], 128+uint64(len(tail)))
	tail = append(tail, abiEncodeDynamicBytes([]byte(amount))...)
	putABIUint64(head[96:128], 128+uint64(len(tail)))
	tail = append(tail, abiEncodeDynamicBytes(extraData)...)
	return appendMethod(assetTransferAssetSelector, append(head, tail...))
}

func EncodeTransferAssetsCall(assetNames, recipients, amounts []string, extraData [][]byte) []byte {
	head := make([]byte, 128)
	tail := make([]byte, 0)
	putABIUint64(head[0:32], 128+uint64(len(tail)))
	tail = append(tail, abiEncodeStringArray(assetNames)...)
	putABIUint64(head[32:64], 128+uint64(len(tail)))
	tail = append(tail, abiEncodeStringArray(recipients)...)
	putABIUint64(head[64:96], 128+uint64(len(tail)))
	tail = append(tail, abiEncodeStringArray(amounts)...)
	putABIUint64(head[96:128], 128+uint64(len(tail)))
	tail = append(tail, abiEncodeBytesArray(extraData)...)
	return appendMethod(assetTransferAssetsSelector, append(head, tail...))
}

func EncodeFundingAssetAmountCall(assetName string) []byte {
	args := make([]byte, 32)
	putABIUint64(args, 32)
	args = append(args, abiEncodeDynamicBytes([]byte(assetName))...)
	return appendMethod(assetFundingAssetSelector, args)
}

func EncodeFundingSatsCall() []byte {
	return assetFundingSatsSelector[:]
}

func EncodeCallerAddressCall() []byte {
	return assetCallerAddressSelector[:]
}

func EncodeClaimFundingAssetCall(assetName, amount string) []byte {
	head := make([]byte, 64)
	tail := make([]byte, 0)
	putABIUint64(head[0:32], 64+uint64(len(tail)))
	tail = append(tail, abiEncodeDynamicBytes([]byte(assetName))...)
	putABIUint64(head[32:64], 64+uint64(len(tail)))
	tail = append(tail, abiEncodeDynamicBytes([]byte(amount))...)
	return appendMethod(assetClaimFundingSelector, append(head, tail...))
}

func EncodeCompareAmountCall(left, right string) []byte {
	return encodeAmountPairCall(assetCompareAmountSelector, left, right)
}

func EncodeAddAmountCall(left, right string) []byte {
	return encodeAmountPairCall(assetAddAmountSelector, left, right)
}

func EncodeSubAmountCall(left, right string) []byte {
	return encodeAmountPairCall(assetSubAmountSelector, left, right)
}

func EncodeMulAmountCall(left, right string) []byte {
	return encodeAmountPairCall(assetMulAmountSelector, left, right)
}

func EncodeDivAmountCall(left, right string) []byte {
	return encodeAmountPairCall(assetDivAmountSelector, left, right)
}

func EncodeUintToAmountCall(value uint64) []byte {
	return appendMethod(assetUintToAmountSelector, abiEncodeUint64(value))
}

func EncodeAmountToUintFloorCall(amount string) []byte {
	args := make([]byte, 32)
	putABIUint64(args, 32)
	args = append(args, abiEncodeDynamicBytes([]byte(amount))...)
	return appendMethod(assetAmountToUintFloorSelector, args)
}

func EncodeAmountToUintCeilCall(amount string) []byte {
	args := make([]byte, 32)
	putABIUint64(args, 32)
	args = append(args, abiEncodeDynamicBytes([]byte(amount))...)
	return appendMethod(assetAmountToUintCeilSelector, args)
}

func DecodeTriggerRegistrationCall(input []byte) (Trigger, error) {
	selector, args, err := splitSelector(input)
	if err != nil {
		return Trigger{}, err
	}
	switch selector {
	case triggerRegisterHeightSelector:
		id, height, gasLimit, calldata, err := decodeTriggerRegistration(args)
		if err != nil {
			return Trigger{}, err
		}
		return Trigger{
			ID:       id,
			Kind:     TriggerAtHeight,
			Height:   int64(height),
			GasLimit: gasLimit,
			Calldata: calldata,
		}, nil
	default:
		return Trigger{}, fmt.Errorf("unknown trigger precompile selector 0x%x", selector)
	}
}

func EncodeRegisterHeightTriggerCall(id string, height uint64, gasLimit int64, calldata []byte) []byte {
	return encodeTriggerRegistrationCall(triggerRegisterHeightSelector, id, height, gasLimit, calldata)
}

func decodeBalanceOf(args []byte) (EVMAddress, string, error) {
	owner, err := abiReadAddress(args, 0)
	if err != nil {
		return EVMAddress{}, "", err
	}
	assetName, err := abiReadString(args, 1)
	if err != nil {
		return EVMAddress{}, "", err
	}
	return owner, assetName, nil
}

func decodeAmountPair(args []byte) (*scommon.Decimal, *scommon.Decimal, error) {
	leftText, err := abiReadString(args, 0)
	if err != nil {
		return nil, nil, err
	}
	rightText, err := abiReadString(args, 1)
	if err != nil {
		return nil, nil, err
	}
	left, err := ParseDecimalAmountString(leftText)
	if err != nil {
		return nil, nil, err
	}
	right, err := ParseDecimalAmountString(rightText)
	if err != nil {
		return nil, nil, err
	}
	if left.Sign() < 0 || right.Sign() < 0 {
		return nil, nil, errors.New("amount must be non-negative")
	}
	return left, right, nil
}

func runAmountBinaryOp(args []byte, op func(*scommon.Decimal, *scommon.Decimal) (*scommon.Decimal, error)) (*scommon.Decimal, error) {
	left, right, err := decodeAmountPair(args)
	if err != nil {
		return nil, err
	}
	result, err := op(left, right)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("amount operation failed")
	}
	return result, nil
}

func decodeTriggerRegistration(args []byte) (string, uint64, int64, []byte, error) {
	id, err := abiReadString(args, 0)
	if err != nil {
		return "", 0, 0, nil, err
	}
	at, err := abiReadUint64(args, 1)
	if err != nil {
		return "", 0, 0, nil, err
	}
	gasLimit, err := abiReadUint64(args, 2)
	if err != nil {
		return "", 0, 0, nil, err
	}
	gasLimitInt, err := contractframework.GasUnitsInt64(gasLimit)
	if err != nil {
		return "", 0, 0, nil, err
	}
	calldata, err := abiReadDynamicBytes(args, 3)
	if err != nil {
		return "", 0, 0, nil, err
	}
	return id, at, gasLimitInt, calldata, nil
}

func encodeTriggerRegistrationCall(selector [4]byte, id string, at uint64, gasLimit int64, calldata []byte) []byte {
	gasLimitUint, err := contractframework.GasUnitsUint64(gasLimit)
	if err != nil {
		gasLimitUint = 0
	}
	head := make([]byte, 128)
	tail := make([]byte, 0)
	putABIUint64(head[0:32], 128+uint64(len(tail)))
	tail = append(tail, abiEncodeDynamicBytes([]byte(id))...)
	putABIUint64(head[32:64], at)
	putABIUint64(head[64:96], gasLimitUint)
	putABIUint64(head[96:128], 128+uint64(len(tail)))
	tail = append(tail, abiEncodeDynamicBytes(calldata)...)
	return appendMethod(selector, append(head, tail...))
}

func encodeAmountPairCall(selector [4]byte, left, right string) []byte {
	head := make([]byte, 64)
	tail := make([]byte, 0)
	putABIUint64(head[0:32], 64+uint64(len(tail)))
	tail = append(tail, abiEncodeDynamicBytes([]byte(left))...)
	putABIUint64(head[32:64], 64+uint64(len(tail)))
	tail = append(tail, abiEncodeDynamicBytes([]byte(right))...)
	return appendMethod(selector, append(head, tail...))
}

func splitSelector(input []byte) ([4]byte, []byte, error) {
	var selector [4]byte
	if len(input) < 4 {
		return selector, nil, errors.New("precompile input is shorter than selector")
	}
	copy(selector[:], input[:4])
	return selector, input[4:], nil
}

func methodSelector(signature string) [4]byte {
	hash := crypto.Keccak256([]byte(signature))
	var selector [4]byte
	copy(selector[:], hash[:4])
	return selector
}

func appendMethod(selector [4]byte, args []byte) []byte {
	out := make([]byte, 0, 4+len(args))
	out = append(out, selector[:]...)
	out = append(out, args...)
	return out
}

func abiReadAddress(args []byte, index int) (EVMAddress, error) {
	var addr EVMAddress
	word, err := abiReadWord(args, index)
	if err != nil {
		return addr, err
	}
	copy(addr[:], word[12:])
	return addr, nil
}

func abiReadString(args []byte, index int) (string, error) {
	b, err := abiReadDynamicBytes(args, index)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func abiReadStringArray(args []byte, index int) ([]string, error) {
	raw, err := abiReadDynamicArray(args, index)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(raw))
	for i := range raw {
		out[i] = string(raw[i])
	}
	return out, nil
}

func abiReadBytesArray(args []byte, index int) ([][]byte, error) {
	return abiReadDynamicArray(args, index)
}

func abiReadDynamicArray(args []byte, index int) ([][]byte, error) {
	offset, err := abiReadUint64(args, index)
	if err != nil {
		return nil, err
	}
	if offset > uint64(len(args)) || offset+32 > uint64(len(args)) {
		return nil, errors.New("ABI array offset out of bounds")
	}
	if offset%32 != 0 {
		return nil, errors.New("ABI array offset is not word aligned")
	}
	count, err := abiWordToUint64(args[offset : offset+32])
	if err != nil {
		return nil, err
	}
	if count > uint64(math.MaxInt32) {
		return nil, errors.New("ABI array length too large")
	}
	headStart := offset + 32
	headEnd := headStart + count*32
	if headEnd > uint64(len(args)) {
		return nil, errors.New("ABI array head out of bounds")
	}
	out := make([][]byte, int(count))
	for i := 0; i < int(count); i++ {
		itemOffset, err := abiWordToUint64(args[headStart+uint64(i*32) : headStart+uint64((i+1)*32)])
		if err != nil {
			return nil, err
		}
		if itemOffset%32 != 0 {
			return nil, errors.New("ABI array item offset is not word aligned")
		}
		absolute := headStart + itemOffset
		if absolute < headStart || absolute+32 > uint64(len(args)) {
			return nil, errors.New("ABI array item offset out of bounds")
		}
		length, err := abiWordToUint64(args[absolute : absolute+32])
		if err != nil {
			return nil, err
		}
		start := absolute + 32
		end := start + length
		if end > uint64(len(args)) {
			return nil, errors.New("ABI array item out of bounds")
		}
		item := make([]byte, length)
		copy(item, args[start:end])
		out[i] = item
	}
	return out, nil
}

func abiReadDynamicBytes(args []byte, index int) ([]byte, error) {
	offset, err := abiReadUint64(args, index)
	if err != nil {
		return nil, err
	}
	if offset > uint64(len(args)) || offset+32 > uint64(len(args)) {
		return nil, errors.New("ABI dynamic offset out of bounds")
	}
	if offset%32 != 0 {
		return nil, errors.New("ABI dynamic offset is not word aligned")
	}
	lengthWord := args[offset : offset+32]
	length, err := abiWordToUint64(lengthWord)
	if err != nil {
		return nil, err
	}
	start := offset + 32
	end := start + length
	if end > uint64(len(args)) {
		return nil, errors.New("ABI dynamic value out of bounds")
	}
	out := make([]byte, length)
	copy(out, args[start:end])
	return out, nil
}

func abiReadUint64(args []byte, index int) (uint64, error) {
	word, err := abiReadWord(args, index)
	if err != nil {
		return 0, err
	}
	return abiWordToUint64(word)
}

func abiReadWord(args []byte, index int) ([]byte, error) {
	start := index * 32
	end := start + 32
	if start < 0 || end > len(args) {
		return nil, errors.New("ABI word out of bounds")
	}
	return args[start:end], nil
}

func abiWordToUint64(word []byte) (uint64, error) {
	for _, b := range word[:24] {
		if b != 0 {
			return 0, errors.New("ABI uint256 overflows uint64")
		}
	}
	return binary.BigEndian.Uint64(word[24:32]), nil
}

func abiEncodeUint64(v uint64) []byte {
	out := make([]byte, 32)
	putABIUint64(out, v)
	return out
}

func abiEncodeBool(v bool) []byte {
	if !v {
		return make([]byte, 32)
	}
	return abiEncodeUint64(1)
}

func abiEncodeInt64(v int64) []byte {
	out := make([]byte, 32)
	if v < 0 {
		for i := range out {
			out[i] = 0xff
		}
	}
	binary.BigEndian.PutUint64(out[24:32], uint64(v))
	return out
}

func abiEncodeDynamicBytes(v []byte) []byte {
	paddedLen := ((len(v) + 31) / 32) * 32
	out := make([]byte, 32+paddedLen)
	putABIUint64(out[:32], uint64(len(v)))
	copy(out[32:], v)
	return out
}

func abiEncodeStringArray(values []string) []byte {
	items := make([][]byte, len(values))
	for i := range values {
		items[i] = []byte(values[i])
	}
	return abiEncodeBytesArray(items)
}

func abiEncodeBytesArray(values [][]byte) []byte {
	head := make([]byte, 32+32*len(values))
	putABIUint64(head[:32], uint64(len(values)))
	tail := make([]byte, 0)
	for i := range values {
		putABIUint64(head[32+i*32:32+(i+1)*32], uint64(32*len(values)+len(tail)))
		tail = append(tail, abiEncodeDynamicBytes(values[i])...)
	}
	return append(head, tail...)
}

func putABIUint64(dst []byte, v uint64) {
	clear(dst)
	binary.BigEndian.PutUint64(dst[24:32], v)
}
