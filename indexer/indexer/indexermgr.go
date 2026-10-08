package indexer

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sat20-labs/satoshinet/indexer/common"
	base_indexer "github.com/sat20-labs/satoshinet/indexer/indexer/base"
	contract_indexer "github.com/sat20-labs/satoshinet/indexer/indexer/contract"
	dkvs_indexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"

	"github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/rpcclient"
	"github.com/sat20-labs/satoshinet/wire"

	indexer "github.com/sat20-labs/indexer/common"
	db "github.com/sat20-labs/indexer/indexer/db"
)

type RPCConfig struct {
	Host      string
	Port      int
	User      string
	Password  string
	EnableTls bool
}

type Config struct {
	DataPath string
	RPCCfg   *RPCConfig
	DKVS     *DKVSIntegrationConfig
}

type DKVSIntegrationConfig struct {
	Resolver                     dkvs_indexer.DIDResolver
	ResolverHTTPBaseURL          string
	ResolverHTTPNamePath         string
	ResolverHTTPServicePath      string
	ResolverL1NSBaseURL          string
	ResolverL1NSNamePath         string
	ResolverL1NSServicePath      string
	FeeVerifier                  dkvs_indexer.FeeVerifier
	FeeVerifierHTTPEndpoint      string
	AutopayStateProvider         dkvs_indexer.AutopayStateProvider
	AutopayContract              string
	AutopayServiceName           string
	AutopayFeeRecipient          string
	AutopayFeeAssetName          string
	AutopayFullRecordFeePerBlock string
	SystemVerifier               dkvs_indexer.SystemVerifier
	EVMSourceVerifier            func(*wire.DKVSRecord) error
	SystemVerifierHTTPEndpoint   string
	MailboxPolicy                dkvs_indexer.MailboxPolicy
	BlobPolicy                   dkvs_indexer.BlobPolicy
	TmpPolicy                    dkvs_indexer.TmpPolicy
	AllowFreeLocal               *bool
	FreeLocalCache               *dkvs_indexer.FreeLocalCachePolicy
}

type IndexerMgr struct {
	cfg   *Config
	dbDir string

	// data from blockchain
	baseDB indexer.KVDB

	// data from market
	localDB indexer.KVDB
	dkvsDB  indexer.KVDB

	// 配置参数
	chaincfgParam   *chaincfg.Params
	maxIndexHeight  int
	periodFlushToDB int

	connectMutex sync.RWMutex
	mutex        sync.RWMutex
	// 跑数据
	checkOnce       bool
	lastCheckHeight int
	compiling       *base_indexer.BaseIndexer
	// 备份所有需要写入数据库的数据
	compilingBackupDB *base_indexer.BaseIndexer
	// 接收前端api访问的实例，隔离内存访问
	rpcService *base_indexer.RpcIndexer

	bRunning  bool
	interrupt <-chan struct{}

	contractIndexer  *contract_indexer.Indexer
	contractBackupDB *contract_indexer.Indexer

	dkvsIndexer         *dkvs_indexer.Indexer
	dkvsPruneStop       chan struct{}
	backgroundWG        sync.WaitGroup
	shutdownRPC         func() error
	closeOnce           sync.Once
	closeErr            error
	lastDKVSPruneHeight int
}

var instance *IndexerMgr

func GetIndexerMgr() *IndexerMgr {
	return instance
}

func NewIndexerMgr(
	cfg *Config,
	bTestNet bool,
	interrupt <-chan struct{},
) *IndexerMgr {

	if instance != nil {
		return instance
	}

	chainParam := &chaincfg.MainNetParams
	if bTestNet {
		indexer.CHAIN = "testnet"
		chainParam = &chaincfg.TestNetParams
	} else {
		chainParam = &chaincfg.MainNetParams
	}

	mgr := &IndexerMgr{
		cfg:               cfg,
		dbDir:             cfg.DataPath + "/db/indexer/" + chainParam.Name + "/",
		chaincfgParam:     chainParam,
		maxIndexHeight:    0,
		periodFlushToDB:   30,
		compilingBackupDB: nil,
		rpcService:        nil,
		bRunning:          false,
		interrupt:         interrupt,
	}

	instance = mgr
	return instance
}

