package indexer

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	contractengine "github.com/sat20-labs/satoshinet/contract/engine"
	"github.com/sat20-labs/satoshinet/indexer/common"
	localwire "github.com/sat20-labs/satoshinet/indexer/rpcserver/wire"
	shareIndexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc"
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

func (s *Model) GetTickerList(protocol string, start, limit int) ([]*common.TickerInfo, int) {
	tickmap := s.indexer.GetTickerMap(protocol)
	mid := make([]*common.TickerInfo, 0)
	for _, v := range tickmap {
		mid = append(mid, v)
	}
	sort.Slice(mid, func(i, j int) bool {
		return mid[i].AssetName.String() < mid[j].AssetName.String()
	})

	total := len(mid)
	if start >= total {
		return nil, 0
	}
	limit += start
	if limit >= total {
		limit = total
	}

	return mid[start:limit], total
}

func (s *Model) GetTickerInfo(tickerName string) (*common.TickerInfo, error) {
	ticker := s.indexer.GetTickerInfo(indexer.NewAssetNameFromString(tickerName))
	if ticker == nil {
		return nil, fmt.Errorf("can't find ticker %s", tickerName)
	}

	return ticker, nil
}

func (s *Model) GetHolderListV3(tickName string, start, limit uint64) ([]*indexerwire.HolderV3, uint64, error) {

	assetName := indexer.NewAssetNameFromString(tickName)
	holders := s.indexer.GetHoldersWithTick(assetName)

	result := make([]*indexerwire.HolderV3, 0, len(holders))
	for address, amt := range holders {
		ordxMintInfo := &indexerwire.HolderV3{
			Wallet:       address,
			TotalBalance: amt.String(),
		}
		result = append(result, ordxMintInfo)
	}
	sort.Slice(result, func(i, j int) bool {
		a, _ := indexer.NewDecimalFromString(result[i].TotalBalance, 20)
		b, _ := indexer.NewDecimalFromString(result[j].TotalBalance, 20)
		return a.Cmp(b) > 0
	})

	total := uint64(len(result))
	end := total
	if start >= end {
		return nil, 0, nil
	}
	if start+limit < end {
		end = start + limit
	}
	result = result[start:end]
	return result, total, nil
}

func (s *Model) getPlainUtxos(address string, value int64, start, limit int) ([]*indexerwire.PlainUtxo, int, error) {
	outputMap, err := s.indexer.GetAssetUTXOsInAddressWithTickV3(address, &indexer.ASSET_PLAIN_SAT)
	if err != nil {
		return nil, 0, err
	}

	utxos := make([]uint64, 0)
	for key := range outputMap {
		utxos = append(utxos, key)
	}

	sort.Slice(utxos, func(i, j int) bool {
		return utxos[i] < utxos[j]
	})

	// // 分页显示
	totalRecords := len(utxos)

	avaibableUtxoList := make([]*indexerwire.PlainUtxo, 0)
	for utxoId, txOut := range outputMap {
		if IsSpent(txOut.OutPoint) {
			continue
		}
		height, index, _ := indexer.FromUtxoId(utxoId)
		if txOut.Value >= value {
			txid, vout, _ := indexer.ParseUtxo(txOut.OutPoint)
			avaibableUtxoList = append(avaibableUtxoList, &indexerwire.PlainUtxo{
				Height: height,
				Index:  index,
				Txid:   txid,
				Vout:   vout,
				Value:  txOut.Value,
			})
		}
	}

	sort.Slice(avaibableUtxoList, func(i, j int) bool {
		return avaibableUtxoList[i].Value > avaibableUtxoList[j].Value
	})

	return avaibableUtxoList, totalRecords, nil
}

