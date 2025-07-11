package base

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/stp"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"

	indexer "github.com/sat20-labs/indexer/common"
	db "github.com/sat20-labs/indexer/indexer/db"
)

type UtxoValue struct {
	Utxo    string
	Address *common.ScriptPubKey
	UtxoId  uint64
	Value   int64
}

type BlockProcCallback func(*common.Block)
type UpdateDBCallback func()

type BaseIndexer struct {
	db      *badger.DB
	stats   *SyncStats // 数据库状态

	// 需要clone的数据
	blockVector []*common.BlockValueInDB //
	utxoIndex   *common.UTXOIndex
	delUTXOs    []*UtxoValue // utxo->address,utxoid
	tickAddressMap map[string]map[string]*indexer.Decimal // ticker->addressId->amount，在某个更新周期中的缓存数据，非全量

	tickInfoMap        map[string]*common.TickerInfo
	addressValueMap    map[string]*indexer.AddressValueV2	// 每个区块处理之前填充所有需要的地址id
	coreNodeMap        map[string]*stp.CoreNodeInfo // pubkey, 不清空
	coreNodeMapUpdated bool
	channelMap         map[string]*common.ChannelInfo // address, 不清空

	lastHeight       int // 内存数据同步区块
	lastHash         string
	prevBlockHashMap map[int]string // 记录过去6个区块hash，判断哪个区块分叉
	////////////

	blocksChan chan *common.Block

	// 配置参数
	periodFlushToDB  int
	keepBlockHistory int
	chaincfgParam    *chaincfg.Params
	maxIndexHeight   int

	blockprocCB BlockProcCallback
	updateDBCB  UpdateDBCallback
}

const BLOCK_PREFETCH = 12

func NewBaseIndexer(
	basicDB *badger.DB,
	chaincfgParam *chaincfg.Params,
	maxIndexHeight int,
	periodFlushToDB int,
) *BaseIndexer {
	indexer := &BaseIndexer{
		db:               basicDB,
		stats:            &SyncStats{},
		periodFlushToDB:  periodFlushToDB,
		keepBlockHistory: 12,
		blocksChan:       make(chan *common.Block, BLOCK_PREFETCH),
		chaincfgParam:    chaincfgParam,
		maxIndexHeight:   maxIndexHeight,
	}

	return indexer
}

func (b *BaseIndexer) Init(cb1 BlockProcCallback, cb2 UpdateDBCallback) {
	dbver := b.GetBaseDBVer()
	common.Log.Infof("base db version: %s", b.GetBaseDBVer())
	if dbver != "" && dbver != common.BASE_DB_VERSION {
		common.Log.Panicf("DB version inconsistent. DB ver %s, but code base %s", dbver, common.BASE_DB_VERSION)
	}

	b.blockprocCB = cb1
	b.updateDBCB = cb2

	b.reset()

	b.coreNodeMap = stp.GetAllCoreNodeFromDB(b.db)
	b.channelMap = stp.GetAllChannelFromDB(b.db)
}

func (b *BaseIndexer) reset() {
	b.loadSyncStatsFromDB()

	b.blocksChan = make(chan *common.Block, BLOCK_PREFETCH)

	b.blockVector = make([]*common.BlockValueInDB, 0)
	b.utxoIndex = common.NewUTXOIndex()
	b.delUTXOs = make([]*UtxoValue, 0)

	b.tickAddressMap = make(map[string]map[string]*indexer.Decimal)
	b.tickInfoMap = make(map[string]*common.TickerInfo)
	b.addressValueMap = make(map[string]*indexer.AddressValueV2)
	b.coreNodeMap = make(map[string]*stp.CoreNodeInfo)
	b.channelMap = make(map[string]*common.ChannelInfo)
	b.prevBlockHashMap = make(map[int]string)
}

// 只保存UpdateDB需要用的数据
func (b *BaseIndexer) Clone() *BaseIndexer {
	startTime := time.Now()
	newInst := NewBaseIndexer(b.db, b.chaincfgParam, b.maxIndexHeight, b.periodFlushToDB)

	newInst.utxoIndex = common.NewUTXOIndex()
	for key, value := range b.utxoIndex.Index {
		newInst.utxoIndex.Index[key] = value
	}
	for _, value := range b.delUTXOs {
		delete(newInst.utxoIndex.Index, value.Utxo)
	}
	newInst.delUTXOs = make([]*UtxoValue, len(b.delUTXOs))
	copy(newInst.delUTXOs, b.delUTXOs)
	
	for key, value := range b.utxoIndex.AscendMap {
		newInst.utxoIndex.AscendMap[key] = value
	}
	for key, value := range b.utxoIndex.DescendMap {
		newInst.utxoIndex.DescendMap[key] = value
	}

	newInst.tickAddressMap = make(map[string]map[string]*indexer.Decimal)
	for k, v := range b.tickAddressMap {
		addrmap := make(map[string]*indexer.Decimal)
		for id, amt := range v {
			addrmap[id] = amt.Clone()
		}
		newInst.tickAddressMap[k] = addrmap
	}


	newInst.tickInfoMap = make(map[string]*common.TickerInfo)
	for k, v := range b.tickInfoMap {
		newInst.tickInfoMap[k] = v
	}

	newInst.coreNodeMap = make(map[string]*stp.CoreNodeInfo)
	for k, v := range b.coreNodeMap {
		node := stp.CoreNodeInfo{
			AscendHeight: v.AscendHeight,
			DescendHeight: v.DescendHeight,
			ChildMiners: make(map[string]int),
		}
		for k2, v2 := range v.ChildMiners {
			node.ChildMiners[k2] = v2
		}
		newInst.coreNodeMap[k] = &node
	}

	newInst.channelMap = make(map[string]*common.ChannelInfo)
	for k, v := range b.channelMap {
		newInst.channelMap[k] = v
	}

	newInst.addressValueMap = make(map[string]*indexer.AddressValueV2)
	for key, value := range b.addressValueMap {
		n := indexer.AddressValueV2{
			AddressType: value.AddressType,
			AddressId: value.AddressId,
			Op: value.Op,
			Utxos: make(map[uint64]bool),
		}
		for id, v := range value.Utxos {
			n.Utxos[id] =v
		}
		newInst.addressValueMap[key] = &n
	}
	newInst.blockVector = make([]*common.BlockValueInDB, len(b.blockVector))
	copy(newInst.blockVector, b.blockVector)

	newInst.lastHash = b.lastHash
	newInst.lastHeight = b.lastHeight
	newInst.stats = b.stats.Clone()
	newInst.blockprocCB = b.blockprocCB
	newInst.updateDBCB = b.updateDBCB

	common.Log.Infof("BaseIndexer->clone takes %v", time.Since(startTime))

	return newInst
}

func (b *BaseIndexer) Subtract(another *BaseIndexer) {
	// 将已经备份到数据库的数据删除，防止内存中数据增长过快
	for key := range another.utxoIndex.Index {
		delete(b.utxoIndex.Index, key)
	}
	for _, del := range another.delUTXOs {
		delete(b.utxoIndex.Index, del.Utxo)
	}

	l := len(another.delUTXOs)
	b.delUTXOs = b.delUTXOs[l:]

	// 统计量不需要更新
	// for k, v := range another.tickerAddressMap {
	// }
}


