// Copyright (c) 2014-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package posminer

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	"github.com/sat20-labs/satoshinet/wire"
	peerpkg "github.com/sat20-labs/satoshinet/peer"
)

const (
	// maxNonce is the maximum value a nonce can be in a block header.
	maxNonce = ^uint32(0) // 2^32 - 1

	// maxExtraNonce is the maximum value an extra nonce used in a coinbase
	// transaction can be.
	maxExtraNonce = ^uint64(0) // 2^64 - 1

	// hpsUpdateSecs is the number of seconds to wait in between each
	// update to the hashes per second monitor.
	hpsUpdateSecs = 10

	// hashUpdateSec is the number of seconds each worker waits in between
	// notifying the speed monitor with how many hashes have been completed
	// while they are actively searching for a solution.  This is done to
	// reduce the amount of syncs between the workers that must be done to
	// keep track of the hashes per second.
	hashUpdateSecs = 15

	// hashUpdateSec is the number of seconds each worker waits in between
	// notifying the speed monitor with how many hashes have been completed
	// while they are actively searching for a solution.  This is done to
	// reduce the amount of syncs between the workers that must be done to
	// keep track of the hashes per second.
	blockGenerateSecs = 60
)

var (
	// defaultNumWorkers is the default number of workers to use for mining
	// and is based on the number of processor cores.  This helps ensure the
	// system stays reasonably responsive under heavy load.
	//defaultNumWorkers = uint32(runtime.NumCPU())
	defaultNumWorkers = uint32(1) // only one worker for a node
)

// Config is a descriptor containing the cpu miner configuration.
type Config struct {
	// ChainParams identifies which chain parameters the cpu miner is
	// associated with.
	ChainParams *chaincfg.Params

	// Btcd Dir
	BtcdDir string

	// BlockTemplateGenerator identifies the instance to use in order to
	// generate block templates that the miner will attempt to solve.
	BlockTemplateGenerator *mining.BlkTmplGenerator

	// MiningAddrs is the payment addresses to use for the generated blocks.
	MiningAddr   btcutil.Address
	MiningPubKey string
	ServerPubKey string

	TimerGenerate bool

	// ProcessBlock defines the function to call with any solved blocks.
	// It typically must run the provided block through the same set of
	// rules and handling as any other block coming from the network.
	ProcessBlock func(*btcutil.Block, blockchain.BehaviorFlags) (bool, error)

	GetPeerByValidatorId func(validatorId string) *peerpkg.Peer
	GetRandomCorePeer func () *peerpkg.Peer

	// ConnectedCount defines the function to use to obtain how many other
	// peers the server is connected to.  This is used by the automatic
	// persistent mining routine to determine whether or it should attempt
	// mining.  This is useful because there is no point in mining when not
	// connected to any peers since there would no be anyone to send any
	// found blocks to.
	ConnectedCount func() int32

	// IsCurrent defines the function to use to obtain whether or not the
	// block chain is current.  This is used by the automatic persistent
	// mining routine to determine whether or it should attempt mining.
	// This is useful because there is no point in mining if the chain is
	// not current since any solved blocks would be on a side chain and and
	// up orphaned anyways.
	IsCurrent func() bool
}

// POSMiner provides facilities for solving blocks (mining) using the POS in
// a concurrency-safe manner.  It consists of two main goroutines -- a speed
// monitor and a controller for worker goroutines which generate and solve
// blocks.  The number of goroutines can be set via the SetMaxGoRoutines
// function, but the default is based on the number of processor cores in the
// system which is typically sufficient.
type POSMiner struct {
	sync.Mutex
	g                 *mining.BlkTmplGenerator
	cfg               Config
	numWorkers        uint32
	started           bool
	discreteMining    bool
	submitBlockLock   sync.Mutex
	wg                sync.WaitGroup
	updateNumWorkers  chan struct{}
	queryHashesPerSec chan float64
	updateHashes      chan uint64
	quit              chan struct{}

	validatorMgr *ValidatorManager
}

