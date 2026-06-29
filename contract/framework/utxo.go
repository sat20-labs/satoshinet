package framework

import (
	"fmt"
	"math"
	"sort"
	"strings"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	l2common "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

type UTXO struct {
	OutPoint       OutPoint
	Contract       contract.ContractAddress
	Height         int64
	IsGasFunding   bool
	SourceCallID   string
	ReservedReason string
	TxOutput       *l2common.TxOutput
}

type ContractUTXOOverlayConfig struct {
	Prefix       string
	ContractType byte
	Base         ContractUTXOProvider
}

type ResultPlanUTXOView struct {
	Contract contract.ContractAddress
	UTXOs    []UTXO
	Inputs   []OutPoint
	Value    int64
	Assets   wire.TxAssets
}

type ContractUTXOOverlay struct {
	Prefix       string
	ContractType byte
	Base         ContractUTXOProvider

	utxos map[string][]UTXO
	spent map[OutPoint]struct{}
}

func NewContractUTXOOverlay(cfg ContractUTXOOverlayConfig) *ContractUTXOOverlay {
	prefix := cfg.Prefix
	if prefix == "" {
		prefix = contract.TestnetContractPrefix
	}
	return &ContractUTXOOverlay{
		Prefix:       prefix,
		ContractType: cfg.ContractType,
		Base:         cfg.Base,
		utxos:        make(map[string][]UTXO),
		spent:        make(map[OutPoint]struct{}),
	}
}

func (o *ContractUTXOOverlay) Provider(contractAddr contract.ContractAddress) ([]UTXO, error) {
	if o == nil {
		return nil, fmt.Errorf("missing contract UTXO overlay")
	}
	out := make([]UTXO, 0)
	seen := make(map[OutPoint]struct{})
	if o.Base != nil {
		base, err := o.Base(contractAddr)
		if err != nil {
			return nil, err
		}
		for _, utxo := range base {
			if _, spent := o.spent[utxo.OutPoint]; spent {
				continue
			}
			if _, ok := seen[utxo.OutPoint]; ok {
				continue
			}
			seen[utxo.OutPoint] = struct{}{}
			out = append(out, utxo.Clone())
		}
	}
	for _, utxo := range o.utxos[contractAddr.EncodeAddress()] {
		if _, spent := o.spent[utxo.OutPoint]; spent {
			continue
		}
		if _, ok := seen[utxo.OutPoint]; ok {
			continue
		}
		seen[utxo.OutPoint] = struct{}{}
		out = append(out, utxo.Clone())
	}
	SortUTXOsForCanonicalSelection(out)
	return out, nil
}

func (o *ContractUTXOOverlay) ApplyTx(tx *wire.MsgTx, height int64) error {
	if o == nil {
		return fmt.Errorf("missing contract UTXO overlay")
	}
	if tx == nil {
		return fmt.Errorf("missing transaction")
	}
	for _, txIn := range tx.TxIn {
		if txIn == nil {
			continue
		}
		o.spent[WireOutPointToFramework(txIn.PreviousOutPoint)] = struct{}{}
	}
	return o.AddTxOutputs(tx, height)
}

func (o *ContractUTXOOverlay) AddTxOutputs(tx *wire.MsgTx, height int64) error {
	if o == nil {
		return fmt.Errorf("missing contract UTXO overlay")
	}
	utxos, err := ContractUTXOsFromTx(tx, o.Prefix, o.ContractType, height)
	if err != nil {
		return err
	}
	for _, utxo := range utxos {
		o.utxos[utxo.Contract.EncodeAddress()] = append(o.utxos[utxo.Contract.EncodeAddress()], utxo)
	}
	return nil
}

func ContractUTXOsFromTx(tx *wire.MsgTx, prefix string, contractType byte, height int64) ([]UTXO, error) {
	if tx == nil {
		return nil, fmt.Errorf("missing transaction")
	}
	if prefix == "" {
		prefix = contract.TestnetContractPrefix
	}
	txid := tx.TxID()
	utxos := make([]UTXO, 0)
	for vout, txOut := range tx.TxOut {
		if txOut == nil {
			return nil, fmt.Errorf("nil output %d", vout)
		}
		contractAddr, ok, err := contract.ParseContractPkScript(txOut.PkScript, prefix)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if contractType != 0 && contractAddr.ContractType() != contractType {
			continue
		}
		if txOut.Value < 0 {
			return nil, fmt.Errorf("output %d has negative value", vout)
		}
		for _, asset := range txOut.Assets {
			if err := ValidateAssetDecimal(asset.Amount); err != nil {
				return nil, fmt.Errorf("output %d asset %s amount: %w",
					vout, asset.Name.String(), err)
			}
		}
		outpoint := OutPoint{TxID: txid, Vout: uint32(vout)}
		utxos = append(utxos, UTXO{
			OutPoint: outpoint,
			Contract: contractAddr,
			Height:   height,
			TxOutput: indexerTxOutputFromWire(outpoint, txOut),
		})
	}
	return utxos, nil
}

func UTXOFromTxOutput(outpoint OutPoint, contractAddr contract.ContractAddress, height int64, txOut *wire.TxOut) UTXO {
	return UTXO{
		OutPoint: outpoint,
		Contract: contractAddr,
		Height:   height,
		TxOutput: indexerTxOutputFromWire(outpoint, txOut),
	}
}

func ContractUTXOProviderWithTxOutputs(base ContractUTXOProvider, txs []*wire.MsgTx, prefix string,
	contractType byte) ContractUTXOProvider {

	overlay := NewContractUTXOOverlay(ContractUTXOOverlayConfig{
		Prefix:       prefix,
		ContractType: contractType,
		Base:         base,
	})
	var overlayErr error
	for _, tx := range txs {
		if err := overlay.ApplyTx(tx, 0); err != nil && overlayErr == nil {
			overlayErr = err
		}
	}
	return func(contractAddr contract.ContractAddress) ([]UTXO, error) {
		if overlayErr != nil {
			return nil, overlayErr
		}
		return overlay.Provider(contractAddr)
	}
}

func (u UTXO) Clone() UTXO {
	return UTXO{
		OutPoint:       u.OutPoint,
		Contract:       u.Contract,
		Height:         u.Height,
		IsGasFunding:   u.IsGasFunding,
		SourceCallID:   u.SourceCallID,
		ReservedReason: u.ReservedReason,
		TxOutput:       u.IndexerTxOutput(),
	}
}

func (u UTXO) AssetAmount(assetName string) (*scommon.Decimal, error) {
	if assetName == "" {
		return nil, ErrInvalidAsset
	}
	if assetName == contract.SatoshiAssetName {
		return scommon.NewDefaultDecimal(u.PlainValue()), nil
	}
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return nil, ErrInvalidAsset
	}
	output := u.IndexerTxOutput()
	if output == nil {
		return ZeroDecimal(), nil
	}
	amount := output.GetAsset(name)
	if amount == nil {
		return ZeroDecimal(), nil
	}
	return amount, nil
}

