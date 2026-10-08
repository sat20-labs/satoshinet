package blockchain

import (
	"bytes"
	"encoding/hex"
	"fmt"
	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/database"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

type posReadiness struct {
	*testAssetReadiness
	seq *scommon.MiningSequenceMgr
}

func (r *posReadiness) GetSeqMgr() *scommon.MiningSequenceMgr { return r.seq }

func setupPOSChain(t *testing.T, networks ...*chaincfg.Params) (*BlockChain, *posReadiness, *btcec.PrivateKey, string, *wire.MsgBlock, func()) {
	t.Helper()
	chain, ready, _, teardown := setupReadinessChain(t)
	if len(networks) > 0 {
		teardown()
		params := *networks[0]
		params.Checkpoints = nil
		var err error
		chain, teardown, err = chainSetup(t.Name()+"-network", &params)
		require.NoError(t, err)
		ready = newTestAssetReadiness(0, chain.bestChain.Tip().hash, chain.interrupt)
	}
	chain.contractBlockValidator = nil
	chain.chainParams.POSV2Height = 1
	chain.chainParams.Bech32HRPSegwit = chaincfg.TestNetParams.Bech32HRPSegwit
	chain.chainParams.Checkpoints = nil
	// Sorter initialization needs the legacy activation checkpoint, even on
	// this isolated regression network. Genesis is the legacy baseline.
	chain.chainParams.Checkpoints = append(chain.chainParams.Checkpoints,
		chaincfg.Checkpoint{Height: 0, Hash: chain.chainParams.GenesisHash})
	key, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
	pub := hex.EncodeToString(key.PubKey().SerializeCompressed())
	seq := scommon.NewMiningSequenceMgr(chain.chainParams)
	if err := seq.Init(map[string]*scommon.CoreNodeInfo{pub: scommon.NewCoreNodeInfo(nil)}, 0, ""); err != nil {
		t.Fatal(err)
	}
	r := &posReadiness{testAssetReadiness: ready, seq: seq}
	chain.assetIndexReadiness = r
	var block *wire.MsgBlock
	if len(networks) == 0 {
		block = readinessTestBlock(t, chain).MsgBlock()
	} else {
		// This branch exercises legacy work selection, not proof-of-work mining.
		coinbase := wire.NewMsgTx(1)
		coinbase.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: ^uint32(0)}, nil, nil))
		coinbase.AddTxOut(wire.NewTxOut(CalcBlockSubsidy(1, chain.chainParams), nil, nil))
		block = &wire.MsgBlock{Header: wire.BlockHeader{Version: 4,
			PrevBlock: *chain.chainParams.GenesisHash,
			Timestamp: chain.chainParams.GenesisBlock.Header.Timestamp.Add(time.Second)},
			Transactions: []*wire.MsgTx{coinbase}}
	}
	coinbase := block.Transactions[0]
	sig := ecdsa.Sign(key, chainhash.HashB(scommon.GetScriptSignData(1, 0))).Serialize()
	script, err := txscript.NewScriptBuilder().AddInt64(1).AddInt64(0).AddData(sig).Script()
	if err != nil {
		t.Fatal(err)
	}
	coinbase.TxIn[0].SignatureScript = script
	addr, err := btcutil.DecodeAddress(seq.GetCurrentMiningAddr(), chain.chainParams)
	if err != nil {
		t.Fatal(err)
	}
	coinbase.TxOut[0].PkScript, err = txscript.PayToAddrScript(addr)
	if err != nil {
		t.Fatal(err)
	}
	coinbase.TxIn[0].Witness = wire.TxWitness{make([]byte, 32)}
	root := CalcMerkleRoot(btcutil.NewBlock(block).Transactions(), true)
	var preimage [64]byte
	copy(preimage[:32], root[:])
	commitment := append(append([]byte(nil), WitnessMagicBytes...), chainhash.DoubleHashB(preimage[:])...)
	coinbase.AddTxOut(wire.NewTxOut(0, nil, commitment))
	block.Header.MerkleRoot = CalcMerkleRoot(btcutil.NewBlock(block).Transactions(), false)
	return chain, r, key, pub, block, teardown
}

func signPOS(key *btcec.PrivateKey) func([]byte) ([]byte, error) {
	return func(msg []byte) ([]byte, error) { return ecdsa.Sign(key, chainhash.HashB(msg)).Serialize(), nil }
}

func TestPOSMalformedCoinbaseNumbersReturnError(t *testing.T) {
	for _, field := range []string{"height", "nonce"} {
		t.Run(field, func(t *testing.T) {
			chain, _, _, _, candidate, teardown := setupPOSChain(t)
			defer teardown()
			oversized := []byte{1, 0, 0, 0, 0, 0, 0, 0, 0x80}
			builder := txscript.NewScriptBuilder()
			if field == "height" {
				builder.AddData(oversized).AddInt64(0)
			} else {
				builder.AddInt64(1).AddData(oversized)
			}
			// No valid signature or approval is needed to reach number parsing.
			script, err := builder.AddData([]byte{1}).Script()
			require.NoError(t, err)
			malformed := candidate.Copy()
			malformed.Transactions[0].TxIn[0].SignatureScript = script
			updatePOSCommitments(malformed)
			var encoded bytes.Buffer
			require.NoError(t, malformed.Serialize(&encoded))
			var decoded wire.MsgBlock
			require.NoError(t, decoded.Deserialize(bytes.NewReader(encoded.Bytes())))
			block := btcutil.NewBlock(&decoded)
			require.NoError(t, CheckBlockSanity(block, chain.chainParams.PowLimit, chain.timeSource))
			before := chain.BestSnapshot().Hash
			require.NotPanics(t, func() {
				main, orphan, err := chain.ProcessBlock(block, BFNone)
				require.Error(t, err)
				require.False(t, main)
				require.False(t, orphan)
			})
			require.Equal(t, before, chain.BestSnapshot().Hash)
			require.False(t, rawBlockStored(t, chain, block.Hash()))
			require.False(t, chain.index.HaveBlock(block.Hash()))
		})
	}
}

