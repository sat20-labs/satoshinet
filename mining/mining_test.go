// Copyright (c) 2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package mining

import (
	"bytes"
	"container/heap"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	scommon "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/btcutil/hdkeychain"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractapi "github.com/sat20-labs/satoshinet/contract"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	contractengine "github.com/sat20-labs/satoshinet/contract/engine"
	"github.com/sat20-labs/satoshinet/contract/evm"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/database"
	_ "github.com/sat20-labs/satoshinet/database/ffldb"
	satsindexer "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/tyler-smith/go-bip39"
)

const miningTestBootstrapMnemonic = "acquire pet news congress unveil erode paddle crumble blue fish match eye"

type anchorTemplateTxSource struct {
	descs  []*TxDesc
	hashes map[chainhash.Hash]struct{}
}

func newAnchorTemplateTxSource(txs ...*btcutil.Tx) *anchorTemplateTxSource {
	source := &anchorTemplateTxSource{
		descs:  make([]*TxDesc, 0, len(txs)),
		hashes: make(map[chainhash.Hash]struct{}, len(txs)),
	}
	for _, tx := range txs {
		source.descs = append(source.descs, &TxDesc{Tx: tx, Added: time.Now()})
		source.hashes[*tx.Hash()] = struct{}{}
	}
	return source
}

func (s *anchorTemplateTxSource) LastUpdated() time.Time { return time.Now() }
func (s *anchorTemplateTxSource) MiningDescs() []*TxDesc { return s.descs }
func (s *anchorTemplateTxSource) HaveTransaction(hash *chainhash.Hash) bool {
	_, ok := s.hashes[*hash]
	return ok
}
func (s *anchorTemplateTxSource) Count() int { return len(s.descs) }

