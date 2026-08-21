package blockchain

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/database"
	sindexercommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

type testAssetReadiness struct {
	mu        sync.Mutex
	height    int
	hash      chainhash.Hash
	valid     bool
	changed   chan struct{}
	interrupt <-chan struct{}
	waits     atomic.Int32
}

func newTestAssetReadiness(height int, hash chainhash.Hash, interrupt <-chan struct{}) *testAssetReadiness {
	return &testAssetReadiness{height: height, hash: hash, valid: true, changed: make(chan struct{}), interrupt: interrupt}
}

func (r *testAssetReadiness) GetInternalTip() (int, chainhash.Hash, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.height, r.hash, r.valid
}

func (r *testAssetReadiness) InternalTipReady(height int, hash *chainhash.Hash) bool {
	h, current, ok := r.GetInternalTip()
	return ok && hash != nil && h == height && current == *hash
}

func (r *testAssetReadiness) EnsureInternalTip(height int, hash *chainhash.Hash, _ int) error {
	r.mu.Lock()
	interrupt := r.interrupt
	r.mu.Unlock()
	if interrupt == nil {
		interrupt = make(chan struct{})
	}
	return r.WaitForInternalTip(height, hash, interrupt)
}

func (r *testAssetReadiness) WaitForInternalTip(height int, hash *chainhash.Hash,
	interrupt <-chan struct{}) error {

	r.waits.Add(1)
	for !r.InternalTipReady(height, hash) {
		r.mu.Lock()
		changed := r.changed
		r.mu.Unlock()
		select {
		case <-changed:
		case <-interrupt:
			return AssetIndexerNotReadyError{TargetHeight: height, TargetHash: *hash}
		}
	}
	return nil
}

func (r *testAssetReadiness) setTip(height int, hash chainhash.Hash) {
	r.mu.Lock()
	r.height = height
	r.hash = hash
	r.valid = true
	close(r.changed)
	r.changed = make(chan struct{})
	r.mu.Unlock()
}

type countingContractValidator struct {
	calls atomic.Int32
	err   error
}

func (v *countingContractValidator) ValidateContractBlock(*btcutil.Block, *UtxoViewpoint) error {
	v.calls.Add(1)
	return v.err
}

func readinessTestBlock(t *testing.T, chain *BlockChain) *btcutil.Block {
	t.Helper()
	genesis := btcutil.NewBlock(chain.chainParams.GenesisBlock)
	genesis.SetHeight(0)
	block, _, err := newBlock(chain, genesis, nil)
	if err != nil {
		t.Fatal(err)
	}
	return block
}

func setupReadinessChain(t *testing.T) (*BlockChain, *testAssetReadiness,
	*countingContractValidator, func()) {

	t.Helper()
	chain, teardown, err := chainSetup(t.Name(), &chaincfg.RegressionNetParams)
	if err != nil {
		t.Fatal(err)
	}
	ready := newTestAssetReadiness(0, chain.bestChain.Tip().hash, chain.interrupt)
	validator := &countingContractValidator{}
	chain.assetIndexReadiness = ready
	chain.contractBlockValidator = validator
	return chain, ready, validator, teardown
}