func (b *BaseIndexer) Repair() {
	// update tick info 
	// tickerMap := make(map[string]*common.TickerInfo, 0)
	// b.db.View(func(txn *badger.Txn) error {
	// 	// 设置前缀扫描选项
	// 	prefixBytes := []byte(stp.DB_KEY_ASCEND)
	// 	prefixOptions := badger.DefaultIteratorOptions
	// 	prefixOptions.Prefix = prefixBytes

	// 	// 使用前缀扫描选项创建迭代器
	// 	it := txn.NewIterator(prefixOptions)
	// 	defer it.Close()

	// 	// 遍历匹配前缀的key
	// 	for it.Seek(prefixBytes); it.ValidForPrefix(prefixBytes); it.Next() {
	// 		item := it.Item()
	// 		if item.IsDeletedOrExpired() {
	// 			continue
	// 		}
	// 		key := string(item.Key())
	// 		if strings.Count(key, ":") < 2 {
	// 			continue
	// 		}

	// 		var info common.TickerInfo
	// 		value, err := item.ValueCopy(nil)
	// 		if err != nil {
	// 			common.Log.Errorln("ValueCopy " + key + " " + err.Error())
	// 		} else {
	// 			err = db.DecodeBytes(value, &info)
	// 			if err == nil {
	// 				tickerMap[info.String()] = &info
	// 			} else {
	// 				common.Log.Errorln("DecodeBytes " + err.Error())
	// 			}
	// 		}
	// 	}
	// 	return nil
	// })

	// if len(tickerMap) == 0 {
	// 	return
	// }

	// wb := b.db.NewWriteBatch()
	// defer wb.Cancel()

	// for k, v := range tickerMap {
	// 	key := stp.GetTickerInfoDBKey(k)
	// 	err := db.SetDB([]byte(key), v, wb)
	// 	if err != nil {
	// 		common.Log.Panicf("Error setting in db %v", err)
	// 	}
	// }
	// err := wb.Flush()
	// if err != nil {
	// 	common.Log.Panicf("BaseIndexer.updateBasicDB-> Error satwb flushing writes to db %v", err)
	// }


}

// only call in compiling data
func (b *BaseIndexer) forceUpdateDB() {
	startTime := time.Now()
	b.UpdateDB()
	common.Log.Infof("BaseIndexer.updateBasicDB: cost: %v", time.Since(startTime))

	// startTime = time.Now()
	b.updateDBCB()
	// common.Log.Infof("BaseIndexer.updateOrdxDB: cost: %v", time.Since(startTime))

	common.Log.Infof("forceUpdateDB sync to height %d", b.stats.SyncHeight)
}

func (b *BaseIndexer) closeDB() {
	err := b.db.Close()
	if err != nil {
		common.Log.Errorf("BaseIndexer.closeDB-> Error closing sat db %v", err)
	}
}

