// Copyright (c) 2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package mining

import (
	"container/heap"
	"math/rand"
	"testing"
	"time"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractengine "github.com/sat20-labs/satoshinet/contract"
	evmcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/contract/evm"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestBlockHasEVMWorkIgnoresTemplateTransactions(t *testing.T) {
	tx, _, err := tmplcontract.BuildDeployTx(tmplcontract.DeployTxBuildRequest{
		ContractPrefix: tmplcontract.TestnetContractPrefix,
		Contract:       tmplcontract.NewLimitOrderContract("ordx:f:test"),
		Deployer:       "miner-test",
		Random:         []byte("miner-template-random"),
		GasLimit:       1000,
		Funding: tmplcontract.TxFunding{
			Value: 1,
		},
	})
	if err != nil {
		t.Fatalf("BuildDeployTx: %v", err)
	}

	txs := []*btcutil.Tx{btcutil.NewTx(tx)}
	if !contractengine.BlockHasContractTypeWork(txs, &chaincfg.TestNetParams, evmcommon.ContractTypeTemplate) {
		t.Fatal("expected template deploy to be classified as template work")
	}
	if contractengine.BlockHasContractTypeWork(txs, &chaincfg.TestNetParams, evmcommon.ContractTypeEVM) {
		t.Fatal("template deploy must not be classified as EVM work")
	}
}

// TestTxFeePrioHeap ensures the priority queue for transaction fees and
// priorities works as expected.
func TestTxFeePrioHeap(t *testing.T) {
	// Create some fake priority items that exercise the expected sort
	// edge conditions.
	testItems := []*txPrioItem{
		{feePerKB: 5678, priority: 3},
		{feePerKB: 5678, priority: 1},
		{feePerKB: 5678, priority: 1}, // Duplicate fee and prio
		{feePerKB: 5678, priority: 5},
		{feePerKB: 5678, priority: 2},
		{feePerKB: 1234, priority: 3},
		{feePerKB: 1234, priority: 1},
		{feePerKB: 1234, priority: 5},
		{feePerKB: 1234, priority: 5}, // Duplicate fee and prio
		{feePerKB: 1234, priority: 2},
		{feePerKB: 10000, priority: 0}, // Higher fee, smaller prio
		{feePerKB: 0, priority: 10000}, // Higher prio, lower fee
	}

	// Add random data in addition to the edge conditions already manually
	// specified.
	randSeed := rand.Int63()
	defer func() {
		if t.Failed() {
			t.Logf("Random numbers using seed: %v", randSeed)
		}
	}()
	prng := rand.New(rand.NewSource(randSeed))
	for i := 0; i < 1000; i++ {
		testItems = append(testItems, &txPrioItem{
			feePerKB: int64(prng.Float64() * btcutil.SatoshiPerBitcoin),
			priority: prng.Float64() * 100,
		})
	}

	// Test sorting by fee per KB then priority.
	var highest *txPrioItem
	priorityQueue := newTxPriorityQueue(len(testItems), true)
	for i := 0; i < len(testItems); i++ {
		prioItem := testItems[i]
		if highest == nil {
			highest = prioItem
		}
		if prioItem.feePerKB >= highest.feePerKB &&
			prioItem.priority > highest.priority {

			highest = prioItem
		}
		heap.Push(priorityQueue, prioItem)
	}

	for i := 0; i < len(testItems); i++ {
		prioItem := heap.Pop(priorityQueue).(*txPrioItem)
		if prioItem.feePerKB >= highest.feePerKB &&
			prioItem.priority > highest.priority {

			t.Fatalf("fee sort: item (fee per KB: %v, "+
				"priority: %v) higher than than prev "+
				"(fee per KB: %v, priority %v)",
				prioItem.feePerKB, prioItem.priority,
				highest.feePerKB, highest.priority)
		}
		highest = prioItem
	}

	// Test sorting by priority then fee per KB.
	highest = nil
	priorityQueue = newTxPriorityQueue(len(testItems), false)
	for i := 0; i < len(testItems); i++ {
		prioItem := testItems[i]
		if highest == nil {
			highest = prioItem
		}
		if prioItem.priority >= highest.priority &&
			prioItem.feePerKB > highest.feePerKB {

			highest = prioItem
		}
		heap.Push(priorityQueue, prioItem)
	}

	for i := 0; i < len(testItems); i++ {
		prioItem := heap.Pop(priorityQueue).(*txPrioItem)
		if prioItem.priority >= highest.priority &&
			prioItem.feePerKB > highest.feePerKB {

			t.Fatalf("priority sort: item (fee per KB: %v, "+
				"priority: %v) higher than than prev "+
				"(fee per KB: %v, priority %v)",
				prioItem.feePerKB, prioItem.priority,
				highest.feePerKB, highest.priority)
		}
		highest = prioItem
	}
}

