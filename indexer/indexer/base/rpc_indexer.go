package base

import (
	"fmt"
	"sync"

	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/stp"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/db"
)

type SatSearchingStatus struct {
	Utxo    string
	Address string
	Status  int // 0 finished; 1 searching; -1 error.
	Ts      int64
}

type RpcIndexer struct {
	BaseIndexer

	// 接收前端api访问的实例，隔离内存访问
	mutex              sync.RWMutex
	addressIdMap       map[uint64]string
	deletedUtxoMap     map[uint64]bool
	bSearching         bool
	satSearchingStatus map[int64]*SatSearchingStatus
}

func NewRpcIndexer(base *BaseIndexer) *RpcIndexer {
	indexer := &RpcIndexer{
		BaseIndexer:        *base.Clone(),
		addressIdMap:       make(map[uint64]string),
		deletedUtxoMap:     make(map[uint64]bool),
		bSearching:         false,
		satSearchingStatus: make(map[int64]*SatSearchingStatus),
	}

	return indexer
}

// 仅用于前端RPC数据查询时，更新地址数据
func (b *RpcIndexer) UpdateServiceInstance() {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	b.addressIdMap = make(map[uint64]string)
	for k, v := range b.addressValueMap {
		b.addressIdMap[v.AddressId] = k
	}
	for _, v := range b.delUTXOs {
		b.deletedUtxoMap[v.UtxoId] = true
	}
}

// sync
func (b *RpcIndexer) GetOrdinalsWithUtxo(utxo string) (uint64, wire.TxAssets, error) {

	// 有可能还没有写入数据库，所以先读缓存
	utxoInfo, ok := b.utxoIndex.Index[utxo]
	if ok {
		return common.GetUtxoId(utxoInfo), utxoInfo.Assets, nil
	}

	if err := indexer.CheckUtxoFormat(utxo); err != nil {
		return 0, nil, err
	}

	output := &common.UtxoValueInDB{}

	key := db.GetUTXODBKey(utxo)
	//err := db.GetValueFromDB(key, txn, output)
	err := db.GetValueFromDB(key, output, b.db)
	if err != nil {
		indexer.Log.Warningf("GetOrdinalsForUTXO %s failed, %v", utxo, err)
		return 0, nil, err
	}

	if err != nil {
		return indexer.INVALID_ID, nil, err
	}

	_, ok = b.deletedUtxoMap[output.UtxoId]
	if ok {
		return 0, nil, fmt.Errorf("utxo %s is spent", utxo)
	}

	return output.UtxoId, output.Assets, nil
}

func (b *RpcIndexer) GetUtxoInfo(utxo string) (*common.UtxoInfo, error) {

	// 有可能还没有写入数据库，所以先读缓存
	utxoInfo, ok := b.utxoIndex.Index[utxo]
	if ok {
		value := &common.UtxoInfo{
			UtxoId:   common.GetUtxoId(utxoInfo),
			Value:    utxoInfo.Value,
			PkScript: utxoInfo.Address.PkScript,
			Assets:   utxoInfo.Assets,
		}
		return value, nil
	}

	if err := indexer.CheckUtxoFormat(utxo); err != nil {
		return nil, err
	}

	output := &common.UtxoValueInDB{}

	key := db.GetUTXODBKey(utxo)
	//err := db.GetValueFromDB(key, txn, output)
	err := db.GetValueFromDB(key, output, b.db)
	if err != nil {
		indexer.Log.Warningf("GetOrdinalsForUTXO %s failed, %v", utxo, err)
		return nil, err
	}

	if err != nil {
		return nil, err
	}

	_, ok = b.deletedUtxoMap[output.UtxoId]
	if ok {
		return nil, fmt.Errorf("utxo %s is spent", utxo)
	}

	info := common.UtxoInfo{}
	var pkScript []byte
	addrType := output.AddressType
	reqSig := output.ReqSig
	if addrType == uint16(txscript.MultiSigTy) {
		var addresses []string
		for _, id := range output.AddressIds {
			addr, err := b.GetAddressByID(id)
			if err != nil {
				return nil, err
			}
			addresses = append(addresses, addr)
		}
		pkScript, err = indexer.MultiSigToPkScript(int(reqSig), addresses, b.IsMainnet())
		if err != nil {
			return nil, err
		}
	} else if addrType == uint16(txscript.NullDataTy) {
		pkScript, _ = txscript.NullDataScript(nil)
	} else {
		addr, err := b.GetAddressByID(output.AddressIds[0])
		if err != nil {
			return nil, err
		}
		pkScript, err = indexer.AddressToPkScript(addr, b.IsMainnet())
		if err != nil {
			return nil, err
		}
	}

	info.UtxoId = output.UtxoId
	info.Value = output.Value
	info.PkScript = pkScript
	info.Assets = output.Assets

	return &info, nil
}