func (b *BaseIndexer) UpdateDB() {
	common.Log.Infof("BaseIndexer->updateBasicDB %d start...", b.lastHeight)

	// 拿到所有的addressId
	// addressValueMap := b.prefechAddress()

	wb := b.db.NewWriteBatch()
	defer wb.Cancel()

	totalAscendSats := int64(0) // 穿越到聪网的聪
	AllUtxoAdded := uint64(0)
	for _, value := range b.blockVector {
		key := db.GetBlockDBKey(value.Height)
		err := db.SetDB(key, value, wb)
		if err != nil {
			common.Log.Panicf("Error setting in db %v", err)
		}
		totalAscendSats += value.OutputSats - value.InputSats
		AllUtxoAdded += uint64(value.OutputUtxo)
	}

	// 所有的地址都保存起来，数据太多。只保存nft相关的地址。
	// TODO 需要先询问nft模块有哪些地址需要保存
	// for k, v := range b.addressIdMap {
	// 	if v.Op > 0 {
	// 		err := common.BindAddressDBKeyToId(k, v.AddressId, wb)
	// 		if err != nil {
	// 			common.Log.Panicf("Error setting in db %v", err)
	// 		}
	// 	}
	// }

	//startTime := time.Now()
	// Add the new utxos first
	utxoAdded := 0
	satsAdded := int64(0)
	utxoSkipped := 0
	totalDescendSats := int64(0)
	for k, v := range b.utxoIndex.Index {
		//if len(v.Ordinals) == 0 {
		// 有些没有聪，一样可以花费，比如1025ca72299155eb5c2ef6c1918e7dfbdcffd04b0d13792e9773af72b827d28a:1 （testnet）
		// 这样的utxo需要保存起来
		//}
		// v.Address.Type == (txscript.NonStandardTy) 这样的utxo需要被记录下来，虽然地址是nil，ordinals也是nil
		// 比如：21e48796d17bcab49b1fea7211199c0fa1e296d2ecf4cf2f900cee62153ee331的所有输出 （testnet）
		if v.Address.Type == int(txscript.NullDataTy) || v.Address.Type == int(txscript.NonStandardTy) {
			// 只有无资产才不记录
			if v.Value == 0 && len(v.Assets) == 0 {
				utxoSkipped++
				continue
			} else {
				// e362e21ff1d2ef78379d401d89b42ce3e0ce3e245f74b1f4cb624a8baa5d53ad:0 testnet
				common.Log.Infof("the OP_RETURN has %d sats in %s", v.Value, k)
				totalDescendSats += v.Value
			}
		}
		key := db.GetUTXODBKey(k)
		utxoId := common.GetUtxoId(v)

		addressIds := make([]uint64, 0)
		for _, address := range v.Address.Addresses {
			// addrvalue := addressValueMap[address]
			// addressIds = append(addressIds, addrvalue.AddressId)
			// addrkey := db.GetAddressValueDBKey(addrvalue.AddressId, utxoId, int(v.Address.Type), i)
			// err := db.SetRawDB(addrkey, indexer.Uint64ToBytes(uint64(v.Value)), wb)
			// if err != nil {
			// 	common.Log.Panicf("Error setting in db %v", err)
			// }
			// if addrvalue.Op > 0 {
			// 	err = db.BindAddressDBKeyToId(address, addrvalue.AddressId, wb)
			// 	if err != nil {
			// 		common.Log.Panicf("Error setting in db %v", err)
			// 	}
			// }

			addrvalue := b.addressValueMap[address]
			addressIds = append(addressIds, addrvalue.AddressId)
		}

		saveUTXO := &common.UtxoValueInDB{
			UtxoId:      utxoId,
			Value:       v.Value,
			AddressType: uint16(v.Address.Type),
			ReqSig:      uint16(v.Address.ReqSig),
			AddressIds:  addressIds,
			Assets:      v.Assets,
		}

		err := db.SetDB(key, saveUTXO, wb)
		if err != nil {
			common.Log.Panicf("Error setting in db %v", err)
		}
		err = db.BindUtxoDBKeyToId(key, saveUTXO.UtxoId, wb)
		if err != nil {
			common.Log.Panicf("Error setting in db %v", err)
		}
		utxoAdded++
		satsAdded += v.Value
	}
	//common.Log.Infof("BaseIndexer.updateBasicDB-> add utxos %d (+ %d), cost: %v", utxoAdded, utxoSkipped, time.Since(startTime))

	// 很多要删除的utxo，其实还没有保存到数据库
	//startTime = time.Now()
	utxoDeled := 0
	for _, value := range b.delUTXOs {

		utxoDeled++
		key := db.GetUTXODBKey(value.Utxo)
		err := wb.Delete([]byte(key))
		if err != nil {
			common.Log.Errorf("BaseIndexer.updateBasicDB-> Error deleting db: %v\n", err)
		}
		err = db.UnBindUtxoId(value.UtxoId, wb)
		if err != nil {
			common.Log.Errorf("BaseIndexer.updateBasicDB-> Error deleting db: %v\n", err)
		}

		// for i, address := range value.Address.Addresses {
		// 	addrvalue, ok := addressValueMap[address]
		// 	if ok {
		// 		addrkey := db.GetAddressValueDBKey(addrvalue.AddressId, value.UtxoId, int(value.Address.Type), i)
		// 		err := wb.Delete(addrkey)
		// 		if err != nil {
		// 			common.Log.Errorf("BaseIndexer.updateBasicDB-> Error deleting db: %v\n", err)
		// 		}
		// 	} else {
		// 		// 不存在
		// 		//common.Log.Infof("address %s not exists", value.Address)
		// 	}
		// }

	}
	//common.Log.Infof("BaseIndexer.updateBasicDB-> delete utxos %d, cost: %v", utxoDeled, time.Since(startTime))

	// address -> utxo
	for k, v := range b.addressValueMap {
		if v.AddressType == uint32(txscript.NullDataTy) || 
		v.AddressType == uint32(txscript.NonStandardTy) {
			// 这两个地址的数据会越来越大，以后考虑分桶保存，再考虑保存这两个地址的utxo
			continue
		}
		key := db.GetAddressDBKeyV2(k)
		value := v.ToAddressValueInDBV2()
		err := db.SetDB(key, value, wb)
		if err != nil {
			common.Log.Panicf("Error setting in db %v", err)
		}
		if v.Op == 1 {
			err = db.BindAddressDBKeyToId(k, v.AddressId, wb)
			if err != nil {
				common.Log.Panicf("Error setting in db %v", err)
			}
		}
	}

	for _, ascend := range b.utxoIndex.AscendMap {
		key := stp.GetAscendDBKey(ascend.FundingUtxo)
		err := db.SetDB([]byte(key), ascend, wb)
		if err != nil {
			common.Log.Panicf("Error setting in db %v", err)
		}
	}

	for _, descend := range b.utxoIndex.DescendMap {
		key := stp.GetDescendDBKey(descend.NullDataUtxo)
		err := db.SetDB([]byte(key), descend, wb)
		if err != nil {
			common.Log.Panicf("Error setting in db %v", err)
		}
	}

	if b.coreNodeMapUpdated {
		key := stp.GetAllCoreNodeDBKey()
		err := db.SetDB([]byte(key), b.coreNodeMap, wb)
		if err != nil {
			common.Log.Panicf("Error setting in db %v", err)
		}
		b.coreNodeMapUpdated = false
	}

	for k, v := range b.channelMap {
		if v.IsNew {
			key := stp.GetChannelDBKey(k)
			err := db.SetDB([]byte(key), &v.ChannelInfoInDB, wb)
			if err != nil {
				common.Log.Panicf("Error setting in db %v", err)
			}
			v.IsNew = false
		}
	}

	for k, v := range b.tickInfoMap {
		key := stp.GetTickerInfoDBKey(k)
		err := db.SetDB([]byte(key), v, wb)
		if err != nil {
			common.Log.Panicf("Error setting in db %v", err)
		}
	}
	// ticker -> holders (partially)
	for ticker, addrmap := range b.tickAddressMap {
		for k, v := range addrmap {
			addrStatus, ok := b.addressValueMap[k]
			if !ok {
				common.Log.Panicf("can't find id of address %s", k)
			}
			key := stp.GetHolderInfoDBKey(ticker, addrStatus.AddressId)
			err := db.SetDB(key, v.ToFormatString(), wb)
			if err != nil {
				common.Log.Panicf("Error setting in db %v", err)
			}
		}
	}

	b.stats.AscendCount += len(b.utxoIndex.AscendMap)
	b.stats.DescendCount += len(b.utxoIndex.DescendMap)
	b.stats.UtxoCount += uint64(utxoAdded)
	b.stats.UtxoCount -= uint64(utxoDeled)
	b.stats.AllUtxoCount += AllUtxoAdded
	b.stats.TotalAscendSats += totalAscendSats
	b.stats.TotalDescendSats += totalDescendSats
	b.stats.SyncBlockHash = b.lastHash
	b.stats.SyncHeight = b.lastHeight
	err := db.SetDB([]byte(SyncStatsKey), b.stats, wb)
	if err != nil {
		common.Log.Panicf("BaseIndexer.updateBasicDB-> Error setting in db %v", err)
	}

	//startTime = time.Now()
	err = wb.Flush()
	if err != nil {
		common.Log.Panicf("BaseIndexer.updateBasicDB-> Error satwb flushing writes to db %v", err)
	}
	//common.Log.Infof("BaseIndexer.updateBasicDB-> flush db,  cost: %v", time.Since(startTime))

	// reset memory buffer
	b.blockVector = make([]*common.BlockValueInDB, 0)
	b.utxoIndex = common.NewUTXOIndex()
	b.delUTXOs = make([]*UtxoValue, 0)
	b.addressValueMap = make(map[string]*indexer.AddressValueV2)
	b.tickInfoMap = make(map[string]*common.TickerInfo)
	b.tickAddressMap = make(map[string]map[string]*indexer.Decimal)

	// if !b.CheckSelf() {
	// 	common.Log.Panicf("BaseIndexer.CheckSelf failed")
	// }
}

func (b *BaseIndexer) forceMajeure() {
	common.Log.Info("Graceful shutdown received, flushing db...")

	b.closeDB()
}

func (b *BaseIndexer) handleReorg(currentBlock *common.Block) int {
	common.Log.Warnf("BaseIndexer.handleReorg-> reorg detected at heigh %d", currentBlock.Height)

	// clean memory and reload stats from DB
	// b.reset()
	//b.stats.ReorgsDetected = append(b.stats.ReorgsDetected, currentBlock.Height)
	b.drainBlocksChan()

	reorgHeight := currentBlock.Height
	for i := b.lastHeight - 6; i <= b.lastHeight; i++ {
		blockHash, ok := b.prevBlockHashMap[i]
		if ok {
			hash, err := getBlockHash(uint64(i))
			if err == nil {
				if hash.String() != blockHash {
					common.Log.Warnf("Detected reorg at height %d", i)
					reorgHeight = i
				}
			}
		}
	}
	b.prevBlockHashMap = make(map[int]string)
	return reorgHeight
}