func (u UTXO) PlainValue() int64 {
	output := u.IndexerTxOutput()
	if output == nil {
		return 0
	}
	return output.GetPlainSat()
}

func (u UTXO) IndexerTxOutput() *l2common.TxOutput {
	if u.TxOutput != nil {
		return u.TxOutput.Clone()
	}
	return nil
}

func (u UTXO) PhysicalValue() int64 {
	output := u.IndexerTxOutput()
	if output == nil {
		return 0
	}
	return output.OutValue.Value
}

func (u UTXO) TxAssets() wire.TxAssets {
	output := u.IndexerTxOutput()
	if output == nil {
		return nil
	}
	return output.OutValue.Assets.Clone()
}

func (u UTXO) HasAsset(assetName string) bool {
	amount, err := u.AssetAmount(assetName)
	return err == nil && amount.Sign() > 0
}

func SumUTXOAssetAmount(utxos []UTXO, assetName string) (*scommon.Decimal, error) {
	if assetName == "" {
		return nil, ErrInvalidAsset
	}
	total := ZeroDecimal()
	for _, utxo := range utxos {
		amount, err := utxo.AssetAmount(assetName)
		if err != nil {
			return nil, err
		}
		total = total.AddAlignPrecision(amount)
	}
	return total, nil
}

func CollectResultPlanUTXOs(plan ResultPlan, provider ContractUTXOProvider) (ResultPlanUTXOView, error) {
	contractAddr, err := contract.DecodeContractAddress(plan.Contract)
	if err != nil {
		return ResultPlanUTXOView{}, err
	}
	view := ResultPlanUTXOView{Contract: contractAddr}
	if provider == nil {
		return view, nil
	}
	utxos, err := provider(contractAddr)
	if err != nil {
		return ResultPlanUTXOView{}, err
	}
	SortUTXOsForCanonicalSelection(utxos)
	for _, utxo := range utxos {
		if !utxo.Contract.Equal(contractAddr) {
			continue
		}
		nextValue, overflow := AddInt64(view.Value, utxo.PhysicalValue())
		if overflow {
			return ResultPlanUTXOView{}, fmt.Errorf("contract UTXO value overflows int64")
		}
		view.Value = nextValue
		view.UTXOs = append(view.UTXOs, utxo.Clone())
		view.Inputs = append(view.Inputs, utxo.OutPoint)
		assets := utxo.TxAssets()
		if len(assets) != 0 {
			if err := view.Assets.Merge(assets); err != nil {
				return ResultPlanUTXOView{}, err
			}
		}
	}
	view.Inputs = UniqueOutPoints(view.Inputs)
	return view, nil
}

func NewAssetSet(assetName string, amount *scommon.Decimal) (wire.TxAssets, error) {
	if assetName == "" || assetName == contract.SatoshiAssetName || amount == nil || amount.IsZero() {
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

func ZeroDecimal() *scommon.Decimal {
	return scommon.NewDefaultDecimal(0)
}

func CloneDecimal(d *scommon.Decimal) *scommon.Decimal {
	if d == nil {
		return ZeroDecimal()
	}
	return d.Clone()
}

func DecimalFromUint64(v uint64) (*scommon.Decimal, error) {
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
