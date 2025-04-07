package indexer

import (
	"fmt"
	"sort"

	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/indexer/common"
	shareIndexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	swire "github.com/sat20-labs/satoshinet/wire"

	indexer "github.com/sat20-labs/indexer/common"
)

type Model struct {
	indexer shareIndexer.Indexer
}

func NewModel(indexer shareIndexer.Indexer) *Model {
	return &Model{
		indexer: indexer,
	}
}

func (s *Model) getPlainUtxos(address string, value int64, start, limit int) ([]*indexerwire.PlainUtxo, int, error) {
	utxomap, err := s.indexer.GetUTXOsWithAddress(address)
	if err != nil {
		return nil, 0, err
	}
	avaibableUtxoList := make([]*indexerwire.PlainUtxo, 0)
	utxos := make([]*indexer.UtxoIdInDB, 0)
	for key, value := range utxomap {
		utxos = append(utxos, &indexer.UtxoIdInDB{UtxoId: key, Value: value})
	}

	// sort.Slice(utxos, func(i, j int) bool {
	// 	return utxos[i].Value > utxos[j].Value
	// })

	// // 分页显示
	totalRecords := len(utxos)
	// if totalRecords < start {
	// 	return nil, totalRecords, fmt.Errorf("start exceeds the count of UTXO")
	// }
	// if totalRecords < start+limit {
	// 	limit = totalRecords - start
	// }
	// end := start + limit
	// utxos = utxos[start:end]

	for _, utxoId := range utxos {
		//Indicates that this utxo has been spent and cannot be used for indexing
		utxo := s.indexer.GetUtxoById(utxoId.UtxoId)
		if utxo == "" {
			continue
		}

		if !IsAvailableUtxo(utxo) {
			continue
		}

		txid, vout, err := indexer.ParseUtxo(utxo)
		if err != nil {
			continue
		}

		//Find utxo with value
		if utxoId.Value >= value {
			avaibableUtxoList = append(avaibableUtxoList, &indexerwire.PlainUtxo{
				Txid:  txid,
				Vout:  vout,
				Value: utxoId.Value,
			})
		}
	}

	sort.Slice(avaibableUtxoList, func(i, j int) bool {
		return avaibableUtxoList[i].Value > avaibableUtxoList[j].Value
	})

	return avaibableUtxoList, totalRecords, nil
}

func (s *Model) getAllUtxos(address string, start, limit int) ([]*indexerwire.PlainUtxo, []*indexerwire.PlainUtxo, int, error) {
	utxomap, err := s.indexer.GetUTXOsWithAddress(address)
	if err != nil {
		return nil, nil, 0, err
	}

	utxos := make([]*indexer.UtxoIdInDB, 0)
	for key, value := range utxomap {
		utxos = append(utxos, &indexer.UtxoIdInDB{UtxoId: key, Value: value})
	}

	sort.Slice(utxos, func(i, j int) bool {
		return utxos[i].Value > utxos[j].Value
	})

	// // 分页显示
	totalRecords := len(utxos)
	// if totalRecords < start {
	// 	return nil, nil, totalRecords, fmt.Errorf("start exceeds the count of UTXO")
	// }
	// if totalRecords < start+limit {
	// 	limit = totalRecords - start
	// }
	// end := start + limit
	// utxos = utxos[start:end]

	plainUtxos := make([]*indexerwire.PlainUtxo, 0)
	otherUtxos := make([]*indexerwire.PlainUtxo, 0)

	for _, utxoId := range utxos {
		//Indicates that this utxo has been spent and cannot be used for indexing
		utxo := s.indexer.GetUtxoById(utxoId.UtxoId)
		if utxo == "" {
			continue
		}

		// 效率很低，需要内部实现内存池
		if IsExistingInMemPool(utxo) {
			continue
		}

		txid, vout, err := indexer.ParseUtxo(utxo)
		if err != nil {
			continue
		}

		//Find common utxo (that is, utxo with non-ordinal attributes)
		if shareIndexer.ShareIndexer.HasAssetInUtxo(utxo) {
			otherUtxos = append(otherUtxos, &indexerwire.PlainUtxo{
				Txid:  txid,
				Vout:  vout,
				Value: utxoId.Value,
			})
		} else {
			plainUtxos = append(plainUtxos, &indexerwire.PlainUtxo{
				Txid:  txid,
				Vout:  vout,
				Value: utxoId.Value,
			})
		}

	}

	return plainUtxos, otherUtxos, totalRecords, nil
}

