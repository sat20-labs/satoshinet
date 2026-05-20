package evm

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	evmcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestValidateResultContractSpend(t *testing.T) {
	contract := testContract(t)
	script, err := ContractPkScript(contract)
	require.NoError(t, err)

	hash := chainhash.Hash{1, 2, 3}
	out := wire.OutPoint{Hash: hash, Index: 0}
	resultScript, err := evmcommon.ResultNullDataScript(ResultPayload{
		Status:      ResultStatusSuccess,
		ResultCount: 1,
	})
	require.NoError(t, err)

	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&out, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, resultScript))

	validated, err := ValidateResultContractSpend(tx, map[OutPoint][]byte{
		WireOutPointToEVM(out): script,
	}, TestnetContractPrefix)
	require.NoError(t, err)
	require.Equal(t, ResultStatusSuccess, validated.Payload.Status)
	require.Len(t, validated.ContractInputs, 1)
	require.True(t, contract.Equal(validated.Contracts[0]))
}

func TestValidateResultContractSpendAllowsMultipleContracts(t *testing.T) {
	first := testContract(t)
	second := testContractWithHash(t, EVMAddress{9, 8, 7})
	firstScript, err := ContractPkScript(first)
	require.NoError(t, err)
	secondScript, err := ContractPkScript(second)
	require.NoError(t, err)

	firstHash := chainhash.Hash{1}
	secondHash := chainhash.Hash{2}
	firstOut := wire.OutPoint{Hash: firstHash, Index: 0}
	secondOut := wire.OutPoint{Hash: secondHash, Index: 1}
	resultScript, err := evmcommon.ResultNullDataScript(ResultPayload{
		Status:      ResultStatusSuccess,
		ResultCount: 2,
	})
	require.NoError(t, err)

	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&firstOut, nil, nil))
	tx.AddTxIn(wire.NewTxIn(&secondOut, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, resultScript))

	validated, err := ValidateResultContractSpend(tx, map[OutPoint][]byte{
		WireOutPointToEVM(firstOut):  firstScript,
		WireOutPointToEVM(secondOut): secondScript,
	}, TestnetContractPrefix)
	require.NoError(t, err)
	require.Len(t, validated.ContractInputs, 2)
	require.Len(t, validated.Contracts, 2)
}

func TestValidateResultContractSpendRejectsMissingContractInput(t *testing.T) {
	hash := chainhash.Hash{1, 2, 3}
	out := wire.OutPoint{Hash: hash, Index: 0}
	resultScript, err := evmcommon.ResultNullDataScript(ResultPayload{
		Status:      ResultStatusSuccess,
		ResultCount: 1,
	})
	require.NoError(t, err)

	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&out, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, resultScript))

	_, err = ValidateResultContractSpend(tx, map[OutPoint][]byte{
		WireOutPointToEVM(out): []byte{0x51},
	}, TestnetContractPrefix)
	require.Error(t, err)
}

func testContractWithHash(t *testing.T, addr EVMAddress) ContractAddress {
	t.Helper()
	contract, err := NewContractAddress(TestnetContractPrefix, AddressVersionV1, ContractTypeEVM, addr)
	require.NoError(t, err)
	return contract
}