// submitBlock submits the passed block to network after ensuring it passes all
// of the consensus validation rules.
func (m *POSMiner) submitBlock(block *btcutil.Block) bool {
	m.submitBlockLock.Lock()
	defer m.submitBlockLock.Unlock()

	// Ensure the block is not stale since a new block could have shown up
	// while the solution was being found.  Typically that condition is
	// detected and all work on the stale block is halted to start work on
	// a new block, but the check only happens periodically, so it is
	// possible a block was found and submitted in between.
	msgBlock := block.MsgBlock()
	if !msgBlock.Header.PrevBlock.IsEqual(&m.g.BestSnapshot().Hash) {
		utils.Log.Errorf("Block submitted via POS miner with previous "+
			"block %s is stale", msgBlock.Header.PrevBlock)
		return false
	}

	// Process this block using the same rules as blocks coming from other
	// nodes.  This will in turn relay it to the network like normal.
	isOrphan, err := m.cfg.ProcessBlock(block, blockchain.BFNone)
	if err != nil {
		// Anything other than a rule violation is an unexpected error,
		// so log that error as an internal error.
		if _, ok := err.(blockchain.RuleError); !ok {
			utils.Log.Errorf("Unexpected error while processing "+
				"block submitted via POS miner: %v", err)
			return false
		}

		utils.Log.Errorf("Block submitted via POS miner rejected: %v", err)
		return false
	}
	if isOrphan {
		utils.Log.Errorf("Block submitted via POS miner is an orphan")
		return false
	}

	// The block was accepted.
	utils.Log.Infof("Block submitted via POS miner accepted (hash %s)", block.Hash())
	return true
}

// solveBlock attempts to find some combination of a nonce, extra nonce, and
// current timestamp which makes the passed block hash to a value less than the
// target difficulty.  The timestamp is updated periodically and the passed
// block is modified with all tweaks during this process.  This means that
// when the function returns true, the block is ready for submission.
//
// This function will return early with false when conditions that trigger a
// stale block such as a new block showing up or periodically when there are
// new transactions and enough time has elapsed without finding a solution.
func (m *POSMiner) solveBlock(msgBlock *wire.MsgBlock, blockHeight int32) bool {

	utils.Log.Tracef("solveBlock ...")
	// Choose a random extra nonce offset for this block template and
	// worker.
	enOffset, err := wire.RandomUint64()
	if err != nil {
		utils.Log.Errorf("Unexpected error while generating random "+
			"extra nonce offset: %v", err)
		enOffset = 0
	}

	// Create some convenience variables.
	header := &msgBlock.Header
	// targetDifficulty := blockchain.CompactToBig(header.Bits)

	// // Initial state.
	// lastGenerated := time.Now()
	// lastTxUpdate := m.g.TxSource().LastUpdated()
	//hashesCompleted := uint64(0)

	// Note that the entire extra nonce range is iterated and the offset is
	// added relying on the fact that overflow will wrap around 0 as
	// provided by the Go spec.
	//for extraNonce := uint64(0); extraNonce < maxExtraNonce; extraNonce++ {
	extraNonce, _ := wire.RandomUint64()
	// Update the extra nonce in the block template with the
	// new value by regenerating the coinbase script and
	// setting the merkle root to the new value.
	m.g.UpdateExtraNonce(msgBlock, blockHeight, extraNonce+enOffset)

	// Search through the entire nonce range for a solution while
	// periodically checking for early quit and stale block
	// conditions along with updates to the speed monitor.
	//for i := uint32(0); i <= maxNonce; i++ {
	i, _ := wire.RandomUint64()
	// select {
	// case <-quit:
	// 	return false

	// case <-ticker.C:
	// 	m.updateHashes <- hashesCompleted
	// 	hashesCompleted = 0

	// The current block is stale if the best block
	// has changed.
	best := m.g.BestSnapshot()
	if !header.PrevBlock.IsEqual(&best.Hash) {
		return false
	}

	// The current block is stale if the memory pool
	// has been updated since the block template was
	// generated and it has been at least one
	// minute.
	// if lastTxUpdate != m.g.TxSource().LastUpdated() &&
	// 	time.Now().After(lastGenerated.Add(time.Minute)) {

	// 	return false
	// }

	m.g.UpdateBlockTime(msgBlock)

	// default:
	// 	// Non-blocking select to fall through
	// }

	// Update the nonce and hash the block header.  Each
	// hash is actually a double sha256 (two hashes), so
	// increment the number of hashes completed for each
	// attempt accordingly.
	header.Nonce = uint32(i)
	// hash := header.BlockHash()
	// hashesCompleted += 2

	// The block is solved when the new block hash is less
	// than the target difficulty.  Yay!
	// if blockchain.HashToBig(&hash).Cmp(targetDifficulty) <= 0 {
	// 	m.updateHashes <- hashesCompleted
	// 	return true
	// }
	//}
	//}

	utils.Log.Tracef("solveBlock done.")
	return true
}