// SyncToBlock continues from the sync height to the current height
func (b *BaseIndexer) SyncToBlock(height int, stopChan chan struct{}) int {
	if b.lastHeight == height {
		//common.Log.Infof("BaseIndexer.SyncToBlock-> already synced to block %d", height)
		return 0
	}

	common.Log.Infof("BaseIndexer.SyncToBlock-> currentHeight %d, targetHeight %d", b.lastHeight, height)

	// if we don't start from precisely this heigh the UTXO index is worthless
	// we need to start from exactly where we left off
	start := b.lastHeight + 1

	periodProcessedTxs := 0
	startTime := time.Now() // Record the start time

	logProgressPeriod := 1

	stopBlockFetcherChan := make(chan struct{})
	go b.spawnBlockFetcher(start, height, stopBlockFetcherChan)

	for i := start; i <= height; i++ {
		if b.maxIndexHeight > 0 && b.lastHeight >= b.maxIndexHeight {
			b.forceUpdateDB()
			break
		}

		select {
		case <-stopChan:
			b.forceMajeure()
			return -1
		default:
			block := <-b.blocksChan

			if block == nil {
				common.Log.Errorf("BaseIndexer.SyncToBlock-> fetch block failed %d", i)
				return -2
			}
			//common.Log.Infof("BaseIndexer.SyncToBlock-> get block: cost: %v", time.Since(startTime))

			ret := b.syncBlock(block, height, true)
			if ret != 0 {
				stopBlockFetcherChan <- struct{}{}
				return ret
			}

			if i%logProgressPeriod == 0 {
				periodProcessedTxs += len(block.Transactions)
				elapsedTime := time.Since(startTime)
				timePerTx := elapsedTime / time.Duration(periodProcessedTxs)
				readableTime := block.Timestamp.Format("2006-01-02 15:04:05")
				common.Log.Infof("processed block %d (%s) with %d transactions took %v (%v per tx)\n", block.Height, readableTime, periodProcessedTxs, elapsedTime, timePerTx)
				startTime = time.Now()
				periodProcessedTxs = 0
			}
			//common.Log.Info("")
		}
	}

	//b.forceUpdateDB()

	common.Log.Infof("BaseIndexer.SyncToBlock-> already synced to block %d-%d\n", b.lastHeight, b.stats.SyncHeight)
	return 0
}


// sync
func (b *BaseIndexer) syncBlock(block *common.Block, tip int, updateDB bool) int {
	common.Log.Infof("BaseIndexer.syncBlock-> currentHeight %d, blockHeight %d", b.lastHeight, block.Height)

	if block.Height != b.lastHeight + 1 {
		common.Log.Warningf("BaseIndexer.syncBlock-> expected block height %d, got %d", b.lastHeight + 1, block.Height)
		return -1
	}

	// detect reorgs
	if b.lastHash != "" && block.PrevBlockHash != b.lastHash {
		common.Log.Warningf("BaseIndexer.syncBlock-> height %d reorg detected", block.Height)
		return b.handleReorg(block)
	}

	//localStartTime := time.Now()
	b.prefetchIndexesFromDB(block)
	//common.Log.Infof("BaseIndexer.syncBlock-> prefetchIndexesFromDB: cost: %v", time.Since(localStartTime))
	//localStartTime = time.Now()
	b.processBlock(block)
	//common.Log.Infof("BaseIndexer.syncBlock-> assignOrdinals: cost: %v", time.Since(localStartTime))

	// Update the sync stats
	b.stats.ChainTip = tip
	b.lastHeight = block.Height
	b.lastHash = block.Hash
	b.prevBlockHashMap[b.lastHeight] = b.lastHash
	if len(b.prevBlockHashMap) > b.keepBlockHistory {
		delete(b.prevBlockHashMap, b.lastHeight-b.keepBlockHistory)
	}

	//localStartTime = time.Now()
	b.blockprocCB(block)
	//common.Log.Infof("BaseIndexer.syncBlock-> blockproc: cost: %v", time.Since(localStartTime))

	if updateDB {
		if (block.Height%b.periodFlushToDB == 0 && tip-block.Height > b.keepBlockHistory) ||
		tip-block.Height == b.keepBlockHistory {
			//localStartTime = time.Now()
			b.forceUpdateDB()
			//common.Log.Infof("BaseIndexer.syncBlock-> forceUpdateDB: cost: %v", time.Since(localStartTime))
		}
	}

	return 0
}

func (b *BaseIndexer) GetTickerInfo(ticker *wire.AssetName) *common.TickerInfo {

	info, ok := b.tickInfoMap[ticker.String()]
	if ok {
		return info
	}

	info, err := stp.GetTickerInfoFromDB(b.db, ticker.String())
	if err != nil {
		common.Log.Errorf("GetTickerInfoFromDB %s failed, %v", ticker, err)
		return nil
	}

	b.tickInfoMap[ticker.String()] = info

	return info
}

