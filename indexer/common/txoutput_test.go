package common

import (
	"fmt"
	"testing"

	"github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)


func TestTxAssets2(t *testing.T) {
	assetName := common.AssetName{
		Protocol: "runes",
		Type: "f",
		Ticker: "65103_1",
	}
	inputAssets := []common.TxAssets{
		{
			{
				Name: assetName,
				Amount: *common.NewDecimal(200, 0),
				BindingSat: 0,
			},
		},
		nil,
	}

	inputValues := []int64{710, 10000}


	var allInputs TxOutput
	for _, assets := range inputAssets {
		allInputs.OutValue.Assets.Merge(assets)
	}
	for _, value := range inputValues {
		allInputs.OutValue.Value += value
	}

	serviceFee := wire.AssetInfo{
		Name:       ASSET_PLAIN_SAT,
		Amount:     *common.NewDefaultDecimal(3084),
		BindingSat: 1,
	}
	err := allInputs.SubAsset(&serviceFee)
	if err != nil {
		t.Fatal(err)
	}
	feeAsset := wire.AssetInfo{
		Name:       ASSET_PLAIN_SAT,
		Amount:     *common.NewDefaultDecimal(10),
		BindingSat: 1,
	}
	err = allInputs.SubAsset(&feeAsset)
	if err != nil {
		t.Fatal(err)
	}

	asset := wire.AssetInfo{
		Name:       assetName,
		Amount:     *common.NewDecimal(10, 0),
		BindingSat: 0,
	}
	var output TxOutput
	output.AddAsset(&asset)

	lockAsset, assetChange, err := allInputs.Split(&assetName, output.Value(), &asset.Amount)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("lockAsset %v\n", lockAsset)
	fmt.Printf("assetChange %v\n", assetChange)

}

func TestSTPScript(t *testing.T) {
	{
		script, err := NullDataScript(CONTENT_TYPE_MIN, []byte("new channel"))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("script: %v\n", script)
		fmt.Printf("is stp script: %v\n", IsSTPNullDataScript(script))

		ctype, data, err := ReadDataFromNullDataScript(script)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("%d %s\n", ctype, string(data))
	}

	{
		script, err := NullDataScript(CONTENT_TYPE_MAX, []byte("new channel"))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("script: %v\n", script)
		fmt.Printf("is stp script: %v\n", IsSTPNullDataScript(script))

		ctype, data, err := ReadDataFromNullDataScript(script)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("%d %s\n", ctype, string(data))
	}
}