// Start begins the POS mining process as well as the speed monitor used to
// track hashing metrics.  Calling this function when the POS miner has
// already been started will have no effect.
//
// This function is safe for concurrent access.
func (m *POSMiner) Start() error {
	m.Lock()
	defer m.Unlock()

	// Nothing to do if the miner is already running or if running in
	// discrete mode (using GenerateNBlocks).
	if m.started || m.discreteMining {
		return nil
	}

	m.quit = make(chan struct{})
	// if m.cfg.TimerGenerate {
	// 	utils.Log.Infof("POS miner started with timerGenerate")
	// 	m.wg.Add(1)
	// 	go m.miningWorkerController()
	// }

	cfg := &ValidatorManagerConfig{
		Config:   &m.cfg,
		PosMiner: m,
	}
	// Start ValidatorManager
	m.validatorMgr = NewValidatorManager(cfg)
	if m.validatorMgr == nil {
		utils.Log.Errorf("NewValidatorManager failed")
		return fmt.Errorf("NewValidatorManager failed")
	}
	
	m.validatorMgr.Start()

	m.started = true
	utils.Log.Infof("POS miner started, mining address: %s", cfg.MiningAddr.EncodeAddress())

	return nil
}

// Stop gracefully stops the mining process by signalling all workers, and the
// speed monitor to quit.  Calling this function when the POS miner has not
// already been started will have no effect.
//
// This function is safe for concurrent access.
func (m *POSMiner) Stop() {
	m.Lock()
	defer m.Unlock()

	// Nothing to do if the miner is not currently running or if running in
	// discrete mode (using GenerateNBlocks).
	if !m.started || m.discreteMining {
		return
	}

	if m.validatorMgr != nil {
		m.validatorMgr.Stop()
	}

	close(m.quit)
	utils.Log.Infof("Wait wg done")
	m.wg.Wait()
	m.started = false
	utils.Log.Infof("POS miner stopped")
}

// IsMining returns whether or not the POS miner has been started and is
// therefore currenting mining.
//
// This function is safe for concurrent access.
func (m *POSMiner) IsMining() bool {
	m.Lock()
	defer m.Unlock()

	return m.started
}

// HashesPerSecond returns the number of hashes per second the mining process
// is performing.  0 is returned if the miner is not currently running.
//
// This function is safe for concurrent access.
func (m *POSMiner) HashesPerSecond() float64 {
	m.Lock()
	defer m.Unlock()

	// Nothing to do if the miner is not currently running.
	if !m.started {
		return 0
	}

	return <-m.queryHashesPerSec
}

// SetNumWorkers sets the number of workers to create which solve blocks.  Any
// negative values will cause a default number of workers to be used which is
// based on the number of processor cores in the system.  A value of 0 will
// cause all POS mining to be stopped.
//
// This function is safe for concurrent access.
func (m *POSMiner) SetNumWorkers(numWorkers int32) {
	if numWorkers == 0 {
		m.Stop()
	}

	// Don't lock until after the first check since Stop does its own
	// locking.
	m.Lock()
	defer m.Unlock()

	// Use default if provided value is negative.
	if numWorkers < 0 {
		m.numWorkers = defaultNumWorkers
	} else {
		m.numWorkers = uint32(numWorkers)
	}

	// When the miner is already running, notify the controller about the
	// the change.
	if m.started {
		m.updateNumWorkers <- struct{}{}
	}
}

// NumWorkers returns the number of workers which are running to solve blocks.
//
// This function is safe for concurrent access.
func (m *POSMiner) NumWorkers() int32 {
	m.Lock()
	defer m.Unlock()

	return int32(m.numWorkers)
}

