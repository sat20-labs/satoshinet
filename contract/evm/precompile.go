package evm

import (
	"encoding/binary"
	"errors"
	"fmt"

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

	assetBalanceOfSelector        = methodSelector("balanceOf(address,string)")
	assetTransferAssetSelector    = methodSelector("transferAsset(string,string,string,bytes)")
	assetCompareAmountSelector    = methodSelector("compareAmount(string,string)")
	assetAddAmountSelector        = methodSelector("addAmount(string,string)")
	assetSubAmountSelector        = methodSelector("subAmount(string,string)")
	assetMulAmountSelector        = methodSelector("mulAmount(string,string)")
	assetDivAmountSelector        = methodSelector("divAmount(string,string)")
	triggerRegisterHeightSelector = methodSelector("registerHeightTrigger(string,uint256,uint256,bytes)")
)

const evmAmountMaxPrecision = scommon.MAX_PRECISION - 1

type AssetBalanceReader interface {
	AssetBalance(owner EVMAddress, assetName string) (*scommon.Decimal, error)
}

type UTXOAssetView struct {
	UTXOs []UTXO
}

type ContractUTXOAssetView struct {
	Prefix   string
	Provider ContractUTXOProvider
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

type AssetPrecompile struct {
	Balances AssetBalanceReader
}

func NewAssetPrecompile(balances AssetBalanceReader) *AssetPrecompile {
	return &AssetPrecompile{Balances: balances}
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

func SatoshiNetPrecompiles(balances AssetBalanceReader, rules vm.PrecompiledContracts) vm.PrecompiledContracts {
	out := make(vm.PrecompiledContracts, len(rules)+2)
	for addr, p := range rules {
		out[addr] = p
	}
	out[AssetPrecompileAddress] = NewAssetPrecompile(balances)
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

func putABIUint64(dst []byte, v uint64) {
	clear(dst)
	binary.BigEndian.PutUint64(dst[24:32], v)
}