func rawBlockStored(t *testing.T, chain *BlockChain, hash *chainhash.Hash) bool {
	t.Helper()
	var stored bool
	if err := chain.db.View(func(tx database.Tx) error {
		var err error
		stored, err = tx.HasBlock(hash)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestContractBlockWaitsForExactAssetTipBeforeStorage(t *testing.T) {
	for _, test := range []struct {
		name   string
		height int
		hash   chainhash.Hash
	}{
		{name: "height behind", height: -1, hash: chainhash.Hash{}},
		{name: "same height wrong hash", height: 0, hash: chainhash.Hash{1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			chain, ready, validator, teardown := setupReadinessChain(t)
			defer teardown()
			block := readinessTestBlock(t, chain)
			ready.setTip(test.height, test.hash)

			done := make(chan error, 1)
			go func() {
				_, _, err := chain.ProcessBlock(block, BFNone)
				done <- err
			}()
			deadline := time.After(2 * time.Second)
			for ready.waits.Load() == 0 {
				select {
				case <-deadline:
					t.Fatal("block processing did not enter AIDX wait")
				default:
					time.Sleep(time.Millisecond)
				}
			}
			if rawBlockStored(t, chain, block.Hash()) || chain.index.HaveBlock(block.Hash()) {
				t.Fatal("waiting block was persisted before AIDX became ready")
			}
			if validator.calls.Load() != 0 {
				t.Fatal("contract validation ran before AIDX became ready")
			}

			ready.setTip(0, block.MsgBlock().Header.PrevBlock)
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("ready block failed: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("block processing did not resume")
			}
			if !rawBlockStored(t, chain, block.Hash()) {
				t.Fatal("accepted block raw data was not stored")
			}
		})
	}
}

func TestContractBlockReadinessWaitCancelsOnShutdown(t *testing.T) {
	chain, ready, validator, teardown := setupReadinessChain(t)
	defer teardown()
	block := readinessTestBlock(t, chain)
	interrupt := make(chan struct{})
	chain.interrupt = interrupt
	ready.mu.Lock()
	ready.interrupt = interrupt
	ready.mu.Unlock()
	ready.setTip(-1, chainhash.Hash{})

	done := make(chan error, 1)
	go func() {
		_, _, err := chain.ProcessBlock(block, BFNone)
		done <- err
	}()
	for ready.waits.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	close(interrupt)
	err := <-done
	if _, ok := err.(AssetIndexerNotReadyError); !ok {
		t.Fatalf("expected transient readiness error, got %T: %v", err, err)
	}
	if _, ok := err.(RuleError); ok {
		t.Fatalf("readiness error must not be RuleError: %v", err)
	}
	if rawBlockStored(t, chain, block.Hash()) || chain.index.HaveBlock(block.Hash()) {
		t.Fatal("canceled readiness wait persisted block state")
	}
	if validator.calls.Load() != 0 {
		t.Fatal("validator ran during canceled readiness wait")
	}
}

type scriptedReadiness struct {
	mu      sync.Mutex
	results []bool
	checks  atomic.Int32
}

func (r *scriptedReadiness) GetInternalTip() (int, chainhash.Hash, bool) {
	return 0, *chaincfg.RegressionNetParams.GenesisHash, true
}

func (r *scriptedReadiness) InternalTipReady(int, *chainhash.Hash) bool {
	r.checks.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.results) == 0 {
		return true
	}
	result := r.results[0]
	r.results = r.results[1:]
	return result
}

func (r *scriptedReadiness) EnsureInternalTip(int, *chainhash.Hash, int) error { return nil }

func (r *scriptedReadiness) WaitForInternalTip(int, *chainhash.Hash, <-chan struct{}) error {
	return nil
}

func TestContractBlockRechecksReadinessUnderChainLock(t *testing.T) {
	chain, _, _, teardown := setupReadinessChain(t)
	defer teardown()
	block := readinessTestBlock(t, chain)
	ready := &scriptedReadiness{results: []bool{true, false, true, true}}
	chain.assetIndexReadiness = ready
	if _, _, err := chain.ProcessBlock(block, BFNone); err != nil {
		t.Fatal(err)
	}
	if ready.checks.Load() < 4 {
		t.Fatalf("locked readiness failure was not rechecked externally, checks=%d", ready.checks.Load())
	}
}

func TestDirectInvalidBlockIsRejectedWithoutRawStorage(t *testing.T) {
	chain, _, validator, teardown := setupReadinessChain(t)
	defer teardown()
	valid := readinessTestBlock(t, chain)
	msg := *valid.MsgBlock()
	msg.Transactions = append([]*wire.MsgTx(nil), valid.MsgBlock().Transactions...)
	msg.Transactions[0] = msg.Transactions[0].Copy()
	msg.Transactions[0].TxOut[0].Value++
	utilTxs := make([]*btcutil.Tx, 0, len(msg.Transactions))
	for _, tx := range msg.Transactions {
		utilTxs = append(utilTxs, btcutil.NewTx(tx))
	}
	msg.Header.MerkleRoot = CalcMerkleRoot(utilTxs, false)
	block := btcutil.NewBlock(&msg)

	_, _, err := chain.ProcessBlock(block, BFNoPoWCheck)
	if err == nil {
		t.Fatal("expected invalid coinbase block rejection")
	}
	if _, ok := err.(RuleError); !ok {
		t.Fatalf("expected permanent RuleError, got %T: %v", err, err)
	}
	if rawBlockStored(t, chain, block.Hash()) || chain.index.HaveBlock(block.Hash()) {
		t.Fatal("permanently invalid direct block was persisted")
	}
	if _, ok := chain.rejectedBlockError(*block.Hash()); !ok {
		t.Fatal("invalid block hash was not retained in bounded reject cache")
	}
	known, err := chain.HaveBlock(block.Hash())
	if err != nil || !known {
		t.Fatalf("reject cache did not suppress duplicate download: known=%v err=%v", known, err)
	}
	before := validator.calls.Load()
	_, _, _ = chain.ProcessBlock(block, BFNoPoWCheck)
	if validator.calls.Load() != before {
		t.Fatal("cached invalid block was revalidated")
	}
}

func TestTemplateValidationIsReusedByBlockAcceptance(t *testing.T) {
	chain, _, validator, teardown := setupReadinessChain(t)
	defer teardown()
	block := readinessTestBlock(t, chain)
	if err := chain.CheckConnectBlockTemplate(block); err != nil {
		t.Fatal(err)
	}
	if _, _, err := chain.ProcessBlock(block, BFNone); err != nil {
		t.Fatal(err)
	}
	if got := validator.calls.Load(); got != 1 {
		t.Fatalf("contract validator ran %d times, want one prepared validation", got)
	}
}

func TestRejectedBlockCacheIsBoundedAndEpochScoped(t *testing.T) {
	cache := newBlockValidationCache()
	ruleErr := ruleError(ErrBadCoinbaseValue, "invalid")
	for i := 0; i < maxRejectedBlocks+10; i++ {
		var hash chainhash.Hash
		hash[0] = byte(i)
		hash[1] = byte(i >> 8)
		cache.addRejected(hash, ruleErr)
	}
	if got := len(cache.rejected); got != maxRejectedBlocks {
		t.Fatalf("reject cache size=%d, want %d", got, maxRejectedBlocks)
	}
	var hash chainhash.Hash
	hash[0] = 0xff
	cache.addRejected(hash, ruleErr)
	cache.mu.Lock()
	cache.rejected[hash].epoch = blockValidationCacheEpoch - 1
	cache.mu.Unlock()
	if _, ok := cache.rejectedError(hash); ok {
		t.Fatal("stale reject-cache epoch remained active")
	}
}

type testBranchAssetView struct {
	mu     sync.Mutex
	height int
	hash   chainhash.Hash
}

func (v *testBranchAssetView) GetInternalTip() (int, chainhash.Hash, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.height, v.hash, true
}

func (v *testBranchAssetView) ConnectBlock(block *wire.MsgBlock, height, _ int) error {
	if block == nil {
		return fmt.Errorf("nil validation block")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if height > 0 && (v.height != height-1 || v.hash != block.Header.PrevBlock) {
		return fmt.Errorf("validation view parent mismatch at %d", height)
	}
	v.height = height
	v.hash = block.BlockHash()
	return nil
}

func (*testBranchAssetView) GetInternalTickerInfo(*wire.AssetName) *sindexercommon.TickerInfo {
	return nil
}

func (*testBranchAssetView) GetInternalAssetUTXOsInAddress(string) map[wire.AssetName][]*sindexercommon.TxOutput {
	return nil
}

type branchReadiness struct {
	*testAssetReadiness
	baseHeight int
	baseHash   chainhash.Hash
}

func (r *branchReadiness) NewValidationAssetView() (ContractAssetIndexView, error) {
	return &testBranchAssetView{height: r.baseHeight, hash: r.baseHash}, nil
}

type branchPreparedValidator struct {
	mu       sync.Mutex
	prepared map[chainhash.Hash]ContractAssetIndexView
	parents  []chainhash.Hash
}

func (v *branchPreparedValidator) PrepareContractAssetIndexView(hash *chainhash.Hash,
	assetView ContractAssetIndexView) error {
	if hash == nil || assetView == nil {
		return fmt.Errorf("invalid prepared view")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.prepared[*hash] = assetView
	return nil
}

func (v *branchPreparedValidator) ValidateContractBlock(block *btcutil.Block, _ *UtxoViewpoint) error {
	if block == nil {
		return fmt.Errorf("nil block")
	}
	v.mu.Lock()
	assetView := v.prepared[*block.Hash()]
	delete(v.prepared, *block.Hash())
	v.mu.Unlock()
	if assetView == nil {
		// Direct best-tip validation intentionally uses the live readiness
		// state and therefore has no prepared branch view.
		return nil
	}
	height, hash, ok := assetView.GetInternalTip()
	wantHeight := int(block.Height() - 1)
	wantHash := block.MsgBlock().Header.PrevBlock
	if !ok || height != wantHeight || hash != wantHash {
		return fmt.Errorf("prepared branch AIDX got %d/%s want %d/%s", height, hash, wantHeight, wantHash)
	}
	v.mu.Lock()
	v.parents = append(v.parents, hash)
	v.mu.Unlock()
	return nil
}

func TestContractBlockReorgUsesBranchAlignedAssetView(t *testing.T) {
	chain, teardown, err := chainSetup(t.Name(), &chaincfg.RegressionNetParams)
	if err != nil {
		t.Fatal(err)
	}
	defer teardown()

	genesis := btcutil.NewBlock(chain.chainParams.GenesisBlock)
	genesis.SetHeight(0)
	genesisHash := *genesis.Hash()
	ready := &branchReadiness{
		testAssetReadiness: newTestAssetReadiness(0, genesisHash, chain.interrupt),
		baseHeight:         0,
		baseHash:           genesisHash,
	}
	validator := &branchPreparedValidator{prepared: make(map[chainhash.Hash]ContractAssetIndexView)}
	chain.assetIndexReadiness = ready
	chain.contractBlockValidator = validator

	mainBlock, _, err := newBlock(chain, genesis, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := chain.ProcessBlock(mainBlock, BFNoPoWCheck); err != nil {
		t.Fatalf("connect main block: %v", err)
	}
	ready.setTip(1, *mainBlock.Hash())

	forkBlock, _, err := newBlock(chain, genesis, nil)
	if err != nil {
		t.Fatal(err)
	}
	// newBlock is deterministic for the same parent/test payload. Change only
	// the header nonce; PoW is explicitly disabled for this regression.
	forkBlock.MsgBlock().Header.Nonce++
	forkBlock.SetHeight(1)
	if main, _, err := chain.ProcessBlock(forkBlock, BFNoPoWCheck); err != nil {
		t.Fatalf("store fork block: %v", err)
	} else if main {
		t.Fatal("equal-work fork unexpectedly became main chain")
	}

	forkTip, _, err := newBlock(chain, forkBlock, nil)
	if err != nil {
		t.Fatal(err)
	}
	if main, _, err := chain.ProcessBlock(forkTip, BFNoPoWCheck); err != nil {
		t.Fatalf("reorg to fork: %v", err)
	} else if !main {
		t.Fatal("higher-work fork did not become main chain")
	}
	if chain.bestChain.Tip().hash != *forkTip.Hash() {
		t.Fatalf("best tip=%s, want fork tip %s", chain.bestChain.Tip().hash, forkTip.Hash())
	}

	validator.mu.Lock()
	parents := append([]chainhash.Hash(nil), validator.parents...)
	validator.mu.Unlock()
	if len(parents) < 2 {
		t.Fatalf("branch validator prepared %d parent views, want at least fork parent and fork block", len(parents))
	}
	if parents[0] != genesisHash {
		t.Fatalf("first branch parent=%s, want genesis %s", parents[0], genesisHash)
	}
	foundForkParent := false
	for _, parent := range parents {
		if parent == *forkBlock.Hash() {
			foundForkParent = true
			break
		}
	}
	if !foundForkParent {
		t.Fatalf("branch validation never reached fork parent %s: %v", forkBlock.Hash(), parents)
	}
}