// 处理RPC消息
// GenerateNBlocks generates the requested number of blocks. It is self
// contained in that it creates block templates and attempts to solve them while
// detecting when it is performing stale work and reacting accordingly by
// generating a new block template.  When a block is solved, it is submitted.
// The function returns a list of the hashes of generated blocks.
func (m *POSMiner) GenerateNBlocks(n uint32) ([]*chainhash.Hash, error) {
	m.Lock()

	// Respond with an error if server is already mining.
	if m.started || m.discreteMining {
		m.Unlock()
		return nil, errors.New("Server is already in POS mining. Please call " +
			"`setgenerate 0` before calling discrete `generate` commands.")
	}

	m.started = true
	m.discreteMining = true

	//m.speedMonitorQuit = make(chan struct{})
	//m.wg.Add(1)
	//go m.speedMonitor()

	m.Unlock()

	utils.Log.Tracef("Generating %d blocks", n)

	i := uint32(0)
	blockHashes := make([]*chainhash.Hash, n)

	// Start a ticker which is used to signal checks for stale work and
	// updates to the speed monitor.
	ticker := time.NewTicker(time.Second * hashUpdateSecs)
	defer ticker.Stop()

	for {
		// Read updateNumWorkers in case someone tries a `setgenerate` while
		// we're generating. We can ignore it as the `generate` RPC call only
		// uses 1 worker.
		select {
		case <-m.updateNumWorkers:
		default:
		}

		// Grab the lock used for block submission, since the current block will
		// be changing and this would otherwise end up building a new block
		// template on a block that is in the process of becoming stale.
		m.submitBlockLock.Lock()
		curHeight := m.g.BestSnapshot().Height

		// Choose a payment address at random.
		// rand.Seed(time.Now().UnixNano())
		// payToAddr := m.cfg.MiningAddrs[rand.Intn(len(m.cfg.MiningAddrs))]
		payToAddr := m.cfg.MiningAddr

		// Create a new block template using the available transactions
		// in the memory pool as a source of transactions to potentially
		// include in the block.
		utils.Log.Tracef("Generating %d blocks", n)
		template, err := m.g.NewBlockTemplate(payToAddr)
		m.submitBlockLock.Unlock()
		if err != nil {
			errStr := fmt.Sprintf("Failed to create new block template: %v", err)
			utils.Log.Warning(errStr)
			continue
		}

		// Attempt to solve the block.  The function will exit early
		// with false when conditions that trigger a stale block, so
		// a new block template can be generated.  When the return is
		// true a solution was found, so submit the solved block.
		if m.solveBlock(template.Block, curHeight+1) {
			block := btcutil.NewBlock(template.Block)
			m.submitBlock(block)
			blockHashes[i] = block.Hash()
			i++
			if i == n {
				utils.Log.Tracef("Generated %d blocks", i)
				m.Lock()
				//close(m.speedMonitorQuit)
				//m.wg.Wait()
				m.started = false
				m.discreteMining = false
				m.Unlock()
				return blockHashes, nil
			}
		}
	}
}

var (
	lastValidBlockHash chainhash.Hash
)

// New returns a new instance of a POS miner for the provided configuration.
// Use Start to begin the mining process.  See the documentation for POSMiner
// type for more details.
func New(cfg *Config) *POSMiner {
	lastValidBlockHash = cfg.BlockTemplateGenerator.BestSnapshot().Hash // For test
	return &POSMiner{
		g:                 cfg.BlockTemplateGenerator,
		cfg:               *cfg,
		numWorkers:        defaultNumWorkers,
		updateNumWorkers:  make(chan struct{}),
		queryHashesPerSec: make(chan float64),
		updateHashes:      make(chan uint64),
	}
}

// OnTimeGenerateBlock is invoke when time to generate block.
func (m *POSMiner) OnTimeGenerateBlock() (*wire.MsgBlock, error) {
	//utils.Log.Debugf("Timeup for OnTimeGenerateBlock ......")

	//return m.GenerateNewTestBlock()

	msgblock, err := m.GenerateNewBlock()
	if err != nil {
		return nil, err
	}
	block := btcutil.NewBlock(msgblock)
	// Ensure the block is building from the expected previous block.
	expectedPrevHash := m.GetBlockHash()
	prevHash := &block.MsgBlock().Header.PrevBlock
	if !expectedPrevHash.IsEqual(prevHash) {
		return nil, fmt.Errorf("not build from tip block")
	}
	if err := m.g.BlockChain().CheckConnectBlockTemplate(block); err != nil {
		return nil, fmt.Errorf("CheckConnectBlockTemplate failed: %v", err)
	}
	return msgblock, nil
}

func (m *POSMiner) GenerateNewTestBlock() (*chainhash.Hash, int32, error) {
	utils.Log.Tracef("GenerateNewTestBlock ......")

	// return nil, errors.New("Test generate failed.")
	curHeight := m.g.BestSnapshot().Height
	if curHeight != 0 && !m.cfg.IsCurrent() {
		time.Sleep(time.Second)
		utils.Log.Tracef("curHeight = %d and not current %d.", curHeight, m.cfg.IsCurrent())
		err := fmt.Errorf("the blockchain is not best chain")
		return nil, 0, err
	}

	currentBlockHash := m.g.BestSnapshot().Hash // Foe test
	if lastValidBlockHash.IsEqual(&currentBlockHash) {
		// No new tx is mind
		err := fmt.Errorf("no any new tx in mempool")
		return nil, 0, err
	}

	lastValidBlockHash = currentBlockHash
	return &currentBlockHash, curHeight, nil

	// blockHash := chainhash.DoubleHashRaw(func(w io.Writer) error {
	// 	buf := make([]byte, 128)
	// 	for i := 0; i < 128; i++ {
	// 		data := rand.Int31()
	// 		buf[i] = byte(data)
	// 	}
	// 	if _, err := w.Write(buf[:]); err != nil {
	// 		return err
	// 	}

	// 	return nil
	// })

	// return &blockHash, curHeight + 1, nil
}