func (s *Model) getAllUtxos(address string, start, limit int) ([]*indexerwire.PlainUtxo, []*indexerwire.PlainUtxo, int, error) {
	outputMap, err := s.indexer.GetAssetUTXOsInAddressWithTickV3(address, nil)
	if err != nil {
		return nil, nil, 0, err
	}

	utxos := make([]uint64, 0)
	for key := range outputMap {
		utxos = append(utxos, key)
	}

	// sort.Slice(utxos, func(i, j int) bool {
	// 	return utxos[i] < utxos[j]
	// })

	// // 分页显示
	totalRecords := len(utxos)

	plainUtxos := make([]*indexerwire.PlainUtxo, 0)
	otherUtxos := make([]*indexerwire.PlainUtxo, 0)
	for utxoId, txOut := range outputMap {
		if IsSpent(txOut.OutPoint) {
			continue
		}

		txid, vout, _ := indexer.ParseUtxo(txOut.OutPoint)

		height, index, _ := indexer.FromUtxoId(utxoId)
		if len(txOut.Assets) == 0 {
			plainUtxos = append(plainUtxos, &indexerwire.PlainUtxo{
				Height: height,
				Index:  index,
				Txid:   txid,
				Vout:   vout,
				Value:  txOut.Value,
			})
		} else {
			otherUtxos = append(otherUtxos, &indexerwire.PlainUtxo{
				Height: height,
				Index:  index,
				Txid:   txid,
				Vout:   vout,
				Value:  txOut.Value,
			})
		}
	}

	sort.Slice(plainUtxos, func(i, j int) bool {
		return plainUtxos[i].Value > plainUtxos[j].Value
	})

	sort.Slice(otherUtxos, func(i, j int) bool {
		return otherUtxos[i].Value > otherUtxos[j].Value
	})

	return plainUtxos, otherUtxos, totalRecords, nil
}

func (s *Model) GetSyncHeight() int {
	return s.indexer.GetSyncHeight()
}

func (s *Model) PutDKVSRecord(record *swire.DKVSRecord) (bool, error) {
	return s.indexer.PutDKVSRecord(record)
}

func (s *Model) GetDKVSRecord(key string) (*swire.DKVSRecord, error) {
	return s.indexer.GetDKVSRecord(key)
}

func (s *Model) ListDKVSRecords(prefix string, start, limit int) ([]*swire.DKVSRecord, int, error) {
	return s.indexer.ListDKVSRecords(prefix, start, limit)
}

func (s *Model) GetDKVSCheckpoint() (interface{}, error) {
	return s.indexer.GetDKVSCheckpoint()
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

		if IsSpent(utxo) {
			continue
		}

		result = append(result, utxo)
	}

	return result, nil
}

func (s *Model) GetAscend(utxo string) (*common.AscendData, error) {
	data := s.indexer.GetAscendData(utxo)
	if data == nil {
		return nil, fmt.Errorf("GetAscendData %s not found", utxo)
	}

	return data, nil
}

func (s *Model) GetDescend(utxo string) (*common.DescendData, error) {
	data := s.indexer.GetDescendData(utxo)
	if data == nil {
		return nil, fmt.Errorf("GetDescendData %s not found", utxo)
	}

	return data, nil
}

func (s *Model) GetChannelLedger(channel string) ([]*common.ChannelLedgerEntry, error) {
	data := s.indexer.GetChannelLedger(channel)
	if data == nil {
		data = []*common.ChannelLedgerEntry{}
	}
	return data, nil
}

func (s *Model) GetChannelStateEvents(channel string) ([]*common.ChannelStateEvent, error) {
	data := s.indexer.GetChannelStateEvents(channel)
	if data == nil {
		data = []*common.ChannelStateEvent{}
	}
	return data, nil
}

func (s *Model) RecordChannelStateEvent(event *common.ChannelStateEvent) error {
	return s.indexer.RecordChannelStateEvent(event)
}

func (s *Model) GetReferrer(address string) (*common.ReferrerInfo, error) {
	return s.indexer.GetReferrer(address)
}