func TestPOSApprovalMissingAndBadVariantsDoNotPoisonHash(t *testing.T) {
	chain, _, key, _, candidate, teardown := setupPOSChain(t)
	defer teardown()
	if _, _, err := chain.ProcessBlock(btcutil.NewBlock(candidate.Copy()), BFFastAdd); err == nil {
		t.Fatal("unsigned ordinary block accepted")
	}
	bad := candidate.Copy()
	bad.Transactions[0].TxIn[0].Witness = append(bad.Transactions[0].TxIn[0].Witness, []byte{1})
	if _, _, err := chain.ProcessBlock(btcutil.NewBlock(bad), BFNone); err == nil {
		t.Fatal("malformed signature accepted")
	}
	hash := candidate.BlockHash()
	if rawBlockStored(t, chain, &hash) || chain.index.HaveBlock(&hash) {
		t.Fatal("bad signature persisted")
	}
	if _, ok := chain.rejectedBlockError(hash); ok {
		t.Fatal("bad witness poisoned block-hash cache")
	}
	valid := candidate.Copy()
	sig, _ := signPOS(key)(scommon.POSApprovalMessage(chain.chainParams.Net, 1, hash))
	valid.Transactions[0].TxIn[0].Witness = append(valid.Transactions[0].TxIn[0].Witness, sig)
	if valid.BlockHash() != hash {
		t.Fatal("approval changed block hash")
	}
	if valid.Transactions[0].TxHash() != candidate.Transactions[0].TxHash() {
		t.Fatal("approval changed txid")
	}
	main, orphan, err := chain.ProcessBlock(btcutil.NewBlock(valid), BFFastAdd)
	if err != nil || !main || orphan {
		t.Fatalf("valid variant rejected: %v %v %v", main, orphan, err)
	}
}

func TestPOSApprovalBindsHeaderWitnessAndNetwork(t *testing.T) {
	for _, mutation := range []string{"header", "network", "height", "wrong-key", "missing-commitment", "reward"} {
		t.Run(mutation, func(t *testing.T) {
			chain, _, key, _, block, teardown := setupPOSChain(t)
			defer teardown()
			net, height := chain.chainParams.Net, int32(1)
			if mutation == "network" {
				net++
			}
			if mutation == "height" {
				height++
			}
			if mutation == "wrong-key" {
				key, _ = btcec.PrivKeyFromBytes(bytes.Repeat([]byte{2}, 32))
			}
			sig, _ := signPOS(key)(scommon.POSApprovalMessage(net, height, block.BlockHash()))
			block.Transactions[0].TxIn[0].Witness = append(block.Transactions[0].TxIn[0].Witness, sig)
			if mutation == "header" {
				block.Header.Nonce++
			}
			if mutation == "missing-commitment" {
				block.Transactions[0].TxOut = block.Transactions[0].TxOut[:1]
			}
			if mutation == "reward" {
				block.Transactions[0].TxOut[0].PkScript = []byte{txscript.OP_TRUE}
			}
			if _, _, err := chain.ProcessBlock(btcutil.NewBlock(block), BFFastAdd); err == nil {
				t.Fatal("mutated approval accepted")
			}
		})
	}
}

func TestPOSConcurrentApprovalAndACKRetry(t *testing.T) {
	chain, _, key, pub, candidate, teardown := setupPOSChain(t)
	defer teardown()
	other := candidate.Copy()
	other.Header.Nonce++
	var wg sync.WaitGroup
	var accepted, signed atomic.Int32
	for _, block := range []*wire.MsgBlock{candidate, other} {
		wg.Add(1)
		go func(block *wire.MsgBlock) {
			defer wg.Done()
			_, err := chain.ApprovePOSBlock(block, pub, func(msg []byte) ([]byte, error) { signed.Add(1); return signPOS(key)(msg) })
			if err == nil {
				accepted.Add(1)
			}
		}(block)
	}
	wg.Wait()
	if accepted.Load() != 1 || signed.Load() != 1 {
		t.Fatalf("accepted=%d signed=%d", accepted.Load(), signed.Load())
	}
	best := chain.BestSnapshot()
	original := candidate
	if other.BlockHash() == best.Hash {
		original = other
	}
	stored, err := chain.ApprovePOSBlock(original, pub, func([]byte) ([]byte, error) { t.Fatal("retry signed again"); return nil, nil })
	if err != nil || *stored.Hash() != best.Hash || len(stored.MsgBlock().Transactions[0].TxIn[0].Witness) != 2 {
		t.Fatalf("retry failed: %v", err)
	}
}

type posSyncDB struct {
	database.DB
	err   error
	calls atomic.Int32
}

func (d *posSyncDB) Sync() error {
	d.calls.Add(1)
	if d.err != nil {
		return d.err
	}
	return database.Sync(d.DB)
}

