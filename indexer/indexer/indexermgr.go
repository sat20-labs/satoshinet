package indexer

import (
	"sync"
	"time"

	"github.com/sat20-labs/satoshinet/indexer/common"
	base_indexer "github.com/sat20-labs/satoshinet/indexer/indexer/base"

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
}

type IndexerMgr struct {
	cfg   *Config
	dbDir string

	// data from blockchain
	baseDB indexer.KVDB

	// data from market
	localDB indexer.KVDB

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
		periodFlushToDB:   12,
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

	if b.lastCheckHeight == -1 {
		b.ConnectBlock(b.chaincfgParam.GenesisBlock, 0, 0)
	}

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
					b.ConnectBlock(nil, tip+1, tip)
				}
			}
		}()
	} else {
		if b.compiling.GetSyncHeight() < tip {
			b.ConnectBlock(nil, tip+1, tip)
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
}

func (b *IndexerMgr) dbgc() {
	db.RunDBGC(b.localDB)
	db.RunDBGC(b.baseDB)
	common.Log.Infof("dbgc completed")
}

func (b *IndexerMgr) closeDB() {
	b.dbgc()

	b.baseDB.Close()
	b.localDB.Close()
}

func (b *IndexerMgr) checkSelf() {
	start := time.Now()
	b.compiling.CheckSelf()

	common.Log.Infof("IndexerMgr.checkSelf takes %v", time.Since(start))
}

func (b *IndexerMgr) forceUpdateDB() {
	//startTime := time.Now()

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
func (b *IndexerMgr) updateDB() {
	b.updateServiceInstance()

	complingHeight := b.compiling.GetHeight()
	syncHeight := b.compiling.GetSyncHeight()
	blocksInHistory := b.compiling.GetBlockHistory()

	gap := complingHeight-syncHeight
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
	b.cleanDBBuffer() // must before UpdateDB
	b.compilingBackupDB.UpdateDB()
	b.compiling.SetSyncBase(b.compilingBackupDB.GetSyncBase())
}

func (b *IndexerMgr) prepareDBBuffer() {
	b.compilingBackupDB = b.compiling.Clone()
	common.Log.Infof("backup instance %d cloned", b.compilingBackupDB.GetHeight())
}

func (b *IndexerMgr) cleanDBBuffer() {
	b.compiling.Subtract(b.compilingBackupDB)
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
	common.Log.Infof("compiling height %d, block %d, tip %d", p.compiling.GetHeight(), height, tip)
	if p.compiling.GetHeight()+1 < height && height > 1 {
		// 因为规避分叉问题，数据库数据高度不够，需要先同步到指定高度
		// stopIndexerChan := make(chan struct{}, 1) // 非阻塞
		// ret := p.compiling.SyncToBlock(height - 1, stopIndexerChan)
		// if ret  == 0 {
		// 	common.Log.Infof("sync to %d succeed", height-1)
		// } else {
		// 	common.Log.Errorf("sync to %d failed", height-1)
		// 	// then ?
		// }

		// 因为同步过程需要实时的corenode数据，所以一边同步一边clone
		for i := p.compiling.GetHeight() + 1; i < height; i++ {
			err := p.compiling.SyncBlockWithHeight(i, tip, true)
			if err != nil {
				common.Log.Errorf("SyncBlockWithHeight %d failed, %v", i, err)
				return
			}
			// 只更新
			p.updateServiceInstance()
		}
		// 重新设置buffer
		p.prepareDBBuffer()
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
	p.updateDB()
	//if height%1000 == 0 {  // TODO 先多检查，以后稳定了再降低检查频率
		p.checkSelf()
	//}
}

func (p *IndexerMgr) DisconnectBlock(height, tip int) {
	p.handleReorg(height)
}