func TestBlockHasEVMWorkIgnoresTemplateTransactions(t *testing.T) {
	tx, _, err := tmplcontract.BuildDeployTx(tmplcontract.DeployTxBuildRequest{
		ContractPrefix: tmplcontract.TestnetContractPrefix,
		Contract:       tmplcontract.NewLimitOrderContract("ordx:f:test"),
		Deployer:       "miner-test",
		DeployNonce:    1,
		GasLimit:       1000,
		Funding: wire.TxOut{
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

func TestContractPrefixUsesNetworkIdentity(t *testing.T) {
	params := chaincfg.MainNetParams
	params.Name = "renamed-mainnet"
	if got := contractPrefix(&params); got != contractapi.MainnetContractPrefix {
		t.Fatalf("renamed mainnet contract prefix=%q, want %q", got, contractapi.MainnetContractPrefix)
	}
	if got := contractPrefix(&chaincfg.TestNetParams); got != contractapi.TestnetContractPrefix {
		t.Fatalf("testnet contract prefix=%q, want %q", got, contractapi.TestnetContractPrefix)
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
		feePerKB:         100000,
		priority:         100000,
		contractTx:       true,
		contractTxType:   evmcommon.TxTypeInvoke,
		contractGasLimit: 100000,
	}
	evmHigherGas := &txPrioItem{
		feePerKB:         2,
		priority:         2,
		contractTx:       true,
		contractTxType:   evmcommon.TxTypeInvoke,
		contractGasLimit: 200000,
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
		Action:    evmcommon.ContractInvokeAPICall,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Index: 0}})
	tx.AddTxOut(&wire.TxOut{PkScript: contractScript})
	tx.AddTxOut(&wire.TxOut{PkScript: invokeScript})

	isEVM, _, txType, gas := contractMiningInfo(tx, evm.TestnetContractPrefix)
	if !isEVM {
		t.Fatalf("expected invoke tx to be classified as EVM")
	}
	if txType != evmcommon.TxTypeInvoke {
		t.Fatalf("unexpected tx type %v", txType)
	}
	if gas != 123 {
		t.Fatalf("unexpected gas limit %d", gas)
	}

	isEVM, _, _, _ = contractMiningInfo(wire.NewMsgTx(2), evm.TestnetContractPrefix)
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
		contractTx: true,
		feeAssets: wire.TxAssets{{
			Name:   wire.AssetName{Protocol: "ordx", Type: "gas", Ticker: "evm"},
			Amount: *scommon.NewDefaultDecimal(1),
		}},
	}
	if shouldSkipLowFeeTx(evmWithAssetFee, true, 100, policy) {
		t.Fatalf("EVM transaction with asset fee must not be skipped by satoshi min fee")
	}

	evmWithoutAssetFee := &txPrioItem{contractTx: true}
	if shouldSkipLowFeeTx(evmWithoutAssetFee, true, 100, policy) {
		t.Fatalf("EVM transaction must not be skipped by satoshi min fee")
	}
}

func TestProtocolZeroFeeTransactionsBypassTemplateFloor(t *testing.T) {
	policy := &Policy{
		BlockMinWeight: 0,
		TxMinFreeFee:   btcutil.Amount(10),
	}
	if !shouldSkipLowFeeTx(&txPrioItem{}, true, 1, policy) {
		t.Fatal("ordinary zero-fee transaction must remain subject to the template fee floor")
	}
	if shouldSkipLowFeeTx(&txPrioItem{protocolFeeExempt: true}, true, 1, policy) {
		t.Fatal("anchor/deanchor transaction must bypass the satoshi fee floor")
	}
	if !shouldSkipLowFeeTx(&txPrioItem{protocolFeeCandidate: true}, true, 1, policy) {
		t.Fatal("classification alone must not grant a protocol fee exemption")
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
	if err := contractapi.VerifyCoinbaseStateRoot(coinbaseTx.MsgTx(), stateRoot); err != nil {
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

func TestCoinbaseSignerIsolatedAcrossGenerators(t *testing.T) {
	for _, name := range []string{"first", "second"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			generator := NewBlkTmplGenerator(nil, &chaincfg.TestNetParams, nil, nil, nil, nil, nil)
			if generator.coinbaseSigner == nil {
				t.Fatal("constructor did not initialize the signer")
			}
			signature := []byte("signature-" + t.Name())
			generator.coinbaseSigner = func([]byte) ([]byte, error) { return signature, nil }
			const height = int32(100)
			coinbase, err := createCoinbaseTx(&chaincfg.TestNetParams, nil, height, nil)
			if err != nil {
				t.Fatal(err)
			}
			block := &wire.MsgBlock{Transactions: []*wire.MsgTx{coinbase.MsgTx()}}
			checkScript := func(script []byte, nonce uint64) {
				t.Helper()
				want, err := txscript.NewScriptBuilder().AddInt64(int64(height)).
					AddInt64(int64(nonce)).AddData(signature).Script()
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(script, want) {
					t.Fatalf("coinbase script=%x, want %x", script, want)
				}
			}
			for nonce := uint64(0); nonce < 100; nonce++ {
				script, err := generator.standardCoinbaseScript(height, nonce)
				if err != nil {
					t.Fatal(err)
				}
				checkScript(script, nonce)
				if err := generator.UpdateExtraNonce(block, height, nonce+1); err != nil {
					t.Fatal(err)
				}
				checkScript(block.Transactions[0].TxIn[0].SignatureScript, nonce+1)
			}
		})
	}
}

func TestCoinbaseSignerErrorPropagates(t *testing.T) {
	generator := NewBlkTmplGenerator(nil, nil, nil, nil, nil, nil, nil)
	signErr := errors.New("test signer failure")
	generator.coinbaseSigner = func([]byte) ([]byte, error) { return nil, signErr }
	if script, err := generator.standardCoinbaseScript(100, 0); !errors.Is(err, signErr) || script != nil {
		t.Fatalf("script=%x err=%v, want signer failure", script, err)
	}
	if err := generator.UpdateExtraNonce(nil, 100, 1); !errors.Is(err, signErr) {
		t.Fatalf("UpdateExtraNonce error=%v, want signer failure", err)
	}
}

func TestAnchorTemplateSelectionAcceptsValidAndRejectsInvalidOrDuplicate(t *testing.T) {
	params := chaincfg.TestNetParams
	db, err := database.Create("ffldb", filepath.Join(t.TempDir(), "blocks"), params.Net)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	timeSource := blockchain.NewMedianTime()
	chain, err := blockchain.New(&blockchain.Config{
		DB: db, ChainParams: &params, TimeSource: timeSource,
	})
	if err != nil {
		t.Fatal(err)
	}

	coreKey, peerKey := miningTestAnchorKeys(t)
	witnessScript, lockedPkScript, err := anchortx.GetP2WSHscript(
		coreKey.PubKey().SerializeCompressed(), peerKey.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}

	const (
		validFunding   = "111122223333444455556666777788889999aaaabbbbccccddddeeeeffff0000:1"
		invalidFunding = "22223333444455556666777788889999aaaabbbbccccddddeeeeffff00001111:0"
		value          = int64(21000)
	)
	server := miningTestL1Indexer(t, map[string]*scommon.AssetsInUtxo{
		validFunding: {
			OutPoint: validFunding, Value: value, PkScript: lockedPkScript,
		},
		invalidFunding: {
			OutPoint: invalidFunding, Value: value - 1, PkScript: lockedPkScript,
		},
	})
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")
	if !anchortx.StartAnchorManager(&anchortx.AnchorConfig{
		IndexerScheme: "http", IndexerHost: host, IndexerProxy: "testnet", ChainParams: &params,
	}) {
		t.Fatal("failed to start anchor manager")
	}
	t.Cleanup(anchortx.Stop)

	valid := miningTestAnchorTx(t, validFunding, witnessScript, value, coreKey, txscript.OP_TRUE)
	duplicate := miningTestAnchorTx(t, validFunding, witnessScript, value, coreKey, txscript.OP_2)
	invalid := miningTestAnchorTx(t, invalidFunding, witnessScript, value, coreKey, txscript.OP_3)
	invalidAmount := valid.MsgTx().Copy()
	invalidAmount.TxOut[0].Value--
	invalidSignature := valid.MsgTx().Copy()
	invalidSignature.TxIn[0].SignatureScript = []byte{txscript.OP_TRUE}
	deanchorMarker, err := satsindexer.NullDataScript(satsindexer.CONTENT_TYPE_DESCENDING, []byte("missing-input"))
	if err != nil {
		t.Fatal(err)
	}
	invalidDeanchor := wire.NewMsgTx(2)
	invalidDeanchor.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{9}, Index: 0}, nil, nil))
	invalidDeanchor.AddTxOut(wire.NewTxOut(330, nil, deanchorMarker))
	if !blockchain.IsDeAnchorTx(invalidDeanchor) {
		t.Fatal("test transaction was not classified as deanchor")
	}
	source := newAnchorTemplateTxSource(valid, duplicate, invalid, btcutil.NewTx(invalidAmount),
		btcutil.NewTx(invalidSignature), btcutil.NewTx(invalidDeanchor))
	generator := NewBlkTmplGenerator(&Policy{
		BlockMaxWeight: blockchain.MaxBlockWeight,
		BlockMinWeight: 0,
		TxMinFreeFee:   btcutil.Amount(10),
	}, &params, source, chain, timeSource, txscript.NewSigCache(10), txscript.NewHashCache(10))
	generator.coinbaseSigner = func([]byte) ([]byte, error) { return []byte("test-signature"), nil }

	template, err := generator.NewBlockTemplate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(template.Block.Transactions) != 2 {
		t.Fatalf("template transaction count=%d, want coinbase plus one anchor", len(template.Block.Transactions))
	}
	includedHash := template.Block.Transactions[1].TxHash()
	if includedHash != *valid.Hash() && includedHash != *duplicate.Hash() {
		t.Fatalf("template included unexpected transaction %s", includedHash)
	}
	if includedHash == *invalid.Hash() {
		t.Fatalf("template included invalid anchor %s", includedHash)
	}
	lockedInfo, err := anchortx.GetLockedTxInfo(template.Block.Transactions[1], false)
	if err != nil {
		t.Fatal(err)
	}
	if lockedInfo.Utxo != validFunding {
		t.Fatalf("included funding utxo=%s, want %s", lockedInfo.Utxo, validFunding)
	}
	if len(template.Fees) != 2 || len(template.SigOpCosts) != 2 {
		t.Fatalf("template accounting missing anchor: fees=%v sigops=%v", template.Fees, template.SigOpCosts)
	}

	// Use a real signed P2WSH spend of an in-template anchor. Supply the
	// transactions in reverse dependency order to exercise deferred validation.
	channel, err := btcutil.NewAddressWitnessScriptHash(chainhash.HashB(witnessScript), &params)
	if err != nil {
		t.Fatal(err)
	}
	parent := miningTestAnchorTx(t, validFunding, witnessScript, value, coreKey, txscript.OP_TRUE)
	parent.MsgTx().TxOut[0].PkScript = lockedPkScript
	keys := []*btcec.PrivateKey{coreKey, peerKey}
	if bytes.Compare(coreKey.PubKey().SerializeCompressed(), peerKey.PubKey().SerializeCompressed()) > 0 {
		keys[0], keys[1] = keys[1], keys[0]
	}
	signSpend := func(tx *wire.MsgTx, amount int64) {
		t.Helper()
		fetcher := txscript.NewCannedPrevOutputFetcher(lockedPkScript, amount, nil)
		hashes := txscript.NewTxSigHashes(tx, fetcher)
		witness := wire.TxWitness{nil}
		for _, key := range keys {
			sig, err := txscript.RawTxInWitnessSignature(tx, hashes, 0, amount, nil, witnessScript, txscript.SigHashAll, key)
			if err != nil {
				t.Fatal(err)
			}
			witness = append(witness, sig)
		}
		tx.TxIn[0].Witness = append(witness, witnessScript)
	}
	for _, test := range []struct {
		name                                   string
		legacy, badPayload, badChannel, badSig bool
		op                                     uint8
		fee                                    int64
		wantIncluded                           bool
	}{
		{name: "legacy deanchor", legacy: true, wantIncluded: true},
		{name: "close deanchor", op: satsindexer.DESCEND_OP_CLOSE, wantIncluded: true},
		{name: "splicing deanchor", op: satsindexer.DESCEND_OP_SPLICING_OUT, wantIncluded: true},
		{name: "force close zero txid", op: satsindexer.DESCEND_OP_FORCE_CLOSE, wantIncluded: true},
		{name: "malformed deanchor", op: satsindexer.DESCEND_OP_CLOSE, badPayload: true},
		{name: "wrong channel", op: satsindexer.DESCEND_OP_CLOSE, badChannel: true},
		{name: "bad deanchor signature", op: satsindexer.DESCEND_OP_CLOSE, badSig: true},
		{name: "malformed pays ordinary fee", op: satsindexer.DESCEND_OP_CLOSE, badPayload: true, fee: 1000, wantIncluded: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			l1TxID := strings.Repeat("1", 64)
			if test.op == satsindexer.DESCEND_OP_FORCE_CLOSE {
				l1TxID = strings.Repeat("0", 64)
			}
			payload := []byte(l1TxID)
			if !test.legacy {
				payload, err = satsindexer.EncodeDescendPayloadV2(l1TxID, test.op, nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			if test.badPayload {
				payload = append(payload, 0)
			}
			marker, err := satsindexer.NullDataScript(satsindexer.CONTENT_TYPE_DESCENDING, payload)
			if err != nil {
				t.Fatal(err)
			}
			channelID := channel.EncodeAddress()
			if test.badChannel {
				channelID = "not-a-channel"
			}
			channelMarker, err := satsindexer.NullDataScript(satsindexer.CONTENT_TYPE_CHANNELID, []byte(channelID))
			if err != nil {
				t.Fatal(err)
			}
			deanchor := wire.NewMsgTx(2)
			deanchor.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: *parent.Hash()}, nil, nil))
			deanchor.AddTxOut(wire.NewTxOut(1000, nil, marker))
			change := value - 1000 - test.fee
			deanchor.AddTxOut(wire.NewTxOut(change, nil, lockedPkScript))
			deanchor.AddTxOut(wire.NewTxOut(0, nil, channelMarker))
			signSpend(deanchor, value)
			if test.badSig {
				deanchor.TxIn[0].Witness[1] = []byte{1}
			}
			deanchorTx := btcutil.NewTx(deanchor)
			view := blockchain.NewUtxoViewpoint()
			view.AddTxOuts(parent, 1)
			if err := blockchain.ValidateTransactionScripts(deanchorTx, view, txscript.StandardVerifyFlags, nil, txscript.NewHashCache(10)); (err != nil) != test.badSig {
				t.Fatalf("fixture signature validation error=%v, badSig=%v", err, test.badSig)
			}
			descendant := wire.NewMsgTx(2)
			descendant.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: *deanchorTx.Hash(), Index: 1}, nil, nil))
			descendant.AddTxOut(wire.NewTxOut(change-10, nil, lockedPkScript))
			signSpend(descendant, change)
			txs := newAnchorTemplateTxSource(btcutil.NewTx(descendant), deanchorTx, parent)
			txs.descs[0].Fee = 10
			txs.descs[1].Fee = test.fee
			generator := NewBlkTmplGenerator(&Policy{
				BlockMaxWeight: blockchain.MaxBlockWeight, TxMinFreeFee: 10,
			}, &params, txs, chain, timeSource, txscript.NewSigCache(10), txscript.NewHashCache(10))
			generator.coinbaseSigner = func([]byte) ([]byte, error) { return []byte("test-signature"), nil }
			got, err := generator.NewBlockTemplate(nil)
			if err != nil {
				t.Fatal(err)
			}
			wantCount := 2
			if test.wantIncluded {
				wantCount = 4
			}
			if len(got.Block.Transactions) != wantCount || len(got.Fees) != wantCount || len(got.SigOpCosts) != wantCount {
				t.Fatalf("template counts: txs=%d fees=%d sigops=%d, want %d", len(got.Block.Transactions), len(got.Fees), len(got.SigOpCosts), wantCount)
			}
			if test.wantIncluded && (got.Block.Transactions[2].TxHash() != deanchor.TxHash() || got.Block.Transactions[3].TxHash() != descendant.TxHash()) {
				t.Fatal("deanchor or descendant missing or out of dependency order")
			}
		})
	}
}

