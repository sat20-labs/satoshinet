package mempool

import (
	"testing"
	"time"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/contract/evm"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestEVMContractOutputIsStandard(t *testing.T) {
	contract := testEVMContractAddress(t)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	contractOut, err := evm.NewContractTxOut(1000, nil, contract)
	if err != nil {
		t.Fatal(err)
	}
	tx.AddTxOut(contractOut)

	err = CheckTransactionStandard(
		btcutil.NewTx(tx),
		1,
		time.Unix(1, 0),
		DefaultMinRelayTxFee,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestEVMContractInputRequiresResultTx(t *testing.T) {
	prevHash := chainhash.Hash{1, 2, 3}
	prevOut := wire.OutPoint{Hash: prevHash, Index: 0}
	contract := testEVMContractAddress(t)
	contractOut, err := evm.NewContractTxOut(1000, nil, contract)
	if err != nil {
		t.Fatal(err)
	}
	view := blockchain.NewUtxoViewpoint()
	view.Entries()[prevOut] = blockchain.NewUtxoEntry(contractOut, 1, false)

	nonResultTx := wire.NewMsgTx(1)
	nonResultTx.AddTxIn(wire.NewTxIn(&prevOut, nil, nil))
	nonResultTx.AddTxOut(wire.NewTxOut(1000, nil, []byte{0x51}))
	if err := checkInputsStandard(btcutil.NewTx(nonResultTx), view); err == nil {
		t.Fatal("expected non-result spend to be rejected")
	}

	resultTx := wire.NewMsgTx(1)
	resultTx.AddTxIn(wire.NewTxIn(&prevOut, nil, nil))
	resultScript, err := evmcommon.ResultNullDataScript(evm.ResultPayload{
		Status:      evm.ResultStatusSuccess,
		ResultCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	resultTx.AddTxOut(wire.NewTxOut(0, nil, resultScript))
	if err := checkInputsStandard(btcutil.NewTx(resultTx), view); err != nil {
		t.Fatal(err)
	}
}

func TestEVMAssetFeeBypassesRelaySatoshiFee(t *testing.T) {
	contract := testEVMContractAddress(t)
	invokeScript, err := evmcommon.InvokeNullDataScript(evm.InvokePayload{
		GasLimit:  1000,
		CallNonce: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	contractOut, err := evm.NewContractTxOut(1000, nil, contract)
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(contractOut)
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))

	pool := &TxPool{cfg: Config{
		Policy: Policy{
			MinRelayTxFee:        btcutil.Amount(1000),
			DisableRelayPriority: true,
		},
		ChainParams: &chaincfg.TestNetParams,
	}}
	feeAssets := wire.TxAssets{{
		Name:   wire.AssetName{Protocol: "ordx", Type: "gas", Ticker: "evm"},
		Amount: *scommon.NewDefaultDecimal(1),
	}}
	err = pool.validateRelayFeeMet(btcutil.NewTx(tx), 0, feeAssets, 1000,
		blockchain.NewUtxoViewpoint(), 1, true, true)
	if err != nil {
		t.Fatal(err)
	}

	err = pool.validateRelayFeeMet(btcutil.NewTx(tx), 0, nil, 1000,
		blockchain.NewUtxoViewpoint(), 1, true, true)
	if err != nil {
		t.Fatal(err)
	}
}

func TestEVMDeployRequiresContractFundingOutput(t *testing.T) {
	deployScript, err := evmcommon.DeployNullDataScript(evm.DeployPayload{
		GasLimit:    1000,
		DeployNonce: 1,
		InitCode:    []byte{0x60, 0x00},
	})
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, deployScript))
	err = CheckTransactionStandard(
		btcutil.NewTx(tx),
		1,
		time.Unix(1, 0),
		DefaultMinRelayTxFee,
		1,
	)
	if err == nil {
		t.Fatal("expected deploy without contract funding output to be rejected")
	}

	contractOut, err := evm.NewContractTxOut(0, wire.TxAssets{{
		Name:   wire.AssetName{Protocol: "ordx", Type: "ft", Ticker: "gas"},
		Amount: *scommon.NewDefaultDecimal(1),
	}}, testEVMContractAddress(t))
	if err != nil {
		t.Fatal(err)
	}
	tx.AddTxOut(contractOut)
	if err := CheckTransactionStandard(
		btcutil.NewTx(tx),
		1,
		time.Unix(1, 0),
		DefaultMinRelayTxFee,
		1,
	); err != nil {
		t.Fatal(err)
	}
}

func TestEVMInvokeRequiresContractFundingOutput(t *testing.T) {
	invokeScript, err := evmcommon.InvokeNullDataScript(evm.InvokePayload{
		GasLimit:  1000,
		CallNonce: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	err = CheckTransactionStandard(
		btcutil.NewTx(tx),
		1,
		time.Unix(1, 0),
		DefaultMinRelayTxFee,
		1,
	)
	if err == nil {
		t.Fatal("expected invoke without contract funding output to be rejected")
	}
}

func testEVMContractAddress(t *testing.T) evm.ContractAddress {
	t.Helper()
	addr, err := evm.ParseEVMAddressHex("00112233445566778899aabbccddeeff00112233")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := evm.NewContractAddress(evm.TestnetContractPrefix, evm.AddressVersionV1, evm.ContractTypeEVM, addr)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}
