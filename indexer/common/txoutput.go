package common

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

type AssetInfo = wire.AssetInfo

var ASSET_PLAIN_SAT wire.AssetName = wire.AssetName{}


type TxOutput struct {
	UtxoId      uint64
	OutPointStr string
	OutValue    wire.TxOut
	// 注意BindingSat属性，TxOutput.OutValue.Value必须大于等于
	// Assets数组中任何一个AssetInfo.BindingSat
}

func NewTxOutput(value int64) *TxOutput {
	return  &TxOutput{
		UtxoId:      common.INVALID_ID,
		OutPointStr: "",
		OutValue:    wire.TxOut{Value:value},
	}
}

func CloneTxOut(a *wire.TxOut) *wire.TxOut {
	n := &wire.TxOut{
		Value: a.Value,
		Assets: a.Assets.Clone(),
	}
	n.PkScript = make([]byte, len(a.PkScript))
	copy(n.PkScript, a.PkScript)

	return n
}

func (p *TxOutput) Clone() *TxOutput {
	return &TxOutput{
		UtxoId:      p.UtxoId,
		OutPointStr: p.OutPointStr,
		OutValue: *CloneTxOut(&p.OutValue),
	}
}

func (p *TxOutput) ToAssetsInUtxo() *common.AssetsInUtxo{
	if p == nil {
		return nil
	}

	assets := make([]*common.DisplayAsset, 0)
	for _, asset := range p.OutValue.Assets {
		display := common.DisplayAsset{
			AssetName: asset.Name,
			Amount: asset.Amount.String(),
			Precision: asset.Amount.Precision,
			BindingSat: int(asset.BindingSat),
		}
		assets = append(assets, &display)
	}
	return &common.AssetsInUtxo{
		UtxoId: p.UtxoId,
		OutPoint: p.OutPointStr,
		Value: p.OutValue.Value,
		PkScript: p.OutValue.PkScript,
		Assets: assets,
	}
}

func (p *TxOutput) Height() int {
	if p.UtxoId == common.INVALID_ID {
		return -1
	}
	h, _, _ := common.FromUtxoId(p.UtxoId)
	return h
}

func (p *TxOutput) Value() int64 {
	return p.OutValue.Value
}

func (p *TxOutput) Zero() bool {
	if p == nil {
		return true
	}
	return p.OutValue.Value == 0 && len(p.OutValue.Assets) == 0
}

func (p *TxOutput) HasPlainSat() bool {
	return p.GetPlainSat() > 0
}

// 是否存在一些n>0并且没有对应聪的资产
func (p *TxOutput) GetUnboundAssetCount() int {
	return p.OutValue.Assets.GetUnboundAssetCount()
}

// 等同于 GetPlainSat_Floor 
// 去掉已经绑定了资产的聪. 与主网不同，聪网上每一个聪，都只能代表一种资产。如果一个聪有多种资产，只能选择一种到聪网流通。
func (p *TxOutput) GetPlainSat() int64 {
	if len(p.OutValue.Assets) == 0 {
		return p.OutValue.Value
	}
	assetAmt := p.OutValue.Assets.GetBindingSatAmout()
	// 聪网需要考虑有一些ordx资产，因为n参数的影响，<n的资产并没有绑定聪
	// 在获取该utxo的空白聪数量时，最好就把这部分聪预留下来
	assetAmt += int64(p.GetUnboundAssetCount())
	if p.OutValue.Value < assetAmt {
		return 0
	}

	return p.OutValue.Value - assetAmt
}

// 完全不考虑哪些不足一聪的聪资产
// 去掉已经绑定了资产的聪. 与主网不同，聪网上每一个聪，都只能代表一种资产。如果一个聪有多种资产，只能选择一种到聪网流通。
func (p *TxOutput) GetPlainSat_Ceil() int64 {
	if len(p.OutValue.Assets) == 0 {
		return p.OutValue.Value
	}
	assetAmt := p.OutValue.Assets.GetBindingSatAmout()
	
	return p.OutValue.Value - assetAmt
}

func (p *TxOutput) OutPoint() *wire.OutPoint {
	outpoint, _ := wire.NewOutPointFromString(p.OutPointStr)
	return outpoint
}

func (p *TxOutput) TxOut() *wire.TxOut {
	return &p.OutValue
}

func (p *TxOutput) TxID() string {
	parts := strings.Split(p.OutPointStr, ":")
	if len(parts) != 2 {
		return ""
	}
	return parts[0]
}

func (p *TxOutput) TxIn() *wire.TxIn {
	outpoint, err := wire.NewOutPointFromString(p.OutPointStr)
	if err != nil {
		return nil
	}
	return wire.NewTxIn(outpoint, nil, nil)
}

func (p *TxOutput) SizeOfBindingSats() int64 {
	return p.OutValue.Assets.GetBindingSatAmout()
}


func (p *TxOutput) Merge(another *TxOutput) error {
	if another == nil {
		return nil
	}

	if p.OutValue.Value + another.OutValue.Value < 0 {
		return fmt.Errorf("out of bounds")
	}
	p.OutValue.Value += another.OutValue.Value
	err := p.OutValue.Assets.Merge(another.OutValue.Assets)
	if err != nil {
		return err
	}
	p.OutPointStr = ""
	p.UtxoId = common.INVALID_ID
	return nil
}