func miningTestAnchorTx(t *testing.T, funding string, witnessScript []byte, value int64,
	key *btcec.PrivateKey, outputOpcode byte) *btcutil.Tx {

	t.Helper()
	invoice, err := anchortx.StandardAnchorScript(funding, witnessScript, value, nil)
	if err != nil {
		t.Fatal(err)
	}
	sig := ecdsa.Sign(key, chainhash.HashB(invoice))
	assets := wire.TxAssets{}
	assetsBuf, err := wire.SerializeTxAssets(&assets)
	if err != nil {
		t.Fatal(err)
	}
	anchorScript, err := txscript.NewScriptBuilder().
		AddData([]byte(funding)).
		AddData(witnessScript).
		AddInt64(value).
		AddData(assetsBuf).
		AddData(sig.Serialize()).
		Script()
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{}, Index: wire.AnchorTxOutIndex},
		SignatureScript:  anchorScript,
	})
	tx.AddTxOut(wire.NewTxOut(value, nil, []byte{outputOpcode}))
	payload := scommon.ASSET_PLAIN_SAT.String() + "-21000000000000000-" +
		strconv.Itoa(0) + "-" + strconv.Itoa(1)
	marker, err := satsindexer.NullDataScript(satsindexer.CONTENT_TYPE_ASCENDING, []byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, marker))
	return btcutil.NewTx(tx)
}