func TestTxPriorityQueueEVMOrdering(t *testing.T) {
	ordinaryLowFee := &txPrioItem{
		feePerKB: 1,
		priority: 1,
	}
	evmHighFee := &txPrioItem{
		feePerKB: 100000,
		priority: 100000,
		evmTx:    true,
		evmType:  evm.TxTypeInvoke,
		evmGas:   100000,
	}
	evmHigherGas := &txPrioItem{
		feePerKB: 2,
		priority: 2,
		evmTx:    true,
		evmType:  evm.TxTypeInvoke,
		evmGas:   200000,
	}

	priorityQueue := newTxPriorityQueue(3, true)
	heap.Push(priorityQueue, evmHighFee)
	heap.Push(priorityQueue, ordinaryLowFee)
	heap.Push(priorityQueue, evmHigherGas)

	if got := heap.Pop(priorityQueue).(*txPrioItem); got != ordinaryLowFee {
		t.Fatalf("ordinary transaction must sort before EVM transactions")
	}
	if got := heap.Pop(priorityQueue).(*txPrioItem); got != evmHigherGas {
		t.Fatalf("EVM transactions must sort by gas limit before fee")
	}
	if got := heap.Pop(priorityQueue).(*txPrioItem); got != evmHighFee {
		t.Fatalf("unexpected last item")
	}
}

func TestEVMMiningInfo(t *testing.T) {
	addr := evm.EVMAddress{1, 2, 3}
	contract, err := evm.NewContractAddress(evm.TestnetContractPrefix,
		evm.AddressVersionV1, evm.ContractTypeEVM, addr)
	if err != nil {
		t.Fatal(err)
	}
	contractScript, err := evm.ContractPkScript(contract)
	if err != nil {
		t.Fatal(err)
	}
	invokeScript, err := evmcommon.InvokeNullDataScript(evm.InvokePayload{
		GasLimit:  123,
		CallNonce: 1,
		Calldata:  []byte{0xaa},
	})
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Index: 0}})
	tx.AddTxOut(&wire.TxOut{PkScript: contractScript})
	tx.AddTxOut(&wire.TxOut{PkScript: invokeScript})

	isEVM, txType, gas := evmMiningInfo(tx, evm.TestnetContractPrefix)
	if !isEVM {
		t.Fatalf("expected invoke tx to be classified as EVM")
	}
	if txType != evm.TxTypeInvoke {
		t.Fatalf("unexpected tx type %v", txType)
	}
	if gas != 123 {
		t.Fatalf("unexpected gas limit %d", gas)
	}

	isEVM, _, _ = evmMiningInfo(wire.NewMsgTx(2), evm.TestnetContractPrefix)
	if isEVM {
		t.Fatalf("empty non-EVM tx classified as EVM")
	}
}

func TestEVMAssetFeeBypassesSatoshiMinFreeFee(t *testing.T) {
	policy := &Policy{
		BlockMinWeight: 100,
		TxMinFreeFee:   btcutil.Amount(10),
	}
	evmWithAssetFee := &txPrioItem{
		evmTx: true,
		feeAssets: wire.TxAssets{{
			Name:   wire.AssetName{Protocol: "ordx", Type: "gas", Ticker: "evm"},
			Amount: *scommon.NewDefaultDecimal(1),
		}},
	}
	if shouldSkipLowFeeTx(evmWithAssetFee, true, 100, policy) {
		t.Fatalf("EVM transaction with asset fee must not be skipped by satoshi min fee")
	}

	evmWithoutAssetFee := &txPrioItem{evmTx: true}
	if shouldSkipLowFeeTx(evmWithoutAssetFee, true, 100, policy) {
		t.Fatalf("EVM transaction must not be skipped by satoshi min fee")
	}
}

