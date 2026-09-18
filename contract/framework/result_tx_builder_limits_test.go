package framework

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestBuildResultTxRejectsTooManyOutputs(t *testing.T) {
	outputs := make([]ResultOutput, MaxContractResultOutputs+1)
	for i := range outputs {
		outputs[i] = ResultOutput{To: "recipient", Value: 1}
	}
	_, err := BuildResultTx(ResultTxBuildRequest{
		Status:        contract.ResultStatusSuccess,
		ResultCount:   1,
		Plans:         []ResultPlan{{Contract: "contract", Outputs: outputs}},
		ResolveScript: func(ResultOutput) ([]byte, error) { return []byte{0x51}, nil },
	}, ResultTxBuildOptions{})
	require.ErrorContains(t, err, "too many contract result outputs")
}


func TestBuildResultTxKeepsPartialRefundsSeparateWhenMergeNeedsCarrier(t *testing.T) {
	name := wire.NewAssetNameFromString("ordx:f:partial-refund")
	require.NotNil(t, name)
	one := func() ResultOutput {
		return ResultOutput{
			To: "caller",
			Assets: wire.TxAssets{{
				Name: *name, Amount: *scommon.NewDefaultDecimal(1), BindingSat: 2,
			}},
		}
	}
	compacted, err := CompactResultOutputs([]ResultOutput{one(), one()})
	require.NoError(t, err)
	require.Len(t, compacted, 2,
		"two individually valid partial refunds must remain separate when merging would require a sat")
	for _, output := range compacted {
		require.Zero(t, output.Value)
		require.Len(t, output.Assets, 1)
		require.Equal(t, "1", output.Assets[0].Amount.String())
		require.Equal(t, uint32(2), output.Assets[0].BindingSat)
	}

	tx, err := BuildResultTx(ResultTxBuildRequest{
		Status:      contract.ResultStatusInvalid,
		ResultCount: 2,
		Plans: []ResultPlan{{
			Contract: "contract",
			Outputs:  compacted,
		}},
		ResolveScript: func(ResultOutput) ([]byte, error) { return []byte{0x51}, nil },
	}, ResultTxBuildOptions{})
	require.NoError(t, err)
	require.Len(t, tx.TxOut, 3) // two refunds + RESULT metadata
}


func TestCompactResultOutputsMergesWhenCarrierRemainsSufficient(t *testing.T) {
	name := wire.NewAssetNameFromString("ordx:f:partial-merge")
	require.NotNil(t, name)
	asset := func(value int64) ResultOutput {
		return ResultOutput{
			To: "caller",
			Value: value,
			Assets: wire.TxAssets{{
				Name: *name, Amount: *scommon.NewDefaultDecimal(1), BindingSat: 2,
			}},
		}
	}
	compacted, err := CompactResultOutputs([]ResultOutput{asset(0), asset(1)})
	require.NoError(t, err)
	require.Len(t, compacted, 1)
	require.Equal(t, int64(1), compacted[0].Value)
	require.Len(t, compacted[0].Assets, 1)
	require.Equal(t, "2", compacted[0].Assets[0].Amount.String())
	require.Equal(t, uint32(2), compacted[0].Assets[0].BindingSat)
}
