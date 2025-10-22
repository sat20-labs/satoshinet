package base

import (
	"encoding/hex"
	"fmt"
	"sync"
	"time"

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
	db    indexer.KVDB
	stats *SyncStats // 数据库状态

	// 需要clone的数据
	blockVector    []*common.BlockValueInDB //
	utxoIndex      *common.UTXOIndex
	delUTXOs       []*UtxoValue                           // utxo->address,utxoid
	tickAddressMap map[string]map[string]*indexer.Decimal // ticker->addressId->amount，在某个更新周期中的缓存数据，非全量

	tickInfoMap        map[string]*common.TickerInfo
	addressValueMap    map[string]*indexer.AddressValueV2 // 每个区块处理之前填充所有需要的地址id
	coreNodeMap        map[string]*common.CoreNodeInfo    // pubkey, 不清空
	coreNodeMapUpdated bool
	channelMap         map[string]*common.ChannelInfo // address, 

	miningAddress    string // 排序器的挖矿地址，可能不是当前区块的地址
	lastHeight       int // 内存数据同步区块
	lastHash         string

	// 不需要复制的数据
	seqMgr             *common.MiningSequenceMgr // 不需要复制到rpc实例
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

	mutex sync.RWMutex // 仅对需要提供给节点实时访问的数据加锁
}

const BLOCK_PREFETCH = 12

func NewBaseIndexer(
	basicDB indexer.KVDB,
	chaincfgParam *chaincfg.Params,
	maxIndexHeight int,
	periodFlushToDB int,
) *BaseIndexer {
	indexer := &BaseIndexer{
		db:               basicDB,
		stats:            &SyncStats{},
		periodFlushToDB:  periodFlushToDB,
		keepBlockHistory: 20,
		blocksChan:       make(chan *common.Block, BLOCK_PREFETCH),
		chaincfgParam:    chaincfgParam,
		maxIndexHeight:   maxIndexHeight,
	}

	return indexer
}

func (b *BaseIndexer) Init() {
	dbver := b.GetBaseDBVer()
	common.Log.Infof("base db version: %s", b.GetBaseDBVer())
	if dbver != "" && dbver != common.BASE_DB_VERSION {
		common.Log.Panicf("DB version inconsistent. DB ver %s, but code base %s", dbver, common.BASE_DB_VERSION)
	}

	b.reset()

	b.coreNodeMap = stp.GetAllCoreNodeFromDB(b.db, b.chaincfgParam)
	b.channelMap = stp.GetAllChannelFromDB(b.db)
	b.seqMgr = common.NewMiningSequenceMgr(b.chaincfgParam)
	err := b.seqMgr.Init(b.coreNodeMap, b.stats.SyncHeight, b.stats.MiningAddr)
	if err != nil {
		common.Log.Panicf("seqMgr init failed, %v", err)
	}
}

func (b *BaseIndexer) SetUpdateDBCallback(cb2 UpdateDBCallback) {
	b.updateDBCB = cb2
}