func TestPOSSyncFailureDoesNotPublishOrSignAgain(t *testing.T) {
	chain, _, key, pub, block, teardown := setupPOSChain(t)
	defer teardown()
	db := &posSyncDB{DB: chain.db, err: fmt.Errorf("injected sync failure")}
	chain.db = db
	var notifications atomic.Int32
	chain.Subscribe(func(*Notification) { notifications.Add(1) })
	approved, err := chain.ApprovePOSBlock(block, pub, signPOS(key))
	if err == nil || approved != nil || db.calls.Load() == 0 {
		t.Fatalf("failed sync returned approval: %v", err)
	}
	hash := block.BlockHash()
	if notifications.Load() != 0 || chain.CanServeBlock(&hash) || chain.POSReady() {
		t.Fatal("failed commit published")
	}
	if _, err := chain.ApprovePOSBlock(block, pub, func([]byte) ([]byte, error) { t.Fatal("signed after storage failure"); return nil, nil }); err == nil {
		t.Fatal("failure did not stop approvals")
	}
}

type posWriteFailureDB struct{ database.DB }

func (d *posWriteFailureDB) Update(func(database.Tx) error) error {
	return fmt.Errorf("injected write failure")
}
func TestPOSWriteFailureStopsApproval(t *testing.T) {
	chain, _, key, pub, block, teardown := setupPOSChain(t)
	defer teardown()
	chain.db = &posWriteFailureDB{DB: chain.db}
	if approved, err := chain.ApprovePOSBlock(block, pub, signPOS(key)); err == nil || approved != nil {
		t.Fatal("failed write returned approval")
	}
	if chain.POSReady() {
		t.Fatal("failed write left producer ready")
	}
	if _, err := chain.ApprovePOSBlock(block, pub, func([]byte) ([]byte, error) { t.Fatal("signed after failed write"); return nil, nil }); err == nil {
		t.Fatal("failed write did not stop approval")
	}
}

func TestPOSUnknownParentIsNotCachedAsOrphan(t *testing.T) {
	chain, _, _, _, block, teardown := setupPOSChain(t)
	defer teardown()
	block.Header.PrevBlock[0] ^= 1
	hash := block.BlockHash()
	_, orphan, err := chain.ProcessBlock(btcutil.NewBlock(block), BFNone)
	rule, ok := err.(RuleError)
	if orphan || !ok || rule.ErrorCode != ErrPreviousBlockUnknown {
		t.Fatalf("unknown parent result: orphan=%v err=%v", orphan, err)
	}
	if _, exists := chain.orphans[hash]; exists || rawBlockStored(t, chain, &hash) {
		t.Fatal("unverified orphan variant persisted")
	}
}

// Rebuild both commitments after mutating transactions in a proposal.
func updatePOSCommitments(block *wire.MsgBlock) {
	root := CalcMerkleRoot(btcutil.NewBlock(block).Transactions(), true)
	var preimage [64]byte
	copy(preimage[:32], root[:])
	copy(preimage[32:], block.Transactions[0].TxIn[0].Witness[0])
	for _, out := range block.Transactions[0].TxOut {
		if bytes.HasPrefix(out.PkScript, WitnessMagicBytes) {
			out.PkScript = append(append([]byte(nil), WitnessMagicBytes...), chainhash.DoubleHashB(preimage[:])...)
		}
	}
	block.Header.MerkleRoot = CalcMerkleRoot(btcutil.NewBlock(block).Transactions(), false)
}

func TestPOSSyncFailureShutdownAndReopenWithSpend(t *testing.T) {
	chain, _, key, pub, block, teardown := setupPOSChain(t)
	defer teardown()
	input := wire.OutPoint{Hash: chainhash.Hash{42}, Index: 0}
	require.NoError(t, chain.db.Update(func(tx database.Tx) error {
		return dbPutUtxoEntry(tx.Metadata().Bucket(utxoSetBucketName), input,
			&UtxoEntry{amount: 100, pkScript: []byte{txscript.OP_TRUE}, blockHeight: 0})
	}))
	spend := wire.NewMsgTx(2)
	spend.AddTxIn(wire.NewTxIn(&input, nil, nil))
	spend.AddTxOut(wire.NewTxOut(90, nil, []byte{txscript.OP_TRUE}))
	block.Transactions = append(block.Transactions, spend)
	block.Transactions[0].TxOut[0].Value = 10
	updatePOSCommitments(block)
	db := &posSyncDB{DB: chain.db, err: fmt.Errorf("one-time sync failure")}
	chain.db = db
	_, err := chain.ApprovePOSBlock(block, pub, signPOS(key))
	require.ErrorContains(t, err, "one-time sync failure")
	require.Equal(t, int32(0), chain.BestSnapshot().Height)
	// Storage recovers, but shutdown must not write the stale in-memory tip.
	db.err = nil
	for _, mode := range []FlushMode{FlushRequired, FlushPeriodic, FlushIfNeeded} {
		require.Error(t, chain.FlushUtxoCache(mode))
	}
	require.NoError(t, db.Close())
	reopened, err := database.Open(testDbType, filepath.Join(testDbRoot, t.Name()), blockDataNet)
	require.NoError(t, err)
	defer reopened.Close()
	// The fork's regression genesis serializes differently from upstream's
	// hard-coded hash. Reopen against the actual genesis installed by chainSetup.
	params := *chain.chainParams
	genesisHash := params.GenesisBlock.BlockHash()
	params.GenesisHash = &genesisHash
	restored, err := New(&Config{DB: reopened, ChainParams: &params, TimeSource: NewMedianTime()})
	require.NoError(t, err, "committed UTXO state must not be replayed twice")
	require.Equal(t, int32(1), restored.BestSnapshot().Height)
	require.Equal(t, block.BlockHash(), restored.BestSnapshot().Hash)
	require.NoError(t, reopened.View(func(tx database.Tx) error {
		entry, err := dbFetchUtxoEntry(tx, tx.Metadata().Bucket(utxoSetBucketName), input)
		require.NoError(t, err)
		require.Nil(t, entry, "spent input must stay deleted")
		entry, err = dbFetchUtxoEntry(tx, tx.Metadata().Bucket(utxoSetBucketName), wire.OutPoint{Hash: spend.TxHash(), Index: 0})
		require.NoError(t, err)
		require.NotNil(t, entry)
		require.Equal(t, int64(90), entry.Amount())
		return nil
	}))
}

