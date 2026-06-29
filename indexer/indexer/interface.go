package indexer

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/wire"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/common"
	base_indexer "github.com/sat20-labs/satoshinet/indexer/indexer/base"
)

// interface for RPC

func (b *IndexerMgr) IsMainnet() bool {
	return b.chaincfgParam.Name == "mainnet"
}

func (b *IndexerMgr) GetBaseDBVer() string {
	return b.compiling.GetBaseDBVer()
}

func (b *IndexerMgr) GetChainParam() *chaincfg.Params {
	return b.chaincfgParam
}

func (b *IndexerMgr) HasAssetInUtxo(utxo string) bool {
	info, err := b.rpcService.GetUtxoInfo(utxo)
	if err != nil {
		return false
	}
	return len(info.Assets) != 0
}

// return: utxoId->asset amount
func (b *IndexerMgr) GetAssetUTXOsInAddressWithTick(address string, ticker *wire.AssetName) (map[uint64]*common.TxOutput, error) {
	utxos, err := b.rpcService.GetUTXOs(address)
	if err != nil {
		return nil, err
	}

	result := make(map[uint64]*common.TxOutput)
	for utxoId := range utxos {
		utxo, err := b.rpcService.GetUtxoByID(utxoId)
		if err != nil {
			continue
		}
		info := b.GetTxOutputWithUtxo(utxo)
		if info == nil {
			continue
		}

		// 因为聪网的灵活性，只要一个utxo中的聪比绑定资产的聪更多，就可以认为该utxo含有白聪
		if ticker == nil {
			result[utxoId] = info
		} else if indexer.IsPlainAsset(ticker) {
			if info.HasPlainSat() {
				result[utxoId] = info
			}
		} else {
			amt := info.GetAsset(ticker)
			if amt.Sign() == 0 {
				continue
			}
			result[utxoId] = info
		}
	}

	return result, nil
}

// return: ticker -> amount
func (b *IndexerMgr) GetAssetSummaryInAddress(address string) map[wire.AssetName]*indexer.Decimal {
	utxos, err := b.rpcService.GetUTXOs(address)
	if err != nil {
		return nil
	}

	value := int64(0)
	result := make(map[wire.AssetName]*indexer.Decimal)
	for utxoId := range utxos {
		utxo, err := b.rpcService.GetUtxoByID(utxoId)
		if err != nil {
			continue
		}
		info, err := b.rpcService.GetUtxoInfo(utxo)
		if err != nil {
			continue
		}

		// 白聪资产去除绑定资产的聪
		assetAmt := int64(0)
		if len(info.Assets) != 0 {
			for _, asset := range info.Assets {
				amt, ok := result[asset.Name]
				if ok {
					amt = amt.Add(&asset.Amount)
				} else {
					amt = &asset.Amount
				}
				result[asset.Name] = amt
			}
			assetAmt = info.Assets.GetBindingSatAmout()
		}

		value += (info.Value - assetAmt)
	}
	result[common.ASSET_PLAIN_SAT] = indexer.NewDefaultDecimal(value)

	return result
}

// return: ticker -> []utxoId
func (b *IndexerMgr) GetAssetUTXOsInAddress(address string) map[wire.AssetName][]*common.TxOutput {
	utxos, err := b.rpcService.GetUTXOs(address)
	if err != nil {
		return nil
	}

	result := make(map[wire.AssetName][]*common.TxOutput)
	for utxoId := range utxos {
		utxo, err := b.rpcService.GetUtxoByID(utxoId)
		if err != nil {
			continue
		}
		info := b.GetTxOutputWithUtxo(utxo)
		if info == nil {
			continue
		}
		for _, asset := range info.OutValue.Assets {
			result[asset.Name] = append(result[asset.Name], info)
		}
		if len(info.OutValue.Assets) == 0 {
			result[common.ASSET_PLAIN_SAT] = append(result[common.ASSET_PLAIN_SAT], info)
		}
	}

	return result
}

func (b *IndexerMgr) GetTxOutputWithUtxo(utxo string) *common.TxOutput {

	info, err := b.rpcService.GetUtxoInfo(utxo)
	if err != nil {
		return nil
	}

	return &common.TxOutput{
		UtxoId:      info.UtxoId,
		OutPointStr: utxo,
		OutValue: wire.TxOut{
			Value:    info.Value,
			Assets:   info.Assets,
			PkScript: info.PkScript,
		},
	}
}

