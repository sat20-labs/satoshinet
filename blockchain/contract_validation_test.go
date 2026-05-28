package blockchain

import (
	"strings"
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractengine "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/agent"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestCompositeContractBlockActivityRoutesResultByCurrentBlockSpentContractUTXO(t *testing.T) {
	cases := []struct {
		name         string
		contractType byte
		want         contractBlockActivity
	}{
		{name: "template", contractType: contractcommon.ContractTypeTemplate, want: contractBlockActivity{Template: true}},
		{name: "evm", contractType: contractcommon.ContractTypeEVM, want: contractBlockActivity{EVM: true}},
		{name: "agent", contractType: contractcommon.ContractTypeAgent, want: contractBlockActivity{Agent: true}},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prevOut := wire.OutPoint{Hash: chainhash.Hash{byte(i + 1)}, Index: 0}
			view := NewUtxoViewpoint()
			view.Entries()[prevOut] = NewUtxoEntry(testContractTxOut(t, tc.contractType), 1, false)
			block := btcutil.NewBlock(&wire.MsgBlock{
				Transactions: []*wire.MsgTx{testEVMCoinbaseTx(), testCommonResultTx(t, prevOut)},
			})

			activity, err := NewCompositeContractBlockValidator(CompositeContractBlockValidatorConfig{
				ChainParams: &chaincfg.TestNetParams,
			}).blockActivity(block, view)
			if err != nil {
				t.Fatalf("blockActivity failed: %v", err)
			}
			if activity != tc.want {
				t.Fatalf("activity mismatch: got %+v want %+v", activity, tc.want)
			}
		})
	}
}

func TestCompositeContractBlockActivityRejectsResultWithoutContractInput(t *testing.T) {
	prevOut := wire.OutPoint{Hash: chainhash.Hash{9}, Index: 0}
	view := NewUtxoViewpoint()
	view.Entries()[prevOut] = NewUtxoEntry(wire.NewTxOut(1, nil, []byte{0x51}), 1, false)
	block := btcutil.NewBlock(&wire.MsgBlock{
		Transactions: []*wire.MsgTx{testEVMCoinbaseTx(), testCommonResultTx(t, prevOut)},
	})

	_, err := NewCompositeContractBlockValidator(CompositeContractBlockValidatorConfig{
		ChainParams: &chaincfg.TestNetParams,
	}).blockActivity(block, view)
	if err == nil || !strings.Contains(err.Error(), "result spends no contract UTXO") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompositeContractBlockActivityRoutesResultBySpentContractUTXO(t *testing.T) {
	prevOut := wire.OutPoint{Hash: chainhash.Hash{8}, Index: 0}
	entry := NewUtxoEntry(testContractTxOut(t, contractcommon.ContractTypeTemplate), 1, false)
	entry.Spend()
	view := NewUtxoViewpoint()
	view.Entries()[prevOut] = entry
	block := btcutil.NewBlock(&wire.MsgBlock{
		Transactions: []*wire.MsgTx{testEVMCoinbaseTx(), testCommonResultTx(t, prevOut)},
	})

	activity, err := NewCompositeContractBlockValidator(CompositeContractBlockValidatorConfig{
		ChainParams: &chaincfg.TestNetParams,
	}).blockActivity(block, view)
	if err != nil {
		t.Fatalf("blockActivity failed: %v", err)
	}
	if !activity.Template || activity.EVM || activity.Agent {
		t.Fatalf("activity mismatch: got %+v", activity)
	}
}

func TestPreviousOutputScriptResolverAllowsCurrentBlockSpentEntry(t *testing.T) {
	prevOut := wire.OutPoint{Hash: chainhash.Hash{7}, Index: 0}
	txOut := testContractTxOut(t, contractcommon.ContractTypeTemplate)
	entry := NewUtxoEntry(txOut, 1, false)
	entry.Spend()
	view := NewUtxoViewpoint()
	view.Entries()[prevOut] = entry

	got, ok := previousOutputScriptResolver(view)(prevOut)
	if !ok {
		t.Fatalf("expected spent current-block entry to resolve")
	}
	if string(got) != string(txOut.PkScript) {
		t.Fatalf("script mismatch")
	}
}

func TestTemplateValidatorRejectsUnexpectedResultWithoutPlan(t *testing.T) {
	prevOut := wire.OutPoint{Hash: chainhash.Hash{3}, Index: 0}
	view := NewUtxoViewpoint()
	view.Entries()[prevOut] = NewUtxoEntry(testContractTxOut(t, contractcommon.ContractTypeTemplate), 1, false)
	coinbase := testEVMCoinbaseTx()
	if err := contractengine.UpsertCoinbaseStateRoot(coinbase, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	block := btcutil.NewBlock(&wire.MsgBlock{
		Transactions: []*wire.MsgTx{coinbase, testCommonResultTx(t, prevOut)},
	})
	block.SetHeight(100)

	err := NewTemplateBlockExecutionValidator(TemplateBlockExecutionConfig{
		ChainParams:         &chaincfg.TestNetParams,
		SkipStateRootVerify: true,
	}).ValidateTemplateBlock(block, view)
	if err == nil || !strings.Contains(err.Error(), "unexpected template RESULT transaction") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAgentValidatorRejectsUnexpectedResultWithoutPlan(t *testing.T) {
	prevOut := wire.OutPoint{Hash: chainhash.Hash{4}, Index: 0}
	view := NewUtxoViewpoint()
	view.Entries()[prevOut] = NewUtxoEntry(testContractTxOut(t, contractcommon.ContractTypeAgent), 1, false)
	coinbase := testEVMCoinbaseTx()
	if err := contractengine.UpsertCoinbaseStateRoot(coinbase, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	block := btcutil.NewBlock(&wire.MsgBlock{
		Transactions: []*wire.MsgTx{coinbase, testCommonResultTx(t, prevOut)},
	})
	block.SetHeight(100)

	err := NewAgentBlockExecutionValidator(AgentBlockExecutionConfig{
		ChainParams:         &chaincfg.TestNetParams,
		SkipStateRootVerify: true,
	}).ValidateAgentBlock(block, view)
	if err == nil || !strings.Contains(err.Error(), "unexpected agent RESULT transaction") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testCommonResultTx(t *testing.T, prevOut wire.OutPoint) *wire.MsgTx {
	t.Helper()
	script, err := contractcommon.ResultNullDataScript(contractcommon.ResultPayload{
		Status:      contractcommon.ResultStatusSuccess,
		ResultCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&prevOut, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	return tx
}

func testContractTxOut(t *testing.T, contractType byte) *wire.TxOut {
	t.Helper()
	contract, err := contractcommon.NewContractAddressFromHash(
		contractcommon.TestnetContractPrefix, contractcommon.AddressVersionV1,
		contractType, testBytes(20, contractType))
	if err != nil {
		t.Fatal(err)
	}
	switch contractType {
	case contractcommon.ContractTypeTemplate:
		txOut, err := template.NewContractTxOut(1, nil, contract)
		if err != nil {
			t.Fatal(err)
		}
		return txOut
	case contractcommon.ContractTypeAgent:
		txOut, err := agent.NewContractTxOut(1, nil, contract)
		if err != nil {
			t.Fatal(err)
		}
		return txOut
	default:
		script, err := contractcommon.ContractPkScript(contract)
		if err != nil {
			t.Fatal(err)
		}
		return wire.NewTxOut(1, nil, script)
	}
}