func setupPOSCoreSubstitute(t *testing.T) (*BlockChain, *posReadiness, *btcec.PrivateKey, string, *wire.MsgBlock, func()) {
	t.Helper()
	chain, ready, key, pub, candidate, teardown := setupPOSChain(t)
	coreKey, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{2}, 32))
	minerKey, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{3}, 32))
	corePub := hex.EncodeToString(coreKey.PubKey().SerializeCompressed())
	minerPub := hex.EncodeToString(minerKey.PubKey().SerializeCompressed())
	core := scommon.NewCoreNodeInfo(nil)
	core.ServerNode = pub
	core.ChildMiners[minerPub] = &scommon.MinerAscendInfo{}
	seq := scommon.NewMiningSequenceMgr(chain.chainParams)
	require.NoError(t, seq.Init(map[string]*scommon.CoreNodeInfo{pub: scommon.NewCoreNodeInfo(nil), corePub: core}, 0, ""))
	ready.seq = seq
	configure := func(block *wire.MsgBlock, height int, producer *btcec.PrivateKey) {
		sig := ecdsa.Sign(producer, chainhash.HashB(scommon.GetScriptSignData(height, 0))).Serialize()
		script, err := txscript.NewScriptBuilder().AddInt64(int64(height)).AddInt64(0).AddData(sig).Script()
		require.NoError(t, err)
		coinbase := block.Transactions[0]
		coinbase.TxIn[0].SignatureScript = script
		coinbase.TxIn[0].Witness = wire.TxWitness{make([]byte, 32)}
		reward, err := seq.POSReward(height, hex.EncodeToString(producer.PubKey().SerializeCompressed()))
		require.NoError(t, err)
		address, err := btcutil.DecodeAddress(reward, chain.chainParams)
		require.NoError(t, err)
		coinbase.TxOut[0].PkScript, err = txscript.PayToAddrScript(address)
		require.NoError(t, err)
		if len(coinbase.TxOut) == 1 {
			coinbase.AddTxOut(wire.NewTxOut(0, nil, append(append([]byte(nil), WitnessMagicBytes...), make([]byte, 32)...)))
		}
		updatePOSCommitments(block)
	}
	for height := 1; height <= 2; height++ {
		producer := key
		if height == 2 {
			producer = coreKey
		}
		configure(candidate, height, producer)
		approved, err := chain.ApprovePOSBlock(candidate, pub, signPOS(key))
		require.NoError(t, err)
		ready.setTip(height, *approved.Hash())
		require.NoError(t, seq.MoveMiningAddr(height, seq.GetCurrentMiningAddr()))
		next, _, err := newBlock(chain, approved, nil)
		require.NoError(t, err)
		candidate = next.MsgBlock()
	}
	configure(candidate, 3, coreKey)
	producer, _, reward, err := seq.POSMiningInfo(candidate.Transactions[0], 3)
	require.NoError(t, err)
	require.Equal(t, corePub, producer)
	require.Equal(t, seq.GetCurrentMiningInfo().MiningAddress, reward, "Core substitute must pay the Miner-Core channel")
	return chain, ready, key, pub, candidate, teardown
}

func TestPOSRejectsDivertedCoinbaseRewards(t *testing.T) {
	for _, kind := range []string{"btc", "asset", "asset-only"} {
		for _, correctValue := range []int64{0, 1} {
			t.Run(fmt.Sprintf("%s/correct-output-%d", kind, correctValue), func(t *testing.T) {
				chain, _, key, pub, block, teardown := setupPOSCoreSubstitute(t)
				defer teardown()
				// A real input funds the fee: this candidate would otherwise pass the
				// full Bootstrap approval entry, including amount and asset accounting.
				input := wire.OutPoint{Hash: chainhash.Hash{43}, Index: 0}
				var fees wire.TxAssets
				if kind != "btc" {
					fees = wire.TxAssets{{Name: *indexercommon.NewAssetNameFromString("brc20:f:fee"), Amount: *indexercommon.NewDefaultDecimal(10)}}
				}
				require.NoError(t, chain.db.Update(func(tx database.Tx) error {
					return dbPutUtxoEntry(tx.Metadata().Bucket(utxoSetBucketName), input,
						&UtxoEntry{amount: 100, txAssets: fees.Clone(), pkScript: []byte{txscript.OP_TRUE}})
				}))
				spend := wire.NewMsgTx(2)
				spend.AddTxIn(wire.NewTxIn(&input, nil, nil))
				spend.AddTxOut(wire.NewTxOut(90, nil, []byte{txscript.OP_TRUE}))
				block.Transactions = append(block.Transactions, spend)
				block.Transactions[0].TxOut[0].Value = correctValue
				other, err := btcutil.NewAddressWitnessPubKeyHash(bytes.Repeat([]byte{9}, 20), chain.chainParams)
				require.NoError(t, err)
				script, err := txscript.PayToAddrScript(other)
				require.NoError(t, err)
				divertedValue := 10 - correctValue
				if kind == "asset-only" {
					divertedValue = 0
				}
				block.Transactions[0].AddTxOut(wire.NewTxOut(divertedValue, fees, script))
				updatePOSCommitments(block)
				candidate := btcutil.NewBlock(block)
				candidate.SetHeight(3)
				require.ErrorContains(t, chain.checkPOSBlockLocked(candidate, true), "reward output")
				signed := false
				_, err = chain.ApprovePOSBlock(block, pub, func(msg []byte) ([]byte, error) {
					signed = true
					return signPOS(key)(msg)
				})
				require.ErrorContains(t, err, "reward output")
				require.False(t, signed, "invalid reward must be rejected before signing")
				// Identical fee-bearing outputs to the correct channel are accepted.
				block.Transactions[0].TxOut[len(block.Transactions[0].TxOut)-1].PkScript = block.Transactions[0].TxOut[0].PkScript
				updatePOSCommitments(block)
				_, err = chain.ApprovePOSBlock(block, pub, signPOS(key))
				require.NoError(t, err)
			})
		}
	}
}

