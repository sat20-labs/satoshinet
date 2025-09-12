package stp

import (
	"fmt"
	"strconv"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	db "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
)

const (
	DB_KEY_ASCEND        = "xa-"
	DB_KEY_DESCEND       = "xd-"
	DB_KEY_REFERRER      = "rer-"
	DB_KEY_REFERREE      = "ree-"
	DB_KEY_TICKINFO      = "t-"
	DB_KEY_TICKER_HOLDER = "th-"
	DB_KEY_CHANNEL       = "c-" // c-address
	DB_KEY_CORENODES     = "cns-all"
)

func GetAscendDBKey(fundingUtxo string) []byte {
	return []byte(DB_KEY_ASCEND + fundingUtxo)
}

func GetDescendDBKey(nullDataUtxo string) []byte {
	return []byte(DB_KEY_DESCEND + nullDataUtxo)
}

func GetReferrerDBKey(address string) []byte {
	return []byte(DB_KEY_REFERRER + address)
}

func GetReferreeDBKey(name string) []byte {
	return []byte(DB_KEY_REFERREE + name)
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

func GetAscendFromDB(ldb indexer.KVDB, fundingUtxo string) (*common.AscendData, error) {
	var result common.AscendData

	v, err := ldb.Read(GetAscendDBKey(fundingUtxo))
	if err != nil {
		//common.Log.Errorf("GetAscendFromDB %s error: %v", fundingUtxo, err)
		return nil, err
	}

	err = db.DecodeBytes(v, &result)
	if err != nil {
		return nil, err
	}
	return &result, err
}

func GetDescendFromDB(ldb indexer.KVDB, nullDataUtxo string) (*common.DescendData, error) {
	var result common.DescendData

	v, err := ldb.Read(GetDescendDBKey(nullDataUtxo))
	if err != nil {
		common.Log.Errorf("GetDescendFromDB %s error: %v", nullDataUtxo, err)
		return nil, err
	}

	err = db.DecodeBytes(v, &result)
	if err != nil {
		return nil, err
	}
	return &result, err
}

func GetReferrerFromDB(ldb indexer.KVDB, address string) (*common.ReferrerInfo, error) {
	var result common.ReferrerInfo

	v, err := ldb.Read(GetReferrerDBKey(address))
	if err != nil {
		//common.Log.Errorf("GetAscendFromDB %s error: %v", fundingUtxo, err)
		return nil, err
	}

	err = db.DecodeBytes(v, &result)
	if err != nil {
		return nil, err
	}
	return &result, err
}

func GetReferreeFromDB(ldb indexer.KVDB, name string) (map[uint64]int, error) {
	var result map[uint64]int

	v, err := ldb.Read(GetReferreeDBKey(name))
	if err != nil {
		//common.Log.Errorf("GetAscendFromDB %s error: %v", fundingUtxo, err)
		return nil, err
	}

	err = db.DecodeBytes(v, &result)
	if err != nil {
		return nil, err
	}
	return result, err
}

func GetReferreesFromDB(ldb indexer.KVDB, referrers []string) (map[string]map[uint64]int, error) {
	result := make(map[string]map[uint64]int)

	ldb.View(func(txn indexer.ReadBatch) error {
		for _, name := range referrers {
			v, err := txn.Get(GetReferreeDBKey(name))
			if err != nil {
				//common.Log.Errorf("GetAscendFromDB %s error: %v", fundingUtxo, err)
				continue
			}
			var referees map[uint64]int

			err = db.DecodeBytes(v, &referees)
			if err != nil {
				continue
			}
			result[name] = referees
		}
		return nil
	})

	return result, nil
}

func GetTickerInfoFromDB(ldb indexer.KVDB, assetName string) (*common.TickerInfo, error) {
	var result common.TickerInfo

	v, err := ldb.Read(GetTickerInfoDBKey(assetName))
	if err != nil {
		common.Log.Errorf("GetTickerInfoFromDB %s error: %v", assetName, err)
		return nil, err
	}

	err = db.DecodeBytes(v, &result)
	if err != nil {
		return nil, err
	}
	return &result, err
}

func GetAllTickerInfoFromDB(ldb indexer.KVDB) map[string]*common.TickerInfo {

	result := make(map[string]*common.TickerInfo, 0)
	ldb.BatchRead([]byte(DB_KEY_TICKINFO), false, func(k, v []byte) error {
		// 设置前缀扫描选项

		var info common.TickerInfo
		err := db.DecodeBytes(v, &info)
		if err == nil {
			result[info.String()] = &info
		} else {
			common.Log.Errorln("DecodeBytes " + err.Error())
		}

		return nil
	})

	return result
}

func GetTickerHolderInfoFromDBTxn(txn indexer.ReadBatch, assetName string, addressId uint64) (*indexer.Decimal, error) {
	var result string

	key := GetHolderInfoDBKey(assetName, addressId)
	v, err := txn.Get(key)
	if err != nil {
		//common.Log.Errorf("GetTickerHolderInfoFromDBTxn %s error: %v", string(key), err)
		return nil, err
	}

	err = db.DecodeBytes(v, &result)
	if err != nil {
		return nil, err
	}
	return indexer.NewDecimalFromFormatString(result)
}

func GetTickerHolderInfoFromDB(ldb indexer.KVDB, assetName string, addressId uint64) (*indexer.Decimal, error) {
	var result string

	key := GetHolderInfoDBKey(assetName, addressId)
	v, err := ldb.Read(key)
	if err != nil {
		//common.Log.Errorf("GetTickerHolderInfoFromDBTxn %s error: %v", string(key), err)
		return nil, err
	}

	err = db.DecodeBytes(v, &result)
	if err != nil {
		return nil, err
	}
	return indexer.NewDecimalFromFormatString(result)
}

func GetTickerHoldersFromDB(ldb indexer.KVDB, assetName string) map[uint64]*indexer.Decimal {
	result := make(map[uint64]*indexer.Decimal, 0)
	ldb.BatchRead([]byte(DB_KEY_TICKER_HOLDER+assetName), false, func(k, v []byte) error {

		key := string(k)
		parts := strings.Split(key, "-")
		if len(parts) != 3 {
			return nil
		}
		id, err := strconv.ParseUint(parts[2], 16, 64)
		if err != nil {
			common.Log.Errorf("ParseUint %s failed, %v", parts[2], err)
			return nil
		}

		var amt string
		err = db.DecodeBytes(v, &amt)
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

		return nil
	})

	return result
}

func GetChannelInfoFromDB(ldb indexer.KVDB, address string) (*common.ChannelInfoInDB, error) {
	var result common.ChannelInfoInDB

	key := GetChannelDBKey(address)
	v, err := ldb.Read(key)
	if err != nil {
		common.Log.Errorf("GetChannelInfoFromDB %s error: %v", string(key), err)
		return nil, err
	}

	err = db.DecodeBytes(v, &result)
	if err != nil {
		return nil, err
	}
	return &result, err
}

func GetAllChannelFromDB(ldb indexer.KVDB) map[string]*common.ChannelInfo {

	result := make(map[string]*common.ChannelInfo, 0)
	ldb.BatchRead([]byte(DB_KEY_CHANNEL), false, func(k, v []byte) error {
		var info common.ChannelInfo
		err := db.DecodeBytes(v, &info.ChannelInfoInDB)
		if err == nil {
			result[info.Address] = &info
		} else {
			common.Log.Errorln("DecodeBytes " + err.Error())
		}

		return nil
	})

	return result
}

func GetAllCoreNodeFromDB(ldb indexer.KVDB, chainParam *chaincfg.Params) map[string]*common.CoreNodeInfo {
	result := make(map[string]*common.CoreNodeInfo)

	key := GetAllCoreNodeDBKey()
	v, err := ldb.Read(key)
	if err == nil {
		err = db.DecodeBytes(v, &result)
		if err != nil {
			common.Log.Errorf("DecodeBytes error: %v", err)
		}
	}

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