func (s *Model) GetReferree(name string, start, limit int) ([]*localwire.ReferreeInfo, int) {
	result := make([]*localwire.ReferreeInfo, 0)
	referrees := s.indexer.GetReferree(name)
	for k, v := range referrees {
		result = append(result, &localwire.ReferreeInfo{
			Name:      k,
			BindBlock: v,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	total := len(result)
	if start >= total {
		return nil, total
	}
	limit += start
	if limit >= total {
		limit = total
	}

	return result[start:limit], total
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

func (s *Model) GetCoreNodeInfo(pubkey string) *common.CoreNodeInfo {
	return s.indexer.GetCoreNodeInfo(pubkey)
}

func (s *Model) CheckMiner(pubkey string) bool {
	return s.indexer.IsMinerNode(pubkey)
}

func (s *Model) GetMinerInfo(pubkey string) *common.MinerInfo {
	return s.indexer.GetMinerInfo(pubkey)
}

func (s *Model) GetAssetSummaryV3(address string, start int, limit int) ([]*indexer.DisplayAsset, error) {
	tickerMap := s.indexer.GetAssetSummaryInAddressV3(address)

	result := make([]*indexer.DisplayAsset, 0)
	for tickName, balance := range tickerMap {
		resp := &indexer.DisplayAsset{}
		resp.AssetName = tickName
		resp.Precision = balance.Precision
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

	if IsSpent(utxo) {
		return nil, fmt.Errorf("utxo %s is spent", utxo)
	}
	ret := s.indexer.GetTxOutputWithUtxoV3(utxo)
	if ret == nil {
		// 直接从TX数据中读
		parts := strings.Split(utxo, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid utxo formate %s", utxo)
		}
		tx, err := satsnet_rpc.GetTx(parts[0])
		if err != nil {
			return nil, fmt.Errorf("GetTx failed, %v", err)
		}
		vout, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, fmt.Errorf("atoi failed, %v", err)
		}
		txOut := tx.MsgTx().TxOut[vout]

		var assetsInUtxo indexer.AssetsInUtxo
		assetsInUtxo.UtxoId = 0
		assetsInUtxo.OutPoint = utxo
		assetsInUtxo.Value = txOut.Value
		assetsInUtxo.PkScript = txOut.PkScript
		for _, asset := range txOut.Assets {
			asset := indexer.DisplayAsset{
				AssetName:  asset.Name,
				Amount:     asset.Amount.String(),
				Precision:  asset.Amount.Precision,
				BindingSat: int(asset.BindingSat),
			}
			assetsInUtxo.Assets = append(assetsInUtxo.Assets, &asset)
		}
		ret = &assetsInUtxo
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

func (s *Model) GetAddressUtxosV3(address string, start, limit int) ([]*indexer.AssetsInUtxo, int, error) {
	result := make([]*indexer.AssetsInUtxo, 0)
	outputMap, err := s.indexer.GetAssetUTXOsInAddressWithTickV3(address, nil)
	if err != nil {
		return nil, 0, err
	}
	for _, txOut := range outputMap {
		if IsSpent(txOut.OutPoint) {
			continue
		}
		result = append(result, txOut)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Value == result[j].Value {
			return result[i].OutPoint < result[j].OutPoint
		}
		return result[i].Value > result[j].Value
	})

	return result, len(result), nil
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
		if IsSpent(txOut.OutPoint) {
			continue
		}
		result = append(result, txOut)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Value == result[j].Value {
			return result[i].OutPoint < result[j].OutPoint
		}
		return result[i].Value > result[j].Value
	})

	return result, len(result), nil
}

func (s *Model) GetSupportedContracts() []string {
	return s.contractQueries().SupportedContracts()
}

func (s *Model) GetDeployedContracts(start, limit int) ([]string, int) {
	return s.contractQueries().DeployedContracts(start, limit)
}

func (s *Model) GetContracts(start, limit int) ([]contractengine.ContractSummary, int) {
	return s.contractQueries().Contracts(start, limit)
}

func (s *Model) GetContract(contractAddress string) (contractengine.ContractSummary, error) {
	return s.contractQueries().Contract(contractAddress)
}

func (s *Model) GetEVMSourceMetadata(contractAddress string) (contractengine.EVMSourceMetadata, bool) {
	return s.indexer.GetEVMSourceMetadata(contractAddress)
}

func (s *Model) PutEVMSourceMetadata(metadata contractengine.EVMSourceMetadata) error {
	return s.indexer.PutEVMSourceMetadata(metadata)
}

func (s *Model) GetContractHistory(contractAddress string, start, limit int) ([]contractengine.ContractHistoryRecord, int, error) {
	return s.contractQueries().History(contractAddress, start, limit)
}

func (s *Model) GetContractAnalytics(contractAddress string) (any, error) {
	return s.contractQueries().Analytics(contractAddress)
}

func (s *Model) GetContractInvokeItemByInUtxo(contractAddress, inUtxo string) (any, error) {
	return s.contractQueries().InvokeItemByInUtxo(contractAddress, inUtxo)
}

func (s *Model) GetContractAllAddresses(contractAddress string, start, limit int) (any, int, error) {
	return s.contractQueries().AllAddresses(contractAddress, start, limit)
}

func (s *Model) GetContractUserStatus(contractAddress, address string) (any, error) {
	return s.contractQueries().UserStatus(contractAddress, address)
}

func (s *Model) GetContractHistoryByAddress(contractAddress, address string, start, limit int) (any, int, error) {
	return s.contractQueries().HistoryByAddress(contractAddress, address, start, limit)
}

func (s *Model) contractQueries() contractengine.QueryService {
	return contractengine.NewQueryServiceForParams(s.indexer, s.indexer.GetChainParam())
}
