package contract

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestValidateResultContractSpend(t *testing.T) {
	contract, err := NewContractAddress(
		TestnetContractPrefix,
		AddressVersionV1,
		ContractTypeAgent,
		EVMAddress{1, 2, 3},
	)
	require.NoError(t, err)
	contractScript, err := ContractPkScript(contract)
	require.NoError(t, err)

	prevOut := wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}
	resultScript, err := ResultNullDataScript(ResultPayload{
		Status:      ResultStatusSuccess,
		ResultCount: 1,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&prevOut, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, resultScript))

	got, err := ValidateResultContractSpend(tx, map[wire.OutPoint][]byte{
		prevOut: contractScript,
	}, TestnetContractPrefix)
	require.NoError(t, err)
	require.Equal(t, ResultStatusSuccess, got.Payload.Status)
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
	}, TestnetContractPrefix)
	require.Error(t, err)
}

func TestValidateResultContractSpendRejectsMixedModules(t *testing.T) {
	templateContract, err := NewContractAddress(
		TestnetContractPrefix,
		AddressVersionV1,
		ContractTypeTemplate,
		EVMAddress{1},
	)
	require.NoError(t, err)
	templateScript, err := ContractPkScript(templateContract)
	require.NoError(t, err)
	evmContract, err := NewContractAddress(
		TestnetContractPrefix,
		AddressVersionV1,
		ContractTypeEVM,
		EVMAddress{2},
	)
	require.NoError(t, err)
	evmScript, err := ContractPkScript(evmContract)
	require.NoError(t, err)

	templateOut := wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}
	evmOut := wire.OutPoint{Hash: chainhash.Hash{2}, Index: 0}
	resultScript, err := ResultNullDataScript(ResultPayload{
		Status:      ResultStatusSuccess,
		ResultCount: 1,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&templateOut, nil, nil))
	tx.AddTxIn(wire.NewTxIn(&evmOut, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, resultScript))

	_, err = ValidateResultContractSpend(tx, map[wire.OutPoint][]byte{
		templateOut: templateScript,
		evmOut:      evmScript,
	}, TestnetContractPrefix)
	require.ErrorContains(t, err, "multiple modules")
}