// only for api access
func (b *RpcIndexer) getAddressValue2(address string, ldb indexer.KVDB) *indexer.AddressValueV2 {
	b.mutex.RLock()
	value, ok := b.addressValueMap[address]
	if !ok {
		data, err := db.GetAddressDataFromDBV2(ldb, address)
		if err == nil {
			value = data.ToAddressValueV2()
			b.addressValueMap[address] = value
			ok = true
		}
	}
	b.mutex.RUnlock()

	return value
}

// only for RPC interface
func (b *RpcIndexer) GetUtxoByID(id uint64) (string, error) {
	utxo, err := db.GetUtxoByID(b.db, id)
	if err != nil {
		for key, value := range b.utxoIndex.Index {
			if common.GetUtxoId(value) == id {
				return key, nil
			}
		}
		indexer.Log.Errorf("RpcIndexer->GetUtxoByID %d failed, err: %v", id, err)
	}

	return utxo, err
}

// only for RPC interface
func (b *RpcIndexer) GetAddressByID(id uint64) (string, error) {
	b.mutex.RLock()
	addrStr, ok := b.addressIdMap[id]
	b.mutex.RUnlock()
	if ok {
		return addrStr, nil
	}

	address, err := db.GetAddressByIDFromDB(b.db, id)
	if err != nil {
		common.Log.Errorf("RpcIndexer->GetAddressByID %d failed, err: %v", id, err)
		return "", err
	}

	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.addressIdMap[id] = address

	return address, err
}

// only for RPC interface
func (b *RpcIndexer) GetAddressId(address string) uint64 {
	b.mutex.RLock()
	id, _ := b.getAddressId(address)
	b.mutex.RUnlock()
	if id == indexer.INVALID_ID {
		data, err := db.GetAddressDataFromDBV2(b.db, address)
		if err == nil {
			b.mutex.Lock()
			value := data.ToAddressValueV2()
			b.addressValueMap[address] = value
			id = value.AddressId
			b.mutex.Unlock()
		}
	}

	return id
}

func (b *RpcIndexer) GetOrdinalsWithUtxoId(id uint64) (string, wire.TxAssets, error) {
	utxo, err := b.GetUtxoByID(id)
	if err != nil {
		return "", nil, err
	}
	_, result, err := b.GetOrdinalsWithUtxo(utxo)
	return utxo, result, err
}

// key: utxoId, value: btc value
func (b *RpcIndexer) GetUTXOs(address string) (map[uint64]bool, error) {
	addrValue, err := b.getUtxosWithAddress(address)
	if err != nil {
		return nil, err
	}
	return addrValue.Utxos, nil
}

// only for RPC
func (b *RpcIndexer) GetUTXOs2(address string) []string {
	addrValue, err := b.getUtxosWithAddress(address)

	if err != nil {
		indexer.Log.Errorf("getUtxosWithAddress %s failed, err %v", address, err)
		return nil
	}

	utxos := make([]string, 0)
	for utxoId := range addrValue.Utxos {
		utxo, err := b.GetUtxoByID(utxoId)
		if err != nil {
			indexer.Log.Errorf("GetUtxoByID failed. address %s, utxo id %d", address, utxoId)
			continue
		}
		utxos = append(utxos, utxo)
	}
	return utxos
}

