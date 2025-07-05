package stp

import (
	"github.com/dgraph-io/badger/v4"
	db "github.com/sat20-labs/indexer/indexer/db"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/common"
)

const (
	DB_KEY_ASCEND    = "xa-"
	DB_KEY_DESCEND   = "xd-"
	DB_KEY_TICKINFO  = "t-"
	DB_KEY_CHANNEL   = "c-" // c-address
	DB_KEY_CORENODES = "cns-all"
)

type CoreNodeInfo struct {
	AscendHeight int	//
	DescendHeight int
	ChildMiners map[string]int // pubkey
}

func NewCoreNodeInfo(h int) *CoreNodeInfo {
	return &CoreNodeInfo{
		AscendHeight: h,
		DescendHeight: -1,
		ChildMiners: make(map[string]int),
	}
}

func GetAscendDBKey(fundingUtxo string) []byte {
	return []byte(DB_KEY_ASCEND + fundingUtxo)
}

func GetDescendDBKey(nullDataUtxo string) []byte {
	return []byte(DB_KEY_ASCEND + nullDataUtxo)
}

func GetTickerInfoDBKey(assetName string) []byte {
	return []byte(DB_KEY_ASCEND + assetName)
}

func GetChannelDBKey(addr string) []byte {
	return []byte(DB_KEY_CHANNEL + addr)
}

func GetAllCoreNodeDBKey() []byte {
	return []byte(DB_KEY_CORENODES)
}

func GetAscendFromDB(ldb *badger.DB, fundingUtxo string) (*common.AscendData, error) {
	var result common.AscendData
	err := ldb.View(func(txn *badger.Txn) error {
		item, err := txn.Get(GetAscendDBKey(fundingUtxo))
		if err != nil {
			//common.Log.Errorf("GetAscendFromDB %s error: %v", fundingUtxo, err)
			return err
		}
		return item.Value(func(v []byte) error {
			return db.DecodeBytes(v, &result)
		})
	})
	if err != nil {
		return nil, err
	}
	return &result, err
}

func GetDescendFromDB(ldb *badger.DB, nullDataUtxo string) (*common.DescendData, error) {
	var result common.DescendData
	err := ldb.View(func(txn *badger.Txn) error {
		item, err := txn.Get(GetDescendDBKey(nullDataUtxo))
		if err != nil {
			common.Log.Errorf("GetDescendFromDB %s error: %v", nullDataUtxo, err)
			return err
		}
		return item.Value(func(v []byte) error {
			return db.DecodeBytes(v, &result)
		})
	})
	if err != nil {
		return nil, err
	}
	return &result, err
}

func GetTickerInfoFromDB(ldb *badger.DB, assetName string) (*common.TickerInfo, error) {
	var result common.TickerInfo
	err := ldb.View(func(txn *badger.Txn) error {
		item, err := txn.Get(GetTickerInfoDBKey(assetName))
		if err != nil {
			common.Log.Errorf("GetTickerInfoFromDB %s error: %v", assetName, err)
			return err
		}
		return item.Value(func(v []byte) error {
			return db.DecodeBytes(v, &result)
		})
	})
	if err != nil {
		return nil, err
	}
	return &result, err
}

func GetChannelInfoFromDB(ldb *badger.DB, address string) (*common.ChannelInfoInDB, error) {
	var result common.ChannelInfoInDB
	err := ldb.View(func(txn *badger.Txn) error {
		key := GetChannelDBKey(address)
		item, err := txn.Get(key)
		if err != nil {
			common.Log.Errorf("GetChannelInfoFromDB %s error: %v", string(key), err)
			return err
		}
		return item.Value(func(v []byte) error {
			return db.DecodeBytes(v, &result)
		})
	})
	if err != nil {
		return nil, err
	}
	return &result, err
}

func GetAllChannelFromDB(ldb *badger.DB) map[string]*common.ChannelInfo {
	count := 0

	result := make(map[string]*common.ChannelInfo, 0)
	ldb.View(func(txn *badger.Txn) error {
		// 设置前缀扫描选项
		prefixBytes := []byte(DB_KEY_CHANNEL)
		prefixOptions := badger.DefaultIteratorOptions
		prefixOptions.Prefix = prefixBytes

		// 使用前缀扫描选项创建迭代器
		it := txn.NewIterator(prefixOptions)
		defer it.Close()

		// 遍历匹配前缀的key
		for it.Seek(prefixBytes); it.ValidForPrefix(prefixBytes); it.Next() {
			item := it.Item()
			if item.IsDeletedOrExpired() {
				continue
			}
			key := string(item.Key())

			var info common.ChannelInfo
			value, err := item.ValueCopy(nil)
			if err != nil {
				common.Log.Errorln("ValueCopy " + key + " " + err.Error())
			} else {
				err = db.DecodeBytes(value, &info.ChannelInfoInDB)
				if err == nil {
					result[info.Address] = &info
				} else {
					common.Log.Errorln("DecodeBytes " + err.Error())
				}
			}

			count++
		}
		return nil
	})

	return result
}

func GetAllCoreNodeFromDB(ldb *badger.DB) map[string]*CoreNodeInfo {
	result := make(map[string]*CoreNodeInfo)
	ldb.View(func(txn *badger.Txn) error {
		key := GetAllCoreNodeDBKey()
		item, err := txn.Get(key)
		if err != nil {
			common.Log.Errorf("GetAllCoreNodeFromDB error: %v", err)
			return err
		}
		return item.Value(func(v []byte) error {
			return db.DecodeBytes(v, &result)
		})
	})

	if len(result) == 0 {
		result[indexer.GetBootstrapPubKey()] = NewCoreNodeInfo(0)
		result[indexer.GetCoreNodePubKey()] = NewCoreNodeInfo(0)
	}

	return result
}