// This failure happens inside the canonical transaction, after raw block and
// block-index writes succeeded, and after the UTXO flush staged its changes.
type posFailOnceConnectIndex struct {
	attempts int
}

func (i *posFailOnceConnectIndex) Init(*BlockChain, <-chan struct{}) error { return nil }
func (i *posFailOnceConnectIndex) DisconnectBlock(database.Tx, *btcutil.Block, []SpentTxOut) error {
	return nil
}
func (i *posFailOnceConnectIndex) ConnectBlock(tx database.Tx, block *btcutil.Block, _ []SpentTxOut) error {
	i.attempts++
	if err := tx.Metadata().Put([]byte("pos-retry-index-marker"), block.Hash()[:]); err != nil {
		return err
	}
	if i.attempts == 1 {
		return fmt.Errorf("one-time canonical index write failure")
	}
	return nil
}

func TestPOSStoredCandidateRetriesAfterCanonicalTransactionFailure(t *testing.T) {
	testPOSStoredCandidateRetry(t, false)
}

func TestPOSReceivedBlockRetriesAfterCanonicalTransactionFailure(t *testing.T) {
	testPOSStoredCandidateRetry(t, true)
}

func testPOSStoredCandidateRetry(t *testing.T, receive bool) {
	chain, ready, key, pub, candidate, teardown := setupPOSChain(t)
	defer teardown()
	if receive {
		// Match the deployed zero-work chain rather than regtest's PoW bits.
		chain.chainParams.PowLimitBits = 0
		candidate.Header.Bits = 0
	}
	input := wire.OutPoint{Hash: chainhash.Hash{44}, Index: 0}
	output := &UtxoEntry{amount: 100, pkScript: []byte{txscript.OP_TRUE}}
	require.NoError(t, chain.db.Update(func(tx database.Tx) error {
		return dbPutUtxoEntry(tx.Metadata().Bucket(utxoSetBucketName), input, output)
	}))
	spend := wire.NewMsgTx(2)
	spend.AddTxIn(wire.NewTxIn(&input, nil, nil))
	spend.AddTxOut(wire.NewTxOut(90, nil, []byte{txscript.OP_TRUE}))
	candidate.Transactions = append(candidate.Transactions, spend)
	candidate.Transactions[0].TxOut[0].Value = 10
	updatePOSCommitments(candidate)
	hash := candidate.BlockHash()
	parent := chain.BestSnapshot().Hash
	index := &posFailOnceConnectIndex{}
	chain.indexManager = index
	var signs, notifications int
	chain.Subscribe(func(*Notification) { notifications++ })
	var approved *btcutil.Block
	var err error
	if receive {
		// Only ordinary reception is exercised: the Bootstrap signature came
		// from the producer's network peer, never from ApprovePOSBlock here.
		sig, signErr := signPOS(key)(scommon.POSApprovalMessage(chain.chainParams.Net, 1, hash))
		require.NoError(t, signErr)
		signs++
		candidate.Transactions[0].TxIn[0].Witness = append(candidate.Transactions[0].TxIn[0].Witness, sig)
		_, _, err = chain.ProcessBlock(btcutil.NewBlock(candidate.Copy()), BFNone)
	} else {
		approved, err = chain.ApprovePOSBlock(candidate, pub, func(message []byte) ([]byte, error) {
			signs++
			return signPOS(key)(message)
		})
	}
	require.ErrorContains(t, err, "one-time canonical index write failure")
	require.Nil(t, approved)
	require.Equal(t, 1, index.attempts)
	require.Equal(t, 1, signs)
	require.Zero(t, notifications, "failed commit must not publish")
	require.Equal(t, parent, chain.BestSnapshot().Hash)
	require.True(t, rawBlockStored(t, chain, &hash))
	require.NotNil(t, chain.index.LookupNode(&hash))
	require.False(t, chain.CanServeBlock(&hash))
	require.Error(t, chain.FlushUtxoCache(FlushRequired))
	checkUTXO := func(tx database.Tx, outpoint wire.OutPoint, amount *int64) {
		entry, err := dbFetchUtxoEntry(tx, tx.Metadata().Bucket(utxoSetBucketName), outpoint)
		require.NoError(t, err)
		if amount == nil {
			require.Nil(t, entry)
			return
		}
		require.NotNil(t, entry)
		require.Equal(t, *amount, entry.Amount())
	}
	inputAmount, childAmount := int64(100), int64(90)
	child := wire.OutPoint{Hash: spend.TxHash(), Index: 0}
	require.NoError(t, chain.db.View(func(tx database.Tx) error {
		state, err := deserializeBestChainState(tx.Metadata().Get(chainStateKeyName))
		require.NoError(t, err)
		require.Equal(t, parent, state.hash)
		require.Nil(t, tx.Metadata().Get([]byte("pos-retry-index-marker")), "canonical transaction must roll back")
		require.Equal(t, parent[:], dbFetchUtxoStateConsistency(tx))
		checkUTXO(tx, input, &inputAmount)
		checkUTXO(tx, child, nil)
		return nil
	}))
	var saved *btcutil.Block
	require.NoError(t, chain.db.View(func(tx database.Tx) error {
		var err error
		saved, err = dbFetchBlockByNode(tx, chain.index.LookupNode(&hash))
		return err
	}))
	savedBytes, err := saved.Bytes()
	require.NoError(t, err)
	require.Len(t, saved.MsgBlock().Transactions[0].TxIn[0].Witness, 2)
	require.NoError(t, chain.db.Close())
	reopened, err := database.Open(testDbType, filepath.Join(testDbRoot, t.Name()), blockDataNet)
	require.NoError(t, err)
	defer reopened.Close()
	params := *chain.chainParams
	genesisHash := params.GenesisBlock.BlockHash()
	params.GenesisHash = &genesisHash
	restored, err := New(&Config{DB: reopened, ChainParams: &params, TimeSource: NewMedianTime(), IndexManager: index})
	require.NoError(t, err)
	restored.assetIndexReadiness = ready
	require.Equal(t, parent, restored.BestSnapshot().Hash)
	storedNode := restored.index.LookupNode(&hash)
	require.NotNil(t, storedNode)
	require.True(t, restored.index.NodeStatus(storedNode).HaveData())
	require.False(t, restored.bestChain.Contains(storedNode))
	// An unsigned retry must finish the stored approval without signing again.
	var accepted, connected int
	restored.Subscribe(func(n *Notification) {
		if n.Type == NTBlockAccepted {
			accepted++
		}
		if n.Type == NTBlockConnected {
			connected++
		}
	})
	if receive {
		known, err := restored.HaveBlock(&hash)
		require.NoError(t, err)
		require.False(t, known, "normal inventory must request the incomplete canonical block again")
		var main, orphan bool
		approved = btcutil.NewBlock(candidate.Copy())
		main, orphan, err = restored.ProcessBlock(approved, BFFastAdd)
		require.True(t, main)
		require.False(t, orphan)
	} else {
		approved, err = restored.ApprovePOSBlock(candidate, pub, func([]byte) ([]byte, error) {
			t.Error("stored approval was signed again")
			return signPOS(key)(scommon.POSApprovalMessage(params.Net, 1, hash))
		})
	}
	require.NoError(t, err)
	require.NotNil(t, approved)
	require.Equal(t, hash, restored.BestSnapshot().Hash)
	require.Equal(t, int32(1), restored.BestSnapshot().Height)
	require.Same(t, storedNode, restored.index.LookupNode(&hash), "recovery must preserve existing node ancestry")
	require.Equal(t, 2, index.attempts)
	require.Equal(t, 1, accepted)
	require.Equal(t, 1, connected)
	require.True(t, restored.CanServeBlock(&hash))
	require.NoError(t, reopened.View(func(tx database.Tx) error {
		checkUTXO(tx, input, nil)
		checkUTXO(tx, child, &childAmount)
		require.Equal(t, hash[:], tx.Metadata().Get([]byte("pos-retry-index-marker")))
		require.Equal(t, hash[:], dbFetchUtxoStateConsistency(tx))
		return nil
	}))
	bytesAfter, err := approved.Bytes()
	require.NoError(t, err)
	require.Equal(t, savedBytes, bytesAfter, "recover original approved bytes")
	// Once canonical, another retry does not connect, spend or notify twice.
	if receive {
		_, _, err = restored.ProcessBlock(btcutil.NewBlock(candidate.Copy()), BFNone)
		require.Error(t, err, "ordinary canonical duplicates retain existing policy")
	} else {
		_, err = restored.ApprovePOSBlock(candidate, pub, func([]byte) ([]byte, error) { t.Fatal("canonical retry signed again"); return nil, nil })
		require.NoError(t, err)
	}
	require.Equal(t, 2, index.attempts)
	require.Equal(t, 1, accepted)
	require.Equal(t, 1, connected)
	finalHash := hash
	if receive {
		for height := 2; height <= 3; height++ {
			require.NoError(t, ready.seq.MoveMiningAddr(height-1, ready.seq.GetCurrentMiningAddr()))
			ready.setTip(height-1, restored.BestSnapshot().Hash)
			next := candidate.Copy()
			next.Header.PrevBlock = restored.BestSnapshot().Hash
			next.Header.Timestamp = restored.BestSnapshot().MedianTime.Add(time.Second)
			next.Header.Bits = 0
			next.Transactions = next.Transactions[:1]
			next.Transactions[0].TxOut[0].Value = 0
			producerSig := ecdsa.Sign(key, chainhash.HashB(scommon.GetScriptSignData(height, 0))).Serialize()
			next.Transactions[0].TxIn[0].SignatureScript, err = txscript.NewScriptBuilder().AddInt64(int64(height)).AddInt64(0).AddData(producerSig).Script()
			require.NoError(t, err)
			next.Transactions[0].TxIn[0].Witness = wire.TxWitness{make([]byte, 32)}
			updatePOSCommitments(next)
			finalHash = next.BlockHash()
			sig, err := signPOS(key)(scommon.POSApprovalMessage(params.Net, int32(height), finalHash))
			require.NoError(t, err)
			next.Transactions[0].TxIn[0].Witness = append(next.Transactions[0].TxIn[0].Witness, sig)
			main, orphan, err := restored.ProcessBlock(btcutil.NewBlock(next), BFNone)
			require.NoError(t, err)
			require.True(t, main)
			require.False(t, orphan)
		}
		require.Equal(t, int32(3), restored.BestSnapshot().Height)
		require.Equal(t, 3, accepted)
		require.Equal(t, 3, connected)
	}
	require.NoError(t, reopened.Close())
	again, err := database.Open(testDbType, filepath.Join(testDbRoot, t.Name()), blockDataNet)
	require.NoError(t, err)
	defer again.Close()
	final, err := New(&Config{DB: again, ChainParams: &params, TimeSource: NewMedianTime()})
	require.NoError(t, err)
	require.Equal(t, finalHash, final.BestSnapshot().Hash)
	require.NoError(t, again.View(func(tx database.Tx) error { checkUTXO(tx, input, nil); checkUTXO(tx, child, &childAmount); return nil }))
}