// satoshinet 只需要保存utxo即可
// 所有聪都来自锚定交易，也就是闪电网络通道
func (b *BaseIndexer) processBlock(block *common.Block) {
	blockValue := &common.BlockValueInDB{Height: block.Height,
		Timestamp: block.Timestamp.Unix(),
		TxAmount:  len(block.Transactions),
	}
	// firstblock := block.Height
	// if len(b.blockVector) > 0 {
	// 	firstblock = b.blockVector[0].Height
	// }

	addedUtxoCount := 0
	deledUtxoCount := 0

	satsInput := int64(0)
	satsOutput := int64(0)
	for txIndex, tx := range block.Transactions {

		if !b.IsMainnet() {
			// 聪网处理anchorTx的一个bug导致AnchorTx出现多次
			if block.Height == 45 && tx.Txid == "a422e1009d59ea5b5c897e1671776a4a528935d40907411aa71173b255f3ae2e" {
				continue
			}
			if block.Height == 426 && tx.Txid == "3808a55f28802bda98cd077c8c530a519919225fa9299ca378b7414552c82442" {
				continue
			}
			if (block.Height == 1464 || block.Height == 1466) && tx.Txid == "95f057e23551d222736b30b34453481211d763c062631260fabe904394bef798" {
				continue
			}
			u := indexer.GetUtxo(block.Height, tx.Txid, 0)
			_, ok := b.utxoIndex.Index[u] 
			if ok {
				common.Log.Infof("DDDDDDD: %s", tx.Txid)
				continue
			}
		}


		
		var ascend *common.AscendData
		for i, input := range tx.Inputs {
			if uint32(input.Vout) == wire.MaxTxInSequenceNum { // coinbase
				continue
			}
			if uint32(input.Vout) == wire.AnchorTxOutIndex { // transcend
				var err error
				ascend, err = GenAscendFromAnchorPkScript(input.SignatureScript, b.chaincfgParam)
				if err != nil {
					common.Log.Errorf("GenAscendFromAnchorPkScript %s input %d failed. %v", tx.Txid, i, err)
					continue
				}
				ascend.Height = block.Height
				ascend.AnchorTxId = tx.Txid
				b.utxoIndex.AscendMap[ascend.FundingUtxo] = ascend

				coreNodeKey := hex.EncodeToString(ascend.PubB)
				_, ok := b.coreNodeMap[coreNodeKey]
				if !ok {
					if b.IsCoreNodeAscend(ascend) {
						// 新增加一个core node
						b.coreNodeMap[hex.EncodeToString(ascend.PubB)] = stp.NewCoreNodeInfo(ascend.Height)
						b.coreNodeMapUpdated = true
						common.Log.Infof("BaseIndexer.processBlock-> add core node %s at height %d", coreNodeKey, ascend.Height)
					} else {
						coreNode, ok := b.coreNodeMap[hex.EncodeToString(ascend.PubA)]
						if ok && b.HasMinerEligibility(ascend.Assets) {
							// 一个连接到corenode的普通miner
							coreNode.ChildMiners[hex.EncodeToString(ascend.PubB)] = ascend.Height
							common.Log.Infof("BaseIndexer.processBlock-> add miner node %s at height %d", hex.EncodeToString(ascend.PubB), ascend.Height)
						} else {
							// 无效的脚本
							common.Log.Infof("not miner ascending tx %s, utxo: %s, %v", tx.Txid, ascend.FundingUtxo, ascend.Assets)
							continue
						}
					}
				}

				// 仅仅是通道地址，有可能是合约控制
				_, ok = b.channelMap[ascend.Address]
				if !ok {
					b.channelMap[ascend.Address] = &common.ChannelInfo{
						ChannelInfoInDB: common.ChannelInfoInDB{
							Address: ascend.Address,
							PubA:    ascend.PubA,
							PubB:    ascend.PubB,
						},
						IsNew: true,
					}
					common.Log.Infof("BaseIndexer.processBlock-> add channel %s", ascend.Address)
				}

				continue
			}

			// the utxo to be spent in the format txid:vout
			utxoKey := indexer.GetUtxo(block.Height, input.Txid, int(input.Vout))

			// delete the utxo from the utxo index
			inputUtxo, ok := b.utxoIndex.Index[utxoKey]
			if !ok {
				common.Log.Panicf("%s does not exist in the utxo index", utxoKey)
			}
			deledUtxoCount++
			delete(b.utxoIndex.Index, utxoKey)
			utxoid := common.GetUtxoId(inputUtxo)
			//if inputUtxo.Height < firstblock {
				value := &UtxoValue{Utxo: utxoKey, Address: inputUtxo.Address,
					UtxoId: utxoid, Value: inputUtxo.Value}
				b.delUTXOs = append(b.delUTXOs, value)
			//}
			satsInput += inputUtxo.Value

			input.Address = inputUtxo.Address
			input.Assets = inputUtxo.Assets
			input.UtxoId = utxoid
			input.Value = inputUtxo.Value
			b.inputUtxo(inputUtxo)
		}

		for i, output := range tx.Outputs {
			if txIndex != 0 && common.IsOpReturn(output.Address.PkScript) {
				ctype, data, err := common.ReadDataFromNullDataScript(output.Address.PkScript)
				if err == nil {
					switch ctype {
					case common.CONTENT_TYPE_DESCENDING:
						descend, err := GenDescend(tx, i, string(data))
						if err == nil {
							descend.Height = block.Height
							b.utxoIndex.DescendMap[descend.NullDataUtxo] = descend

							var bindingSatNum int64
							if len(descend.Assets) > 0 {
								bindingSatNum = descend.Assets.GetBindingSatAmout()
								for _, asset := range descend.Assets {
									ticker := b.GetTickerInfo(&asset.Name)
									if ticker == nil {
										common.Log.Panicf("GetTickerInfo %s failed", asset.Name.String())
									}
									ticker.TotalDescendAmt = ticker.TotalDescendAmt.Add(&asset.Amount)
									if ticker.TotalDescendAmt.Cmp(ticker.TotalAscendAmt) > 0 {
										common.Log.Panicf("asset %s invalid amt: ascend %s, but descend %s", 
											asset.Name.String(), ticker.TotalAscendAmt.String(), ticker.TotalDescendAmt.String())
									}
								}
							} 
							value := descend.Value - bindingSatNum
							if value > 0 {
								ticker := b.GetTickerInfo(&indexer.ASSET_PLAIN_SAT)
								if ticker == nil {
									common.Log.Panicf("GetTickerInfo %s failed", indexer.ASSET_PLAIN_SAT.String())
								}
								ticker.TotalDescendAmt = ticker.TotalDescendAmt.Add(indexer.NewDefaultDecimal(value))
								if ticker.TotalDescendAmt.Cmp(ticker.TotalAscendAmt) > 0 {
									common.Log.Panicf("sats invalid amt: ascend %s, but descend %s", 
										ticker.TotalAscendAmt.String(), ticker.TotalDescendAmt.String())
								}
							}

							// TODO
							// 需要检查通道中是否还有足够的资产，才能确定是否是corenode退出，现在不支持corenode退出
							// if b.IsCoreNodeDescend(descend) {
							// 	channel, ok := b.channelMap[descend.Address]
							// 	if ok {
							// 		delete(b.coreNodeMap, hex.EncodeToString(channel.PubB))
							// 		b.coreNodeMapUpdated = true
							// 	}
							// }
						} else {
							common.Log.Errorf("GenDescend %s:%d failed, %v", tx.Txid, i, err)
						}
					case common.CONTENT_TYPE_ASCENDING:
						tickerInfo, err := common.GenTickerInfo(data)
						if err == nil {
							if ascend != nil {
								if len(ascend.Assets) != 0 {
									tickerInfo.TotalAscendAmt = ascend.Assets[0].Amount.Clone()
								} else {
									tickerInfo.TotalAscendAmt = indexer.NewDecimal(ascend.Value, 0)
								}
							}
							existingTicker := b.GetTickerInfo(&tickerInfo.AssetName)
							if existingTicker != nil {
								existingTicker.TotalAscendAmt = existingTicker.TotalAscendAmt.Add(tickerInfo.TotalAscendAmt)
							} else {
								b.tickInfoMap[tickerInfo.AssetName.String()] = tickerInfo
							}
						} else {
							common.Log.Errorf("GenTickerInfo %s:%d failed, %v",tx.Txid, i, err)
						}
					}
				} else {
					common.Log.Errorf("ReadDataFromNullDataScript %s:%d failed, %v", tx.Txid, i, err)
				}
			}

			u := indexer.GetUtxo(block.Height, tx.Txid, int(output.N))
			b.utxoIndex.Index[u] = output
			addedUtxoCount++
			satsOutput += output.Value	// 包括op_return的聪
			b.outputUtxo(output)
		}
	}

	blockValue.InputUtxo = deledUtxoCount
	blockValue.OutputUtxo = addedUtxoCount
	blockValue.InputSats = satsInput
	blockValue.OutputSats = satsOutput

	b.blockVector = append(b.blockVector, blockValue)
}

func (b *BaseIndexer) inputUtxo(input *common.Output) {	
	for _, asset := range input.Assets {
		for _, address := range input.Address.Addresses {
			name := asset.Name.String()
			addrmap, ok := b.tickAddressMap[name]
			if !ok {
				// 不可能走到这里
				addrmap = make(map[string]*indexer.Decimal)
				b.tickAddressMap[name] = addrmap
			}
			addrmap[address] = indexer.DecimalSub(addrmap[address], &asset.Amount)
			if addrmap[address].Sign() < 0 {
				common.Log.Panicf("%s asset %s incorrect in %d:%d:%d", address, name, input.Height, input.TxId, input.N)
			}
		}
	}
	plainSats := input.Value
	if len(input.Assets) > 0 {
		plainSats -= input.Assets.GetBindingSatAmout()
	}
	if plainSats > 0 {
		for _, address := range input.Address.Addresses {
			name := indexer.ASSET_PLAIN_SAT.String()
			addrmap, ok := b.tickAddressMap[name]
			if !ok {
				// 不可能走到这里
				addrmap = make(map[string]*indexer.Decimal)
				b.tickAddressMap[name] = addrmap
			}
			addrmap[address] = indexer.DecimalSub(addrmap[address], indexer.NewDefaultDecimal(plainSats))
			if addrmap[address].Sign() < 0 {
				common.Log.Panicf("%s sats incorrect in %d:%d:%d", address, input.Height, input.TxId, input.N)
			}
		}
	}

	utxoId := indexer.ToUtxoId(input.Height, input.TxId, int(input.N))
	for _, address := range input.Address.Addresses {
		utxomap, ok := b.addressValueMap[address]
		if ok {
			delete(utxomap.Utxos, utxoId)
		} else {
			common.Log.Panicf("%s should be loaded before", address)
		}
	}
}

