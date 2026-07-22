package indexer

import (
	"sync"
	"time"

	"github.com/sat20-labs/satoshinet/indexer/common"
	base_indexer "github.com/sat20-labs/satoshinet/indexer/indexer/base"
	contract_indexer "github.com/sat20-labs/satoshinet/indexer/indexer/contract"
	dkvs_indexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"

	"github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/wire"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/db"
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
		b.ConnectBlock(b.chaincfgParam.GenesisBlock, 0, 0)
	}

	b.startDKVSPruneTimer()
}

func (b *IndexerMgr) dkvsConfig() dkvs_indexer.Config {
	defaults := dkvs_indexer.NetworkDefaultsForParams(b.chaincfgParam)
	cfg := dkvs_indexer.Config{
		AllowFreeLocal: b.chaincfgParam.Name != chaincfg.MainNetParams.Name,
		FreeLocalCache: dkvs_indexer.DefaultFreeLocalCachePolicy(),
		CurrentHeight: func() uint64 {
			if b.compiling == nil {
				return 0
			}
			height := b.compiling.GetSyncHeight()
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
	if err != nil {
		go func() {
			n := 0
			var err error
			for n < 30 {
				tip, err = satsnet_rpc.InitSatsNetClient(
					cfg.Host, cfg.Port, cfg.User, cfg.Password, dbPath, cfg.EnableTls,
				)
				if err == nil {
					break
				}
				time.Sleep(1 * time.Second)
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
}

func (b *IndexerMgr) dbgc() {
	db.RunDBGC(b.localDB)
	db.RunDBGC(b.baseDB)
	db.RunDBGC(b.dkvsDB)
	common.Log.Infof("dbgc completed")
}

func (b *IndexerMgr) closeDB() {
	b.dbgc()

	b.baseDB.Close()
	b.localDB.Close()
	if b.dkvsDB != nil {
		b.dkvsDB.Close()
	}
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

func (b *IndexerMgr) forceUpdateDB() {
	//startTime := time.Now()

	if b.contractIndexer != nil {
		b.contractIndexer.UpdateDB()
	}
	//common.Log.Infof("IndexerMgr.forceUpdateDB: takes: %v", time.Since(startTime))
}

func (b *IndexerMgr) handleReorg(height int) {
	b.closeDB()
	b.Init()
	b.compiling.SetReorgHeight(height)
	common.Log.Infof("IndexerMgr handleReorg completed.")
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
	// The live compiling buffers must be trimmed before the backup writes to DB.
	// Subtract keeps only post-backup deltas in memory while the backup commits
	// the syncHeight view.
	b.cleanDBBuffer()
	b.compilingBackupDB.UpdateDB()
	if b.contractBackupDB != nil {
		b.contractBackupDB.UpdateDB()
	}
	b.compiling.SetSyncBase(b.compilingBackupDB.GetSyncBase())
}

func (b *IndexerMgr) prepareDBBuffer() {
	b.compilingBackupDB = b.compiling.Clone(true)
	if b.contractIndexer != nil {
		b.contractBackupDB = b.contractIndexer.Clone()
	}
	common.Log.Infof("backup instance %d cloned", b.compilingBackupDB.GetHeight())
}

func (b *IndexerMgr) cleanDBBuffer() {
	b.compiling.Subtract(b.compilingBackupDB)
	if b.contractIndexer != nil && b.contractBackupDB != nil {
		b.contractIndexer.Subtract(b.contractBackupDB)
	}
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

// tip: 本地最长链； block，新接收到的区块，一般会比tip高1
func (p *IndexerMgr) ConnectBlock(block *wire.MsgBlock, height, tip int) {
	p.connectMutex.Lock()
	defer p.connectMutex.Unlock()

	stepRun := false
	if stepRun {
		lastHeight := p.compiling.GetHeight()
		if lastHeight+1 < height && height > 1 {
			for i := lastHeight + 1; i <= height; i++ {
				tip2 := p.compiling.GetHeight() + 1
				err := p.compiling.SyncBlockWithHeight(i, tip2, false)
				if err != nil {
					common.Log.Errorf("SyncBlockWithHeight %d failed, %v", i, err)
					return
				}
				p.updateDB(height, tip2)

			}
			// 重新设置buffer
			p.prepareDBBuffer()
		}
	} else {
		lastHeight := p.compiling.GetHeight()
		common.Log.Infof("compiling height %d, block %d, tip %d", lastHeight, height, tip)
		if lastHeight+1 < height && height > 1 {
			// 节点区块数据已经同步，但是索引器重建，走这个流程
			for i := lastHeight + 1; i <= height; i++ {
				err := p.compiling.SyncBlockWithHeight(i, tip, true)
				if err != nil {
					common.Log.Errorf("SyncBlockWithHeight %d failed, %v", i, err)
					return
				}
				// 只更新
				// p.updateServiceInstance()
			}
			// 重新设置buffer
			p.prepareDBBuffer()
		}
	}

	if block != nil {
		err := p.compiling.SyncBlock(block, height, tip, false)
		if err != nil {
			common.Log.Errorf("ConnectBlock failed, %v", err)
			return
		}
	}

	// 聪网节点processBlock过程中，需要同步读取索引器数据，所以这里需要同步更新 rpcService
	// TODO 优化indexer的设计
	p.updateDB(height, tip)
	if (height+1)%200 == 0 { // TODO 先多检查，以后稳定了再降低检查频率
		p.checkSelf()
	}
}

func (p *IndexerMgr) DisconnectBlock(height int) {
	p.handleReorg(height)
}