func (b *IndexerMgr) Init() {
	b.connectMutex.Lock()
	defer b.connectMutex.Unlock()
	b.initLocked()
}

// initLocked initializes the indexer while connectMutex is already held.
// It must not re-enter public ConnectBlock when seeding an empty database.
func (b *IndexerMgr) initLocked() {
	err := b.initDB()
	if err != nil {
		common.Log.Panicf("initDB failed. %v", err)
	}
	b.compiling = base_indexer.NewBaseIndexer(b.baseDB, b.chaincfgParam, b.maxIndexHeight, b.periodFlushToDB)
	b.compiling.Init()
	b.contractIndexer = contract_indexer.NewIndexer(b.baseDB, b.chaincfgParam)
	b.dkvsIndexer = dkvs_indexer.New(b.dkvsDB, b.dkvsConfig())
	b.compiling.SetUpdateDBCallback(b.forceUpdateDB)
	b.compiling.SetBlockCallback(b.processBlock)
	b.lastCheckHeight = b.compiling.GetSyncHeight()

	dbver := b.GetBaseDBVer()
	common.Log.Infof("base db version: %s", dbver)
	if dbver != "" && dbver != common.BASE_DB_VERSION {
		common.Log.Panicf("DB version inconsistent. DB ver %s, but code base %s", dbver, common.BASE_DB_VERSION)
	}

	b.rpcService = base_indexer.NewRpcIndexer(b.compiling)

	b.compilingBackupDB = nil
	b.contractBackupDB = nil

	if b.lastCheckHeight == -1 {
		if err := b.connectBlockLocked(b.chaincfgParam.GenesisBlock, 0, 0); err != nil {
			common.Log.Panicf("connect genesis block failed. %v", err)
		}
	}

	b.startDKVSPruneTimer()
}