func miningTestL1Indexer(t *testing.T, utxos map[string]*scommon.AssetsInUtxo) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/testnet/v3/utxo/info/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			t.Errorf("unexpected L1 indexer path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		utxo := strings.TrimPrefix(r.URL.Path, prefix)
		data, ok := utxos[utxo]
		response := indexerwire.TxOutputRespV3{Data: data}
		if ok {
			response.BaseResp = indexerwire.BaseResp{Code: 0, Msg: "ok"}
		} else {
			response.BaseResp = indexerwire.BaseResp{Code: 404, Msg: "missing utxo"}
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("encode L1 response: %v", err)
		}
	}))
}

func miningTestAnchorKeys(t *testing.T) (*btcec.PrivateKey, *btcec.PrivateKey) {
	t.Helper()
	oldEnableTesting := scommon.ENABLE_TESTING
	scommon.ENABLE_TESTING = true
	t.Cleanup(func() { scommon.ENABLE_TESTING = oldEnableTesting })
	coreKey := miningTestAnchorKey(t, 0)
	if got := hex.EncodeToString(coreKey.PubKey().SerializeCompressed()); got != scommon.GetBootstrapPubKey() {
		t.Fatalf("bootstrap key=%s, want %s", got, scommon.GetBootstrapPubKey())
	}
	return coreKey, miningTestAnchorKey(t, 1)
}

func miningTestAnchorKey(t *testing.T, index uint32) *btcec.PrivateKey {
	t.Helper()
	if !bip39.IsMnemonicValid(miningTestBootstrapMnemonic) {
		t.Fatal("invalid test bootstrap mnemonic")
	}
	master, err := hdkeychain.NewMaster(bip39.NewSeed(miningTestBootstrapMnemonic, ""), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range []uint32{
		hdkeychain.HardenedKeyStart + 86,
		hdkeychain.HardenedKeyStart,
		hdkeychain.HardenedKeyStart,
		0,
		index,
	} {
		master, err = master.Derive(child)
		if err != nil {
			t.Fatal(err)
		}
	}
	key, err := master.ECPrivKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}