func TestAddEVMResultsToTemplateCommitsRootAndFees(t *testing.T) {
	contract, err := evm.NewContractAddress(evm.TestnetContractPrefix,
		evm.AddressVersionV1, evm.ContractTypeEVM, evm.EVMAddress{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	contractScript, err := evm.ContractPkScript(contract)
	if err != nil {
		t.Fatal(err)
	}
	gasAsset := wire.NewAssetNameFromString("ordx:ft:gas")
	if gasAsset == nil {
		t.Fatal("invalid test gas asset")
	}

	fundingTx := wire.NewMsgTx(2)
	fundingTx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}, nil, nil))
	fundingTx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{
		Name:   *gasAsset,
		Amount: *scommon.NewDefaultDecimal(100),
	}}, contractScript))
	funding := btcutil.NewTx(fundingTx)

	resultScript, err := evmcommon.ResultNullDataScript(evm.ResultPayload{
		Status:      evm.ResultStatusSuccess,
		ResultCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	resultTx := wire.NewMsgTx(2)
	resultTx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: *funding.Hash(), Index: 0}, nil, nil))
	resultTx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{
		Name:   *gasAsset,
		Amount: *scommon.NewDefaultDecimal(60),
	}}, contractScript))
	resultTx.AddTxOut(wire.NewTxOut(0, nil, resultScript))

	var stateRoot [32]byte
	stateRoot[0] = 0xaa
	builderCalled := false
	policy := &Policy{
		BlockMaxWeight: blockchain.MaxBlockWeight,
		ContractResultBuilder: func(req ContractBuildRequest) (ContractBuildResult, error) {
			builderCalled = true
			if req.Height != 100 {
				t.Fatalf("unexpected height %d", req.Height)
			}
			return ContractBuildResult{
				ResultTxs: []*wire.MsgTx{resultTx},
				StateRoot: stateRoot,
			}, nil
		},
	}
	g := &BlkTmplGenerator{
		policy:      policy,
		chainParams: &chaincfg.TestNetParams,
		sigCache:    txscript.NewSigCache(10),
		hashCache:   txscript.NewHashCache(10),
	}
	coinbaseTx := btcutil.NewTx(wire.NewMsgTx(2))
	coinbaseTx.MsgTx().AddTxIn(wire.NewTxIn(&wire.OutPoint{
		Hash:  chainhash.Hash{},
		Index: wire.MaxPrevOutIndex,
	}, nil, nil))
	coinbaseTx.MsgTx().AddTxOut(wire.NewTxOut(0, nil, []byte{txscript.OP_TRUE}))
	blockTxns := []*btcutil.Tx{coinbaseTx}
	view := blockchain.NewUtxoViewpoint()
	view.AddTxOuts(funding, 99)

	weight, sigOps, txFees, txSigOps, fees, feeAssets, err :=
		g.addContractResultsToTemplate(&blockTxns, coinbaseTx, view, 100,
			chainhash.Hash{9}, time.Unix(1710000000, 0), true, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !builderCalled {
		t.Fatal("expected EVM result builder to be called")
	}
	if weight == 0 {
		t.Fatal("expected EVM result tx/root to add block weight")
	}
	if sigOps != 0 || len(txSigOps) != 1 || txSigOps[0] != 0 {
		t.Fatalf("unexpected sigops: total=%d per-tx=%v", sigOps, txSigOps)
	}
	if fees != 0 || len(txFees) != 1 || txFees[0] != 0 {
		t.Fatalf("unexpected satoshi fees: total=%d per-tx=%v", fees, txFees)
	}
	if len(blockTxns) != 2 || blockTxns[1].MsgTx() != resultTx {
		t.Fatal("expected EVM result tx to be appended")
	}
	if len(feeAssets) != 1 ||
		feeAssets[0].Name.String() != "ordx:ft:gas" ||
		feeAssets[0].Amount.Cmp(scommon.NewDefaultDecimal(40)) != 0 {
		t.Fatalf("unexpected EVM fee assets: %v", feeAssets)
	}
	if err := contractengine.VerifyCoinbaseStateRoot(coinbaseTx.MsgTx(), stateRoot); err != nil {
		t.Fatal(err)
	}
}

func TestMergeMissingEVMResultUtxosFetchesContractAssetInput(t *testing.T) {
	contract, err := evm.NewContractAddress(evm.TestnetContractPrefix,
		evm.AddressVersionV1, evm.ContractTypeEVM, evm.EVMAddress{4, 5, 6})
	if err != nil {
		t.Fatal(err)
	}
	contractScript, err := evm.ContractPkScript(contract)
	if err != nil {
		t.Fatal(err)
	}
	gasAsset := wire.NewAssetNameFromString("ordx:ft:gas")
	if gasAsset == nil {
		t.Fatal("invalid test gas asset")
	}

	fundingTx := wire.NewMsgTx(2)
	fundingTx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{7}, Index: 0}, nil, nil))
	fundingTx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{
		Name:   *gasAsset,
		Amount: *scommon.NewDefaultDecimal(100),
	}}, contractScript))
	funding := btcutil.NewTx(fundingTx)

	resultTx := wire.NewMsgTx(2)
	resultInput := wire.OutPoint{Hash: *funding.Hash(), Index: 0}
	resultTx.AddTxIn(wire.NewTxIn(&resultInput, nil, nil))
	result := btcutil.NewTx(resultTx)

	blockUtxos := blockchain.NewUtxoViewpoint()
	fetches := 0
	err = mergeMissingContractResultUtxos(blockUtxos, result, func(tx *btcutil.Tx) (*blockchain.UtxoViewpoint, error) {
		fetches++
		view := blockchain.NewUtxoViewpoint()
		view.AddTxOuts(funding, 99)
		return view, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if fetches != 1 {
		t.Fatalf("expected one utxo fetch, got %d", fetches)
	}
	entry := blockUtxos.LookupEntry(resultInput)
	if entry == nil || entry.IsSpent() {
		t.Fatalf("expected result input %s to be available after merge", resultInput)
	}

	err = mergeMissingContractResultUtxos(blockUtxos, result, func(tx *btcutil.Tx) (*blockchain.UtxoViewpoint, error) {
		fetches++
		return blockchain.NewUtxoViewpoint(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if fetches != 1 {
		t.Fatalf("expected no extra fetch when input already exists, got %d", fetches)
	}
}
