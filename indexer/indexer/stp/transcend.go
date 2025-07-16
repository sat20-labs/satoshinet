package stp

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dgraph-io/badger/v4"
	indexer "github.com/sat20-labs/indexer/common"
	db "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
)

const (
	DB_KEY_ASCEND    = "xa-"
	DB_KEY_DESCEND   = "xd-"
	DB_KEY_TICKINFO  = "t-"
	DB_KEY_TICKER_HOLDER = "th-"
	DB_KEY_CHANNEL   = "c-" // c-address
	DB_KEY_CORENODES = "cns-all"
)

func GetAscendDBKey(fundingUtxo string) []byte {
	return []byte(DB_KEY_ASCEND + fundingUtxo)
}

func GetDescendDBKey(nullDataUtxo string) []byte {
	return []byte(DB_KEY_DESCEND + nullDataUtxo)
}

func GetTickerInfoDBKey(assetName string) []byte {
	return []byte(DB_KEY_TICKINFO + assetName)
}

func GetHolderInfoDBKey(assetName string, addressId uint64) []byte {
	return []byte(fmt.Sprintf("%s%s-%x", DB_KEY_TICKER_HOLDER, assetName, addressId))
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


func GetAllTickerInfoFromDB(ldb *badger.DB) map[string]*common.TickerInfo {
	count := 0

	result := make(map[string]*common.TickerInfo, 0)
	ldb.View(func(txn *badger.Txn) error {
		// 设置前缀扫描选项
		prefixBytes := []byte(DB_KEY_TICKINFO)
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

			var info common.TickerInfo
			value, err := item.ValueCopy(nil)
			if err != nil {
				common.Log.Errorln("ValueCopy " + key + " " + err.Error())
			} else {
				err = db.DecodeBytes(value, &info)
				if err == nil {
					result[info.String()] = &info
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


func GetTickerHolderInfoFromDBTxn(txn *badger.Txn, assetName string, addressId uint64) (*indexer.Decimal, error) {
	var result string
	
	key := GetHolderInfoDBKey(assetName, addressId)
	item, err := txn.Get(key)
	if err != nil {
		//common.Log.Errorf("GetTickerHolderInfoFromDBTxn %s error: %v", string(key), err)
		return nil, err
	}
	err = item.Value(func(v []byte) error {
		return db.DecodeBytes(v, &result)
	})
	if err != nil {
		return nil, err
	}
	return indexer.NewDecimalFromFormatString(result)
}

func GetTickerHolderInfoFromDB(ldb *badger.DB, assetName string, addressId uint64) (*indexer.Decimal, error) {
	var result *indexer.Decimal
	err := ldb.View(func(txn *badger.Txn) error {
		var err error
		result, err = GetTickerHolderInfoFromDBTxn(txn, assetName, addressId)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func GetTickerHoldersFromDB(ldb *badger.DB, assetName string) map[uint64]*indexer.Decimal {
	result := make(map[uint64]*indexer.Decimal, 0)
	ldb.View(func(txn *badger.Txn) error {
		// 设置前缀扫描选项
		prefixBytes := []byte(DB_KEY_TICKER_HOLDER+assetName)
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
			parts := strings.Split(key, "-")
			if len(parts) != 3 {
				continue
			}
			id, err := strconv.ParseUint(parts[2], 16, 64)
			if err != nil {
				common.Log.Errorf("ParseUint %s failed, %v", parts[2], err)
				continue
			}

			var amt string
			value, err := item.ValueCopy(nil)
			if err != nil {
				common.Log.Errorln("ValueCopy " + key + " " + err.Error())
			} else {
				err = db.DecodeBytes(value, &amt)
				if err == nil {
					dAmt, err := indexer.NewDecimalFromFormatString(amt)
					if err != nil {
						common.Log.Errorf("NewDecimalFromFormatString %s failed, %v", amt, err)
					} else if dAmt.Sign() > 0 {
						result[id] = dAmt
					}
				} else {
					common.Log.Errorln("DecodeBytes " + err.Error())
				}
			}
		}
		return nil
	})

	return result
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

func GetAllCoreNodeFromDB(ldb *badger.DB, chainParam *chaincfg.Params) map[string]*common.CoreNodeInfo {
	result := make(map[string]*common.CoreNodeInfo)
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
		bootstrapNode := common.NewCoreNodeInfo(nil)
		result[indexer.GetBootstrapPubKey()] = bootstrapNode

		corenode := common.NewCoreNodeInfo(nil)
		corenode.ServerNode = indexer.GetBootstrapPubKey()
		corenode.ChannelAddr, _ = common.GetDefaultChannelAddress(chainParam)
		result[indexer.GetCoreNodePubKey()] = corenode

		bootstrapNode.ChildMiners[indexer.GetCoreNodePubKey()] = ""
	}

	return result
}