func (b *BaseIndexer) outputUtxo(output *common.Output) {
	for _, asset := range output.Assets {
		for _, address := range output.Address.Addresses {
			addrmap, ok := b.tickAddressMap[asset.Name.String()]
			if !ok {
				addrmap = make(map[string]*indexer.Decimal)
				b.tickAddressMap[asset.Name.String()] = addrmap
			}
			addrmap[address] = indexer.DecimalAdd(addrmap[address], &asset.Amount)
		}
	}
	plainSats := output.Value
	if len(output.Assets) > 0 {
		plainSats -= output.Assets.GetBindingSatAmout()
	}
	if plainSats > 0 {
		for _, address := range output.Address.Addresses {
			addrmap, ok := b.tickAddressMap[indexer.ASSET_PLAIN_SAT.String()]
			if !ok {
				addrmap = make(map[string]*indexer.Decimal)
				b.tickAddressMap[indexer.ASSET_PLAIN_SAT.String()] = addrmap
			}
			addrmap[address] = indexer.DecimalAdd(addrmap[address], indexer.NewDefaultDecimal(plainSats))
		}
	}
	utxoId := indexer.ToUtxoId(output.Height, output.TxId, int(output.N))
	for _, address := range output.Address.Addresses {
		utxomap, ok := b.addressValueMap[address]
		if !ok {
			common.Log.Panicf("%s should be loaded before", address)
			// 前面应该加载过了
			// utxomap = &indexer.AddressValueV2{
			// 	AddressType: uint32(output.Address.Type),
			// 	AddressId: 0,
			// 	Utxos: make(map[uint64]bool),
			// }
			// b.addressIdMap[address] = utxomap
		}
		utxomap.Utxos[utxoId] = true
	}
}

func (b *BaseIndexer) SyncToChainTip(stopChan chan struct{}) int {
	count, err := getBlockCount()
	if err != nil {
		common.Log.Errorf("failed to get block count %v", err)
		return -2
	}

	bRunInStepMode := false
	if bRunInStepMode {
		if count == int64(b.lastHeight) {
			return 0
		}
		count = int64(b.lastHeight) + 1
	}

	return b.SyncToBlock(int(count), stopChan)
}

func (b *BaseIndexer) SyncBlock(block *wire.MsgBlock, height, tip int, updateDB bool) error {
	bk := ConvertBlock(block, height, b.chaincfgParam)
	ret := b.syncBlock(bk, tip, updateDB)
	if ret != 0 {
		return fmt.Errorf("syncBlock %d failed, %v", height, ret)
	}
	return nil
}

func (b *BaseIndexer) loadUtxoFromDB(txn *badger.Txn, utxostr string) error {
	utxo := &common.UtxoValueInDB{}
	dbKey := db.GetUTXODBKey(utxostr)
	err := db.GetValueFromDB(dbKey, txn, utxo)
	if err == badger.ErrKeyNotFound {
		return err
	}
	if err != nil {
		common.Log.Errorf("failed to get value of utxo: %s, %v", utxostr, err)
		return err
	}

	var addresses common.ScriptPubKey
	for _, addressId := range utxo.AddressIds {
		address, err := db.GetAddressByIDFromDBTxn(txn, addressId)
		if err != nil {
			common.Log.Errorf("failed to get address by id %d, utxo: %s, utxoId: %d, err: %v", addressId, utxostr, utxo.UtxoId, err)
			return err
		}
		_, ok := b.addressValueMap[address]
		if !ok {
			data, err := db.GetAddressDataFromDBTxn(txn, address)
			if err != nil {
				common.Log.Errorf("failed to get address data by address %s, utxo: %s, utxoId: %d, err: %v", address, utxostr, utxo.UtxoId, err)
				return err
			}
			b.addressValueMap[address] = data.ToAddressValueV2()
		}
		addresses.Addresses = append(addresses.Addresses, address)
	}
	addresses.Type = int(utxo.AddressType)
	addresses.ReqSig = int(utxo.ReqSig)

	// TODO 对于多签的utxo，目前相当于把这个utxo给第一个地址
	height, txid, vout := indexer.FromUtxoId(utxo.UtxoId)
	b.utxoIndex.Index[utxostr] = &common.Output{Height: height, TxId: txid,
		Value:   utxo.Value,
		Address: &addresses,
		N:       uint32(vout),
		Assets:  utxo.Assets}
	return nil
}

func (b *BaseIndexer) prefetchTickerInfoFromDB(name string, divisibility int, addresses []string, txn *badger.Txn) {
	addrmap, ok := b.tickAddressMap[name]
	if !ok {
		addrmap = make(map[string]*indexer.Decimal)
		b.tickAddressMap[name] = addrmap
	}
	for _, addr := range addresses {
		_, ok := addrmap[addr]
		if !ok {
			addrId := b.addressValueMap[addr]
			amt, err := stp.GetTickerHolderInfoFromDBTxn(txn, name, addrId.AddressId)
			if err != nil {
				amt = indexer.NewDecimal(0, divisibility)
			}
			addrmap[addr] = amt
		}
	}
}

func (b *BaseIndexer) prefetchIndexesFromDB(block *common.Block) {
	//startTime := time.Now()
	b.db.View(func(txn *badger.Txn) error {
		for _, tx := range block.Transactions {
			for _, input := range tx.Inputs {
				if input.Vout >= wire.AnchorTxOutIndex {
					continue
				}
				
				utxo := indexer.GetUtxo(block.Height, input.Txid, int(input.Vout))
				output, ok := b.utxoIndex.Index[utxo]
				if !ok {
					err := b.loadUtxoFromDB(txn, utxo)
					if err == badger.ErrKeyNotFound {
						// 该区块生成的utxo，还没有进入b.utxoIndex.Index
						continue
					} else if err != nil {
						common.Log.Panicf("failed to get value of utxo: %s, %v", utxo, err)
					}
					output = b.utxoIndex.Index[utxo]
				}

				for _, asset := range output.Assets {
					name := asset.Name.String()
					b.prefetchTickerInfoFromDB(name, asset.Amount.Precision, output.Address.Addresses, txn)
				}
				b.prefetchTickerInfoFromDB(indexer.ASSET_PLAIN_SAT.String(), 0, output.Address.Addresses, txn)
			}

			for _, output := range tx.Outputs {
				// 包括op_return 和unknown这两种地址
				for _, address := range output.Address.Addresses {
					_, ok := b.addressValueMap[address]
					if !ok {
						data, err := db.GetAddressDataFromDBTxn(txn, address)
						if err != nil {
							addressId := b.generateAddressId()
							b.addressValueMap[address] = &indexer.AddressValueV2{
								AddressType: uint32(output.Address.Type),
								AddressId: addressId,
								Op: 1,
								Utxos: make(map[uint64]bool),
							}
						} else {
							b.addressValueMap[address] = data.ToAddressValueV2()
						}
					}
				}

				for _, asset := range output.Assets {
					name := asset.Name.String()
					b.prefetchTickerInfoFromDB(name, asset.Amount.Precision, output.Address.Addresses, txn)
				}
				b.prefetchTickerInfoFromDB(indexer.ASSET_PLAIN_SAT.String(), 0, output.Address.Addresses, txn)
			}
		}

		return nil
	})

	//common.Log.Infof("BaseIndexer.prefetchIndexesFromDB-> prefetched in %v\n", time.Since(startTime))
}