func TestPOSStoredCandidateRecoveryRejectsInvalidOrUndurableBlock(t *testing.T) {
	for _, failure := range []string{"unsigned", "bad-approval", "bad-spend", "known-invalid", "sync"} {
		t.Run(failure, func(t *testing.T) {
			chain, _, key, pub, candidate, teardown := setupPOSChain(t)
			defer teardown()
			if failure == "bad-spend" {
				input := wire.OutPoint{Hash: chainhash.Hash{45}, Index: 0}
				require.NoError(t, chain.db.Update(func(tx database.Tx) error {
					return dbPutUtxoEntry(tx.Metadata().Bucket(utxoSetBucketName), input,
						&UtxoEntry{amount: 100, pkScript: []byte{txscript.OP_FALSE}})
				}))
				spend := wire.NewMsgTx(2)
				spend.AddTxIn(wire.NewTxIn(&input, nil, nil))
				spend.AddTxOut(wire.NewTxOut(100, nil, []byte{txscript.OP_TRUE}))
				candidate.Transactions = append(candidate.Transactions, spend)
				updatePOSCommitments(candidate)
			}
			hash := candidate.BlockHash()
			stored := candidate.Copy()
			if failure != "unsigned" {
				sig, err := signPOS(key)(scommon.POSApprovalMessage(chain.chainParams.Net, 1, hash))
				require.NoError(t, err)
				if failure == "bad-approval" {
					sig = []byte{1}
				}
				stored.Transactions[0].TxIn[0].Witness = append(stored.Transactions[0].TxIn[0].Witness, sig)
			}
			parent := chain.bestChain.Tip()
			node := newBlockNode(&stored.Header, parent)
			// Persisted KnownValid must never bypass recovery's full validation.
			node.status = statusDataStored | statusValid
			if failure == "known-invalid" {
				node.status |= statusValidateFailed
			}
			require.NoError(t, chain.db.Update(func(tx database.Tx) error {
				return dbStoreBlock(tx, btcutil.NewBlock(stored))
			}))
			chain.index.AddNode(node)
			require.NoError(t, chain.index.flushToDB())
			if failure == "sync" {
				chain.db = &posSyncDB{DB: chain.db, err: fmt.Errorf("recovery sync failure")}
			}
			var notifications int
			chain.Subscribe(func(*Notification) { notifications++ })
			approved, err := chain.ApprovePOSBlock(candidate, pub, func([]byte) ([]byte, error) {
				t.Fatal("stored candidate must not be signed again")
				return nil, nil
			})
			require.Error(t, err)
			require.Nil(t, approved)
			switch failure {
			case "unsigned":
				require.ErrorContains(t, err, "expected 2")
			case "bad-approval":
				require.ErrorContains(t, err, "canonical DER")
			case "bad-spend":
				var ruleErr RuleError
				require.ErrorAs(t, err, &ruleErr)
				require.Equal(t, ErrScriptValidation, ruleErr.ErrorCode)
			case "known-invalid":
				require.ErrorContains(t, err, "not recoverable")
			case "sync":
				require.ErrorContains(t, err, "recovery sync failure")
				require.False(t, chain.POSReady())
				require.Error(t, chain.FlushUtxoCache(FlushRequired))
			}
			require.Zero(t, notifications)
			require.Equal(t, parent.hash, chain.BestSnapshot().Hash)
			require.False(t, chain.CanServeBlock(&hash))
			require.Same(t, node, chain.index.LookupNode(&hash))
		})
	}
}