func (b *IndexerMgr) dkvsConfig() dkvs_indexer.Config {
	defaults := dkvs_indexer.NetworkDefaultsForParams(b.chaincfgParam)
	cfg := dkvs_indexer.Config{
		AllowFreeLocal: b.chaincfgParam != nil && !b.IsMainnet(),
		FreeLocalCache: dkvs_indexer.DefaultFreeLocalCachePolicy(),
		CurrentHeight: func() uint64 {
			if b.compiling == nil {
				return 0
			}
			height := b.compiling.GetHeight()
			if height < 0 {
				return 0
			}
			return uint64(height)
		},
	}
	ext := (*DKVSIntegrationConfig)(nil)
	if b.cfg != nil {
		ext = b.cfg.DKVS
	}
	if ext == nil {
		ext = &DKVSIntegrationConfig{}
	}
	if ext.AllowFreeLocal != nil {
		cfg.AllowFreeLocal = *ext.AllowFreeLocal
	}
	cfg.Resolver = ext.Resolver
	if cfg.Resolver == nil && ext.ResolverL1NSBaseURL != "" {
		cfg.Resolver = dkvs_indexer.L1NSResolver{
			BaseURL:       ext.ResolverL1NSBaseURL,
			NamePath:      ext.ResolverL1NSNamePath,
			ServicePath:   ext.ResolverL1NSServicePath,
			AddressParams: b.chaincfgParam,
		}
	}
	if cfg.Resolver == nil && ext.ResolverHTTPBaseURL != "" {
		cfg.Resolver = dkvs_indexer.HTTPDIDResolver{
			BaseURL:     ext.ResolverHTTPBaseURL,
			NamePath:    ext.ResolverHTTPNamePath,
			ServicePath: ext.ResolverHTTPServicePath,
		}
	}
	cfg.FeeVerifier = ext.FeeVerifier
	autopayFeeRecipient := ext.AutopayFeeRecipient
	autopayFeeAssetName := ext.AutopayFeeAssetName
	autopayFullRecordFeePerBlock := ext.AutopayFullRecordFeePerBlock
	autopayContract := ext.AutopayContract
	autopayServiceName := ext.AutopayServiceName
	if defaults.UseAutopayFeeVerifier {
		if autopayContract == "" {
			autopayContract = defaults.AutopayContract
		}
		if autopayServiceName == "" {
			autopayServiceName = defaults.AutopayServiceName
		}
		if autopayFeeRecipient == "" {
			autopayFeeRecipient = defaults.AutopayRecipient
		}
		if autopayFeeAssetName == "" {
			autopayFeeAssetName = defaults.AutopayFeeAssetName
		}
		if autopayFullRecordFeePerBlock == "" {
			autopayFullRecordFeePerBlock = defaults.FullRecordFeePerBlock
		}
	}
	if cfg.FeeVerifier == nil && autopayFullRecordFeePerBlock != "" {
		stateProvider := ext.AutopayStateProvider
		if stateProvider == nil {
			stateProvider = dkvs_indexer.RPCAutopayStateProvider{Call: satsnet_rpc.Call}
		}
		if _, ok := stateProvider.(*dkvs_indexer.HeightCachedAutopayStateProvider); !ok {
			stateProvider = &dkvs_indexer.HeightCachedAutopayStateProvider{
				Provider:      stateProvider,
				CurrentHeight: cfg.CurrentHeight,
			}
		}
		autopayVerifier := dkvs_indexer.AutopayFeeVerifier{
			StateProvider:         stateProvider,
			Contract:              autopayContract,
			ServiceName:           autopayServiceName,
			Recipient:             autopayFeeRecipient,
			FeeAssetName:          autopayFeeAssetName,
			FullRecordFeePerBlock: autopayFullRecordFeePerBlock,
			AddressParams:         b.chaincfgParam,
		}
		if cfg.AllowFreeLocal {
			cfg.FeeVerifier = dkvs_indexer.LocalCacheAutopayFeeVerifier{
				AutopayFeeVerifier: autopayVerifier,
				AllowFreeLocal:     true,
			}
		} else {
			cfg.FeeVerifier = autopayVerifier
		}
	}
	if cfg.FeeVerifier == nil && ext.FeeVerifierHTTPEndpoint != "" {
		cfg.FeeVerifier = dkvs_indexer.HTTPFeeVerifier{Endpoint: ext.FeeVerifierHTTPEndpoint}
	}
	cfg.SystemVerifier = ext.SystemVerifier
	cfg.EVMSourceVerifier = ext.EVMSourceVerifier
	if cfg.SystemVerifier == nil && ext.SystemVerifierHTTPEndpoint != "" {
		cfg.SystemVerifier = dkvs_indexer.HTTPSystemVerifier{Endpoint: ext.SystemVerifierHTTPEndpoint}
	}
	cfg.MailboxPolicy = ext.MailboxPolicy
	cfg.BlobPolicy = ext.BlobPolicy
	cfg.TmpPolicy = ext.TmpPolicy
	if ext.FreeLocalCache != nil {
		cfg.FreeLocalCache = *ext.FreeLocalCache
	}
	return cfg
}

func (b *IndexerMgr) GetBaseDB() indexer.KVDB {
	return b.baseDB
}

