package contract

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestValidateResultContractSpend(t *testing.T) {
	contract, err := contractcommon.NewContractAddress(
		contractcommon.TestnetContractPrefix,
		contractcommon.AddressVersionV1,
		contractcommon.ContractTypeAgent,
		contractcommon.EVMAddress{1, 2, 3},
	)
	require.NoError(t, err)
	contractScript, err := contractcommon.ContractPkScript(contract)
	require.NoError(t, err)

	prevOut := wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}
	resultScript, err := contractcommon.ResultNullDataScript(contractcommon.ResultPayload{
		Status:      contractcommon.ResultStatusSuccess,
		ResultCount: 1,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&prevOut, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, resultScript))

	got, err := ValidateResultContractSpend(tx, map[wire.OutPoint][]byte{
		prevOut: contractScript,
	}, contractcommon.TestnetContractPrefix)
	require.NoError(t, err)
	require.Equal(t, contractcommon.ResultStatusSuccess, got.Payload.Status)
	require.Len(t, got.ContractInputs, 1)
	require.Equal(t, contract, got.ContractInputs[0].Contract)
	require.Len(t, got.Contracts, 1)
}

func TestValidateResultContractSpendRejectsNonResult(t *testing.T) {
	prevOut := wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&prevOut, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, []byte{0x51}))

	_, err := ValidateResultContractSpend(tx, map[wire.OutPoint][]byte{
		prevOut: []byte{0x51},
	}, contractcommon.TestnetContractPrefix)
	require.Error(t, err)
}