func (p *TxOutput) Subtract(another *TxOutput) error {
	if another == nil {
		return nil
	}

	if p.OutValue.Value < another.OutValue.Value {
		return fmt.Errorf("can't split")
	}

	tmpAssets := p.OutValue.Assets.Clone()
	err := tmpAssets.Split(another.OutValue.Assets)
	if err != nil {
		return err
	}
	bindingSat := tmpAssets.GetBindingSatAmout()
	if p.OutValue.Value - another.OutValue.Value < bindingSat {
		return fmt.Errorf("no enough sats")
	}
	
	p.OutValue.Value -= another.OutValue.Value
	p.OutValue.Assets = tmpAssets

	p.OutPointStr = ""
	p.UtxoId = common.INVALID_ID

	return nil
}

// 聪网utxo允许有多种资产，但没有offset属性
func (p *TxOutput) Split(name *wire.AssetName, value int64, amt *common.Decimal) (*TxOutput, *TxOutput, error) {

	if value == 0 {
		// 按照资产数量确定value
		if name == nil || *name == ASSET_PLAIN_SAT {
			value = amt.Int64()
		} else {
			asset, err := p.OutValue.Assets.Find(name)
			if err != nil {
				return nil, nil, err
			}
			n := asset.BindingSat
			if n != 0 {
				value = common.GetBindingSatNum(amt, n)
			}
		}
	}

	if p.Value() < value {
		return nil, nil, fmt.Errorf("output value too small")
	}
	
	var value1, value2 int64
	value1 = value
	value2 = p.Value() - value1
	part1 := NewTxOutput(value1)
	part2 := NewTxOutput(value2)

	if name == nil || *name == ASSET_PLAIN_SAT {
		if p.Value() < amt.Int64() {
			return nil, nil, fmt.Errorf("amount too large")
		}
		part2.OutValue.Assets = p.OutValue.Assets
		return part1, part2, nil
	}

	asset, err := p.OutValue.Assets.Find(name)
	if err != nil {
		return nil, nil, err
	}
	n := asset.BindingSat
	if n != 0 {
		// if amt%int64(n) != 0 {
		// 	return nil, nil, fmt.Errorf("amt must be times of %d", n)
		// }
		requiredValue := common.GetBindingSatNum(amt, asset.BindingSat)
		if requiredValue > value {
			return nil, nil, fmt.Errorf("value too small")
		}
	}

	if asset.Amount.Cmp(amt) < 0 {
		return nil, nil, fmt.Errorf("amount too large")
	}
	asset1 := asset.Clone()
	asset1.Amount = *amt.Clone()
	assets2 := p.OutValue.Assets.Clone()
	assets2.Subtract(asset1)

	part1.OutValue.Assets = wire.TxAssets{*asset1}
	part2.OutValue.Assets = assets2
	
	if !common.IsBindingSat(name) {
		return part1, part2, nil
	}

	if part2.Zero() {
		part2 = nil
	}

	return part1, part2, nil
}

func (p *TxOutput) GetAsset(assetName *wire.AssetName) *common.Decimal {
	if assetName == nil || *assetName == ASSET_PLAIN_SAT {
		return common.NewDefaultDecimal(p.GetPlainSat())
	}
	asset, err := p.OutValue.Assets.Find(assetName)
	if err != nil {
		return nil
	}
	return asset.Amount.Clone()
}

func (p *TxOutput) AddAsset(asset *AssetInfo) error {
	if asset == nil {
		return nil
	}
	
	if asset.Name == ASSET_PLAIN_SAT {
		if p.OutValue.Value + asset.Amount.Int64() < 0 {
			return fmt.Errorf("out of bounds")
		}
		p.OutValue.Value += asset.Amount.Int64()
		return nil
	}

	if asset.BindingSat > 0 {
		if p.OutValue.Value + common.GetBindingSatNum(&asset.Amount, asset.BindingSat) < 0 {
			return fmt.Errorf("out of bounds")
		}
	}

	err := p.OutValue.Assets.Add(asset)
	if err != nil {
		return err
	}

	if asset.BindingSat > 0 {
		p.OutValue.Value += common.GetBindingSatNum(&asset.Amount, asset.BindingSat)
	}

	p.OutPointStr = ""
	p.UtxoId = common.INVALID_ID

	return nil
}

func (p *TxOutput) SubAsset(asset *AssetInfo) error {
	if asset == nil {
		return nil
	}
	if asset.Name == ASSET_PLAIN_SAT {
		if p.OutValue.Value < asset.Amount.Int64() {
			return fmt.Errorf("no enough sats")
		}
		bindingSat := p.OutValue.Assets.GetBindingSatAmout()
		if p.OutValue.Value - asset.Amount.Int64() < bindingSat {
			return fmt.Errorf("no enough sats")
		}
		p.OutValue.Value -= asset.Amount.Int64()
		p.OutPointStr = ""
		p.UtxoId = common.INVALID_ID
		return nil
	}

	if asset.BindingSat > 0 {
		tmpAssets := p.OutValue.Assets.Clone()
		err := tmpAssets.Subtract(asset)
		if err != nil {
			return err
		}
		bindingSat := tmpAssets.GetBindingSatAmout()
		satsNum := common.GetBindingSatNum(&asset.Amount, asset.BindingSat)
		if p.OutValue.Value - satsNum < bindingSat {
			return fmt.Errorf("no enough sats")
		}

		p.OutValue.Value -= satsNum
		p.OutValue.Assets = tmpAssets
		p.OutPointStr = ""
		p.UtxoId = common.INVALID_ID
		return nil
	}

	err := p.OutValue.Assets.Subtract(asset)
	if err != nil {
		return err
	}

	p.OutPointStr = ""
	p.UtxoId = common.INVALID_ID

	return nil
}

func GenerateTxOutput(tx *wire.MsgTx, index int) *TxOutput {
	return &TxOutput{
		UtxoId:      common.INVALID_ID,
		OutPointStr: tx.TxHash().String() + ":" + strconv.Itoa(index),
		OutValue:    *tx.TxOut[index],
	}
}

