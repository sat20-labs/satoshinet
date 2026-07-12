package framework

import (
	"testing"

	contract "github.com/sat20-labs/satoshinet/contract"
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