func (m *POSMiner) GenerateNewBlock() (*wire.MsgBlock, error) {
	// Wait until there is a connection to at least one other peer
	// since there is no way to relay a found block or receive
	// transactions to work on when there are no connected peers.
	// if m.cfg.ConnectedCount() == 0 {
	// 	time.Sleep(time.Second)
	// 	continue
	// }

	// No point in searching for a solution before the chain is
	// synced.  Also, grab the same lock as used for block
	// submission, since the current block will be changing and
	// this would otherwise end up building a new block template on
	// a block that is in the process of becoming stale.
	utils.Log.Debugf("GenerateNewBlock ...")
	m.submitBlockLock.Lock()
	curHeight := m.g.BestSnapshot().Height
	if curHeight != 0 && !m.cfg.IsCurrent() {
		m.submitBlockLock.Unlock()
		utils.Log.Warningf("curHeight %d is not current.", curHeight)
		return nil, fmt.Errorf("the blockchain is not best chain")
	}

	// Choose a payment address at random.
	// rand.Seed(time.Now().UnixNano())
	// payToAddr := m.cfg.MiningAddrs[rand.Intn(len(m.cfg.MiningAddrs))]
	payToAddr := m.cfg.MiningAddr

	// Create a new block template using the available transactions
	// in the memory pool as a source of transactions to potentially
	// include in the block.
	utils.Log.Tracef("NewBlockTemplate...")
	template, err := m.g.NewBlockTemplate(payToAddr)
	m.submitBlockLock.Unlock()
	if err != nil {
		errStr := fmt.Sprintf("Failed to create new block template: %v", err)
		utils.Log.Warning(errStr)
		return nil, err
	}

	utils.Log.Tracef("NewBlockTemplate done.")

	// Attempt to solve the block.  The function will exit early
	// with false when conditions that trigger a stale block, so
	// a new block template can be generated.  When the return is
	// true a solution was found, so submit the solved block.
	if m.solveBlock(template.Block, curHeight+1) {
		// 暂时不提交
		// utils.Log.Debugf("submitBlock ...")
		// block := btcutil.NewBlock(template.Block)
		// m.submitBlock(block)
		// blockHash := block.Hash()
		// utils.Log.Debugf("submitBlock %d", curHeight + 1)
		return template.Block, nil
	}

	return nil, fmt.Errorf("failed to solve block")
}

// submit a new block
func (m *POSMiner) SubmitNewBlock(block *wire.MsgBlock) (*chainhash.Hash, int32, error) {
	utils.Log.Debugf("SubmitNewBlock ...")
	newblock := btcutil.NewBlock(block)
	m.submitBlock(newblock)
	utils.Log.Infof("SubmitNewBlock %d %s", newblock.Height(), newblock.Hash().String())
	return newblock.Hash(), newblock.Height(), nil
}

// GetBlockHeight invoke when get block height from pos miner.
func (m *POSMiner) GetBlockHeight() int32 {
	return m.g.BestSnapshot().Height
}

// GetBlockHeight invoke when get block height from pos miner.
func (m *POSMiner) GetBlockHash() chainhash.Hash {
	return m.g.BestSnapshot().Hash
}

// GetBlockHeight invoke when get block height from pos miner.
func (m *POSMiner) GetBlockRecvTime() int64 {
	return m.g.BestSnapshot().RecvTime
}


func (m *POSMiner) GetMempoolTxSize() int32 {

	if m.g == nil {
		utils.Log.Errorf("[PosMiner] Invalid mempool generator.")
		return 0
	}
	txSource := m.g.TxSource()
	if txSource == nil {
		utils.Log.Tracef("[PosMiner] Invalid mempool tx source.")
		return 0
	}
	sourceTxns := txSource.MiningDescs()

	txSize := len(sourceTxns)

	utils.Log.Tracef("[PosMiner] Current mempool tx size = %d", txSize)

	return int32(txSize)
}

func (m *POSMiner) GetPeerByValidatorId(validatorId string) *peerpkg.Peer {
	return m.cfg.GetPeerByValidatorId(validatorId)
}

func (m *POSMiner) GetRandomCorePeer() *peerpkg.Peer {
	return m.cfg.GetRandomCorePeer()
}