func (b *IndexerMgr) initRpcClient(dbPath string, cfg *RPCConfig) error {
	tip, err := satsnet_rpc.InitSatsNetClient(
		cfg.Host, cfg.Port, cfg.User, cfg.Password, dbPath, cfg.EnableTls,
	)
	if errors.Is(err, rpcclient.ErrClientShutdown) {
		return err // Normal shutdown must not start a fresh reconnect attempt.
	}
	if err != nil {
		b.backgroundWG.Add(1)
		go func() {
			defer b.backgroundWG.Done()
			n := 0
			var err error
			for n < 30 {
				select {
				case <-b.interrupt:
					return
				default:
				}
				tip, err = satsnet_rpc.InitSatsNetClient(
					cfg.Host, cfg.Port, cfg.User, cfg.Password, dbPath, cfg.EnableTls,
				)
				if errors.Is(err, rpcclient.ErrClientShutdown) {
					return
				}
				if err == nil {
					break
				}
				select {
				case <-b.interrupt:
					return
				case <-time.After(time.Second):
				}
				n++
			}
			if err != nil {
				common.Log.Panic("rpc client init failed")
			} else {
				if b.compiling.GetSyncHeight() < tip {
					b.ConnectBlock(nil, tip, tip)
				}
			}
		}()
	} else {
		if b.compiling.GetSyncHeight() < tip {
			b.ConnectBlock(nil, tip, tip)
		}
	}

	return nil
}

func (b *IndexerMgr) Start() error {
	err := b.initRpcClient(b.cfg.DataPath, b.cfg.RPCCfg)
	if err != nil {
		common.Log.Error(err)
		return err
	}

	if !b.bRunning {
		b.bRunning = true
		// 直接使用 ConnectBlock
		//go b.StartDaemon(b.interrupt)
		b.repair()
	}

	return nil
}

func (b *IndexerMgr) Stop() {
	b.bRunning = false
	b.stopDKVSPruneTimer()
	// The local node RPC may stop before HTTP/P2P/background users drain.
	// Cancel their futures instead of leaving them waiting for auto-reconnect.
	satsnet_rpc.ShutdownSatsNetClient()
}

// SetRPCShutdown registers the HTTP service owned by the indexer entry point.
// Configure it during initialization, before node shutdown can begin.
func (b *IndexerMgr) SetRPCShutdown(shutdown func() error) { b.shutdownRPC = shutdown }

// Close releases index databases after the owner has stopped mining, RPC and
// P2P access. Stop only signals background work; it must not close live handles.
func (b *IndexerMgr) Close() error {
	if b == nil {
		return nil
	}
	b.closeOnce.Do(func() {
		b.Stop()
		if b.shutdownRPC != nil {
			if err := b.shutdownRPC(); err != nil {
				b.closeErr = fmt.Errorf("stop indexer RPC: %w", err)
				return // Do not close databases while readers may remain.
			}
		}
		b.backgroundWG.Wait()
		b.connectMutex.Lock()
		defer b.connectMutex.Unlock()
		b.closeErr = b.closeDB()
	})
	return b.closeErr
}

func (b *IndexerMgr) dbgc() {
	for _, database := range []indexer.KVDB{b.localDB, b.baseDB, b.dkvsDB} {
		if database == nil {
			continue
		}
		if err := db.RunDBGC(database); err != nil && !errors.Is(err, db.ErrGCUnsupported) {
			common.Log.Warningf("indexer DB GC failed: %v", err)
		}
	}
	common.Log.Infof("dbgc completed")
}