func TestPOSApprovalAfterMembershipJoinSnapshot(t *testing.T) {
	chain, ready, bootstrapKey, bootstrapPub, first, teardown := setupPOSChain(t)
	defer teardown()
	newKey, _ := btcec.PrivKeyFromBytes([]byte{2})
	oldKey, _ := btcec.PrivKeyFromBytes([]byte{3})
	newPub := hex.EncodeToString(newKey.PubKey().SerializeCompressed())
	oldPub := hex.EncodeToString(oldKey.PubKey().SerializeCompressed())
	require.Less(t, newPub, oldPub)
	_, err := ready.seq.AddNode(oldPub, bootstrapPub, 0)
	require.NoError(t, err)
	_, err = chain.ApprovePOSBlock(first, bootstrapPub, signPOS(bootstrapKey))
	require.NoError(t, err)
	_, err = ready.seq.AddNode(newPub, bootstrapPub, 1)
	require.NoError(t, err)
	cursor := ready.seq.GetCurrentMiningAddr()
	require.NoError(t, ready.seq.MoveMiningAddr(1, cursor))
	require.Equal(t, oldPub, ready.seq.GetCurrentMiningInfo().PubKey)
	root := scommon.NewCoreNodeInfo(nil)
	oldCore, newCore := scommon.NewCoreNodeInfo(nil), scommon.NewCoreNodeInfo(nil)
	oldCore.ServerNode = bootstrapPub
	newCore.ServerNode, newCore.AscendHeight = bootstrapPub, 1
	restored := scommon.NewMiningSequenceMgr(chain.chainParams)
	require.NoError(t, restored.Init(map[string]*scommon.CoreNodeInfo{bootstrapPub: root, oldPub: oldCore, newPub: newCore}, 1, cursor))
	next := first.Copy()
	next.Header.PrevBlock = chain.BestSnapshot().Hash
	next.Header.Timestamp = chain.BestSnapshot().MedianTime.Add(time.Second)
	producerSig := ecdsa.Sign(oldKey, chainhash.HashB(scommon.GetScriptSignData(2, 0))).Serialize()
	next.Transactions[0].TxIn[0].SignatureScript, err = txscript.NewScriptBuilder().AddInt64(2).AddInt64(0).AddData(producerSig).Script()
	require.NoError(t, err)
	next.Transactions[0].TxIn[0].Witness = wire.TxWitness{make([]byte, 32)}
	reward, err := ready.seq.POSReward(2, oldPub)
	require.NoError(t, err)
	addr, err := btcutil.DecodeAddress(reward, chain.chainParams)
	require.NoError(t, err)
	next.Transactions[0].TxOut[0].PkScript, err = txscript.PayToAddrScript(addr)
	require.NoError(t, err)
	updatePOSCommitments(next)
	ready.setTip(1, chain.BestSnapshot().Hash)
	require.NoError(t, chain.CheckConnectBlockTemplate(btcutil.NewBlock(next.Copy())), "continuous sorter approves the old Core's next slot")
	ready.seq = restored
	approved, err := chain.ApprovePOSBlock(next, bootstrapPub, signPOS(bootstrapKey))
	require.NoError(t, err, "snapshot sorter must formally approve the same candidate")
	require.Equal(t, *approved.Hash(), chain.BestSnapshot().Hash)
	require.Equal(t, int32(2), chain.BestSnapshot().Height)
	require.Equal(t, next.Transactions[0].TxOut[0].PkScript, approved.MsgBlock().Transactions[0].TxOut[0].PkScript)
}