func (b *BaseIndexer) SetBlockCallback(cb1 BlockProcCallback) {
	b.blockprocCB = cb1
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
	b.coreNodeMap = make(map[string]*common.CoreNodeInfo)
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
	newInst.delUTXOs = make([]*UtxoValue, len(b.delUTXOs))
	copy(newInst.delUTXOs, b.delUTXOs)

	for key, value := range b.utxoIndex.AscendMap {
		newInst.utxoIndex.AscendMap[key] = value
	}
	for key, value := range b.utxoIndex.DescendMap {
		newInst.utxoIndex.DescendMap[key] = value
	}
	for key, value := range b.utxoIndex.ReferrerMap {
		newInst.utxoIndex.ReferrerMap[key] = &common.ReferrerInfo{
			Name: value.Name,
			BindBlock: value.BindBlock,
		}
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

	newInst.coreNodeMap = make(map[string]*common.CoreNodeInfo)
	for k, v := range b.coreNodeMap {
		newInst.coreNodeMap[k] = v.Clone()
	}
	newInst.coreNodeMapUpdated = b.coreNodeMapUpdated

	newInst.channelMap = make(map[string]*common.ChannelInfo)
	for k, v := range b.channelMap {
		newInst.channelMap[k] = v
	}

	newInst.addressValueMap = make(map[string]*indexer.AddressValueV2)
	for key, value := range b.addressValueMap {
		n := indexer.AddressValueV2{
			AddressType: value.AddressType,
			AddressId:   value.AddressId,
			Op:          value.Op,
			Utxos:       make(map[uint64]bool),
		}
		for id, v := range value.Utxos {
			n.Utxos[id] = v
		}
		newInst.addressValueMap[key] = &n
	}
	newInst.blockVector = make([]*common.BlockValueInDB, len(b.blockVector))
	copy(newInst.blockVector, b.blockVector)

	newInst.lastHash = b.lastHash
	newInst.lastHeight = b.lastHeight
	newInst.miningAddress = b.miningAddress

	newInst.stats = b.stats.Clone()
	newInst.blockprocCB = b.blockprocCB
	newInst.updateDBCB = b.updateDBCB

	common.Log.Infof("BaseIndexer->clone takes %v", time.Since(startTime))

	return newInst
}

// 在 UpdateDB 用到的数据，这里需要先剪去，这些剪去的数据，当作已经备份到数据库
func (b *BaseIndexer) Subtract(another *BaseIndexer) {
	// 将已经备份到数据库的数据删除，防止内存中数据增长过快
	for key := range another.utxoIndex.Index {
		delete(b.utxoIndex.Index, key)
	}

	// TODO 需要增加一个重新加载机制，以便释放老的不需要的数据
	// for k := range another.addressValueMap {
	// 	delete(b.addressValueMap, k)
	// }
	// for k := range b.tickInfoMap {
	// 	delete(b.tickInfoMap, k)
	// }
	// for k := range b.tickAddressMap {
	// 	delete(b.tickAddressMap, k)
	// }

	l := len(another.delUTXOs)
	//b.delUTXOs = b.delUTXOs[l:] 不会释放前面的内存
	b.delUTXOs = append([]*UtxoValue(nil), b.delUTXOs[l:]...) // 释放前面删除的切片

	l = len(another.blockVector)
	// b.blockVector = b.blockVector[l:]
	b.blockVector = append([]*common.BlockValueInDB(nil), b.blockVector[l:]...)
}

func (b *BaseIndexer) Repair() {

}

// only call in compiling data
func (b *BaseIndexer) forceUpdateDB() {
	if b.updateDBCB != nil {
		startTime := time.Now()
		b.UpdateDB()
		common.Log.Infof("BaseIndexer.updateBasicDB: cost: %v", time.Since(startTime))

		// startTime = time.Now()
		b.updateDBCB()
		// common.Log.Infof("BaseIndexer.updateOrdxDB: cost: %v", time.Since(startTime))

		common.Log.Infof("forceUpdateDB sync to height %d", b.stats.SyncHeight)
	} //else {
	// 	common.Log.Infof("don't run forceUpdateDB after entering service mode")
	// }
}

func (b *BaseIndexer) prefechAddress() {

	b.db.View(func(txn indexer.ReadBatch) error {
		for _, v := range b.utxoIndex.Index {
			if v.Address.Type == int(txscript.NullDataTy) {
				// 只有OP_RETURN 才不记录
				if v.Value == 0 && len(v.Assets) == 0 {
					continue
				}
			}
			for _, addr := range v.Address.Addresses {
				_, ok := b.addressValueMap[addr]
				if !ok {
					data, err := db.GetAddressDataFromDBTxnV2(txn, addr)
					if err != nil {
						common.Log.Errorf("failed to get address data by address %s: %v", addr, err)
						continue
					}
					b.addressValueMap[addr] = data.ToAddressValueV2()
				}
			}
		}

		return nil
	})
}

func (b *BaseIndexer) UpdateDB() {
	common.Log.Infof("BaseIndexer->updateBasicDB %d start...", b.lastHeight)

	// 拿到所有的addressId
	b.prefechAddress()

	referrers := make([]string, 0)
	for _, name := range b.utxoIndex.ReferrerMap {
		referrers = append(referrers, name.Name)
	}
	referreesMap, err := stp.GetReferreesFromDB(b.db, referrers)
	if err != nil {
		common.Log.Panicf("GetReferreesFromDB failed, %v", err)
	}


	wb := b.db.NewWriteBatch()
	defer wb.Close()

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

	for addr, referrer := range b.utxoIndex.ReferrerMap {
		key := stp.GetReferrerDBKey(addr)
		err := db.SetDB([]byte(key), referrer, wb)
		if err != nil {
			common.Log.Panicf("Error setting in db %v", err)
		}

		addrvalue := b.addressValueMap[addr]
		referrees, ok := referreesMap[referrer.Name]
		if !ok {
			referrees = make(map[uint64]int)
		}
		// 有序插入，避免重复
		referrees[addrvalue.AddressId] = referrer.BindBlock
		referreesMap[referrer.Name] = referrees
	}
	for referrer, referrees := range referreesMap {
		key := stp.GetReferreeDBKey(referrer)
		err = db.SetDB([]byte(key), referrees, wb)
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
	b.stats.MiningAddr = b.miningAddress
	err = db.SetDB([]byte(SyncStatsKey), b.stats, wb)
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

	// TODO 临时打开，验证数据
	// if !b.CheckSelf() {
	// 	common.Log.Panicf("BaseIndexer.CheckSelf failed")
	// }
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

func getMiningAddress(block *common.Block) string {
	if block == nil || len(block.Transactions) == 0 {
		return ""
	}

	coinbaseTx := block.Transactions[0]
	// 聪网的coinbase输出只有一个有效地址
	for _, txOut := range coinbaseTx.Outputs {
		if common.IsOpReturn(txOut.Address.PkScript) {
			continue
		}
		return txOut.Address.Addresses[0]
	}
	return ""
}


// sync
func (b *BaseIndexer) syncBlock(block *common.Block, tip int, updateDB bool) int {
	common.Log.Infof("BaseIndexer.syncBlock-> currentHeight %d, blockHeight %d", b.lastHeight, block.Height)

	if block.Height != b.lastHeight+1 {
		common.Log.Warningf("BaseIndexer.syncBlock-> expected block height %d, got %d", b.lastHeight+1, block.Height)
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
	b.miningAddress = b.seqMgr.GetCurrentMiningAddr() //getMiningAddress(block)
	b.seqMgr.MoveMiningAddr(block.Height, b.miningAddress)
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

func (b *BaseIndexer) handleStakeAsset(ascend *common.AscendData, data []byte) {
	/*
	质押资产成为挖矿节点的条件：
	1. 通道地址：核心通道地址，或者连接核心节点的通道地址
	2. 足够的资产
	3. 交易中有一个质押的op_return (posv2版本之后)
	目前的限制：现在只会在一个ascending交易中做判断，这不是合理的方式，可能将穿越资产当作质押资产
	TODO：采用合约的方式，将质押资产锁定在合约中，交易也明确必须是STAKE的合约动作
	*/

	if ascend.Height <= int(b.chaincfgParam.Checkpoints[0].Height) {
		// 不需要检查是否有一个stake的op_return
	} else {
		if data == nil {
			return
		}
		assetName, amt, err := common.ParseStakeInvoice(data)
		if err != nil {
			common.Log.Errorf("handleStakeAsset no staking asset info, %v", err)
			return
		}
		if assetName == indexer.ASSET_PLAIN_SAT.String() {
			if len(ascend.Assets) != 0 || fmt.Sprintf("%d", ascend.Value) != amt.String() {
				common.Log.Errorf("handleStakeAsset invalid sats value, %d -> %s", ascend.Value, amt.String())
				return
			}
		} else {
			info, err := ascend.Assets.Find(indexer.NewAssetNameFromString(assetName))
			if err != nil || info.Amount.Cmp(amt) != 0 {
				common.Log.Errorf("handleStakeAsset invalid asset amt, %s -> %s", info.Amount.String(), amt.String())
				return 
			}
		}
	}

	b.addMinerNode(ascend)
}

func (b *BaseIndexer) handleStakeAssetV2(height int, tx *common.Transaction, data []byte) {
	name, amt, err := common.ParseStakeInvoice(data)
	if err != nil {
		common.Log.Errorf("handleStakeAssetV2 no staking asset info, %v", err)
		return
	}
	assetName := indexer.NewAssetNameFromString(name)
	// 检查该tx是否有对应的资产信息
	var stakeAsset *common.Output
	for _, txOut := range tx.Outputs {
		if common.IsOpReturn(txOut.Address.PkScript) {
			continue
		}
		if txOut.Address.Type != int(txscript.WitnessV0ScriptHashTy) {
			continue
		}

		if name == indexer.ASSET_PLAIN_SAT.String() {
			if len(txOut.Assets) != 0 || fmt.Sprintf("%d", txOut.Value) != amt.String() {
				common.Log.Errorf("handleStakeAssetV2 %s invalid sats value, %d -> %s", tx.Txid, txOut.Value, amt.String())
				continue
			}
		} else {
			info, err := txOut.Assets.Find(assetName)
			if err != nil || info.Amount.Cmp(amt) != 0 {
				common.Log.Errorf("handleStakeAssetV2 %s invalid asset amt, %s -> %s", tx.Txid, info.Amount.String(), amt.String())
				continue 
			}
		}

		stakeAsset = txOut
		break
	}
	if stakeAsset == nil {
		common.Log.Errorf("can't find staking asset output, tx %s", tx.Txid)
		return
	}

	channelInfo, ok := b.channelMap[stakeAsset.Address.Addresses[0]]
	if ok {
		common.Log.Errorf("can't find channel info from %s", stakeAsset.Address.Addresses[0])
		return
	}
	ascend := &common.AscendData{
		Height: height,
		FundingUtxo: fmt.Sprintf("%s:%d", tx.Txid, stakeAsset.N),
		AnchorTxId: "",
		Address: channelInfo.Address,
		Value: stakeAsset.Value,
		Assets: stakeAsset.Assets,
		PubA: channelInfo.PubA,
		PubB: channelInfo.PubB,
	}

	b.addMinerNode(ascend)
}

func (b *BaseIndexer) addMinerNode(ascend *common.AscendData) {
	coreNodeKey := hex.EncodeToString(ascend.PubB)
	_, ok := b.coreNodeMap[coreNodeKey]
	if !ok {
		if b.IsCoreNodeAscend(ascend) {
			// 新增加一个core node
			coreNode := common.NewCoreNodeInfo(ascend)
			coreNodeKey = hex.EncodeToString(ascend.PubB)

			b.mutex.Lock()
			b.coreNodeMap[coreNodeKey] = coreNode
			serverNodeKey := hex.EncodeToString(ascend.PubA)
			serverNode := b.coreNodeMap[serverNodeKey]
			serverNode.ChildMiners[coreNodeKey] = &common.MinerAscendInfo{
				AscendHeight: ascend.Height,
				AscendUtxo: coreNode.AscendUtxo,
			} 
			b.seqMgr.AddNode(coreNodeKey, serverNodeKey, ascend.Height)
			b.coreNodeMapUpdated = true
			b.mutex.Unlock()

			common.Log.Infof("add core node %s at height %d", coreNodeKey, ascend.Height)
		} else {
			b.mutex.Lock()
			coreNodeKey := hex.EncodeToString(ascend.PubA)
			coreNode, ok := b.coreNodeMap[coreNodeKey]
			if ok && b.HasMinerEligibility(ascend.Assets) {
				// 一个连接到corenode的普通miner
				b.coreNodeMapUpdated = true
				childKey := hex.EncodeToString(ascend.PubB)
				coreNode.ChildMiners[childKey] =  &common.MinerAscendInfo{
					AscendHeight: ascend.Height,
					AscendUtxo: ascend.FundingUtxo,
				}
				b.seqMgr.AddNode(childKey, coreNodeKey, ascend.Height)
				b.mutex.Unlock()
				common.Log.Infof("add miner node %s at height %d", hex.EncodeToString(ascend.PubB), ascend.Height)
			} else {
				b.mutex.Unlock()
				// 无效的脚本
				common.Log.Infof("not miner staking tx %s, utxo: %s, %v", ascend.AnchorTxId, ascend.FundingUtxo, ascend.Assets)
			}
		}
	}
}

// satoshinet 只需要保存utxo即可
// 所有聪都来自锚定交易，也就是闪电网络通道
func (b *BaseIndexer) processBlock(block *common.Block) {
	blockValue := &common.BlockValueInDB{Height: block.Height,
		Timestamp: block.Timestamp.Unix(),
		TxAmount:  len(block.Transactions),
	}
	

	addedUtxoCount := 0
	deledUtxoCount := 0

	satsInput := int64(0)
	satsOutput := int64(0)
	for txIndex, tx := range block.Transactions {

		if !b.IsMainnet() {
			// 聪网测试网处理anchorTx的一个bug导致AnchorTx出现多次
			if block.Height == 1709 && tx.Txid == "2025513a5ad2bdb180bc1d239915fa813237f9c6724acfcaf5ae02971d803215" {
				continue
			}
		}

		var inputAddress string
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

				b.handleStakeAsset(ascend, nil)
				// 仅仅是通道地址，有可能是合约控制
				_, ok := b.channelMap[ascend.Address]
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

			if inputAddress == "" {
				inputAddress = inputUtxo.Address.Addresses[0]
			}
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
							common.Log.Errorf("GenTickerInfo %s:%d failed, %v", tx.Txid, i, err)
						}

					case common.CONTENT_TYPE_CHANNELID:
						// 如果是通道更新，data中包含通道承诺高度
						// 更新通道的最新高度 TODO

					case common.CONTENT_TYPE_STAKE:
						// 质押资产，成为矿机
						if ascend != nil {
							// 利用ascending质押
							b.handleStakeAsset(ascend, data)
						} else {
							// 直接在聪网上转账并质押，只需要有对应的op_return
							b.handleStakeAssetV2(block.Height, tx, data)
						}
						

					case common.CONTENT_TYPE_BINDREFERRER:
						// tx的输入和输出都是被推荐人地址，data是推荐人名字，每个地址只能绑定一个推荐人
						_, ok := b.utxoIndex.ReferrerMap[inputAddress]
						if !ok {
							existing, err := b.loadReferrerFromDB(inputAddress)
							if err != nil {
								//
								b.utxoIndex.ReferrerMap[inputAddress] = &common.ReferrerInfo{
									Name: string(data),
									BindBlock: block.Height,
								}
							} else {
								common.Log.Warningf("%s has binded to referrer %s", inputAddress, existing.Name)
							}
						}
					}
				} else {
					common.Log.Errorf("ReadDataFromNullDataScript %s:%d failed, %v", tx.Txid, i, err)
				}
			}

			u := indexer.GetUtxo(block.Height, tx.Txid, int(output.N))
			b.utxoIndex.Index[u] = output
			addedUtxoCount++
			satsOutput += output.Value // 包括op_return的聪
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

func (b *BaseIndexer) SyncBlockWithHeight(height, tip int, updateDB bool) error {
	block := b.fetchBlock(height)
	if block == nil {
		return fmt.Errorf("can't fetch block %d", height)
	}
	ret := b.syncBlock(block, tip, updateDB)
	if ret != 0 {
		return fmt.Errorf("syncBlock %d failed, %v", height, ret)
	}
	return nil
}

func (b *BaseIndexer) SyncBlock(block *wire.MsgBlock, height, tip int, updateDB bool) error {
	bk := ConvertBlock(block, height, b.chaincfgParam)
	ret := b.syncBlock(bk, tip, updateDB)
	if ret != 0 {
		return fmt.Errorf("syncBlock %d failed, %v", height, ret)
	}
	return nil
}

func (b *BaseIndexer) loadUtxoFromDB(utxostr string) error {
	return b.db.View(func(txn indexer.ReadBatch) error {
		return b.loadUtxoFromTxn(utxostr, txn)
	})
}

func (b *BaseIndexer) loadUtxoFromTxn(utxostr string, txn indexer.ReadBatch) error {
	utxo := &common.UtxoValueInDB{}
	dbKey := db.GetUTXODBKey(utxostr)
	err := db.GetValueFromTxn(dbKey, utxo, txn)
	if err == indexer.ErrKeyNotFound {
		return err
	}
	if err != nil {
		common.Log.Errorf("failed to get value of utxo: %s, %v", utxostr, err)
		return err
	}

	var addresses common.ScriptPubKey
	for _, addressId := range utxo.AddressIds {
		address, err := db.GetAddressByIDFromTxn(txn, addressId)
		if err != nil {
			common.Log.Errorf("failed to get address by id %d, utxo: %s, utxoId: %d, err: %v", addressId, utxostr, utxo.UtxoId, err)
			return err
		}
		_, ok := b.addressValueMap[address]
		if !ok {
			data, err := db.GetAddressDataFromDBTxnV2(txn, address)
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

func (b *BaseIndexer) loadReferrerFromDB(address string) (*common.ReferrerInfo, error) {
	referrer, ok := b.utxoIndex.ReferrerMap[address]
	if ok {
		return referrer, nil
	}

	referrer, err := stp.GetReferrerFromDB(b.db, address)
	if err != nil {
		return nil, err
	}
	b.utxoIndex.ReferrerMap[address] = referrer
	return referrer, nil
}

func (b *BaseIndexer) prefetchTickerInfoFromDB(name string, divisibility int, addresses []string, txn indexer.ReadBatch) {
	addrmap, ok := b.tickAddressMap[name]
	if !ok {
		addrmap = make(map[string]*indexer.Decimal)
		b.tickAddressMap[name] = addrmap
	}
	for _, addr := range addresses {
		_, ok := addrmap[addr]
		if !ok {
			addrValue, ok := b.addressValueMap[addr]
			if !ok {
				data, err := db.GetAddressDataFromDBTxnV2(txn, addr)
				if err != nil {
					common.Log.Errorf("failed to get address data by address %s: %v", addr, err)
					continue
				}
				b.addressValueMap[addr] = data.ToAddressValueV2()
			}
			amt, err := stp.GetTickerHolderInfoFromDBTxn(txn, name, addrValue.AddressId)
			if err != nil {
				amt = indexer.NewDecimal(0, divisibility)
			}
			addrmap[addr] = amt
		}
	}
}

func (b *BaseIndexer) prefetchIndexesFromDB(block *common.Block) {
	//startTime := time.Now()

	// TODO 跑数据性能下降时，需要优化这个函数。参考indexer的同名函数。
	b.db.View(func(txn indexer.ReadBatch) error {
		for _, tx := range block.Transactions {
			for _, input := range tx.Inputs {
				if input.Vout >= wire.AnchorTxOutIndex {
					continue
				}

				utxo := indexer.GetUtxo(block.Height, input.Txid, int(input.Vout))
				output, ok := b.utxoIndex.Index[utxo]
				if !ok {
					err := b.loadUtxoFromTxn(utxo, txn)
					if err == indexer.ErrKeyNotFound {
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
						data, err := db.GetAddressDataFromDBTxnV2(txn, address)
						if err != nil {
							addressId := b.generateAddressId()
							b.addressValueMap[address] = &indexer.AddressValueV2{
								AddressType: uint32(output.Address.Type),
								AddressId:   addressId,
								Op:          1,
								Utxos:       make(map[uint64]bool),
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

	syncStats := &SyncStats{}
	err := db.GetValueFromDB([]byte(SyncStatsKey), syncStats, b.db)
	if err == indexer.ErrKeyNotFound {
		common.Log.Info("BaseIndexer.LoadSyncStatsFromDB-> No sync stats found in db")
		syncStats.SyncHeight = -1
	} else if err != nil {
		common.Log.Panicf("BaseIndexer.LoadSyncStatsFromDB failed, %v", err)
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

	common.Log.Infof("stats: %v", b.stats)
	common.Log.Infof("Code Ver: %s", common.SATOSHINET_INDEXER_VERSION)
	common.Log.Infof("DB Ver: %s", b.GetBaseDBVer())
	// totalSats := common.FirstOrdinalInTheory(b.stats.SyncHeight + 1)
	// common.Log.Infof("expected total sats %d", totalSats)
	// common.Log.Infof("total leak sats %d", totalSats-b.stats.TotalSats)

	ascendSats1 := int64(0)

	startTime2 := time.Now()
	common.Log.Infof("calculating in %s table ...", common.DB_KEY_BLOCK)

	for i := 0; i <= b.stats.SyncHeight; i++ {
		key := db.GetBlockDBKey(i)
		value := common.BlockValueInDB{}
		err := db.GetValueFromDB(key, &value, b.db)
		if err != nil {
			common.Log.Panicf("GetValueFromDB %s error: %v", key, err)
		}
		if value.Height != i {
			common.Log.Panicf("block %d invalid value %d", i, value.Height)
		}

		ascendSats1 += value.OutputSats - value.InputSats
	}

	common.Log.Infof("total address %d", b.stats.AddressCount)
	for i := uint64(0); i < (b.stats.AddressCount); i++ {
		addr, err := db.GetAddressByIDFromDB(b.db, i)
		if err != nil {
			common.Log.Panicf("GetAddressByIDFromDB %d error: %v", i, err)
		}
		id, err := db.GetAddressIdFromDB(b.db, addr)
		if err != nil {
			common.Log.Panicf("GetAddressIdFromDB %d error: %v", i, err)
		}
		if id != i {
			common.Log.Panicf("address id different %d %d", i, id)
		}
	}

	// 计算下聪网上有多少聪，是否跟状态一致
	if ascendSats1 != b.stats.TotalAscendSats {
		common.Log.Panicf("sats amount different. %d %d", ascendSats1, b.stats.TotalAscendSats)
	}

	common.Log.Infof("%s table takes %v", common.DB_KEY_BLOCK, time.Since(startTime2))

	descendSats := int64(0)
	satsInUtxo := int64(0)
	utxoCount := 0
	nonZeroUtxo := 0
	addressInUtxo := 0
	addressesInT1 := make(map[uint64]bool, 0)
	utxosInT1 := make(map[uint64]bool, 0)
	startTime2 = time.Now()
	common.Log.Infof("calculating in %s table ...", common.DB_KEY_UTXO)

	b.db.BatchRead([]byte(common.DB_KEY_UTXO), false, func(k, v []byte) error {

		var value common.UtxoValueInDB

		err := db.DecodeBytes(v, &value)
		if err != nil {
			common.Log.Panicf("item.Value error: %v", err)
		}
		utxoCount++
		if value.AddressType == uint16(txscript.NullDataTy) ||
			value.AddressType == uint16(txscript.NonStandardTy) {
			descendSats += value.Value
		} else {
			// 用于打印不存在table2中的utxo
			// if value.UtxoId == 0x17453400960000 {
			// 	key := item.Key()
			// 	str, _ := db.GetUtxoByDBKey(key)
			// 	common.Log.Infof("%x %s", value.UtxoId, str)
			// }

			sats := value.Value
			// if sats > 0 {
				nonZeroUtxo++
			//}

			satsInUtxo += sats
			utxosInT1[value.UtxoId] = true
		}

		for _, addressId := range value.AddressIds {
			addressesInT1[addressId] = true
		}
		
		return nil
	})
	addressInUtxo = len(addressesInT1)

	common.Log.Infof("%s table takes %v", common.DB_KEY_UTXO, time.Since(startTime2))
	common.Log.Infof("1. utxo: %d(%d), sats %d, descend %d, address %d", utxoCount, nonZeroUtxo, satsInUtxo, descendSats, addressInUtxo)

	satsInAddress := int64(0)
	allAddressCount := 0
	allutxoInAddress := 0
	nonZeroUtxoInAddress := 0
	addressesInT2 := make(map[uint64]bool, 0)
	utxosInT2 := make(map[uint64]bool, 0)
	startTime2 = time.Now()
	common.Log.Infof("calculating in %s table ...", indexer.DB_KEY_ADDRESSV2)
	b.db.BatchRead([]byte(indexer.DB_KEY_ADDRESSV2), false, func(k, v []byte) error {

		var value indexer.AddressValueInDBV2
		err := db.DecodeBytes(v, &value)
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
		}
		if len(value.Utxos) > 0 {
			addressesInT2[value.AddressId] = true
		}

		return nil
	})
	allAddressCount = len(addressesInT2)
	nonZeroUtxoInAddress = len(utxosInT2)

	common.Log.Infof("%s table takes %v", common.DB_KEY_ADDRESSVALUE, time.Since(startTime2))
	common.Log.Infof("2. utxo: %d(%d), sats %d, address %d", allutxoInAddress, nonZeroUtxoInAddress, satsInAddress, allAddressCount)

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
		str, _ := db.GetAddressByIDFromDB(b.db, uid)
		common.Log.Infof("%s", str)
	}

	common.Log.Infof("address not in table %s", common.DB_KEY_UTXO)
	utxos4 := findDifferentItems(addressesInT2, addressesInT1)
	for uid := range utxos4 {
		str, _ := db.GetAddressByIDFromDB(b.db, uid)
		common.Log.Infof("%s", str)
	}

	if len(utxos1) > 0 || len(utxos2) > 0 || len(utxos3) > 0 || len(utxos4) > 0 {
		common.Log.Panic("utxos or address differents")
	}

	if addressInUtxo != allAddressCount {
		common.Log.Panicf("address count different %d %d", addressInUtxo, allAddressCount)
	}

	if nonZeroUtxo != nonZeroUtxoInAddress {
		common.Log.Panicf("utxo different %d %d", nonZeroUtxo, nonZeroUtxoInAddress)
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

// map1不存在map2的key
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
	b.db.BatchRead([]byte(common.DB_KEY_UTXO), false, func(k, v []byte) error {

		var value common.UtxoValueInDB
		err := db.DecodeBytes(v, &value)
		if err != nil {
			common.Log.Errorf("item.Value error: %v", err)
			return err
		}

		// 用于打印不存在table2中的utxo
		if _, ok := utxos[value.UtxoId]; ok {
			str, err := db.GetUtxoByDBKey(k)
			if err == nil {
				common.Log.Infof("%x %s %d", value.UtxoId, str, value.Value)
				result[value.UtxoId] = str
			}

			delete(utxos, value.UtxoId)
			if len(utxos) == 0 {
				return nil
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

func (b *BaseIndexer) GetBaseDB() indexer.KVDB {
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
	err := db.GobSetDB([]byte(SyncStatsKey), b.stats, b.db)
	if err != nil {
		common.Log.Panicf("Error setting in db %v", err)
	}
}

func (b *BaseIndexer) GetBlockHistory() int {
	return b.keepBlockHistory
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