func (b *IndexerMgr) GetAscendData(fundingUtxo string) *common.AscendData {
	return b.rpcService.GetAscendData(fundingUtxo)
}

func (b *IndexerMgr) GetDescendData(nullDataUtxo string) *common.DescendData {
	return b.rpcService.GetDescendData(nullDataUtxo)
}

func (b *IndexerMgr) GetChannelLedger(channel string) []*common.ChannelLedgerEntry {
	return b.rpcService.GetChannelLedger(channel)
}

func (b *IndexerMgr) GetChannelStateEvents(channel string) []*common.ChannelStateEvent {
	return b.rpcService.GetChannelStateEvents(channel)
}

func (b *IndexerMgr) RecordChannelStateEvent(event *common.ChannelStateEvent) error {
	if b.IsMainnet() {
		return fmt.Errorf("channel state event report is disabled on mainnet")
	}
	if err := b.compiling.RecordChannelStateEvent(event); err != nil {
		return err
	}
	newService := base_indexer.NewRpcIndexer(b.compiling)
	newService.UpdateServiceInstance()
	b.mutex.Lock()
	b.rpcService = newService
	b.mutex.Unlock()
	return nil
}

func (b *IndexerMgr) GetReferrer(address string) (*common.ReferrerInfo, error) {
	return b.rpcService.GetReferrer(address)
}

func (b *IndexerMgr) GetReferree(name string) map[string]int {
	result, _ := b.rpcService.GetReferree(name)
	return result
}

func (b *IndexerMgr) GetTickerInfo(ticker *wire.AssetName) *common.TickerInfo {
	return b.rpcService.GetTickerInfo(ticker)
}

// return: ticker's name -> ticker info
func (b *IndexerMgr) GetTickerMap(protocol string) map[string]*common.TickerInfo {

	result := make(map[string]*common.TickerInfo)
	tickerMap := b.rpcService.GetTickerMap()
	for k, v := range tickerMap {

		if protocol == "" || protocol == "*" {
			result[k] = v
		} else {
			name := indexer.NewAssetNameFromString(k)
			if name.Protocol == protocol {
				result[k] = v
			}
		}
	}

	return result
}

// return: addressId -> asset amount
func (b *IndexerMgr) GetHoldersWithTick(tickerName *common.TickerName) map[string]*indexer.Decimal {
	return b.rpcService.GetHoldersWithTick(tickerName)
}

func (b *IndexerMgr) GetBindingSat(ticker *wire.AssetName) int {
	info := b.rpcService.GetTickerInfo(ticker)
	if info == nil {
		return 0
	}
	return info.N
}

func (b *IndexerMgr) GetAllCoreNode() map[string]*common.CoreNodeInfo {
	return b.rpcService.GetAllCoreNode()
}

// 为了在重跑聪网索引器数据时，不用每个区块clone一次compiling的数据
func (b *IndexerMgr) IsCoreNode(pubkey string) bool {
	//return b.rpcService.IsCoreNode(pubkey)
	return b.compiling.IsCoreNode(pubkey)
}

func (b *IndexerMgr) GetCoreNodeInfo(pubkey string) *common.CoreNodeInfo {
	return b.rpcService.GetCoreNodeInfo(pubkey)
}

// 为了在重跑聪网索引器数据时，不用每个区块clone一次compiling的数据
func (b *IndexerMgr) IsMinerNode(pubkey string) bool {
	//return b.rpcService.IsMinerNode(pubkey)
	return b.compiling.IsMinerNode(pubkey)
}

// 为了在重跑聪网索引器数据时，不用每个区块clone一次compiling的数据
func (b *IndexerMgr) GetSeqMgr() *common.MiningSequenceMgr {
	return b.compiling.GetSequenceMgr()
}

func (b *IndexerMgr) GetMinerInfo(pubkey string) *common.MinerInfo {
	return b.rpcService.GetMinerInfo(pubkey)
}

func (b *IndexerMgr) CheckBlockMiningInfo(block *btcutil.Block) error {
	return b.compiling.CheckBlockMiningInfo(block)
}