func (b *RpcIndexer) getUtxosWithAddress(address string) (*indexer.AddressValueV2, error) {
	addressValueInDB := b.getAddressValue2(address, b.db)
	if addressValueInDB == nil {
		indexer.Log.Infof("RpcIndexer.getUtxosWithAddress-> No address %s found in db", address)
		return nil, fmt.Errorf("not found")
	}

	return addressValueInDB, nil
}

func (b *RpcIndexer) GetBlockInfo(height int) (*common.BlockInfo, error) {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	for _, block := range b.blockVector {
		if block.Height == height {
			info := common.BlockInfo{
				Height:     height,
				Timestamp:  block.Timestamp,
				InputUtxo:  int(block.InputUtxo),
				OutputUtxo: block.OutputUtxo,
				InputSats:  block.InputSats,
				OutputSats: block.OutputSats,
				TxAmount:   block.TxAmount,
			}
			return &info, nil
		}
	}

	key := db.GetBlockDBKey(height)
	block := indexer.BlockValueInDB{}
	err := db.GetValueFromDB(key, &block, b.db)
	if err != nil {
		return nil, err
	}

	info := common.BlockInfo{
		Height:     height,
		Timestamp:  block.Timestamp,
		InputUtxo:  int(block.InputUtxo),
		OutputUtxo: block.OutputUtxo,
		InputSats:  block.InputSats,
		OutputSats: block.OutputSats,
		TxAmount:   block.TxAmount,
	}
	return &info, nil

}

// only for RPC interface
func (b *RpcIndexer) GetAscendData(fundingUtxo string) *common.AscendData {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	return b.getAscendData(fundingUtxo)
}

// only for RPC interface
func (b *RpcIndexer) getAscendData(fundingUtxo string) *common.AscendData {
	info, ok := b.utxoIndex.AscendMap[fundingUtxo]
	if ok {
		return info
	}

	info, err := stp.GetAscendFromDB(b.db, fundingUtxo)
	if err != nil {
		//common.Log.Errorf("GetAscendFromDB %s failed, %v", fundingUtxo, err)
		return nil
	}
	return info
}

// only for RPC interface
func (b *RpcIndexer) GetDescendData(nullDataUtxo string) *common.DescendData {
	b.mutex.RLock()
	info, ok := b.utxoIndex.DescendMap[nullDataUtxo]
	b.mutex.RUnlock()
	if ok {
		return info
	}

	info, err := stp.GetDescendFromDB(b.db, nullDataUtxo)
	if err != nil {
		common.Log.Errorf("GetDescendFromDB %s failed, %v", nullDataUtxo, err)
		return nil
	}
	b.mutex.Lock()
	b.utxoIndex.DescendMap[nullDataUtxo] = info
	b.mutex.Unlock()
	return info
}

// only for RPC interface
func (b *RpcIndexer) GetReferrer(address string) (string, error) {
	b.mutex.RLock()
	referrer, ok := b.utxoIndex.ReferrerMap[address]
	b.mutex.RUnlock()
	if ok {
		return referrer, nil
	}

	referrer, err := stp.GetReferrerFromDB(b.db, address)
	if err != nil {
		common.Log.Errorf("GetReferrerFromDB %s failed, %v", address, err)
		return "", err
	}
	b.mutex.Lock()
	b.utxoIndex.ReferrerMap[address] = referrer
	b.mutex.Unlock()
	return referrer, nil
}

