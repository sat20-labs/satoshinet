package indexer

import (
	"sync"
	"time"

	"github.com/sat20-labs/satoshinet/indexer/common"
	base_indexer "github.com/sat20-labs/satoshinet/indexer/indexer/base"

	"github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/wire"

	"github.com/dgraph-io/badger/v4"

	"github.com/sat20-labs/indexer/indexer/db"
)

type RPCConfig struct {
	Host     string 
	Port     int    
	User     string 
	Password string
	EnableTls bool
}

type Config struct {
	DataPath string
	RPCCfg   *RPCConfig
}

type IndexerMgr struct {
	
	cfg *Config
	dbDir string

	// data from blockchain
	baseDB *badger.DB

	// data from market
	localDB *badger.DB

	// 配置参数
	chaincfgParam   *chaincfg.Params
	maxIndexHeight  int
	periodFlushToDB int

	mutex sync.RWMutex
	// 跑数据
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

	chainParam := &chaincfg.SatsMainNetParams
	if bTestNet {
		chainParam = &chaincfg.SatsTestNetParams
	} else {
		chainParam = &chaincfg.SatsMainNetParams
	}

	mgr := &IndexerMgr{
		cfg:               cfg,
		dbDir:             cfg.DataPath+"/db/indexer/"+chainParam.Name+"/",
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
	b.compiling.Init(b.processBlock, b.forceUpdateDB)
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

func (b *IndexerMgr) GetBaseDB() *badger.DB {
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
	}
	
	return nil
}

func (b *IndexerMgr) Stop() {
	b.bRunning = false
}

func (b *IndexerMgr) StartDaemon(stopChan <-chan struct{}) {
	n := 10
	ticker := time.NewTicker(time.Duration(n) * time.Second)

	stopIndexerChan := make(chan struct{}, 1) // 非阻塞

	if b.repair() {
		common.Log.Infof("repaired, check again.")
		return
	}

	common.Log.Info("IndexerMgr running...")

	bWantExit := false
	isRunning := false
	disableSync := false
	tick := func() {
		if disableSync {
			return
		}
		if !isRunning {
			isRunning = true
			go func() {
				ret := b.compiling.SyncToChainTip(stopIndexerChan)
				if ret == 0 {
					if b.maxIndexHeight > 0 {
						if b.maxIndexHeight <= b.compiling.GetHeight() {
							b.checkSelf()
							common.Log.Infof("reach expected height, set exit flag")
							bWantExit = true
						}
					} else {
						b.updateDB()
						b.dbgc()
						// 每周定期检查数据 （目前主网一次检查需要半个小时-1个小时，需要考虑这个影响）
						// if b.lastCheckHeight != b.compiling.GetSyncHeight() {
						// 	period := 1000
						// 	if b.compiling.GetSyncHeight()%period == 0 {
						// 		b.lastCheckHeight = b.compiling.GetSyncHeight()
						// 		b.checkSelf()
						// 	}
						// }
						if b.dbStatistic() {
							bWantExit = true
						}
					}
				} else if ret > 0 {
					// handle reorg
					b.handleReorg(ret)
				} else {
					if ret == -1 {
						common.Log.Infof("IndexerMgr inner thread exit by SIGINT signal")
						bWantExit = true
					}
				}

				isRunning = false
			}()
		}
	}

	onConneted := func(height int32, header *wire.BlockHeader, txns []*btcutil.Tx) {
		tick()
	}

	for !satsnet_rpc.RpcClientReady() {
		time.Sleep(time.Second)
	}
	
	tick()
	satsnet_rpc.RegisterOnConnected(onConneted) // 主要靠这个

	for !bWantExit && b.bRunning {
		select {
		case <-ticker.C:
			if bWantExit {
				break
			}
			//tick()
		case <-stopChan:
			common.Log.Info("IndexerMgr got SIGINT")
			if bWantExit {
				break
			}
			if isRunning {
				select {
				case stopIndexerChan <- struct{}{}:
					// 成功发送
				default:
					// 通道已满或没有接收者，执行其他操作
				}
				for isRunning {
					time.Sleep(time.Second / 10)
				}
				common.Log.Info("IndexerMgr inner thread exited")
			}
			bWantExit = true
		}
	}

	ticker.Stop()

	// close all
	b.closeDB()

	common.Log.Info("IndexerMgr exited.")
}

func (b *IndexerMgr) dbgc() {
	db.RunBadgerGC(b.localDB)
	db.RunBadgerGC(b.baseDB)
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
	startTime := time.Now()

	common.Log.Infof("IndexerMgr.forceUpdateDB: takes: %v", time.Since(startTime))
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

	if b.compiling.GetHeight()-b.compiling.GetSyncHeight() < b.compiling.GetBlockHistory() {
		common.Log.Infof("updateDB do nothing at height %d-%d", b.compiling.GetHeight(), b.compiling.GetSyncHeight())
		return
	}

	if b.compiling.GetHeight()-b.compiling.GetSyncHeight() == b.compiling.GetBlockHistory() {
		// 先备份数据在缓存
		if b.compilingBackupDB == nil {
			b.prepareDBBuffer()
			common.Log.Infof("updateDB clone data at height %d-%d", b.compiling.GetHeight(), b.compiling.GetSyncHeight())
		}
		return
	}

	// 这个区间不备份数据
	if b.compiling.GetHeight()-b.compiling.GetSyncHeight() < 2*b.compiling.GetBlockHistory() {
		common.Log.Infof("updateDB do nothing at height %d-%d", b.compiling.GetHeight(), b.compiling.GetSyncHeight())
		return
	}

	// b.GetHeight()-b.GetSyncHeight() == 2*b.GetBlockHistory()

	// 到达双倍高度时，将备份的数据写入数据库中。
	if b.compilingBackupDB != nil {
		if b.compiling.GetHeight()-b.compilingBackupDB.GetHeight() < b.compiling.GetBlockHistory() {
			common.Log.Infof("updateDB do nothing at height %d, backup instance %d", b.compiling.GetHeight(), b.compilingBackupDB.GetHeight())
			return
		}
		common.Log.Infof("updateDB do backup->forceUpdateDB() at height %d-%d", b.compiling.GetHeight(), b.compiling.GetSyncHeight())
		b.performUpdateDBInBuffer()
	}
	b.prepareDBBuffer()
	common.Log.Infof("updateDB clone data at height %d-%d", b.compiling.GetHeight(), b.compiling.GetSyncHeight())
}

func (b *IndexerMgr) performUpdateDBInBuffer() {
	b.cleanDBBuffer() // must before UpdateDB
	b.compilingBackupDB.UpdateDB()
}

func (b *IndexerMgr) prepareDBBuffer() {
	b.compilingBackupDB = b.compiling.Clone()
	b.compiling.ResetBlockVector()

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
	//p.compiling.Repair()
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

func (p *IndexerMgr) ConnectBlock(block *wire.MsgBlock, height, tip int) {

	if p.compiling.GetHeight() + 1 < height && height > 1 {
		// 因为规避分叉问题，数据库数据高度不够，需要先同步到指定高度
		stopIndexerChan := make(chan struct{}, 1) // 非阻塞
		ret := p.compiling.SyncToBlock(height - 1, stopIndexerChan)
		if ret  == 0 {
			common.Log.Infof("sync to %d succeed", height-1)
		} else {
			common.Log.Errorf("sync to %d failed", height-1)
			// then ?
		}
	}

	if block != nil {
		err := p.compiling.SyncBlock(block, height, tip)
		if err != nil {
			common.Log.Errorf("ConnectBlock failed, %v", err)
			return 
		}
	}
	
	// 聪网节点processBlock过程中，需要同步读取索引器数据，所以这里需要同步更新 rpcService
	// TODO 优化indexer的设计
	p.updateDB()
	if height == tip {	
		p.dbgc()
	}
}

func (p *IndexerMgr) DisconnectBlock(height, tip int) {
	p.handleReorg(height)
}

