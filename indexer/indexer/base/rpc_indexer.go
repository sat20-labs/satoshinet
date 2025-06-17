package base

import (
	"fmt"
	"sync"

	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/stp"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"

	"github.com/dgraph-io/badger/v4"
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
	addressValueMap    map[string]*indexer.AddressValueInDB
	addressIdMap       map[uint64]string
	deletedUtxoMap     map[uint64]bool
	bSearching         bool
	satSearchingStatus map[int64]*SatSearchingStatus
}

func NewRpcIndexer(base *BaseIndexer) *RpcIndexer {
	indexer := &RpcIndexer{
		BaseIndexer:        *base.Clone(),
		addressValueMap:    make(map[string]*indexer.AddressValueInDB),
		addressIdMap:       make(map[uint64]string),
		deletedUtxoMap:     make(map[uint64]bool),
		bSearching:         false,
		satSearchingStatus: make(map[int64]*SatSearchingStatus),
	}

	return indexer
}

// 仅用于前端RPC数据查询时，更新地址数据
func (b *RpcIndexer) UpdateServiceInstance() {
	b.addressValueMap = b.prefechAddress()
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
	err := b.db.View(func(txn *badger.Txn) error {
		key := db.GetUTXODBKey(utxo)
		//err := db.GetValueFromDB(key, txn, output)
		err := db.GetValueFromDB(key, txn, output)
		if err != nil {
			indexer.Log.Warningf("GetOrdinalsForUTXO %s failed, %v", utxo, err)
			return err
		}

		return nil
	})

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
	err := b.db.View(func(txn *badger.Txn) error {
		key := db.GetUTXODBKey(utxo)
		//err := db.GetValueFromDB(key, txn, output)
		err := db.GetValueFromDB(key, txn, output)
		if err != nil {
			indexer.Log.Warningf("GetOrdinalsForUTXO %s failed, %v", utxo, err)
			return err
		}
		return nil
	})

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
func (b *RpcIndexer) getAddressValue2(address string, txn *badger.Txn) *indexer.AddressValueInDB {
	result := &indexer.AddressValueInDB{AddressId: indexer.INVALID_ID}
	addressId, err := db.GetAddressIdFromDBTxn(txn, address)
	if err == nil {
		utxos := make(map[uint64]*indexer.UtxoValue)
		prefix := []byte(fmt.Sprintf("%s%x-", indexer.DB_KEY_ADDRESSVALUE, addressId))
		itr := txn.NewIterator(badger.DefaultIteratorOptions)
		defer itr.Close()

		for itr.Seek(prefix); itr.ValidForPrefix(prefix); itr.Next() {
			item := itr.Item()
			if item.IsDeletedOrExpired() {
				continue
			}
			value := int64(0)
			item.Value(func(data []byte) error {
				value = int64(indexer.BytesToUint64(data))
				return nil
			})

			newAddressId, utxoId, typ, _, err := indexer.ParseAddressIdKey(string(item.Key()))
			if err != nil {
				indexer.Log.Panicf("ParseAddressIdKey %s failed: %v", string(item.Key()), err)
			}
			if newAddressId != addressId {
				indexer.Log.Panicf("ParseAddressIdKey %s get different addressid %d, %d", string(item.Key()), newAddressId, addressId)
			}
			result.AddressType = uint32(typ)

			utxos[utxoId] = &indexer.UtxoValue{Op: 0, Value: value}
		}

		result.AddressId = addressId
		result.Op = 0
		result.Utxos = utxos
	}

	b.mutex.RLock()
	value, ok := b.addressValueMap[address]
	if ok {
		result.AddressType = value.AddressType
		result.AddressId = value.AddressId
		if result.Utxos == nil {
			result.Utxos = make(map[uint64]*indexer.UtxoValue)
		}
		// 过滤已经删除的utxo
		for k, v := range value.Utxos {
			if v.Op > 0 {
				result.Utxos[k] = v
			} else if v.Op < 0 {
				delete(result.Utxos, k)
			}
		}
	}
	b.mutex.RUnlock()

	if result.AddressId == indexer.INVALID_ID {
		return nil
	}

	return result
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

	address, err := db.GetAddressByID(b.db, id)
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

	id, err := db.GetAddressIdFromDB(b.db, address)
	if err != nil {
		id, _ = b.BaseIndexer.getAddressId(address)
		if id != indexer.INVALID_ID {
			err = nil
		} else {
			indexer.Log.Infof("getAddressId %s failed.", address)
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
func (b *RpcIndexer) GetUTXOs(address string) (map[uint64]int64, error) {
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

func (b *RpcIndexer) getUtxosWithAddress(address string) (*indexer.AddressValue, error) {
	var addressValueInDB *indexer.AddressValueInDB
	b.db.View(func(txn *badger.Txn) error {
		addressValueInDB = b.getAddressValue2(address, txn)
		return nil
	})

	value := &indexer.AddressValue{}
	value.Utxos = make(map[uint64]int64)
	if addressValueInDB == nil {
		indexer.Log.Infof("RpcIndexer.getUtxosWithAddress-> No address %s found in db", address)
		return value, nil
	}

	value.AddressId = addressValueInDB.AddressId
	for utxoid, utxovalue := range addressValueInDB.Utxos {
		value.Utxos[utxoid] = utxovalue.Value
	}
	return value, nil
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
	err := b.db.View(func(txn *badger.Txn) error {
		return db.GetValueFromDB(key, txn, &block)
	})
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
	info, ok := b.utxoIndex.AscendMap[fundingUtxo]
	b.mutex.RUnlock()
	if ok {
		return info
	}

	info, err := stp.GetAscendFromDB(b.db, fundingUtxo)
	if err != nil {
		common.Log.Errorf("GetAscendFromDB %s failed, %v", fundingUtxo, err)
		return nil
	}
	b.mutex.Lock()
	b.utxoIndex.AscendMap[fundingUtxo] = info
	b.mutex.Unlock()
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
		return &common.TickerInfo{
			AssetName: *ticker,
			MaxSupply: "21000000000000000",
			Precition: 0,
			N:         1,
		}
	}

	b.mutex.Lock()
	b.tickInfoMap[ticker.String()] = info
	b.mutex.Unlock()

	return info
}

// only for RPC interface
func (b *RpcIndexer) GetAllCoreNode() map[string]int {
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