// only for RPC interface
func (b *RpcIndexer) GetReferree(name string) ([]string, error) {

	result := make([]string, 0)
	referrees, err := stp.GetReferreeFromDB(b.db, name)
	if err == nil {
		for _, addrId := range referrees {
			addr, err := b.GetAddressByID(addrId)
			if err != nil {
				common.Log.Errorf("can't find address by id %d", addrId)
				continue
			}
			result = append(result, addr)
		}
	}

	b.mutex.RLock()
	defer b.mutex.RUnlock()
	for addr, referrer := range b.utxoIndex.ReferrerMap {
		if referrer == name {
			result = append(result, addr)
		}
	}

	return result, nil
}

// only for RPC interface
func (b *RpcIndexer) GetTickerInfo(ticker *wire.AssetName) *common.TickerInfo {
	b.mutex.RLock()
	info, ok := b.tickInfoMap[ticker.String()]
	b.mutex.RUnlock()
	if ok {
		return info
	}

	info, err := stp.GetTickerInfoFromDB(b.db, ticker.String())
	if err != nil {
		common.Log.Errorf("GetTickerInfoFromDB %s failed, %v", ticker, err)
		return nil
	}

	b.mutex.Lock()
	b.tickInfoMap[ticker.String()] = info
	b.mutex.Unlock()

	return info
}

// only for RPC interface
func (b *RpcIndexer) GetAllCoreNode() map[string]*common.CoreNodeInfo {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	return b.coreNodeMap
}

// only for RPC interface
// 与引导节点建立通道并且将资产质押到通道中
func (b *RpcIndexer) IsCoreNode(pubkey string) bool {
	b.mutex.RLock()
	_, ok := b.coreNodeMap[pubkey]
	b.mutex.RUnlock()
	return ok
}

func (b *RpcIndexer) GetCoreNodeInfo(pubkey string) *common.CoreNodeInfo {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	info, ok := b.coreNodeMap[pubkey]
	if ok {
		return info.Clone()
	}

	return nil
}

// only for RPC interface
// 与核心节点建立通道并且将资产质押到通道中
func (b *RpcIndexer) IsMinerNode(pubkey string) bool {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	_, ok := b.coreNodeMap[pubkey]
	if ok {
		return true
	}

	for _, v := range b.coreNodeMap {
		_, ok := v.ChildMiners[pubkey]
		if ok {
			return true
		}
	}

	return false
}

func (b *RpcIndexer) GetMinerInfo(pubkey string) *common.MinerInfo {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	info, ok := b.coreNodeMap[pubkey]
	if ok {
		return &info.MinerInfo
	}

	for _, v := range b.coreNodeMap {
		ascendUtxo, ok := v.ChildMiners[pubkey]
		if ok {
			data := b.getAscendData(ascendUtxo)
			return data.ToMinerInfo()
		}
	}

	return nil
}

func (b *RpcIndexer) GetTickerMap() map[string]*common.TickerInfo {
	tickInfoMap := make(map[string]*common.TickerInfo)
	b.mutex.RLock()
	for k, v := range b.tickInfoMap {
		tickInfoMap[k] = v
	}
	b.mutex.RUnlock()

	tickerInDB := stp.GetAllTickerInfoFromDB(b.db)
	for k, v := range tickerInDB {
		_, ok := tickInfoMap[k]
		if !ok {
			tickInfoMap[k] = v
		}
	}
	return tickInfoMap
}

func (b *RpcIndexer) GetHoldersWithTick(tickerName *common.TickerName) map[string]*indexer.Decimal {
	result := make(map[string]*indexer.Decimal)

	b.mutex.RLock()
	addrmap, ok := b.tickAddressMap[tickerName.String()]
	b.mutex.RUnlock()
	if ok {
		for k, v := range addrmap {
			result[k] = v
		}
	}

	holdersInDB := stp.GetTickerHoldersFromDB(b.db, tickerName.String())
	for k, v := range holdersInDB {
		address, err := b.GetAddressByID(k)
		if err != nil {
			continue
		}
		_, ok := result[address]
		if !ok {
			result[address] = v
		}
	}

	return result
}