func (s *Model) GetSyncHeight() int {
	return s.indexer.GetSyncHeight()
}

func (s *Model) GetBlockInfo(height int) (*common.BlockInfo, error) {
	return s.indexer.GetBlockInfo(height)
}

func (s *Model) GetAssetSummary(address string, start int, limit int) (*indexerwire.AssetSummary, error) {
	tickerMap := s.indexer.GetAssetSummaryInAddressV3(address)

	result := indexerwire.AssetSummary{}
	for tickName, amount := range tickerMap {
		resp := &swire.AssetInfo{}
		resp.Name = tickName
		resp.Amount = *amount.Clone()
		resp.BindingSat = uint32(s.indexer.GetBindingSat(&tickName))
		result.Data = append(result.Data, resp)
	}
	result.Start = 0
	result.Total = uint64(len(result.Data))

	sort.Slice(result.Data, func(i, j int) bool {
		return result.Data[i].Amount.Cmp(&result.Data[j].Amount) > 0
	})

	return &result, nil
}


func (s *Model) GetExistingUtxos(req *indexerwire.UtxosReq) ([]string, error) {
	result := make([]string, 0)
	for _, utxo := range req.Utxos {
		utxoId := s.indexer.GetUtxoId(utxo)
		if utxoId == indexer.INVALID_ID {
			continue
		}

		if IsExistingInMemPool(utxo) {
			continue
		}

		result = append(result, utxo)
	}

	return result, nil
}

func (s *Model) GetAscend(utxo string) (*common.AscendData, error) {
	data := s.indexer.GetAscendData(utxo)
	if data == nil {
		return nil, fmt.Errorf("GetAscendData %s failed", utxo)
	}

	return data, nil
}

func (s *Model) GetDescend(utxo string) (*common.DescendData, error) {
	data := s.indexer.GetDescendData(utxo)
	if data == nil {
		return nil, fmt.Errorf("GetDescendData %s failed", utxo)
	}

	return data, nil
}

func (s *Model) GetAllCoreNode() ([]string, error) {
	data := s.indexer.GetAllCoreNode()

	result := make([]string, 0)
	for k, _ := range data {
		result = append(result, k)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i] < result[j]
	})

	return result, nil
}

func (s *Model) CheckCoreNode(pubkey string) bool {
	return s.indexer.IsCoreNode(pubkey)
}

func (s *Model) GetAssetSummaryV3(address string, start int, limit int) ([]*indexer.DisplayAsset, error) {
	tickerMap := s.indexer.GetAssetSummaryInAddressV3(address)

	result := make([]*indexer.DisplayAsset, 0)
	for tickName, balance := range tickerMap {
		resp := &indexer.DisplayAsset{}
		resp.AssetName = tickName
		resp.Amount = balance.String()
		resp.BindingSat = (s.indexer.GetBindingSat(&tickName))
		result = append(result, resp)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Amount > result[j].Amount
	})

	return result, nil
}

func (s *Model) GetUtxoInfoV3(utxo string) (*indexer.AssetsInUtxo, error) {
	if IsExistingInMemPool(utxo) {
		return nil, fmt.Errorf("utxo %s is in mempool", utxo)
	}
	ret := s.indexer.GetTxOutputWithUtxoV3(utxo)
	if ret == nil {
		return nil, fmt.Errorf("can't find utxo %s", utxo)
	}
	return ret, nil
}

func (s *Model) GetUtxoInfoListV3(req *indexerwire.UtxosReq) ([]*indexer.AssetsInUtxo, error) {
	result := make([]*indexer.AssetsInUtxo, 0)
	for _, utxo := range req.Utxos {
		txOutput, err := s.GetUtxoInfoV3(utxo)
		if err != nil {
			continue
		}

		result = append(result, txOutput)
	}

	return result, nil
}

// name == * , 返回所有utxo
func (s *Model) GetUtxosWithAssetNameV3(address, name string, start, limit int) ([]*indexer.AssetsInUtxo, int, error) {
	result := make([]*indexer.AssetsInUtxo, 0)
	assetName := swire.NewAssetNameFromString(name)
	outputMap, err := s.indexer.GetAssetUTXOsInAddressWithTickV3(address, assetName)
	if err != nil {
		return nil, 0, err
	}
	for _, txOut := range outputMap {
		if IsExistingInMemPool(txOut.OutPoint) {
			continue
		}
		result = append(result, txOut)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Value > result[j].Value
	})

	return result, len(result), nil
}