// Only called by Close, never during reorg while database users are active.
func (b *IndexerMgr) closeDB() error {
	b.dbgc()
	var errs []error
	for _, database := range []indexer.KVDB{b.baseDB, b.localDB, b.dkvsDB} {
		if database != nil {
			if err := database.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (b *IndexerMgr) checkSelf() {
	start := time.Now()
	b.compiling.CheckSelf()
	if b.contractIndexer != nil && !b.contractIndexer.CheckSelf() {
		common.Log.Panicf("ContractIndexer.CheckSelf failed")
	}
	if b.dkvsIndexer == nil {
		common.Log.Panicf("DKVS indexer is nil")
	}

	common.Log.Infof("IndexerMgr.checkSelf takes %v", time.Since(start))
}

// Base has already committed before invoking this callback, as in L1.
// 故障处理约定：任何索引写盘失败都 panic。修复根因后丢弃索引 DB 并从头同步，
// 不保证部分提交后的自动恢复；不要为此改变回调职责或增加跨索引原子提交。
func (b *IndexerMgr) forceUpdateDB() {
	if b.contractIndexer != nil {
		b.contractIndexer.UpdateDB()
	}
}

func (b *IndexerMgr) handleReorg(height int) {
	// Reorg is an operator-handled fault, including administrator rollback.
	// Stop before closing/replacing DBs still used by DKVS P2P, RPC and timers.
	// Fatal exit cannot be recovered by an HTTP handler like a panic can.
	common.Log.Fatalf("indexer reorg to height %d is forbidden; stop the node and handle recovery manually", height)
}

// 为了回滚数据，我们采用这样的策略：
// 假设当前最新高度是h，那么数据库记录，最多只到（h-6），这样确保即使回滚，只需要从数据库回滚即可
// 为了保证数据库记录最高到（h-6），我们做一次数据备份，到合适实际再写入数据库
func (b *IndexerMgr) updateDB(height, tip int) {
	if height >= tip {
		b.updateServiceInstance()
	}

	complingHeight := b.compiling.GetHeight()
	syncHeight := b.compiling.GetSyncHeight()
	blocksInHistory := b.compiling.GetBlockHistory()

	gap := complingHeight - syncHeight
	if gap < blocksInHistory {
		common.Log.Infof("performUpdateDBInBuffer nothing to do at height %d-%d", complingHeight, syncHeight)
	} else {
		if b.compilingBackupDB == nil {
			b.prepareDBBuffer()
		}
		// 这个区间不备份数据
		if gap < 2*blocksInHistory {
			common.Log.Infof("performUpdateDBInBuffer nothing to do at height %d-%d", complingHeight, syncHeight)
			return
		}

		// 到达高度时，将备份的数据写入数据库中。
		common.Log.Infof("performUpdateDBInBuffer performUpdateDBInBuffer at height %d-%d", complingHeight, syncHeight)
		b.performUpdateDBInBuffer()

		// 备份当前高度的数据
		b.prepareDBBuffer()
	}
}

func (b *IndexerMgr) performUpdateDBInBuffer() {
	// Trim before committing; Base's lock also prevents stale DB cache refills.
	// Base and Contract intentionally commit separately under the failure policy
	// documented on forceUpdateDB. A panic requires a fresh full index replay.
	if b.contractIndexer != nil && b.contractBackupDB != nil {
		b.contractIndexer.Subtract(b.contractBackupDB)
	}
	b.compiling.CommitBackup(b.compilingBackupDB)
	if b.contractBackupDB != nil {
		b.contractBackupDB.UpdateDB()
	}
}

func (b *IndexerMgr) prepareDBBuffer() {
	b.compilingBackupDB = b.compiling.Clone(true)
	if b.contractIndexer != nil {
		b.contractBackupDB = b.contractIndexer.Clone()
	}
	common.Log.Infof("backup instance %d cloned", b.compilingBackupDB.GetHeight())
}

func (b *IndexerMgr) updateServiceInstance() {
	if b.rpcService.GetHeight() == b.compiling.GetHeight() {
		return
	}

	newService := base_indexer.NewRpcIndexer(b.compiling)
	common.Log.Infof("service instance %d cloned", newService.GetHeight())

	newService.UpdateServiceInstance()
	b.mutex.Lock()
	b.rpcService = newService
	b.mutex.Unlock()
}

func (p *IndexerMgr) repair() bool {
	p.compiling.Repair()
	return false
}

func (p *IndexerMgr) dbStatistic() bool {
	// save to latest DB first, save time.
	// if p.compilingBackupDB == nil {
	// 	p.prepareDBBuffer()
	// }
	// p.performUpdateDBInBuffer()

	//common.Log.Infof("start searching...")
	//return p.SearchPredefinedName()
	//return p.searchName()
	return false
}

type connectBlockOps struct {
	internalTip       func() (int, string)
	syncBlockAtHeight func(height, tip int) error
	syncBlock         func(block *wire.MsgBlock, height, tip int) error
	prepareDBBuffer   func()
	publish           func(height, tip int)
}

func connectBlock(ops connectBlockOps, block *wire.MsgBlock, height, tip int) error {
	lastHeight, _ := ops.internalTip()
	targetHeight := height
	if block != nil {
		targetHeight--
	}
	if lastHeight > targetHeight {
		if block == nil || lastHeight > height {
			return fmt.Errorf("indexer tip height %d is ahead of target %d", lastHeight, targetHeight)
		}

		// A repeated notification for the block already compiled is idempotent,
		// but still publishes the compiling snapshot to the RPC service.
		_, lastHash := ops.internalTip()
		blockHash := block.BlockHash().String()
		if lastHash != blockHash {
			return fmt.Errorf("indexer block hash mismatch at height %d: got %s, want %s", height, lastHash, blockHash)
		}
		ops.publish(height, tip)
		return nil
	}

	backfilled := false
	for i := lastHeight + 1; i <= targetHeight; i++ {
		if err := ops.syncBlockAtHeight(i, tip); err != nil {
			return fmt.Errorf("sync block at height %d: %w", i, err)
		}
		backfilled = true
	}
	if backfilled {
		ops.prepareDBBuffer()
	}

	if block != nil {
		parentHeight, parentHash := ops.internalTip()
		if height == 0 {
			if parentHeight != -1 {
				return fmt.Errorf("indexer tip height %d is not before genesis", parentHeight)
			}
		} else {
			if parentHeight != height-1 {
				return fmt.Errorf("indexer parent height mismatch for block %d: got %d, want %d", height, parentHeight, height-1)
			}
			wantParentHash := block.Header.PrevBlock.String()
			if parentHash != wantParentHash {
				return fmt.Errorf("indexer parent hash mismatch for block %d: got %s, want %s", height, parentHash, wantParentHash)
			}
		}
		if err := ops.syncBlock(block, height, tip); err != nil {
			return fmt.Errorf("sync provided block at height %d: %w", height, err)
		}
	}

	ops.publish(height, tip)
	return nil
}

func (p *IndexerMgr) connectBlockOps() connectBlockOps {
	return connectBlockOps{
		internalTip: p.compiling.GetInternalTip,
		syncBlockAtHeight: func(height, tip int) error {
			// A chain-lock holder must supply contiguous blocks. RPC here
			// would invert chainLock -> connectMutex through getblock.
			return fmt.Errorf("missing asset block %d; supply canonical blocks before connecting", height)
		},
		syncBlock: func(block *wire.MsgBlock, height, tip int) error {
			return p.compiling.SyncBlock(block, height, tip, false)
		},
		prepareDBBuffer: p.prepareDBBuffer,
		publish:         p.updateDB,
	}
}

func (p *IndexerMgr) connectBlockLocked(block *wire.MsgBlock, height, tip int) error {
	return connectBlock(p.connectBlockOps(), block, height, tip)
}

func ensureInternalTip(ops connectBlockOps, height int, hash *chainhash.Hash, tip int) error {
	if hash == nil {
		return fmt.Errorf("nil internal tip target")
	}
	currentHeight, currentHashText := ops.internalTip()
	currentHash, err := chainhash.NewHashFromStr(currentHashText)
	if err != nil {
		return fmt.Errorf("decode current internal tip hash at height %d: %w", currentHeight, err)
	}
	if currentHeight == height && *currentHash == *hash {
		return nil
	}
	if currentHeight > height {
		return fmt.Errorf("indexer tip %d is ahead of readiness target %d", currentHeight, height)
	}
	if err := connectBlock(ops, nil, height, tip); err != nil {
		return fmt.Errorf("repair internal tip to %d/%s: %w", height, hash, err)
	}
	currentHeight, currentHashText = ops.internalTip()
	currentHash, err = chainhash.NewHashFromStr(currentHashText)
	if err != nil {
		return fmt.Errorf("decode repaired internal tip hash at height %d: %w", currentHeight, err)
	}
	if currentHeight != height || *currentHash != *hash {
		return fmt.Errorf("repaired internal tip mismatch: got %d/%s want %d/%s",
			currentHeight, currentHash, height, hash)
	}
	return nil
}

// catchUpWithRPC is used by startup/standalone repair, never by a chain-lock
// holder supplying a block. Read each block without connectMutex, then verify
// the compiling instance and parent again before applying it.
func (p *IndexerMgr) catchUpWithRPC(height, tip int) error {
	for {
		p.connectMutex.Lock()
		compiling := p.compiling
		lastHeight, lastHash := compiling.GetInternalTip()
		if lastHeight >= height {
			p.updateDB(height, tip)
			p.connectMutex.Unlock()
			return nil
		}
		p.connectMutex.Unlock()
		if p.interrupt != nil {
			select {
			case <-p.interrupt:
				return fmt.Errorf("asset RPC catch-up canceled")
			default:
			}
		}
		// Preserve the existing transient-RPC retries, also outside the lock.
		var hash *chainhash.Hash
		var block *wire.MsgBlock
		var err error
		for attempt := 0; attempt < 10; attempt++ {
			if attempt > 0 {
				select {
				case <-p.interrupt:
					return fmt.Errorf("asset RPC catch-up canceled")
				case <-time.After(time.Duration(attempt) * time.Second):
				}
			}
			hash, err = satsnet_rpc.GetBlockHash(int64(lastHeight + 1))
			if err == nil {
				block, err = satsnet_rpc.GetRawBlock(hash)
			}
			if err == nil {
				break
			}
		}
		if err != nil {
			return err
		}
		if block.BlockHash() != *hash {
			return fmt.Errorf("RPC block hash mismatch at height %d", lastHeight+1)
		}
		p.connectMutex.Lock()
		if p.compiling != compiling {
			p.connectMutex.Unlock()
			return fmt.Errorf("asset index changed during RPC catch-up")
		}
		currentHeight, currentHash := compiling.GetInternalTip()
		if currentHeight != lastHeight || currentHash != lastHash {
			p.connectMutex.Unlock()
			continue // A normal connection advanced the same compiling index.
		}
		// RPC and supplied blocks share snapshot ownership and the 20/40-block
		// commit window. Force-writing the live index here would bypass any
		// pending backup which already owns transferred address dirty flags.
		err = p.connectBlockLocked(block, lastHeight+1, tip)
		p.connectMutex.Unlock()
		if err != nil {
			return err
		}
	}
}

// tip: 本地最长链； block，新接收到的区块，一般会比tip高1
func (p *IndexerMgr) ConnectBlock(block *wire.MsgBlock, height, tip int) {
	if block == nil {
		if err := p.catchUpWithRPC(height, tip); err != nil {
			common.Log.Errorf("ConnectBlock catch-up failed: %v", err)
			return
		}
		p.connectMutex.Lock()
		defer p.connectMutex.Unlock()
		if (height+1)%200 == 0 {
			p.checkSelf()
		}
		return
	}
	p.connectMutex.Lock()
	defer p.connectMutex.Unlock()

	lastHeight := p.compiling.GetHeight()
	common.Log.Infof("compiling height %d, block %d, tip %d", lastHeight, height, tip)
	err := p.connectBlockLocked(block, height, tip)
	if err != nil {
		common.Log.Errorf("ConnectBlock failed, %v", err)
		return
	}

	if (height+1)%200 == 0 { // TODO 先多检查，以后稳定了再降低检查频率
		p.checkSelf()
	}
}

func (p *IndexerMgr) DisconnectBlock(height int) {
	p.connectMutex.Lock()
	defer p.connectMutex.Unlock()
	p.handleReorg(height)
}