func (b *BaseIndexer) loadSyncStatsFromDB() {
	err := b.db.View(func(txn *badger.Txn) error {
		syncStats := &SyncStats{}
		err := db.GetValueFromDB([]byte(SyncStatsKey), txn, syncStats)
		if err == badger.ErrKeyNotFound {
			common.Log.Info("BaseIndexer.LoadSyncStatsFromDB-> No sync stats found in db")
			syncStats.SyncHeight = -1
		} else if err != nil {
			return err
		}
		common.Log.Infof("stats: %v", syncStats)
		common.Log.Infof("Code Ver: %s", common.SATOSHINET_INDEXER_VERSION)
		common.Log.Infof("DB Ver: %s", b.GetBaseDBVer())

		if syncStats.ReorgsDetected == nil {
			syncStats.ReorgsDetected = make([]int, 0)
		}

		b.stats = syncStats
		b.lastHash = b.stats.SyncBlockHash
		b.lastHeight = b.stats.SyncHeight

		return nil
	})

	if err != nil {
		common.Log.Panicf("BaseIndexer.LoadSyncStatsFromDB-> Error loading sync stats from db: %v", err)
	}
}

// triggerReorg is meant to be used for debugging and tests only
// I used it to simulate a reorg
// func (b *BaseIndexer) triggerReorg() {
// 	common.Log.Errorf("set reorg flag when test")
// 	b.lastHash = "wrong"
// }

func (b *BaseIndexer) generateAddressId() uint64 {
	id := b.stats.AddressCount
	b.stats.AddressCount++
	return id
}

// 耗时很长。仅用于在数据编译完成时验证数据，或者测试时验证数据。
func (b *BaseIndexer) CheckSelf() bool {

	common.Log.Info("BaseIndexer->checkSelf ... ")
	// for height, leak := range b.leakBlocks.SatsLeakBlocks {
	// 	common.Log.Infof("block %d leak %d", height, leak)
	// }
	// common.Log.Infof("Total leaks %d", b.leakBlocks.TotalLeakSats)

	startTime := time.Now()

	lsm, vlog := b.db.Size()
	common.Log.Infof("DB lsm: %0.2f, vlog: %0.2f", float64(lsm)/(1024*1024), float64(vlog)/(1024*1024))

	common.Log.Infof("stats: %v", b.stats)
	common.Log.Infof("Code Ver: %s", common.SATOSHINET_INDEXER_VERSION)
	common.Log.Infof("DB Ver: %s", b.GetBaseDBVer())
	// totalSats := common.FirstOrdinalInTheory(b.stats.SyncHeight + 1)
	// common.Log.Infof("expected total sats %d", totalSats)
	// common.Log.Infof("total leak sats %d", totalSats-b.stats.TotalSats)

	// var wg sync.WaitGroup
	// wg.Add(3)
	ascendSats1 := int64(0)
	b.db.View(func(txn *badger.Txn) error {
		//defer wg.Done()

		startTime2 := time.Now()
		common.Log.Infof("calculating in %s table ...", common.DB_KEY_BLOCK)
		
		for i := 0; i <= b.stats.SyncHeight; i++ {
			key := db.GetBlockDBKey(i)
			value := common.BlockValueInDB{}
			err := db.GetValueFromDB(key, txn, &value)
			if err != nil {
				common.Log.Panicf("GetValueFromDB %s error: %v", key, err)
			}
			if value.Height != i {
				common.Log.Panicf("block %d invalid value %d", i, value.Height)
			}

			ascendSats1 += value.OutputSats - value.InputSats
		}

		// 计算下聪网上有多少聪，是否跟状态一致
		if ascendSats1 != b.stats.TotalAscendSats {
			common.Log.Panicf("sats amount different. %d %d", ascendSats1, b.stats.TotalAscendSats)
		}

		common.Log.Infof("%s table takes %v", common.DB_KEY_BLOCK, time.Since(startTime2))
		return nil
	})

	descendSats := int64(0)
	satsInUtxo := int64(0)
	utxoCount := 0
	nonZeroUtxo := 0
	addressInUtxo := 0
	addressesInT1 := make(map[uint64]bool, 0)
	utxosInT1 := make(map[uint64]bool, 0)
	b.db.View(func(txn *badger.Txn) error {
		//defer wg.Done()

		var err error
		prefix := []byte(common.DB_KEY_UTXO)
		itr := txn.NewIterator(badger.DefaultIteratorOptions)
		defer itr.Close()

		startTime2 := time.Now()
		common.Log.Infof("calculating in %s table ...", common.DB_KEY_UTXO)

		for itr.Seek([]byte(prefix)); itr.ValidForPrefix([]byte(prefix)); itr.Next() {
			item := itr.Item()
			if item.IsDeletedOrExpired() {
				continue
			}
			var value common.UtxoValueInDB
			err = item.Value(func(data []byte) error {
				//return common.DecodeBytes(data, &value)
				return db.DecodeBytes(data, &value)
			})
			if err != nil {
				common.Log.Panicf("item.Value error: %v", err)
			}
			if value.AddressType == uint16(txscript.NullDataTy) || 
				value.AddressType == uint16(txscript.NonStandardTy) {
				descendSats += value.Value
				continue
			}

			// 用于打印不存在table2中的utxo
			// if value.UtxoId == 0x17453400960000 {
			// 	key := item.Key()
			// 	str, _ := db.GetUtxoByDBKey(key)
			// 	common.Log.Infof("%x %s", value.UtxoId, str)
			// }

			sats := value.Value
			if sats > 0 {
				nonZeroUtxo++
			}

			satsInUtxo += sats
			utxoCount++

			for _, addressId := range value.AddressIds {
				addressesInT1[addressId] = true
			}
			utxosInT1[value.UtxoId] = true
		}

		addressInUtxo = len(addressesInT1)

		common.Log.Infof("%s table takes %v", common.DB_KEY_UTXO, time.Since(startTime2))
		common.Log.Infof("1. utxo: %d(%d), sats %d, descend %d, address %d", utxoCount, nonZeroUtxo, satsInUtxo, descendSats, addressInUtxo)

		return nil
	})

	satsInAddress := int64(0)
	allAddressCount := 0
	allutxoInAddress := 0
	nonZeroUtxoInAddress := 0
	addressesInT2 := make(map[uint64]bool, 0)
	utxosInT2 := make(map[uint64]bool, 0)
	b.db.View(func(txn *badger.Txn) error {
		//defer wg.Done()

		startTime2 := time.Now()
		common.Log.Infof("calculating in %s table ...", indexer.DB_KEY_ADDRESSV2)

		prefix := []byte(indexer.DB_KEY_ADDRESSV2)
		itr := txn.NewIterator(badger.DefaultIteratorOptions)
		defer itr.Close()
		for itr.Seek(prefix); itr.ValidForPrefix(prefix); itr.Next() {
			item := itr.Item()
			if item.IsDeletedOrExpired() {
				continue
			}
			var value indexer.AddressValueInDBV2
			err := item.Value(func(data []byte) error {
				return db.DecodeBytes(data, &value)
			})
			if err != nil {
				common.Log.Panicf("item.Value error: %v", err)
			}

			for _, utxoId := range value.Utxos {
				allutxoInAddress++
				
				if value.AddressType == uint32(txscript.NullDataTy) || 
				value.AddressType == uint32(txscript.NonStandardTy) {
					continue
				}
				utxosInT2[utxoId] = true
				nonZeroUtxoInAddress++
			}
			if len(value.Utxos) > 0 {
				addressesInT2[value.AddressId] = true
			}
			
		}
		allAddressCount = len(addressesInT2)

		common.Log.Infof("%s table takes %v", common.DB_KEY_ADDRESSVALUE, time.Since(startTime2))
		common.Log.Infof("2. utxo: %d(%d), sats %d, address %d", allutxoInAddress, nonZeroUtxoInAddress, satsInAddress, allAddressCount)

		return nil
	})

	//wg.Wait()

	common.Log.Infof("utxos not in table %s", common.DB_KEY_ADDRESSVALUE)
	utxos1 := findDifferentItems(utxosInT1, utxosInT2)
	if len(utxos1) > 0 {
		b.printfUtxos(utxos1)
		common.Log.Panic("different utxos1")
	}

	common.Log.Infof("utxos not in table %s", common.DB_KEY_UTXO)
	utxos2 := findDifferentItems(utxosInT2, utxosInT1)
	if len(utxos2) > 0 {
		b.printfUtxos(utxos2)
		common.Log.Panicf("different utxos2 %v", utxos2)
	}

	common.Log.Infof("address not in table %s", common.DB_KEY_ADDRESSVALUE)
	utxos3 := findDifferentItems(addressesInT1, addressesInT2)
	for uid := range utxos3 {
		str, _ := db.GetAddressByID(b.db, uid)
		common.Log.Infof("%s", str)
	}

	common.Log.Infof("address not in table %s", common.DB_KEY_UTXO)
	utxos4 := findDifferentItems(addressesInT2, addressesInT1)
	for uid := range utxos4 {
		str, _ := db.GetAddressByID(b.db, uid)
		common.Log.Infof("%s", str)
	}

	if len(utxos1) > 0 || len(utxos2) > 0 || len(utxos3) > 0 || len(utxos4) > 0 {
		common.Log.Panic("utxos or address differents")
	}

	if addressInUtxo != allAddressCount {
		common.Log.Panicf("address count different %d %d", addressInUtxo, allAddressCount)
	}

	if utxoCount != allutxoInAddress {
		common.Log.Panicf("utxo different %d %d", utxoCount, allutxoInAddress)
	}

	// testnet: block 26432 多奖励了0.001btc，2642多奖励了0.0015，所以测试网络对比数据会有异常，只在主网上验证
	// mainnet: 早期软件原因有些块没有拿到足够的奖励，比如124724
	// if b.stats.TotalSats != satsInAddress {
	// 	common.Log.Panicf("sats wrong %d %d", satsInAddress, b.stats.TotalSats)
	// }

	b.setDBVersion()

	common.Log.Infof("DB checked successfully, %v", time.Since(startTime))
	return true
}

