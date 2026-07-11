package wire

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/sat20-labs/indexer/common"
)

// //////////////////////////////////////////////////////////////
// 定义在聪网中
// type AssetName struct {
// 	Protocol string // 必填，比如ordx, ordinals, brc20，runes，eth，等等
// 	Type     string // 可选，默认是ft，参考indexer的定义
// 	Ticker   string // 如果Type是nft类型，ticker是合集名称#铭文序号（或者聪序号）
// }

type AssetName = common.AssetName

func NewAssetNameFromString(name string) *AssetName {
	parts := strings.Split(name, ":")
	if len(parts) != 3 {
		return nil
	}
	return &AssetName{
		Protocol: parts[0],
		Type:     parts[1],
		Ticker:   parts[2],
	}
}

// type AssetInfo struct {
// 	Name       AssetName
// 	Amount     int64  // 资产数量
// 	BindingSat uint32 // 非0 -> 每一聪绑定的资产的数量, 0 -> 不绑定聪
// }

type AssetInfo = common.AssetInfo

// 有序数组，根据名字排序
// type TxAssets []AssetInfo
type TxAssets = common.TxAssets

func SerializeTxAssets(p *TxAssets) ([]byte, error) {
	var w bytes.Buffer

	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)

	err := AssetsWriteToBuf(&w, 0, *p, buf)
	if err != nil {
		return nil, err
	}

	return w.Bytes(), nil
}

func DeserializeTxAssets(p *TxAssets, r []byte) error {

	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)

	sbuf := scriptPool.Borrow()
	defer scriptPool.Return(sbuf)

	assets, err := AssetsReadFromBuf(bytes.NewReader(r), 0, buf, sbuf[:])
	if err != nil {
		return err
	}

	*p = assets
	return nil
}

func AssetsWriteToBuf(w io.Writer, pver uint32, assets TxAssets, buf []byte) error {
	// get count for sats range, and write to w
	assetsCount := uint64(len(assets))
	err := WriteVarIntBuf(w, pver, assetsCount, buf)
	if err != nil {
		return err
	}

	for _, asset := range assets {
		if err := asset.Amount.Validate(); err != nil {
			return fmt.Errorf("invalid asset %s amount: %w", asset.Name.String(), err)
		}
		// Write asset, Name（Protocol，Type，Ticker）, Amount, BindingSat
		err = WriteVarBytesBuf(w, pver, []byte(asset.Name.Protocol), buf)
		if err != nil {
			return err
		}
		err = WriteVarBytesBuf(w, pver, []byte(asset.Name.Type), buf)
		if err != nil {
			return err
		}
		err = WriteVarBytesBuf(w, pver, []byte(asset.Name.Ticker), buf)
		if err != nil {
			return err
		}
		err = WriteVarBytesBuf(w, pver, []byte(asset.Amount.ToFormatString()), buf)
		if err != nil {
			return err
		}
		err = WriteVarIntBuf(w, pver, uint64(asset.BindingSat), buf)
		if err != nil {
			return err
		}
	}
	return nil
}

func AssetsReadFromBuf(r io.Reader, pver uint32, buf, s []byte) (TxAssets, error) {
	// count fr sats range
	count, err := ReadVarIntBuf(r, pver, buf)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}

	assets := make(TxAssets, 0)
	for i := uint64(0); i < count; i++ {
		newAsset := AssetInfo{}
		// Get sats start and size
		newAsset.Name.Protocol, err = readString(r, pver, buf, s, "asset protocol")
		if err != nil {
			return nil, err
		}
		newAsset.Name.Type, err = readString(r, pver, buf, s, "asset type")
		if err != nil {
			return nil, err
		}
		newAsset.Name.Ticker, err = readString(r, pver, buf, s, "asset ticker")
		if err != nil {
			return nil, err
		}
		formatedAmount, err := readString(r, pver, buf, s, "asset amount")
		if err != nil {
			return nil, err
		}
		amount, err := common.NewDecimalFromFormatString(formatedAmount)
		if err != nil {
			return nil, err
		}
		newAsset.Amount = *amount
		bindingSat, err := ReadVarIntBuf(r, pver, buf)
		if err != nil {
			return nil, err
		}
		newAsset.BindingSat = uint32(bindingSat)
		assets = append(assets, newAsset)
	}
	return assets, nil
}

// amt的资产需要多少聪
func GetBindingSatNum(amt int64, n uint32) int64 {
	if n == 0 {
		return 0
	}
	return (amt + int64(n) - 1) / int64(n)
}
