package evm

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	scommon "github.com/sat20-labs/indexer/common"
	evmcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	MainnetContractPrefix = evmcommon.MainnetContractPrefix
	TestnetContractPrefix = evmcommon.TestnetContractPrefix
	AddressVersionV1      = evmcommon.AddressVersionV1
	ContractTypeTemplate  = evmcommon.ContractTypeTemplate
	ContractTypeEVM       = evmcommon.ContractTypeEVM
	ContractTypeAgent     = evmcommon.ContractTypeAgent
	PayloadVersionV1      = evmcommon.PayloadVersionV1
	SatoshiAssetName      = evmcommon.SatoshiAssetName
)

type TxType = evmcommon.TxType

const (
	TxTypeDeploy            = evmcommon.TxTypeDeploy
	TxTypeInvoke            = evmcommon.TxTypeInvoke
	TxTypeResult            = evmcommon.TxTypeResult
	TxTypeCoinbaseStateRoot = evmcommon.TxTypeCoinbaseStateRoot
)

type ResultStatus = evmcommon.ResultStatus

const (
	ResultStatusSuccess  = evmcommon.ResultStatusSuccess
	ResultStatusRevert   = evmcommon.ResultStatusRevert
	ResultStatusOutOfGas = evmcommon.ResultStatusOutOfGas
	ResultStatusInvalid  = evmcommon.ResultStatusInvalid
)

type ExecutionKind byte

const (
	ExecutionKindDeploy ExecutionKind = iota + 1
	ExecutionKindInvoke
	ExecutionKindTrigger
)

type ExecutionStatus byte

const (
	StatusSuccess ExecutionStatus = iota
	StatusRevert
	StatusOutOfGas
	StatusInvalid
)

type EVMAddress = evmcommon.EVMAddress

var ParseEVMAddressHex = evmcommon.ParseEVMAddressHex

type DeployPayload = evmcommon.DeployPayload
type InvokePayload = evmcommon.InvokePayload
type ResultPayload = evmcommon.ResultPayload
type StateRootPayload = evmcommon.StateRootPayload

type OutPoint struct {
	TxID string
	Vout uint32
}

func (o OutPoint) String() string {
	return fmt.Sprintf("%s:%d", o.TxID, o.Vout)
}

type UTXO struct {
	OutPoint       OutPoint
	Contract       ContractAddress
	Value          uint64
	Assets         wire.TxAssets
	Height         int64
	IsGasFunding   bool
	SourceCallID   string
	ReservedReason string
}

func (u UTXO) Clone() UTXO {
	return UTXO{
		OutPoint:       u.OutPoint,
		Contract:       u.Contract,
		Value:          u.Value,
		Assets:         u.Assets.Clone(),
		Height:         u.Height,
		IsGasFunding:   u.IsGasFunding,
		SourceCallID:   u.SourceCallID,
		ReservedReason: u.ReservedReason,
	}
}

func (u UTXO) AssetAmount(assetName string) (*scommon.Decimal, error) {
	if assetName == "" {
		return nil, ErrInvalidAsset
	}
	if assetName == SatoshiAssetName {
		return decimalFromUint64(u.Value)
	}
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return nil, ErrInvalidAsset
	}
	asset, err := u.Assets.Find(name)
	if err != nil {
		return zeroDecimal(), nil
	}
	if asset == nil {
		return zeroDecimal(), nil
	}
	return asset.Amount.Clone(), nil
}

func (u UTXO) HasAsset(assetName string) bool {
	amount, err := u.AssetAmount(assetName)
	return err == nil && amount.Sign() > 0
}

func NewAssetSet(assetName string, amount *scommon.Decimal) (wire.TxAssets, error) {
	if assetName == "" || assetName == SatoshiAssetName || amount == nil || amount.IsZero() {
		return nil, nil
	}
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return nil, ErrInvalidAsset
	}
	if amount.Sign() < 0 {
		return nil, fmt.Errorf("asset amount must be non-negative")
	}
	return wire.TxAssets{{
		Name:   *name,
		Amount: *amount.Clone(),
	}}, nil
}

type AssetIntent struct {
	CallID      string
	IntentIndex uint32
	From        ContractAddress
	To          string
	AssetName   string
	Amount      *scommon.Decimal
	ExtraData   []byte
}

func SortUTXOsForCanonicalSelection(utxos []UTXO) {
	sort.SliceStable(utxos, func(i, j int) bool {
		a, b := utxos[i], utxos[j]
		if a.Height != b.Height {
			return a.Height < b.Height
		}
		if a.OutPoint.TxID != b.OutPoint.TxID {
			return a.OutPoint.TxID < b.OutPoint.TxID
		}
		return a.OutPoint.Vout < b.OutPoint.Vout
	})
}

var (
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrInvalidAsset      = errors.New("invalid asset")
)

func zeroDecimal() *scommon.Decimal {
	return scommon.NewDefaultDecimal(0)
}

func cloneDecimal(d *scommon.Decimal) *scommon.Decimal {
	if d == nil {
		return zeroDecimal()
	}
	return d.Clone()
}

func decimalFromUint64(v uint64) (*scommon.Decimal, error) {
	if v > uint64(math.MaxInt64) {
		return nil, fmt.Errorf("amount overflows int64")
	}
	return scommon.NewDefaultDecimal(int64(v)), nil
}

func ParseDecimalAmountString(s string) (*scommon.Decimal, error) {
	if strings.Contains(s, ":") {
		return scommon.NewDecimalFromFormatString(s)
	}
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		return scommon.NewDecimalFromString(s, len(s)-dot-1)
	}
	return scommon.NewDecimalFromFormatString(s)
}