func findDifferentItems(map1, map2 map[uint64]bool) map[uint64]bool {
	differentItems := make(map[uint64]bool)
	for key := range map1 {
		if _, exists := map2[key]; !exists {
			differentItems[key] = true
		}
	}

	return differentItems
}

// only for test
func (b *BaseIndexer) printfUtxos(utxos map[uint64]bool) map[uint64]string {
	result := make(map[uint64]string)
	b.db.View(func(txn *badger.Txn) error {
		var err error
		prefix := []byte(common.DB_KEY_UTXO)
		itr := txn.NewIterator(badger.DefaultIteratorOptions)
		defer itr.Close()

		for itr.Seek([]byte(prefix)); itr.ValidForPrefix([]byte(prefix)); itr.Next() {
			item := itr.Item()
			if item.IsDeletedOrExpired() {
				continue
			}
			var value common.UtxoValueInDB
			err = item.Value(func(data []byte) error {
				return db.DecodeBytes(data, &value)
			})
			if err != nil {
				common.Log.Errorf("item.Value error: %v", err)
				continue
			}

			// 用于打印不存在table2中的utxo
			if _, ok := utxos[value.UtxoId]; ok {
				key := item.Key()
				str, err := db.GetUtxoByDBKey(key)
				if err == nil {
					common.Log.Infof("%x %s %d", value.UtxoId, str, value.Value)
					result[value.UtxoId] = str
				}

				delete(utxos, value.UtxoId)
				if len(utxos) == 0 {
					return nil
				}
			}
		}

		return nil
	})

	return result
}

func (b *BaseIndexer) setDBVersion() {
	err := db.SetRawValueToDB([]byte(BaseDBVerKey), []byte(common.BASE_DB_VERSION), b.db)
	if err != nil {
		common.Log.Panicf("Error setting in db %v", err)
	}
}

func (b *BaseIndexer) GetBaseDBVer() string {
	value, err := db.GetRawValueFromDB([]byte(BaseDBVerKey), b.db)
	if err != nil {
		common.Log.Errorf("GetRawValueFromDB failed %v", err)
		return ""
	}

	return string(value)
}

func (b *BaseIndexer) GetBaseDB() *badger.DB {
	return b.db
}

func (b *BaseIndexer) GetSyncHeight() int {
	return b.stats.SyncHeight
}

func (b *BaseIndexer) GetSyncBase() *SyncBase {
	return &b.stats.SyncBase
}

func (b *BaseIndexer) SetSyncBase(sync *SyncBase) {
	b.stats.SyncBase = *sync
}

func (b *BaseIndexer) GetHeight() int {
	return b.lastHeight
}

func (b *BaseIndexer) GetChainTip() int {
	return b.stats.ChainTip
}

func (b *BaseIndexer) SetReorgHeight(height int) {
	b.stats.ReorgsDetected = append(b.stats.ReorgsDetected, height)
	err := db.GobSetDB1([]byte(SyncStatsKey), b.stats, b.db)
	if err != nil {
		common.Log.Panicf("Error setting in db %v", err)
	}
}

func (b *BaseIndexer) GetBlockHistory() int {
	return b.keepBlockHistory
}

func (b *BaseIndexer) ResetBlockVector() {
	b.blockVector = make([]*common.BlockValueInDB, 0)
}

func (p *BaseIndexer) GetBlockInBuffer(height int) *common.BlockValueInDB {
	for _, block := range p.blockVector {
		if block.Height == height {
			return block
		}
	}

	return nil
}

func (p *BaseIndexer) getAddressId(address string) (uint64, int) {
	value, ok := p.addressValueMap[address]
	if !ok {
		common.Log.Errorf("can't find addressId %s", address)
		return indexer.INVALID_ID, -1
	}
	return value.AddressId, value.Op
}

// 服务节点是引导节点，并且有足够资产才算是
func (p *BaseIndexer) IsCoreNodeAscend(ascend *common.AscendData) bool {
	if hex.EncodeToString(ascend.PubA) == indexer.GetBootstrapPubKey() {
		return p.HasCoreNodeEligibility(ascend.Assets)
	}
	return false
	// 连接core node的都只是普通miner
}

func (p *BaseIndexer) IsCoreNodeDescend(descend *common.DescendData) bool {
	return p.HasCoreNodeEligibility(descend.Assets)
}

func (p *BaseIndexer) HasCoreNodeEligibility(assets wire.TxAssets) bool {
	coreAssetName := indexer.CORENODE_STAKING_ASSET_NAME
	coreAssetAmount := indexer.CORENODE_STAKING_ASSET_AMOUNT
	if !p.IsMainnet() {
		coreAssetName = indexer.TESTNET_CORENODE_STAKING_ASSET_NAME
		coreAssetAmount = indexer.TESTNET_CORENODE_STAKING_ASSET_AMOUNT
	}
	for _, asset := range assets {
		if asset.Name.String() == coreAssetName {
			return asset.Amount.Int64() >= coreAssetAmount
		}
	}
	return false
}

// 暂时等于corenode
func (p *BaseIndexer) HasMinerEligibility(assets wire.TxAssets) bool {
	return p.HasCoreNodeEligibility(assets)
}