func TestPOSActivatedTipRejectsLegacyHigherWorkFork(t *testing.T) {
	chain, ready, key, pub, first, teardown := setupPOSChain(t, &chaincfg.TestNetParams)
	defer teardown()
	chain.chainParams.POSV2Height = 2
	first.Header.Bits = 0
	first.Header.Timestamp = chain.chainParams.GenesisBlock.Header.Timestamp.Add(time.Second)
	main, orphan, err := chain.ProcessBlock(btcutil.NewBlock(first.Copy()), BFFastAdd)
	require.NoError(t, err)
	require.True(t, main)
	require.False(t, orphan)
	ready.setTip(1, first.BlockHash())
	require.NoError(t, ready.seq.MoveMiningAddr(1, ready.seq.GetCurrentMiningAddr()))
	second := first.Copy()
	second.Header.PrevBlock = first.BlockHash()
	second.Header.Timestamp = first.Header.Timestamp.Add(time.Second)
	sig := ecdsa.Sign(key, chainhash.HashB(scommon.GetScriptSignData(2, 0))).Serialize()
	second.Transactions[0].TxIn[0].SignatureScript, err = txscript.NewScriptBuilder().AddInt64(2).AddInt64(0).AddData(sig).Script()
	require.NoError(t, err)
	second.Header.MerkleRoot = CalcMerkleRoot(btcutil.NewBlock(second).Transactions(), false)
	approved, err := chain.ApprovePOSBlock(second, pub, signPOS(key))
	require.NoError(t, err)
	fork := first.Copy()
	fork.Header.Bits = chain.chainParams.PowLimitBits
	fork.Header.Timestamp = chain.chainParams.GenesisBlock.Header.Timestamp.Add(1201 * time.Second)
	ready.setTip(0, *chain.chainParams.GenesisHash)
	for nonce := uint32(1); nonce <= 5; nonce++ {
		variant := fork.Copy()
		variant.Header.Nonce = nonce
		main, orphan, err = chain.ProcessBlock(btcutil.NewBlock(variant), BFFastAdd)
		require.Error(t, err, "impossible competing branches must be rejected before storage")
		require.False(t, orphan)
		require.False(t, main)
		hash := variant.BlockHash()
		require.Nil(t, chain.index.LookupNode(&hash))
		require.NoError(t, chain.db.View(func(tx database.Tx) error {
			stored, err := tx.HasBlock(&hash)
			require.NoError(t, err)
			require.False(t, stored)
			return nil
		}))
	}
	require.Equal(t, *approved.Hash(), chain.BestSnapshot().Hash)
	require.Equal(t, int32(2), chain.BestSnapshot().Height)
}

func TestFailedSameHashProposalInvalidatesPreparedState(t *testing.T) {
	chain, _, key, _, candidate, teardown := setupPOSChain(t)
	defer teardown()
	require.NoError(t, chain.CheckConnectBlockTemplate(btcutil.NewBlock(candidate.Copy())))
	hash := candidate.BlockHash()
	bad := candidate.Copy()
	bad.Transactions[0].TxIn[0].Witness = append(bad.Transactions[0].TxIn[0].Witness, []byte{1})
	require.Equal(t, hash, bad.BlockHash())
	require.Error(t, chain.CheckConnectBlockTemplate(btcutil.NewBlock(bad)))
	require.False(t, chain.takePreparedBlock(hash, candidate.Header.PrevBlock), "released state must never retain prepared eligibility")
	valid := candidate.Copy()
	sig, _ := signPOS(key)(scommon.POSApprovalMessage(chain.chainParams.Net, 1, hash))
	valid.Transactions[0].TxIn[0].Witness = append(valid.Transactions[0].TxIn[0].Witness, sig)
	main, orphan, err := chain.ProcessBlock(btcutil.NewBlock(valid), BFFastAdd)
	require.NoError(t, err)
	require.True(t, main)
	require.False(t, orphan)
}
